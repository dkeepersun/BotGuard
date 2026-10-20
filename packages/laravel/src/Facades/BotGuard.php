<?php

declare(strict_types=1);

namespace BotGuard\Laravel\Facades;

use Illuminate\Support\Facades\Facade;

/**
 * @method static \BotGuard\VerifyResult verify(mixed $token, string $action, ?string $requestId = null)
 * @method static bool passes(mixed $token, string $action)
 * @method static \BotGuard\VerifyResult|\BotGuard\Exception\BotGuardException|null last()
 */
final class BotGuard extends Facade
{
    // The service is request-scoped; never keep one across requests (Octane).
    protected static $cached = false;

    protected static function getFacadeAccessor(): string
    {
        return \BotGuard\Laravel\BotGuard::class;
    }
}
