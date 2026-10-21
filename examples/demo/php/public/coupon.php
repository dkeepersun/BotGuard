<?php

declare(strict_types=1);

// "coupon" always shows one phase-three kind (see seed.sh).
require __DIR__ . '/../src/bootstrap.php';

handle_form('coupon', fn(string $user) => "领券成功（演示）：满 100 减 10，用户 {$user}");
