<?php

declare(strict_types=1);

// "vote" always shows one phase-three kind (see seed.sh).
require __DIR__ . '/../src/bootstrap.php';

handle_form('vote', fn(string $user) => "投票成功（演示），用户 {$user}");
