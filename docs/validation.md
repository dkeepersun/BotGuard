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
- 迁移 0005（`sys_admin`、`sys_audit_log`、`bg_app.remark`）；后台和命令行的所有变更都与审计记录同事务写入。
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
