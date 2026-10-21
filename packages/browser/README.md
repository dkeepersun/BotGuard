# BotGuard 浏览器组件

`<botguard-widget>`：浏览器端的人机验证组件，作用相当于易盾的 `initNECaptcha`。

它在点击时采集风险信号，向 gateway 获取 challenge。出什么题由服务端决定：

- **无感**：只在后台算 PoW；
- **滑块拼图**：用户拖动的同时在后台算 PoW；
- **文字点选 / 图形点选**：按提示顺序点击图中的汉字或图形，同时在后台算 PoW；
- **无障碍备用**：用户在拼图面板选择“无法完成拼图？”后进入，只算更难的 PoW。

完成后换取一次性 token，放进表单的隐藏字段。组件里没有任何密钥，也拿不到自己的风险分。

原生 ES 模块，不需要构建工具。尚未发布到 npm。

## 三种引入方式

### 1. 易盾式：引用 gateway 托管的脚本（不需要 npm）

和易盾的 `load.min.js` + `initNECaptcha` 用法一致：

```html
<script src="https://botguard.example.com/sdk/v1/load.js"></script>
<div id="captcha"></div>
<script>
  initBotGuard({
    element: '#captcha',
    endpoint: 'https://botguard.example.com',
    appKey: 'app_xxx',
    action: 'login',
    onVerify: function (err, ret) {
      if (!err) document.querySelector('#token').value = ret.token; // 相当于易盾的 ret.validate
    }
  }, function onload(instance) {}, function onerror(err) {});
</script>
```

如果想在用户点提交按钮时再弹出验证（对应易盾的 bind 模式）：

```js
initBotGuard({ mode: 'popup', bindTo: '#submit', endpoint, appKey, action: 'login' });
```

`#submit` 在表单里时，组件在表单的 `submit` 事件（捕获阶段）拦截，所以在输入框里按回车、点其他提交按钮也同样受保护：

- 已经验证过的直接提交；提交事件处理完后清空 token（token 只能用一次）。业务自己的 `submit` 处理函数（例如改用 fetch 提交）里仍能读到 token，请在处理函数里同步读取；
- 否则先拦下这次提交（业务的 `submit` 处理函数不会执行），弹出验证，通过后用原来的提交按钮重新提交一次（`requestSubmit`，iOS 16 以下改为点击该按钮），带上 `botguard_token`；
- 连续点击、验证期间再按回车，只会进行一次验证、提交一次；
- 表单校验（如 `required`）没通过时浏览器不会提交，token 也不会被清空；
- 按钮自己的 `click` 处理函数照常执行，组件不再吞掉它们。
- 弹出层里出错时，`verify()` 照常 reject，但弹出层保留错误信息和“重试”按钮；用户点“重试”并通过后会自动继续这次提交，关闭弹出层则放弃。

`#submit` 不在任何表单里时，组件改为拦截按钮的 `click`：验证通过前业务的 click 处理函数不会执行，通过后组件再“点击”一次按钮，此时处理函数里可以用 `instance.token` 读到 token（请同步读取，随后会被清空）。

`element`、`bindTo` 写的选择器找不到元素时，`init()` 会直接抛错（`initBotGuard` 则调用 `onerror`），不会悄悄挂到 `<body>` 上。

**异步加载**：给 `load.js` 加 `async` 时，页面脚本可能在它执行之前就调用 `initBotGuard`。在它前面放一行占位代码即可，调用会排队，加载完成后依次执行：

```html
<script>window.initBotGuard=window.initBotGuard||function(){(initBotGuard.q=initBotGuard.q||[]).push(arguments)}</script>
<script async src="https://botguard.example.com/sdk/v1/load.js"></script>
```

`load.js` 必须用普通的 `<script src>` 引入（不能是 `type="module"`，也不能内联或打包进别的脚本），它要靠自己的地址找到同目录的 `botguard.js`；找不到时会通过 `onerror`（或控制台）给出明确错误。

