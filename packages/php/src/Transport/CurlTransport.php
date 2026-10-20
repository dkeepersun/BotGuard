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

    /** Seconds. Anything shorter is raised to this, so a timeout is never "unlimited". */
    public const MIN_TIMEOUT = 0.05;
    public const DEFAULT_TIMEOUT = 3.0;
    public const DEFAULT_CONNECT_TIMEOUT = 1.0;

    private readonly float $timeout;
    private readonly float $connectTimeout;

    public function __construct(float $timeout = self::DEFAULT_TIMEOUT, float $connectTimeout = self::DEFAULT_CONNECT_TIMEOUT)
    {
        // cURL treats 0 as "no limit": a zero, negative or NaN timeout would
        // let one slow gateway hang a PHP-FPM worker indefinitely.
        $this->timeout = self::seconds($timeout, self::DEFAULT_TIMEOUT);
        $this->connectTimeout = self::seconds($connectTimeout, self::DEFAULT_CONNECT_TIMEOUT);
    }

    /**
     * A usable timeout in seconds: non-numeric, non-finite or non-positive
     * values fall back to $default; tiny values are raised to MIN_TIMEOUT.
     */
    public static function seconds(mixed $value, float $default): float
    {
        if (is_string($value)) {
            $value = trim($value);
        }
        if (!is_int($value) && !is_float($value) && !(is_string($value) && is_numeric($value))) {
            return $default;
        }
        $s = (float) $value;
        if (!is_finite($s) || $s <= 0) {
            return $default;
        }
        return max($s, self::MIN_TIMEOUT);
    }

    public function post(string $url, array $headers, string $body): Response
    {
        $lines = ['Content-Type: application/json', 'Accept: application/json'];
        foreach ($headers as $name => $value) {
            $lines[] = $name . ': ' . $value;
        }
        $respHeaders = [];
        $respBody = '';
        $truncated = false;
        $ch = curl_init($url);
        curl_setopt_array($ch, [
            CURLOPT_POST => true,
            CURLOPT_POSTFIELDS => $body,
            CURLOPT_HTTPHEADER => $lines,
            CURLOPT_FOLLOWLOCATION => false,
            CURLOPT_PROTOCOLS => CURLPROTO_HTTP | CURLPROTO_HTTPS,
            // Sub-second timeouts need NOSIGNAL with the synchronous resolver.
            CURLOPT_NOSIGNAL => true,
            CURLOPT_TIMEOUT_MS => max(1, (int) round($this->timeout * 1000)),
            CURLOPT_CONNECTTIMEOUT_MS => max(1, (int) round($this->connectTimeout * 1000)),
            CURLOPT_HEADERFUNCTION => static function ($ch, string $line) use (&$respHeaders): int {
                $parts = explode(':', $line, 2);
                if (count($parts) === 2) {
                    $respHeaders[strtolower(trim($parts[0]))] = trim($parts[1]);
                }
                return strlen($line);
            },
            CURLOPT_WRITEFUNCTION => static function ($ch, string $chunk) use (&$respBody, &$truncated): int {
                if (strlen($respBody) + strlen($chunk) > self::MAX_RESPONSE) {
                    $truncated = true;
                    return 0; // aborts the transfer
                }
                $respBody .= $chunk;
                return strlen($chunk);
            },
        ]);
        $ok = curl_exec($ch);
        $status = (int) curl_getinfo($ch, CURLINFO_RESPONSE_CODE);
        $error = curl_error($ch);
        // An oversized body still came with a status line: report it, flagged,
        // instead of pretending no response arrived (which would be retried).
        if ($truncated && $status > 0) {
            return new Response($status, $respHeaders, $respBody, true);
        }
        if ($ok === false || $status === 0) {
            throw new TransportException($error !== '' ? $error : 'no HTTP response');
        }
        return new Response($status, $respHeaders, $respBody);
    }
}
