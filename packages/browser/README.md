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

点击 `#submit` 时：

- 已经验证过的直接提交；
- 否则先完成验证，再带着 `botguard_token` 自动提交所在的表单，提交后清空 token。

### 2. npm

```sh
npm install @botguard/browser
```

```js
import { init } from '@botguard/browser';
init({ element: '#captcha', endpoint, appKey, action: 'login', onVerify(err, ret) { /* ... */ } });
```

如果用打包工具（Vite、webpack 等），需要把 `worker.js` 和 `solver.js` 作为同目录的静态资源一起发布。也可以直接把 `node_modules/@botguard/browser/src/` 整个目录复制到静态资源目录，用 `<script type="module">` 引入。

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

事件（会冒泡）：

| 事件 | 说明 |
|---|---|
| `botguard:challenge` | 服务端决定的题型：`invisible` / `slider` / `click_text` / `click_shape` / `fallback` |
| `botguard:verified` | 验证通过：token、过期时间、题型、哈希次数、用时 |
| `botguard:expired` | token 过期，组件已清空 |
| `botguard:error` | 出错：错误码和信息 |
| `botguard:trace` | 每一步的细节，供调试面板使用 |

方法：

| 方法 / 属性 | 说明 |
|---|---|
| `reset()` | 清空 token，重新开始。token 只能用一次，表单提交后应调用 |
| `verify()` | 返回 Promise：有 token 直接返回；没有则开始验证，通过后返回 token；用户取消、出错或连续失败时 reject |
| `token` | 当前 token |

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

- **弹出层**（`mode="popup"`）会自动适配小屏：
  - 宽度不超过屏幕宽度减 24px，高度不超过可视高度，内容放不下时内部滚动；
  - 避开 iPhone 刘海和底部安全区；
  - 图片高度不超过屏幕高度的 38%，横屏手机上也放得下。
- **实测**：在 375×812（竖屏手机）、375×560（App WebView）、812×375（横屏手机）三种视口下都完整显示、不溢出。紧凑模式下，图形点选的面板高度从 313px 降到 229px。

## 滑块与风险信号

- 拼图面板支持鼠标、触屏（Pointer Events）和键盘：Tab 聚焦滑块，方向键移动（Shift 加速），回车确认。
- 组件记录拖动轨迹（`[毫秒, x, y]`，最多 400 点）提交给服务端校验。失败会自动换一张图，连续 3 次失败后冷却 10 秒。
- 点选面板记录每次点击的时间、原图坐标和点击之间的指针移动次数；点满目标数后自动提交，点错可以「重置」。点选题无法用键盘完成，键盘和读屏用户请用「无法完成验证？」进入备用路径。
- 信号只上报结论（例如是否存在 webdriver、是否软件渲染），不上报原始指纹。这些信号都能伪造，服务端会结合 IP 失败次数等自己观察到的信号一起判断。详见 `docs/challenge-types.md`。

## 注意

- 需要在 HTTPS 或 localhost 页面中使用，因为依赖 WebCrypto。
- 页面的 Origin 必须先用 `botguard-ctl origin add` 加入应用的允许列表，否则签发会被拒。
- **从 gateway 跨域加载脚本时**：浏览器不允许直接用跨域地址创建 Worker，组件会改用同源的 blob Worker 去导入 gateway 上的 worker 模块（gateway 对 `/sdk/v1/` 返回了 CORS 头）。如果页面的 CSP 不允许 `worker-src blob:`，组件会退回到主线程计算（较慢，但不会卡死）。
- 如果页面设置了 CSP，需要允许 `script-src` 和 `connect-src` 访问 gateway，跨域时还需允许 `worker-src blob:`。
- 请求用 `text/plain` 发送，属于 CORS 简单请求，不会触发预检。

## 测试

```sh
node --test packages/browser/test/
```

测试用的 PoW 用例 `api/fixtures/pow-sha256-target-v1.json` 与 Go 服务端共用，确保浏览器算出的答案服务端一定接受。
