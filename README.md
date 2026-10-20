# BotGuard

自建反机器人验证服务：go-zero + Redis + MySQL，后台使用 Go 服务端模板和 PearAdmin。

**当前还不是可交付的防护产品。** gateway 已实现 `/v1/challenges`、`/v1/solutions`、`/v1/verify`（SHA-256 多题 PoW、无状态签名 challenge、一次性 token、verify 幂等），`/readyz` 在 Redis 与 MySQL 都可用时才返回 200。尚未实现：管理后台认证与站点/密钥管理、迁移执行器（`botguard-ctl`）、浏览器/Go/PHP SDK、审计与事件记录。在 `botguard-ctl` 完成前，站点和密钥只能手工写入 MySQL。

## 本地运行

需要 Go 1.26.3 或更高版本（当前开发机验证版本），依赖 go-zero v1.10.3。Go 版本与最低 SDK 版本将独立维护。

```sh
make check
make build
make admin    # http://127.0.0.1:8091
# 在另一个终端：
export BOTGUARD_MYSQL_DSN='user:pass@tcp(127.0.0.1:3306)/botguard?parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27'
export BOTGUARD_CHALLENGE_KEY_K1="$(openssl rand -base64 32)"
make gateway  # http://127.0.0.1:8090/readyz
```

gateway 需要 Redis（建议 ≥ 7.2）和已执行 `internal/migration/sql/` 的 MySQL；密钥与 DSN 只从环境变量读取，缺失时拒绝启动。admin-web 仍是只读预览，只接受 loopback 监听地址，不能经反向代理公开。

可选的真实 Redis 测试（会清空该实例，务必使用专用的临时实例）：

```sh
BOTGUARD_TEST_REDIS_ADDR=127.0.0.1:6397 go test ./internal/repository/ -count=1
```

## 设计与交付

- [架构、PayHub 对照、目录和实施任务](docs/architecture.md)
- [v1 API 与安全契约草案](api/contract-v1.md)
- [SDK 与发布方案](docs/sdk-integration.md)
- [验证记录与边界](docs/validation.md)

`packages/` 是 SDK 源码边界的预留目录，其中说明文件不是可安装 SDK。包名均为提案，尚未注册或发布。