### 2. npm

```sh
npm install @botguard/browser
```

```js
import { init } from '@botguard/browser';
init({ element: '#captcha', endpoint, appKey, action: 'login', onVerify(err, ret) { /* ... */ } });
```

组件用打包工具能识别的写法 `new Worker(new URL('./worker.js', import.meta.url), { type: 'module' })` 创建 Worker，Vite、webpack 5、Parcel 会自动把 `worker.js` 及其依赖 `solver.js` 打进产物，不需要手工复制。Vite 开发模式下依赖预构建会破坏这种写法，请把本包加入 `optimizeDeps.exclude: ['@botguard/browser']`。万一 Worker 加载失败或 5 秒内没有任何响应，组件会自动改在主线程计算（较慢，但不会卡死）。

**webpack 4 / HBuilderX（uni-app Vue 2）**：webpack 4 不认 `package.json` 的 `exports`，也解析不了 `import.meta`。本包为它准备了 `dist/botguard.legacy.js`（ES2017，Worker 代码内联），`main` / `module` 字段指向这个文件，webpack 4 会自动选用，写法和上面一样 `import { init } from '@botguard/browser'`，不需要额外配置。读 `exports` 的打包工具（Vite、webpack 5 等）仍然使用 `src/` 下的源码。legacy 版的 Worker 从 blob URL 启动，页面有 CSP 时需要 `worker-src blob:`，否则退回主线程计算。uni-app 里请只在 H5 条件编译（`// #ifdef H5`）中引入：App 端的逻辑层没有 DOM，组件不能在那里运行。

也可以直接把 `node_modules/@botguard/browser/src/` 整个目录复制到静态资源目录，用 `<script type="module">` 引入。

服务端渲染（Next.js、Nuxt 等）时可以直接在服务端 import 本包，模块顶层不访问 `document`、`HTMLElement`、`customElements`；组件只在浏览器里注册，`init()` 只能在浏览器里调用。

### 3. 直接写标签

```html
<script type="module" src="/botguard/botguard.js"></script>
<form method="post" action="/login">
  <botguard-widget endpoint="https://botguard.example.com" app-key="app_xxx" action="login"></botguard-widget>
</form>
```

验证通过后，表单会带上 `botguard_token` 字段，由业务服务器用 SDK 核验。

| 属性 | 说明 |
|---|---|
| `endpoint` | gateway 地址 |
| `app-key` | 应用的公开 key |
| `action` | 场景名，须在应用中启用，并与服务端核验时写死的 action 一致 |
| `field-name` | 隐藏字段名，默认 `botguard_token` |
| `mode` | `embed`（默认，嵌入页面）或 `popup`（弹出层，配合 `verify()` 或 `bindTo`） |
| `auto` | 页面加载后自动开始验证，不用点击 |
| `nonce` | CSP nonce，见下文“内容安全策略（CSP）”；`init()` 中对应 `nonce` 选项 |

`endpoint`、`app-key`、`action`、`field-name` 在验证通过后（或验证进行中）被修改时，组件会作废旧 token、回到初始状态，因为旧 token 是按旧的应用和场景签发的。

事件（会冒泡）：

| 事件 | 说明 |
|---|---|
| `botguard:challenge` | 服务端决定的题型：`invisible` / `slider` / `click_text` / `click_shape` / `fallback` |
| `botguard:verified` | 验证通过：token、过期时间、题型、哈希次数、用时 |
| `botguard:expired` | token 过期，组件已清空 |
| `botguard:error` | 出错：错误码和信息 |
| `botguard:cancel` | 用户关闭了弹出层（× 或 Esc） |
| `botguard:trace` | 每一步的细节，供调试面板使用 |

方法：

| 方法 / 属性 | 说明 |
|---|---|
| `reset()` | 清空 token，重新开始。token 只能用一次，表单提交后应调用 |
| `verify()` | 返回 Promise：有 token 直接返回；没有则开始验证，通过后返回 token。验证进行中重复调用返回同一个 Promise |
| `token` | 当前 token |

