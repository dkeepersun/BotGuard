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
- 本机 MySQL 需要密码，未连接；MySQL 仓储的 SQL 只经过编译，未对真实库执行，迁移也未执行。
- 没有浏览器端实现，因此没有真机求解耗时数据；默认难度（每题 2000 次、8 题）只是占位值。
- 未做压测；限流为固定窗口，窗口边界最多允许 2 倍突发。
