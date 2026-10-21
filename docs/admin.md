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
  - 空闲 30 分钟、最长 8 小时过期；
  - 改密码、停用账号、修改角色时 `session_version` 加 1，该账号已有的会话全部失效。
- 写操作一律是 POST，并且必须同时满足：会话中的 CSRF token 匹配、`Origin` 与后台自身一致、具备相应权限。
- 安全响应头：CSP（只允许同源资源；Layui 需要内联样式，所以允许 `style-src 'unsafe-inline'`；内联脚本带每次请求生成的 nonce）、`X-Frame-Options: SAMEORIGIN`（PearAdmin 用同源 iframe 显示标签页）、`Referrer-Policy: same-origin`、`X-Content-Type-Options: nosniff`。

## 2. 角色与权限

内置三个角色，不提供自定义角色（以后需要时再加 `botguard_role` 系列表）：

| 角色 | 能做什么 |
|---|---|
| 超级管理员 `owner` | 全部操作，包括管理员账号与系统设置 |
| 运维 `operator` | 应用、来源、action 策略、密钥对；查看统计、事件与审计 |
| 只读 `viewer` | 查看应用、统计、事件；看不到密钥对操作入口 |

每条路由在注册时声明所需权限；只在菜单上隐藏不算授权。

## 3. 功能

路由与权限一览（每条路由在注册时声明权限，见 `internal/adminweb/server.go`）：

| 路由 | 说明 | 权限 |
|---|---|---|
| `GET /login`、`POST /login`、`POST /logout` | 登录、退出 | 无 / 已登录 |
| `GET /`、`/console`、`/apps`、`/apps/:key`、`/stats`、`/events`、`/preview`、`/system` | 查看页面 | view（所有角色） |
| `POST /apps/create`、`/apps/:key/update`、`/apps/:key/status`、`/apps/:key/origins/*`、`/apps/:key/actions/*` | 应用、来源、策略 | apps.manage |
| `POST /apps/:key/secrets/create`、`/rotate`、`/revoke` | 密钥对 | secrets.manage |
| `GET /audit` | 审计日志 | audit.view |
| `GET /admins`、`POST /admins/*` | 管理员 | admins.manage |
| `GET /profile`、`POST /profile/*` | 个人设置 | 已登录 |

| 页面 | 内容 |
|---|---|
| 控制台 | 今日与近 24 小时的签发量、通过率、拦截量、verify 通过/拒绝；按题型、风险等级、原因码的分布；按小时的趋势图；各应用排行；gateway 与存储状态 |
| 应用管理 | 应用列表（搜索、状态）、新建、改名、启用/停用；详情页分四个标签：概览与接入代码、允许来源、action 策略（模式、题型多选、PoW 难度和预估耗时）、密钥对（创建时弹窗只显示一次 secret_key、轮换、吊销） |
| 数据统计 | 选择应用、时间范围（24 小时 / 7 天 / 30 天），看趋势与分布 |
| 实时事件 | 最近的 solve 与 verify 事件（每个应用保留最近 2000 条，在 Redis 中）：时间、action、题型、结果、原因码、风险等级、IP 摘要 |
| 题型预览 | 直接调用服务端生成器，现场生成各题型的样例图，方便配置前直观比较 |
| 审计日志 | 所有管理操作（含 `botguard-ctl`）：操作人、操作、对象、脱敏后的变更内容、IP、时间；可按人和操作筛选 |
| 管理员 | 新建、停用、重置密码、修改角色（仅超级管理员） |
| 个人设置 | 修改密码、绑定或解绑 TOTP |

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
