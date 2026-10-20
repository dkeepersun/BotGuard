<?php

declare(strict_types=1);

// For projects without Composer: require this file instead of vendor/autoload.php.
spl_autoload_register(static function (string $class): void {
    if (!str_starts_with($class, 'BotGuard\\')) {
        return;
    }
    $file = __DIR__ . '/src/' . str_replace('\\', '/', substr($class, strlen('BotGuard\\'))) . '.php';
    if (is_file($file)) {
        require $file;
    }
});
