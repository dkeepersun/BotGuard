# BotGuard Go SDK

业务服务器用它调用 BotGuard 的 `/v1/verify` 核验浏览器提交的 token，作用相当于易盾的 NECaptchaVerifier。要求 Go ≥ 1.22，核心包只用标准库。

| 模块 | 用途 | 安装 |
|---|---|---|
| `github.com/dkeepersun/BotGuard/packages/go` | 核心：`Client.Verify`、net/http 中间件 | `go get github.com/dkeepersun/BotGuard/packages/go` |
| `github.com/dkeepersun/BotGuard/packages/go/gin` | gin 中间件 | `go get github.com/dkeepersun/BotGuard/packages/go/gin` |
| `github.com/dkeepersun/BotGuard/packages/go/gozero` | go-zero 中间件 | `go get github.com/dkeepersun/BotGuard/packages/go/gozero` |

适配包是独立的模块，不用 gin / go-zero 的项目不会引入它们的依赖。包名分别是 `botguard`、`botguardgin`、`botguardgozero`（导入路径最后一段是 `go`，建议导入时写上别名）。

## 直接调用

```go
import botguard "github.com/dkeepersun/BotGuard/packages/go"

// secret_id / secret_key 来自 botguard-ctl secret create，只放在服务端配置里
client, err := botguard.New("https://botguard.example.com", secretID, secretKey,
	botguard.WithAppKey("app_xxx"), botguard.WithTimeout(3*time.Second))

// 先做普通字段校验，再核验；action 由当前接口写死，不能取自浏览器
res, err := client.VerifyToken(ctx, r.PostFormValue("botguard_token"), "login")
switch {
case err != nil:
	// *botguard.CallerError：密钥、时钟、应用配置、endpoint 写错等，告警，不要让用户重试
	// *botguard.UnknownError：429/5xx/网络错误/响应不符合契约，服务端可能已消费 token，不要放行
	return err
case !res.Passed:
	// 拒绝：token 无效、过期、已用过、不属于本应用或本 action。提示用户重新验证（res.Reason 是原因码）
	return errCaptchaFailed
}
// 通过：继续执行登录 / 注册等业务
```

需要在结果未知时重试，请自己固定 request_id（例如由业务幂等键派生），用 `Verify`：

```go
res, err := client.Verify(ctx, botguard.VerifyRequest{
	AppKey: "app_xxx", Token: token, Action: "login", RequestID: orderID,
})
```

区分两类错误用 `errors.As`，或者 `botguard.IsCallerError(err)` / `botguard.IsUnknown(err)`：

```go
var unknown *botguard.UnknownError
if errors.As(err, &unknown) && unknown.RetryAfter > 0 {
	w.Header().Set("Retry-After", strconv.Itoa(int(unknown.RetryAfter.Seconds())))
}
```

## 中间件

### net/http（chi、echo 等同样适用）

```go
guard := botguard.NewGuard(client, "login")
mux.Handle("POST /login", guard.Middleware(loginHandler))

func loginHandler(w http.ResponseWriter, r *http.Request) {
	res, _ := botguard.ResultFrom(r.Context()) // 能执行到这里说明已通过
	if res.RiskLevel == "high" { /* 可以追加短信验证 */ }
}
```

### gin

```go
import botguardgin "github.com/dkeepersun/BotGuard/packages/go/gin"

r.POST("/login", botguardgin.Protect(botguard.NewGuard(client, "login")), func(c *gin.Context) {
	res, _ := botguardgin.Result(c)
})
```

`botguardgin.OnReject`、`botguardgin.OnError` 可以换成自己的响应格式，例如 `c.JSON(400, ...)`。回调返回后 `Protect` 一定会中止后续处理器，回调里不需要、也不能靠自己调用 `c.Abort`；回调什么都没写时，返回默认的 403 / 500 / 503。

### go-zero

```go
import botguardgozero "github.com/dkeepersun/BotGuard/packages/go/gozero"

login := botguardgozero.Middleware(botguard.NewGuard(client, "login"))
server.AddRoutes(rest.WithMiddlewares([]rest.Middleware{login}, rest.Route{
	Method: http.MethodPost, Path: "/login", Handler: loginHandler,
}))
```

项目里用 `httpx.SetErrorHandlerCtx` 统一了错误格式时，加上 `botguardgozero.UseErrorHandler()`：拒绝以 `*botguardgozero.RejectedError` 交给 `httpx.ErrorCtx`，配置错误和未知结果以 `*botguard.CallerError` / `*botguard.UnknownError` 交给它。不用适配包也可以：`guard.HandlerFunc` 的类型就是 `rest.Middleware`。

### 中间件的默认行为

| 情况 | 响应 |
|---|---|
| 通过 | 放行，结果放进 request context（`botguard.ResultFrom`） |
| 拒绝 | 403 `{"code":"BOTGUARD_REJECTED","reason":"TOKEN_EXPIRED","message":"人机验证未通过，请重新验证"}` |
| 通过，但需要额外验证（见下文） | 403 `{"code":"BOTGUARD_STEP_UP","reason":"STEP_UP_FALLBACK","message":"需要进一步验证"}` |
| `CallerError` | 记录 error 日志，500 `{"code":"BOTGUARD_MISCONFIGURED",...}` |
| `UnknownError` | 记录 error 日志，503 `{"code":"BOTGUARD_UNAVAILABLE",...}`，有 Retry-After 时透传 |

