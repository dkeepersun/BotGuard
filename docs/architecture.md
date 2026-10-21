# BotGuard 架构与落地计划

日期：2026-10-20。本文区分已确认约束、建议默认值与待实现能力。

## 1. 现场结论

- `/Users/alex/workspace/BotGuard` 初始只有 `.git`，没有提交、源码或未提交业务改动；配置的 origin 为 `https://github.com/dkeepersun/BotGuard.git`。没有核验远程内容，也没有拉取、提交或推送。
- 后台参考 PearAdmin（Pear Admin Layui）的常见 Go SSR 接入方式，本次不修改任何参考代码。
- 参考会话可读取用户需求，但旧助手回答只返回内容引用占位符，无法作为已有完整设计的证据；本文基于本次本地源码与已提供的用户要求重新设计。
- 确认约束：核心运行栈仅 go-zero、Redis、MySQL；后台 Go SSR + PearAdmin；浏览器 npm、Go 和 PHP/Laravel 接入。
- 建议默认：先做单体分层 + 两个 HTTP 入口；一个源码仓库；不引入微服务 RPC、etcd、Kafka、独立搜索或分析数据库。TypeScript/PHP 是交付 SDK 的语言，不是新增服务端基础设施。

## 2. 沿用的 PearAdmin 后台模式

| 参考位置 | BotGuard 采用方式 |
|---|---|
| `AGENTS.md`、`cmd/admin-web/main.go` | main 只装配；业务逻辑、仓储、HTTP 分层 |
| `internal/adminweb/server.go` | 手写路由；每条业务路由声明权限；page/data/action 三类包装 |
| `internal/adminweb/auth_handlers.go` | `/pear-config`、`/menus` 返回 PearAdmin 所需的裸 JSON |
| `internal/adminweb/templates/pages/shell.html` | 顶栏、侧栏、iframe 标签页；不恢复上一管理员的会话标签 |
| `internal/webui/render.go` | `html/template`、嵌入模板、先缓冲再写响应；后续多页面按布局 clone 防止模板块串页 |
| `internal/response/response.go` | 普通业务响应统一 envelope；layui 表格独立 `{code,msg,count,data}` |
| `internal/webui/session.go`、后台 action 包装 | Redis 会话；POST + CSRF + Origin + 权限；成功变更与审计同事务 |
| `internal/webui/static/NOTICE.md` | 本地静态资源随二进制嵌入；保留第三方许可与来源 |

不继承参考项目的业务逻辑、业务 JS、密钥或数据，只保留 PearAdmin 静态资源与布局约定。代码注释英文，界面与错误文案中文。配置不放真实密钥；日志不记录密钥、原始 token、完整请求体。客户端 IP 只信任显式配置的代理。

已实现（见 `docs/admin.md`）：page/action 安全包装（会话、同源、CSRF、路由权限）、Redis 会话、按角色过滤的菜单、审计与变更同事务。

## 3. 部署和模块

```text
浏览器 npm widget ── challenge / solve ──> gateway (go-zero)
       │                                       │
       └─ token + 业务请求 ─> 业务后端 SDK ─ verify ┘
                                               │
                      logic → service → repository
                                      ├─ Redis: 临时状态、限流、消费结果
                                      └─ MySQL: 配置、账户、审计、规则版本
管理员 ──> admin-web (go-zero + html/template + PearAdmin)
```

- `gateway`：公开 challenge/solve；带服务端密钥的 verify。两类接口分开限流和日志策略。
- `admin-web`：独立端口，建议内网/VPN 访问；管理员认证、应用、域名、密钥、规则、事件、权限与审计。
- `botguard-ctl`（已建立）：迁移（带 checksum 与 MySQL `GET_LOCK` 互斥）、应用/来源/action 配置、密钥对（secret_id + secret_key）生成/轮换/吊销。`admin create` 创建首个管理员、`admin reset-password` 找回密码；不提供公共管理员注册。命令行变更以 `cli:<系统用户名>` 写入审计。
- `worker`（P2，确有必要才加）：规则缓存失效重试、过期事件清理、按分钟聚合。同一 Go module，可独立进程；MySQL outbox + Redis 足够。
- `logic` 负责用例与事务，`service` 包含风险决策、PoW、凭证、会话等能力；`repository` 屏蔽 Redis/MySQL；`model` 放生成模型和扩展查询。
- MVP 单区域 Redis 是验证状态权威源；服务多副本不存进程内消费状态。Redis 故障默认不放行。MySQL 配置缓存必须有最大陈旧时间；撤销密钥/应用要有生效 SLA。

