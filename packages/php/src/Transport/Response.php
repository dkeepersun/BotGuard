<?php

declare(strict_types=1);

namespace BotGuard\Transport;

final class Response
{
    /**
     * @param array<string, string> $headers lower-cased names
     * @param bool $truncated the body exceeded the transport's size cap and was cut off
     */
    public function __construct(
        public readonly int $status,
        public readonly array $headers,
        public readonly string $body,
        public readonly bool $truncated = false,
    ) {
    }
}
