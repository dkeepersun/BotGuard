<?php

declare(strict_types=1);

if (!function_exists('botguard')) {
    /** botguard()->passes($request->input('botguard_token'), 'login'), like yidun_captcha()->verify(). */
    function botguard(): \BotGuard\Laravel\BotGuard
    {
        return \Illuminate\Container\Container::getInstance()->make(\BotGuard\Laravel\BotGuard::class);
    }
}
