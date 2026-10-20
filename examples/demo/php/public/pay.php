<?php

declare(strict_types=1);

// "pay" always shows the shape-click puzzle (see seed.sh).
require __DIR__ . '/../src/bootstrap.php';

handle_form('pay', fn(string $user) => "支付成功：订单已提交（演示，不扣款），付款人 {$user}");
