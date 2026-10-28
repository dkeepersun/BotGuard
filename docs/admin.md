# 管理后台（admin-web）

状态：**已实现**。日期：2026-10-21。

管理后台是运营 BotGuard 的主入口，可以替代大部分 `botguard-ctl` 操作。`botguard-ctl` 保留给迁移、创建首个管理员、应急处理等脚本化场景。

## 1. 部署与安全基线

- 独立进程 `cmd/admin-web`，默认 8091 端口，建议只在内网或 VPN 内开放。界面用 Go 服务端渲染（`html/template`）加 PearAdmin（Layui），静态资源全部内嵌，不依赖任何 CDN。
- 配置项：数据库（默认 SQLite，可选 MySQL，见 README）、Redis、`BOTGUARD_SECRET_KEK`（加密新建的密钥对和 TOTP 密钥）、gateway 的公开地址（用于生成接入代码）、会话时长、可信代理、Cookie 是否只走 HTTPS。
- 认证：
  - 用户名 + 密码，密码用 PBKDF2-SHA256（60 万次迭代，16 字节随机盐）存储；
  - 可选 TOTP 二次验证（RFC 6238，30 秒步长，允许前后各 1 步，同一验证码不能重复使用）；
  - 登录失败按“IP”和“用户名”两个维度限流，5 分钟内超过 10 次就锁定，并写入审计。
- 会话：
  - 服务端会话存在 Redis，Cookie 只保存随机 ID 的明文；Cookie 设为 `HttpOnly`、`SameSite=Strict`，HTTPS 部署时加 `Secure`；
  - 会话记录带 HMAC 签名（密钥由 `BOTGUARD_SECRET_KEK` 派生），覆盖记录内容和它在 Redis 里的键：能写 Redis 的人伪造不了会话，也不能把别人的会话挪到自己的 Cookie 下。签名不对的记录按未登录处理。更换 KEK 会让所有人重新登录；没配置 KEK 时会话不签名，启动日志里有提醒；
  - 空闲 30 分钟、最长 8 小时过期；
  - 改密码、停用账号、修改角色时 `session_version` 加 1，该账号已有的会话全部失效。
- 写操作一律是 POST，并且必须同时满足：会话中的 CSRF token 匹配、`Origin` 与后台自身一致、具备相应权限。
- 安全响应头：CSP（只允许同源资源；Layui 需要内联样式，所以允许 `style-src 'unsafe-inline'`；内联脚本带每次请求生成的 nonce）、`X-Frame-Options: SAMEORIGIN`（PearAdmin 用同源 iframe 显示标签页）、`Referrer-Policy: same-origin`、`X-Content-Type-Options: nosniff`。

## 2. 角色与权限

内置三个角色，不提供自定义角色（以后需要时再加 `botguard_role` 系列表）：

| 角色 | 能做什么 |
|---|---|
| 超级管理员 `owner` | 全部操作，包括管理员账号、系统设置和版本更新 |
| 运维 `operator` | 应用、来源、action 策略、密钥对；查看统计、事件与审计 |
| 只读 `viewer` | 查看应用、统计、事件；看不到密钥对操作入口 |

每条路由在注册时声明所需权限；只在菜单上隐藏不算授权。

## 3. 功能

路由与权限一览（每条路由在注册时声明权限，见 `internal/adminweb/server.go`）：

| 路由 | 说明 | 权限 |
|---|---|---|
| `GET /login`、`POST /login`、`POST /logout` | 登录、退出 | 无 / 已登录 |
| `GET /`、`/console`、`/apps`、`/apps/:key`、`/apps/:key/backgrounds/:id`、`/stats`、`/events`、`/preview`、`/system` | 查看页面（`backgrounds/:id` 是上传的背景图） | view（所有角色） |
| `POST /apps/create`、`/apps/:key/update`、`/apps/:key/status`、`/apps/:key/origins/*`、`/apps/:key/actions/*` | 应用、来源、策略 | apps.manage |
| `POST /apps/:key/backgrounds/mode`、`/upload`、`/delete` | 背景来源、上传和删除背景图 | apps.manage |
| `POST /apps/:key/secrets/create`、`/rotate`、`/revoke` | 密钥对 | secrets.manage |
| `GET /audit` | 审计日志 | audit.view |
| `GET /admins`、`POST /admins/*` | 管理员 | admins.manage |
| `POST /system/update-check` | 立即检查新版本 | system.manage（仅超级管理员） |
| `POST /system/upgrade`、`GET /system/upgrade`、`GET /api/upgrade` | 一键升级：提交请求（需再次输入密码）、查看进度 | system.manage（仅超级管理员） |
| `GET /profile`、`POST /profile/*` | 个人设置 | 已登录 |

