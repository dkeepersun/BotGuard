<?php

declare(strict_types=1);

// Shared setup for the demo pages. The SDK is loaded with its plain
// autoloader; a Composer project would use vendor/autoload.php instead.
require getenv('BG_SDK_AUTOLOAD') ?: '/sdk/php/autoload.php';
require __DIR__ . '/RecordingTransport.php';
require __DIR__ . '/BotSolver.php';

use BotGuard\Client;
use BotGuard\Exception\CallerErrorException;
use BotGuard\Exception\ProtocolException;
use BotGuard\Exception\UnknownResultException;
use BotGuard\VerifyRequest;

/** @return array<string, string> credentials written by seed.sh */
function demo_config(): array
{
    static $cfg = null;
    if ($cfg !== null) {
        return $cfg;
    }
    $file = getenv('BG_DEMO_ENV') ?: '/shared/demo.env';
    $cfg = [];
    foreach (is_readable($file) ? file($file, FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES) : [] as $line) {
        [$k, $v] = array_pad(explode('=', $line, 2), 2, '');
        $cfg[trim($k)] = trim($v);
    }
    $cfg['public_endpoint'] = getenv('BG_PUBLIC_ENDPOINT') ?: 'http://localhost:8090';
    $cfg['internal_endpoint'] = getenv('BG_INTERNAL_ENDPOINT') ?: 'http://localhost:8090';
    return $cfg;
}

function demo_ready(): bool
{
    $c = demo_config();
    return ($c['BG_APP_KEY'] ?? '') !== '' && ($c['BG_SECRET_KEY'] ?? '') !== '';
}

function client_for(string $secretId, string $secretKey, RecordingTransport $recorder): Client
{
    return new Client(demo_config()['internal_endpoint'], $secretId, $secretKey, ['transport' => $recorder]);
}

/**
 * Runs one verify and maps it onto the four contract outcomes, exactly as a
 * business server should branch on them.
 *
 * @return array<string, mixed>
 */
function run_verify(Client $client, VerifyRequest $req): array
{
    try {
        $r = $client->verify($req);
        return $r->passed()
            ? ['category' => 'Passed', 'label' => '通过', 'detail' => $r->toArray()]
            : ['category' => 'Rejected', 'label' => '拒绝', 'reason' => $r->reason, 'detail' => $r->toArray()];
    } catch (CallerErrorException $e) {
        return ['category' => 'CallerError', 'label' => '调用方错误', 'reason' => $e->errorCode, 'http' => $e->httpStatus, 'message' => $e->getMessage()];
    } catch (ProtocolException $e) {
        return ['category' => 'Unknown', 'label' => '结果未知（协议错误）', 'http' => $e->httpStatus, 'message' => $e->getMessage()];
    } catch (UnknownResultException $e) {
        return ['category' => 'Unknown', 'label' => '结果未知', 'reason' => $e->errorCode, 'http' => $e->httpStatus,
            'retry_after' => $e->retryAfter, 'message' => $e->getMessage()];
    }
}

/** @param array<string, mixed> $data */
function json_out(array $data, int $status = 200): never
{
    http_response_code($status);
    header('Content-Type: application/json; charset=utf-8');
    header('Cache-Control: no-store');
    echo json_encode($data, JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES | JSON_PRETTY_PRINT);
    exit;
}

function h(string $s): string
{
    return htmlspecialchars($s, ENT_QUOTES | ENT_SUBSTITUTE, 'UTF-8');
}

function mask(string $s, int $keep = 6): string
{
    return strlen($s) <= $keep ? $s : substr($s, 0, $keep) . '…';
}

/**
 * The integration a business server actually needs: validate the form,
 * verify the token with the SDK (action fixed by the handler, never taken
 * from the browser), and only then run the business operation. The risk
 * fields let the business add its own checks, e.g. SMS for high risk.
 *
 * @param callable(string): string $onSuccess
 */
function handle_form(string $action, callable $onSuccess): never
{
    if ($_SERVER['REQUEST_METHOD'] !== 'POST') {
        json_out(['error' => 'POST only'], 405);
    }
    session_start();
    $cfg = demo_config();

    // 1. Ordinary field validation first; it must not consume the token.
    $username = trim((string) ($_POST['username'] ?? ''));
    $password = (string) ($_POST['password'] ?? '');
    if ($username === '' || $password === '') {
        json_out(['business' => '请填写用户名和密码（尚未调用 BotGuard，token 未被消耗）', 'outcome' => null, 'calls' => []]);
    }

    // 2. Verify.
    $token = (string) ($_POST['botguard_token'] ?? '');
    $recorder = new RecordingTransport();
    $client = client_for($cfg['BG_SECRET_ID'], $cfg['BG_SECRET_KEY'], $recorder);
    $outcome = run_verify($client, new VerifyRequest($cfg['BG_APP_KEY'], $token, $action, VerifyRequest::newRequestId()));

    // 3. Only a pass may continue. Keep the token so the replay scenario can try it again.
    if ($token !== '') {
        $_SESSION['last_token'] = $token;
    }
    $business = match ($outcome['category']) {
        'Passed' => $onSuccess($username) . risk_note($outcome['detail']),
        'Rejected' => '人机验证未通过，请重新验证后再提交',
        'CallerError' => '服务配置错误，请联系管理员（不要提示用户重试）',
        default => '验证服务暂时不可用，请稍后再试（不要放行）',
    };
    json_out(['business' => $business, 'outcome' => $outcome, 'calls' => $recorder->calls]);
}

/** @param array<string, mixed> $detail */
function risk_note(array $detail): string
{
    $kinds = ['invisible' => '无感验证', 'slider' => '滑块验证', 'fallback' => '无障碍备用验证'];
    $kind = $kinds[$detail['challenge_kind'] ?? ''] ?? '未知方式';
    $note = "。本次通过方式：{$kind}，风险等级：" . ($detail['risk_level'] ?? '-');
    if (($detail['risk_level'] ?? '') === 'high' || ($detail['challenge_kind'] ?? '') === 'fallback') {
        $note .= '（真实业务可在此追加短信验证）';
    }
    return $note;
}
