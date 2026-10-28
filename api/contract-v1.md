# v1 API 契约草案

服务端以本文为 P1 输入，再补齐 OpenAPI 3.1 和各语言共享的成功/失败 fixtures。字段在发布 v1 前可调整，不宣称与 ALTCHA 线协议兼容。

## 统一约定

HTTPS；JSON；snake_case；严格 body 大小和字段长度；浏览器端没有服务端密钥。错误格式：

```json
{"code":"CHALLENGE_EXPIRED","message":"验证已过期，请重试","request_id":"..."}
```

API 成功用 `{"code":"OK","data":{...},"request_id":"..."}`；后台继续采用 PearAdmin 的数值 code 格式，两者不混用。错误不回显 token、签名、secret、内部 SQL 或原始 IP。

- 浏览器接口（challenges/solutions）：400 `INVALID_REQUEST`（字段错误、畸形 JSON）或 `CHALLENGE_INVALID`（签名、kid、格式不对）；403 `APP_FORBIDDEN`（应用不存在或关闭）、`ORIGIN_NOT_ALLOWED`、`ACTION_FORBIDDEN`；409 `CHALLENGE_USED`；410 `CHALLENGE_EXPIRED`；有效格式但证明错误为 422 `PROOF_INVALID`；滑块位置或轨迹未通过为 422 `ANSWER_INVALID`；429 限流（Retry-After）；503 依赖不可用。只要请求的 Origin 已知对该应用允许（包括限流、challenge 已过期，以及应用已在缓存中时的 `CHALLENGE_INVALID`），错误响应也带 CORS 头，组件才能读到错误码；未知应用或不允许的 Origin 不带。
- verify 接口的业务拒绝不用非 2xx 表达，语义见下文“verify 结果语义”。非 2xx 只表示调用方错误或服务不可用。
- request_id 在 verify 中必填，由调用方生成，字符集 `[A-Za-z0-9_-]`，1–64 字符（UUID v4 符合）；验证服务在日志中仅记录此 ID 及非敏感决策元数据。
- 请求体为单个 JSON 对象，最大 8 KiB（solutions 为 32 KiB，用于容纳滑块轨迹），超限返回 400 `INVALID_REQUEST`（JSON 格式，不是空的 413）；Content-Type 接受 `application/json` 或 `text/plain`。浏览器组件应使用 `text/plain`，使请求成为 CORS 简单请求、免去预检；未知字段忽略，以便新版 SDK 兼容旧服务端。
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

## 验证题型（kind）

每个 action 配置一种模式（`invisible` / `interactive` / `adaptive`）和一个交互题型列表（`slider`、`click_text`、`click_shape` 的任意组合），gateway 结合风险分决定本次出什么题，题型写入签名 challenge 的 `kind` 字段。所有题型都附带 PoW。设计与规则见 `docs/challenge-types.md`、`docs/click-challenges.md`。

| kind | 浏览器要做什么 | 何时出现 |
|---|---|---|
| `invisible` | 只在后台算 PoW | invisible 模式；adaptive 模式且风险低 |
| `slider` | 拖动拼图对准缺口，同时后台算 PoW | 题型列表中含 slider，且被选中 |
| `click_text` | 按顺序点击图中的汉字，同时后台算 PoW | 题型列表中含 click_text，且被选中 |
| `rotate` | 拖动滑块转动中央圆盘，使图案与外圈对齐（`docs/rotate-challenge.md`），同时后台算 PoW | 题型列表中含 rotate，且被选中 |
| `jigsaw` | 推理拼图：把错位的两块图块拖动交换回来（`docs/challenge-catalog.md`），同时后台算 PoW | 题型列表中含 jigsaw，且被选中 |
| `click_order` | 语序点选：按成语的正确顺序点击被打乱的字，同时后台算 PoW | 题型列表中含 click_order，且被选中 |
| `click_icon` | 图标点选：按提示图标的顺序点击大图中同款图标，同时后台算 PoW | 题型列表中含 click_icon，且被选中 |
| `spatial` | 空间推理：点击被描述图形旁边紧挨着的图形，同时后台算 PoW | 题型列表中含 spatial，且被选中 |
| `click_shape` | 按顺序点击描述的图形（要数点数、边数），同时后台算 PoW | 题型列表中含 click_shape，且被选中 |
| `clock` | 时钟读数：按顺序点击显示指定时间的模拟时钟（`docs/challenge-catalog.md` §7），同时后台算 PoW | 题型列表中含 clock，且被选中 |
| `grid_count` | 九宫格计数：选出所有恰好有 N 个某种图形的格子后点“确认”，同时后台算 PoW | 题型列表中含 grid_count，且被选中 |
| `mirror` | 镜像辨别：对照示例，选出所有被翻转过（而不只是转动）的图形后点“确认”，同时后台算 PoW | 题型列表中含 mirror，且被选中 |
| `fallback` | 只算 PoW，难度 × 8，独立限流 | 用户在滑块面板选择“无法完成拼图？”，且风险不为高（invisible 模式下无此路径） |

