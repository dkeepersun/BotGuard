// BotGuard console page behaviour: charts, dialogs, confirmations, copy,
// tabs, live tables. Plain DOM, no dependencies; data comes from
// <script type="application/json"> blocks, so pages need no inline code.
(function () {
  'use strict';
  const $ = (sel, root = document) => root.querySelector(sel);
  const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));
  const svgNS = 'http://www.w3.org/2000/svg';
  const el = (tag, attrs = {}, parent) => {
    const n = document.createElementNS(svgNS, tag);
    for (const [k, v] of Object.entries(attrs)) n.setAttribute(k, v);
    if (parent) parent.appendChild(n);
    return n;
  };
  const fmt = (n) => Number(n || 0).toLocaleString('zh-CN');
  const data = (id) => {
    const node = document.getElementById(id);
    return node ? JSON.parse(node.textContent) : null;
  };

  // ---------- Toast (flash messages) ----------
  $$('.toast').forEach((t) => {
    setTimeout(() => t.classList.add('hide'), 4200);
    setTimeout(() => t.remove(), 4600);
  });

  // ---------- Line chart ----------
  function niceMax(v) {
    if (v <= 4) return 4;
    const p = Math.pow(10, Math.floor(Math.log10(v)));
    for (const m of [1, 2, 2.5, 5, 10]) if (m * p >= v) return m * p;
    return 10 * p;
  }

  function lineChart(box) {
    const src = data(box.dataset.src);
    if (!src) return;
    const series = box.dataset.series.split(',').map((s) => {
      const [key, name, color] = s.split(':');
      return { key, name, color, on: true, values: (src.lines && src.lines[key]) || [] };
    });
    const labels = src.labels || [];
    const legend = document.createElement('div');
    legend.className = 'legend';
    box.before(legend);
    const tip = document.createElement('div');
    tip.className = 'chart-tip';
    box.appendChild(tip);

    function draw() {
      box.querySelector('svg')?.remove();
      const W = box.clientWidth || 600, H = Number(box.dataset.height || 240);
      const pad = { l: 44, r: 12, t: 12, b: 26 };
      const svg = el('svg', { viewBox: `0 0 ${W} ${H}`, height: H });
      box.prepend(svg);
      const visible = series.filter((s) => s.on);
      const top = niceMax(Math.max(0, ...visible.flatMap((s) => s.values)));
      const n = Math.max(1, labels.length - 1);
      const x = (i) => pad.l + (i / n) * (W - pad.l - pad.r);
      const y = (v) => pad.t + (1 - v / top) * (H - pad.t - pad.b);
      const grid = el('g', { class: 'grid' }, svg);
      const axis = el('g', { class: 'axis' }, svg);
      for (let k = 0; k <= 4; k++) {
        const v = (top / 4) * k;
        el('line', { x1: pad.l, x2: W - pad.r, y1: y(v), y2: y(v) }, grid);
        const t = el('text', { x: pad.l - 8, y: y(v) + 4, 'text-anchor': 'end' }, axis);
        t.textContent = v >= 10000 ? (v / 10000).toFixed(v % 10000 ? 1 : 0) + '万' : fmt(v);
      }
      const every = Math.max(1, Math.ceil(labels.length / Math.max(2, Math.floor((W - pad.l) / 70))));
      labels.forEach((l, i) => {
        if (i % every !== 0 && i !== labels.length - 1) return;
        const t = el('text', { x: x(i), y: H - 6, 'text-anchor': 'middle' }, axis);
        t.textContent = l;
      });
      for (const s of visible) {
        if (!s.values.length) continue;
        const pts = s.values.map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`);
        const area = el('path', { d: `M${x(0)},${y(0)} L${pts.join(' L')} L${x(s.values.length - 1)},${y(0)} Z`, fill: s.color, opacity: 0.07 }, svg);
        area.style.pointerEvents = 'none';
        el('polyline', { points: pts.join(' '), fill: 'none', stroke: s.color, 'stroke-width': 2, 'stroke-linejoin': 'round', 'stroke-linecap': 'round' }, svg);
      }
      const cursor = el('line', { y1: pad.t, y2: H - pad.b, stroke: '#94a3b8', 'stroke-dasharray': '3 3', visibility: 'hidden' }, svg);
      const hit = el('rect', { x: pad.l, y: pad.t, width: W - pad.l - pad.r, height: H - pad.t - pad.b, fill: 'transparent' }, svg);
      hit.addEventListener('mousemove', (e) => {
        const r = svg.getBoundingClientRect();
        const px = ((e.clientX - r.left) / r.width) * W;
        const i = Math.max(0, Math.min(labels.length - 1, Math.round(((px - pad.l) / (W - pad.l - pad.r)) * n)));
        cursor.setAttribute('x1', x(i));
        cursor.setAttribute('x2', x(i));
        cursor.setAttribute('visibility', 'visible');
        tip.innerHTML = `<div>${labels[i] || ''}</div>` + visible.map((s) => `<div><i style="background:${s.color}"></i>${s.name}：${fmt(s.values[i])}</div>`).join('');
        tip.style.display = 'block';
        tip.style.left = `${(x(i) / W) * r.width}px`;
        tip.style.top = `${pad.t + 10}px`;
      });
      hit.addEventListener('mouseleave', () => {
        cursor.setAttribute('visibility', 'hidden');
        tip.style.display = 'none';
      });
    }

    legend.innerHTML = series.map((s, i) => `<span data-i="${i}"><i style="background:${s.color}"></i>${s.name}</span>`).join('');
    legend.addEventListener('click', (e) => {
      const span = e.target.closest('span');
      if (!span) return;
      const s = series[Number(span.dataset.i)];
      s.on = !s.on;
      span.classList.toggle('off', !s.on);
      draw();
    });
    draw();
    let t;
    window.addEventListener('resize', () => {
      clearTimeout(t);
      t = setTimeout(draw, 120);
    });
  }

  // ---------- Donut ----------
  const palette = ['#2563eb', '#16a34a', '#f59e0b', '#7c3aed', '#0891b2', '#db2777', '#64748b'];
  // Fixed colours for known codes, so a risk level or a kind always looks
  // the same wherever it appears.
  const codeColors = {
    low: '#16a34a', medium: '#f59e0b', high: '#dc2626',
    invisible: '#94a3b8', slider: '#2563eb', rotate: '#7c3aed', jigsaw: '#0d9488', click_text: '#0891b2', click_order: '#ca8a04',
    click_icon: '#ea580c', click_shape: '#db2777', spatial: '#4f46e5', fallback: '#64748b',
  };
  const colorOf = (it, i) => codeColors[it.Code] || palette[i % palette.length];
  function donut(box) {
    const items = data(box.dataset.src) || [];
    const total = items.reduce((a, b) => a + b.Count, 0);
    const svg = el('svg', { viewBox: '0 0 120 120' });
    const r = 46, c = 2 * Math.PI * r;
    el('circle', { cx: 60, cy: 60, r, fill: 'none', stroke: '#eef1f6', 'stroke-width': 16 }, svg);
    let off = 0;
    items.forEach((it, i) => {
      const len = total ? (it.Count / total) * c : 0;
      el('circle', { cx: 60, cy: 60, r, fill: 'none', stroke: colorOf(it, i), 'stroke-width': 16,
        'stroke-dasharray': `${len} ${c - len}`, 'stroke-dashoffset': -off, transform: 'rotate(-90 60 60)' }, svg);
      off += len;
    });
    const t = el('text', { x: 60, y: 58, 'text-anchor': 'middle', 'font-size': 16, 'font-weight': 600, fill: '#1f2937' }, svg);
    t.textContent = total >= 10000 ? (total / 10000).toFixed(1) + '万' : fmt(total);
    const s = el('text', { x: 60, y: 76, 'text-anchor': 'middle', 'font-size': 9, fill: '#8a94a6' }, svg);
    s.textContent = box.dataset.unit || '合计';
    const list = document.createElement('ul');
    list.className = 'bars';
    list.innerHTML = items.length ? items.map((it, i) => `<li><span class="name"><i class="dot" style="background:${colorOf(it, i)}"></i>${it.Label}</span>` +
      `<span class="val">${fmt(it.Count)}<small>${it.Share.toFixed(1)}%</small></span></li>`).join('') : '<li class="muted">暂无数据</li>';
    box.append(svg, list);
  }

  $$('[data-chart="line"]').forEach(lineChart);
  $$('[data-chart="donut"]').forEach(donut);

  // ---------- Shell tabs ----------
  // Inside the PearAdmin shell, each page lives in a tab whose iframe id is
  // the tab id: switch to (or open) that tab; when this frame is already that
  // tab, or there is no shell, just navigate.
  const shell = window.parent !== window ? window.parent.layui?.admin : null;
  const openTab = (id, title, href) => {
    if (!shell || window.frameElement?.id === id) location.href = href;
    else shell.jump(id, title, href);
  };

  // ---------- Dialogs and confirmation ----------
  document.addEventListener('click', (e) => {
    const open = e.target.closest('[data-open]');
    if (open) {
      const d = document.getElementById(open.dataset.open);
      if (d) {
        // Pre-fill fields from data-fill-* attributes (edit dialogs).
        for (const [k, v] of Object.entries(open.dataset)) {
          if (!k.startsWith('fill')) continue;
          const name = k.slice(4).replace(/^./, (c) => c.toLowerCase()).replace(/[A-Z]/g, (c) => '_' + c.toLowerCase());
          $$(`[name="${name}"]`, d).forEach((f) => {
            if (f.type === 'checkbox' || f.type === 'radio') f.checked = v.split(',').includes(f.value);
            else f.value = v;
            f.dispatchEvent(new Event('change', { bubbles: true }));
          });
        }
        // One dialog serves every row: the opener names the row's endpoint.
        if (open.dataset.formAction) $('form', d).action = open.dataset.formAction;
        d.showModal();
        $('input:not([type=hidden]):not([readonly]), select, textarea', d)?.focus();
      }
    }
    const close = e.target.closest('[data-close]');
    if (close) close.closest('dialog')?.close();
    const copy = e.target.closest('[data-copy]');
    if (copy) {
      const src = copy.dataset.copy.startsWith('#') ? $(copy.dataset.copy) : null;
      const text = src ? (src.value || src.textContent) : copy.dataset.copy;
      navigator.clipboard?.writeText(text.trim()).then(() => {
        const old = copy.textContent;
        copy.textContent = '已复制';
        setTimeout(() => (copy.textContent = old), 1400);
      });
    }
    const tab = e.target.closest('a[data-tab-open]');
    if (tab && shell && !e.ctrlKey && !e.metaKey && !e.shiftKey) {
      e.preventDefault();
      openTab(tab.dataset.tabOpen, tab.dataset.title || tab.textContent.trim(), tab.getAttribute('href'));
    }
  });

  // App switcher: every app opens in a tab of its own.
  $$('select[data-app-switch]').forEach((sel) => {
    const current = sel.value;
    sel.addEventListener('change', () => {
      const opt = sel.selectedOptions[0];
      sel.value = current;
      openTab('app-' + opt.value, opt.dataset.title || opt.textContent, '/apps/' + encodeURIComponent(opt.value));
    });
  });

  // A just-created app opens in its own tab; drop ?open= so a reload of the
  // list does not open it again.
  const auto = $('a[data-auto-open]');
  if (auto) {
    const u = new URL(location.href);
    u.searchParams.delete('open');
    history.replaceState(null, '', u);
    openTab(auto.dataset.tabOpen, auto.dataset.title, auto.getAttribute('href'));
  }

  // One-time secrets show in a dialog on load. A locked one is only closed
  // on purpose ("I have saved it"): browsers let Esc close a dialog opened
  // without a user gesture even when cancel is prevented, so reopen it.
  $$('dialog[data-autoshow]').forEach((d) => {
    // The page came from a POST; a reload should fetch the plain page, which
    // does not show the secret again.
    if (d.dataset.url) history.replaceState(null, '', d.dataset.url);
    if (d.dataset.locked !== undefined) {
      d.addEventListener('cancel', (e) => e.preventDefault());
      d.addEventListener('click', (e) => { if (e.target.closest('[data-done]')) d.dataset.done = '1'; }, true);
      d.addEventListener('close', () => { if (!d.dataset.done) d.showModal(); });
    }
    d.showModal();
  });

  const confirmDlg = document.createElement('dialog');
  confirmDlg.className = 'modal';
  confirmDlg.innerHTML = '<div class="m-head"><h3>请确认</h3><button class="x" data-close aria-label="关闭">×</button></div>' +
    '<div class="m-body"><p class="c-text" style="margin:0"></p></div><div class="m-foot"><button class="btn" data-close>取消</button><button class="btn primary c-ok">确定</button></div>';
  document.body.appendChild(confirmDlg);
  let pending = null;
  document.addEventListener('submit', (e) => {
    const f = e.target;
    if (!f.dataset.confirm || f.dataset.confirmed) return;
    e.preventDefault();
    pending = f;
    $('.c-text', confirmDlg).textContent = f.dataset.confirm;
    const ok = $('.c-ok', confirmDlg);
    ok.className = 'btn c-ok ' + (f.dataset.danger !== undefined ? 'danger solid' : 'primary');
    ok.textContent = f.dataset.okText || '确定';
    confirmDlg.showModal();
  });
  $('.c-ok', confirmDlg).addEventListener('click', () => {
    confirmDlg.close();
    if (pending) {
      pending.dataset.confirmed = '1';
      pending.requestSubmit ? pending.requestSubmit() : pending.submit();
    }
  });
  // Prevent double submits.
  document.addEventListener('submit', (e) => {
    if (e.defaultPrevented) return;
    const b = e.target.querySelector('button[type=submit], button:not([type])');
    if (b) setTimeout(() => (b.disabled = true), 0);
  });

  // ---------- Tabs ----------
  $$('.tabs').forEach((bar) => {
    const buttons = $$('button[data-tab]', bar);
    const select = (name, push) => {
      buttons.forEach((b) => {
        const on = b.dataset.tab === name;
        b.setAttribute('aria-selected', on);
        const panel = document.getElementById('tab-' + b.dataset.tab);
        if (panel) panel.hidden = !on;
      });
      if (push) {
        const u = new URL(location.href);
        u.searchParams.set('tab', name);
        history.replaceState(null, '', u);
      }
    };
    bar.addEventListener('click', (e) => {
      const b = e.target.closest('button[data-tab]');
      if (b) select(b.dataset.tab, true);
    });
    // ?tab= wins; a page rendered by a POST (no query) names its own tab.
    const want = new URL(location.href).searchParams.get('tab') || bar.dataset.tabDefault;
    select(buttons.some((b) => b.dataset.tab === want) ? want : buttons[0]?.dataset.tab, false);
  });

  // ---------- Action editor ----------
  $$('form.action-form').forEach((f) => {
    const est = $('.estimate', f);
    const kinds = $('.kinds-row', f);
    const update = () => {
      const p = Number(f.puzzles.value) || 0, d = Number(f.difficulty.value) || 0;
      const sec = (p * d) / 1.5e6;
      if (est) est.textContent = `每次验证约 ${fmt(p * d)} 次哈希，普通手机约 ${sec < 0.1 ? '< 0.1' : sec.toFixed(1)} 秒（高风险 ×4，备用路径 ×8）`;
      const mode = $('input[name=mode]:checked', f)?.value;
      if (kinds) kinds.style.opacity = mode === 'invisible' ? 0.45 : 1;
    };
    f.addEventListener('input', update);
    f.addEventListener('change', update);
    update();
  });

  // ---------- Live events ----------
  const live = $('[data-live-events]');
  if (live) {
    const body = $('tbody', live);
    const toggle = $('#live-toggle');
    let timer = null;
    const esc = (s) => String(s ?? '').replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));
    const render = (rows) => {
      body.innerHTML = rows.length ? rows.map((r) => `<tr>
        <td class="nowrap mono">${esc(r.at)}</td><td class="nowrap">${esc(r.app)}</td>
        <td class="nowrap"><span class="badge plain ${r.type === 'verify' ? 'violet' : 'blue'}">${r.type === 'verify' ? '服务端核验' : '浏览器提交'}</span></td>
        <td class="mono">${esc(r.action)}</td><td class="nowrap">${esc(r.kind || '—')}</td>
        <td><span class="badge ${r.pass ? 'ok' : 'bad'}">${esc(r.label)}</span></td>
        <td>${r.level ? `<span class="badge ${r.level === '高' ? 'bad' : r.level === '中' ? 'warn' : 'ok'} plain">${esc(r.level)}</span>` : '—'}</td>
        <td class="cell-sub">${(r.reasons || []).map((x) => `<span class="tag">${esc(x)}</span>`).join('') || '—'}</td>
        <td class="cell-sub nowrap">${esc(r.device || '—')}${r.pointer ? `<div>${esc(r.pointer)}</div>` : ''}</td>
        <td class="mono cell-sub">${esc(r.ip || r.request || '—')}</td></tr>`).join('')
        : '<tr><td colspan="10"><div class="empty"><div class="big">◎</div>暂无事件。完成一次验证后，这里会实时出现记录。</div></td></tr>';
    };
    const poll = async () => {
      try {
        const res = await fetch('/api/events' + location.search, { credentials: 'same-origin', headers: { Accept: 'application/json' } });
        if (res.redirected || !res.ok) return;
        render((await res.json()).events);
      } catch { /* keep the last rows */ }
    };
    const start = () => { timer = setInterval(poll, 3000); };
    toggle?.addEventListener('change', () => (toggle.checked ? start() : clearInterval(timer)));
    if (!toggle || toggle.checked) start();
  }

  // ---------- Challenge preview ----------
  const pv = $('[data-preview]');
  if (pv) {
    const kinds = [['slider', '滑块拼图'], ['rotate', '旋转对齐'], ['jigsaw', '推理拼图'], ['click_text', '文字点选'],
      ['click_order', '语序点选'], ['click_icon', '图标点选'], ['click_shape', '图形点选'], ['spatial', '空间推理']];
    const showAnswer = $('#pv-answer');
    pv.innerHTML = kinds.map(([k, n]) => `<div class="card"><div class="card-head"><h2>${n}<span class="hint mono">${k}</span></h2>
      <button class="btn sm" data-regen="${k}">换一张</button></div><div class="card-body" id="pv-${k}"><div class="empty">生成中…</div></div></div>`).join('');
    const load = async (kind) => {
      const box = $('#pv-' + kind);
      box.innerHTML = '<div class="empty">生成中…</div>';
      const t0 = performance.now();
      const res = await fetch('/api/preview?kind=' + kind, { credentials: 'same-origin' });
      if (!res.ok) { box.innerHTML = '<div class="empty">生成失败</div>'; return; }
      const d = await res.json();
      const ms = Math.round(performance.now() - t0);
      const stage = document.createElement('div');
      stage.className = 'pv-stage' + (showAnswer.checked ? '' : ' hide-answer');
      const meta = document.createElement('div');
      meta.className = 'pv-meta';
      if (d.slider) {
        const s = d.slider;
        stage.style.aspectRatio = `${s.width} / ${s.height}`;
        stage.innerHTML = `<img class="bg" src="${s.background}" alt=""><img class="piece" src="${s.piece}" alt="" style="width:${(s.piece_size / s.width) * 100}%;top:${(s.piece_y / s.height) * 100}%;left:0">`;
        const piece = $('.piece', stage);
        const place = () => (piece.style.left = showAnswer.checked ? `${(s.answer_x / s.width) * 100}%` : '0');
        place();
        stage.place = place;
        meta.innerHTML = `<span>答案：x = ${s.answer_x}px（容差 ±4px）</span><span>服务端生成 ${ms}ms</span>`;
      } else if (d.rotate) {
        const s = d.rotate;
        stage.style.aspectRatio = `${s.width} / ${s.height}`;
        const size = (2 * s.radius) / s.width * 100;
        stage.innerHTML = `<img class="bg" src="${s.background}" alt=""><img class="disc" src="${s.disc}" alt="" style="width:${size}%;left:${((s.cx - s.radius) / s.width) * 100}%;top:${((s.cy - s.radius) / s.height) * 100}%">`;
        const disc = $('.disc', stage);
        const range = document.createElement('input');
        range.type = 'range'; range.className = 'range'; range.min = 0; range.max = s.track; range.value = 0;
        range.setAttribute('aria-label', '拖动旋转圆盘');
        const turn = (x) => (disc.style.transform = `rotate(${(x / s.track) * 360}deg)`);
        range.addEventListener('input', () => { disc.style.transition = 'none'; turn(range.value); });
        const place = () => { disc.style.transition = ''; const x = showAnswer.checked ? (s.angle / 360) * s.track : 0; range.value = x; turn(x); };
        place();
        stage.place = place;
        box.dataset.extra = '1';
        meta.innerHTML = `<span>答案：旋转 ${s.angle.toFixed(1)}°（拖动 ${((s.angle / 360) * s.track).toFixed(1)}px，容差 ±7°）</span><span>服务端生成 ${ms}ms</span>`;
        box.replaceChildren(stage, range, meta);
        return;
      } else if (d.jigsaw) {
        const s = d.jigsaw;
        stage.style.aspectRatio = `${s.width} / ${s.height}`;
        const tw = 100 / s.cols, th = 100 / s.rows;
        const tile = (i) => `left:${(i % s.cols) * tw}%;top:${Math.floor(i / s.cols) * th}%;width:${tw}%;height:${th}%`;
        stage.innerHTML = `<img class="bg" src="${s.image}" alt="">` + s.swap.map((i, n) =>
          `<div class="target" style="${tile(i)};border-radius:4px">${n === 0 ? 'A' : 'B'}</div>`).join('');
        meta.innerHTML = `<span>答案：交换 ${s.swap[0] + 1} 号与 ${s.swap[1] + 1} 号图块（从左上角起按行编号）</span><span>服务端生成 ${ms}ms</span>`;
      } else if (d.click) {
        const s = d.click;
        stage.style.aspectRatio = `${s.width} / ${s.height}`;
        stage.innerHTML = `<img class="bg" src="${s.image}" alt="">` + s.targets.map((t, i) =>
          `<div class="target" style="left:${((t.x - t.r) / s.width) * 100}%;top:${((t.y - t.r) / s.height) * 100}%;width:${((2 * t.r) / s.width) * 100}%;aspect-ratio:1">${i + 1}</div>`).join('');
        const prompt = document.createElement('div');
        prompt.className = 'pv-prompt';
        prompt.textContent = s.prompt;
        if (s.prompt_image) {
          const strip = document.createElement('img');
          strip.src = s.prompt_image;
          strip.alt = '提示图标';
          strip.style.cssText = 'display:block;height:30px;margin-top:6px;border-radius:6px';
          prompt.appendChild(strip);
        }
        meta.innerHTML = `<span>答案：按编号依次点击绿色圈内（半径 +4px 容差）</span><span>服务端生成 ${ms}ms</span>`;
        box.replaceChildren(prompt, stage, meta);
        return;
      }
      box.replaceChildren(stage, meta);
    };
    pv.addEventListener('click', (e) => {
      const b = e.target.closest('[data-regen]');
      if (b) load(b.dataset.regen);
    });
    showAnswer?.addEventListener('change', () => {
      $$('.pv-stage', pv).forEach((s) => { s.classList.toggle('hide-answer', !showAnswer.checked); s.place?.(); });
    });
    kinds.forEach(([k]) => load(k));
  }

  // Inside the shell, a full-page redirect to the login form means the
  // session ended: send the whole window there.
  if (document.body.classList.contains('login') && window.top !== window.self) window.top.location.href = '/login';
})();
