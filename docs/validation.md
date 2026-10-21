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
- 参考代码保持不变。BotGuard 原为空仓库，本次新增均未提交/推送；未创建发布仓库或发布包。
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

## 2026-10-20：第一期（验证题型框架、风险信号、滑块）

已通过：
- 临时 MySQL 8.4.9 与 Redis 8.4.0 上 `go test -p 1 ./...` 全部通过（16 个包），含迁移 0003、`action set -mode`、新建 action 默认 adaptive、修改时保留原模式。
- 新增单测：签名 challenge 的加密字段（往返、密钥轮换、挪到别的 challenge 失败、payload 不含答案）；风险规则逐条与组合；滑块生成（固定种子可复现、50 个种子的答案范围与图片可解码）；轨迹规则（人类样例通过，匀速、等间隔、瞬移、直线、过快、点数过少、终点不一致被拒，键盘路径单独处理）；自适应选题、IP 失败升级、fallback 难度与独立限流、不可信事件与键盘输入计入风险。
- `go test -bench . ./internal/service/slider`：Apple M4 Pro 上单张拼图约 2.1 毫秒，因此未做预生成题库，只设并发上限。
- `php packages/php/tests/run.php`：27 项；`node --test packages/browser/test/`：16 项（含信号采集）。
- Docker 演示与内置浏览器实测：
  - 登录（adaptive）对正常浏览器出无感题，verify 返回 `invisible/low`；
  - 注册（slider）正常显示拼图和假缺口；
  - 浏览器工具的机器拖动（4 点 / 23 毫秒）被拒，失败后自动换图；
  - 键盘方向键完成拼图通过；
  - fallback 路径难度 ×8 且通过，verify 带 `FALLBACK_CHOSEN`；
  - 演示站 5 个新场景全部符合预期，其中“仿人轨迹”通过，属于如实展示的局限。
- 实测中发现并修复：
  - 页面加载时采集环境信号导致内置 WebView 误报 `ZERO_OUTER`，改为点击时采集；
  - 滑块题的 PoW 用时把等待用户拖动的时间也算了进去；
  - 键盘路径可被脚本冒用，改为加 `KEYBOARD_INPUT` 原因码。

边界：
- **真人用鼠标和手指拖动尚未实测**，轨迹阈值是按经验和合成样例定的，需要你在演示站上实际拖几次，并在手机上试，再据此调整。
- 只在 Chromium 内核的内置浏览器中验证过，未测 Safari、Firefox、微信内置浏览器。
- 风险信号与权重未经真实流量校准。

## 2026-10-20：第二期（点选题型与随机出题）

已通过：
- 临时 MySQL 8.4.9 与 Redis 8.4.0 上 `go test -p 1 ./...` 全部通过（19 个包），含：迁移 0004 把第一期的 `slider` 模式转为 `interactive` + `[slider]`；`-kinds` 校验；新建 action 默认 adaptive + 全部题型。
- 新增单测：点选规则（正确、顺序错、点偏、读题过快、点击过快、等间隔、瞬移、触屏放宽）；选题（interactive 能选到列表中每一种；高风险选列表中最难的；fallback）；点选 solve 全流程；挑战 payload 中不含目标坐标。
- 生成器：图形点选 300 个种子、文字点选 200 个种子，均满足目标在图内、互不重叠、描述唯一（图形）或字在字表内（文字）；字表无重复、无形近字。单张生成约 2.4 毫秒（图形）和 3.8 毫秒（文字）。
- `php packages/php/tests/run.php`：27 项；`node --test packages/browser/test/`：16 项。
- Docker 演示与内置浏览器实测：发帖（文字点选）按顺序点 4 个字通过；支付（图形点选）数清点数后点 3 个图形通过；“脚本随机点击图形题”被拒；业务端分别看到 `click_text`、`click_shape`。实测发现小号骰子的点太小，已改为骰子一律大号、点半径至少 3 像素。

边界：
- 真人（尤其手机）点选未实测；点击行为规则未经真实流量校准。
- 文字点选已有专门的识别工具和打码服务；图形点选目前对通用多模态大模型最难，但可以被专门训练的模型攻破。两者都只提高成本。

## 2026-10-20：SDK 易盾式用法与发布前安装验证

