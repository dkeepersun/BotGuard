# BotGuard

自建反机器人验证服务：go-zero + Redis + MySQL，后台使用 Go 服务端模板和 PearAdmin。

**当前还不是可交付的防护产品。** gateway 已实现 `/v1/challenges`、`/v1/solutions`、`/v1/verify`（SHA-256 多题 PoW、无状态签名 challenge、一次性 token、verify 幂等），`/readyz` 在 Redis 与 MySQL 都可用时才返回 200。运维命令行 `botguard-ctl` 已能执行迁移、管理站点/来源/action、生成/轮换/吊销服务端密钥。尚未实现：管理后台认证与网页管理、浏览器/Go/PHP SDK、审计与事件记录（命令行的变更目前也没有审计记录）。

## 本地运行

需要 Go 1.26.3 或更高版本（当前开发机验证版本），依赖 go-zero v1.10.3。Go 版本与最低 SDK 版本将独立维护。

```sh
make check
make build
make admin    # http://127.0.0.1:8091
# 在另一个终端：
export BOTGUARD_MYSQL_DSN='user:pass@tcp(127.0.0.1:3306)/botguard'
export BOTGUARD_CHALLENGE_KEY_K1="$(openssl rand -base64 32)"
make gateway  # http://127.0.0.1:8090/readyz
```

gateway 需要 Redis（建议 ≥ 7.2）和已迁移的 MySQL；密钥与 DSN 只从环境变量读取，缺失时拒绝启动。DSN 会被自动补上 `parseTime=true`、`loc=UTC`、`time_zone='+00:00'`，并强制关闭 `multiStatements`。admin-web 仍是只读预览，只接受 loopback 监听地址，不能经反向代理公开。

## 用 botguard-ctl 接入一个站点

`botguard-ctl` 同样从 `BOTGUARD_MYSQL_DSN` 读取连接（不接受命令行参数，避免进入 shell 历史）。完整命令见 `bin/botguard-ctl help`。

```sh
make build
bin/botguard-ctl migrate up
bin/botguard-ctl site create -name 商城              # 输出 site_key
bin/botguard-ctl origin add -site pk_xxx -origin https://shop.example.com
bin/botguard-ctl action set -site pk_xxx -action login
bin/botguard-ctl key create -site pk_xxx             # secret 只显示这一次
bin/botguard-ctl site show -site pk_xxx
```

轮换密钥用 `key rotate -key sk_xxx -grace 24h`：立即生成新密钥，旧密钥在宽限期结束后失效，期间两把都能用。变更在 gateway 站点缓存过期后生效（默认 10 秒内）。

## 测试

`make check` 只需要 Go。集成测试需要专用的临时 MySQL 库和 Redis 实例，**其中所有表和键都会被清空**：

```sh
export BOTGUARD_TEST_MYSQL_DSN='root@tcp(127.0.0.1:3399)/botguard_test'
export BOTGUARD_TEST_REDIS_ADDR=127.0.0.1:6397   # 可选，用真实 Redis 跑 verify 脚本
make test-integration
```

## 设计与交付

- [架构、PayHub 对照、目录和实施任务](docs/architecture.md)
- [v1 API 与安全契约草案](api/contract-v1.md)
- [SDK 与发布方案](docs/sdk-integration.md)
- [验证记录与边界](docs/validation.md)

`packages/` 是 SDK 源码边界的预留目录，其中说明文件不是可安装 SDK。包名均为提案，尚未注册或发布。