## 4. 目录目标与当前落地范围

```text
BotGuard/
├── go.mod / go.sum                 # 服务主 module，已建立
├── cmd/
│   ├── gateway/main.go             # 已建立：challenge / solve / verify / 探针
│   ├── admin-web/main.go           # 管理后台（docs/admin.md）
│   ├── botguard-ctl/               # 已建立，运维命令行
│   └── worker/                     # P2 按需
├── configs/                       # 已建立，无真实凭据
├── internal/
│   ├── config/ handler/ response/  # 已建立
│   ├── adminweb/templates/         # 已建立，PearAdmin shell + console
│   ├── webui/static/               # 已建立，embed 静态资源
│   ├── svc/ logic/ service/        # 已建立：gateway 装配、三个用例、pow / challenge / clientip
│   ├── repository/                 # 已建立：Redis 状态与限流、MySQL 应用只读仓储与缓存
│   ├── migration/                  # 已建立：嵌入式前向迁移执行器 + sql/0001
│   ├── domain/ errx/               # 已建立；obs/ 脱敏观测待实现
│   └── testdb/                     # P1 隔离集成测试
├── api/contract-v1.md              # v1 契约草案；api/fixtures/ 为跨语言 PoW 用例
├── packages/
│   ├── browser/                   # 已建立：<botguard-widget> + Worker 求解（原生 ES 模块 + webpack 4 用的 legacy 构建，npm 已发布 0.1.0）
│   ├── go/                        # 仅说明；P1 独立 Go module
│   ├── php/                       # 已建立：签名 verify 客户端，仅依赖 ext-curl（Packagist 已发布 0.1.0）
│   └── laravel/                   # 已建立：Laravel 适配包（Packagist 已发布 0.1.0）
├── examples/demo/                 # 已建立：Docker 一键演示（浏览器组件 + PHP SDK + gateway）
├── docs/                          # 本次架构、发布、验收文档
└── Makefile                       # 已建立 build / test / check / admin / gateway
```

不预造大量空包，也不把 SDK 的包清单标成已可安装。真正创建 Go 子 module 后，CI 必须显式进入它测试；根 `go test ./...` 不覆盖嵌套 module。

## 5. MySQL 数据模型草案

默认 `utf8mb4`；ID 使用 bigint，外部 app_key 使用不可枚举的随机字符串；时间统一 UTC（连接设置 `time_zone='+00:00'`）。`botguard_app`、`botguard_app_origin`、`botguard_app_action`、`botguard_app_secret` 由 `internal/migration/sql/0001`、`0002` 建立（0002 把 0001 的 site 表改名为 app，并把 Bearer 密钥表改造为密钥对表），迁移执行器见 `internal/migration`；`botguard_admin`、`botguard_audit_log` 由 0005 建立，其余表（策略版本、名单、事件持久化）尚未建立。

| 表 | 主要字段与约束 |
|---|---|
| `botguard_app` | id、app_key UNIQUE（对应易盾 captchaId，公开）、name、status、active_policy_version、timestamps |
| `botguard_app_origin` | id、app_id、origin；UNIQUE(app_id, origin)；精确 scheme+host+port，不允许含糊后缀匹配 |
| `botguard_app_action` | app_id、action、enabled、challenge_mode（invisible / interactive / adaptive）、challenge_kinds（slider、rotate、click_text、click_shape 的逗号列表；0003、0004 加入）、pow_puzzles、pow_difficulty（每题期望尝试次数）；UNIQUE(app_id, action) |
| `botguard_app_secret` | id、app_id、secret_id UNIQUE、secret_ciphertext（AES-256-GCM，附加数据为 secret_id）、kek_id、status、expires_at、rotated_from；secret_key 仅创建时展示一次 |
| `botguard_policy_version` | id、app_id、version、schema_version、rules_json、status、created_by；UNIQUE(app_id, version) |
| `botguard_list_entry` | id、app_id、kind、value_hash 或 CIDR、action、expires_at；固定类型，禁止任意脚本规则 |
| `botguard_verify_event` | id、app_id、request_id、action、result、reason_code、policy_version、created_at；UNIQUE(app_id, request_id)；按应用和时间检索 |
| `botguard_admin` | id、username UNIQUE、password_hash、status、session_version、totp_secret_ciphertext（启用时） |
| `botguard_role` / `botguard_admin_role` / `botguard_role_permission` | 管理员与权限关联，关联键唯一 |
| `botguard_audit_log` | actor、operation、target、脱敏差异、request_id、created_at；变更与成功审计同事务 |
| `botguard_outbox` | 配置缓存失效事件、重试时间、状态；与配置发布同事务 |