改动：
- 浏览器组件新增 `BotGuard.init()` / `initBotGuard(config, onload, onerror)`（对应易盾的 `initNECaptcha`）、`mode="popup"`、`bindTo`（点击提交按钮时验证，对应易盾的 bind 模式）、Promise 形式的 `verify()`；新增经典脚本 `load.js`（对应 `load.min.js`）；gateway 在 `/sdk/v1/` 托管这些脚本并返回 CORS 头。
- 修复跨域部署的问题：从 gateway 加载脚本时，浏览器不允许跨域创建 Worker。现在会改用同源 blob Worker；CSP 禁止 blob 时退回主线程计算。
- PHP SDK 新增 `verifyToken($token, $action)`；新增 Laravel 包 `botguard/laravel`（自动发现、Facade、`botguard:action` 规则、Rule 对象、`<x-botguard>` 组件、`botguard()` 辅助函数，按请求作用域缓存，避免同一请求重复消耗 token）。
- 发布准备：`package.json` 去掉 private 并补全元数据；两个 `composer.json` 补全元数据和 branch-alias；新增 CI、npm 发布、PHP 镜像拆分三个 GitHub Actions 工作流；新增 `docs/release.md`。

发布前验证（均在临时目录中进行，未提交进仓库）：
- **npm**：`npm pack` 打出 `botguard-browser-0.1.0.tgz`（7 个文件，15.9 kB），装进全新的 npm 项目；子路径导出 `@botguard/browser/solver`、`@botguard/browser/signals` 可以正常导入。
- **Composer + Laravel**：`composer create-project laravel/laravel` 新建 Laravel 13.37 项目；以 path 仓库（复制模式，不用软链接）安装 `botguard/laravel` 和 `botguard/php-sdk`，依赖 `^0.1` 通过 branch-alias 正确解析，ServiceProvider 被自动发现。
- **浏览器端到端**：测试站在 `127.0.0.1:8081`，gateway 在 `localhost:8090`，属于跨域。

  | 方式 | 结果 |
  |---|---|
  | Blade `<x-botguard action="pay">` + 规则 `botguard:pay` | 通过，`click_shape / low` |
  | 弹出模式绑定提交按钮 + `BotGuardToken` 规则对象 | 验证后自动提交并通过，`click_text / low` |
  | 易盾式 `load.js` + `initBotGuard` + `botguard()->passes()` | 通过，`invisible / low` |
  | npm 包文件自托管 + `BotGuard.init()` + `BotGuard::verify()` | 通过，`slider / medium`（键盘完成，带 `KEYBOARD_INPUT`） |

  跨域时 trace 显示 `workers: 8`，blob Worker 方案生效；浏览器控制台无错误。
- **反例**：
  - 空 token → `TOKEN_INVALID`；伪造 token → 验证规则失败；
  - 错误 secret_key → `passes()` 返回 false 并写错误日志，`verify()` 抛 `CallerErrorException 401 UNAUTHORIZED`；
  - 这一步发现 Facade 会缓存实例，在 Octane 等常驻进程下可能跨请求复用，已改为不缓存。
- **Laravel 9 兼容性**：只读检查了 yihuipay（Laravel 9.52.22）的 vendor，包依赖的 `scoped()`、Facade 缓存开关、Rule 接口、`@once` 都存在。没有在 Laravel 9 上实际运行。
- 测试用的 8081 来源已从演示应用中移除。

边界：
- **三个包都还没发布**，`npm install @botguard/browser` 和 `composer require botguard/laravel` 目前都会失败。发布需要你完成 `docs/release.md` 里的账号和令牌步骤；发布后再在干净环境里做一次真实安装验证。
- 许可证尚未确定（目前为 `UNLICENSED` / `proprietary`），见 `docs/release.md`。
- 用打包工具（Vite、webpack）打包浏览器组件的情况未验证。

## 2026-10-20：组件尺寸定制（H5 / App）

改动：
- 组件宽度、图片最大高度、紧凑模式、主色都可以定制，方式有三种：属性（`width`、`image-height`、`size="compact"`、`accent`）、`init()` 选项、CSS 变量（`--botguard-width` 等）。
- 图片按比例缩放，最小高度 100px。
- 弹出层改为不使用 transform 的居中方式（原方式与抖动动画冲突）；高度不超过可视区，内容放不下时内部滚动；避开安全区；短屏时图片高度不超过屏幕高度的 38%。
- Laravel 的 `<x-botguard>` 组件可以透传这些参数；演示站新增“显示方式”切换。

