<?php

declare(strict_types=1);

namespace BotGuard\Exception;

/**
 * The response did not match the contract (not JSON, missing success, a pass
 * for a different app or action). Treated as unknown, logged separately.
 */
final class ProtocolException extends UnknownResultException
{
}
