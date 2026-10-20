# BotGuard PHP SDK

业务服务器用它调用 BotGuard 的 `/v1/verify` 核验浏览器提交的 token。要求 PHP ≥ 8.1，只依赖 `ext-curl` 和 `ext-json`，不依赖任何框架。尚未发布到 Packagist。

## 安装

Composer（发布前可用 path 仓库）：

```json
{
  "repositories": [{"type": "path", "url": "../BotGuard/packages/php"}],
  "require": {"botguard/php-sdk": "*"}
}
```

不用 Composer 时，直接 `require 'packages/php/autoload.php';`。

## 用法

```php
use BotGuard\Client;
use BotGuard\VerifyRequest;
use BotGuard\Exception\CallerErrorException;
use BotGuard\Exception\UnknownResultException;

// secret_id / secret_key 来自 botguard-ctl secret create，只放在服务端配置里
$client = new Client('https://botguard.example.com', $secretId, $secretKey, ['timeout' => 3]);

// 先做普通字段校验，再核验；action 由当前接口写死，不能取自浏览器。
// VerifyRequest 的 token 参数是 string：$_POST 里可能是数组，先过滤（verifyToken() 则可以直接接收原值）
$token = $_POST['botguard_token'] ?? '';
$request = new VerifyRequest($appKey, is_string($token) ? $token : '', 'login', VerifyRequest::newRequestId());

try {
    $result = $client->verify($request);
    if (!$result->passed()) {
        // 拒绝：token 无效、过期、已用过、不属于本应用或本 action。提示用户重新验证。
        return error('人机验证未通过，请重试');      // $result->reason 是原因码
    }
    // 通过：继续执行登录 / 注册等业务
} catch (CallerErrorException $e) {
    // 除 429 外的 3xx/4xx（密钥、时钟、应用配置、request_id 用法、endpoint 写错被代理返回 404 等），
    // 或本地发现 app_key/action/request_id 格式不对。告警，不要让用户重试
} catch (UnknownResultException $e) {
    // 429/5xx/网络错误/响应不符合契约：服务端可能已消费 token。不要放行；稍后用同一个 request_id 重试
}
```

## 本地校验

发请求前 SDK 先在本地检查格式（与网关规则一致），不合格的不发网络请求：

| 字段 | 规则 | 不合格时 |
|---|---|---|
| token | 字符串，只含 `[A-Za-z0-9._-]`，1–256 字节 | 返回 Rejected，`reason` 为 `TOKEN_INVALID`（与网关一致） |
| action | `[a-z0-9_]`，1–32 字符 | 抛 `CallerErrorException`，`errorCode` 为 `INVALID_REQUEST`，`httpStatus` 为 0 |
| app_key | `[A-Za-z0-9_-]`，8–64 字符 | 同上 |
| request_id | `[A-Za-z0-9_-]`，1–64 字符 | 同上 |

token 来自浏览器，空值、null、数组、非法 UTF-8、超长都只是“用户没通过”，可以直接把 `$_POST['botguard_token'] ?? null` 传给 `verifyToken()`；action、app_key、request_id 由后端决定，格式不对说明代码或配置有误，所以按调用方错误抛出。

## 错误分类与重试

| 情况 | 结果 | 自动重试 |
|---|---|---|
| 200 且符合契约 | Passed / Rejected | — |
| 除 429 外的 3xx、4xx（含 404/405/413/415、重定向） | `CallerErrorException`，保留 `httpStatus` 和响应体里的 `code` | 否 |
| 429 | `UnknownResultException`，`retryAfter` 取自 Retry-After | 否 |
| 5xx、网络错误、超时 | `UnknownResultException` | 是（`retries`） |
| 响应体超过 64 KiB、200 但格式不符、其他意外状态码（如 204） | `ProtocolException`（`UnknownResultException` 子类），保留 `httpStatus` | 否 |

`retryAfter` 支持秒数和 HTTP-date 两种写法，无法解析时为 null。

## 超时

`timeout`（默认 3 秒）和 `connect_timeout`（默认 1 秒）为空、0、负数或非数字时使用默认值，小于 0.05 秒按 0.05 秒算。cURL 把 0 当作“不限时”，这样可以避免网关无响应时挂住 PHP-FPM 进程。

## 密钥保护

`secret_key` 参数带 `#[\SensitiveParameter]`（PHP ≥ 8.2 生效），不会出现在异常堆栈里；`var_dump()` / `print_r()` 一个 `Client` 时显示为 `[REDACTED]`。

## 风险信息

通过时，`VerifyResult` 还带有这个 token 是怎么拿到的：

```php
$result->challengeKind;  // invisible / slider / click_text / click_shape / fallback（旧版 gateway 为 null）
$result->riskLevel;      // low / medium / high
$result->riskReasons;    // 例如 ['NO_SIGNALS', 'IP_RECENT_FAILURES', 'FALLBACK_CHOSEN']

if ($result->passed() && ($result->riskLevel === 'high' || $result->challengeKind === 'fallback')) {
    // 业务自行决定是否追加验证，例如短信验证码
}
```

这些字段不改变“通过”这一结论，只供业务方二次决策。

## 一行式调用

和易盾的 `verify($validate)` 一样，可以只传 token 和 action：

```php
$client = new Client($endpoint, $secretId, $secretKey, ['app_key' => 'app_xxx']);
$result = $client->verifyToken($_POST['botguard_token'] ?? null, 'login');   // 非字符串在本地判为 TOKEN_INVALID
```

Laravel 项目请直接用 `botguard/laravel`（见 packages/laravel/README.md）。

## 和易盾 NECaptchaVerifier 的区别

- **认证方式**：与易盾一样，`secret_key` 不随请求发送。签名改用 HMAC-SHA256 覆盖整个请求体，并带时间戳和 nonce；同一个已签名请求重发会被拒（401 `NONCE_REUSED`）。
- **结果分类**：易盾只返回 true/false，网络错误也会被当成 false。这里分成四类：通过和拒绝是返回值，配置错误和结果未知是异常，避免把配置故障误报成“用户没通过验证”。
- **重试**：`['retries' => 1]` 只会在网络错误或 5xx 时重试，每次都重新签名，但 request_id 和请求体保持不变，所以重试拿到的是幂等结果，不会重复消费 token。429 不会自动重试。

## 测试

```sh
php packages/php/tests/run.php
```

测试不依赖 PHPUnit；其中一部分会用 `php -S` 在 127.0.0.1 起一个临时假网关（`tests/server.php`）检验真实的 cURL 行为。签名用例 `api/fixtures/hmac-signature-v1.json` 与 Go 服务端共用。
