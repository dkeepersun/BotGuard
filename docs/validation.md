# 本次验证记录

2026-10-20，本地 macOS arm64，Go 1.26.3。

已通过：
- `go vet ./...`。
- `go test ./...`：公开监听地址拒绝、未实现验证时 ready 返回 503、SSR 模板与关键嵌入资源可读。
- `make build`：gateway / admin-web 二进制编译。
- admin-web 实际启动：`/`、`/console`、`/pear-config`、`/menus`、`/healthz` 返回 200；`/readyz` 返回 503；不存在的 `/v1/verify` 返回 404。
- gateway 实际启动：`/healthz` 返回 200，`/readyz` 返回 503；测试进程正常停止。
- 内置浏览器视觉检查：PearAdmin 侧栏、首页标签页、SSR 模块表与“尚未连接 MySQL / Redis，不能用于业务放行”提示可见。

边界：
- 没有连接 MySQL / Redis，也没有修改用户现有数据库。
- 没有实现/测试 PoW、一次性消费、登录/权限、SDK 或生产部署；这些是设计和 P1/P2 任务。
- 旧会话的助手设计正文不可读取，不能声称延续了其未展示的细节。
- PayHub 工作区保持不变。BotGuard 原为空仓库，本次新增均未提交/推送；未创建发布仓库或发布包。
- 预览测试进程在验收后停止；可在 BotGuard 目录使用 `make admin` 重开。

## 2026-10-20：gateway 验证链路

已通过：
- `gofmt -l .` 无输出；`go vet ./...`；`go test ./...`。
- PoW：`api/fixtures/pow-sha256-target-v1.json` 共享用例（含 digest 等于 target、k=1、nonce=0、20 位 nonce、前导零、数量不符）。
- 签名 challenge：kid 轮换（verify-only 可校验、删除后拒绝）、篡改 payload/MAC/kid/版本、超长。
- verify 脚本：通过与重放、他人消费、过期、未知、action/Origin 不符、request_id 冲突、50 并发仅一个成功。另在临时启动的 Redis 8.4.0（独立端口，非本机已有实例）上跑同一批脚本用例通过。
- 逻辑层：一次提交（错误证明后再提交返回 409）、客户端 params 副本被忽略、过期、跨站/跨 Origin、签发后移除 Origin 立即生效、吊销密钥和禁用站点优先于缓存的通过结果、IPv6 /64 限流分桶。
- HTTP 层：预检、`text/plain` 简单请求、CORS 仅对允许来源、四类 verify 响应、Retry-After。
- gateway 进程冒烟（MySQL 不可达）：`/healthz` 200，`/readyz` 503，签发与 verify 均为 503 `UNAVAILABLE`，未放行；缺少签名密钥时拒绝启动；日志中无 token 或凭据，go-zero 已去掉 DSN 中的账号密码。

边界：
- 本机 MySQL 需要密码，未连接；MySQL 仓储的 SQL 只经过编译，未对真实库执行，迁移也未执行。（已由下一节的临时 MySQL 验证补上。）
- 没有浏览器端实现，因此没有真机求解耗时数据；默认难度（每题 2000 次、8 题）只是占位值。
- 未做压测；限流为固定窗口，窗口边界最多允许 2 倍突发。

## 2026-10-20：botguard-ctl 与 MySQL 集成

环境：在临时目录另起的 MySQL 8.4.9（端口 3399，独立数据目录）和 Redis 8.4.0（端口 6397），用完即关；未连接或修改本机已有的 MySQL/Redis。

已通过：
- `BOTGUARD_TEST_MYSQL_DSN=… BOTGUARD_TEST_REDIS_ADDR=… go test -p 1 -count=1 ./...` 全部通过；不设环境变量时 5 个集成测试跳过，`make check` 通过。
- 迁移：重复执行无副作用；已执行文件被修改（checksum 不一致）、程序版本旧于数据库、乱序版本均报错。
- botguard-ctl：全部命令在真实库上执行；来源规范化（`HTTPS://A.Example:443` → `https://a.example`）、重复来源与带路径来源被拒绝；action 只修改显式给出的参数；轮换把旧密钥的剩余有效期缩短到宽限期；已吊销密钥不能轮换；`site show` 不输出任何 secret 或摘要；时间按 UTC 存取。
- 端到端（真实 MySQL 仓储 + miniredis）：签发读取库中的 PoW 参数；轮换宽限期内新旧密钥都可用，过期后旧密钥 401；移除来源后已签发 token 返回 `ORIGIN_NOT_ALLOWED`；停用站点 403；吊销密钥 401；带缓存时吊销在 TTL 后生效。
- 真实进程冒烟：ctl 建站点与密钥 → gateway `/readyz` 200 → curl 签发、求解、核验通过 → 同一 request_id 重放结果一致 → 换 request_id 得到 `TOKEN_CONSUMED` → 吊销后 10 秒内变为 401。gateway 日志中没有 token 或 secret。

边界：
- 尚未在你提供的 MySQL 开发库上执行。
- 迁移中途失败时，已执行的 DDL 不会回滚（MySQL 的 DDL 隐式提交），需要人工处理后重跑。
- 命令行变更没有审计记录；也没有“创建首个管理员”命令，要等管理后台切片。

## 2026-10-20：多应用签名认证与 Docker 演示

改动：site 全面改名为 app（迁移 0002 改表名与列名）；verify 认证由 Bearer 改为参考易盾的 secret_id + HMAC-SHA256 签名（时间戳 ±300 秒、nonce 防重放），secret_key 用 KEK 以 AES-256-GCM 加密存库；新增 PHP SDK、浏览器组件和 `examples/demo`。

已通过：
- 临时 MySQL 8.4.9 与 Redis 8.4.0 上 `go test -p 1 ./...` 全部通过，含：从版本 1 升级到 0002（数据保留、旧 Bearer 密钥被吊销）；签名篡改、错误密钥、时间戳超窗、nonce 重放、格式错误；错误 KEK 返回 503 并确认库中不存明文。
- `php packages/php/tests/run.php`：25 项检查，含与 Go 共用的签名用例。
- `node --test packages/browser/test/`：11 项，浏览器求解结果与 Go 参考实现逐个一致。
- Docker 演示（`examples/demo/start.sh`）：迁移 0001→0002、两个演示应用初始化成功；内置浏览器中组件 8 个 Worker 约 0.1 秒完成 23,038 次哈希；正常登录通过；11 个攻击/误用场景结果全部与预期一致；浏览器控制台无错误。
- gateway 关闭了 go-zero 默认的 SQL 语句日志，避免把来自未认证请求的参数写进日志。

边界：
- 只在 macOS 的 Chromium 内核内置浏览器验证过组件，未测 Safari/Firefox 和低端手机，默认难度仍是占位值。
- “脚本自动求解”场景表明：这个难度下脚本 8 毫秒就能拿到真 token。PoW 只提高成本，必须配合限流和风险规则。
- 尚未在你的 MySQL 开发库上执行。
