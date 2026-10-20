<?php

declare(strict_types=1);

namespace BotGuard\Transport;

final class Response
{
    /** @param array<string, string> $headers lower-cased names */
    public function __construct(
        public readonly int $status,
        public readonly array $headers,
        public readonly string $body,
    ) {
    }
}
