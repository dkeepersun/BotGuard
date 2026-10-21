<?php

declare(strict_types=1);

// Simulated attacks and mistakes. Each scenario returns what was expected,
// what happened, and every HTTP exchange, so the page can show why it failed.
require __DIR__ . '/../src/bootstrap.php';

use BotGuard\Signer;
use BotGuard\VerifyRequest;

if ($_SERVER['REQUEST_METHOD'] !== 'POST') {
    json_out(['error' => 'POST only'], 405);
}
session_start();
$cfg = demo_config();
$scenario = (string) ($_POST['scenario'] ?? '');
$widgetToken = (string) ($_POST['token'] ?? '');
$rec = new RecordingTransport();
$app1 = fn() => client_for($cfg['BG_SECRET_ID'], $cfg['BG_SECRET_KEY'], $rec);
$verify = fn($client, string $appKey, string $token, string $action) => run_verify($client, new VerifyRequest($appKey, $token, $action, VerifyRequest::newRequestId()));
$needToken = fn() => json_out(['scenario' => $scenario, 'error' => '这个场景需要一个新的 token：请先点击左侧验证组件完成验证（不要点登录）']);

/** Sends a hand-signed verify request, bypassing the SDK. */
$rawVerify = function (string $body, array $headers) use ($cfg, $rec): array {
    try {
        $resp = $rec->post($cfg['internal_endpoint'] . Signer::VERIFY_PATH, $headers, $body);
        $json = json_decode($resp->body, true);
        $category = $resp->status === 200 ? (($json['data']['success'] ?? false) ? 'Passed' : 'Rejected') : ($resp->status < 429 ? 'CallerError' : 'Unknown');
        $labels = ['Passed' => '通过', 'Rejected' => '拒绝', 'CallerError' => '调用方错误', 'Unknown' => '结果未知'];
        return ['category' => $category, 'label' => $labels[$category], 'http' => $resp->status,
            'reason' => $json['data']['reason'] ?? $json['code'] ?? null, 'message' => $json['message'] ?? null];
    } catch (Throwable $e) {
        return ['category' => 'Unknown', 'label' => '结果未知', 'message' => $e->getMessage()];
    }
};