MVP 单平台管理多个应用，所有业务查询都必须带 app_id；不声称已有多租户 SaaS。以后加入组织/租户时，先重构授权边界和联合索引。不要先建一套没有隔离测试的租户模型。

默认事件保留 30 天、IP 只存带轮换盐的摘要，是建议值；实际保留期及合规需求待业务确认。审计与风控事件分别设保留策略，不因通用清理任务一起删除。

## 6. Redis 数据与一致性

- challenge 签发无状态：算法/难度、app、action、origin、policy_version、期限写入 HMAC 签名的 challenge 字符串（格式见 `api/contract-v1.md`），签发时不写 per-challenge 记录。建议有效期 120 秒。
- `bg:pow:used:<challenge_id>`：challenge 已提交标记，solve 时 `SET NX EX <剩余有效期>`；单 key 操作，不需要 hash tag。
- `bg:{appID}:token:<sha256(token)>`：证明通过后的随机 token 摘要，绑定 app/action/origin，TTL 60 秒。原始 token 不存日志。
- `bg:{appID}:verify:<requestID>`：规范化请求摘要、消费结果，建议 TTL 10 分钟，服务端认证后才允许读取。
- `bg:{appID}:rate:<dimension>:<bucket>`：应用/IP/action 配额；Lua 原子计数和设置 TTL。每个进程记住 Redis 已报告超限的 key，同一窗口内后续请求直接拒绝，不再访问 Redis；Redis 计数在窗口内只增不减，所以这不会拒绝 Redis 本会放行的请求，多副本也成立。
- `bg:admin:session:<hash>`：管理员 ID、会话版本、CSRF、空闲/绝对过期；建议 30 分钟/8 小时，后续配置化。

为什么签发无状态：`/v1/challenges` 是无需认证的公开接口，也是调用量最大的接口。若每次签发都写 Redis，攻击者只要刷签发就能打满 Redis 内存。改为签名后，签发只更新有界的限流计数；Redis 写入推迟到 solve，且 solve 先限流、先校验签名，伪造或过期的 challenge 不会产生写入。代价是多了一组需要轮换和保护的签名密钥：它由部署配置注入，所有 gateway 副本共享，泄露即 PoW 失效（轮换流程见契约）。

业务服务器认证参考易盾：每个应用有公开的 `app_key` 和一组或多组密钥对（`secret_id` + `secret_key`）。调用 verify 时 secret_key 不出网，请求带 `X-BG-Secret-Id`、时间戳、nonce 和 BG1-HMAC-SHA256 签名（覆盖请求体原始字节）；gateway 校验时间戳窗口（默认 ±300 秒）、验签，验签通过后才在 Redis 记录 `bg:nonce:<secret_id>:<nonce>`（保留 2 倍窗口加时钟偏差）防重放；同一 IP 验签失败过多时，在查密钥之前直接限流。验签要求服务端持有 secret_key，因此用 KEK（`BOTGUARD_SECRET_KEK`，环境变量注入，轮换期间旧 KEK 放在 `BOTGUARD_SECRET_KEK_PREVIOUS`）加密存库；botguard-ctl 创建密钥对前会用当前 KEK 解开库里同一 KEK id 下最新的一个密钥对，解不开就拒绝，防止 ctl 和 gateway 用了不同的 KEK；KEK 不对时返回 503 而不是 401，避免把配置故障误报成调用方签名错误。签名细节见 `api/contract-v1.md`。

