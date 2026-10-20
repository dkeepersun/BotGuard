<?php

declare(strict_types=1);

namespace BotGuard\Laravel;

use BotGuard\Client;
use BotGuard\Exception\BotGuardException;
use BotGuard\VerifyResult;
use Psr\Log\LoggerInterface;

/**
 * Request-scoped entry point, bound as `scoped` so its memo never leaks
 * between requests (including under Octane). A token is single-use: the
 * same token and action are verified at most once per request, so running
 * validation twice cannot burn the token.
 */
final class BotGuard
{
    /** @var array<string, VerifyResult|BotGuardException> */
    private array $memo = [];
    private VerifyResult|BotGuardException|null $last = null;

    public function __construct(
        private readonly Client $client,
        private readonly LoggerInterface $log,
    ) {
    }

    /**
     * Full result: returns Passed/Rejected, throws CallerErrorException or
     * UnknownResultException. Use this when you want to branch on each case.
     */
    public function verify(string $token, string $action, ?string $requestId = null): VerifyResult
    {
        $key = $action . '|' . $token;
        if (!array_key_exists($key, $this->memo)) {
            try {
                $this->memo[$key] = $this->client->verifyToken($token, $action, $requestId);
            } catch (BotGuardException $e) {
                $this->memo[$key] = $e;
            }
        }
        $this->last = $this->memo[$key];
        if ($this->last instanceof BotGuardException) {
            throw $this->last;
        }
        return $this->last;
    }

    /**
     * Yidun-style boolean, like yidun_captcha()->verify(): true only for a
     * pass. Configuration and availability problems also return false (fail
     * closed) but are logged as errors, so they are not mistaken for users
     * failing the check.
     */
    public function passes(?string $token, string $action): bool
    {
        try {
            return $this->verify((string) $token, $action)->passed();
        } catch (BotGuardException $e) {
            $this->log->error('BotGuard verify failed: ' . $e->getMessage(), [
                'exception' => $e::class, 'http_status' => $e->httpStatus, 'code' => $e->errorCode, 'action' => $action,
            ]);
            return false;
        }
    }

    /** The last result or exception, e.g. to read riskLevel after a pass. */
    public function last(): VerifyResult|BotGuardException|null
    {
        return $this->last;
    }
}
