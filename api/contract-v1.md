# v1 API 契约草案

服务端以本文为 P1 输入，再补齐 OpenAPI 3.1 和各语言共享的成功/失败 fixtures。字段在发布 v1 前可调整，不宣称与 ALTCHA 线协议兼容。

## 统一约定

HTTPS；JSON；snake_case；严格 body 大小和字段长度；浏览器端没有服务端密钥。错误格式：

```json
{"code":"CHALLENGE_EXPIRED","message":"验证已过期，请重试","request_id":"..."}
```

API 成功用 `{"code":"OK","data":{...},"request_id":"..."}`；后台继续采用 PearAdmin 的数值 code 格式，两者不混用。错误不回显 token、签名、secret、内部 SQL 或原始 IP。

- 浏览器接口（challenges/solutions）：400 `INVALID_REQUEST`（字段错误、畸形 JSON）或 `CHALLENGE_INVALID`（签名、kid、格式不对）；403 `APP_FORBIDDEN`（应用不存在或关闭）、`ORIGIN_NOT_ALLOWED`、`ACTION_FORBIDDEN`；409 `CHALLENGE_USED`；410 `CHALLENGE_EXPIRED`；有效格式但证明错误为 422 `PROOF_INVALID`；429 限流（Retry-After）；503 依赖不可用。
- verify 接口的业务拒绝不用非 2xx 表达，语义见下文“verify 结果语义”。非 2xx 只表示调用方错误或服务不可用。
- request_id 在 verify 中必填，由调用方生成，字符集 `[A-Za-z0-9_-]`，1–64 字符（UUID v4 符合）；验证服务在日志中仅记录此 ID 及非敏感决策元数据。
- 请求体为单个 JSON 对象，最大 8 KiB；Content-Type 接受 `application/json` 或 `text/plain`。浏览器组件应使用 `text/plain`，使请求成为 CORS 简单请求、免去预检；未知字段忽略，以便新版 SDK 兼容旧服务端。
- app_key 字符集 `[A-Za-z0-9_-]`，8–64 字符；action 为 `[a-z0-9_]`，1–32 字符。

## PoW 算法

算法由服务端策略按 app/action 选择，写入签名 challenge；客户端不能选择或降级算法。所有算法共用“多题 + 数值阈值”结构：

- 一个 challenge 含 `puzzles` 道独立子题（k，服务端限制 1–32，建议默认 8）。客户端须为每道子题 i（0 ≤ i < k）找到 nonce，使 `H(proof_input(i, nonce))` 按大端无符号整数解释后严格小于 `target`。
- `target` 为 64 位小写十六进制（256 位大端）。服务端由每题期望尝试次数 D 计算 `target = floor(2^256 / D)`，D 有服务端上下限。比较时按字节从高到低逐字节比较即可，各语言无需大整数库。
- 证明输入：UTF-8 编码的 `BG1\n<algorithm>\n<challenge_id>\n<i>\n<nonce>`。i 与 nonce 均为十进制非负整数字符串，仅 `[0-9]`，nonce 最多 20 位，无正负号/空格/前导零（零本身除外）。
- 选择多题而不是单题：单题求解次数服从几何分布，p99 约为平均值的 4.6 倍；k=8 时总耗时 p99 约为平均的 2.0 倍，k=16 约 1.7 倍（指数分布求和的模拟结果）。总期望尝试次数为 k×D。子题之间相互独立，浏览器可按 `navigator.hardwareConcurrency` 分给多个 Worker 并行求解。
- 数值阈值替代“前导零位”：难度可以按任意倍数调整，不受 2 倍一档的限制。

| algorithm | H | 状态 |
|---|---|---|
| `sha256-target-v1` | SHA-256(proof_input) | P1 唯一实现的算法。服务端校验每道子题约 1 微秒，不存在校验放大问题 |
| `argon2id-target-v1` | Argon2id(password = proof_input, salt = challenge_id 解码后的 16 字节, version 0x13, `memory_kib`, `iterations`, parallelism = 1, 输出 32 字节) | 保留名称，P2 评估，未实现 |

