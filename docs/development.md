# 开发指南

本文面向在本机修改和调试 BotGuard 的开发者。部署到服务器请看 [install.md](install.md)。

## 演示环境

只需要 Docker：

```sh
examples/demo/start.sh   # 演示站 http://localhost:8080，管理后台 http://localhost:8091（账号和初始密码会打印出来）
```

详见 [examples/demo/README.md](../examples/demo/README.md)：在浏览器里完成验证，查看 PHP SDK 签名核验的完整请求，并逐个触发 17 种攻击、误用和风险场景。

## 本地运行

需要 Go（版本见 `go.mod`，当前为 1.26.3）和 Redis。数据库默认是内嵌的 SQLite：第一次启动时自动创建 `data/botguard.db` 并建表，不需要安装 MySQL，也不需要手动迁移。

密钥只生成一次，写进一个只有自己可读的文件，gateway、管理后台和 botguard-ctl 都从它加载。三者的 KEK 必须一致：如果在两个终端里各生成一次，ctl 创建的密钥对在 gateway 上无法解密（ctl 发现库里已有密钥对解不开时会拒绝创建）。

```sh
umask 077
cat > ~/.botguard.env <<ENV
export BOTGUARD_DB_DSN="\$HOME/botguard/data/botguard.db"   # 可选；不设时为当前目录下的 data/botguard.db
export BOTGUARD_CHALLENGE_KEY_K1='$(openssl rand -base64 32)'
export BOTGUARD_SECRET_KEK='k1:$(openssl rand -base64 32)'
export BOTGUARD_PUBLIC_ENDPOINT='http://127.0.0.1:8090'
ENV
. ~/.botguard.env

make check
make build
bin/botguard-ctl admin create -username admin   # 第一个超级管理员，初始密码只显示一次
make gateway  # http://127.0.0.1:8090/readyz
make admin    # 另一个终端，管理后台 http://127.0.0.1:8091
```

`BOTGUARD_PUBLIC_ENDPOINT` 是 gateway 的公开地址，会写进接入代码；可选的 `BOTGUARD_GATEWAY_URL` 供管理后台检查 gateway 状态，见 `configs/admin-web.yaml`。

密钥和数据库连接只从环境变量读取，密钥缺失时拒绝启动。管理后台默认只发送 HTTPS Cookie；`CookieSecure: false` 只允许在 loopback 监听地址上使用。

## 数据库：SQLite 或 MySQL

| 环境变量 | 说明 |
|---|---|
| `BOTGUARD_DB_DRIVER` | `sqlite`（默认）或 `mysql` |
| `BOTGUARD_DB_DSN` | SQLite 文件路径（默认 `data/botguard.db`，相对于工作目录），或 MySQL DSN |
| `BOTGUARD_MYSQL_DSN` | 旧变量，仍然有效：没有设置 `BOTGUARD_DB_DRIVER` 时，设了它就继续使用 MySQL，老部署升级后不会被换成空的 SQLite |

- **SQLite**（默认）：纯 Go 驱动，不依赖 CGO 和系统库。gateway、管理后台、botguard-ctl 启动时自动建表和迁移（多个进程同时启动也安全）。文件权限 0600，开启 WAL，gateway 读和后台写互不阻塞。三个程序必须指向**同一个文件**，生产环境请使用绝对路径。适合单机部署。
- **MySQL**（MySQL ≥ 8.0 或 MariaDB ≥ 10.5.2，迁移时会检查）：`BOTGUARD_DB_DRIVER=mysql`，`BOTGUARD_DB_DSN='user:pass@tcp(127.0.0.1:3306)/botguard'`。启动前执行 `bin/botguard-ctl migrate up`；库结构落后时 gateway 的 `/readyz` 返回 `schema_outdated`。DSN 会被自动补上 `parseTime=true`、`loc=UTC`、`time_zone='+00:00'`，并强制关闭 `multiStatements`。适合多台机器共用配置。

两种数据库之间没有自动的数据迁移工具；已有的 MySQL 部署继续使用 MySQL 即可。

## 长期运行：自动清理

服务按无人值守长期运行设计，数据和日志都不会无限增长：

- **数据库**：管理后台每小时清理一次（`configs/admin-web.yaml` 的 `Cleanup`），删除 180 天前的审计日志，以及宽限期结束或吊销满 30 天的密钥对；SQLite 随后回收空闲页、截断 WAL。天数设为 0 表示永久保留。没有运行管理后台时，可以用 `botguard-ctl data purge` 配合 cron 清理。gateway 不做清理，因为它可能只有只读的数据库账号。
- **Redis**：无需清理。token、限流、会话等每个键都有过期时间，每小时统计保留 32 天，实时事件每个应用最多保留 2000 条。
- **日志**：默认写入 `logs/gateway`、`logs/admin-web`，单个文件超过 100 MB 就切分并压缩，7 天前的自动删除（`configs/*.yaml` 的 `Log`）。安装脚本生成的服务器配置改为 `Mode: console`，由 journald 收集和轮转。

## 用命令行管理应用

一般在管理后台操作即可。需要脚本化时可以用 `botguard-ctl`，它同样从环境变量读取数据库配置和 `BOTGUARD_SECRET_KEK`（不接受命令行参数，避免进入 shell 历史）。服务器上用 `sudo botguard ctl ...`，会自动加载 `/etc/botguard/botguard.env`。完整命令见 `botguard-ctl help`。

```sh
bin/botguard-ctl app create -name 商城               # 输出 app_key（前端用，相当于易盾 captchaId）
bin/botguard-ctl origin add -app app_xxx -origin https://shop.example.com
bin/botguard-ctl action set -app app_xxx -action login   # 默认 adaptive + 全部题型；-mode invisible|interactive|adaptive，-kinds slider,rotate,click_text,click_shape
bin/botguard-ctl secret create -app app_xxx          # 输出 secret_id + secret_key，secret_key 只显示这一次
bin/botguard-ctl app show -app app_xxx
```

轮换密钥对用 `secret rotate -secret sid_xxx -grace 24h`：立即生成新密钥对，旧的在宽限期结束后失效，期间两组都可以使用。变更在 gateway 的应用缓存过期后生效（默认 10 秒内）。

## 测试

`make check` 只需要 Go：数据库集成测试默认在临时 SQLite 文件上运行，Redis 使用 miniredis。`make test-sdk` 运行 PHP SDK 与浏览器组件的测试（需要 php 和 node）。

要在 MySQL 和真实 Redis 上再跑一遍，需要专用的临时库和实例，**其中所有表和键都会被清空**：

```sh
export BOTGUARD_TEST_MYSQL_DSN='root@tcp(127.0.0.1:3399)/botguard_test'
export BOTGUARD_TEST_REDIS_ADDR=127.0.0.1:6397   # 可选，用真实 Redis 跑 verify 脚本
make test-integration
```

安装脚本、deb 包和 Docker 镜像的端到端测试在 CI 的 `install` 任务中运行（`.github/workflows/ci.yml`）。本机检查打包结果：`scripts/release.sh v0.0.0-dev linux/amd64`（装了 nfpm 时同时生成 deb 和 rpm）。

## 设计文档

- [架构、PearAdmin 后台模式、目录和实施任务](architecture.md)
- [v1 API 与安全契约](../api/contract-v1.md)
- [SDK 与发布方案](sdk-integration.md)、[发布流程](release.md)
- [验证题型](challenge-types.md)、[点选题型](click-challenges.md)、[旋转题型](rotate-challenge.md)、[题型调研与选型](challenge-catalog.md)
- [管理后台](admin.md)
- [验证记录与边界](validation.md)
