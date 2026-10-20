// <botguard-widget> — browser side of BotGuard, playing the role of Yidun's
// initNECaptcha. It reports risk signals, gets a challenge whose kind the
// server decides (invisible proof of work, a slider puzzle, or the
// accessible fallback), completes it, exchanges the result for a one-time
// token and puts that token into a hidden form field. The browser never
// holds a secret and never learns its risk score.
//
//   <form method="post" action="/login">
//     <botguard-widget endpoint="https://botguard.example.com" app-key="app_xxx" action="login"></botguard-widget>
//   </form>
//
// Attributes: endpoint, app-key, action, field-name (default botguard_token),
// auto (start without a click). Events (bubbling): botguard:challenge,
// botguard:verified, botguard:expired, botguard:error, botguard:trace.

import { collectEnvironment, trackInteraction } from './signals.js';

const WORKER_URL = new URL('./worker.js', import.meta.url);
const SOLVE_TIMEOUT_MS = 60_000;
const EXPIRY_MARGIN_MS = 3_000;
const MAX_FAILURES = 3;
const COOLDOWN_MS = 10_000;
const MAX_POINTS = 400;

// Pointer activity is tracked from page load; environment signals are read at
// activation, when the page is certainly visible.
const interaction = trackInteraction();

const STYLE = `
:host { display: block; font: 14px/1.5 -apple-system, "PingFang SC", "Microsoft YaHei", sans-serif; max-width: 360px; }
.box { display: flex; align-items: center; gap: 12px; padding: 12px 14px; border: 1px solid #d0d7de;
  border-radius: 8px; background: #f6f8fa; cursor: pointer; user-select: none; min-height: 44px; }
.box[data-state="solving"], .box[data-state="loading"] { cursor: progress; }
.box[data-state="puzzle"], .box[data-state="cooldown"] { cursor: default; }
.box[data-state="verified"] { cursor: default; border-color: #1a7f37; background: #f0fff4; }
.box[data-state="error"], .box[data-state="cooldown"] { border-color: #cf222e; background: #fff5f5; }
.mark { width: 22px; height: 22px; flex: none; border: 2px solid #8c959f; border-radius: 50%; display: grid; place-items: center; }
[data-state="loading"] .mark, [data-state="solving"] .mark { border-color: #0969da; border-top-color: transparent; animation: spin .8s linear infinite; }
[data-state="puzzle"] .mark { border-color: #0969da; }
[data-state="verified"] .mark { border-color: #1a7f37; background: #1a7f37; color: #fff; }
[data-state="verified"] .mark::after { content: "✓"; font-size: 14px; font-weight: 700; }
[data-state="error"] .mark, [data-state="cooldown"] .mark { border-color: #cf222e; color: #cf222e; }
[data-state="error"] .mark::after, [data-state="cooldown"] .mark::after { content: "!"; font-weight: 700; }
.text { flex: 1; min-width: 0; }
.title { color: #1f2328; }
.sub { color: #656d76; font-size: 12px; }
.bar { height: 4px; margin-top: 6px; border-radius: 2px; background: #d0d7de; overflow: hidden; display: none; }
[data-state="solving"] .bar { display: block; }
.bar > i { display: block; height: 100%; width: 0; background: #0969da; transition: width .15s; }
.brand { font-size: 11px; color: #8c959f; flex: none; }
.panel { margin-top: 8px; padding: 10px; border: 1px solid #d0d7de; border-radius: 8px; background: #fff; }
.panel[hidden] { display: none; }
.panel.shake { animation: shake .4s; }
.stage { position: relative; width: 100%; aspect-ratio: 2 / 1; overflow: hidden; border-radius: 4px; background: #eaeef2; touch-action: none; }
.stage img { position: absolute; display: block; user-select: none; -webkit-user-drag: none; pointer-events: none; }
.stage .bg { inset: 0; width: 100%; height: 100%; }
.stage .piece { left: 0; filter: drop-shadow(0 0 2px rgba(0,0,0,.6)); }
.track { position: relative; height: 40px; margin-top: 10px; border-radius: 20px; background: #eaeef2; touch-action: none; }
.track .fill { position: absolute; left: 0; top: 0; bottom: 0; border-radius: 20px; background: #ddf4ff; }
.track .hint { position: absolute; inset: 0; display: grid; place-items: center; color: #656d76; font-size: 13px; pointer-events: none; }
.track .handle { position: absolute; top: 0; left: 0; height: 40px; border-radius: 20px; background: #0969da; color: #fff;
  display: grid; place-items: center; cursor: grab; touch-action: none; box-shadow: 0 1px 3px rgba(0,0,0,.3); }
.track .handle:focus-visible { outline: 3px solid #54aeff; }
.track .handle.dragging { cursor: grabbing; }
.foot { display: flex; justify-content: space-between; margin-top: 8px; font-size: 12px; }
.foot button { border: 0; background: none; color: #0969da; cursor: pointer; padding: 0; font: inherit; }
.msg { min-height: 18px; font-size: 12px; color: #cf222e; margin-top: 4px; }
@keyframes spin { to { transform: rotate(360deg); } }
@keyframes shake { 0%, 100% { transform: none; } 25% { transform: translateX(-6px); } 75% { transform: translateX(6px); } }
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

const round1 = (v) => Math.round(v * 10) / 10;

export class BotGuardWidget extends HTMLElement {
  #root;
  #box;
  #panel;
  #input;
  #workers = [];
  #run = 0;
  #expiryTimer = 0;
  #token = '';
  #failures = 0;
  #abortPuzzle = null;

  connectedCallback() {
    if (this.#root) return;
    this.#root = this.attachShadow({ mode: 'open' });
    this.#root.innerHTML = `<style>${STYLE}</style>
      <div class="box" part="box" role="button" tabindex="0" data-state="idle">
        <span class="mark"></span>
        <span class="text"><div class="title"></div><div class="sub"></div><div class="bar"><i></i></div></span>
        <span class="brand">BotGuard</span>
      </div>
      <div class="panel" part="panel" hidden>
        <div class="stage"><img class="bg" alt="拼图背景"><img class="piece" alt="拼图块"></div>
        <div class="track"><div class="fill"></div><div class="hint">向右拖动滑块，将拼图块对准缺口</div>
          <div class="handle" role="slider" tabindex="0" aria-label="拖动滑块完成拼图，也可以用左右方向键移动、回车确认" aria-valuemin="0">➜</div></div>
        <div class="msg" aria-live="polite"></div>
        <div class="foot"><button type="button" class="refresh">换一张</button><button type="button" class="fallback">无法完成拼图？</button></div>
      </div>`;
    this.#box = this.#root.querySelector('.box');
    this.#panel = this.#root.querySelector('.panel');
    this.#box.addEventListener('click', (e) => this.start({ trigger: e }));
    this.#box.addEventListener('keydown', (e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), this.start({ trigger: e })));
    this.#root.querySelector('.refresh').addEventListener('click', (e) => this.#restart({ trigger: e }));
    this.#root.querySelector('.fallback').addEventListener('click', (e) => this.#restart({ trigger: e, fallback: true }));
    // The hidden field lives in light DOM so the surrounding form submits it.
    this.#input = document.createElement('input');
    this.#input.type = 'hidden';
    this.#input.name = this.getAttribute('field-name') || 'botguard_token';
    this.appendChild(this.#input);
    this.#idle();
    if (this.hasAttribute('auto')) this.start();
  }

  disconnectedCallback() {
    this.#cancel();
    clearTimeout(this.#expiryTimer);
  }

  get token() {
    return this.#token;
  }

  /** Clears the token, e.g. after the server rejected it or the form was submitted. */
  reset() {
    this.#cancel();
    this.#failures = 0;
    clearTimeout(this.#expiryTimer);
    this.#setToken('');
    this.#idle();
  }

  async start(opts = {}) {
    const state = this.#box.dataset.state;
    if (!['idle', 'error'].includes(state)) return;
    this.#begin(opts);
  }

  #restart(opts) {
    this.#cancel();
    this.#begin(opts);
  }

  #cancel() {
    this.#run++;
    this.#stopWorkers();
    if (this.#abortPuzzle) this.#abortPuzzle();
    this.#panel.hidden = true;
  }

  async #begin({ trigger = null, fallback = false } = {}) {
    const run = ++this.#run;
    const endpoint = (this.getAttribute('endpoint') || '').replace(/\/+$/, '');
    const appKey = this.getAttribute('app-key') || '';
    const action = this.getAttribute('action') || '';
    if (!globalThis.isSecureContext || !globalThis.crypto?.subtle) {
      return this.#fail(run, Object.assign(new Error('需要在 HTTPS 或 localhost 页面中使用'), { code: 'INSECURE_CONTEXT' }));
    }
    try {
      this.#show('loading', fallback ? '正在准备无障碍验证…' : '正在获取验证…', '');
      const env = await collectEnvironment().catch(() => null);
      const signals = { ...(env || {}), ...interaction.snapshot(trigger) };
      const t0 = performance.now();
      const ch = await postJSON(`${endpoint}/v1/challenges`, { app_key: appKey, action, signals, fallback });
      if (run !== this.#run) return;
      const perPuzzle = expectedAttempts(ch.params.target);
      this.#emit('botguard:challenge', { kind: ch.kind });
      this.#trace('challenge', { ms: Math.round(performance.now() - t0), kind: ch.kind, challenge_id: ch.challenge_id, algorithm: ch.algorithm,
        puzzles: ch.params.puzzles, target: ch.params.target, expected_attempts: ch.params.puzzles * perPuzzle, expires_at: ch.expires_at });

      // Proof of work always runs; for a slider it runs while the user drags.
      const t1 = performance.now();
      const slider = ch.kind === 'slider';
      const work = this.#solve(run, ch, perPuzzle, slider);
      work.catch(() => {});
      let answer;
      let drag = {};
      if (slider) {
        const result = await this.#puzzle(run, ch.slider);
        if (!result || run !== this.#run) return;
        ({ answer, drag } = result);
        this.#trace('drag', { points: answer.trajectory.length, ms: Math.round(answer.trajectory.at(-1)[0]), x: answer.x, pointer_type: drag.pointer_type });
        this.#show('loading', '正在校验…', '');
      } else if (ch.kind === 'fallback') {
        this.#show('solving', '正在进行无障碍验证，需要几秒…', '');
      }
      const { nonces, attempts, workers, ms: solveMs } = await work;
      if (run !== this.#run) return;
      this.#trace('solve', { ms: solveMs, attempts, workers, nonces });

      const t2 = performance.now();
      const sol = await postJSON(`${endpoint}/v1/solutions`, {
        app_key: appKey, challenge: ch.challenge, nonces, answer, signals: { ...signals, ...drag },
      });
      if (run !== this.#run) return;
      this.#trace('solution', { ms: Math.round(performance.now() - t2), token: sol.token, expires_at: sol.expires_at });

      this.#failures = 0;
      this.#panel.hidden = true;
      this.#setToken(sol.token);
      const how = slider ? '拼图完成' : ch.kind === 'fallback' ? '无障碍验证完成' : `本机计算 ${attempts.toLocaleString()} 次`;
      const took = slider ? performance.now() - t1 : solveMs;
      this.#show('verified', '验证通过', `${how}，用时 ${(took / 1000).toFixed(2)} 秒`);
      this.#emit('botguard:verified', { token: sol.token, expiresAt: sol.expires_at, kind: ch.kind, attempts, solveMs });
      const ttl = sol.expires_at * 1000 - Date.now() - EXPIRY_MARGIN_MS;
      this.#expiryTimer = setTimeout(() => {
        if (run !== this.#run) return;
        this.#setToken('');
        this.#show('idle', '验证已过期，点击重新验证', '');
        this.#emit('botguard:expired', {});
      }, Math.max(0, ttl));
    } catch (err) {
      if (run !== this.#run) return;
      if (err.code === 'ANSWER_INVALID') return this.#answerRejected(run, err);
      this.#fail(run, err);
    }
  }

  #answerRejected(run, err) {
    this.#failures++;
    this.#trace('error', { code: err.code, message: err.message, status: err.status, failures: this.#failures });
    this.#emit('botguard:error', { code: err.code, message: err.message, status: err.status });
    this.#panel.hidden = false;
    this.#panel.classList.remove('shake');
    void this.#panel.offsetWidth;
    this.#panel.classList.add('shake');
    if (this.#failures >= MAX_FAILURES) {
      this.#stopWorkers();
      this.#root.querySelector('.msg').textContent = '';
      this.#panel.hidden = true;
      this.#show('cooldown', `失败次数过多，请 ${COOLDOWN_MS / 1000} 秒后重试`, '');
      setTimeout(() => {
        if (run !== this.#run) return;
        this.#failures = 0;
        this.#idle();
      }, COOLDOWN_MS);
      return;
    }
    this.#root.querySelector('.msg').textContent = '没有对准，请再试一次';
    setTimeout(() => run === this.#run && this.#restart({}), 900);
  }

  // Shows the puzzle and resolves with the answer when the user lets go
  // (or presses Enter); resolves null if the run is abandoned.
  #puzzle(run, s) {
    const stage = this.#root.querySelector('.stage');
    const bg = this.#root.querySelector('.bg');
    const piece = this.#root.querySelector('.piece');
    const handle = this.#root.querySelector('.handle');
    const fill = this.#root.querySelector('.fill');
    const msg = this.#root.querySelector('.msg');
    bg.src = s.background;
    piece.src = s.piece;
    msg.textContent = '';
    this.#panel.hidden = false;
    this.#show('puzzle', '请完成下方拼图', '拖动滑块，使拼图块与缺口对齐');

    const scale = stage.clientWidth / s.width;
    const maxX = s.width - s.piece_size;
    piece.style.width = piece.style.height = `${s.piece_size * scale}px`;
    piece.style.top = `${s.piece_y * scale}px`;
    handle.style.width = `${s.piece_size * scale}px`;
    handle.setAttribute('aria-valuemax', String(maxX));
    const place = (x) => {
      piece.style.left = handle.style.left = `${x * scale}px`;
      fill.style.width = `${x * scale + s.piece_size * scale / 2}px`;
      handle.setAttribute('aria-valuenow', String(Math.round(x)));
    };
    place(0);

    return new Promise((resolve) => {
      let points = [];
      let x = 0;
      let drag = null;
      let keys = null;
      let untrusted = false;
      const clamp = (v) => Math.max(0, Math.min(maxX, v));
      const record = (t, y) => {
        if (points.length < MAX_POINTS) points.push([round1(t), round1(x), round1(y)]);
      };
      const finish = (pointerType) => {
        cleanup();
        resolve({ answer: { x: round1(x), trajectory: points }, drag: { pointer_type: pointerType, untrusted_event: untrusted } });
      };
      const down = (e) => {
        if (drag || keys) return;
        e.preventDefault();
        handle.setPointerCapture(e.pointerId);
        handle.classList.add('dragging');
        untrusted ||= !e.isTrusted;
        drag = { x0: e.clientX, y0: e.clientY, t0: e.timeStamp, type: e.pointerType || 'mouse' };
        points = [];
        record(0, 0);
      };
      const move = (e) => {
        if (!drag) return;
        untrusted ||= !e.isTrusted;
        const samples = e.getCoalescedEvents ? e.getCoalescedEvents() : [e];
        for (const p of samples.length ? samples : [e]) {
          x = clamp((p.clientX - drag.x0) / scale);
          record(p.timeStamp - drag.t0, p.clientY - drag.y0);
        }
        place(x);
      };
      const up = (e) => {
        if (!drag) return;
        untrusted ||= !e.isTrusted;
        x = clamp((e.clientX - drag.x0) / scale);
        record(e.timeStamp - drag.t0, e.clientY - drag.y0);
        place(x);
        handle.classList.remove('dragging');
        finish(drag.type);
      };
      const key = (e) => {
        if (drag) return;
        untrusted ||= !e.isTrusted;
        if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') {
          e.preventDefault();
          if (!keys) {
            keys = { t0: e.timeStamp };
            points = [];
            record(0, 0);
          }
          x = clamp(x + (e.key === 'ArrowRight' ? 1 : -1) * (e.shiftKey ? 10 : 2));
          place(x);
          record(e.timeStamp - keys.t0, 0);
        } else if (e.key === 'Enter' && keys) {
          e.preventDefault();
          record(e.timeStamp - keys.t0, 0);
          finish('keyboard');
        }
      };
      const cleanup = () => {
        handle.removeEventListener('pointerdown', down);
        handle.removeEventListener('pointermove', move);
        handle.removeEventListener('pointerup', up);
        handle.removeEventListener('pointercancel', up);
        handle.removeEventListener('keydown', key);
        handle.classList.remove('dragging');
        this.#abortPuzzle = null;
      };
      handle.addEventListener('pointerdown', down);
      handle.addEventListener('pointermove', move);
      handle.addEventListener('pointerup', up);
      handle.addEventListener('pointercancel', up);
      handle.addEventListener('keydown', key);
      this.#abortPuzzle = () => {
        cleanup();
        resolve(null);
      };
      if (run !== this.#run) this.#abortPuzzle();
    });
  }

  #solve(run, ch, perPuzzle, quiet) {
    const k = ch.params.puzzles;
    const count = Math.max(1, Math.min(k, navigator.hardwareConcurrency || 2, 8));
    const nonces = new Array(k);
    const expected = k * perPuzzle;
    let solved = 0;
    let attempts = 0;
    const t0 = performance.now();
    if (!quiet) this.#show('solving', `正在验证 0/${k}`, '');
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
          if (!quiet && this.#box.dataset.state === 'solving') this.#progress(solved, k, Math.min(1, (solved / k + attempts / expected) / 2));
          if (solved === k) finish(() => resolve({ nonces, attempts, workers: count, ms: Math.round(performance.now() - t0) }));
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
    this.#panel.hidden = true;
    this.#setToken('');
    const code = err.code || 'ERROR';
    this.#show('error', `${err.message}，点击重试`, code);
    this.#trace('error', { code, message: err.message, status: err.status });
    this.#emit('botguard:error', { code, message: err.message, status: err.status });
  }

  #idle() {
    this.#show('idle', '点击进行人机验证', '大多数情况下无需操作，必要时会出现拼图');
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