内置浏览器实测：
- 375×812，默认：图形点选面板 319×313。
- 375×812，紧凑：入口框高 36px；面板 319×229；图片 192×120，比例正确；骰子点数在 120px 高度下仍能数清。
- 375×560，弹出层（滑块）：面板完整位于可视区内，图片 329×165。
- 812×375，横屏弹出层（图形点选）：面板高 268px，完整显示、无需滚动，图片自动缩到 143px 高。
- 紧凑模式下的滑块：图片与滑轨同宽（240px）且左边缘对齐，键盘移动时拼图块与滑块位置一致。

未验证：真机（iOS Safari、Android Chrome、微信内置浏览器、各 App 的 WebView）。

## 2026-10-21：整体审查与缺陷修复

四路代码审查（gateway、浏览器组件、PHP/Laravel SDK、运维与文档）共发现约 50 条问题。每条都先复现或核对代码，确认后修复并补测试。

**浏览器组件**
- 修复了一轮验证的生命周期问题：上一轮的超时会杀掉新一轮的 Worker；`verify()` 在 reset、元素移除、冷却期间永不返回；元素移除后插回会卡死；bindTo 双击会提交两次，第二次 token 为空。
- 客户端时钟偏快时 token 一拿到就过期，改为以服务端新增的 `expires_in` 计时。
- 高回报率鼠标的拖动轨迹满 400 点后，结尾被截掉，服务端判为 END_MISMATCH。
- bindTo 改在表单 submit 阶段拦截，覆盖回车和其他提交按钮。
- 弹窗模式补上加载、出错的界面反馈和焦点管理；pointercancel 后复位滑块；处理多指针。
- 打包工具能识别 Worker；SSR 中 import 不再报错；CSP 下优先用 adoptedStyleSheets。

**PHP / Laravel**
- `botguard` 规则和 `BotGuardToken` 改为隐式规则：少写 `required` 不再绕过验证。
- 非法 UTF-8、数组、null 形式的 token 不再导致 500，在本地判为 TOKEN_INVALID。
- 超时为 0 时不再无限等待。
- 除 429 外的 3xx/4xx 归为调用方错误，不重试。
- 未配置时 `passes()` 返回 false 并记日志。
- Octane 下解析当前请求的容器。
- 文档纠正了“规则放最后就不会消耗 token”的错误说法。

**gateway**
- `pointer_type` 只认四种取值，其余一律按鼠标规则校验；触摸输入在 verify 中带 `TOUCH_INPUT`。
- “challenge 已用”标记的有效期补上时钟偏差余量，多副本下不会被重复兑换。
- 同一 IP 验签失败过多时，在查密钥之前返回 429；未命中的缓存单独存放。
- 限流、challenge 过期等提前返回的错误也带 CORS 头。
- 关闭 go-zero 的熔断和 MaxBytes 中间件：请求体超限返回 JSON 400，不再是空的 413。
- 高风险用户选 fallback 时，难度倍数改为相乘。
- 遇到未知模式时按 adaptive 处理，不再降级为无感。
- 校验 `MaxDifficulty`，并要求 DSN 带库名。

**运维**
- `/readyz` 检查库结构版本，落后时返回 `schema_outdated`。
- 迁移前检查 MySQL ≥ 8.0 / MariaDB ≥ 10.5.2；checksum 不受 CRLF 影响。
- botguard-ctl 的错误改写到 stderr；创建密钥对前检查 KEK 与库中已有密钥对是否一致；轮换时显示真实的失效时间；`-mode slider` 与 `-kinds` 同时给出时报冲突；题型去重。
- demo 的 seed、start 脚本在失败时不再静默。
- npm 发布改用 Trusted Publishing。

