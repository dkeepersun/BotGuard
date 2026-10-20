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

如果想在点提交按钮时再弹出验证（类似易盾的 bind 模式）：

```blade
<button id="login-btn">登录</button>
<x-botguard action="login" mode="popup" bind="#login-btn" />
```

## 后端：三种写法

```php
// 1. 表单验证规则（推荐，放在其他字段之后）
$request->validate([
    'username' => 'required',
    'password' => 'required',
    'botguard_token' => 'required|botguard:login',
]);
// 或 'botguard_token' => ['required', new \BotGuard\Laravel\Rules\BotGuardToken('login')]

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

**说明**：

- `passes()` 和验证规则遇到配置错误、服务不可用时也返回 false（不放行），但会写一条错误日志，以免被误当成“用户没通过”。
- 同一个请求里，同一个 token 和 action 只会核验一次，所以验证规则被执行两次也不会把 token 消耗掉。
- action 必须由后端写死，不能取自浏览器提交的值。
