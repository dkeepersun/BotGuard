// Demo page glue: shows widget events and server results on a timeline.
import { collectEnvironment } from '/botguard/signals.js';
import { solvePuzzle } from '/botguard/solver.js';

const widget = document.getElementById('widget');
const timeline = document.getElementById('timeline');
const business = document.getElementById('business');

const CATEGORY_CLASS = { Passed: 'ok', Rejected: 'reject', CallerError: 'caller', Unknown: 'unknown' };

function el(tag, attrs = {}, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === 'class') node.className = v;
    else node.setAttribute(k, v);
  }
  for (const c of children) node.append(c instanceof Node ? c : document.createTextNode(String(c)));
  return node;
}

function push(item) {
  timeline.prepend(item);
}

function json(value) {
  return el('pre', {}, JSON.stringify(value, null, 2));
}

function short(token) {
  return token ? `${token.slice(0, 28)}…` : '（空）';
}

const KIND = { invisible: '无感', slider: '滑块', click_text: '文字点选', click_shape: '图形点选', fallback: '无障碍备用' };

const TRACE = {
  challenge: (d) => `获取 challenge（${d.ms} ms）：服务端决定出「${KIND[d.kind] || d.kind}」题，PoW ${d.puzzles} 道子题，预计约 ${Math.round(d.expected_attempts).toLocaleString()} 次哈希`,
  drag: (d) => d.swap
    ? `用户拖动拼图：把 ${d.swap[0] + 1} 号图块拖到 ${d.swap[1] + 1} 号上交换，${d.points} 个采样点，用时 ${d.ms} ms（${d.pointer_type}）`
    : `用户拖动滑块：${d.points} 个采样点，用时 ${d.ms} ms，停在 x=${d.x}（${d.pointer_type}）`,
  clicks: (d) => `用户点选 ${d.count} 处，用时 ${d.ms} ms，点击间的指针移动次数 ${d.moves_between.join(' / ')}（${d.pointer_type}）`,
  solve: (d) => `浏览器完成 PoW：${d.workers} 个 Worker 并行，实际 ${d.attempts.toLocaleString()} 次哈希，用时 ${d.ms} ms`,
  solution: (d) => `提交答案换取 token（${d.ms} ms）：${short(d.token)}，${Math.max(0, d.expires_at - Math.floor(Date.now() / 1000))} 秒内有效、只能用一次`,
  error: (d) => d.code === 'ANSWER_INVALID'
    ? `服务端拒绝了这次答案（ANSWER_INVALID，第 ${d.failures} 次）：位置、顺序或操作轨迹不对，具体原因不告诉浏览器`
    : `组件出错：${d.code} ${d.message}`,
};

widget.addEventListener('botguard:trace', (e) => {
  const d = e.detail;
  const item = el('li', { class: `browser ${d.step === 'error' ? 'caller' : ''}` }, el('span', { class: 'tag' }, '浏览器'), TRACE[d.step]?.(d) ?? d.step);
  if (d.step !== 'error') item.append(el('details', {}, el('summary', {}, '详情'), json(d)));
  push(item);
});
widget.addEventListener('botguard:expired', () => {
  push(el('li', { class: 'browser' }, el('span', { class: 'tag' }, '浏览器'), 'token 已过期，组件已清空，需要重新验证'));
});

function renderServer(title, result) {
  const o = result.outcome;
  const cls = o ? CATEGORY_CLASS[o.category] || 'unknown' : 'unknown';
  const item = el('li', { class: `server ${cls}` }, el('span', { class: 'tag' }, '服务端'), el('b', {}, title));
  if (result.error) {
    item.className = 'server unknown';
    item.append(el('div', {}, result.error));
    return push(item);
  }
  if (result.expect) item.append(el('div', { class: 'expect' }, `预期：${result.expect}`));
  if (o) {
    const parts = [o.label];
    if (o.http) parts.push(`HTTP ${o.http}`);
    if (o.reason) parts.push(o.reason);
    item.append(el('div', { class: 'result' }, `实际：${parts.join(' · ')}`));
    if (o.message) item.append(el('div', { class: 'msg' }, o.message));
  }
  if (result.business) item.append(el('div', { class: 'msg' }, `业务处理：${result.business}`));
  if (result.explain) item.append(el('div', { class: 'explain' }, result.explain));
  for (const call of result.calls || []) {
    item.append(el('details', {}, el('summary', {}, `${call.method} ${call.url} → ${call.status || '无响应'}（${call.ms} ms）`),
      el('div', { class: 'label' }, '请求头'), json(call.request_headers),
      el('div', { class: 'label' }, '请求体'), json(call.request_body),
      el('div', { class: 'label' }, '响应'), json(call.response_body)));
  }
  push(item);
}

async function post(url, data) {
  const resp = await fetch(url, { method: 'POST', body: new URLSearchParams(data) });
  return resp.json();
}

