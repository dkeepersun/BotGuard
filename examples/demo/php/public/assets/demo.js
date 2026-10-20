// Demo page glue: shows widget events and server results on a timeline.
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

const TRACE = {
  challenge: (d) => `获取 challenge（${d.ms} ms）：${d.puzzles} 道子题，预计约 ${Math.round(d.expected_attempts).toLocaleString()} 次哈希`,
  solve: (d) => `浏览器完成 PoW：${d.workers} 个 Worker 并行，实际 ${d.attempts.toLocaleString()} 次哈希，用时 ${d.ms} ms`,
  solution: (d) => `提交答案换取 token（${d.ms} ms）：${short(d.token)}，${Math.max(0, d.expires_at - Math.floor(Date.now() / 1000))} 秒内有效、只能用一次`,
  error: (d) => `组件出错：${d.code} ${d.message}`,
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

document.getElementById('login').addEventListener('submit', async (e) => {
  e.preventDefault();
  const form = new FormData(e.target);
  const result = await post('/login.php', Object.fromEntries(form));
  business.hidden = false;
  business.className = `business ${result.outcome ? CATEGORY_CLASS[result.outcome.category] : 'reject'}`;
  business.textContent = result.business;
  renderServer(`登录提交（token：${short(form.get('botguard_token'))}）`, result);
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
