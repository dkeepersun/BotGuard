// Shared jsdom page for the widget tests: a fake gateway (fetch), fake Web
// Workers that the test answers, and a stage width, since jsdom has no layout.
// Each *.test.mjs file runs in its own process and gets its own page.
import { mock } from 'node:test';
import { JSDOM, VirtualConsole } from 'jsdom';

// Drops jsdom's "not implemented" notices (canvas, navigation on submit).
const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'https://site.example/', virtualConsole: new VirtualConsole() });
const win = dom.window;
for (const k of ['document', 'HTMLElement', 'customElements', 'CustomEvent', 'Event', 'KeyboardEvent', 'MouseEvent', 'PointerEvent', 'FormData', 'location']) {
  Object.defineProperty(globalThis, k, { value: win[k], configurable: true, writable: true });
}
Object.defineProperty(globalThis, 'window', { value: win, configurable: true, writable: true });
globalThis.isSecureContext = true;

export const layout = { stageWidth: 320 };
Object.defineProperty(win.HTMLElement.prototype, 'clientWidth', {
  configurable: true,
  get() { return this.classList.contains('stage') ? layout.stageWidth : 0; },
});

export const workers = [];
globalThis.Worker = class FakeWorker {
  static silent = false; // a worker that never loads: no message, no error
  constructor(url, opts) {
    this.url = String(url);
    this.opts = opts;
    this.terminated = false;
    this.silent = FakeWorker.silent;
    workers.push(this);
  }
  postMessage(msg) {
    if (msg.type !== 'solve') return;
    this.job = msg;
    // A loaded worker reports progress soon after it starts.
    if (!this.silent) queueMicrotask(() => !this.terminated && this.onmessage?.({ data: { type: 'progress', attempts: 1 } }));
  }
  terminate() {
    this.terminated = true;
  }
  solveAll() {
    if (this.terminated || !this.job) return;
    for (const index of this.job.indices) this.onmessage({ data: { type: 'solved', index, nonce: '1' } });
  }
};
export const liveWorkers = () => workers.filter((w) => !w.terminated && w.job);

// The fake gateway. Handlers return a data object, or { error, status } for
// a failure response.
export const gateway = {
  calls: [],
  kind: 'invisible',
  challenge() {
    return {
      kind: gateway.kind, challenge_id: 'c1', challenge: 'sig', algorithm: 'sha256-target-v1',
      params: { puzzles: 2, target: 'f'.repeat(64) }, expires_at: Math.floor(Date.now() / 1000) + 120,
      slider: { width: 320, height: 160, background: 'data:,', piece: 'data:,', piece_y: 10, piece_size: 60 },
    };
  },
  solution() {
    return { token: `tok${gateway.calls.length}`, expires_at: Math.floor(Date.now() / 1000) + 120, expires_in: 120 };
  },
};
const defaults = { challenge: gateway.challenge, solution: gateway.solution };
export const solutions = () => gateway.calls.filter((c) => c.url.endsWith('/v1/solutions'));
export const challenges = () => gateway.calls.filter((c) => c.url.endsWith('/v1/challenges'));

globalThis.fetch = async (url, opts) => {
  const body = JSON.parse(opts.body);
  gateway.calls.push({ url, body });
  const res = url.endsWith('/v1/challenges') ? gateway.challenge(body) : gateway.solution(body);
  if (res.error) return { ok: false, status: res.status || 400, json: async () => ({ code: res.error, message: res.message || res.error }) };
  return { ok: true, status: 200, json: async () => ({ code: 'OK', data: res }) };
};

/** Lets pending promises and fetches settle (timers stay mocked). */
export async function flush(n = 30) {
  for (let i = 0; i < n; i++) await new Promise((r) => setImmediate(r));
}

/** Answers every running worker, then lets the widget react. */
export async function solveAll() {
  for (const w of liveWorkers()) w.solveAll();
  await flush();
}

export function widget(attrs = {}, parent = document.body) {
  const el = document.createElement('botguard-widget');
  const all = { endpoint: 'https://gw.example', 'app-key': 'app_test', action: 'login', ...attrs };
  for (const [k, v] of Object.entries(all)) el.setAttribute(k, v);
  parent.appendChild(el);
  return el;
}

export const $ = (el, sel) => el.shadowRoot.querySelector(sel);
export const state = (el) => $(el, '.box').dataset.state;

/** Fresh page, gateway and fake clock for every test. */
export function setup() {
  document.body.replaceChildren();
  workers.length = 0;
  globalThis.Worker.silent = false;
  gateway.calls.length = 0;
  gateway.kind = 'invisible';
  Object.assign(gateway, defaults);
  layout.stageWidth = 320;
  mock.timers.enable({ apis: ['setTimeout'] });
}

export function teardown() {
  // Removing widgets rejects any verify() still waiting; tests that care
  // have already observed theirs.
  document.body.replaceChildren();
  mock.timers.reset();
}

export const tick = (ms) => mock.timers.tick(ms);

export const { BotGuardWidget, init } = await import('../src/botguard.js');
