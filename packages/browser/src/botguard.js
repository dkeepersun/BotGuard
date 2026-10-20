// <botguard-widget> — browser side of BotGuard, playing the role of Yidun's
// initNECaptcha. It fetches a challenge, solves the PoW in Web Workers,
// exchanges the solution for a one-time token and puts that token into a
// hidden form field. The business server then verifies the token with its
// secret pair; the browser never holds any secret.
//
//   <form method="post" action="/login">
//     <botguard-widget endpoint="https://botguard.example.com" app-key="app_xxx" action="login"></botguard-widget>
//   </form>
//
// Attributes: endpoint, app-key, action, field-name (default botguard_token),
// auto (start without a click). Events (bubbling): botguard:verified,
// botguard:expired, botguard:error, botguard:trace (step-by-step details).

const WORKER_URL = new URL('./worker.js', import.meta.url);
const SOLVE_TIMEOUT_MS = 60_000;
const EXPIRY_MARGIN_MS = 3_000;

const STYLE = `
:host { display: block; font: 14px/1.5 -apple-system, "PingFang SC", "Microsoft YaHei", sans-serif; }
.box { display: flex; align-items: center; gap: 12px; padding: 12px 14px; border: 1px solid #d0d7de;
  border-radius: 8px; background: #f6f8fa; cursor: pointer; user-select: none; min-height: 44px; }
.box[data-state="solving"], .box[data-state="loading"] { cursor: progress; }
.box[data-state="verified"] { cursor: default; border-color: #1a7f37; background: #f0fff4; }
.box[data-state="error"] { border-color: #cf222e; background: #fff5f5; }
.mark { width: 22px; height: 22px; flex: none; border: 2px solid #8c959f; border-radius: 50%; display: grid; place-items: center; }
[data-state="loading"] .mark, [data-state="solving"] .mark { border-color: #0969da; border-top-color: transparent; animation: spin .8s linear infinite; }
[data-state="verified"] .mark { border-color: #1a7f37; background: #1a7f37; color: #fff; }
[data-state="verified"] .mark::after { content: "✓"; font-size: 14px; font-weight: 700; }
[data-state="error"] .mark { border-color: #cf222e; color: #cf222e; }
[data-state="error"] .mark::after { content: "!"; font-weight: 700; }
.text { flex: 1; min-width: 0; }
.title { color: #1f2328; }
.sub { color: #656d76; font-size: 12px; }
.bar { height: 4px; margin-top: 6px; border-radius: 2px; background: #d0d7de; overflow: hidden; display: none; }
[data-state="solving"] .bar { display: block; }
.bar > i { display: block; height: 100%; width: 0; background: #0969da; transition: width .15s; }
.brand { font-size: 11px; color: #8c959f; flex: none; }
@keyframes spin { to { transform: rotate(360deg); } }
`;

function expectedAttempts(targetHex) {
  // 2^256 / target, estimated from the leading 48 bits; good enough for a progress bar.
  const lead = parseInt(targetHex.slice(0, 12), 16);
  return lead === 0 ? Infinity : Math.max(1, Math.round(2 ** 48 / lead));
}

async function postJSON(url, body) {
  let resp;
  try {
    // text/plain keeps this a CORS "simple request": no preflight round trip.
    resp = await fetch(url, { method: 'POST', headers: { 'Content-Type': 'text/plain' }, body: JSON.stringify(body), credentials: 'omit' });
  } catch {
    throw Object.assign(new Error('无法连接验证服务（网络错误，或当前来源未被允许）'), { code: 'NETWORK' });
  }
  let json = null;
  try { json = await resp.json(); } catch { /* handled below */ }
  if (!resp.ok || !json || json.code !== 'OK') {
    throw Object.assign(new Error((json && json.message) || `验证服务返回 HTTP ${resp.status}`), { code: (json && json.code) || `HTTP_${resp.status}`, status: resp.status });
  }
  return json.data;
}

export class BotGuardWidget extends HTMLElement {
  #root;
  #box;
  #input;
  #workers = [];
  #run = 0;
  #expiryTimer = 0;
  #token = '';

