<?php

declare(strict_types=1);

// "comment" always shows the rotate puzzle (see seed.sh).
require __DIR__ . '/../src/bootstrap.php';

handle_form('comment', fn(string $user) => "评论已发布（演示），作者 {$user}");
