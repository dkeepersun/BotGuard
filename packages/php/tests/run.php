<?php

declare(strict_types=1);

// Dependency-free test runner: php packages/php/tests/run.php
require __DIR__ . '/../autoload.php';

use BotGuard\Client;
use BotGuard\Exception\CallerErrorException;
use BotGuard\Exception\ProtocolException;
use BotGuard\Exception\UnknownResultException;
use BotGuard\Signer;
use BotGuard\Transport\CurlTransport;
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

/** @return mixed the return value, or the exception thrown */
function outcome(callable $fn): mixed
{
    try {
        return $fn();
    } catch (Throwable $e) {
        return $e;
    }
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
    'challenge_kind' => 'slider', 'risk_level' => 'medium', 'risk_reasons' => ['NO_SIGNALS', 7], 'origin' => 'https://m.shop.example'])]);
$res = $c->verify($req);
check($res->challengeKind === 'slider' && $res->riskLevel === 'medium' && $res->riskReasons === ['NO_SIGNALS'], 'risk fields');
check($res->origin === 'https://m.shop.example' && $res->toArray()['origin'] === 'https://m.shop.example', 'origin field');
[, $c] = $mk([ok(['success' => true, 'app_key' => 'app_demo', 'action' => 'login', 'verified_at' => 1])]);
$res = $c->verify($req);
check($res->challengeKind === null && $res->riskReasons === [] && $res->origin === null, 'risk fields absent');

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

$pass = fn(int $at = 1) => ok(['success' => true, 'app_key' => 'app_demo', 'action' => 'login', 'verified_at' => $at]);

// Malformed tokens come from the browser: a local TOKEN_INVALID rejection,
// never a request (no JsonException on bad UTF-8, no 400 on oversized input).
$badTokens = ['bad UTF-8' => "\xff\xfe", 'truncated UTF-8' => "\xc3", 'empty' => '', 'over 256 bytes' => str_repeat('a', 257),
    'trailing newline' => "tok\n", 'space' => 'to k', 'non-ASCII' => 'tök', 'huge' => str_repeat('a', 20000)];
foreach ($badTokens as $name => $tok) {
    [$t, $c] = $mk([$pass()]);
    $res = outcome(fn() => $c->verify(new VerifyRequest('app_demo', $tok, 'login', 'req-1')));
    check($res instanceof BotGuard\VerifyResult && $res->rejected() && !$res->passed() && $res->reason === 'TOKEN_INVALID' && $res->requestId === 'req-1' && $t->calls === [], "local reject: $name");
}
foreach (['null' => null, 'array' => ['tok'], 'int' => 123, 'float' => 1.5, 'bool' => true, 'object' => new stdClass()] as $name => $tok) {
    $t1 = new FakeTransport([$pass()]);
    $c1 = new Client('http://gw', 'sid_demo1234', 'key', ['transport' => $t1, 'app_key' => 'app_demo']);
    $res = outcome(fn() => $c1->verifyToken($tok, 'login'));
    check($res instanceof BotGuard\VerifyResult && $res->reason === 'TOKEN_INVALID' && $t1->calls === [], "verifyToken $name token is a local rejection");
}
[$t, $c] = $mk([$pass()]);
check($c->verify(new VerifyRequest('app_demo', str_repeat('a', 256), 'login', 'req-1'))->passed() && count($t->calls) === 1, '256-byte token is sent');
[$t, $c] = $mk([$pass()]);
$issued = 'bgt1.app_demo.' . rtrim(strtr(base64_encode(random_bytes(32)), '+/', '-_'), '=');
check($c->verify(new VerifyRequest('app_demo', $issued, 'login', 'req-1'))->passed() && count($t->calls) === 1, 'gateway-shaped token is sent');

// Malformed app_key/action/request_id are hard-coded by the caller: CallerError, nothing sent.
$badCaller = ['action uppercase' => ['app_demo', 'Login', 'req-1'], 'action empty' => ['app_demo', '', 'req-1'],
    'action dash' => ['app_demo', 'log-in', 'req-1'], 'action 33 chars' => ['app_demo', str_repeat('a', 33), 'req-1'],
    'action newline' => ['app_demo', "login\n", 'req-1'], 'action bad UTF-8' => ['app_demo', "\xff", 'req-1'],
    'app_key short' => ['app', 'login', 'req-1'], 'request_id space' => ['app_demo', 'login', 'req 1'],
    'request_id 65 chars' => ['app_demo', 'login', str_repeat('r', 65)]];
