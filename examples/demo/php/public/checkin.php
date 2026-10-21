<?php

declare(strict_types=1);

// "checkin" always shows one phase-three kind (see seed.sh).
require __DIR__ . '/../src/bootstrap.php';

handle_form('checkin', fn(string $user) => "签到成功（演示），连续签到 1 天，用户 {$user}");