`verify()` 一定会有结果，reject 的错误对象带 `code`：

| `code` | 时机 |
|---|---|
| `CANCELLED` | 用户关闭弹出层、调用了 `reset()`、组件被移出页面，或修改了 `endpoint` 等属性 |
| `TOO_MANY_FAILURES` | 连续答错 3 次；冷却的 10 秒内调用 `verify()` 会立即以此 reject |
| `TIMEOUT` | 计算超过 60 秒 |
| `NETWORK`、`HTTP_xxx` 或服务端错误码 | 请求失败 |

组件被移出页面时，进行中的验证会被取消（Worker 停止，状态回到初始），再插回页面后可以重新验证；已拿到的 token 会保留，过期计时继续。

token 的有效期优先使用服务端返回的相对时长 `expires_in`（秒），不受用户电脑时钟偏差影响；旧版 gateway 没有这个字段时按 `expires_at` 计算，结果不合理（≤ 0 或超过 10 分钟）时按 60 秒处理。组件会提前 3 秒视为过期。

## 尺寸与外观（H5 / App WebView）

组件默认宽度为 100%，最宽 360px；图片高度随宽度按比例变化。宽、高和主色都可以定制。

| 设置 | 属性 / `init()` 选项 / CSS 变量 | 说明 |
|---|---|---|
| 最大宽度 | `width="100%"` / `width: '100%'` / `--botguard-width` | 数字表示像素，也可写 `100%`、`20rem` 等 |
| 图片最大高度 | `image-height="120"` / `imageHeight: 120` / `--botguard-image-height` | 超出时图片按比例变窄，不会变形；最小 100px，以免图片过小看不清 |
| 紧凑模式 | `size="compact"` / `size: 'compact'` | 收紧边距，隐藏副标题和品牌名；入口框高度约 36px |
| 主色 | `accent="#ff6a00"` / `accent: '#ff6a00'` / `--botguard-accent` | 按钮、滑块、进度条、点选标记的颜色 |

```html
<!-- H5 / App 内嵌页面：铺满宽度，图片高度不超过 120px -->
<botguard-widget size="compact" width="100%" image-height="120" ...></botguard-widget>
```

```css
/* 也可以在 App 的样式表里统一设置 */
botguard-widget { --botguard-width: 100%; --botguard-image-height: 120px; --botguard-accent: #ff6a00; }
```

- **弹出层**（`mode="popup"`）从开始验证就显示：获取题目、后台计算（带进度条）、出错（带“重试”按钮）、失败过多的冷却提示都在弹出层里，不会出现点了没反应的情况。它是 `role="dialog"` + `aria-modal="true"`，打开时焦点移入，Tab 在弹出层内循环，Esc 或 × 关闭，关闭后焦点回到打开前的元素（例如提交按钮）。
- **弹出层**会自动适配小屏：
  - 宽度不超过屏幕宽度减 24px，高度不超过可视高度，内容放不下时内部滚动；
  - 避开 iPhone 刘海和底部安全区；
  - 图片高度不超过屏幕高度的 38%，横屏手机上也放得下。
- **实测**：在 375×812（竖屏手机）、375×560（App WebView）、812×375（横屏手机）三种视口下都完整显示、不溢出。紧凑模式下，图形点选的面板高度从 313px 降到 229px。

## 滑块与风险信号

