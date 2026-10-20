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
