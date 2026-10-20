# 第一期设计：验证类型框架、无感风险信号、滑块验证

状态：**已实现**（实现记录见第 12 节）。日期：2026-10-20。

## 1. 背景与目标

现在只有一种验证方式：浏览器在后台算 PoW。它有两个已经确认的问题：

- AI 代理操作的是真浏览器，PoW 照样会在浏览器里自动算完；
- 脚本不经浏览器也能算，演示里 8 毫秒就拿到了真 token。

调研结论（见上一轮评估）：任何图形题型都挡不住 AI，只能提高成本。研究中通用 AI 代理在交互式验证码上的通过率最高 40%，专门的求解器在 26 类验证码上平均 60.7%，人工打码平台则什么题都能解。业界真正有效的是**分层**：先在后台采集风险信号，低风险直接放行，有风险再出题，风险越高题越难。

第一期目标：

1. **验证类型框架**：每个应用的每个 action 可以选择验证方式（无感 / 滑块 / 按风险自适应），以后加文字点选、旋转等题型时，不再改协议骨架。
2. **无感风险信号**：浏览器环境信号、交互信号和服务端信号合成一个可解释的风险分，决定出什么题。
3. **滑块验证**：服务端生成拼图，前端拖动，服务端同时校验位置和拖动轨迹。
4. **业务方接入不变**：`/v1/verify`、PHP SDK、yihuipay 的接入代码都不用改。verify 的响应只**新增**风险字段，旧 SDK 会忽略它们。

非目标：

- 不宣称能防住 AI，文档和演示都如实说明局限；
- 不引入机器学习模型，风险规则全部可解释、可调；
- 文字点选、旋转、图标点选留到第二期以后；
- 不做设备指纹的长期存储。

## 2. 核心概念

| 概念 | 说明 |
|---|---|
| 题型（kind） | `invisible`（只有后台 PoW，即现状）、`slider`（滑块）、`fallback`（无障碍备用路径，见 6.5）。以后扩展 `click_text`、`rotate` 等 |
| 模式（mode） | action 级配置：`invisible` 固定无感；`slider` 固定滑块；`adaptive` 按风险决定 |
| 风险分（risk score） | 0–100，由多条规则加权得出，每条命中的规则都有原因码 |
| 风险等级 | `low` < 30 ≤ `medium` < 70 ≤ `high`（阈值可配置） |

**所有题型都附带 PoW。** 滑块题在用户拖动的同时，浏览器在后台算 PoW，提交时两者都要正确。这样即使滑块被自动化解掉，每次请求仍有计算成本。

`adaptive` 模式的决策表（第一期）：

| 风险等级 | 出题 | PoW 难度 |
|---|---|---|
| low | invisible | action 配置值 |
| medium | slider | action 配置值 |
| high | slider | action 配置值 × 4 |

**第一期不做“直接拒绝”**（已确认）。误判会把真实用户挡在门外，而高风险用户仍需解出更难的题。等有真实流量数据后再评估是否加入按 action 开启的硬拒绝。

## 3. 整体流程

```text
浏览器                                       gateway
  │ 页面加载：组件开始采集环境/交互信号
  │ 用户点击组件
  │ POST /v1/challenges {app_key, action, signals} ─▶ 规则打分（环境 + 交互 + 服务端信号）
  │                                                  按 mode 和风险等级选题型
  │ ◀── {kind, PoW 参数, slider 图片?, challenge(含加密答案)}
  │ 后台 Worker 算 PoW；若是滑块，同时展示拼图
  │ 用户拖动滑块（记录轨迹）
  │ POST /v1/solutions {challenge, nonces, answer, signals} ─▶ 校验签名、PoW、滑块位置、轨迹
  │                                                  更新风险分，签发 token（记录题型和风险）
  │ ◀── {token}
  │ 表单提交 token ─▶ 业务服务器 ─▶ POST /v1/verify（不变）
  │                                ◀── {success, ..., challenge_kind, risk_level, risk_reasons}
```