  connectedCallback() {
    if (this.#root) return;
    this.#root = this.attachShadow({ mode: 'open' });
    this.#root.innerHTML = `<style>${STYLE}</style>
      <div class="box" part="box" role="button" tabindex="0" data-state="idle">
        <span class="mark"></span>
        <span class="text"><div class="title"></div><div class="sub"></div><div class="bar"><i></i></div></span>
        <span class="brand">BotGuard</span>
      </div>`;
    this.#box = this.#root.querySelector('.box');
    this.#box.addEventListener('click', () => this.start());
    this.#box.addEventListener('keydown', (e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), this.start()));
    // The hidden field lives in light DOM so the surrounding form submits it.
    this.#input = document.createElement('input');
    this.#input.type = 'hidden';
    this.#input.name = this.getAttribute('field-name') || 'botguard_token';
    this.appendChild(this.#input);
    this.#show('idle', '点击进行人机验证', '验证在浏览器后台完成，无需拼图');
    if (this.hasAttribute('auto')) this.start();
  }

  disconnectedCallback() {
    this.#stopWorkers();
    clearTimeout(this.#expiryTimer);
  }

  get token() {
    return this.#token;
  }

  /** Clears the token, e.g. after the server rejected it or the form was submitted. */
  reset() {
    this.#run++;
    this.#stopWorkers();
    clearTimeout(this.#expiryTimer);
    this.#setToken('');
    this.#show('idle', '点击进行人机验证', '验证在浏览器后台完成，无需拼图');
  }

  async start() {
    const state = this.#box.dataset.state;
    if (state === 'loading' || state === 'solving' || state === 'verified') return;
    const run = ++this.#run;
    const endpoint = (this.getAttribute('endpoint') || '').replace(/\/+$/, '');
    const appKey = this.getAttribute('app-key') || '';
    const action = this.getAttribute('action') || '';
    if (!globalThis.isSecureContext || !globalThis.crypto?.subtle) {
      return this.#fail(run, Object.assign(new Error('需要在 HTTPS 或 localhost 页面中使用'), { code: 'INSECURE_CONTEXT' }));
    }
    try {
      this.#show('loading', '正在获取验证…', '');
      const t0 = performance.now();
      const ch = await postJSON(`${endpoint}/v1/challenges`, { app_key: appKey, action });
      if (run !== this.#run) return;
      const perPuzzle = expectedAttempts(ch.params.target);
      this.#trace('challenge', { ms: Math.round(performance.now() - t0), challenge_id: ch.challenge_id, algorithm: ch.algorithm,
        puzzles: ch.params.puzzles, target: ch.params.target, expected_attempts: ch.params.puzzles * perPuzzle, expires_at: ch.expires_at });

      const t1 = performance.now();
      const { nonces, attempts, workers } = await this.#solve(run, ch, perPuzzle);
      if (run !== this.#run) return;
      const solveMs = Math.round(performance.now() - t1);
      this.#trace('solve', { ms: solveMs, attempts, workers, nonces });

      this.#show('loading', '正在提交验证结果…', '');
      const t2 = performance.now();
      const sol = await postJSON(`${endpoint}/v1/solutions`, { app_key: appKey, challenge: ch.challenge, nonces });
      if (run !== this.#run) return;
      this.#trace('solution', { ms: Math.round(performance.now() - t2), token: sol.token, expires_at: sol.expires_at });

      this.#setToken(sol.token);
      this.#show('verified', '验证通过', `本机计算 ${attempts.toLocaleString()} 次，用时 ${(solveMs / 1000).toFixed(2)} 秒`);
      this.#emit('botguard:verified', { token: sol.token, expiresAt: sol.expires_at, attempts, solveMs });
      const ttl = sol.expires_at * 1000 - Date.now() - EXPIRY_MARGIN_MS;
      this.#expiryTimer = setTimeout(() => {
        if (run !== this.#run) return;
        this.#setToken('');
        this.#show('idle', '验证已过期，点击重新验证', '');
        this.#emit('botguard:expired', {});
      }, Math.max(0, ttl));
    } catch (err) {
      this.#fail(run, err);
    }
  }

  #solve(run, ch, perPuzzle) {
    const k = ch.params.puzzles;
    const count = Math.max(1, Math.min(k, navigator.hardwareConcurrency || 2, 8));
    const nonces = new Array(k);
    const expected = k * perPuzzle;
    let solved = 0;
    let attempts = 0;
    this.#show('solving', `正在验证 0/${k}`, '');
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => { this.#stopWorkers(); reject(Object.assign(new Error('验证计算超时'), { code: 'TIMEOUT' })); }, SOLVE_TIMEOUT_MS);
      const finish = (fn) => { clearTimeout(timer); this.#stopWorkers(); fn(); };
      for (let w = 0; w < count; w++) {
        const worker = new Worker(WORKER_URL, { type: 'module' });
        this.#workers.push(worker);
        worker.onmessage = ({ data }) => {
          if (run !== this.#run) return finish(() => {});
          if (data.type === 'progress') {
            attempts += data.attempts;
          } else if (data.type === 'solved') {
            nonces[data.index] = data.nonce;
            solved++;
          } else if (data.type === 'error') {
            return finish(() => reject(Object.assign(new Error(data.message), { code: 'SOLVER' })));
          }
          this.#progress(solved, k, Math.min(1, (solved / k + attempts / expected) / 2));
          if (solved === k) finish(() => resolve({ nonces, attempts, workers: count }));
        };
        worker.onerror = (e) => finish(() => reject(Object.assign(new Error(e.message || 'Worker 加载失败'), { code: 'SOLVER' })));
        const indices = [];
        for (let i = w; i < k; i += count) indices.push(i);
        worker.postMessage({ type: 'solve', algorithm: ch.algorithm, challengeId: ch.challenge_id, target: ch.params.target, indices });
      }
    });
  }

  #fail(run, err) {
    if (run !== this.#run) return;
    this.#stopWorkers();
    this.#setToken('');
    const code = err.code || 'ERROR';
    this.#show('error', `${err.message}，点击重试`, code);
    this.#trace('error', { code, message: err.message, status: err.status });
    this.#emit('botguard:error', { code, message: err.message, status: err.status });
  }

  #stopWorkers() {
    for (const w of this.#workers) {
      w.postMessage({ type: 'stop' });
      w.terminate();
    }
    this.#workers = [];
  }

  #setToken(token) {
    this.#token = token;
    if (this.#input) this.#input.value = token;
  }

  #show(state, title, sub) {
    this.#box.dataset.state = state;
    this.#root.querySelector('.title').textContent = title;
    this.#root.querySelector('.sub').textContent = sub;
    this.#box.setAttribute('aria-label', title);
  }

  #progress(solved, k, fraction) {
    this.#root.querySelector('.title').textContent = `正在验证 ${solved}/${k}`;
    this.#root.querySelector('.bar > i').style.width = `${Math.round(fraction * 100)}%`;
  }

  #emit(name, detail) {
    this.dispatchEvent(new CustomEvent(name, { detail, bubbles: true, composed: true }));
  }

  #trace(step, detail) {
    this.#emit('botguard:trace', { step, ...detail });
  }
}

if (!customElements.get('botguard-widget')) customElements.define('botguard-widget', BotGuardWidget);
