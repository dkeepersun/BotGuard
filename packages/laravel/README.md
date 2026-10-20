# BotGuard Laravel 适配包

在 Laravel 里用 BotGuard，写法对应 yihuipay 现在的 `yidun_captcha()->verify()`。支持 Laravel 9–13、PHP ≥ 8.1，依赖 `botguard/php-sdk`。

## 安装

```sh
composer require botguard/laravel
php artisan vendor:publish --tag=botguard-config   # 可选，生成 config/botguard.php
```

ServiceProvider 和 `BotGuard` Facade 会被 Laravel 自动发现。在 `.env` 中配置：

```dotenv
BOTGUARD_ENDPOINT=https://botguard.example.com          # 服务端调用 verify 的地址
BOTGUARD_PUBLIC_ENDPOINT=https://botguard.example.com   # 浏览器加载组件的地址（通常相同）
BOTGUARD_APP_KEY=app_xxx                                # botguard-ctl app create
BOTGUARD_SECRET_ID=sid_xxx                              # botguard-ctl secret create
BOTGUARD_SECRET_KEY=xxxxxxxx                            # 只放服务端，不要进视图或 JS
```

## 前端：Blade 组件

```blade
<form method="post" action="/login">
    @csrf
    <input name="username"> <input name="password" type="password">
    <x-botguard action="login" />
    <button>登录</button>
</form>
```

组件会从 gateway 加载脚本，用法类似易盾的 `load.min.js`，并在表单里放入隐藏字段 `botguard_token`。

如果想在点提交按钮时再弹出验证（类似易盾的 bind 模式），给出 `bind` 即可，默认就是弹窗：

```blade
<button id="login-btn">登录</button>
<x-botguard action="login" bind="#login-btn" />
```

`mode` 写了就以 `mode` 为准，没写时：有 `bind` 为 `popup`，否则为 `embed`。几种组合：

| 写法 | 效果 |
|---|---|
| `<x-botguard action="login" />` | 嵌入式组件 |
| `bind="#btn"` | 弹窗；点按钮先验证，通过后再提交表单 |
| `mode="embed" bind="#btn"` | 嵌入式组件；点按钮时若还没验证，先完成验证再提交 |
| `mode="popup"`（不写 bind） | 弹窗组件，由你自己的 JS 调用元素的 `verify()` 打开 |

`mode` 只能是 `embed` 或 `popup`，写错会在渲染时报错。

H5 页面或 App 内嵌页面，可以压缩高度、改主色：

```blade
<x-botguard action="login" size="compact" width="100%" image-height="120" accent="#ff6a00" />
```

其他属性（`class`、`id`、`data-*`、`auto` 等）会原样加到 `<botguard-widget>` 元素上；`bind` 模式下由脚本设置到生成的元素上。`endpoint`、`app-key`、`field-name` 只取自配置，不能通过属性覆盖。

## 后端：三种写法

```php
// 1. 表单验证规则：先校验普通字段，通过后再单独校验 botguard
$data = $request->validate([
    'username' => 'required',
    'password' => 'required',
]);
$request->validate(['botguard_token' => 'botguard:login']);
// 或 ['botguard_token' => [new \BotGuard\Laravel\Rules\BotGuardToken('login')]]

// 2. 布尔值，等同易盾的写法
if (!botguard()->passes($request->input('botguard_token'), 'login')) {
    return back()->withErrors(['botguard_token' => '验证失败']);
}

// 3. 完整结果，可区分各种情况，并读取风险信息
$result = \BotGuard::verify($request->input('botguard_token'), 'login');
if ($result->passed() && $result->riskLevel === 'high') {
    // 业务自行决定是否追加短信验证
}
```

### token 什么时候被消耗

规则一执行就会消耗 token。Laravel 在某个字段校验失败后，**仍会继续校验其余字段**（`bail` 只对同一个字段内的多条规则生效），所以把 `botguard` 规则写在数组最后并不能避免消耗：用户名写错时 token 照样被用掉，用户改完再提交只会得到“验证未通过”。两种做法：

- 推荐：像上面那样分两步，普通字段全部通过后再单独 `validate` botguard。
- 或者在同一个校验器里把 botguard 放在最后，并开启“首个失败即停止”：

```php
Validator::make($request->all(), [
    'username' => 'required',
    'password' => 'required',
    'botguard_token' => 'botguard:login',   // 必须放最后
])->stopOnFirstFailure()->validate();

// FormRequest 中：protected $stopOnFirstFailure = true;
```

### 规则说明

- `botguard` 规则和 `BotGuardToken` 都是隐式规则：字段缺失、为空字符串或 null 时同样执行并判为失败，不写 `required`、写了 `nullable` 也不会被跳过（显式写 `sometimes` 时，字段缺失则按 Laravel 语义跳过，请勿对验证码字段使用）。
- `botguard` 规则必须且只能带一个 action 参数：`botguard`、`botguard:`、`botguard:a,b` 都会抛 `InvalidArgumentException`。`new BotGuardToken('')` 同样抛出。
- action 必须由后端写死，不能取自浏览器提交的值；格式为 `[a-z0-9_]`、1–32 个字符，格式不对时不会发请求，`verify()` 抛 `CallerErrorException`（`INVALID_REQUEST`），`passes()` 和规则返回 false 并写错误日志。

**说明**：

- `passes()`、`verify()` 接收浏览器提交的任意值：null、数组、非字符串、非法 UTF-8、超长或含非法字符的 token 一律在本地判为拒绝（reason `TOKEN_INVALID`，与网关一致），不发请求，也不会抛 `TypeError` / `JsonException` 导致 500。
- `passes()` 和验证规则遇到配置错误、服务不可用时也返回 false（不放行），但会写一条 error 日志，以免被误当成“用户没通过”。
- 未配置 `BOTGUARD_APP_KEY` / `BOTGUARD_SECRET_ID` / `BOTGUARD_SECRET_KEY` 时，应用照常启动、页面照常渲染；`passes()` 和规则返回 false 并写 error 日志，`verify()` 抛 `CallerErrorException`（`errorCode` 为 `NOT_CONFIGURED`）。
- 同一个请求里，同一个 token 和 action 只会核验一次，所以验证规则被执行两次也不会把 token 消耗掉。
- `BOTGUARD_TIMEOUT`（默认 3 秒）和 `BOTGUARD_CONNECT_TIMEOUT`（默认 1 秒）为空、0、负数或非数字时使用默认值，小于 0.05 秒按 0.05 秒算，不会出现“不限时”把 PHP-FPM 进程挂住的情况。
- 规则、`botguard()` helper 和 Facade 每次都从当前容器取服务，Octane 下不会拿到启动时的旧容器。
