<?php

declare(strict_types=1);

namespace BotGuard\Transport;

/**
 * cURL transport: no redirects (they could carry signature headers to another
 * host), bounded timeouts and a capped response size.
 */
final class CurlTransport implements TransportInterface
{
    private const MAX_RESPONSE = 65536;

    public function __construct(
        private readonly float $timeout = 3.0,
        private readonly float $connectTimeout = 1.0,
    ) {
    }

    public function post(string $url, array $headers, string $body): Response
    {
        $lines = ['Content-Type: application/json', 'Accept: application/json'];
        foreach ($headers as $name => $value) {
            $lines[] = $name . ': ' . $value;
        }
        $respHeaders = [];
        $respBody = '';
        $ch = curl_init($url);
        curl_setopt_array($ch, [
            CURLOPT_POST => true,
            CURLOPT_POSTFIELDS => $body,
            CURLOPT_HTTPHEADER => $lines,
            CURLOPT_FOLLOWLOCATION => false,
            CURLOPT_PROTOCOLS => CURLPROTO_HTTP | CURLPROTO_HTTPS,
            CURLOPT_TIMEOUT_MS => (int) ($this->timeout * 1000),
            CURLOPT_CONNECTTIMEOUT_MS => (int) ($this->connectTimeout * 1000),
            CURLOPT_HEADERFUNCTION => static function ($ch, string $line) use (&$respHeaders): int {
                $parts = explode(':', $line, 2);
                if (count($parts) === 2) {
                    $respHeaders[strtolower(trim($parts[0]))] = trim($parts[1]);
                }
                return strlen($line);
            },
            CURLOPT_WRITEFUNCTION => static function ($ch, string $chunk) use (&$respBody): int {
                if (strlen($respBody) + strlen($chunk) > self::MAX_RESPONSE) {
                    return 0; // aborts the transfer
                }
                $respBody .= $chunk;
                return strlen($chunk);
            },
        ]);
        $ok = curl_exec($ch);
        $status = (int) curl_getinfo($ch, CURLINFO_RESPONSE_CODE);
        $error = curl_error($ch);
        curl_close($ch);
        if ($ok === false || $status === 0) {
            throw new TransportException($error !== '' ? $error : 'no HTTP response');
        }
        return new Response($status, $respHeaders, $respBody);
    }
}