| 页面 | 内容 |
|---|---|
| 控制台 | 今日与近 24 小时的签发量、通过率、拦截量、verify 通过/拒绝；按题型、风险等级、原因码的分布；按小时的趋势图；各应用排行；gateway 与存储状态 |
| 应用管理 | 应用列表（搜索、状态）、新建、改名、启用/停用；详情页的标签：概览与接入代码、允许来源、action 策略（模式、题型多选、PoW 难度和预估耗时）、密钥对（创建时弹窗只显示一次 secret_key、轮换、吊销）、背景图（见下文「背景图」） |
| 数据统计 | 选择应用、时间范围（24 小时 / 7 天 / 30 天），看趋势与分布 |
| 实时事件 | 最近的 solve 与 verify 事件（每个应用保留最近 2000 条，在 Redis 中）：时间、action、题型、结果、原因码、风险等级、IP 摘要 |
| 运行状况 | 各 gateway 每分钟上报的请求数、限流、503、降级、题库余量、协程数和内存（保留 24 小时）；近 60 分钟趋势；失联实例；异常记录（只记录，不通知）。见 [deploy.md](deploy.md)「运行监控」 |
| 题型预览 | 直接调用服务端生成器，现场生成各题型的样例图，方便配置前直观比较；`/preview?bg=photo` 只用内置照片，`bg=procedural` 只用程序生成 |
| 审计日志 | 所有管理操作（含 `botguard-ctl`）：操作人、操作、对象、脱敏后的变更内容、IP、时间；可按人和操作筛选 |
| 管理员 | 新建、停用、重置密码、修改角色（仅超级管理员） |
| 个人设置 | 修改密码、绑定或解绑 TOTP |
| 系统状态 | 依赖检查、运行配置；版本更新：管理后台和各 gateway 的版本、最新正式版本（见下文「版本更新」） |

### 背景图

应用详情页的「背景图」标签决定所有题型的背景从哪里来，约 10 秒内对新签发的验证生效：

| 设置 | 说明 |
|---|---|
| 出题背景 | 混合（默认，照片与程序生成的图案各占一半）、只用照片、只用程序生成 |
| 使用内置照片 | 默认勾选。内置 57 张 CC0（公有领域）风景照，来源见 `internal/service/imagegen/photos/photos.json` |
| 自定义照片 | 每个应用最多 50 张，JPEG、PNG 或 WebP，至少 640×400，单张不超过 10 MB，一次可以选多张。上传后按 16:10 居中裁切、缩放到 720×450 并重新压缩，EXIF 等元数据不保留；同一张图不能重复上传 |

没有任何照片可用（只用照片但取消了内置照片，又没有上传）时一律用程序生成。

**安全提示**：内置照片随源码公开，攻击者可以拿到全部原图，把题目图片和原图比对来定位缺口和文字。每张照片出题时都会随机裁切、扭曲、调色并叠加纹理，能增加比对的难度，但不能消除（实测见 [challenge-catalog.md](challenge-catalog.md) §8）。安全要求高的应用请上传自己的照片并取消勾选内置照片，照片不要公开在网站上。

上传的照片存在数据库里（`botguard_app_background`），gateway 按应用缓存在内存中，上传或删除后自动重新加载。使用自己背景设置的应用不走预生成题库，每题现场生成（约 3–6 ms）。上传、删除和修改设置都记入审计日志（操作分组「背景图」）。

### 版本更新

管理后台每 `Update.IntervalHours` 小时（默认 6）查一次 GitHub 上的最新正式版本（`api.github.com/repos/dkeepersun/BotGuard/releases/latest`，不含预发布），只取版本号和发布页地址，不发送这台服务器的任何信息。

- 有更新的版本时，超级管理员在控制台看到提示，「系统状态 → 版本更新」显示新版本、发布时间、更新说明链接和各安装方式的升级命令；超级管理员可以点「立即检查」（一分钟内只查一次）。运维和只读角色只能在系统状态页看到版本信息。
- gateway 每分钟上报运行状况时带上自己的版本，系统状态和运行状况页列出各实例的版本；有实例与管理后台版本不同时，控制台提示超级管理员。v0.5.0 及更早的 gateway 不上报版本，显示为“未上报”。管理后台启动后还没有上报过的实例显示为“等待上报”，不算版本不一致：升级会同时重启 gateway 和管理后台，gateway 每分钟才上报一次，在它第一次上报之前，读到的还是重启前那个进程的版本。一两分钟后恢复正常比较。
- 源码编译的开发构建没有正式版本号，不和发布版本比较。
- 服务器不能或不应访问 GitHub 时，在 `admin-web.yaml` 里设 `Update.Check: false`；`Update.URL` 可以改为同一 API 的镜像地址。
- 升级方法见 [deploy.md](deploy.md)「升级」；deb/rpm 和发布包安装可以一键升级（下一节）。

### 一键升级