**结果**
- `make check` 通过。
- 临时 MySQL 8.4.9、Redis 8.4.0 上 `go test -race -p 1 ./...` 全部通过，确认 MySQL 用例实际执行、没有被跳过。
- PHP 97 项、浏览器 45 项通过。浏览器新增的行为测试放到旧代码上跑，26 项中 22 项失败，说明这些测试能抓到原来的缺陷。
- 重建 demo 后用真实浏览器验证：
  - 无感登录 → PHP SDK 核验通过；
  - 弹窗滑块：焦点进入对话框，Esc 关闭后焦点回到按钮；
  - 全部攻击与误用场景结果符合预期；
  - 黑盒探测：超限请求体返回 JSON 400；随机 secret_id 第 61 次起返回 429；限流和坏 challenge 的响应对允许的来源带 CORS。
- 本次修好的 demo 场景：「脚本自动求解 PoW」原来不带信号，被 NO_SIGNALS 升级成交互题，没有演示出预期效果。现在脚本伪造一份干净信号；如果 IP 风险已经升高，会说明原因。
- Laravel 13 应用跨源实测：
  - `load.js + botguard()->passes()` 通过；
  - `popup bind + BotGuardToken` 在双击后只提交一次，通过 fallback 自动续交并核验通过。

**仍未验证**
- 真人在真机上（iOS Safari、安卓、1000Hz 鼠标）拖动滑块和点选。
- Node 22 上运行浏览器测试（本机为 Node 26，CI 用 Node 22）。
- Laravel 9–12 和 PHP 8.1 的实际运行：CI 新增了 PHP 8.1 / 8.4 矩阵，但还没有推送跑过。

## 2026-10-21：管理后台与旋转对齐题型

**新增内容**
- 旋转对齐题型（`rotate`）：服务端程序生成画面和圆盘，答案（旋转角）加密封存在签名 challenge 中；组件把拖动距离换算成旋转角，复用滑块的轨迹校验和键盘路径。
- 管理后台：
  - 账号与安全：管理员登录（PBKDF2 密码、可选 TOTP 动态码，二维码由自带的 QR 编码器生成）、三种角色、Redis 会话（空闲与绝对超时）、CSRF、同源检查、CSP（带 nonce）、登录失败锁定；
  - 页面：控制台、统计报表、实时事件、应用管理（来源、验证策略、密钥对、接入代码）、题型预览、审计日志、管理员、个人设置、系统状态。
- gateway 统计：按小时写入 Redis 计数和最近事件。
- 迁移 0005（`botguard_admin`、`botguard_audit_log`、`botguard_app.remark`）；后台和命令行的所有变更都与审计记录同事务写入。
- 命令行：新增 `botguard-ctl admin create|list|reset-password|enable|disable`。

**结果**
- 自动化测试：
  - `make check` 通过；临时 MySQL 8.4.9、Redis 8.4.0 上 `go test -race -p 1 ./...` 24 个包全部通过（race 模式下跳过旋转题出图耗时断言，race 下约慢 12 倍）。
  - PHP 97 项、浏览器 46 项通过。
- 新增测试：
  - 旋转题：按答案转回后与原图的平均色差约 5，偏 25° 时约 25；出图约 6ms；解题流程覆盖容差内与容差外、键盘路径及 `KEYBOARD_INPUT` 标记；组件测试覆盖旋转角、ARIA、与滑块切换。
  - 统计：各阶段计数、事件列表、IP 只记录加盐哈希。
  - 后台逻辑：登录、会话失效、TOTP 绑定与防重放、锁定、最后一个超级管理员保护、审计不含密钥。
  - 后台 HTTP 端到端：强制改密码、所有页面渲染、CSRF 与跨站拒绝、只读角色的权限边界、密钥只显示一次、审计记录。
  - 重复保存相同的策略：不产生新策略版本，也不写审计。
- QR 编码器：版本 1–10 的样例（含真实的 otpauth 链接），以及后台页面截图里的二维码，都用 macOS CoreImage 解码还原成功。
- Docker 演示加入管理后台，用真实浏览器检查：
  - 登录与首次强制改密码；改密码失败时回到原页面并提示（修复了失败后跳转到 POST 地址导致白页的问题）；
  - 控制台数据、策略编辑弹窗、题型预览（显示答案）、实时事件、审计详情；
  - 演示站「评论 · 旋转对齐」标签页：用方向键转到对齐后验证通过，PHP 端核验通过；
  - 后台把 login 改为只出旋转题后，约 10 秒内 gateway 就按新配置签发旋转题；
  - 攻击场景全部符合预期。

