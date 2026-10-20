# BotGuard 浏览器组件

`<botguard-widget>`：浏览器端的人机验证组件，作用相当于易盾的 `initNECaptcha`。

它会向 gateway 获取 challenge，在 Web Worker 里求解 PoW，换取一次性 token，再把 token 放进表单的隐藏字段。组件里没有任何密钥。

原生 ES 模块，不需要构建工具。尚未发布到 npm。

## 用法

把 `src/` 下的三个文件（`botguard.js`、`worker.js`、`solver.js`）放到同一目录下，作为静态资源对外提供：

```html
<script type="module" src="/botguard/botguard.js"></script>

<form method="post" action="/login">
  <input name="username">
  <botguard-widget endpoint="https://botguard.example.com" app-key="app_xxx" action="login"></botguard-widget>
  <button>登录</button>
</form>
```

验证通过后，表单会带上 `botguard_token` 字段，由业务服务器用 SDK 核验。

| 属性 | 说明 |
|---|---|
| `endpoint` | gateway 地址 |
| `app-key` | 应用的公开 key |
| `action` | 场景名，须在应用中启用，并与服务端核验时写死的 action 一致 |
| `field-name` | 隐藏字段名，默认 `botguard_token` |
| `auto` | 页面加载后自动开始验证，不用点击 |

事件（会冒泡）：

| 事件 | 说明 |
|---|---|
| `botguard:verified` | 验证通过：token、过期时间、哈希次数、用时 |
| `botguard:expired` | token 过期，组件已清空 |
| `botguard:error` | 出错：错误码和信息 |
| `botguard:trace` | 每一步的细节，供调试面板使用 |

方法：

| 方法 / 属性 | 说明 |
|---|---|
| `reset()` | 清空 token，重新开始。token 只能用一次，表单提交后应调用 |
| `token` | 当前 token |

## 注意

- 需要在 HTTPS 或 localhost 页面中使用，因为依赖 WebCrypto。
- 页面的 Origin 必须先用 `botguard-ctl origin add` 加入应用的允许列表，否则签发会被拒。
- 如果页面设置了 CSP，需要允许 `connect-src` 访问 gateway，并允许从自身来源加载 Worker（`worker-src 'self'`）。组件不使用 blob Worker。
- 请求用 `text/plain` 发送，属于 CORS 简单请求，不会触发预检。

## 测试

```sh
node --test packages/browser/test/
```

测试用的 PoW 用例 `api/fixtures/pow-sha256-target-v1.json` 与 Go 服务端共用，确保浏览器算出的答案服务端一定接受。
