<?php

declare(strict_types=1);

// The integration a business server actually needs: validate the form,
// verify the token with the SDK, and only then run the business operation.
require __DIR__ . '/../src/bootstrap.php';

use BotGuard\VerifyRequest;

if ($_SERVER['REQUEST_METHOD'] !== 'POST') {
    json_out(['error' => 'POST only'], 405);
}
session_start();
$cfg = demo_config();

// 1. Ordinary field validation comes first; it must not consume the token.
$username = trim((string) ($_POST['username'] ?? ''));
$password = (string) ($_POST['password'] ?? '');
if ($username === '' || $password === '') {
    json_out(['business' => '请填写用户名和密码（尚未调用 BotGuard，token 未被消耗）', 'outcome' => null, 'calls' => []]);
}

// 2. Verify. The action is fixed by this handler, never taken from the browser.
$token = (string) ($_POST['botguard_token'] ?? '');
$recorder = new RecordingTransport();
$client = client_for($cfg['BG_SECRET_ID'], $cfg['BG_SECRET_KEY'], $recorder);
$outcome = run_verify($client, new VerifyRequest($cfg['BG_APP_KEY'], $token, 'login', VerifyRequest::newRequestId()));

// 3. Only a pass may continue. Keep the token so the replay scenario can try it again.
if ($token !== '') {
    $_SESSION['last_token'] = $token;
}
$business = match ($outcome['category']) {
    'Passed' => "登录成功：欢迎，{$username}（演示站不校验密码）",
    'Rejected' => '人机验证未通过，请重新验证后再提交',
    'CallerError' => '服务配置错误，请联系管理员（不要提示用户重试）',
    default => '验证服务暂时不可用，请稍后再试（不要放行）',
};

json_out(['business' => $business, 'outcome' => $outcome, 'calls' => $recorder->calls]);