// Display presets: the widget's modes, and what an H5 page or app WebView
// would set on it.
const SIZES = {
  default: {},
  float: { mode: 'float' },
  inline: { mode: 'inline' },
  compact: { size: 'compact', width: '100%', 'image-height': '120' },
  popup: { mode: 'popup' },
};
document.getElementById('size').addEventListener('change', (e) => {
  for (const name of ['size', 'width', 'image-height', 'mode']) widget.removeAttribute(name);
  for (const [name, value] of Object.entries(SIZES[e.target.value])) widget.setAttribute(name, value);
  widget.reset();
});

document.getElementById('login').addEventListener('submit', async (e) => {
  e.preventDefault();
  // Popup mode: verify when the button is pressed, then continue submitting.
  if (widget.getAttribute('mode') === 'popup' && !widget.token) {
    try {
      await widget.verify();
    } catch {
      return;
    }
  }
  const form = new FormData(e.target);
  const result = await post(e.target.dataset.endpoint, Object.fromEntries(form));
  business.hidden = false;
  business.className = `business ${result.outcome ? CATEGORY_CLASS[result.outcome.category] : 'reject'}`;
  business.textContent = result.business;
  renderServer(`${document.getElementById('submit').textContent}提交（token：${short(form.get('botguard_token'))}）`, result);
  // A token is single-use: whatever the outcome, the widget starts over.
  if (result.outcome) widget.reset();
});

for (const btn of document.querySelectorAll('[data-scenario]')) {
  btn.addEventListener('click', async () => {
    btn.disabled = true;
    try {
      const result = await post('/attack.php', { scenario: btn.dataset.scenario, token: widget.token });
      renderServer(result.title || btn.textContent, result);
    } finally {
      btn.disabled = false;
    }
  });
}

document.getElementById('clear').addEventListener('click', () => timeline.replaceChildren());

// Login (adaptive) and register (slider) are separate server handlers with
// their own fixed actions; the widget follows the selected tab.
const HINTS = {
  login: '登录用 adaptive 模式：正常浏览器通常无感通过；风险中等时随机出滑块、旋转、文字点选或图形点选，风险高时出最难的图形点选。演示站不校验密码。',
  register: '注册固定出滑块拼图，也可以 Tab 到滑块用方向键操作。面板下方的「无法完成验证？」是无障碍备用路径。',
  post: '发帖固定出文字点选：按提示的顺序点击图中的汉字，点错了可以「重置」。',
  pay: '支付固定出图形点选：要数清骰子的点数、多边形的边数，再按顺序点击。这类题对多模态大模型最难。',
  comment: '评论固定出旋转对齐：拖动滑块转动中间的圆盘，让圆盘里的线条和外圈接上再松手。',
  share: '分享固定出推理拼图：图中有两块位置被交换了，把其中一块拖到另一块上换回来。',
  checkin: '签到固定出语序点选：图中有一个四字成语被打乱了，按成语的正确顺序依次点击。',
  coupon: '领券固定出图标点选：按提示条里图标的顺序，点击大图中对应的图标（大图里的图标会旋转、变色）。',
  vote: '投票固定出空间推理：读懂提示，点击被描述图形旁边紧挨着的那个图形，而不是被描述的图形本身。',
};
for (const tab of document.querySelectorAll('.tab')) {
  tab.addEventListener('click', () => {
    document.querySelectorAll('.tab').forEach((t) => t.classList.toggle('active', t === tab));
    widget.setAttribute('action', tab.dataset.action);
    document.getElementById('login').dataset.endpoint = tab.dataset.endpoint;
    document.getElementById('submit').textContent = tab.dataset.submit;
    document.getElementById('mode-hint').textContent = HINTS[tab.dataset.action];
    widget.reset();
  });
}

// ---- In-page script scenarios: what automation inside a real browser does.

async function findGap(slider) {
  const img = new Image();
  img.src = slider.background;
  await img.decode();
  const canvas = document.createElement('canvas');
  canvas.width = slider.width;
  canvas.height = slider.height;
  const ctx = canvas.getContext('2d');
  ctx.drawImage(img, 0, 0);
  const { data } = ctx.getImageData(0, 0, slider.width, slider.height);
  // The slot is darkened; scan the piece's row band for the darkest window.
  const cols = new Array(slider.width).fill(0);
  for (let y = slider.piece_y + 12; y < slider.piece_y + slider.piece_size - 12; y++) {
    for (let x = 0; x < slider.width; x++) {
      const i = (y * slider.width + x) * 4;
      cols[x] += 0.299 * data[i] + 0.587 * data[i + 1] + 0.114 * data[i + 2];
    }
  }
  let best = 0;
  let bestSum = Infinity;
  for (let x = 0; x + slider.piece_size <= slider.width; x++) {
    let sum = 0;
    for (let c = x + 12; c < x + slider.piece_size - 12; c++) sum += cols[c];
    if (sum < bestSum) {
      bestSum = sum;
      best = x;
    }
  }
  return best;
}

