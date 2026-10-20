<?php

declare(strict_types=1);

namespace BotGuard;

/**
 * BG1-HMAC-SHA256 request signing, the counterpart of the gateway's
 * internal/service/signature. Like Yidun, the secret key never leaves the
 * business server; unlike Yidun's MD5 over concatenated parameters, the HMAC
 * covers the exact request body bytes.
 */
final class Signer
{
    public const SCHEME = 'BG1-HMAC-SHA256';
    public const VERIFY_PATH = '/v1/verify';

    public static function canonical(string $method, string $path, int $timestamp, string $nonce, string $body): string
    {
        return self::SCHEME . "\n" . $method . "\n" . $path . "\n" . $timestamp . "\n" . $nonce . "\n" . hash('sha256', $body);
    }

    public static function sign(#[\SensitiveParameter] string $secretKey, string $canonical): string
    {
        return hash_hmac('sha256', $canonical, $secretKey);
    }

    /** A 32-character URL-safe nonce from a CSPRNG. */
    public static function newNonce(): string
    {
        return rtrim(strtr(base64_encode(random_bytes(24)), '+/', '-_'), '=');
    }

    /**
     * Headers for one verify request. Call again for every retry: a reused
     * nonce is rejected as a replay.
     *
     * @return array<string, string>
     */
    public static function headers(string $secretId, #[\SensitiveParameter] string $secretKey, string $body, ?int $timestamp = null, ?string $nonce = null): array
    {
        $timestamp ??= time();
        $nonce ??= self::newNonce();
        return [
            'X-BG-Secret-Id' => $secretId,
            'X-BG-Timestamp' => (string) $timestamp,
            'X-BG-Nonce' => $nonce,
            'X-BG-Signature' => self::sign($secretKey, self::canonical('POST', self::VERIFY_PATH, $timestamp, $nonce, $body)),
        ];
    }
}