## 4. 协议变化（全部为新增字段，向后兼容）

### POST /v1/challenges

请求新增：

- `signals`：可选对象，序列化后最大 4 KiB；缺失视为“没有信号”，会计入风险。字段见第 5 节。
- `fallback`：可选布尔值，表示用户选择了无障碍备用路径，见 6.5。

响应新增：

```json
{
  "kind": "slider",
  "challenge": "v1.k1....",
  "challenge_id": "...",
  "algorithm": "sha256-target-v1",
  "params": {"puzzles": 8, "target": "..."},
  "expires_at": 1790000120,
  "slider": {
    "width": 320, "height": 160,
    "background": "data:image/jpeg;base64,...",
    "piece": "data:image/png;base64,...",
    "piece_y": 47, "piece_size": 56
  }
}
```

- 图片直接内联在响应里（背景 JPEG 约 15–25 KB，拼块 PNG 约 3–5 KB），不需要额外的图片接口，也就不需要另存图片状态。
- 风险分和原因码**不返回给浏览器**，避免告诉攻击者哪条信号暴露了它。

### POST /v1/solutions

请求新增：

```json
{
  "answer": {"x": 183.5, "trajectory": [[0, 0, 0], [16, 3, 0], [33, 9, 1]]},
  "signals": {"...": "交互信号，见第 5 节"}
}
```

- `answer` 只在滑块题时必填。`x` 是拼块左边缘在原图坐标系中的位置（组件按显示缩放换算回原图像素）；`trajectory` 每项为 `[自按下起的毫秒数, x, y]`，最多 400 个点。
- 请求体上限从 8 KiB 提高到 32 KiB，只对 solutions 接口放宽。

新增的失败结果：

| HTTP | code | 说明 |
|---|---|---|
| 422 | `ANSWER_INVALID` | 滑块位置不对，**或**轨迹被判定为非人类。两种情况故意不区分，不告诉攻击者是哪一项没过；具体原因只写服务端日志 |

与现在一样，一个 challenge 只能提交一次，失败后组件重新获取新题。

### POST /v1/verify（业务服务器）

请求不变。通过时的响应新增：

```json
{"success": true, "app_key": "...", "action": "login", "verified_at": 1790000030,
 "challenge_kind": "slider", "risk_level": "medium", "risk_reasons": ["NO_POINTER_BEFORE_CLICK", "IP_RECENT_FAILURES"]}
```

业务方可以据此做二次决策，例如高风险登录再要求短信验证。业务服务器是受信方，可以看到原因码。PHP SDK 的 `VerifyResult` 增加对应的只读属性；旧版 SDK 会忽略这些字段，照常工作。

## 5. 风险信号

### 5.1 采集什么

**浏览器环境信号**（组件加载时采集，只上报结论，不上报原始指纹）：

| 信号 | 含义 |
|---|---|
| `webdriver` | `navigator.webdriver` 为 true |
| `headless_ua` | UA 含 HeadlessChrome 等 |
| `software_gl` | WebGL 渲染器是 SwiftShader、llvmpipe 等软件渲染，常见于无头浏览器和虚拟机 |
| `zero_outer` | `outerWidth/outerHeight` 为 0 |
| `no_plugins` / `no_languages` | 插件列表或语言列表为空 |
| `ua_platform_mismatch` | UA 声称的平台与 `navigator.platform`、触屏能力矛盾 |
| `permission_mismatch` | 通知权限的两种查询方式结果矛盾（无头浏览器的已知特征） |
| `automation_globals` | 存在常见自动化框架注入的全局变量 |

**交互信号**（从页面加载到点击、到拖动结束）：

