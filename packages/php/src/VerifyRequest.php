<?php

declare(strict_types=1);

namespace BotGuard;

/**
 * One verify call. Keep requestId stable for the whole business operation:
 * retries after an unknown result must reuse it, never mint a new one.
 */
final class VerifyRequest
{
    public function __construct(
        public readonly string $appKey,
        public readonly string $token,
        public readonly string $action,
        public readonly string $requestId,
    ) {
    }

    /** A UUID v4, suitable as request_id. */
    public static function newRequestId(): string
    {
        $b = random_bytes(16);
        $b[6] = chr((ord($b[6]) & 0x0f) | 0x40);
        $b[8] = chr((ord($b[8]) & 0x3f) | 0x80);
        return vsprintf('%s%s-%s-%s-%s-%s%s%s', str_split(bin2hex($b), 4));
    }

    public function toJson(): string
    {
        return json_encode([
            'app_key' => $this->appKey,
            'token' => $this->token,
            'action' => $this->action,
            'request_id' => $this->requestId,
        ], JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE | JSON_THROW_ON_ERROR);
    }
}