SHA-256 在原生代码/GPU 上比浏览器快 2–3 个数量级，所以难度只能按低端手机设定，攻击者的单位成本远低于合法用户。Argon2id 等内存困难算法能缩小这一差距，但服务端每校验一道子题也要付出同等的内存和 CPU 成本：伪造的 solve 请求会变成对服务端的放大攻击。因此 `argon2id-target-v1` 只能在满足以下条件后，作为策略对高价值 action 或可疑流量启用，不作为全局默认：

1. 低端真机上 k、D、memory_kib 组合的耗时分布有实测；
2. solve 先限流，再做廉价校验（签名、期限、应用、Origin、格式），最后做哈希校验；任一子题失败立即中止；
3. 服务端哈希校验有全局并发上限，超过上限返回 503 而不是排队；
4. 有校验 CPU/内存指标和放大比告警。

浏览器和服务端必须用同一组 fixture（含边界：target 恰好等于 digest、k=1、nonce=0、20 位 nonce）校验每个算法。

## POST /v1/challenges（浏览器）

请求：`app_key`、`action`。服务端从请求读取 Origin，校验精确应用白名单；缺失/`null` Origin 默认拒绝。仅为允许来源返回 CORS，`Vary: Origin`，不用跨站 cookie。预检请求不带 body、无法得知应用，因此对任何 Origin 都返回允许（`Access-Control-Max-Age: 600`）；真正的请求仍按应用白名单校验，且只对允许来源返回 CORS 头。

签发前校验应用/action 启用并完成签发限流。签发是无状态的：不写入任何 per-challenge 的 Redis 记录，只更新有界的限流计数。

响应 data：

```json
{
  "challenge": "v1.k2026a.eyJ...<payload>.<mac>",
  "challenge_id": "base64url(16 字节随机)",
  "algorithm": "sha256-target-v1",
  "params": {"puzzles": 8, "target": "0000a7c5ac471b47..."},
  "expires_at": 1790000120
}
```

`challenge` 是服务端签名的不透明字符串，格式为 `v1.<kid>.<payload_b64>.<mac_b64>`：

- payload 为 UTF-8 JSON，字段：`challenge_id`、`app_key`、`action`、`origin`、`algorithm`、`params`、`policy_version`、`iat`、`exp`（Unix 秒）。b64 均为 base64url 无 padding。
- `mac = HMAC-SHA256(key[kid], "v1." + kid + "." + payload_b64)`。服务端校验 MAC 时针对收到的原始 payload_b64 字节，不重新序列化 JSON。
- payload 只签名、不加密，客户端可读，因此不得包含内部 ID、IP 或其它敏感信息。
- `challenge` 总长度上限 1024 字节，超过直接 400，不解码。
- 响应中的 `challenge_id`、`algorithm`、`params`、`expires_at` 只是 payload 的副本，供客户端求解使用；solve 时服务端只认签名 payload，绝不读取客户端回传的这些值。

签名密钥（`kid` → ≥32 字节随机 secret）由部署配置或密钥管理注入，所有 gateway 副本共享，不进仓库、不进 MySQL 明文。同一时间只有一个 active kid 用于签发，另可有若干 verify-only kid 用于校验。轮换顺序：新 kid 以 verify-only 发布到全部副本 → 切为 active → 等待 challenge 最长 TTL 加时钟偏差后删除旧 kid。签名密钥泄露等于攻击者可以自签零难度 challenge，PoW 防护失效（token 仍受应用/action/Origin 与限流约束），必须立即删除该 kid。

## POST /v1/solutions（浏览器）

请求：`app_key`、`challenge`（原样回传的签名字符串）、`nonces`（长度等于 `params.puzzles` 的字符串数组，第 i 项对应子题 i）。