**仍未验证**
- 真人在真机上完成旋转题和其他题型的通过率与耗时，以及用手机身份验证器扫码绑定（二维码已用 CoreImage 验证可解码）。
- 旋转题能被沿圆周做边缘匹配的脚本解出：上面的浏览器验证就是这样求出答案的。这一局限已写进设计文档，题型只作为随机出题池中的一种。

### 补充验证与改进（同日）

- **动态码改为两步登录**：
  - 密码正确后，服务端签发 5 分钟有效的一次性登录凭据；第二步只输入动态码，不用重新输入密码。
  - 每张凭据最多试 5 次；输错仍计入用户名和 IP 的锁定次数。
  - 新增测试覆盖：错误码保留凭据、凭据只能用一次、伪造凭据被拒、第 6 次尝试失效。
- **真实浏览器中走完动态码全流程**：用一份独立的 Python RFC 6238 实现充当手机身份验证器（这次没有用真手机）。依次验证了：绑定、只输密码时进入第二步、绑定时用过的动态码被拒、下一个动态码登录成功（运维角色）。
- **手机尺寸（375×812，模拟）**：
  - 演示站紧凑模式下的旋转题渲染正常。原先拖动提示在窄轨道里被截断，已缩短为「向右拖动，转正圆盘 / 补全拼图」。
  - 自动化工具产生的瞬时拖动被服务端以 TOO_FEW_POINTS 拒绝，并自动换题；这是预期的防护效果。
  - 网关日志改为记录实际题型，不再一律写 slider。
- **管理后台的手机与平板尺寸**：原来缺少 PearAdmin 小屏用的悬浮菜单按钮，侧栏打不开；已补上，现在侧栏可以展开和收起，内容区为单列布局。
- **演示站**：新增 `examples/demo/reset-admin.sh`，重置 admin 密码，并同步更新 start.sh 打印的密码。
- **回归**：`make check` 通过；`go test -race -p 1 ./...` 全部通过；PHP 97 项、浏览器 46 项通过。

**仍未验证**：真人在真机上的操作（真实触屏拖动和点选、真实手机上的身份验证器 App）。

## 2026-10-21：yihuipay + yihui-h5 接入模拟

目的：在真正对接前，用 yihuipay（Laravel 9 / PHP 8.2 后端）和 yihui-h5（uni-app Vue 2，HBuilderX 工程）按正式安装方式跑通一次，连接自己搭建的 gateway。两个仓库各用 git worktree 新开 `sim/botguard` 分支修改，数据库用 `yihuipay` 的副本，不影响原项目。

**安装方式**
- Composer：把 `packages/php`、`packages/laravel` 打成带版本号的 zip，放进本地 artifact 仓库，`composer require botguard/laravel:^0.1`。按 Packagist 的 dist 包安装，lock 只新增这两个包，原有依赖不变。
- npm：在本地 Verdaccio 发布 `@botguard/browser@0.1.0`，yihui-h5 用 `npm install @botguard/browser` 安装。
- H5 构建：本机没有 HBuilderX，改用 uni-app 官方 Vue 2 CLI（vue-cli 4.5 / webpack 4.47，与 HBuilderX 的 Vue 2 编译器同代）构建生产包；构建前把 `config.service.js` 的域名全部替换成本地地址，确保请求不会打到生产环境。

**发现并修复的问题**
- webpack 4 找不到 `@botguard/browser`：包只在 `exports` 里声明入口，webpack 4 不认这个字段。
- 改用完整路径引入后，webpack 4 又解析不了 `import.meta.url`（用于定位 Worker）。
- 修复：Worker 的启动逻辑移到 `src/spawn.js`；发布时用 esbuild 额外生成 `dist/botguard.legacy.js`（ES2017，Worker 代码以 blob 方式内联），`main` / `module` 字段指向它。Vite、webpack 5 和 gateway 托管仍使用原来的源码，行为不变。新增 4 项测试，其中一项在 Node 的 vm 里运行内联的 Worker 代码，结果与 Go 共用的 PoW 用例一致。

