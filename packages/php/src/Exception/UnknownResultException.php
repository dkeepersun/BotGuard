<?php

declare(strict_types=1);

namespace BotGuard\Exception;

/**
 * 429, 5xx, network failure or timeout: the gateway may or may not have
 * consumed the token. Do not run the business operation; retry later with
 * the same request_id (the SDK re-signs each attempt).
 */
class UnknownResultException extends BotGuardException
{
    public function __construct(
        string $message,
        int $httpStatus = 0,
        ?string $errorCode = null,
        ?string $requestId = null,
        /** Seconds from Retry-After (delay-seconds or HTTP-date), if present and parseable. */
        public readonly ?int $retryAfter = null,
        ?\Throwable $previous = null,
    ) {
        parent::__construct($message, $httpStatus, $errorCode, $requestId, $previous);
    }
}