interactive 模式每次从列表随机选题；adaptive 模式中风险随机选、高风险选列表中最难的（spatial > grid_count > clock > click_shape > mirror > click_icon > click_order > click_text > jigsaw > rotate > slider）。风险为高时，任何题型的 PoW 难度 × 4；高风险请求即使带 `fallback: true` 也照样出列表中最难的题型，因为 fallback 只有 PoW，机器人用原生代码几毫秒就能算完。实际难度不超过 `Challenge.MaxDifficulty`。应用超过图片题配额（`RateLimit.InteractivePerAppPerMinute`），或图片生成满载且预生成题库为空时，本该出的图片题降级为 `invisible`，PoW 难度再 × `Risk.DegradedDifficultyFactor`（默认 8），且每道子题不低于 `Risk.DegradedMinDifficulty`（默认 60000，8 道子题在浏览器里约 2 秒），verify 的 `risk_reasons` 带 `CAPACITY_DEGRADED`；不返回 503。签发不需要任何工作量，为了不让少数网络替所有人用光应用配额，同一应用下同一网络（IPv4 /24、IPv6 /48）每分钟的图片题另有上限（`RateLimit.InteractivePerPrefixPerMinute`，默认 300），超出返回 429 `RATE_LIMITED`，不走降级。同一网络跨应用每分钟的签发也有上限（`RateLimit.ChallengePerPrefixPerMinute`，默认 1200），同一 IP 在失败窗口内失败达到 `Risk.FailureBlockThreshold`（默认 20）次后签发也返回 429，直到失败过期。风险分和原因码只通过 verify 返回给业务服务器，不返回给浏览器。

### 前端指定题型

签发请求可以带 `kinds` 或 `levels`（二者只能选一个），在 action 的配置范围内指定题型。**只有 action 打开了「允许页面指定题型」（`client_select`，管理后台的 action 设置或 `botguard-ctl action set -client-kinds`）才生效，默认关闭；关闭时这两个字段被忽略（不报错），按 action 策略出题。** 这两个字段谁都能写，打开后机器人也能每次只要列表里最简单的题型，所以只在确实需要固定题型或难度阶梯的场景打开：

| 写法 | 效果 |
|---|---|
| `"kinds": ["slider"]` | 固定出这一种 |
| `"kinds": ["slider", "rotate"]` | 在这几种中随机 |
| `"kinds": ["*"]` | 在 action 启用的全部题型中随机 |
| `"levels": [["invisible"], ["slider", "rotate"], ["spatial"]]` | 难度阶梯：从易到难的若干档，档内随机；按用户行为升降档 |

规则：

- 只认 action 题型列表中启用的题型，其余（含未知名称）忽略；过滤后为空时按 action 策略出题。`invisible` 只在 adaptive 模式、风险为低时有效，所以前端不能把 interactive 模式降为无感。invisible 模式的 action 忽略前端指定，始终无感。
- 风险为高时仍出 action 列表中最难的题型，不受前端指定影响；fallback、PoW 难度倍数和容量降级规则不变。指定了 `kinds` 时风险为低也出交互题（invisible 除外）。
- 阶梯：gateway 按应用 + IP 段记录用户所在档位（从 0 开始）。用 `levels` 签发的 challenge 在 solve 时，PoW 或答案错误升一档，连续通过 `Risk.LadderDowngradePasses`（默认 3）次降一档；档位在最后一次 solve 后 `Risk.LadderWindowSeconds`（默认 1800）秒过期。风险为中时至少从第 2 档开始，风险为高时同上一条。某档过滤后为空时用更难的一档（已在最高档则用更易的一档）。档位最多 8 档。
- `kinds` 最多 16 项；`levels` 最多 8 档、每档最多 16 项；超出或两者同时出现返回 400 `INVALID_REQUEST`。