服务端按从廉价到昂贵的顺序校验，任一步失败即返回：

1. body 大小、字段格式、`challenge` 长度；
2. kid 已知，常量时间比较 MAC；
3. 解码 payload，检查 `exp`（允许不超过 5 秒的时钟偏差，副本需 NTP 同步）；
4. 请求 `app_key` 与 payload 一致，应用和 action 当前仍启用；请求 Origin 与 payload `origin` 一致且当前仍在允许列表；
5. solve 限流（应用、IP、challenge 维度）；
6. `nonces` 数量与格式；
7. 逐题校验哈希，首个失败即停止。

通过第 6 步后、校验哈希之前，无论证明对错，服务端都会以 `SET bg:pow:used:<challenge_id> 1 NX EX <exp-now>` 标记 challenge 已用：一个 challenge 只有一次提交机会，防止把服务端当作求解预言机。标记已存在返回 409 `CHALLENGE_USED`；证明错误返回 422 `PROOF_INVALID`；证明正确则生成随机 256 位 token 并写入 token 状态。若标记成功而 token 写入失败，返回 503，浏览器重新获取 challenge。这一路径只会少发 token，不会多发，所以 solve 不需要多 key Lua 脚本。

响应 data：`token`、`expires_at`（建议 60 秒）。challenge 默认 120 秒；TTL 由服务端控制。token 为 opaque，不是可由业务端离线接受的 JWT。

签发后策略变更（如提高难度）不追溯已签发且未过期的 challenge；应用/action 禁用、Origin 移除会在第 4 步立即生效。

浏览器 solve 超时没有 token 时，可重新获取挑战；不得把失败/超时当成功。challenge 不幂等返回原始 token，以避免再次分发凭证。

## POST /v1/verify（业务服务器）

### 应用与密钥对

参考易盾：每个接入方是一个**应用（app）**，有一个公开的 `app_key`（对应易盾的 captchaId，可以放在前端），以及一组或多组**密钥对**（`secret_id` + `secret_key`，对应易盾的 secretId/secretKey，只能放在业务服务器）。一个平台可以建任意多个应用；密钥对绑定到单个应用，不能跨应用使用。

`secret_key` 只在创建时显示一次。服务端为了验签必须持有它，因此用主密钥（KEK，AES-256-GCM，`secret_id` 作为附加数据）加密后存库；数据库单独泄露拿不到可用的 secret_key。轮换密钥对时新旧两组在宽限期内都可用。

### 签名（BG1-HMAC-SHA256）

与易盾一样，`secret_key` 不随请求发送，请求带 `secret_id`、时间戳、nonce 和签名。与易盾不同的是用 HMAC-SHA256 签整个请求体，而不是 MD5 签拼接后的参数：

```text
canonical = "BG1-HMAC-SHA256\n" + "POST" + "\n" + "/v1/verify" + "\n"
          + timestamp + "\n" + nonce + "\n" + hex(sha256(请求体原始字节))
signature = hex(HMAC-SHA256(key = secret_key 的 UTF-8 字节, canonical))
```

| Header | 内容 |
|---|---|
| `X-BG-Secret-Id` | secret_id |
| `X-BG-Timestamp` | Unix 秒，与 gateway 时间相差不超过 ±300 秒（`SignatureWindowSeconds`） |
| `X-BG-Nonce` | 每个请求新生成，`[A-Za-z0-9_-]`，16–64 字符 |
| `X-BG-Signature` | 64 位小写十六进制 |