**结果（真实浏览器，375×812 模拟手机）**
- adaptive 模式：正常浏览器无感通过，登录成功进入首页；gateway 事件记录 issue → solve → verify pass。
- 改为 interactive（滑块）：弹出层在 uni-app 页面中完整显示；点 × 关闭后静默返回，不提示错误，也不发登录请求。
- Worker：legacy 版在真实浏览器中以 blob Worker 运行（8 个 Worker，约 1.6 万次哈希用时 56 ms）；从 gateway 跨域加载 `load.js` 的方式在新增 `spawn.js` 后同样正常。
- 后端用真实 token 直接调用 `/api/login`：
  - 密码错误时 token 也会被消耗（验证码先于密码校验，和易盾一致；前端每次提交前都重新验证）；
  - 重放同一个 token 被拒，gateway 记为 TOKEN_CONSUMED；
  - 用 register 的 token 登录被拒，记为 ACTION_MISMATCH；
  - 篡改的 token 被拒，记为 TOKEN_INVALID；
  - 新 token 加正确密码，登录成功。
- 切换开关：易盾 / BotGuard / 迁移期两者均可，三种模式按预期分流（由 gateway 事件确认是否真的调用了 BotGuard）；gateway 不可达时拒绝登录，并写 `UnknownResultException` 错误日志。
- 回归：浏览器 50 项、PHP 97 项、`go test ./...` 通过。

**仍未验证**
- HBuilderX 本身的编译（本机没有安装），以及 HBuilderX 内置的 uView 样式；CLI 构建里部分 uView 样式和 HBuilderX 不一致，与 BotGuard 无关。
- App 端（APP-PLUS）：仍使用易盾原生插件。组件依赖 DOM，不能在 uni-app App 的逻辑层运行，需要另行方案（renderjs 或 web-view 页面）。
- 真人在真机上拖动滑块：本次弹出层只验证到显示和关闭。

### 真机 H5 测试（同日）

- **局域网 HTTPS 测试模式**：新增 `examples/demo/lan.sh`，用 Caddy 本机自签证书把演示站（与 gateway 同源，8443）和管理后台（8444）开放到局域网，并把 HTTPS 来源加入演示应用。
  - 修复：开启后，电脑上 localhost 页面的验证也会被导到局域网入口。现在只有经 HTTPS 代理访问的请求才使用局域网地址。
  - 新增：代理的访问日志（只记录路径、状态码和 User-Agent）。
- **实时事件新增「设备 / 输入」**：从请求头的 User-Agent 归纳出粗粒度的平台和浏览器，例如「iPhone · Chrome」，不记录版本号和完整 UA。
- **iPhone（iOS 27）+ Chrome，真实触屏**，事件记录的输入方式都是 `touch`：
  - 滑块、旋转对齐、文字点选、图形点选、无感验证都通过；
  - 旋转对齐、图形点选提交到业务后端后，服务端核验通过，风险原因带 `TOUCH_INPUT`；
  - 文字点选前两次 `POSITION` 未通过：手指点偏，紧凑布局下目标只有约 12 CSS 像素。已把触摸输入的命中容差从 +4 放宽到 +9 图像像素，并补了测试。
- **后台动态码（局域网 HTTPS 入口）**：
  - iPhone 用 admin 登录成功，并打开了绑定页（17:04–17:05）。
  - 绑定确认和用动态码登录是在 Mac 浏览器里完成的（17:07），动态码由身份验证器扫码生成，服务端校验通过。记录里看不出是哪个 App 生成的动态码。
  - 发现并修复：审计的脱敏规则把「是否使用了动态码」字段也过滤掉了，登录记录的详情只剩 `{}`。现在改为记录 `mfa` 字段；详情为空时不再写 `{}`。
- **演示便利**：新增 `botguard-ctl admin set-password`，新密码从环境变量读取，用弱密码必须加 `-allow-weak`。演示环境的 admin 默认密码为 123456，`reset-admin.sh` 可以恢复成这个值。正式环境的密码强度要求不变。

## 2026-10-21：第三期题型（推理拼图、语序点选、图标点选、空间推理）

