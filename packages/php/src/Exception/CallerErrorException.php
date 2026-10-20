<?php

declare(strict_types=1);

namespace BotGuard\Exception;

/**
 * The request or the configuration is wrong: the contract's 400/401/403/409
 * (bad secret, clock skew, disabled app, reused request_id), any other 3xx
 * or 4xx except 429 (wrong endpoint, a proxy answering 404/405/413/415), or
 * a malformed app_key/action/request_id the SDK refused to send
 * (errorCode INVALID_REQUEST, httpStatus 0). Retrying will not help; alert
 * operators instead of telling the user they failed the check.
 */
final class CallerErrorException extends BotGuardException
{
}