- 签名中的路径固定为 API 路由 `/v1/verify`，与实际请求 URL 的前缀无关，反向代理改写路径不影响验签。
- 签名覆盖请求体的原始字节：SDK 必须先序列化 JSON，再对同一份字节签名并发送，不能签完再重新序列化。
- gateway 先检查格式和时间戳，再验签；验签通过后才记录 nonce（Redis，保留 2 倍窗口），所以没有密钥的人无法预先占用 nonce。
- 同一个已签名请求再次发送会被判为重放（401 `NONCE_REUSED`）。SDK 重试时必须**重新签名**（新 nonce、新时间戳），但保持 `request_id` 和请求体不变，这样仍能拿到幂等结果。
- 未知 secret_id 与签名错误返回同一个错误，避免被用来探测 secret_id 是否存在。
- 签名用例见 `api/fixtures/hmac-signature-v1.json`，各语言 SDK 必须用它测试。

请求：
```json
{"app_key":"app_xxx","token":"opaque_token","action":"login","request_id":"caller-generated-uuid"}
```

应用必须同时匹配密钥对、token、请求的 app_key。action 必须由业务处理器写死或由可信路由配置选择，不能直接信任浏览器请求值。核验 token 内的签发 origin 是否仍在应用许可范围内。若需要绑定某一业务表单/操作 ID，需 P1 扩展服务端预签发 intent，不能让客户端自报可信业务身份。

### verify 结果语义

HTTP 状态码只描述“这次调用是否被正确处理”，`data.success` 才描述“token 是否通过核验”。由浏览器提供、调用方无法控制的 token 引起的一切失败都是业务拒绝，返回 200；调用方自己能修正的错误和服务不可用才用非 2xx。

通过：
```json
{"code":"OK","data":{"success":true,"app_key":"...","action":"login","verified_at":1790000030},"request_id":"..."}
```

拒绝：
```json
{"code":"OK","data":{"success":false,"reason":"TOKEN_EXPIRED"},"request_id":"..."}
```

| 结果 | HTTP | code / reason | SDK 分类 | 调用方处理 |
|---|---|---|---|---|
| 通过 | 200 | `success: true` | Passed | 继续业务事务 |
| token 缺失、为空、格式错误、伪造、未知 | 200 | `TOKEN_INVALID` | Rejected | 提示用户重新验证 |
| token 过期 | 200 | `TOKEN_EXPIRED` | Rejected | 同上 |
| token 已被其它 request_id 消费 | 200 | `TOKEN_CONSUMED` | Rejected | 同上 |
| token 属于其它应用 | 200 | `APP_MISMATCH` | Rejected | 同上 |
| token 的 action 与请求不符 | 200 | `ACTION_MISMATCH` | Rejected | 同上 |
| 签发 Origin 已不在允许列表 | 200 | `ORIGIN_NOT_ALLOWED` | Rejected | 同上 |
| app_key/action/request_id 缺失或格式错误、畸形 JSON、body 超限 | 400 | `INVALID_REQUEST` | CallerError | 不重试，告警修代码 |
| secret_id 未知、已吊销或过期，签名错误，签名头缺失或格式错误 | 401 | `UNAUTHORIZED` | CallerError | 不重试，告警修配置 |
| 时间戳超出 ±300 秒 | 401 | `TIMESTAMP_EXPIRED` | CallerError | 检查业务服务器时钟（NTP） |
| nonce 已用过（同一签名请求被重放） | 401 | `NONCE_REUSED` | CallerError | 检查重试逻辑：重试必须重新签名 |
| 密钥对与 app_key 不属于同一应用、应用已停用 | 403 | `APP_FORBIDDEN` | CallerError | 不重试，告警 |
| 同 request_id 但请求体不同 | 409 | `IDEMPOTENCY_CONFLICT` | CallerError | 不重试，修复 request_id 生成 |
| 限流 | 429 | `RATE_LIMITED` | Unknown | 按 Retry-After 用原 request_id 重试 |
| 依赖不可用、内部错误 | 503/5xx | `UNAVAILABLE` | Unknown | 用原 request_id 重试或提示稍后再试 |
| 网络错误、超时、响应体畸形、缺 `success`，或 `success: true` 但 app_key/action 与请求不符 | — | — | Unknown | 同上 |

