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
    /**
     * Step-up reasons. The gateway passed these tokens, but how they were
     * earned is cheap for a bot: the accessible fallback and the
     * capacity-degraded path are proof of work only, and a high-risk client
     * may be automation that solved the hardest picture. passes() and the
     * validation rules treat them as failures unless config('botguard.accept')
     * allows them; verify() returns the gateway's result unchanged.
     */
    public const STEP_UP_FALLBACK = 'STEP_UP_FALLBACK';
    public const STEP_UP_DEGRADED = 'STEP_UP_DEGRADED';
    public const STEP_UP_HIGH_RISK = 'STEP_UP_HIGH_RISK';

    /** @var array<string, VerifyResult|BotGuardException> */
    private array $memo = [];
    private VerifyResult|BotGuardException|null $last = null;

    /**
     * @param Client|\Closure(): Client $client a client, or a resolver that is
     *   only called on the first verify; it throws CallerErrorException
     *   (NOT_CONFIGURED) when BotGuard is not configured, so booting the app
     *   and rendering pages never fail for that reason.
     */
    /**
     * @param array{fallback?: bool, degraded?: bool, high_risk?: bool} $accept
     *   step-up classes passes() lets through; all off by default.
     * @param list<string> $origins page origins a pass must come from, exact
     *   (https://shop.example.com) or https://*.example.com for any
     *   subdomain; empty checks nothing. See config('botguard.origins').
     */
    public function __construct(
        private Client|\Closure $client,
        private readonly LoggerInterface $log,
        private readonly array $accept = [],
        array $origins = [],
    ) {
        foreach ($origins as $o) {
            $o = rtrim(strtolower(trim((string) $o)), '/');
            if (self::validOriginPattern($o)) {
                $this->origins[] = $o;
            } else {
                $this->badOrigins[] = (string) $o;
            }
        }
    }

    /** @var list<string> */
    private array $origins = [];
    /** @var list<string> */
    private array $badOrigins = [];

    /**
     * Full result: returns Passed/Rejected, throws CallerErrorException
     * (including NOT_CONFIGURED and a malformed action) or
     * UnknownResultException. Use this when you want to branch on each case.
     *
     * $token is the raw submitted value: null, arrays and other non-strings,
     * like malformed strings, are a local TOKEN_INVALID rejection that never
     * reaches the gateway.
     */
    public function verify(mixed $token, string $action, ?string $requestId = null): VerifyResult
    {
        $token = is_string($token) ? $token : '';
        $key = $action . '|' . $token;
        if (!array_key_exists($key, $this->memo)) {
            try {
                $this->memo[$key] = $this->client()->verifyToken($token, $action, $requestId);
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
    public function passes(mixed $token, string $action): bool
    {
        try {
            $result = $this->verify($token, $action);
            if (!$result->passed()) {
                return false;
            }
            if ($this->badOrigins !== []) {
                $this->log->error('BotGuard config botguard.origins has malformed entries, every pass is refused: ' . implode(', ', $this->badOrigins), ['action' => $action]);
                return false;
            }
            if ($this->origins !== [] && !self::originAllowed($this->origins, $result->origin)) {
                $this->log->info('BotGuard pass from an origin outside botguard.origins, treated as a failure', [
                    'action' => $action, 'origin' => $result->origin,
                ]);
                return false;
            }
            $why = self::stepUp($result);
            if ($why !== null && !$this->accepts($why)) {
                $this->log->info('BotGuard pass needs a step-up, treated as a failure: ' . $why, [
                    'action' => $action, 'challenge_kind' => $result->challengeKind, 'risk_level' => $result->riskLevel,
                ]);
                return false;
            }
            return true;
        } catch (BotGuardException $e) {
            $this->log->error('BotGuard verify failed: ' . $e->getMessage(), [
                'exception' => $e::class, 'http_status' => $e->httpStatus, 'code' => $e->errorCode, 'action' => $action,
            ]);
            return false;
        }
    }

    /**
     * Whether $origin matches one of $patterns as the gateway matches allowed
     * origins: exact, or https://*.example.com for any longer host ending in
     * .example.com (not example.com itself). A null origin (older gateways)
     * matches nothing. Patterns must be lower case without a trailing slash.
     *
     * @param list<string> $patterns
     */
    public static function originAllowed(array $patterns, ?string $origin): bool
    {
        if ($origin === null || $origin === '') {
            return false;
        }
        $origin = strtolower($origin);
        foreach ($patterns as $p) {
            $i = strpos($p, '://*.');
            if ($i === false) {
                if ($p === $origin) {
                    return true;
                }
                continue;
            }
            $prefix = substr($p, 0, $i + 3);
            $suffix = substr($p, $i + 4);
            if (strlen($origin) > strlen($prefix) + strlen($suffix) && str_starts_with($origin, $prefix) && str_ends_with($origin, $suffix)) {
                return true;
            }
        }
        return false;
    }

    private static function validOriginPattern(string $p): bool
    {
        return preg_match('~^https?://(\*\.)?[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?$~', $p) === 1;
    }

    /**
     * The step-up reason a passed result carries, or null: fallback first,
     * then capacity degradation, then high risk.
     */
    public static function stepUp(VerifyResult $result): ?string
    {
        return match (true) {
            !$result->passed() => null,
            $result->challengeKind === 'fallback' => self::STEP_UP_FALLBACK,
            in_array('CAPACITY_DEGRADED', $result->riskReasons, true) => self::STEP_UP_DEGRADED,
            $result->riskLevel === 'high' => self::STEP_UP_HIGH_RISK,
            default => null,
        };
    }

    /**
     * Why the last passes() call failed although the gateway passed the
     * token, e.g. to offer an SMS code instead of another captcha; null when
     * it did not fail for a step-up.
     */
    public function lastStepUp(): ?string
    {
        if (!$this->last instanceof VerifyResult) {
            return null;
        }
        $why = self::stepUp($this->last);
        return $why !== null && !$this->accepts($why) ? $why : null;
    }

    private function accepts(string $why): bool
    {
        $key = [self::STEP_UP_FALLBACK => 'fallback', self::STEP_UP_DEGRADED => 'degraded', self::STEP_UP_HIGH_RISK => 'high_risk'][$why];
        return ($this->accept[$key] ?? false) === true;
    }

    /** The last result or exception, e.g. to read riskLevel after a pass. */
    public function last(): VerifyResult|BotGuardException|null
    {
        return $this->last;
    }

    private function client(): Client
    {
        if ($this->client instanceof \Closure) {
            $this->client = ($this->client)();
        }
        return $this->client;
    }
}