| 信号 | 含义 |
|---|---|
| `ms_to_click` | 页面加载到点击组件的时间 |
| `pointer_moves_before_click` | 点击前的指针移动次数 |
| `click_without_move` | 点击前没有任何指针移动或键盘焦点变化 |
| `untrusted_event` | 事件 `isTrusted` 为 false（脚本伪造的事件） |
| `pointer_type` | mouse / touch / pen / keyboard |

**服务端信号**（攻击者无法伪造）：

| 信号 | 含义 |
|---|---|
| `IP_RECENT_FAILURES` | 该 IP 最近 10 分钟内 solve 失败的次数 |
| `IP_HIGH_RATE` | 该 IP 签发速率接近限流阈值 |
| `UA_HEADER_MISMATCH` | HTTP 头里的 UA 与 JS 上报的 UA 不一致 |
| `NO_SIGNALS` | 请求没有带 signals（多半是脚本直接调接口） |

### 5.2 怎么打分

每条规则有固定权重，命中即累加，上限 100。示例：`webdriver` +60、`software_gl` +25、`click_without_move` +20、`NO_SIGNALS` +40、`IP_RECENT_FAILURES` 每次 +10（最多 +30）。权重写在代码的规则表里，用测试锁定，网关配置可以整体覆盖阈值。

### 5.3 诚实的边界

- 浏览器上报的信号都**可以伪造**：熟练的攻击者会把 `webdriver` 改成 false、模拟指针移动。它们的作用是让低成本的自动化先暴露，迫使攻击者投入更多工夫；真正难伪造的是服务端信号。
- 信号只用于本次判定：不落库、不做跨站追踪，原始指纹不离开浏览器。原因码会随 token 记录在 Redis 中，与 token 一起过期。若以后要落库做事件分析，需单独评估个人信息合规（PIPL）。

## 6. 滑块验证

### 6.1 图片生成

- **背景**：第一期用程序随机生成，不用照片。做法是多层随机多边形、圆和渐变色块叠加噪声纹理，让拼块缺口的边缘混在大量干扰边缘里。这样没有图片版权问题，题目也不会重复。管理员上传自定义背景图留到管理后台阶段。
- **拼块**：56×56 左右的拼图形状，四边随机凸凹；y 坐标随机。
- **干扰**：
  - 背景上除真缺口外，再挖一个颜色更淡的假缺口；
  - 背景按 JPEG 有损压缩（质量随机 60–80），增加压缩噪声；
  - 拼块边缘加随机描边和阴影，让模板匹配更难。
- **实现**：只用 Go 标准库（`image`、`image/draw`、`image/jpeg`、`image/png`），不加第三方依赖。

### 6.2 答案保存（保持无状态）

正确的 x 坐标写进签名 challenge 的新字段 `sealed_answer`：

- 用 AES-256-GCM 加密，`challenge_id` 作为附加数据；
- 加密密钥由 challenge 签名密钥经 HKDF 派生（标准库 `crypto/hkdf`），不需要新增配置；
- 浏览器看得到这段密文，但解不开。

### 6.3 校验

solve 时按从便宜到贵的顺序校验：签名 → 期限 → 应用 / Origin → 限流 → 格式 → 标记“已提交一次” → PoW → 滑块位置 → 轨迹规则。

- **位置**：`|x - 答案| ≤ 4` 像素（原图坐标）。
- **轨迹规则**（命中任一即判为 `ANSWER_INVALID`，并计入该 IP 的失败次数）：

| 规则 | 判定 |
|---|---|
| 太快 / 太慢 | 总时长 < 300 毫秒或 > 30 秒 |
| 点太少 | 少于 8 个采样点 |
| 瞬移 | 相邻两点位移超过总距离的 50% |
| 匀速 | 速度的变异系数接近 0（人手先加速后减速） |
| 等间隔 | 采样时间间隔几乎完全相等（程序生成的特征） |
| 纯直线 | y 方向完全没有抖动 |
| 终点不一致 | 轨迹最后一点与提交的 x 相差过大 |