## GET /sdk/v1/{file}（浏览器组件脚本）

gateway 托管浏览器组件：`load.js`（经典脚本，提供 `initBotGuard`，对应易盾的 `load.min.js`）、`botguard.js`、`signals.js`、`solver.js`、`worker.js`。这些是公开文件，返回 `Access-Control-Allow-Origin: *`，因为模块脚本和跨域 Worker 导入都要求 CORS；`Cache-Control: public, max-age=300`。不兼容的改动发布到新的路径版本（`/sdk/v2/`）。

## GET /v1/ui?app_key=…（浏览器）

返回应用在管理后台「外观与文案」里的设置，组件加载时读取：`lang`（`zh-CN`、`zh-TW`、`en`、`auto`）、`theme`（`light`、`dark`、`auto`）、`accent`（`#rrggbb`）、`radius`（像素）、`texts`（按组件文案键替换文案，如 `{"idle": "点击开始验证"}`；组件只在语言与 `lang` 相同时使用，`lang` 为空或 `auto` 时按简体中文）。都是可选字段，没有设置时 data 为 `{}`。页面自己设置的选项优先于这里的值。

和签发一样按应用的允许来源校验 Origin，只对允许来源返回 CORS 头；不允许的来源 403 `ORIGIN_NOT_ALLOWED`，未知或停用的应用 403 `APP_FORBIDDEN`。内容不是秘密，响应带 `Cache-Control: public, max-age=60`，修改后约 1 分钟内生效。

## POST /v1/challenges（浏览器）

请求：`app_key`、`action`，可选 `signals`（风险信号对象，字段见 `docs/challenge-types.md` §5，缺失计为 `NO_SIGNALS`）、`fallback`（布尔，选择无障碍备用路径），以及 `kinds`（字符串数组）或 `levels`（字符串数组的数组），见上文“前端指定题型”，`widget`（字符串，浏览器组件的版本号，0.3.0 起由组件自动带上）、`lang`（页面语言，BCP 47 标签，0.4.0 起由组件带上）。`signals.activation` 为 `auto` 时（组件 0.5.0 起，嵌入式 `mode="inline"`），表示没有点击就开始了验证：不计点击相关的两条风险规则，但不会出无感，低风险也出交互题。`lang` 决定 `click` / `grid` 的 `prompt` 用哪种语言：简体中文（缺省、`zh`、`zh-CN`、`zh-Hans`、`zh-SG`）、繁体中文（`zh-TW`、`zh-HK`、`zh-MO`、`zh-Hant`）、英文（其他一切）。非中文页面不出文字点选 `click_text` 和语序点选 `click_order`（要认读汉字），从 action 的其余题型中出；只剩这两种时出滑块。`widget` 只用于管理后台统计仍在使用的旧版组件（早于 0.3.0 的组件不能显示 `clock`、`mirror`、`grid_count`），不影响出题：脚本省略它和旧组件一样容易，所以 gateway 不会因为缺少它就换题型。服务端从请求读取 Origin，按应用的允许来源校验；缺失/`null` Origin 默认拒绝。允许来源有三种写法：精确来源 `https://shop.example.com`（协议、域名、端口都要一致）；子域名通配 `https://*.example.com`，匹配任意层子域名，协议和端口要一致，不含 `example.com` 本身，`*.com` 这类只有一级的写法会被拒绝；全部允许 `*`，任何来源都通过（包括 `app://`），等于放弃来源校验，任何网站都能用这个 app_key 拿到 token（例如把验证组件嵌到别的网站、诱导访客代做验证），只在域名多且经常变化时使用，并由业务端核对 verify 返回的 `origin`。原生 App（包括 uni-app App 端从逻辑层发出的请求）没有 Origin，可改用请求头 `X-BotGuard-Origin: app://包名`，白名单里登记同样的 `app://包名`；只有请求不带 Origin 时才读这个头，所以浏览器里的网页（POST 一定带 Origin，`null` 也算）无法冒充 App。App 来源的响应不带 CORS 头。和 Origin 一样，这个头能被脚本伪造，只用于区分应用的使用场景，不构成身份。仅为允许来源返回 CORS，`Vary: Origin`，不用跨站 cookie。预检请求不带 body、无法得知应用，因此对任何 Origin 都返回允许（`Access-Control-Max-Age: 600`）；真正的请求仍按应用白名单校验，且只对允许来源返回 CORS 头。