- token 默认先读请求体里的表单字段 `botguard_token`，没有再读请求头 `X-BotGuard-Token`（用 fetch 提交时方便）。**不读 URL 查询参数**，以免 token 进入访问日志。JSON 请求体用 `botguard.WithTokenFrom` 自己取。
- 字段名不同用 `botguard.WithTokenField("captcha")`；request_id 默认每个请求新生成，要和业务幂等键绑定用 `botguard.WithRequestID`。
- 一个请求的 token 只核验一次。中间件会消耗 token，所以放在普通字段校验、登录态检查之后，业务副作用之前。
- 日志默认写到 `slog.Default()`，可以用 `botguard.WithLogger` 换掉；`botguard.OnReject` / `botguard.OnError` 可以替换 net/http 中间件的响应。

### 额外验证（step-up）

有三类 token 网关判为通过，但拿到的成本对机器人很低。中间件（net/http、gin、go-zero）**默认把它们当作未通过**，原因码分别是：

| 原因码 | 情况 | 为什么 |
|---|---|---|
| `STEP_UP_FALLBACK` | 用户选了无障碍备用路径（`challenge_kind` 为 `fallback`） | 只有工作量证明，原生代码几十毫秒就能算完 |
| `STEP_UP_DEGRADED` | 网关图片题容量不足，降级为工作量证明（`risk_reasons` 含 `CAPACITY_DEGRADED`） | 同上；攻击者可以故意刷满配额触发降级 |
| `STEP_UP_HIGH_RISK` | 风险等级为 `high` | 解出了最难的题，但信号显示很可能是自动化 |

响应码是 `BOTGUARD_STEP_UP`，页面可以据此改为让用户做短信验证等额外检查，而不是再弹一次验证码。确实要放行时逐类打开：

```go
guard := botguard.NewGuard(client, "login",
	botguard.AllowFallback(),  // 例如业务自己会对这类请求追加短信验证
	botguard.AllowDegraded(),  // 宁可放宽也不想在配额被刷满时影响用户
	botguard.AllowHighRisk(),
)
```

`OnReject` 拿到的 `*Result` 保留了网关返回的题型、风险等级和原因码。直接调用 `client.Verify` 时不做这项判断，可以用 `res.StepUp()` 自己判断。
- `NewGuard` 在 app_key 缺失或 action 格式不对时直接 panic：这是启动时就该发现的配置错误。

## 本地校验

发请求前 SDK 先在本地检查格式（与网关规则一致），不合格的不发网络请求：

| 字段 | 规则 | 不合格时 |
|---|---|---|
| token | 只含 `[A-Za-z0-9._-]`，1–256 字节 | 返回拒绝，`Reason` 为 `TOKEN_INVALID`（与网关一致） |
| action | `[a-z0-9_]`，1–32 字符 | `*CallerError`，`Code` 为 `INVALID_REQUEST`，`Status` 为 0 |
| app_key | `[A-Za-z0-9_-]`，8–64 字符 | 同上 |
| request_id | `[A-Za-z0-9_-]`，1–64 字符 | 同上 |

## 错误分类与重试

| 情况 | 结果 | 自动重试（`WithRetries`） |
|---|---|---|
| 200 且符合契约 | `*Result`（`Passed` 为 true 或 false） | — |
| 除 429 外的 3xx、4xx（含 404/405/413/415、重定向） | `*CallerError`，保留 `Status` 和响应体里的 `Code` | 否 |
| 429 | `*UnknownError`，`RetryAfter` 取自 Retry-After | 否 |
| 5xx、网络错误、超时 | `*UnknownError` | 是 |
| 响应体超过 64 KiB、200 但格式不符、通过结果的 app_key/action 与请求不符、其他意外状态码 | `*UnknownError`，`Protocol` 为 true | 否 |

- 重试每次都重新签名（新时间戳、新 nonce），request_id 和请求体保持不变，所以拿到的是幂等结果，不会重复消费 token。默认不重试。
- 不跟随重定向：3xx 说明 endpoint 配错了。
- 超时默认 3 秒，`WithTimeout` 为 0 或负数时取默认值，小于 50 ms 按 50 ms 算；`ctx` 上的截止时间同样生效。`WithHTTPClient` 可以换传输层（代理、TLS），超时和不跟随重定向依然保留。
- `Retry-After` 支持秒数和 HTTP-date 两种写法。

## 风险信息

通过时，`Result` 还带有这个 token 是怎么拿到的：

```go
res.ChallengeKind // invisible / slider / click_text / click_shape / fallback ...（旧版 gateway 为空）
res.RiskLevel     // low / medium / high
res.RiskReasons   // 例如 [NO_SIGNALS IP_RECENT_FAILURES]
res.Origin        // 签发 token 的页面来源，如 https://shop.example.com；应用允许来源用了通配或 * 时，在这里核对自己的域名（旧版 gateway 为空）
```

这些字段不改变“通过”这一结论，只供业务方二次决策，例如高风险时追加短信验证。

## 密钥保护

`Client` 实现了 `String()` / `GoString()`，用 `%v`、`%+v`、`%#v` 打印时 secret_key 显示为 `[REDACTED]`。

## 开发与测试

```sh
cd packages/go
go test ./... ./gin/... ./gozero/...
```

`go.work` 把三个模块放在同一个工作区：适配包 `require` 的是核心包已发布的版本，工作区里的 `replace` 让它们在发布前用本目录的代码。用户 `go get` 时不会读到 `go.work`。签名用例 `testdata/hmac-signature-v1.json` 是 `api/fixtures` 的副本（模块压缩包不含模块目录以外的文件），测试会检查两者一致。

发布见 `docs/release.md`「Go SDK」。
