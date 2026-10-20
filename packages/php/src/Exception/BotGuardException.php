<?php

declare(strict_types=1);

namespace BotGuard\Exception;

/** Base of every SDK exception; none of them means "verification passed". */
abstract class BotGuardException extends \RuntimeException
{
    public function __construct(
        string $message,
        public readonly int $httpStatus = 0,
        public readonly ?string $errorCode = null,
        public readonly ?string $requestId = null,
        ?\Throwable $previous = null,
    ) {
        parent::__construct($message, 0, $previous);
    }
}