签发前校验应用/action 启用并完成签发限流。签发是无状态的：不写入任何 per-challenge 的 Redis 记录，只更新有界的限流计数。

响应 data：

```json
{
  "kind": "invisible",
  "challenge": "v1.k2026a.eyJ...<payload>.<mac>",
  "challenge_id": "base64url(16 字节随机)",
  "algorithm": "sha256-target-v1",
  "params": {"puzzles": 8, "target": "0000a7c5ac471b47..."},
  "expires_at": 1790000120
}
```

`kind` 为 `click_text` / `click_shape` 时响应带 `click` 对象：`width`、`height`（320×200）、`image`（JPEG data URL）、`prompt`（纯文本提示，如“请依次点击：有 5 个点的方块、六边形、红色的三角形”）、`targets`（需点击的个数，3 或 4）。

`kind` 为 `slider` 时响应还带 `slider` 对象：`width`、`height`（原图尺寸 320×160）、`background`（JPEG data URL）、`piece`（PNG data URL）、`piece_y`、`piece_size`。正确答案不在响应里。

`challenge` 是服务端签名的不透明字符串，格式为 `v1.<kid>.<payload_b64>.<mac_b64>`：

- payload 为 UTF-8 JSON，字段：`challenge_id`、`app_key`、`action`、`origin`、`algorithm`、`params`、`policy_version`、`iat`、`exp`（Unix 秒）、`kind`、`sealed`。b64 均为 base64url 无 padding。
- `sealed` 是只有服务端能解开的数据（滑块答案、签发时的风险分和原因码）：AES-256-GCM，密钥由该 kid 的签名密钥经 HKDF-SHA256（info `botguard challenge seal v1`）派生，`challenge_id` 作为附加数据。nonce 为随机 96 位：同一 kid 下签发量接近 2³² 次时 nonce 碰撞概率超出 NIST 建议，碰撞会泄露两题答案的异或（伪造仍不可能，sealed 受 HMAC 覆盖）。因此 challenge 签名密钥应定期轮换 kid，远早于这个量级（例如每月或每 10 亿次签发）。
- `mac = HMAC-SHA256(key[kid], "v1." + kid + "." + payload_b64)`。服务端校验 MAC 时针对收到的原始 payload_b64 字节，不重新序列化 JSON。
- payload 只签名、不加密，客户端可读，因此不得包含内部 ID、IP 或其它敏感信息。
- `challenge` 总长度上限 2048 字节，超过直接 400，不解码。
- 响应中的 `challenge_id`、`algorithm`、`params`、`expires_at` 只是 payload 的副本，供客户端求解使用；solve 时服务端只认签名 payload，绝不读取客户端回传的这些值。

签名密钥（`kid` → ≥32 字节随机 secret）由部署配置或密钥管理注入，所有 gateway 副本共享，不进仓库、不进 MySQL 明文。同一时间只有一个 active kid 用于签发，另可有若干 verify-only kid 用于校验。轮换顺序：新 kid 以 verify-only 发布到全部副本 → 切为 active → 等待 challenge 最长 TTL 加时钟偏差后删除旧 kid。签名密钥泄露等于攻击者可以自签零难度 challenge，PoW 防护失效（token 仍受应用/action/Origin 与限流约束），必须立即删除该 kid。

## POST /v1/solutions（浏览器）

请求：`app_key`、`challenge`（原样回传的签名字符串）、`nonces`（长度等于 `params.puzzles` 的字符串数组，第 i 项对应子题 i），可选 `signals`（交互期间的信号，如 `pointer_type`、`untrusted_event`）。滑块题还必须带 `answer`：

```json
{"x": 172.4, "trajectory": [[0, 0, 0], [16.7, 3.2, 0.5], [33.4, 9.8, 1.1]]}
```