foreach ($badCaller as $name => [$app, $action, $rid]) {
    [$t, $c] = $mk([$pass()]);
    $e = thrown(fn() => $c->verify(new VerifyRequest($app, "\xff", $action, $rid)));
    check($e instanceof CallerErrorException && $e->errorCode === 'INVALID_REQUEST' && $e->httpStatus === 0 && $t->calls === [], "caller field: $name");
}

// Any 3xx/4xx except 429 is a caller error: kept status and code, never retried.
foreach ([301, 302, 307, 404, 405, 413, 415, 422] as $status) {
    [$t, $c] = $mk([new Response($status, [], '<html>proxy page</html>'), $pass()], 3);
    $e = thrown(fn() => $c->verify($req));
    check($e instanceof CallerErrorException && $e->httpStatus === $status && $e->errorCode === null && count($t->calls) === 1, "HTTP $status is a caller error, not retried");
}
[$t, $c] = $mk([err(404, 'NOT_FOUND'), $pass()], 3);
$e = thrown(fn() => $c->verify($req));
check($e instanceof CallerErrorException && $e->errorCode === 'NOT_FOUND' && count($t->calls) === 1, '404 keeps the body error code');
foreach ([500, 502, 504] as $status) {
    [$t, $c] = $mk([err($status, 'UNAVAILABLE'), err($status, 'UNAVAILABLE'), err($status, 'UNAVAILABLE')], 2);
    $e = thrown(fn() => $c->verify($req));
    check($e instanceof UnknownResultException && !($e instanceof ProtocolException) && $e->httpStatus === $status && count($t->calls) === 3, "HTTP $status is unknown and retried");
}
foreach ([201, 204] as $status) {
    [$t, $c] = $mk([new Response($status, [], ''), $pass()], 3);
    $e = thrown(fn() => $c->verify($req));
    check($e instanceof ProtocolException && $e->httpStatus === $status && count($t->calls) === 1, "HTTP $status is a protocol error");
}

// A body over the transport cap keeps its status, is a protocol error and is not retried.
[$t, $c] = $mk([new Response(200, [], '{"code":"OK"', true), $pass()], 3);
$e = thrown(fn() => $c->verify($req));
check($e instanceof ProtocolException && $e->httpStatus === 200 && count($t->calls) === 1, 'truncated response');

// Retry-After: delay-seconds or HTTP-date; null when unparseable.
$retryAfter = function (string $value, int $status = 429) use ($mk, $req): ?int {
    [, $c] = $mk([err($status, 'RATE_LIMITED', ['retry-after' => $value])]);
    $e = thrown(fn() => $c->verify($req));
    return $e instanceof UnknownResultException ? $e->retryAfter : -1;
};
$ra = $retryAfter(gmdate('D, d M Y H:i:s \G\M\T', time() + 120));
check($ra !== null && $ra >= 115 && $ra <= 120, 'Retry-After IMF-fixdate');
$ra = $retryAfter(gmdate('l, d-M-y H:i:s \G\M\T', time() + 60));
check($ra !== null && $ra >= 55 && $ra <= 60, 'Retry-After RFC 850 date');
$ra = $retryAfter(gmdate('D M j H:i:s Y', time() + 60));
check($ra !== null && $ra >= 55 && $ra <= 60, 'Retry-After asctime date');
check($retryAfter('Wed, 21 Oct 2015 07:28:00 GMT') === 0, 'Retry-After date in the past');
check($retryAfter('soon') === null && $retryAfter('-5') === null && $retryAfter('') === null, 'Retry-After unparseable');
check($retryAfter('5', 503) === 5, 'Retry-After on 503');

// Timeouts: zero, empty, negative or junk fall back to defaults; tiny ones get a floor.
$timeouts = fn(array $options): mixed => outcome(function () use ($options): array {
    $transport = (new ReflectionProperty(Client::class, 'transport'))->getValue(new Client('http://gw', 'sid_demo1234', 'key', $options));
    return [(new ReflectionProperty(CurlTransport::class, 'timeout'))->getValue($transport),
        (new ReflectionProperty(CurlTransport::class, 'connectTimeout'))->getValue($transport)];
});
foreach (['0' => 0, 'empty string' => '', 'negative' => -1, 'junk' => 'abc', 'NAN' => NAN, 'INF' => INF, 'null' => null, 'false' => false] as $name => $v) {
    check($timeouts(['timeout' => $v, 'connect_timeout' => $v]) === [3.0, 1.0], "timeout $name uses defaults");
}
check($timeouts(['timeout' => 0.0001, 'connect_timeout' => '0.0004']) === [0.05, 0.05], 'tiny timeout raised to 50ms');
check($timeouts(['timeout' => '2.5', 'connect_timeout' => 2]) === [2.5, 2.0], 'numeric timeout kept');
$direct = new CurlTransport(0, -1);
check([(new ReflectionProperty(CurlTransport::class, 'timeout'))->getValue($direct), (new ReflectionProperty(CurlTransport::class, 'connectTimeout'))->getValue($direct)] === [3.0, 1.0], 'CurlTransport normalizes too');

