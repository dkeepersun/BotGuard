<?php

declare(strict_types=1);

// Dependency-free test runner: php packages/php/tests/run.php
require __DIR__ . '/../autoload.php';

use BotGuard\Client;
use BotGuard\Exception\CallerErrorException;
use BotGuard\Exception\ProtocolException;
use BotGuard\Exception\UnknownResultException;
use BotGuard\Signer;
use BotGuard\Transport\Response;
use BotGuard\Transport\TransportException;
use BotGuard\Transport\TransportInterface;
use BotGuard\VerifyRequest;

$failures = 0;
$count = 0;
function check(bool $ok, string $name): void
{
    global $failures, $count;
    $count++;
    if (!$ok) {
        $failures++;
        fwrite(STDERR, "FAIL: $name\n");
    }
}

/** @return mixed the thrown exception, or null */
function thrown(callable $fn): mixed
{
    try {
        $fn();
    } catch (Throwable $e) {
        return $e;
    }
    return null;
}

final class FakeTransport implements TransportInterface
{
    /** @var list<array{url: string, headers: array<string, string>, body: string}> */
    public array $calls = [];

    /** @param list<Response|TransportException> $replies */
    public function __construct(private array $replies)
    {
    }

    public function post(string $url, array $headers, string $body): Response
    {
        $this->calls[] = ['url' => $url, 'headers' => $headers, 'body' => $body];
        $next = array_shift($this->replies);
        if ($next instanceof TransportException) {
            throw $next;
        }
        return $next;
    }
}

function ok(array $data): Response
{
    return new Response(200, [], json_encode(['code' => 'OK', 'data' => $data, 'request_id' => 'r']));
}

function err(int $status, string $code, array $headers = []): Response
{
    return new Response($status, $headers, json_encode(['code' => $code, 'message' => 'm', 'request_id' => 'r']));
}

// Shared signature fixtures: identical to the Go gateway.
$fixtures = json_decode(file_get_contents(__DIR__ . '/../../../api/fixtures/hmac-signature-v1.json'), true);
check(is_array($fixtures) && count($fixtures) > 0, 'fixtures load');
foreach ($fixtures as $f) {
    $canon = Signer::canonical($f['method'], $f['path'], $f['timestamp'], $f['nonce'], $f['body']);
    check($canon === $f['canonical'], "canonical: {$f['name']}");
    check(Signer::sign($f['secret_key'], $canon) === $f['signature'], "signature: {$f['name']}");
}
check(preg_match('/^[A-Za-z0-9_-]{32}$/', Signer::newNonce()) === 1, 'nonce format');
check(preg_match('/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/', VerifyRequest::newRequestId()) === 1, 'uuid v4');

$req = new VerifyRequest('app_demo', 'tok', 'login', 'req-1');
$mk = fn(array $replies, int $retries = 0) => [$t = new FakeTransport($replies), new Client('http://gw/', 'sid_demo1234', 'key', ['transport' => $t, 'retries' => $retries])];

// Passed: signed headers cover the exact body that was sent.
[$t, $c] = $mk([ok(['success' => true, 'app_key' => 'app_demo', 'action' => 'login', 'verified_at' => 1790000000])]);
$res = $c->verify($req);
check($res->passed() && $res->verifiedAt === 1790000000, 'passed');
$call = $t->calls[0];
check($call['url'] === 'http://gw/v1/verify', 'url');
$h = $call['headers'];
check($h['X-BG-Signature'] === Signer::sign('key', Signer::canonical('POST', '/v1/verify', (int) $h['X-BG-Timestamp'], $h['X-BG-Nonce'], $call['body'])), 'signature covers body');
check(json_decode($call['body'], true) === ['app_key' => 'app_demo', 'token' => 'tok', 'action' => 'login', 'request_id' => 'req-1'], 'body fields');

// Risk fields are surfaced when present and default when absent.
[, $c] = $mk([ok(['success' => true, 'app_key' => 'app_demo', 'action' => 'login', 'verified_at' => 1,
    'challenge_kind' => 'slider', 'risk_level' => 'medium', 'risk_reasons' => ['NO_SIGNALS', 7]])]);