`x` 是拼块左边缘在原图坐标中的位置；`trajectory` 每项为 `[自按下起的毫秒数, x, y]`，最多 400 个点。位置容差 ±4 像素（`Slider.Tolerance`）；轨迹规则（时长、点数、瞬移、等间隔、匀速、纯直线、终点不一致）见 `docs/challenge-types.md` §6.3。位置错和轨迹不像人都返回同一个 422 `ANSWER_INVALID`，具体原因只记服务端日志。点选类（`click_text`、`click_order`、`click_icon`、`click_shape`、`clock`、`spatial`）共用点选的 `answer` 格式；`click_icon` 的签发响应在 `click.prompt_image` 中给出提示图标条。推理拼图（`jigsaw`）的签发响应带 `jigsaw`（`image`、`rows`、`cols`），`answer` 为 `{"swap": [被拖动的块, 放下的块], "trajectory": [[ms, x, y], ...]}`，块按行从 0 编号，轨迹为图片坐标，校验交换的两块、轨迹起止块，以及与滑块相同的轨迹规则。旋转题（`rotate`）的 `answer` 与滑块格式相同：`x` 是拖动距离（0 到签发响应中的 `rotate.track`），换算为旋转角 `x / track × 360°`，容差 ±7°（`Rotate.ToleranceDegrees`），轨迹规则与滑块一致。点选题的 `answer` 为 `{"clicks": [[ms, x, y], ...], "moves_between": [n, ...]}`：`clicks` 按点击顺序、坐标为原图坐标、时间自面板显示起；`moves_between[i]` 为第 i 次点击前的指针移动次数。校验数量、位置（目标半径 + 4 像素，触摸输入 + 9 像素）、顺序、读题时间、点击间隔和鼠标移动，规则见 `docs/click-challenges.md` §5。`pointer_type` 只认 `mouse`、`touch`、`pen`、`keyboard`，其他值（含大小写不同）一律按 `mouse` 处理，即最严格的规则。用键盘完成的滑块走宽松规则，并在 verify 中带上 `KEYBOARD_INPUT` 原因码；点选题不能用键盘完成，声称 `keyboard` 时按鼠标规则校验。选格题（`grid_count`、`mirror`）的签发响应带 `grid`（`image`、`prompt`、`rows`、`cols`，`mirror` 另有示例图 `prompt_image`），格子把图片均分为 `rows × cols`，按行从 0 编号。`answer` 与点选格式相同：`clicks` 是每一次点格子（选中或取消）的 `[ms, x, y]`，最多 24 次，`moves_between` 同点选；服务端按点击重放得出最后选中的格子，必须与答案完全一致，并沿用点选的读题时间、点击间隔和鼠标移动规则。触摸输入免去鼠标专用的检查（纯直线拖动、点击间无移动），verify 中带 `TOUCH_INPUT`（权重 0，仅供业务方参考）。PoW 或滑块失败都会计入该 IP 及其网段的失败次数（10 分钟窗口），提高其后续风险分。签发时 gateway 把激活组件的指针类型、客户端网段和签发时间封进 challenge：解题声称 `touch` / `pen` 而激活时不是，按鼠标规则校验并带 `POINTER_MISMATCH`；解题换了网段带 `IP_CHANGED`；图片题 800 ms 内提交带 `SOLVE_TOO_FAST`。这些只加风险分，不拒绝。

服务端按从廉价到昂贵的顺序校验，任一步失败即返回：

1. body 大小、字段格式、`challenge` 长度；
2. kid 已知，常量时间比较 MAC；
3. 解码 payload，检查 `exp`（允许不超过 5 秒的时钟偏差，副本需 NTP 同步）；
4. 请求 `app_key` 与 payload 一致，应用和 action 当前仍启用；请求 Origin 与 payload `origin` 一致且当前仍在允许列表；
5. solve 限流（应用、IP、challenge 维度）；
6. `nonces` 数量与格式；
7. 逐题校验哈希，首个失败即停止。

通过第 6 步后、校验哈希之前，无论证明对错，服务端都会以 `SET bg:pow:used:<challenge_id> 1 NX EX <exp+2×skew-now>` 标记 challenge 已用（多出的时钟偏差余量保证时钟稍慢的副本也认得这个标记）：一个 challenge 只有一次提交机会，防止把服务端当作求解预言机。标记已存在返回 409 `CHALLENGE_USED`；证明错误返回 422 `PROOF_INVALID`；证明正确则生成随机 256 位 token 并写入 token 状态。若标记成功而 token 写入失败，返回 503，浏览器重新获取 challenge。这一路径只会少发 token，不会多发，所以 solve 不需要多 key Lua 脚本。