// A gateway that accepts the connection but never answers must time out.
$silent = stream_socket_server('tcp://127.0.0.1:0', $errno, $errstr);
$silentUrl = 'http://' . stream_socket_get_name($silent, false);
foreach (['tiny timeout' => 0.0001, 'zero timeout' => 0] as $name => $v) {
    $t0 = microtime(true);
    $e = thrown(fn() => (new Client($silentUrl, 'sid_demo1234', 'key', ['timeout' => $v, 'connect_timeout' => $v]))->verify($req));
    $elapsed = microtime(true) - $t0;
    check($e instanceof UnknownResultException && $elapsed < ($v > 0 ? 1.5 : 4.5), "silent gateway, $name: gives up after " . round($elapsed, 2) . 's');
}
fclose($silent);

// Real cURL against a local `php -S`: oversized bodies and redirects.
$probe = stream_socket_server('tcp://127.0.0.1:0');
$addr = stream_socket_get_name($probe, false);
fclose($probe);
$hitsFile = tempnam(sys_get_temp_dir(), 'bg-hits');
$server = proc_open([PHP_BINARY, '-S', $addr, __DIR__ . '/server.php'], [1 => ['file', '/dev/null', 'w'], 2 => ['file', '/dev/null', 'w']],
    $pipes, null, ['BG_TEST_HITS' => $hitsFile] + getenv());
for ($i = 0; $i < 100 && !@fsockopen('tcp://' . $addr); $i++) {
    usleep(30000);
}
$live = function (string $case, int $retries = 2) use ($addr, $hitsFile, $req): array {
    file_put_contents($hitsFile, '');
    $e = thrown(fn() => (new Client("http://$addr/$case", 'sid_demo1234', 'key', ['retries' => $retries]))->verify($req));
    return [$e, count(file($hitsFile))];
};
[$e, $hits] = $live('ok');
check($e === null && $hits === 1, 'live server: pass');
[$e, $hits] = $live('big');
check($e instanceof ProtocolException && $e->httpStatus === 200 && $hits === 1, 'live: oversized 200 is a protocol error, not retried');
[$e, $hits] = $live('big401');
check($e instanceof ProtocolException && $e->httpStatus === 401 && $hits === 1, 'live: oversized 401 keeps its status');
[$e, $hits] = $live('r302');
check($e instanceof CallerErrorException && $e->httpStatus === 302 && $hits === 1, 'live: redirect is not followed and not retried');
[$e, $hits] = $live('n404');
check($e instanceof CallerErrorException && $e->httpStatus === 404 && $hits === 1, 'live: 404 is a caller error');
[$e, $hits] = $live('down', 1);
check($e instanceof UnknownResultException && $e->httpStatus === 500 && $hits === 2, 'live: 500 is retried');
proc_terminate($server);
proc_close($server);
@unlink($hitsFile);

// Secrets stay out of stack traces and dumps; curl_close() is gone (deprecated in PHP 8.5).
$sensitive = fn(string $class, string $method, int $i) => (new ReflectionMethod($class, $method))->getParameters()[$i]->getAttributes(SensitiveParameter::class) !== [];
check($sensitive(Signer::class, 'sign', 0) && $sensitive(Signer::class, 'headers', 1) && $sensitive(Client::class, '__construct', 2), 'secret_key parameters are #[SensitiveParameter]');
$secretClient = new Client('http://gw', 'sid_demo1234', 'TOP-SECRET-KEY-42', ['transport' => new FakeTransport([])]);
ob_start();
var_dump($secretClient);
$dump = ob_get_clean() . print_r($secretClient, true) . print_r(['nested' => $secretClient], true);
check(!str_contains($dump, 'TOP-SECRET-KEY-42') && str_contains($dump, '[REDACTED]') && str_contains($dump, 'sid_demo1234'), '__debugInfo hides secret_key');
check(!str_contains((string) file_get_contents(__DIR__ . '/../src/Transport/CurlTransport.php'), 'curl_close('), 'no curl_close');

echo $failures === 0 ? "OK ($count checks)\n" : "FAILED $failures of $count\n";
exit($failures === 0 ? 0 : 1);