- 拼图面板支持鼠标、触屏（Pointer Events）和键盘：Tab 聚焦滑块，方向键移动（Shift 加速），回车确认。
- 组件记录拖动轨迹（`[毫秒, x, y]`）提交给服务端校验：相邻两点至少间隔 8 毫秒；超过 400 点时隔一个丢一个、采样间隔加倍，所以拖得再久也不会丢掉结尾；松手的那一点一定会记录，保证轨迹终点与提交的位置一致。失败会自动换一张图，连续 3 次失败后冷却 10 秒。
- 只跟随按下滑块的那根手指 / 那个指针，其他指针的事件被忽略。拖动被浏览器打断（`pointercancel`，例如页面滚动、系统手势）时滑块回到起点，请用户重新拖动，不会当作松手提交。用方向键移动过之后仍然可以改用鼠标拖动（会从起点重新开始）。
- 每次按下滑块或按方向键时都会重新测量图片尺寸，所以拼图显示后页面缩放、横竖屏切换都不影响换算；尺寸为 0（例如还没显示出来）时不响应拖动。
- 点选面板记录每次点击的时间、原图坐标和点击之间的指针移动次数；点满目标数后自动提交，点错可以「重置」。点选题无法用键盘完成，键盘和读屏用户请用「无法完成验证？」进入备用路径。
- 信号只上报结论（例如是否存在 webdriver、是否软件渲染），不上报原始指纹。这些信号都能伪造，服务端会结合 IP 失败次数等自己观察到的信号一起判断。详见 `docs/challenge-types.md`。

## 注意

- 需要在 HTTPS 或 localhost 页面中使用，因为依赖 WebCrypto。
- 页面的 Origin 必须先用 `botguard-ctl origin add` 加入应用的允许列表，否则签发会被拒。
- **从 gateway 跨域加载脚本时**：浏览器不允许直接用跨域地址创建 Worker，组件会改用同源的 blob Worker 去导入 gateway 上的 worker 模块（gateway 对 `/sdk/v1/` 返回了 CORS 头），Worker 启动后立即释放 blob URL。如果页面的 CSP 不允许 `worker-src blob:`，或 Worker 5 秒内没有任何响应，组件会退回到主线程计算（较慢，但不会卡死，超时或取消后会停止）。
- 请求用 `text/plain` 发送，属于 CORS 简单请求，不会触发预检。

## 内容安全策略（CSP）

页面设置了 CSP 时，需要放行：

| 指令 | 值 | 原因 |
|---|---|---|
| `script-src` | gateway 地址（或你托管 SDK 的地址） | 加载 `load.js`、`botguard.js` 等模块，Worker 内部也要导入 `worker.js` |
| `connect-src` | gateway 地址 | 获取题目、提交答案 |
| `img-src` | `data:` | 拼图和点选图片以 data URL 下发 |
| `worker-src` | `blob:`（跨域加载 SDK 时）；同源托管时 `'self'` 即可 | 后台计算用的 Worker；不放行时退回主线程计算 |
| `style-src` | 通常不需要改 | 见下 |

组件的样式优先用 constructable stylesheet（`adoptedStyleSheets`）注入，不受 `style-src` 限制。不支持它的旧浏览器（Safari 16.4 以下、Firefox 101 以下）会退回 `<style>` 元素，这时如果 CSP 没有 `'unsafe-inline'`，请把页面的 nonce 传给组件：`<botguard-widget nonce="...">` 或 `init({ nonce: '...' })`。

旧版 Safari 不支持的 `aspect-ratio`（15 以下）和 `color-mix()`（16.2 以下）都有降级样式，图片比例和颜色在这些版本上也正常。

## 测试

```sh
npm ci --prefix packages/browser     # 安装 jsdom（仅开发依赖，不会进入发布包）
npm test --prefix packages/browser   # 即 node --test test/*.test.mjs
```

或在仓库根目录执行 `make test-sdk`（同时跑 PHP SDK 测试）。组件的行为测试（`widget`、`slider`、`bind`）在 jsdom 里运行，用假的 Worker、fetch 和定时器；`ssr.test.mjs` 在没有 DOM 的 Node 里直接 import 组件。

测试用的 PoW 用例 `api/fixtures/pow-sha256-target-v1.json` 与 Go 服务端共用，确保浏览器算出的答案服务端一定接受。