- 用键盘方向键完成的滑块，单独走一套宽松规则（不要求抖动和加减速），以照顾无障碍场景。

### 6.4 防止刷题耗 CPU

签发接口不需要认证，生成图片要耗 CPU。措施：

1. 先过签发限流和风险打分，确定要出滑块后才生成图片；
2. 用 benchmark 实测单张耗时。若超过 5 毫秒，就改为后台协程预生成题库：每道题只用一次，题库空了就降级为“invisible + 高难度 PoW”并告警；
3. 全局并发上限，超过返回 503，而不是排队。

### 6.5 无障碍备用路径（已确认采用）

滑块面板下方提供“无法完成拼图？”入口，点击后组件重新请求 challenge，并带上 `fallback: true`：

- gateway 返回 `kind: "fallback"`：不出图形题，只有 PoW，难度为 action 配置值 × `Risk.FallbackDifficultyFactor`（默认 8）；在普通电脑上大约需要几秒，组件显示进度；
- fallback 签发有独立的、更严格的限流（默认每 IP 每分钟 5 次），与普通签发分开计数；
- 签发出的 token 记录 `challenge_kind: "fallback"`，并在 `risk_reasons` 中带上 `FALLBACK_CHOSEN`。verify 结果对业务方可见，由业务方决定是否追加验证（例如短信）；
- 机器人也会走这条路，所以它被设计得**更贵、更受限、而且对业务方可见**，而不是更容易。

## 7. 配置与数据变更

迁移 0003：

```sql
ALTER TABLE bg_app_action
  ADD COLUMN challenge_mode VARCHAR(16) NOT NULL DEFAULT 'invisible' COMMENT 'invisible | slider | adaptive';
```

- 列默认值为 `invisible`，因此**已有 action 的行为不变**。
- **新建 action 默认 `adaptive`**（已确认）：`botguard-ctl action set` 新建 action 时，不带 `-mode` 即为 `adaptive`；修改已有 action 时，不带 `-mode` 则保持原值。
- 命令行：`botguard-ctl action set -app app_xxx -action login -mode adaptive`；`app show` 显示每个 action 的模式。
- 网关配置新增 `Risk.MediumThreshold`（默认 30）、`Risk.HighThreshold`（默认 70）、`Risk.HighDifficultyFactor`（默认 4）、`Risk.FallbackDifficultyFactor`（默认 8）、`RateLimit.FallbackPerIPPerMinute`（默认 5）、`Slider.Tolerance`（默认 4）。
- Redis 新增：`bg:ipfail:<ip桶>`（10 分钟窗口的失败计数）；token 记录新增 `kind`、`risk`、`reasons` 三个字段。

## 8. 前端组件

- `<botguard-widget>` 不需要新增必填属性，题型由服务端决定。
- **invisible**：与现在一样，点击后后台计算。
- **slider**：
  - 点击后在组件下方展开拼图面板：背景图、左侧拼块，以及“向右拖动滑块完成拼图”的滑条；
  - 拖动时用 Pointer Events 记录轨迹，同时兼容鼠标和触屏；
  - 松手即提交，期间后台 PoW 并行计算，算完才发出 solutions 请求；
  - 失败时面板抖动提示，并自动换一张新图；连续失败 3 次后要等待 10 秒；
  - 支持键盘：方向键移动滑块、回车提交；
  - 面板下方提供“无法完成拼图？”入口，进入 6.5 的备用路径。
- 新增事件：`botguard:challenge`，告诉页面出的是哪种题，演示面板用它显示当前题型。
- 采集信号的代码放在 `src/signals.js`，是纯函数，方便在 Node 中测试。

## 9. 演示与测试

**演示站**：

- 登录用 `adaptive`，注册用固定 `slider`；
- 页面上显示本次的题型、风险等级和原因码（由 PHP 从 verify 结果中取出）；
- 新增攻击场景：