function straightDrag(x) {
  const pts = [];
  for (let i = 0; i <= 30; i++) pts.push([i * 16, Math.round((x * i / 30) * 10) / 10, 0]);
  return pts;
}

// Ease-in-out with jittered timing, wobble and an overshoot correction: the
// kind of generator a slightly more careful script would use.
function humanLikeDrag(x) {
  const pts = [];
  let t = 0;
  for (let i = 0; i <= 45; i++) {
    const p = i / 45;
    const ease = p * p * (3 - 2 * p);
    pts.push([Math.round(t * 10) / 10, Math.round(ease * (x + 5) * 10) / 10, Math.round((Math.sin(p * 4) * 2 + Math.random()) * 10) / 10]);
    t += 11 + Math.random() * 9;
  }
  pts.push([Math.round((t + 90) * 10) / 10, x + 1.5, 1.8], [Math.round((t + 170) * 10) / 10, x, 1.2]);
  return pts;
}

function strip(value) {
  return JSON.parse(JSON.stringify(value, (k, v) => (typeof v === 'string' && v.startsWith('data:image') ? `${v.slice(0, 40)}…（图片已省略）` : v)));
}

async function call(calls, url, body) {
  const t0 = performance.now();
  const resp = await fetch(url, { method: 'POST', headers: { 'Content-Type': 'text/plain' }, body: JSON.stringify(body) });
  const json = await resp.json().catch(() => null);
  calls.push({ method: 'POST', url, request_headers: { Origin: location.origin + '（浏览器自动带上）' }, request_body: strip(body),
    status: resp.status, response_body: strip(json), ms: Math.round(performance.now() - t0) });
  return { status: resp.status, json };
}

async function botSlider(human) {
  const endpoint = widget.getAttribute('endpoint');
  const appKey = widget.getAttribute('app-key');
  const calls = [];
  // Real environment signals, plus an invented "the user moved the mouse" story.
  const signals = { ...(await collectEnvironment()), ms_to_click: 2500, pointer_moves_before_click: 37, pointer_type: 'mouse' };
  const ch = await call(calls, `${endpoint}/v1/challenges`, { app_key: appKey, action: 'register', signals });
  if (ch.status !== 200) {
    return renderServer('页面脚本：签发失败', { error: `HTTP ${ch.status} ${ch.json?.code || ''}`, calls });
  }
  const c = ch.json.data;
  const t0 = performance.now();
  const x = await findGap(c.slider);
  const findMs = Math.round(performance.now() - t0);
  const nonces = [];
  for (let i = 0; i < c.params.puzzles; i++) nonces.push((await solvePuzzle(c.algorithm, c.challenge_id, i, c.params.target)).nonce);
  const trajectory = human ? humanLikeDrag(x) : straightDrag(x);
  const sol = await call(calls, `${endpoint}/v1/solutions`, { app_key: appKey, challenge: c.challenge, nonces, answer: { x, trajectory }, signals });
  const title = human ? '页面脚本：canvas 找缺口 + 生成仿人轨迹' : '页面脚本：canvas 找缺口 + 匀速直线拖动';
  const found = `脚本 ${findMs} ms 就从图片里算出缺口在 x=${x}。`;
  if (sol.status !== 200) {
    return renderServer(title, {
      expect: human ? '可能通过（如实展示）' : 'ANSWER_INVALID', calls,
      outcome: { category: 'Rejected', label: '拒绝（solve 阶段）', http: sol.status, reason: sol.json?.code },
      explain: `${found}位置是对的，但轨迹是等间隔、匀速、纯直线，被轨迹规则拦下。注意：这次失败也计入了你这个 IP 的失败次数，接下来登录可能会出现滑块。`,
    });
  }
  const verified = await post('/attack.php', { scenario: 'verify_token', token: sol.json.data.token, action: 'register' });
  renderServer(title, {
    expect: human ? '可能通过（如实展示）' : 'ANSWER_INVALID', calls: [...calls, ...(verified.calls || [])], outcome: verified.outcome,
    explain: `${found}再配上加速、减速、抖动和回拉的仿人轨迹，就骗过了轨迹规则，拿到了真 token。这是滑块的真实上限：它挡住粗糙的脚本、让攻击者多花功夫，挡不住认真投入的对手。业务方可以从风险等级和原因里看到异常，再决定是否加验。`,
  });
}

for (const btn of document.querySelectorAll('[data-bot]')) {
  btn.addEventListener('click', async () => {
    btn.disabled = true;
    try {
      await botSlider(btn.dataset.bot === 'human');
    } catch (err) {
      renderServer('页面脚本出错', { error: String(err) });
    } finally {
      btn.disabled = false;
    }
  });
}
