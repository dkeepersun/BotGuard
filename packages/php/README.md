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

// 先做普通字段校验，再核验；action 由当前接口写死，不能取自浏览器
$request = new VerifyRequest($appKey, $_POST['botguard_token'] ?? '', 'login', VerifyRequest::newRequestId());

try {
    $result = $client->verify($request);
    if (!$result->passed()) {
        // 拒绝：token 无效、过期、已用过、不属于本应用或本 action。提示用户重新验证。
        return error('人机验证未通过，请重试');      // $result->reason 是原因码
    }
    // 通过：继续执行登录 / 注册等业务
} catch (CallerErrorException $e) {
    // 400/401/403/409：密钥、时钟、应用配置或 request_id 用法有问题。告警，不要让用户重试
} catch (UnknownResultException $e) {
    // 429/5xx/网络错误：服务端可能已消费 token。不要放行；稍后用同一个 request_id 重试
}
```

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
$result = $client->verifyToken($_POST['botguard_token'] ?? '', 'login');
```

Laravel 项目请直接用 `botguard/laravel`（见 packages/laravel/README.md）。

## 和易盾 NECaptchaVerifier 的区别

- **认证方式**：与易盾一样，`secret_key` 不随请求发送。签名改用 HMAC-SHA256 覆盖整个请求体，并带时间戳和 nonce；同一个已签名请求重发会被拒（401 `NONCE_REUSED`）。
- **结果分类**：易盾只返回 true/false，网络错误也会被当成 false。这里分成四类：通过和拒绝是返回值，配置错误和结果未知是异常，避免把配置故障误报成“用户没通过验证”。
- **重试**：`['retries' => 1]` 会在网络错误或 5xx 时重试，每次都重新签名，但 request_id 和请求体保持不变，所以重试拿到的是幂等结果，不会重复消费 token。429 不会自动重试。

## 测试

```sh
php packages/php/tests/run.php
```

测试不依赖 PHPUnit，用的签名用例 `api/fixtures/hmac-signature-v1.json` 与 Go 服务端共用。
