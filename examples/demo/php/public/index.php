<?php

declare(strict_types=1);

require __DIR__ . '/../src/bootstrap.php';
$cfg = demo_config();
$ready = demo_ready();
?>
<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>BotGuard 演示</title>
  <link rel="stylesheet" href="/assets/demo.css">
  <!-- 前端只需要引入组件，并给出 gateway 地址、公开的 app_key 和 action。 -->
  <script type="module" src="/botguard/botguard.js"></script>
  <script type="module" src="/assets/demo.js"></script>
</head>
<body>
<header>
  <h1>BotGuard 演示 <small>浏览器组件 + PHP SDK + gateway</small></h1>
  <div class="chips">
    <span class="chip">应用 <b>演示商城</b> <code><?= h($cfg['BG_APP_KEY'] ?? '-') ?></code></span>
    <span class="chip">secret_id <code><?= h($cfg['BG_SECRET_ID'] ?? '-') ?></code></span>
    <span class="chip">secret_key <code><?= h(mask($cfg['BG_SECRET_KEY'] ?? '-', 4)) ?></code>（只在 PHP 服务端）</span>
    <span class="chip">gateway <a href="<?= h($cfg['public_endpoint']) ?>/readyz" target="_blank"><?= h($cfg['public_endpoint']) ?></a></span>
  </div>
</header>

<?php if (!$ready): ?>
  <p class="warn">没有读到演示凭据（/shared/demo.env）。请确认 seed 服务已成功运行：<code>docker compose logs seed</code></p>
<?php endif; ?>

<main>
  <section class="card">
    <h2>① 模拟登录 / 注册</h2>
    <div class="tabs" role="tablist">
      <button type="button" class="tab active" data-action="login" data-endpoint="/login.php" data-submit="登录">登录 · 自适应</button>
      <button type="button" class="tab" data-action="register" data-endpoint="/register.php" data-submit="注册">注册 · 滑块</button>
      <button type="button" class="tab" data-action="post" data-endpoint="/post.php" data-submit="发帖">发帖 · 文字点选</button>
      <button type="button" class="tab" data-action="pay" data-endpoint="/pay.php" data-submit="支付">支付 · 图形点选</button>
      <button type="button" class="tab" data-action="comment" data-endpoint="/comment.php" data-submit="评论">评论 · 旋转对齐</button>
      <button type="button" class="tab" data-action="share" data-endpoint="/share.php" data-submit="分享">分享 · 推理拼图</button>
      <button type="button" class="tab" data-action="checkin" data-endpoint="/checkin.php" data-submit="签到">签到 · 语序点选</button>
      <button type="button" class="tab" data-action="coupon" data-endpoint="/coupon.php" data-submit="领取">领券 · 图标点选</button>
      <button type="button" class="tab" data-action="vote" data-endpoint="/vote.php" data-submit="投票">投票 · 空间推理</button>
    </div>
    <p class="hint" id="mode-hint">登录用 adaptive 模式：正常浏览器通常无感通过；风险中等时随机出滑块、旋转、文字点选或图形点选，风险高时出最难的图形点选。演示站不校验密码。</p>
    <label class="size-pick">显示方式
      <select id="size">
        <option value="default">默认（嵌入，最宽 360px）</option>
        <option value="compact">紧凑（H5 / App：宽 100%，图片高 120px）</option>
        <option value="popup">弹出层（点提交按钮时弹出）</option>
      </select>
    </label>
    <form id="login" autocomplete="off" data-endpoint="/login.php">
      <label>用户名 <input name="username" value="demo"></label>
      <label>密码 <input name="password" type="password" value="demo123"></label>
      <botguard-widget id="widget"
        endpoint="<?= h($cfg['public_endpoint']) ?>"
        app-key="<?= h($cfg['BG_APP_KEY'] ?? '') ?>"
        action="login"></botguard-widget>
      <button type="submit" id="submit">登录</button>
    </form>
    <div id="business" class="business" hidden></div>

    <h2>② 模拟攻击与误用</h2>
    <p class="hint">每个按钮由 PHP 服务端模拟一种攻击或配置错误，结果显示在右侧。标 ★ 的需要先点验证组件拿到一个新 token（不要点登录）。</p>
    <div class="attacks">
      <button data-scenario="no_token">不带 token 提交</button>
      <button data-scenario="forged_token">伪造 token</button>
      <button data-scenario="replay">重放已用过的 token</button>
      <button data-scenario="wrong_action" data-needs-token>★ 登录 token 用于注册</button>
      <button data-scenario="cross_app" data-needs-token>★ 商城 token 拿到论坛用</button>
      <button data-scenario="wrong_pair">用论坛密钥对冒充商城</button>
      <button data-scenario="wrong_secret">错误的 secret_key</button>
      <button data-scenario="replay_signed">重发已签名的请求</button>
      <button data-scenario="stale_signature">过期的签名时间戳</button>
      <button data-scenario="flood">刷签发接口 40 次</button>
      <button data-scenario="bot_solve">脚本自动求解 PoW</button>
    </div>

    <h2>③ 风险信号与滑块</h2>
    <p class="hint">「无头浏览器特征」「不带信号」「同一 IP 连续失败」「随机点击」由 PHP 服务端模拟脚本请求；「脚本找缺口」两个在本页面里运行，模拟 AI 或脚本在真实浏览器中用 canvas 分析图片、提交拖动轨迹。</p>
    <div class="attacks">
      <button data-scenario="headless_signals">无头浏览器特征</button>
      <button data-scenario="no_signals">不带信号直接调接口</button>
      <button data-scenario="failures_escalate">同一 IP 连续失败</button>
      <button data-bot="line">脚本找缺口 + 直线拖动</button>
      <button data-bot="human">脚本找缺口 + 仿人轨迹</button>
      <button data-scenario="random_click">脚本随机点击图形题</button>
    </div>
  </section>

  <section class="card">
    <h2>调用过程 <button id="clear" class="link">清空</button></h2>
    <p class="hint">浏览器端（蓝）是组件发出的事件；服务端（绿/橙/红/灰）是 PHP SDK 调用 /v1/verify 的结果，按契约分为 通过 / 拒绝 / 调用方错误 / 结果未知 四类。展开可看签名请求和原始响应。</p>
    <ol id="timeline"></ol>
  </section>
</main>
</body>
</html>
