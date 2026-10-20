<?php

declare(strict_types=1);

use BotGuard\Transport\CurlTransport;
use BotGuard\Transport\Response;
use BotGuard\Transport\TransportException;
use BotGuard\Transport\TransportInterface;

/**
 * Demo-only transport: forwards to cURL and keeps a copy of each exchange so
 * the page can show the signed request and the raw response. Real
 * integrations should use the default transport.
 */
final class RecordingTransport implements TransportInterface
{
    /** @var list<array<string, mixed>> */
    public array $calls = [];
    private CurlTransport $inner;

    public function __construct()
    {
        $this->inner = new CurlTransport(3.0, 1.0);
    }

    public function post(string $url, array $headers, string $body): Response
    {
        $t0 = microtime(true);
        $call = ['method' => 'POST', 'url' => $url, 'request_headers' => $headers, 'request_body' => json_decode($body, true) ?? $body];
        try {
            $resp = $this->inner->post($url, $headers, $body);
            $call += ['status' => $resp->status, 'response_body' => json_decode($resp->body, true) ?? $resp->body];
            return $resp;
        } catch (TransportException $e) {
            $call += ['status' => 0, 'response_body' => '网络错误：' . $e->getMessage()];
            throw $e;
        } finally {
            $call['ms'] = (int) round((microtime(true) - $t0) * 1000);
            $this->calls[] = $call;
        }
    }

    /**
     * A plain call to a browser endpoint, recorded the same way. Used by the
     * bot scenarios, which talk to the gateway exactly like a script would.
     *
     * @param array<string, mixed> $payload
     * @return array{0: int, 1: mixed}
     */
    public function browserCall(string $url, string $origin, array $payload): array
    {
        $t0 = microtime(true);
        $body = json_encode($payload, JSON_UNESCAPED_SLASHES);
        $ch = curl_init($url);
        curl_setopt_array($ch, [
            CURLOPT_POST => true,
            CURLOPT_POSTFIELDS => $body,
            CURLOPT_HTTPHEADER => ['Content-Type: text/plain', 'Origin: ' . $origin],
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_TIMEOUT => 3,
        ]);
        $raw = curl_exec($ch);
        $status = (int) curl_getinfo($ch, CURLINFO_RESPONSE_CODE);
        curl_close($ch);
        $decoded = is_string($raw) ? (json_decode($raw, true) ?? $raw) : null;
        $this->calls[] = ['method' => 'POST', 'url' => $url, 'request_headers' => ['Origin' => $origin . '（脚本伪造）'],
            'request_body' => $payload, 'status' => $status, 'response_body' => $decoded, 'ms' => (int) round((microtime(true) - $t0) * 1000)];
        return [$status, $decoded];
    }
}