响应 data：`token`、`expires_at`（建议 60 秒）、`expires_in`（剩余秒数）。浏览器应以 `expires_in` 计时，因为客户端时钟可能不准；`expires_at` 保留给旧版组件。challenge 默认 120 秒；TTL 由服务端控制。token 为 opaque，不是可由业务端离线接受的 JWT。

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
- gateway 先检查格式和时间戳，再验签；验签通过后才记录 nonce（Redis，保留 2 倍窗口加时钟偏差），所以没有密钥的人无法预先占用 nonce。
- 同一 IP 每分钟验签失败（`UNAUTHORIZED`）超过 `RateLimit.AuthFailuresPerIPPerMinute`（默认 60）次后，该 IP 的 verify 在查密钥之前直接返回 429，以免随机 secret_id 变成数据库查询。`TIMESTAMP_EXPIRED` 不计入。
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
{"code":"OK","data":{"success":true,"app_key":"...","action":"login","verified_at":1790000030,
 "challenge_kind":"click_shape","risk_level":"medium","risk_reasons":["NO_SIGNALS","IP_RECENT_FAILURES"],
 "origin":"https://shop.example.com"},"request_id":"..."}
```

`challenge_kind`（invisible / slider / click_text / click_shape / fallback）、`risk_level`（low / medium / high）、`risk_reasons` 是新增字段，告诉业务方这个 token 是怎么拿到的，便于对高风险、fallback 或 `CAPACITY_DEGRADED`（容量不足降级）追加验证。`origin` 是 token 签发时页面的来源（已规范化），应用使用通配或 `*` 时，业务端可以用它核对自己的域名清单。它们不改变通过与否；旧版 SDK 忽略即可。

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
| 限流（每个密钥对的配额，或该 IP 验签失败过多） | 429 | `RATE_LIMITED` | Unknown | 按 Retry-After 用原 request_id 重试 |
| 依赖不可用、内部错误 | 503/5xx | `UNAVAILABLE` | Unknown | 用原 request_id 重试或提示稍后再试 |
| 网络错误、超时、响应体畸形、缺 `success`，或 `success: true` 但 app_key/action 与请求不符 | — | — | Unknown | 同上 |

- 除 Passed 外，所有分类都不得执行业务副作用。Rejected 面向终端用户；CallerError 面向运维告警；Unknown 表示服务端可能已经消费 token，只能用原 request_id 重试，SDK 禁止自动换 request_id。
- 4xx 只出现在调用方可确定修正的情况，不会出现在“token 不对”的情况。这样 SDK 不需要按 reason 列表猜测某个 4xx 是拒绝还是故障，新增 reason 也不会被旧 SDK 误判为异常。SDK 遇到未知 reason 一律按 Rejected 处理。
- 幂等缓存同时保存 Passed 和 Rejected 结果。同 request_id 加完全相同请求的重试返回原结果；认证、应用启停检查在读取幂等结果之前执行，因此吊销密钥对后重试得到 401，而不是缓存的通过结果。
- 无 token 不返回通过。幂等窗口（建议 10 分钟）过期后，重试只会得到 Rejected，不保证重放原来的通过结果；业务必须以自己的幂等记录为准。

业务服务顺序：普通字段校验 → 用户身份/业务授权 → verify → 本地业务幂等事务。若本地事务失败，使用原业务幂等键恢复；不能仅凭已经消费的凭证反复执行副作用。

## 管理后台

管理后台的路由、权限、会话与审计见 `docs/admin.md`。要点：所有写操作 POST，Redis 会话 + CSRF + 同源检查 + 路由权限；菜单过滤不能代替路由权限；secret_key 只在创建 / 轮换后的响应页显示一次；审计与变更同事务写入且脱敏。

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
- 题型与风险：sealed 数据不出现在 payload 明文中，挪到另一个 challenge 上解不开；滑块位置错、匀速直线、等间隔、瞬移、点数过少均为 `ANSWER_INVALID`；失败计入 IP；无信号、无头特征、IP 近期失败会升级题型；fallback 难度 × 8 且独立限流；verify 返回题型与原因码。