deb/rpm 包和发布包安装的 Linux 服务器（systemd），「系统状态 → 版本更新」有「升级到 vX.Y.Z」按钮，超级管理员再次输入密码后提交。之后自动完成：下载并校验、替换程序、迁移数据库、重启 gateway 和管理后台，进度页实时显示状态和日志；整个过程通常一分钟以内，期间验证服务短暂不可用。每次提交记一条审计日志（操作「一键升级」）。

管理后台本身不升级：它以 `botguard` 用户运行，没有替换程序和重启服务的权限。分工如下：

1. 管理后台把请求（只有版本号和操作人）写到 `/var/lib/botguard/update-request`；
2. `botguard-update.path` 发现文件后，以 root 启动 `botguard-update.service`，执行 `botguard self-update`；
3. `self-update` 以 `botguard` 用户的身份读取并删除请求，只接受 GitHub 上的最新正式版本、而且比当前版本新的版本号，管理后台被攻破也不能指定旧版本、预发布或其他内容；
4. deb/rpm 安装用 `dnf` / `apt-get` 升级（包管理器校验 GPG 签名，包的安装后脚本负责迁移和重启）；发布包安装用 `botguard upgrade`（校验 `checksums.txt`）；
5. 状态和日志写在 `/var/lib/botguard-update/`（root 所有，`botguard` 组只读），管理后台据此显示进度。服务重启后，进度页自动恢复并显示新版本。

说明：

- 这两个单元由 `botguard install-systemd` 安装（deb/rpm 包安装和升级时会自动执行）；源码、Docker 和 macOS 安装没有一键升级，页面上显示升级命令。不想提供按钮时，在 `admin-web.yaml` 设 `Update.OneClick: false`。
- 按钮只升级管理后台所在的这台服务器；其他运行 gateway 的服务器，在各自服务器上执行升级命令。
- 迁移只能向前，升级后不能退回旧版本。升级失败时进度页显示原因和日志末尾，`/var/lib/botguard-update/update.log` 是完整日志。
- 提交后 2 分钟没有被处理，说明服务器上的 `botguard-update.path` 没有运行，页面会提示。可以用 `sudo systemctl status botguard-update.path` 查看，或重新执行 `sudo botguard install-systemd --user botguard` 恢复。

## 4. 统计数据从哪来

gateway 在签发、solve、verify 时更新 Redis 计数，写入失败不影响主流程，只记日志：

- `bg:stat:{appID}:h:<yyyymmddHH>`：按小时的 hash，字段如 `issue:slider`、`solve_ok:rotate`、`solve_fail:ANSWER_INVALID`、`verify_pass`、`verify_reject:TOKEN_EXPIRED`、`risk:high`、`reason:WEBDRIVER`。保留 32 天。
- `bg:events:{appID}`：最近事件列表（LPUSH + LTRIM 2000），每条是脱敏后的 JSON：IP 只存加盐哈希的前 12 位，不存 token、签名或请求体。

统计是近似值：Redis 写入失败时会少计。不用于计费。

## 5. 审计

`botguard_audit_log` 由迁移 0005 建立。仓储层的写操作在同一个事务里写入审计记录：业务变更提交了，审计一定在；业务变更失败，就不会留下审计。审计记录的操作人：后台操作为 `admin:<用户名>`，命令行操作为 `cli:<系统用户名>`。变更内容会脱敏，`secret_key`、密码、TOTP 密钥永远不写进审计。

审计日志默认保留 180 天，由后台的定期清理删除，见下一节。

## 6. 数据清理

管理后台启动后立即清理一次，之后每 `Cleanup.IntervalMinutes` 分钟（默认 60）一次：

| 数据 | 默认保留 | 配置项 |
|---|---|---|
| 审计日志 | 180 天 | `Cleanup.AuditLogDays` |
| 宽限期已结束或已吊销的密钥对 | 30 天 | `Cleanup.RetiredSecretDays` |

- 天数为 0 表示永久保留；`Cleanup.Enabled: false` 关闭清理。
- 每次最多删除 1000 行，分批进行，不会长时间锁住 SQLite。删除后执行 `PRAGMA incremental_vacuum` 归还空间，并截断 WAL 文件。
- 清理不写审计；有删除时在日志中记一行数量。「系统状态」页显示当前使用的数据库和清理策略。
- 不运行后台的部署，可以用 `botguard-ctl data purge [-audit-days 180] [-secret-days 30]` 配合 cron 定时执行。

## 7. 首次使用

SQLite（默认）不需要迁移，第一次运行任一程序时自动建表；MySQL 先执行 `bin/botguard-ctl migrate up`。

```sh
bin/botguard-ctl admin create -username admin -role owner   # 生成随机初始密码，只显示一次
make admin                                                  # http://127.0.0.1:8091
```

忘记密码：`botguard-ctl admin reset-password -username admin`。这个命令会重新生成密码，并让该账号所有已登录的会话失效。
