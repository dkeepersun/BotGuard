<?php
// Step-up policy of BotGuard\Laravel\BotGuard, without a Laravel install:
// php packages/laravel/tests/stepup.php (monorepo only; uses packages/php).
declare(strict_types=1);
namespace Psr\Log { interface LoggerInterface { public function info($m, array $c = []); public function error($m, array $c = []); } }
namespace {
require __DIR__ . '/../../php/autoload.php';
require __DIR__ . '/../src/BotGuard.php';
use BotGuard\Client; use BotGuard\Laravel\BotGuard; use BotGuard\Transport\{TransportInterface, Response};
final class Log implements Psr\Log\LoggerInterface { public array $lines = []; public function info($m, array $c = []) { $this->lines[] = "info: $m"; } public function error($m, array $c = []) { $this->lines[] = "error: $m"; } }
final class Fake implements TransportInterface {
    public function __construct(private string $extra) {}
    public function post(string $url, array $headers, string $body): Response {
        $b = json_decode($body, true);
        return new Response(200, [], '{"code":"OK","data":{"success":true,"app_key":"' . $b['app_key'] . '","action":"' . $b['action'] . '","verified_at":1' . $this->extra . '}}');
    }
}
$fails = 0;
$check = function (string $name, bool $ok) use (&$fails) { echo ($ok ? 'ok   ' : 'FAIL ') . $name . "\n"; if (!$ok) $fails++; };
$client = fn (string $extra) => new Client('https://gw.example', 'key_test_1', 'secret', ['app_key' => 'app_test_1', 'transport' => new Fake($extra)]);
$cases = [
    'fallback' => [',"challenge_kind":"fallback","risk_level":"low","risk_reasons":["FALLBACK_CHOSEN"]', BotGuard::STEP_UP_FALLBACK],
    'degraded' => [',"challenge_kind":"invisible","risk_level":"low","risk_reasons":["CAPACITY_DEGRADED"]', BotGuard::STEP_UP_DEGRADED],
    'high_risk' => [',"challenge_kind":"spatial","risk_level":"high","risk_reasons":["WEBDRIVER"]', BotGuard::STEP_UP_HIGH_RISK],
];
foreach ($cases as $key => [$extra, $why]) {
    $log = new Log();
    $g = new BotGuard($client($extra), $log);
    $check("$key rejected by default", $g->passes('tok', 'login') === false && $g->lastStepUp() === $why && str_contains(implode("\n", $log->lines), $why));
    $check("$key verify() unchanged", $g->verify('tok', 'login')->passed() === true);
    $g = new BotGuard($client($extra), new Log(), [$key => true]);
    $check("$key allowed when accepted", $g->passes('tok', 'login') === true && $g->lastStepUp() === null);
    $g = new BotGuard($client($extra), new Log(), [$key => 'true']);
    $check("$key: only bool true accepts (provider casts env strings)", $g->passes('tok', 'login') === false);
}
$g = new BotGuard($client(',"challenge_kind":"slider","risk_level":"medium"'), new Log());
$check('ordinary pass', $g->passes('tok', 'login') === true && $g->lastStepUp() === null);
$g = new BotGuard($client(''), new Log());
$check('older gateway without risk fields', $g->passes('tok', 'login') === true);
echo $fails ? "$fails FAILED\n" : "all passed\n";
exit($fails ? 1 : 0);
}
