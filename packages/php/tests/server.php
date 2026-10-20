<?php

declare(strict_types=1);

// Router for `php -S`, started by run.php to exercise the real cURL transport.
// The first path segment picks the reply; every hit is appended to the file
// named by BG_TEST_HITS so the runner can count attempts.
$path = (string) parse_url($_SERVER['REQUEST_URI'], PHP_URL_PATH);
$hits = getenv('BG_TEST_HITS');
if (is_string($hits) && $hits !== '') {
    file_put_contents($hits, $path . "\n", FILE_APPEND);
}
header('Content-Type: application/json');
switch (explode('/', trim($path, '/'))[0]) {
    case 'big':
        echo json_encode(['code' => 'OK', 'data' => ['success' => false, 'reason' => 'TOKEN_INVALID', 'pad' => str_repeat('x', 70000)]]);
        break;
    case 'big401':
        http_response_code(401);
        echo json_encode(['code' => 'UNAUTHORIZED', 'message' => str_repeat('y', 70000)]);
        break;
    case 'r302':
        http_response_code(302);
        header('Location: http://127.0.0.1:9/v1/verify');
        break;
    case 'n404':
        http_response_code(404);
        header('Content-Type: text/html');
        echo '<html>404 Not Found</html>';
        break;
    case 'ok':
        $b = json_decode((string) file_get_contents('php://input'), true);
        echo json_encode(['code' => 'OK', 'data' => ['success' => true, 'app_key' => $b['app_key'], 'action' => $b['action'], 'verified_at' => 1]]);
        break;
    default:
        http_response_code(500);
        echo '{"code":"UNAVAILABLE"}';
}
