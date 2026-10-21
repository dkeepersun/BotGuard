<?php

declare(strict_types=1);

namespace BotGuard;

/**
 * Passed or Rejected. Caller errors and unknown outcomes are exceptions, so a
 * misconfiguration can never be mistaken for "the user failed the check".
 */
final class VerifyResult
{
    /** Reason for a missing or malformed token; the SDK also returns it locally without calling the gateway. */
    public const TOKEN_INVALID = 'TOKEN_INVALID';

    private function __construct(
        private readonly bool $ok,
        /** Contract reason code when rejected, e.g. TOKEN_EXPIRED. Unknown codes are still rejections. */
        public readonly ?string $reason,
        public readonly ?string $appKey,
        public readonly ?string $action,
        public readonly ?int $verifiedAt,
        public readonly string $requestId,
        /** How the token was earned: invisible, slider or fallback (null for older gateways). */
        public readonly ?string $challengeKind = null,
        /** low / medium / high, as scored by the gateway. */
        public readonly ?string $riskLevel = null,
        /** @var list<string> reason codes such as NO_SIGNALS, FALLBACK_CHOSEN or CAPACITY_DEGRADED */
        public readonly array $riskReasons = [],
    ) {
    }

    /** @param list<string> $riskReasons */
    public static function ofPass(string $appKey, string $action, int $verifiedAt, string $requestId,
        ?string $challengeKind = null, ?string $riskLevel = null, array $riskReasons = []): self
    {
        return new self(true, null, $appKey, $action, $verifiedAt, $requestId, $challengeKind, $riskLevel, $riskReasons);
    }

    public static function ofReject(string $reason, string $requestId): self
    {
        return new self(false, $reason, null, null, null, $requestId);
    }

    /** Only a pass may let the business operation run. */
    public function passed(): bool
    {
        return $this->ok;
    }

    /** The token was refused (see $reason); ask the user to verify again. */
    public function rejected(): bool
    {
        return !$this->ok;
    }

    /** @return array<string, mixed> */
    public function toArray(): array
    {
        return [
            'passed' => $this->ok,
            'reason' => $this->reason,
            'app_key' => $this->appKey,
            'action' => $this->action,
            'verified_at' => $this->verifiedAt,
            'request_id' => $this->requestId,
            'challenge_kind' => $this->challengeKind,
            'risk_level' => $this->riskLevel,
            'risk_reasons' => $this->riskReasons,
        ];
    }
}
