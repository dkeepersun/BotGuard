<?php

declare(strict_types=1);

namespace BotGuard\Laravel\Rules;

use BotGuard\Laravel\BotGuard;
use Illuminate\Container\Container;
use Illuminate\Contracts\Validation\ImplicitRule;

/**
 * 'botguard_token' => [new BotGuardToken('login')]
 *
 * Implicit: a missing, empty or null field fails instead of being skipped,
 * with or without `required`.
 *
 * Running this rule consumes the token, and Laravel keeps validating the
 * remaining attributes after one fails, wherever this rule is placed (`bail`
 * only stops the rules of a single attribute). Validate the other fields
 * first and this rule in a separate step, or use stopOnFirstFailure() with
 * this rule last, so a typo in another field does not burn the token.
 */
final class BotGuardToken implements ImplicitRule
{
    public function __construct(private readonly string $action)
    {
        if ($action === '') {
            throw new \InvalidArgumentException('BotGuardToken 需要 action，例如 new BotGuardToken(\'login\')');
        }
    }

    public function passes($attribute, $value): bool
    {
        return Container::getInstance()->make(BotGuard::class)->passes($value, $this->action);
    }

    public function message(): string
    {
        return '人机验证未通过，请重新验证';
    }
}
