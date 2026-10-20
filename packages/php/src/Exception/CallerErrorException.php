<?php

declare(strict_types=1);

namespace BotGuard\Exception;

/**
 * 400/401/403/409: the request or the configuration is wrong (bad secret,
 * clock skew, disabled app, reused request_id). Retrying will not help;
 * alert operators instead of telling the user they failed the check.
 */
final class CallerErrorException extends BotGuardException
{
}