- 除 Passed 外，所有分类都不得执行业务副作用。Rejected 面向终端用户；CallerError 面向运维告警；Unknown 表示服务端可能已经消费 token，只能用原 request_id 重试，SDK 禁止自动换 request_id。
- 4xx 只出现在调用方可确定修正的情况，不会出现在“token 不对”的情况。这样 SDK 不需要按 reason 列表猜测某个 4xx 是拒绝还是故障，新增 reason 也不会被旧 SDK 误判为异常。SDK 遇到未知 reason 一律按 Rejected 处理。
- 幂等缓存同时保存 Passed 和 Rejected 结果。同 request_id 加完全相同请求的重试返回原结果；认证、应用启停检查在读取幂等结果之前执行，因此吊销密钥对后重试得到 401，而不是缓存的通过结果。
- 无 token 不返回通过。幂等窗口（建议 10 分钟）过期后，重试只会得到 Rejected，不保证重放原来的通过结果；业务必须以自己的幂等记录为准。

业务服务顺序：普通字段校验 → 用户身份/业务授权 → verify → 本地业务幂等事务。若本地事务失败，使用原业务幂等键恢复；不能仅凭已经消费的凭证反复执行副作用。

## 管理后台路由方案

| 读页面 / 数据 | 写操作 | 权限 |
|---|---|---|
| `/app/index`、`/app/list` | `/app/save`、`/app/status` | app.view / app.manage |
| `/app/secret/list` | `/app/secret/create`、`/app/secret/rotate`、`/app/secret/revoke` | secret.view / secret.manage |
| `/policy/index`、`/policy/list` | `/policy/publish`、`/policy/rollback` | policy.view / policy.manage |
| `/event/index`、`/event/list` | 无 | event.view |
| `/admin/index`、`/role/index` | `/admin/save`、`/role/save` | admin.manage / role.manage |
| `/audit/index`、`/audit/list` | 无 | audit.view |

所有写操作 POST，Redis session + CSRF + Origin + 权限检查。菜单过滤不能代替路由权限。secret_key 只展示一次、审计脱敏；secret.manage 授权只允许超级管理员。

## 必须验证的安全用例

- 篡改签名 challenge 的 payload（algorithm、params、exp、app_key、action、origin）或 MAC；未知 kid、已删除 kid；超长 challenge；nonces 数量不符或格式非法；超大 body；跨 app/action/origin；过期边界和时钟偏差边界。
- 同一 challenge 第二次提交（无论第一次对错）返回 409；客户端回传的 `params` 副本被忽略。
- 签名密钥轮换：verify-only kid 签发的 challenge 仍可校验，删除后被拒。
- PoW fixtures：各算法的 digest 恰好等于 target、k=1、nonce=0、20 位 nonce；浏览器与服务端结果一致。
- 并发 solve：单一 challenge 仅一次签发；并发 verify：不同 request_id 仅一个成功，其余为 200 `TOKEN_CONSUMED`。
- verify 结果语义：每种 token 类失败都返回 200 + `success:false`；调用方错误返回对应 4xx；SDK 把未知 reason 归为 Rejected，把畸形响应归为 Unknown。
- 签名：篡改请求体、错误 secret_key、时间戳超窗、nonce 重放、签名格式错误均为 401；未知 secret_id 与签名错误的响应一致；用错误 KEK 解不开的密钥对返回 503 而不是 401。
- verify 已提交但响应丢失，重新签名后以同一 request_id/body 重试恢复原结果（含原拒绝结果）；同 ID 换 token/action 返回 409 `IDEMPOTENCY_CONFLICT`。
- Redis/MySQL 故障、应用禁用、密钥撤销、规则变更；无静默绕过。
- Origin 伪造不构成身份；X-Forwarded-For 只信任配置代理；限流覆盖 challenge/solve/verify。
- 后台未登录、越权、CSRF、跨 Origin POST、session 固定/撤销和敏感日志泄漏。
