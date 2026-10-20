<?php

declare(strict_types=1);

namespace BotGuard\Laravel\Rules;

use BotGuard\Laravel\BotGuard;
use Illuminate\Contracts\Validation\Rule;

/**
 * 'botguard_token' => ['required', new BotGuardToken('login')]
 *
 * Put it last: Laravel stops at the first failing attribute only with
 * `bail`, and the token is consumed when this rule runs, so other fields
 * should be validated before it.
 */
final class BotGuardToken implements Rule
{
    public function __construct(private readonly string $action)
    {
    }

    public function passes($attribute, $value): bool
    {
        return app(BotGuard::class)->passes(is_string($value) ? $value : '', $this->action);
    }

    public function message(): string
    {
        return '人机验证未通过，请重新验证';
    }
}
