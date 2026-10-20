# BotGuard 本地演示

一条命令在本机拉起完整环境：MySQL、Redis、gateway、PHP 演示站。在浏览器里能直接看到验证组件怎么工作、PHP SDK 怎么签名调用 `/v1/verify`，以及各种攻击和误用被拒的原因。

## 启动

需要 Docker（含 compose）和 openssl。

```sh
examples/demo/start.sh
```

然后打开 <http://localhost:8080>。

- 第一次运行会生成 `examples/demo/.env`，里面是 MySQL 密码、challenge 签名密钥和 secret 加密主密钥（KEK）。这些都是本地演示用的随机值，已被 git 忽略。
- 第一次启动要拉取 `php:8.3-cli-alpine` 镜像，并在容器里编译 gateway 和 botguard-ctl，需要几分钟。如果访问 proxy.golang.org 慢，先 `export GOPROXY=https://goproxy.cn,direct` 再运行。
- 服务只监听 127.0.0.1：页面在 8080，gateway 在 8090。

停止：

```sh
docker compose -f examples/demo/docker-compose.yml down      # 保留数据
docker compose -f examples/demo/docker-compose.yml down -v   # 连数据一起清空，下次重新初始化
```

## 启动时做了什么

`seed` 服务用 `botguard-ctl` 完成初始化（重复启动不会重复创建）：

1. 执行数据库迁移；
2. 创建两个应用「演示商城」和「演示论坛」，用来演示多应用隔离；
3. 每个应用允许来源 `http://localhost:8080` 和 `http://127.0.0.1:8080`，配置两个 action（PoW 均为 8 道子题、每题约 3000 次哈希）：`login` 为 adaptive（按风险出题），`register` 为 slider（固定滑块）。已有演示数据在每次启动时也会同步这两个模式；
4. 每个应用生成一组密钥对，写入容器内的 `/shared/demo.env`，只供 PHP 服务端读取，不会打印到日志。

## 页面上能看到什么

**① 模拟登录 / 注册**：两个标签页对应两种模式。

- **登录（adaptive）**：正常浏览器通常无感通过；信号可疑或该 IP 最近有失败时，会出现滑块。
- **注册（slider）**：每次都出拼图，拖动滑块对准缺口即可；也可以用 Tab 把焦点移到滑块，用方向键移动、回车确认。拼图下方的「无法完成拼图？」是无障碍备用路径。

提交后，PHP 用 SDK 签名调用 verify，页面会显示这次的通过方式和风险等级。

右侧“调用过程”按时间倒序显示每一步：

- 蓝色：浏览器端事件，包括取 challenge、求解（哈希次数和用时）、换 token；
- 绿 / 橙 / 红 / 灰：服务端核验结果，分别对应通过 / 拒绝 / 调用方错误 / 结果未知四类；
- 每条都可以展开，看到带签名头的请求和原始响应。

**② 模拟攻击与误用**：

| 按钮 | 预期结果 | 说明 |
|---|---|---|
| 不带 token 提交 | 拒绝 · TOKEN_INVALID | 跳过前端组件直接提交表单 |
| 伪造 token | 拒绝 · TOKEN_INVALID | token 是服务端随机数，拼不出来 |
| 重放已用过的 token | 拒绝 · TOKEN_CONSUMED | token 只能用一次（需先登录一次） |
| ★ 登录 token 用于注册 | 拒绝 · ACTION_MISMATCH | token 绑定 action；不消耗 token |
| ★ 商城 token 拿到论坛用 | 拒绝 · APP_MISMATCH | 多应用隔离；不消耗 token |
| 用论坛密钥对冒充商城 | 调用方错误 · 403 APP_FORBIDDEN | 密钥对只属于一个应用 |
| 错误的 secret_key | 调用方错误 · 401 UNAUTHORIZED | 签名对不上 |
| 重发已签名的请求 | 调用方错误 · 401 NONCE_REUSED | 截获的签名请求不能重放 |
| 过期的签名时间戳 | 调用方错误 · 401 TIMESTAMP_EXPIRED | 签名超出 ±300 秒窗口 |
| 刷签发接口 40 次 | 部分 429 | 演示配置为每 IP 每分钟 30 次 |
| 脚本自动求解 PoW | **通过** | 诚实展示 PoW 的局限：脚本也能算，只是有成本 |

标 ★ 的场景要先点验证组件拿到新 token（不要点登录）。

**③ 风险信号与滑块**：

| 按钮 | 预期结果 | 说明 |
|---|---|---|
| 无头浏览器特征 | 无感 → 滑块，PoW ×4 | 上报 webdriver、HeadlessChrome、软件渲染 WebGL，进入高风险档 |
| 不带信号直接调接口 | 无感 → 滑块 | 没有信号本身就是信号（NO_SIGNALS） |
| 同一 IP 连续失败 | 无感 → 滑块 | IP 近期失败次数是攻击者无法伪造的服务端信号 |
| 脚本找缺口 + 直线拖动 | 拒绝 · ANSWER_INVALID | 页面脚本用 canvas 几毫秒就找到缺口，但匀速直线的轨迹被拦下 |
| 脚本找缺口 + 仿人轨迹 | **通过** | 如实展示滑块的上限：仿人轨迹能骗过规则；业务方仍能看到风险原因 |

“页面脚本”两个场景在你自己的浏览器里运行，失败会计入你这个 IP 的失败次数，之后登录可能会出现滑块（10 分钟后恢复）。这两种拒绝不会消耗 token，之后仍可以用它正常登录。

## 对照代码看接入方式

| 位置 | 内容 |
|---|---|
| `php/public/index.php` | 前端接入：引入 `/botguard/botguard.js`，放一个 `<botguard-widget>`，只需要公开的 `app_key` |
| `php/public/login.php` | 服务端接入：字段校验 → SDK 核验（action 写死）→ 只有“通过”才执行业务 |
| `php/src/bootstrap.php` | 把 SDK 的四类结果映射成业务处理方式 |
| `php/public/attack.php` | 各个攻击场景的实现 |
| `seed.sh` | 用 `botguard-ctl` 开通应用的完整命令 |

`php/src/RecordingTransport.php` 只为在页面上展示请求和响应，真实接入直接用 SDK 默认的 cURL 传输即可。

## 和易盾的对应关系

| 易盾 | BotGuard |
|---|---|
| captchaId（前端公开） | `app_key` |
| secretId / secretKey | `secret_id` / `secret_key`（`botguard-ctl secret create` 生成） |
| `initNECaptcha({captchaId, ...})` | `<botguard-widget endpoint app-key action>` |
| 表单字段 `NECaptchaValidate` | 表单字段 `botguard_token` |
| `NECaptchaVerifier->verify($validate, $user)` | `Client->verify(new VerifyRequest($appKey, $token, $action, $requestId))` |
| MD5(排序参数 + secretKey) 签名 | HMAC-SHA256 签整个请求体，带时间戳和 nonce 防重放 |
| 返回 `result: true/false` | 通过 / 拒绝 / 调用方错误 / 结果未知四类，配置错误不会被当成“用户没通过” |