Lua 脚本一律用 EVAL 而不是 EVALSHA：go-zero 的 Redis 熔断器会把 `NOSCRIPT` 计为失败，Redis 重启后脚本缓存为空，并发请求会直接把熔断器打开。同理，Redis < 7.2 不支持 `CLIENT SETINFO`，必须在配置中设 `DisableIdentity: true`，否则每次建连的握手错误都会计入熔断。最低建议 Redis 7.2。

solve：校验签名、期限、应用、Origin、限流和 nonces 格式后，先 `SET NX` 标记 challenge 已提交（无论证明对错，每个 challenge 只有一次提交机会），再校验 PoW，通过后写入 token。标记成功但 token 写入失败时返回 503，只会少发不会多发，所以不需要跨 key 原子脚本。verify：先校验服务端密钥与应用/action 绑定，再用 Lua 原子检查原请求结果、检查 token、消费 token、缓存结果（通过与拒绝都缓存）。同应用 `{appID}` hash tag 为未来 Redis Cluster 多 key 脚本准备，但部署初期不要求 Cluster。token 字符串内嵌公开的 site_key，跨站 token 不查 Redis 即可判为 `SITE_MISMATCH`；格式错误和跨站 token 仍经过 verify 脚本写入幂等记录，以便检测同一 request_id 的请求体冲突。token 记录在逻辑过期后继续保留一个幂等窗口，使 verify 能区分 `TOKEN_EXPIRED`/`TOKEN_CONSUMED` 与 `TOKEN_INVALID`。

verify 的业务拒绝（token 无效、过期、已消费、应用/action/Origin 不符）返回 HTTP 200 + `success:false` + reason；非 2xx 只用于调用方错误（400/401/403/409）和服务不可用（429/5xx）。这样 SDK 只看状态码就能区分“拒绝”“配置错误”“结果未知”三类，逐项对照表见契约。

同 request_id + 同规范化请求重试返回原结果；同 request_id + 不同 token/action/业务绑定返回冲突；不同 request_id 消费同 token 只能一个成功。未知网络结果不生成新 request_id。结果缓存过期后不能承诺重放原成功；需返回明确过期/已消费，业务自身保留最终幂等结果。

Redis 异步复制故障切换可能丢失刚写入的消费标记。MVP 不承诺跨故障切换 exactly-once；支付等敏感业务仍必须由业务库唯一键保证副作用幂等。若产品要求故障切换后凭证也绝不二次消费，P2 增加 MySQL 凭证消费唯一记录作为持久权威源，再设计缓存协调，不把 Lua 当作持久一致性保证。

## 7. 风控与防护边界

MVP 使用可解释规则：app/action 启停、精确 Origin 允许列表、可信代理解析的 IP/CIDR 拦截、应用与 IP 限流、失败次数、挑战与消费速率。决策为 allow/challenge/deny；allow 仍签发短期凭证并走后端 verify，不能让前端自行跳过核验。

PoW 采用“多题 + 数值阈值”结构：一个 challenge 含 k 道独立子题（建议默认 8，上限 32），每道子题要求哈希值小于服务端给定的 256 位 target。多题把求解耗时的 p99 从平均的约 4.6 倍（单题）压到约 2.0 倍（k=8），避免少数用户长时间等待；子题还可以分给多个 Worker 并行求解。数值阈值让难度可以任意细调，不受 2 倍一档的限制。

算法由策略按 app/action 选择并写入签名 challenge，客户端不能选择或降级：

- P1 只实现 `sha256-target-v1`。它的局限要明确：原生代码/GPU 计算 SHA-256 比浏览器快 2–3 个数量级，难度又只能按低端手机设定，所以它对有算力的攻击者成本很低，主要价值在于抬高脚本批量请求的门槛。好处是服务端校验几乎零成本。
- `argon2id-target-v1`（内存困难）在 P2 评估，用于缩小攻击者的硬件优势。它的问题是服务端校验每道子题也要付出同等的内存和 CPU，伪造的 solve 会变成对服务端的放大攻击。只有在完成低端真机实测、solve 先限流再廉价校验再哈希、哈希校验有全局并发上限（超限返回 503）、有放大比监控之后，才作为策略对高价值 action 或可疑流量启用，不作为全局默认。