switch ($scenario) {
    case 'no_token':
        $r = ['title' => '不带 token 直接提交', 'expect' => 'Rejected · TOKEN_INVALID',
            'explain' => '机器人跳过前端组件、直接 POST 表单。缺失、为空或格式不对的 token 由 PHP SDK 在本地直接判为业务拒绝，不会请求 gateway（所以下面没有网络交互记录）；业务服务器据此拒绝登录。',
            'outcome' => $verify($app1(), $cfg['BG_APP_KEY'], '', 'login')];
        break;

    case 'forged_token':
        $fake = 'bgt1.' . $cfg['BG_APP_KEY'] . '.' . rtrim(strtr(base64_encode(random_bytes(32)), '+/', '-_'), '=');
        $r = ['title' => '伪造格式正确的 token', 'expect' => 'Rejected · TOKEN_INVALID',
            'explain' => 'token 是服务端生成的 256 位随机数，只在 Redis 中记录摘要；照着格式拼一个，gateway 查不到记录。',
            'outcome' => $verify($app1(), $cfg['BG_APP_KEY'], $fake, 'login')];
        break;

    case 'replay':
        $last = (string) ($_SESSION['last_token'] ?? '');
        if ($last === '') {
            json_out(['scenario' => $scenario, 'error' => '请先用左侧表单完成一次登录，再重放那次的 token']);
        }
        $r = ['title' => '重放上一次登录用过的 token', 'expect' => 'Rejected · TOKEN_CONSUMED',
            'explain' => 'token 是一次性的：核验通过时原子地标记为已消费，换一个 request_id 再用就会被拒。',
            'outcome' => $verify($app1(), $cfg['BG_APP_KEY'], $last, 'login')];
        break;

    case 'wrong_action':
        $widgetToken !== '' || $needToken();
        $r = ['title' => '拿登录的 token 去注册', 'expect' => 'Rejected · ACTION_MISMATCH',
            'explain' => 'token 绑定签发时的 action。业务服务器在 verify 时写死自己的 action，挪用到别的操作会被拒；这次拒绝不会消耗 token，它仍可用于登录。',
            'outcome' => $verify($app1(), $cfg['BG_APP_KEY'], $widgetToken, 'register')];
        break;

    case 'cross_app':
        $widgetToken !== '' || $needToken();
        $app2 = client_for($cfg['BG_SECRET2_ID'], $cfg['BG_SECRET2_KEY'], $rec);
        $r = ['title' => '把「演示商城」的 token 拿到「演示论坛」使用', 'expect' => 'Rejected · APP_MISMATCH',
            'explain' => '多应用隔离：论坛用自己的 app_key 和密钥对核验，但 token 属于商城，直接拒绝，且不会消耗商城的 token。',
            'outcome' => $verify($app2, $cfg['BG_APP2_KEY'], $widgetToken, 'login')];
        break;

    case 'wrong_pair':
        $app2 = client_for($cfg['BG_SECRET2_ID'], $cfg['BG_SECRET2_KEY'], $rec);
        $r = ['title' => '用论坛的密钥对去核验商城的 app_key', 'expect' => 'CallerError · 403 APP_FORBIDDEN',
            'explain' => '密钥对只属于一个应用。拿 A 应用的密钥对冒充 B 应用是配置错误或越权，返回 403，SDK 抛出 CallerErrorException。',
            'outcome' => $verify($app2, $cfg['BG_APP_KEY'], 'any', 'login')];
        break;

    case 'wrong_secret':
        $bad = client_for($cfg['BG_SECRET_ID'], 'this-is-not-the-secret-key', $rec);
        $r = ['title' => '用错误的 secret_key 签名', 'expect' => 'CallerError · 401 UNAUTHORIZED',
            'explain' => 'secret_key 从不出现在请求里，只用来算 HMAC 签名；签名对不上即 401。未知 secret_id 也返回同样的错误，无法用来探测。',
            'outcome' => $verify($bad, $cfg['BG_APP_KEY'], 'any', 'login')];
        break;

    case 'replay_signed':
        // Capture one signed request and send it twice, byte for byte.
        $body = (new VerifyRequest($cfg['BG_APP_KEY'], '', 'login', VerifyRequest::newRequestId()))->toJson();
        $headers = Signer::headers($cfg['BG_SECRET_ID'], $cfg['BG_SECRET_KEY'], $body);
        $first = $rawVerify($body, $headers);
        $second = $rawVerify($body, $headers);
        $r = ['title' => '截获一个已签名的 verify 请求并原样重发', 'expect' => '第二次 CallerError · 401 NONCE_REUSED',
            'explain' => "第一次正常处理（{$first['category']} · {$first['reason']}）。签名里的 nonce 在 Redis 中只能用一次，原样重发被判为重放。合法重试应由 SDK 重新签名。",
            'outcome' => $second];
        break;

    case 'stale_signature':
        $body = (new VerifyRequest($cfg['BG_APP_KEY'], '', 'login', VerifyRequest::newRequestId()))->toJson();
        $r = ['title' => '发送 10 分钟前签名的请求', 'expect' => 'CallerError · 401 TIMESTAMP_EXPIRED',
            'explain' => '签名带时间戳，与 gateway 时间相差超过 ±300 秒即拒绝，旧请求即使 nonce 没用过也无法重放；也能帮助发现服务器时钟不准。',
            'outcome' => $rawVerify($body, Signer::headers($cfg['BG_SECRET_ID'], $cfg['BG_SECRET_KEY'], $body, time() - 600))];
        break;

    case 'flood':
        $counts = [];
        for ($i = 0; $i < 40; $i++) {
            [$status] = $rec->browserCall($cfg['internal_endpoint'] . '/v1/challenges', 'http://localhost:8080', ['app_key' => $cfg['BG_APP_KEY'], 'action' => 'login']);
            $counts[$status] = ($counts[$status] ?? 0) + 1;
        }
        // Keep only a few exchanges so the page stays readable.
        $rec->calls = array_merge(array_slice($rec->calls, 0, 2), array_slice($rec->calls, -2));
        $ok = $counts[200] ?? 0;
        $limited = $counts[429] ?? 0;
        $r = ['title' => '脚本 1 秒内刷 40 次签发接口（伪造 Origin）', 'expect' => '同一 IP 每分钟累计超过 30 次后返回 429',
            'explain' => "结果：{$ok} 次成功、{$limited} 次被限流（演示配置为每 IP 每分钟 30 次）。注意脚本可以随意伪造 Origin 头，Origin 只约束浏览器，不能当作身份。限流按 IP 计，同一分钟内再点「脚本自动求解」也会被限流。",
            'outcome' => ['category' => $limited > 0 ? 'Rejected' : 'Passed', 'label' => $limited > 0 ? '已触发限流' : '未触发限流', 'reason' => "200×{$ok}，429×{$limited}"]];
        break;

    case 'bot_solve':
        $base = $cfg['internal_endpoint'];
        $origin = 'http://localhost:8080';
        // A script can forge "clean browser" signals; without them NO_SIGNALS
        // alone escalates it to an interactive puzzle.
        $signals = ['user_agent' => 'Mozilla/5.0 (Macintosh) Chrome/140', 'ms_to_click' => 2600,
            'pointer_moves_before_click' => 40, 'pointer_type' => 'mouse'];
        [$s1, $ch] = $rec->browserCall("$base/v1/challenges", $origin, ['app_key' => $cfg['BG_APP_KEY'], 'action' => 'login', 'signals' => $signals]);
        if ($s1 !== 200) {
            $r = ['title' => '脚本自动求解 PoW', 'expect' => 'Passed', 'explain' => '签发被拒（可能刚运行过「刷签发接口」，等一分钟限流窗口过去再试）。',
                'outcome' => ['category' => 'Unknown', 'label' => "签发失败 HTTP $s1", 'reason' => $ch['code'] ?? null]];
            break;
        }
        if (($ch['data']['kind'] ?? '') !== 'invisible') {
            // Other server-side scenarios (e.g. repeated failures) share this IP and raise its risk.
            $r = ['title' => '脚本不经浏览器，自己求解 PoW', 'expect' => 'Passed（这是 PoW 的真实局限）',
                'explain' => '这个 IP 刚有过失败记录（例如运行过「同一 IP 连续失败」），风险升高，服务端改出交互题；只会算 PoW 的脚本拿不到 token。等 10 分钟失败窗口过去再试，就能看到纯 PoW 被脚本攻破。',
                'outcome' => ['category' => 'Rejected', 'label' => '已升级', 'reason' => '本次出题：' . $ch['data']['kind']]];
            break;
        }
        $t0 = microtime(true);
        $sol = BotSolver::solve($ch['data']['algorithm'], $ch['data']['challenge_id'], $ch['data']['params']);
        $ms = (int) round((microtime(true) - $t0) * 1000);
        [, $s] = $rec->browserCall("$base/v1/solutions", $origin, ['app_key' => $cfg['BG_APP_KEY'], 'challenge' => $ch['data']['challenge'], 'nonces' => $sol['nonces']]);
        $token = $s['data']['token'] ?? '';
        $r = ['title' => '脚本不经浏览器，自己求解 PoW', 'expect' => 'Passed（这是 PoW 的真实局限）',
            'explain' => "脚本伪造了一份正常浏览器的信号，再用 PHP 在服务器上 {$ms} 毫秒完成了 " . number_format($sol['attempts']) . ' 次哈希，拿到了真 token。PoW 只提高批量请求的成本，不能区分人和机器；需要配合限流、按风险调高难度和业务规则。',
            'outcome' => $verify($app1(), $cfg['BG_APP_KEY'], $token, 'login')];
        break;

    case 'headless_signals':
    case 'no_signals':
        // The same adaptive "login" action, asked for by a clean browser and by a script.
        $base = $cfg['internal_endpoint'] . '/v1/challenges';
        $clean = ['user_agent' => 'Mozilla/5.0 (Macintosh) Chrome/140', 'ms_to_click' => 2600,
            'pointer_moves_before_click' => 40, 'pointer_type' => 'mouse'];
        $bad = $scenario === 'no_signals' ? null : $clean + ['webdriver' => true, 'headless_ua' => true, 'software_gl' => true];
        [, $a] = $rec->browserCall($base, 'http://localhost:8080', ['app_key' => $cfg['BG_APP_KEY'], 'action' => 'login', 'signals' => $clean]);
        $payload = ['app_key' => $cfg['BG_APP_KEY'], 'action' => 'login'];
        if ($bad !== null) {
            $payload['signals'] = $bad;
        }
        [$status, $b] = $rec->browserCall($base, 'http://localhost:8080', $payload);
        if ($status !== 200) {
            json_out(['scenario' => $scenario, 'error' => "签发失败 HTTP $status（可能刚运行过「刷签发接口」，等一分钟再试）", 'calls' => $rec->calls]);
        }
        $work = fn(array $c) => number_format($c['data']['params']['puzzles'] * round(2 ** 48 / max(1, hexdec(substr($c['data']['params']['target'], 0, 12)))));
        $kinds = ['invisible' => '无感', 'slider' => '滑块', 'rotate' => '旋转对齐', 'jigsaw' => '推理拼图', 'click_order' => '语序点选', 'click_icon' => '图标点选', 'spatial' => '空间推理', 'click_text' => '文字点选', 'click_shape' => '图形点选', 'fallback' => '备用'];
        $r = $scenario === 'no_signals'
            ? ['title' => '脚本直接调签发接口，不带任何浏览器信号', 'expect' => '正常浏览器：无感；脚本：随机一种交互题',
                'explain' => '没有信号本身就是信号（NO_SIGNALS +40）：真实浏览器里的组件一定会上报。中等风险从题型列表里随机出题，攻击者没法只针对一种题优化。风险分不会返回给浏览器。']
            : ['title' => '无头浏览器特征（webdriver、HeadlessChrome UA、软件渲染 WebGL）', 'expect' => '正常浏览器：无感；无头浏览器：图形点选 + PoW ×4',
                'explain' => '命中 WEBDRIVER、HEADLESS_UA、SOFTWARE_GL，风险分 100，进入高风险档：出列表中最难的图形点选，PoW 计算量也翻 4 倍。注意这些信号都能伪造，熟练的攻击者会把它们改掉，所以它们只是提高成本。'];
        $r['outcome'] = ['category' => 'Rejected', 'label' => '已升级',
            'reason' => "正常浏览器：{$kinds[$a['data']['kind']]}、约 {$work($a)} 次哈希；本次：{$kinds[$b['data']['kind']]}、约 {$work($b)} 次哈希"];
        break;

    case 'failures_escalate':
        // Three failed solves from this IP (the PHP container), then a clean browser request.
        $base = $cfg['internal_endpoint'];
        $origin = 'http://localhost:8080';
        $clean = ['ms_to_click' => 2600, 'pointer_moves_before_click' => 40, 'pointer_type' => 'mouse'];
        [, $before] = $rec->browserCall("$base/v1/challenges", $origin, ['app_key' => $cfg['BG_APP_KEY'], 'action' => 'login', 'signals' => $clean]);
        for ($i = 0; $i < 3; $i++) {
            [$st, $ch] = $rec->browserCall("$base/v1/challenges", $origin, ['app_key' => $cfg['BG_APP_KEY'], 'action' => 'register', 'signals' => $clean]);
            if ($st !== 200) {
                json_out(['scenario' => $scenario, 'error' => "签发失败 HTTP $st（可能刚运行过「刷签发接口」，等一分钟再试）", 'calls' => $rec->calls]);
            }
            $rec->browserCall("$base/v1/solutions", $origin, ['app_key' => $cfg['BG_APP_KEY'], 'challenge' => $ch['data']['challenge'],
                'nonces' => array_fill(0, $ch['data']['params']['puzzles'], '0'), 'answer' => ['x' => 0, 'trajectory' => []]]);
        }
        [, $after] = $rec->browserCall("$base/v1/challenges", $origin, ['app_key' => $cfg['BG_APP_KEY'], 'action' => 'login', 'signals' => $clean]);
        $r = ['title' => '同一 IP 连续 3 次求解失败后再来', 'expect' => '之前：无感；之后：交互题（随机）',
            'explain' => 'gateway 按 IP 记录 10 分钟内的失败次数（IP_RECENT_FAILURES，每次 +10，最多 +30），这是攻击者无法伪造的服务端信号。即使浏览器信号完全正常，也会被升级。这里的 IP 是 PHP 容器，不影响你自己的浏览器。',
            'outcome' => ['category' => 'Rejected', 'label' => '已升级', 'reason' => "之前：{$before['data']['kind']}，之后：{$after['data']['kind']}"]];
        $rec->calls = array_merge(array_slice($rec->calls, 0, 1), array_slice($rec->calls, 1, 2), array_slice($rec->calls, -1));
        break;

    case 'random_click':
        // A script that cannot read the image clicks at random, but with
        // plausible timing, against the shape-counting puzzle.
        $base = $cfg['internal_endpoint'];
        $origin = 'http://localhost:8080';
        $clean = ['ms_to_click' => 2600, 'pointer_moves_before_click' => 40, 'pointer_type' => 'mouse'];
        [$st, $ch] = $rec->browserCall("$base/v1/challenges", $origin, ['app_key' => $cfg['BG_APP_KEY'], 'action' => 'pay', 'signals' => $clean]);
        if ($st !== 200) {
            json_out(['scenario' => $scenario, 'error' => "签发失败 HTTP $st（可能刚运行过「刷签发接口」，等一分钟再试）", 'calls' => $rec->calls]);
        }
        $c = $ch['data'];
        $pow = BotSolver::solve($c['algorithm'], $c['challenge_id'], $c['params']);
        $clicks = [];
        $t = 900;
        for ($i = 0; $i < $c['click']['targets']; $i++) {
            $t += random_int(500, 1200);
            $clicks[] = [$t, random_int(10, $c['click']['width'] - 10), random_int(10, $c['click']['height'] - 10)];
        }
        [$st] = $rec->browserCall("$base/v1/solutions", $origin, ['app_key' => $cfg['BG_APP_KEY'], 'challenge' => $c['challenge'], 'nonces' => $pow['nonces'],
            'answer' => ['clicks' => $clicks, 'moves_between' => array_fill(0, count($clicks), 12)], 'signals' => $clean]);
        $r = ['title' => '脚本对图形点选题随机点击', 'expect' => 'ANSWER_INVALID',
            'explain' => "题目是「{$c['click']['prompt']}」。脚本读得懂文字提示，但不会数点数和边数、也找不到位置，随机点 {$c['click']['targets']} 下几乎不可能全中（还要顺序正确）。这道题对通用多模态大模型也最难；但专门训练的检测模型可以攻破它，所以它仍然只是提高成本。",
            'outcome' => ['category' => $st === 200 ? 'Passed' : 'Rejected', 'label' => $st === 200 ? '意外通过' : '拒绝（solve 阶段）', 'http' => $st, 'reason' => $st === 422 ? 'ANSWER_INVALID' : null]];
        break;

    case 'verify_token':
        // Used by the in-page script scenarios: verify whatever token they obtained.
        $action = in_array($_POST['action'] ?? '', ['login', 'register'], true) ? $_POST['action'] : 'register';
        $r = ['title' => '业务服务器核验脚本拿到的 token', 'expect' => '看 risk_level 和 risk_reasons',
            'explain' => '即使脚本通过了滑块，业务服务器仍能从 verify 结果中看到题型和风险原因，可据此追加验证。',
            'outcome' => $verify($app1(), $cfg['BG_APP_KEY'], $widgetToken, $action)];
        break;

    default:
        json_out(['error' => '未知场景'], 400);
}

json_out(['scenario' => $scenario] + $r + ['calls' => $rec->calls]);
