<?php

declare(strict_types=1);

// Register uses the "register" action, which always shows the slider (see seed.sh).
require __DIR__ . '/../src/bootstrap.php';

handle_form('register', fn(string $user) => "注册成功：已创建账号 {$user}（演示，不落库）");
