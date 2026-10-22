<?php

declare(strict_types=1);

namespace BotGuard\Laravel;

use BotGuard\Client;
use BotGuard\Exception\CallerErrorException;
use BotGuard\Laravel\View\Widget;
use Illuminate\Container\Container;
use Illuminate\Support\ServiceProvider;
use Psr\Log\LoggerInterface;
use Psr\Log\NullLogger;

final class BotGuardServiceProvider extends ServiceProvider
{
    /** Same floor as the SDK: a timeout is never zero, i.e. never unlimited. */
    private const MIN_TIMEOUT = 0.05;

    public function register(): void
    {
        $this->mergeConfigFrom(__DIR__ . '/../config/botguard.php', 'botguard');

        // Resolved lazily, on the first verify: a missing configuration must
        // not break booting, artisan or page rendering, only verification.
        $this->app->singleton(Client::class, function ($app) {
            $c = (array) $app['config']['botguard'];
            if (empty($c['secret_id']) || empty($c['secret_key']) || empty($c['app_key'])) {
                throw new CallerErrorException(
                    'BotGuard 未配置：请设置 BOTGUARD_APP_KEY、BOTGUARD_SECRET_ID、BOTGUARD_SECRET_KEY', 0, 'NOT_CONFIGURED');
            }
            return new Client((string) ($c['endpoint'] ?? ''), (string) $c['secret_id'], (string) $c['secret_key'], [
                'app_key' => (string) $c['app_key'],
                'timeout' => self::seconds($c['timeout'] ?? null, 3.0),
                'connect_timeout' => self::seconds($c['connect_timeout'] ?? null, 1.0),
                'retries' => max(0, (int) ($c['retries'] ?? 0)),
            ]);
        });
        $this->app->scoped(BotGuard::class, fn ($app) => new BotGuard(
            static fn (): Client => $app->make(Client::class),
            self::logger($app),
            self::accept($app['config']['botguard.accept'] ?? []),
        ));
    }

    public function boot(): void
    {
        $configPath = self::configPath($this->app, 'botguard.php');
        if ($configPath !== null) {
            $this->publishes([__DIR__ . '/../config/botguard.php' => $configPath], 'botguard-config');
        }

        $this->callAfterResolving('blade.compiler', static function ($blade): void {
            $blade->component('botguard', Widget::class);
        });

        // 'botguard_token' => 'botguard:login'. Implicit, so it also runs, and
        // fails, when the field is missing, empty or null: forgetting
        // `required` must not skip the check.
        $this->callAfterResolving('validator', static function ($validator): void {
            $validator->extendImplicit('botguard', static function ($attribute, $value, $parameters): bool {
                // 'botguard:' parses as [null], 'botguard:a,b' as two parameters.
                if (!is_array($parameters) || count($parameters) !== 1 || !is_string($parameters[0]) || trim($parameters[0]) === '') {
                    throw new \InvalidArgumentException('botguard 规则需要且只能有一个 action 参数，例如 botguard:login');
                }
                // The current container, not the one captured at boot (Octane).
                return Container::getInstance()->make(BotGuard::class)->passes($value, (string) $parameters[0]);
            }, '人机验证未通过，请重新验证');
        });
    }

    /**
     * Step-up classes to let through, from config/env: only an explicit
     * true ("true", "1", "on", "yes") counts.
     *
     * @return array{fallback: bool, degraded: bool, high_risk: bool}
     */
    private static function accept(mixed $value): array
    {
        $value = is_array($value) ? $value : [];
        $on = static fn (string $k): bool => filter_var($value[$k] ?? false, FILTER_VALIDATE_BOOLEAN);
        return ['fallback' => $on('fallback'), 'degraded' => $on('degraded'), 'high_risk' => $on('high_risk')];
    }

    /** Seconds from config/env: empty, non-numeric or non-positive values use the default. */
    private static function seconds(mixed $value, float $default): float
    {
        if (is_string($value)) {
            $value = trim($value);
        }
        if (!is_int($value) && !is_float($value) && !(is_string($value) && is_numeric($value))) {
            return $default;
        }
        $s = (float) $value;
        return is_finite($s) && $s > 0 ? max($s, self::MIN_TIMEOUT) : $default;
    }

    private static function logger($app): LoggerInterface
    {
        foreach (['log', LoggerInterface::class] as $id) {
            if ($app->bound($id)) {
                $logger = $app->make($id);
                if ($logger instanceof LoggerInterface) {
                    return $logger;
                }
            }
        }
        return new NullLogger();
    }

    /** config_path() without depending on the Foundation helpers. */
    private static function configPath($app, string $file): ?string
    {
        if (method_exists($app, 'configPath')) {
            return $app->configPath($file);
        }
        if (function_exists('config_path')) {
            return config_path($file);
        }
        return null;
    }
}
