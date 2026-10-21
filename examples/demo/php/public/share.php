<?php

declare(strict_types=1);

// "share" always shows one phase-three kind (see seed.sh).
require __DIR__ . '/../src/bootstrap.php';

handle_form('share', fn(string $user) => "分享成功（演示），用户 {$user}");
