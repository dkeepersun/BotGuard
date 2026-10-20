<?php

declare(strict_types=1);

// "post" always shows the text-click puzzle (see seed.sh).
require __DIR__ . '/../src/bootstrap.php';

handle_form('post', fn(string $user) => "发帖成功：帖子已发布（演示，不落库），作者 {$user}");
