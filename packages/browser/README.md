# BotGuard 浏览器组件

`<botguard-widget>`：浏览器端的人机验证组件，作用相当于易盾的 `initNECaptcha`。

它在点击时采集风险信号，向 gateway 获取 challenge。出什么题由服务端决定：

- **无感**：只在后台算 PoW；
- **滑块拼图**：用户拖动的同时在后台算 PoW；
- **文字点选 / 图形点选**：按提示顺序点击图中的汉字或图形，同时在后台算 PoW；
- **无障碍备用**：用户在拼图面板选择“无法完成拼图？”后进入，只算更难的 PoW。

完成后换取一次性 token，放进表单的隐藏字段。组件里没有任何密钥，也拿不到自己的风险分。

原生 ES 模块，不需要构建工具。已发布到 [npm](https://www.npmjs.com/package/@botguard/browser)。

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

yarn、pnpm 装的也是 npm 上的同一个包。npm 出故障或访问慢时，还有几种不经过 npm 的装法：

| 方式 | 写法 | 说明 |
|---|---|---|
| GitHub Release | `npm install https://github.com/dkeepersun/BotGuard/releases/download/browser-v0.3.0/botguard-browser-0.3.0.tgz --allow-remote=root` | 与 npm 上的同版本是同一个文件，release 里附有 SHA-256；npm、pnpm、yarn 都支持。从 0.2.1 开始提供。npm 12 起默认拒绝从网址安装（`EALLOWREMOTE`），要加 `--allow-remote=root`，之后的 `npm install` / `npm ci` 也要带上，建议在项目 `.npmrc` 里写 `allow-remote=root`；npm 11 及以下、pnpm、yarn 不需要 |
| jsDelivr（直接取 GitHub 标签） | `https://cdn.jsdelivr.net/gh/dkeepersun/BotGuard@browser-v0.3.0/packages/browser/src/botguard.js` | 用 `<script type="module">` 引入，或下载到项目里；不经过 npm。只有 `src/` 源码，webpack 4 用的 `dist/` 构建产物不在仓库里，请用 GitHub Release 的包 |
| 国内镜像 | `npm install @botguard/browser --registry=https://registry.npmmirror.com` | 国内更快；已同步过去的版本在 npm 出问题时也能装 |
| gateway 托管 | `<script src="https://你的gateway/sdk/v1/load.js">` | 见上文第 1 种方式，版本始终与 gateway 一致 |

```js
import { init } from '@botguard/browser';
init({ element: '#captcha', endpoint, appKey, action: 'login', onVerify(err, ret) { /* ... */ } });
```

**版本与服务端题型**：npm 装的组件不会随 gateway 升级。时钟读数 `clock`、镜像辨别 `mirror`、九宫格计数 `grid_count` 需要组件 0.3.0 及以上，旧版组件抽到这三种题会直接验证失败。组件在每次签发请求里带上自己的版本（`widget` 字段，`import { VERSION } from '@botguard/browser'`）；管理后台在应用的「验证策略」页统计旧版组件的请求，仍有旧组件时给出提醒。新建 action 默认不勾选这三种题型，升级组件后再勾选。

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
| `mode` | 展示形式：`embed`（默认）、`float`（触发式）、`inline`（嵌入式）、`popup`（弹出式），见下文“展示形式” |
| `auto` | 页面加载后自动开始验证，不用点击；风险评分与旧版相同（按点击规则评判，低风险仍可能无感通过）。要拼图常驻页面请用 `mode="inline"` |
| `nonce` | CSP nonce，见下文“内容安全策略（CSP）”；`init()` 中对应 `nonce` 选项 |
| `kinds` | 指定题型，逗号分隔：`slider` 固定、`slider,rotate` 随机、`*` 全部随机；见下文“指定题型” |
| `levels` | 难度阶梯，档之间用 `;` 分隔：`invisible; slider,rotate; spatial`；与 `kinds` 同时设置时以 `levels` 为准 |

`endpoint`、`app-key`、`action`、`field-name` 在验证通过后（或验证进行中）被修改时，组件会作废旧 token、回到初始状态，因为旧 token 是按旧的应用和场景签发的。

事件（会冒泡）：

| 事件 | 说明 |
|---|---|
| `botguard:challenge` | 服务端决定的题型：`invisible`、`fallback`，或交互题型 `slider`、`rotate`、`jigsaw`、`click_text`、`click_order`、`click_icon`、`mirror`、`click_shape`、`clock`、`grid_count`、`spatial` |
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

## 指定题型

默认由服务端按 action 配置和风险决定出什么题。也可以在前端指定，但要先在管理后台为该 action 勾选「允许页面指定题型」（默认不勾选，不勾选时组件发来的 `kinds` / `levels` 会被忽略），而且只能在 action 已启用的题型里选（该 action 的「可出题型」），其余忽略。打开这个开关后，机器人也能每次只要最简单的题型，只在确实需要时打开：

```js
init({ element: '#captcha', endpoint, appKey, action: 'login', kinds: ['slider'] });            // 固定滑块
init({ element: '#captcha', endpoint, appKey, action: 'login', kinds: ['slider', 'rotate'] });  // 两种随机
init({ element: '#captcha', endpoint, appKey, action: 'login', kinds: ['*'] });                 // 全部随机
// 难度阶梯：答错升一档，连续答对 3 次降一档（服务端按应用 + IP 记录，30 分钟无操作后归零）
init({ element: '#captcha', endpoint, appKey, action: 'login',
  levels: [['invisible'], ['slider', 'rotate'], ['click_text', 'click_icon'], ['spatial']] });
```

标签写法：`<botguard-widget kinds="slider,rotate" …>`、`<botguard-widget levels="invisible; slider,rotate; spatial" …>`。

服务端的兜底规则（前端无法绕过）：

- 风险为高时始终出 action 中最难的题型，PoW 难度也照常提高；风险为中时阶梯至少从第 2 档开始；
- `invisible`（无感）只在 action 为自适应模式、风险为低时有效；action 为无感模式时前端指定被忽略；
- 档位数、升降规则和有效期见 [API 文档](../../api/contract-v1.md)“前端指定题型”。

## 展示形式（0.5.0 起支持 float、inline）

对应易盾的触发式、嵌入式、弹出式：

| `mode` | 形式 | 效果 |
|---|---|---|
| `embed`（默认） | 点击展开 | 页面上是“点击进行人机验证”的入口框，点击后开始验证，需要做题时在入口框下方展开，会把下面的内容往下推 |
| `float` | 触发式 | 鼠标移到入口框上（或键盘聚焦、触屏点一下）时开始验证，拼图浮在入口框上方显示，上方放不下时改到下方，不挤动页面布局；鼠标移开 0.3 秒后收起（拖动中不会收起），按 Esc 也会收起，回来时还是同一道题；触屏点页面其他地方收起。调用 `verify()`（包括 `bindTo` 绑定的按钮）时会直接显示 |
| `inline` | 嵌入式 | 组件出现在屏幕上、且页面在前台时自动开始验证，拼图常驻在表单里；`reset()` 后（例如表单提交后）、连错三次的冷却结束后、凭证过期后都会自动加载下一道题 |
| `popup` | 弹出式 | 弹出层，由 `verify()` 或 `bindTo` 绑定的提交按钮打开，见上文 |

```js
init({ element: '#captcha', mode: 'float', endpoint, appKey, action: 'login' });   // 触发式
init({ element: '#captcha', mode: 'inline', endpoint, appKey, action: 'login' });  // 嵌入式
init({ mode: 'popup', bindTo: '#submit', endpoint, appKey, action: 'login' });     // 弹出式
```

几点说明：

- **嵌入式一定要做题**：`inline` 是在没有点击的情况下开始的，组件会在签发请求里标明（`activation: "auto"`），gateway 不再用“点击前没有鼠标移动”“点击过快”这两条规则加分，但这样开始的验证不会无感通过，至少出一道交互题。所以 `inline` 适合登录、注册这类本来就要做题的页面。拼图在页面上放久了，组件会在过期前 10 秒自动换一道（`float` 也一样）。
- **`float` 的浮层**是相对入口框绝对定位的：入口框放在 `overflow: hidden` 的容器里时，浮层会被容器裁掉，这时给容器留出空间，或改用 `embed` / `popup`。
- **运行中可以切换**：修改 `mode` 属性立即生效，正在做的题保留；切到 `inline` 时如果组件空闲，会自动开始。
- **签发量**：`inline` 每次打开页面都会签发一道题（组件不在屏幕上时不签发），比点击才开始的形式多；`float` 在鼠标经过时签发。
- 旧版 gateway 不认识 `activation`，`inline` 仍能用，只是会按原规则给“点击过快”等加分。

## 语言、主题与文案（0.4.0 起）

| 设置 | 属性 / `init()` 选项 | 说明 |
|---|---|---|
| 语言 | `lang="en"` / `lang: 'en'` | `zh-CN`（默认）、`zh-TW`、`en`，或 `auto` 跟随浏览器语言。组件的全部文字和 gateway 返回的题目提示都跟着变；英文页面不出文字点选和语序点选（要认读汉字） |
| 主题 | `theme="dark"` / `theme: 'dark'` | `light`（默认）、`dark`，或 `auto` 跟随系统深浅色 |
| 圆角 | `radius="12"` / `radius: 12` / `--botguard-radius` | 入口框和面板的圆角，图片取一半；默认 8px |
| 文案 | `el.texts = {…}` / `texts: {…}` | 按键替换组件自带的文案，对所有语言生效：`{ idle: '点我开始验证' }`；或按语言：`{ en: { idle: 'Tap to verify' } }`。键名见源码中的 `MESSAGES`，也可以 `import { MESSAGES } from '@botguard/browser'` 查看 |
| 后台设置 | `remote-ui="off"` / `remoteUI: false` | 组件加载时会读取管理后台「外观与文案」里的设置（`GET /v1/ui`，同一页面同一应用只请求一次）；关掉后只用页面自己的设置 |

优先级：页面自己设置的属性和选项 > 管理后台的设置 > 组件默认值。

```js
init({ element: '#captcha', endpoint, appKey, action: 'login', lang: 'auto', theme: 'auto',
  texts: { idle: '点击完成安全验证', en: { idle: 'Click to verify' } } });
```

服务端返回的错误信息是简体中文；繁体和英文界面下，组件对已知的错误码（限流、来源不允许、服务不可用等）显示本语言的文字，未知错误码仍显示服务端信息。

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

## uni-app App 端（renderjs）

uni-app App 的页面逻辑跑在没有 DOM 的逻辑层，组件只能放进 renderjs（页面 WebView）里。但这个 WebView 是 `file://` 页面，请求带的是 `Origin: null`，gateway 一律拒绝（放行 `null` 等于允许任何网页的沙箱 iframe 冒用 app_key）。做法是：

1. 应用的允许来源里登记 `app://包名`，如 `app://com.example.shop`（与 manifest 里的 Android 包名 / iOS Bundle ID 一致）；
2. 组件照常在 renderjs 里显示和计算，但用 `transport` 把网络请求交回逻辑层，由 `uni.request` 发出。原生请求没有 Origin，加上请求头 `X-BotGuard-Origin: app://包名`。只有不带 Origin 的请求才读这个头，浏览器里的网页无法冒充。

`transport` 是 `init()` 的选项，也是元素上的属性：`({ url, body }) => Promise<{ status, data }>`，`data` 为响应体（对象或 JSON 字符串都可以）。设置后组件不再调用 `fetch`。

```vue
<template>
	<view>
		<view :task="task" :change:task="bg.onTask"></view>
		<view :reply="reply" :change:reply="bg.onReply"></view>
	</view>
</template>

<script module="bg" lang="renderjs">
import { init } from '@botguard/browser'
export default {
	mounted() { this.waiting = {}; this.nextId = 0 },
	methods: {
		onTask(task) {
			if (!task) return
			this.ins = this.ins || init({ mode: 'popup', endpoint: task.endpoint, appKey: task.appKey, action: task.action,
				transport: ({ url, body }) => new Promise((resolve, reject) => {
					const id = ++this.nextId
					this.waiting[id] = { resolve, reject }
					this.$ownerInstance.callMethod('bgRequest', { id, url, body })
				}) })
			this.ins.verify().then((token) => { this.ins.reset(); this.$ownerInstance.callMethod('bgDone', { token }) },
				(err) => this.$ownerInstance.callMethod('bgDone', { error: { code: err.code, message: err.message } }))
		},
		onReply(r) {
			const w = r && this.waiting[r.id]
			if (!w) return
			delete this.waiting[r.id]
			r.error ? w.reject(new Error(r.error)) : w.resolve({ status: r.status, data: r.data })
		},
	},
}
</script>

<script>
export default {
	data() { return { task: null, reply: null } },
	methods: {
		verify(opts) { // 页面调用：this.$refs.botguard.verify({ endpoint, appKey, action })
			return new Promise((resolve, reject) => { this.done = { resolve, reject }; this.task = { ...opts, seq: Date.now() } })
		},
		bgRequest({ id, url, body }) {
			uni.request({ url, method: 'POST', data: body,
				header: { 'Content-Type': 'text/plain', 'X-BotGuard-Origin': 'app://com.example.shop' },
				success: (res) => { this.reply = { id, status: res.statusCode, data: res.data } },
				fail: (err) => { this.reply = { id, error: err.errMsg } } })
		},
		bgDone({ token, error }) { error ? this.done.reject(Object.assign(new Error(error.message), error)) : this.done.resolve(token) },
	},
}
</script>
```

完整的组件（取消、重复调用、多个 action）见 yihui-h5 接入模拟里的 `components/botguard-app`（`docs/validation.md`）。`X-BotGuard-Origin` 只在 App 端加：H5 端的 `uni.request` 是浏览器请求，自定义请求头会触发 CORS 预检。webpack 4（HBuilderX 的 Vue 2 编译器）编译 renderjs 时同样会用上文的 legacy 构建。

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