浏览器在 Web Worker 中求解，支持取消和超时；默认 k 与难度在真实低端手机、电池和无障碍场景评估后确定。挑战中所有参数来自服务器签名，客户端回传的副本不被读取。限制 nonce 长度与数量、body、并发、签发速率，先限流再运算。

验证题型与风险信号（第一期已实现）：每个 action 选 invisible / interactive / adaptive 模式并配置交互题型列表（滑块、文字点选、图形计数点选，见 `docs/click-challenges.md`）；浏览器上报环境与交互信号，gateway 结合 IP 近期失败、请求速率、UA 一致性等服务端信号做可解释的规则打分，决定出无感、滑块还是加难度；滑块答案与风险分封存在签名 challenge 的加密字段里，服务端仍无状态；verify 向业务方返回题型、风险等级和原因码。详见 `docs/challenge-types.md`。

PoW 只增加自动化成本，不证明“真人”。MVP 无法替代成熟厂商的设备情报、代理网络识别、行为模型或攻击规模防护。Origin/CORS 是浏览器约束，非浏览器可以伪造 Origin，不作为身份认证。信誉、风险评分和行为采集放在后续阶段，不编造命中率。

## 8. 实施阶段及退出条件

### P0：本次基础

架构、API 草案、SDK 发布边界；go-zero 入口；本地 SSR 预览；明确 health 与 ready；编译、单测、资源可达性。未启用实际防护。

### P1：最小可接入闭环

1. 加隔离测试库与增量迁移、svc、Redis/MySQL repository、配置验证和日志脱敏。
2. 管理员创建/登录/注销、密码安全、Redis 会话撤销、CSRF/Origin、权限路由、审计；登录限流和会话固定攻击测试。
3. 应用、origin、action、密钥对创建/吊销/轮换（已由 botguard-ctl 完成）；规则只先支持名单+限流+按 action 配置的 `sha256-target-v1` 多题 PoW（k 与难度可配）。
4. 无状态签名 challenge（含签名密钥配置与 kid 轮换）→ solve（一次提交标记）→ token → verify；严格版本契约、verify Lua 原子性、按契约的 verify 结果语义与 SDK 分类、缓存失效。
5. browser Web Component、PHP core 与普通 PHP 演示已完成（`examples/demo`）；Go SDK 与 Go 处理器示例待做。
6. CI：根服务、各 SDK、共享契约 fixtures；隔离 MySQL + 真实 Redis 集成测试。部署依赖只有 Redis/MySQL。

退出：两条后端语言接入均成功；无浏览器 secret；过期/篡改签名 challenge/重复提交 challenge/跨应用/跨 action 被拒；签发接口不产生 per-challenge Redis 写入；PoW fixtures 在浏览器与服务端一致；所有 token 类失败均为 200 + `success:false`，SDK 不把拒绝误判为异常；并发消费唯一成功；同请求丢响应恢复；Redis 故障不放行；禁用应用/密钥在规定时间内生效；无管理员越权。

### P2：Laravel 与运营可用

Laravel provider/config/facade/validation rule（消费时机见 SDK 文档）、复杂规则版本管理/回滚、事件检索与汇总、清理 worker、指标、容量/压测、备份恢复、API 密钥与 challenge 签名密钥轮换演练、`argon2id-target-v1` 评估（低端真机耗时、服务端校验并发上限与放大比监控，满足条件后才允许策略启用）、发布镜像和安装验证。根据业务 SLO 决定持久消费记录。

退出：安装后的 Laravel 示例通过；CSRF/会话/应用隔离端到端测试；浏览器兼容与慢设备体验有实测；新旧 SDK 兼容矩阵、故障演练、可回滚部署流程完整。

### P3：依据真实攻击样本演进

自适应难度、挑战升级、可选风险信号、灰度策略和误杀申诉。是否引入外部情报或托管厂商兜底需独立评估，不能作为本阶段既有能力。

## 9. 尚需产品定案的参数

包名与发布账号、首批接入 action、允许域名、流量/延迟目标、事件保留期、SDK PHP/Laravel/浏览器支持矩阵、故障时业务提示。建议默认拒绝验证基础设施故障，不静默 fail-open；这些未定参数不阻碍 P0。
