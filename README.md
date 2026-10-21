# BotGuard

自建反机器人验证服务：go-zero + Redis + MySQL，后台使用 Go 服务端模板和 PearAdmin。

已实现：

- gateway：`/v1/challenges`、`/v1/solutions`、`/v1/verify`。包括 SHA-256 多题 PoW、无状态签名 challenge、一次性 token、verify 幂等，以及参考易盾的多应用 + 密钥对 HMAC 签名认证；
- 验证题型与风险：每个 action 可选无感 / 交互（从题型列表随机出题）/ 按风险自适应；交互题型有滑块拼图、旋转对齐、推理拼图、文字点选、语序点选、图标点选、图形计数点选和空间推理共 8 种；浏览器信号与服务端信号做可解释的风险打分，并提供无障碍备用路径（见 [docs/challenge-types.md](docs/challenge-types.md)、[docs/click-challenges.md](docs/click-challenges.md)、[docs/rotate-challenge.md](docs/rotate-challenge.md)、题型调研与选型 [docs/challenge-catalog.md](docs/challenge-catalog.md)）；
- 管理后台 `admin-web`：管理员登录（密码 + 可选 TOTP 动态码）、三种角色、控制台与统计报表、实时事件、应用 / 来源 / 验证策略 / 密钥对管理、题型预览、审计日志（见 [docs/admin.md](docs/admin.md)）；
- 运维命令行 `botguard-ctl`：迁移、应用配置、密钥对、管理员账号的创建与找回；
- 浏览器组件 `packages/browser`：标签、`BotGuard.init()` / `initBotGuard()`（易盾式）、弹出与绑定提交按钮；gateway 同时在 `/sdk/v1/load.js` 托管脚本；
- PHP SDK `packages/php` 与 Laravel 适配包 `packages/laravel`（Facade、`botguard:action` 验证规则、`<x-botguard>` Blade 组件、`botguard()` 辅助函数）；
- Docker 一键演示 `examples/demo`。

尚未实现：Go SDK、自定义角色、多租户隔离。