- 调研与选型见 `docs/challenge-catalog.md`：整理了国内外常见题型和 Open CaptchaWorld（NeurIPS 2025）的结论，选出 4 种可以完全程序生成的题型。
- **实现**：
  - 推理拼图 `internal/service/jigsaw`：图片切成 2×4 块，其中两块被交换；校验交换的两块、轨迹的起止块，以及与滑块相同的轨迹规则。
  - 语序点选 `clicktext.GenerateOrder`：内置 78 个成语，补入 137 个字，字形图集扩到 473 字，仍然排除形近字和旋转后会认错的字。
  - 图标点选 `internal/service/clickicon`：14 种程序绘制的图标，测试保证它们的剪影两两不相似；提示是一条图标小图。
  - 空间推理 `internal/service/spatial`：图形摆在 4×3 网格里，测试保证被描述的图形唯一、所指方向上紧挨着的格子里只有一个图形。
  - 迁移 0006 把题型列表字段放宽到 255 字符。
- **测试**：
  - Go 新增生成与校验测试，`-race` 下 27 个包全部通过；浏览器组件测试 52 项通过（新增拖动交换、图标提示条）。
  - 后台题型预览打开「显示答案」后，逐一核对了 8 种题型的答案位置。
  - 演示站「分享 · 推理拼图」：页面脚本按“接缝色差最小”找出被交换的两块，拖动交换后验证通过，服务端核验也通过。这同时说明这类题能被接缝分析解出，与选型文档写的局限一致。

### 浏览器人工完成（同日）

- 在 Mac 浏览器里用真实的鼠标移动和点击完成了语序点选、图标点选、空间推理，三题都提交到业务后端，服务端核验通过（签到、领券、投票）。
- 过程中被拒的两次都是脚本化操作造成的，属于预期的拦截：
  - 语序点选第一次被拒，原因 `TOO_FAST`：点击间隔小于 150 ms；
  - 图标点选第一次被拒，原因 `EQUAL_GAPS`：自动化的点击间隔几乎相同。
- 经局域网 HTTPS 入口（8443）又做了一轮四种新题型（17:44–17:45），四题都通过，服务端核验也都通过。记录显示这一轮用的是 Mac 上的 Chrome 和鼠标，不是手机：代理日志里这段时间只有 Mac Chrome 的 User-Agent，iPhone 最后一次访问是 17:05。推理拼图先有两次选错了要交换的两块（`PAIR`），第三次通过，拖动都是真人操作。
- **iPhone（iOS 27）+ Chrome，真实触屏**（17:47–17:48）：推理拼图、语序点选、图标点选、空间推理都一次通过，四次提交服务端核验都通过，事件记录的输入方式是 `touch`，设备「iPhone · Chrome」，风险原因带 `TOUCH_INPUT`。
  - 发现并修复：推理拼图那次核验多了一条 `CLICK_WITHOUT_MOVE`（点击前无鼠标移动）。原因是 iOS WebKit 在轻触后合成的 click 事件，pointerType 是空的或写成 mouse，组件就把「点击进行人机验证」当成了鼠标点击。现在组件同时记录最近一次可信 pointerdown 的类型，1 秒内是 touch 或 pen 就以它为准；脚本派发的事件不算。已补测试，浏览器组件测试 53 项通过。

## 2026-10-21：uni-app App 端（renderjs）接入模拟

接着 yihuipay + yihui-h5 的模拟，补上 App 端。

**问题**：App 的页面逻辑在没有 DOM 的逻辑层，组件只能放进 renderjs（页面 WebView）。WebView 是 `file://` 页面，请求带 `Origin: null`，gateway 拒绝；放行 `null` 又等于允许任何网页的沙箱 iframe 冒用 app_key。

**方案**
- gateway：允许来源可以登记 `app://包名`。只有请求不带 Origin 时才读 `X-BotGuard-Origin` 头；App 来源的响应不带 CORS 头。浏览器 POST 一定带 Origin（`null` 也算），所以网页无法冒充 App。
- 组件：新增 `transport` 选项（元素属性同名），设置后用它代替 `fetch` 发请求。
- yihui-h5：新增 `components/botguard-app`。组件在 renderjs 里显示和计算，请求经 `callMethod` 交回逻辑层，用 `uni.request` 发出并加上 `X-BotGuard-Origin: app://com.ehuipay.app`，结果再通过 prop 送回 renderjs。服务端切到 BotGuard 后，App 端不再弹易盾。

