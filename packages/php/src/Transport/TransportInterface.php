<?php

declare(strict_types=1);

namespace BotGuard\Transport;

interface TransportInterface
{
    /**
     * POST a body and return the response. Throws TransportException when no
     * HTTP response was received.
     *
     * @param array<string, string> $headers
     */
    public function post(string $url, array $headers, string $body): Response;
}
