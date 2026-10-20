<?php

declare(strict_types=1);

// Login uses the "login" action, which adapts to risk (see seed.sh).
require __DIR__ . '/../src/bootstrap.php';

handle_form('login', fn(string $user) => "登录成功：欢迎，{$user}（演示站不校验密码）");
