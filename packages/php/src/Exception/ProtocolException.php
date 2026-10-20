<?php

declare(strict_types=1);

namespace BotGuard\Exception;

/**
 * The response did not match the contract (not JSON, missing success, a pass
 * for a different app or action, a body over the 64 KiB cap, an unexpected
 * status such as 204). Treated as unknown, logged separately, never retried
 * automatically; httpStatus keeps the status that was received.
 */
final class ProtocolException extends UnknownResultException
{
}