| 场景 | 预期 |
|---|---|
| 无头浏览器特征（上报 webdriver 等信号） | 被升级为滑块并提高 PoW 难度 |
| 脚本直接调接口、不带信号 | 风险升级 |
| 位置正确但轨迹是匀速直线 | `ANSWER_INVALID` |
| 位置错误 | `ANSWER_INVALID` |
| 连续失败后 | 该 IP 风险上升 |

- 继续保留“脚本能做到什么”的如实展示。

**测试**：

- 图片生成：固定随机种子，保证可复现；
- 答案加密：往返解密、篡改检测；
- 轨迹规则：用我在演示页实际拖动录下的人类轨迹作为样本，再用匀速、瞬移、等间隔等合成轨迹做反例，表格驱动测试；
- 风险打分：每条规则单独测试，再测组合；
- 集成测试：真实 MySQL 和 Redis 走完整流程；
- 最后在内置浏览器里实际拖动验证，并检查控制台无错误。

## 10. 已确认的决策

| 问题 | 决定 |
|---|---|
| 无障碍备用路径 | 提供。更高难度 PoW + 独立的严格限流，verify 中标记 `fallback`（见 6.5） |
| 默认模式 | 已有 action 保持 `invisible`；新建 action 默认 `adaptive` |
| 硬拒绝 | 第一期只升级、不拒绝 |
| 风险原因码 | 通过 verify 返回给业务服务器，不返回给浏览器 |
| 背景图来源 | 第一期只用程序生成；自定义图片留到管理后台阶段 |

## 11. 实施步骤与工期

| 步骤 | 内容 | 预计 |
|---|---|---|
| 1 | 框架：题型 / 模式、迁移 0003、ctl `-mode`、challenge 答案加密字段、协议新增字段、verify 新增风险字段 | 2 天 |
| 2 | 风险信号：前端 `signals.js`、服务端规则表与打分、IP 失败计数 | 2–3 天 |
| 3 | 滑块：图片生成与 benchmark、位置与轨迹校验、组件拼图面板（鼠标、触屏、键盘）、无障碍备用路径 | 3–4 天 |
| 4 | PHP SDK 新字段、演示站新场景、文档与验证记录 | 1–2 天 |

合计约 1.5–2 周。每一步完成后都能单独测试，最后统一在演示站中验收。

## 12. 实现记录

与设计的差异和实现中发现的问题：

- **不需要预生成题库**：在 Apple M4 Pro 上单张拼图生成约 2.1 毫秒（`go test -bench . ./internal/service/slider`），低于 5 毫秒的门槛。只加了全局并发上限（CPU 核数），超过时返回 503 而不排队。服务器 CPU 较慢时需要重新测。
- **环境信号改为点击时采集**：最初在页面加载时采集，结果在内置浏览器里误报了 `ZERO_OUTER`。原因是页面加载时 WebView 还没显示，窗口尺寸读出来是 0。后台标签页里预加载的页面也会这样。现在改为点击组件时采集，只有 WebGL 探测结果按页面缓存。
- **键盘完成的滑块需要标记**：键盘路径为了无障碍放宽了轨迹规则，脚本也可以自称 `keyboard` 来绕开轨迹检查。所以加了 `KEYBOARD_INPUT` 原因码（+15）：放行，但业务方能看到。这和 fallback 的处理思路一致。
- **滑块的真实上限**：演示站的“页面脚本”场景显示，用 canvas 找缺口只要几毫秒；配上加速、减速、抖动、回拉的仿人轨迹，就能通过轨迹规则。滑块挡住的是粗糙的脚本，挡不住认真投入的对手，这也是第 1 节里的结论。
- **验证方式**：用浏览器工具做的机器拖动（4 个采样点、23 毫秒）被拒；用键盘方向键完成的拼图通过；fallback 路径通过，verify 中带 `FALLBACK_CHOSEN`。**真人用鼠标或手指拖动尚未实测**，需要人工在演示站上试，并据此调整轨迹规则的阈值。