三个包已发布 0.1.0（MIT）：[`@botguard/browser`](https://www.npmjs.com/package/@botguard/browser)（npm）、[`botguard/php-sdk`](https://packagist.org/packages/botguard/php-sdk) 和 [`botguard/laravel`](https://packagist.org/packages/botguard/laravel)（Packagist）。发布流程见 [docs/release.md](docs/release.md)。

## 先看演示

```sh
examples/demo/start.sh   # 演示站 http://localhost:8080，管理后台 http://localhost:8091（账号和初始密码会打印出来）
```

详见 [examples/demo/README.md](examples/demo/README.md)：在浏览器里完成验证，看 PHP SDK 签名核验的完整请求，并逐个触发 17 种攻击、误用和风险场景。

## 本地运行

需要 Go 1.26.3 或更高版本（当前开发机验证版本），依赖 go-zero v1.10.3。Go 版本与最低 SDK 版本将独立维护。

密钥只生成一次，写进一个只有自己可读的文件，gateway 和 botguard-ctl 都从它加载。KEK 必须两边一致：如果在两个终端里各生成一次，ctl 生成的密钥对在 gateway 上无法解密（ctl 发现库里已有密钥对解不开时会拒绝创建）。

```sh
umask 077
cat > ~/.botguard.env <<ENV
export BOTGUARD_MYSQL_DSN='user:pass@tcp(127.0.0.1:3306)/botguard'
export BOTGUARD_CHALLENGE_KEY_K1='$(openssl rand -base64 32)'
export BOTGUARD_SECRET_KEK='k1:$(openssl rand -base64 32)'
ENV
. ~/.botguard.env

make check
make build
bin/botguard-ctl migrate up   # gateway 在库结构落后时 /readyz 返回 schema_outdated
bin/botguard-ctl admin create -username admin   # 第一个超级管理员，初始密码只显示一次
make gateway  # http://127.0.0.1:8090/readyz
make admin    # 另一个终端，管理后台 http://127.0.0.1:8091
```

管理后台还需要 `BOTGUARD_PUBLIC_ENDPOINT`（gateway 的公开地址，写进接入代码）和可选的 `BOTGUARD_GATEWAY_URL`（用于检查 gateway 状态），见 `configs/admin-web.yaml`。

gateway 需要 Redis（建议 ≥ 7.2）和已迁移的 MySQL（MySQL ≥ 8.0 或 MariaDB ≥ 10.5.2，迁移会检查）；密钥与 DSN 只从环境变量读取，缺失时拒绝启动。DSN 会被自动补上 `parseTime=true`、`loc=UTC`、`time_zone='+00:00'`，并强制关闭 `multiStatements`。管理后台默认只用 HTTPS Cookie；`CookieSecure: false` 只允许 loopback 监听地址。建议只在内网或 VPN 内开放管理后台。

## 接入一个应用

推荐在管理后台操作：「应用管理 → 新建应用」，再在应用详情里添加允许来源、调整验证策略、创建密钥对，「接入代码」标签页给出填好 app_key 的前后端代码。

也可以用命令行。`botguard-ctl` 同样从环境变量读取 `BOTGUARD_MYSQL_DSN` 和 `BOTGUARD_SECRET_KEK`（不接受命令行参数，避免进入 shell 历史）。完整命令见 `bin/botguard-ctl help`。

```sh
make build
bin/botguard-ctl migrate up
bin/botguard-ctl app create -name 商城               # 输出 app_key（前端用，相当于易盾 captchaId）
bin/botguard-ctl origin add -app app_xxx -origin https://shop.example.com
bin/botguard-ctl action set -app app_xxx -action login   # 新建默认 adaptive + 全部题型；-mode invisible|interactive|adaptive，-kinds slider,rotate,click_text,click_shape
bin/botguard-ctl secret create -app app_xxx          # 输出 secret_id + secret_key，secret_key 只显示这一次
bin/botguard-ctl app show -app app_xxx
```

轮换密钥对用 `secret rotate -secret sid_xxx -grace 24h`：立即生成新密钥对，旧的在宽限期结束后失效，期间两组都能用。变更在 gateway 应用缓存过期后生效（默认 10 秒内）。

## 服务器部署

[GitHub Releases](https://github.com/dkeepersun/BotGuard/releases) 提供编译好的 Linux 发布包（amd64 / arm64），解压即可部署，服务器上不需要 Go。`scripts/botguard.sh` 管理 gateway 和 admin-web：启停、迁移、看日志，`install-systemd` 一条命令装成开机自启的 systemd 服务，之后用 `scripts/botguard.sh upgrade` 升级到新版本（源码部署用 `update`）。目录约定、首次部署步骤和注意事项见 [docs/deploy.md](docs/deploy.md)，发版流程见 [docs/release.md](docs/release.md)。

```sh
scripts/botguard.sh install-systemd   # 安装 botguard-gateway / botguard-admin-web 并启动
scripts/botguard.sh status            # 状态、PID 和 /readyz
scripts/botguard.sh upgrade           # 下载最新发布包、校验、迁移并重启
scripts/botguard.sh logs gateway -f
```

## 升级

先迁移，再换 gateway（`scripts/botguard.sh rebuild` 已按这个顺序执行）：

1. 用新版 `botguard-ctl migrate up`。迁移只向前、带 checksum，并用数据库锁防止并发执行。
2. 滚动替换 gateway。新版 gateway 在库结构落后时 `/readyz` 返回 503 `schema_outdated`，不会接流量。

迁移后、旧 gateway 下线前的短暂窗口里，旧 gateway 可能不认识新写入的配置值。例如 0004 之后，旧版会把 `interactive` 当作无感处理。这个窗口应尽量短。新版 gateway 遇到不认识的模式时一律按 adaptive 处理，不会降级成无感。

更换 KEK 时，把旧值放进 `BOTGUARD_SECRET_KEK_PREVIOUS`（见 `configs/gateway.yaml`）。用新 KEK 轮换完所有密钥对后，再移除旧值。

## 测试

`make check` 只需要 Go；`make test-sdk` 运行 PHP SDK 与浏览器组件的测试（需要 php 和 node）。集成测试需要专用的临时 MySQL 库和 Redis 实例，**其中所有表和键都会被清空**：

```sh
export BOTGUARD_TEST_MYSQL_DSN='root@tcp(127.0.0.1:3399)/botguard_test'
export BOTGUARD_TEST_REDIS_ADDR=127.0.0.1:6397   # 可选，用真实 Redis 跑 verify 脚本
make test-integration
```

## 设计与交付

- [架构、PearAdmin 后台模式、目录和实施任务](docs/architecture.md)
- [v1 API 与安全契约草案](api/contract-v1.md)
- [SDK 与发布方案](docs/sdk-integration.md)
- [验证记录与边界](docs/validation.md)

`packages/` 下是三个 SDK 的源码（`@botguard/browser`、`botguard/php-sdk`、`botguard/laravel`），已发布 0.1.0：`npm install @botguard/browser`、`composer require botguard/laravel`（会一并装上 `botguard/php-sdk`）。两个 PHP 包从这里自动同步到只读镜像仓库再进入 Packagist，见 [docs/release.md](docs/release.md)。
