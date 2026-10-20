<?php

declare(strict_types=1);

namespace BotGuard\Laravel;

use BotGuard\Client;
use BotGuard\Laravel\View\Widget;
use Illuminate\Support\Facades\Blade;
use Illuminate\Support\Facades\Validator;
use Illuminate\Support\ServiceProvider;

final class BotGuardServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        $this->mergeConfigFrom(__DIR__ . '/../config/botguard.php', 'botguard');

        $this->app->singleton(Client::class, function ($app) {
            $c = $app['config']['botguard'];
            if (empty($c['secret_id']) || empty($c['secret_key']) || empty($c['app_key'])) {
                throw new \RuntimeException('BotGuard 未配置：请设置 BOTGUARD_APP_KEY、BOTGUARD_SECRET_ID、BOTGUARD_SECRET_KEY');
            }
            return new Client($c['endpoint'], $c['secret_id'], $c['secret_key'], [
                'app_key' => $c['app_key'],
                'timeout' => (float) $c['timeout'],
                'retries' => (int) $c['retries'],
            ]);
        });
        $this->app->scoped(BotGuard::class, fn ($app) => new BotGuard($app->make(Client::class), $app->make('log')));
    }

    public function boot(): void
    {
        $this->publishes([__DIR__ . '/../config/botguard.php' => config_path('botguard.php')], 'botguard-config');

        Blade::component('botguard', Widget::class);

        // 'botguard_token' => 'required|botguard:login'
        Validator::extend('botguard', function ($attribute, $value, $parameters) {
            return $this->app->make(BotGuard::class)->passes(is_string($value) ? $value : '', $parameters[0] ?? 'default');
        }, '人机验证未通过，请重新验证');
    }
}