**验证**（单独起了一个 gateway 实例：本机 MySQL 的 `botguard_appsim` 库 + 独立 Redis 容器，端口 18090，不影响演示环境）
- 单元测试：
  - `NormalizeOrigin` 接受 `app://包名`，拒绝空 id、带路径、带端口、连续点号、带账号的写法；
  - handler 测试覆盖：`Origin: null` 或第三方 Origin 加 `X-BotGuard-Origin`、未登记的包名、两个头都没有，都会被拒；challenge 仍绑定签发时的来源；App 来源能完成 issue → solve → verify，且响应不带 CORS 头；
  - 组件的 `transport` 新增 4 项测试。浏览器 57 项通过，`go test ./...` 通过。
- 用 Node 模拟 App 逻辑层（不带 Origin，只带 `X-BotGuard-Origin`）：签发和求解通过，用拿到的 token 登录 yihuipay 成功；`Origin: null`、`null` 或第三方 Origin 加该头、未登记的包名、两个头都没有，都返回 403 `ORIGIN_NOT_ALLOWED`。
- 用 webpack 4 构建 `app-plus`，检查两层的分工：组件、blob Worker、renderjs 部分只在视图层 `app-view.js`，且没有 `import.meta`；`X-BotGuard-Origin` 和包名只在逻辑层 `app-service.js`。Node 26 移除了 `util.isRegExp`，构建时用预加载脚本补回（HBuilderX 自带旧版 Node，不受影响）。
- renderjs 中转链路在浏览器里跑通（H5 同样支持 renderjs；只在构建外壳里让 H5 也渲染 botguard-app）：
  - 拦截记录显示，gateway 的两个请求都由 `uni.request`（XHR）发出，组件自己的 `fetch` 一次没用；
  - 无感通过后登录成功；
  - 改为滑块后，弹出层在 renderjs 中完整显示；点 × 关闭后静默返回，不发登录请求。

**仍未验证**
- 真机 App。本机没有 HBuilderX，离线 SDK 需要 DCloud 账号申请的 AppKey，所以没有在模拟器上跑。需要在真机上确认三点：
  - `file://` WebView 里的 `crypto.subtle`：Android WebView 视为安全上下文，iOS 需确认；
  - blob Worker 能否启动：不能时退回主线程计算，较慢但能完成；
  - `uni.request` 确实不带 Origin。
- 真人在 App 里拖动滑块。

## 2026-10-22：首次发布与发布后验证

三个包发布 0.1.0（MIT）：`@botguard/browser` 发到 npm，`botguard/php-sdk`、`botguard/laravel` 发到 Packagist（由 split-php 工作流同步到镜像仓库，标签 `php-v0.1.0` / `laravel-v0.1.0` 在镜像上成为 `v0.1.0`）。推送后 CI 全部通过（主任务、PHP 8.1、PHP 8.4）。

**发布后验证**（都在干净的临时目录中进行，验证结束后删除）
- npm：`npm install @botguard/browser` 从 registry.npmjs.org 装上 0.1.0。
  - 包内容：LICENSE、README、`src/`、`dist/botguard.legacy.js`；
  - 解析：`exports` 指向源码，`main` / `module` 指向 legacy 构建，`/legacy` 子路径可以导入；
  - Node 中 import 不报错，`init()` 给出“只能在浏览器中调用”。
- Composer：
  - 先把两个镜像仓库作为 VCS 仓库装了一次，确认标签与依赖关系；
  - 收录到 Packagist 后，新建 Laravel 13.38 项目执行 `composer require botguard/laravel`。lock 中两个包的 notification-url 都是 packagist.org，压缩包来自镜像仓库的 v0.1.0；清掉缓存重装也成功；
  - 两个镜像仓库都已装好 Packagist 的 webhook。
- 端到端：
  - 单独起了一个 gateway（基于发布提交 4c25d82，独立的 Redis 容器和数据库），Laravel 页面用 `<x-botguard action="login" />`；
  - 在浏览器中点击后无感通过（约 1.7 万次哈希，0.03 秒），提交后控制器 `BotGuard::verify()` 返回 passed，风险等级 low；
  - 伪造的 token 返回 `TOKEN_INVALID`，`passes(null)` 返回 false，`botguard:login` 规则判定失败。