$res = $c->verify($req);
check($res->challengeKind === 'slider' && $res->riskLevel === 'medium' && $res->riskReasons === ['NO_SIGNALS'], 'risk fields');
[, $c] = $mk([ok(['success' => true, 'app_key' => 'app_demo', 'action' => 'login', 'verified_at' => 1])]);
$res = $c->verify($req);
check($res->challengeKind === null && $res->riskReasons === [], 'risk fields absent');

// Rejected, including reasons this SDK has never heard of.
[, $c] = $mk([ok(['success' => false, 'reason' => 'TOKEN_CONSUMED'])]);
$res = $c->verify($req);
check(!$res->passed() && $res->reason === 'TOKEN_CONSUMED', 'rejected');
[, $c] = $mk([ok(['success' => false, 'reason' => 'SOME_FUTURE_REASON'])]);
check(!$c->verify($req)->passed(), 'unknown reason is a rejection');

// A pass for another app or action is not a pass.
[, $c] = $mk([ok(['success' => true, 'app_key' => 'app_other', 'action' => 'login', 'verified_at' => 1])]);
check(thrown(fn() => $c->verify($req)) instanceof ProtocolException, 'mismatched pass');
[, $c] = $mk([new Response(200, [], 'not json')]);
check(thrown(fn() => $c->verify($req)) instanceof ProtocolException, 'non-JSON 200');

// Caller errors.
foreach ([400 => 'INVALID_REQUEST', 401 => 'NONCE_REUSED', 403 => 'APP_FORBIDDEN', 409 => 'IDEMPOTENCY_CONFLICT'] as $status => $code) {
    [, $c] = $mk([err($status, $code)]);
    $e = thrown(fn() => $c->verify($req));
    check($e instanceof CallerErrorException && $e->errorCode === $code && $e->httpStatus === $status, "caller error $status");
}

// Unknown outcomes; 429 carries Retry-After and is not retried.
[$t, $c] = $mk([err(429, 'RATE_LIMITED', ['retry-after' => '17'])], 3);
$e = thrown(fn() => $c->verify($req));
check($e instanceof UnknownResultException && $e->retryAfter === 17 && count($t->calls) === 1, '429');
[, $c] = $mk([new TransportException('timeout')]);
$e = thrown(fn() => $c->verify($req));
check($e instanceof UnknownResultException && !($e instanceof ProtocolException), 'network error');

// Retries re-sign with a new nonce but keep body and request_id.
[$t, $c] = $mk([err(503, 'UNAVAILABLE'), new TransportException('reset'), ok(['success' => true, 'app_key' => 'app_demo', 'action' => 'login', 'verified_at' => 5])], 2);
check($c->verify($req)->passed() && count($t->calls) === 3, 'retry until pass');
check($t->calls[0]['body'] === $t->calls[2]['body'], 'retry keeps body');
check($t->calls[0]['headers']['X-BG-Nonce'] !== $t->calls[2]['headers']['X-BG-Nonce'], 'retry re-signs');

check(thrown(fn() => new Client('http://gw', '', 'k')) instanceof InvalidArgumentException, 'empty secret id');

// One-line form with a configured app_key.
$t1 = new FakeTransport([ok(['success' => true, 'app_key' => 'app_demo', 'action' => 'login', 'verified_at' => 9])]);
$c1 = new Client('http://gw', 'sid_demo1234', 'key', ['transport' => $t1, 'app_key' => 'app_demo']);
$res = $c1->verifyToken('tok', 'login');
$sent = json_decode($t1->calls[0]['body'], true);
check($res->passed() && $sent['app_key'] === 'app_demo' && strlen($sent['request_id']) === 36, 'verifyToken');
check(thrown(fn() => (new Client('http://gw', 's', 'k'))->verifyToken('t', 'login')) instanceof LogicException, 'verifyToken needs app_key');

echo $failures === 0 ? "OK ($count checks)\n" : "FAILED $failures of $count\n";
exit($failures === 0 ? 0 : 1);
