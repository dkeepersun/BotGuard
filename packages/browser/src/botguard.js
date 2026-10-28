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
// mode ("embed" or "popup"), auto (start without a click), width,
// image-height, size ("compact"), accent, nonce (for a CSP without
// constructable stylesheets), kinds / levels (the kinds the page asks for,
// see selection() below). Events (bubbling): botguard:challenge,
// botguard:verified, botguard:expired, botguard:error, botguard:cancel,
// botguard:trace.
//
// Yidun-style function API (see init() below):
//
//   const bg = BotGuard.init({ element: '#captcha', endpoint, appKey, action: 'login',
//     onVerify(err, ret) { if (!err) form.botguard_token.value = ret.token } });
//   // or popup mode bound to the submit button, like Yidun's "bind" mode:
//   BotGuard.init({ mode: 'popup', bindTo: '#submit', endpoint, appKey, action: 'login' });
//
// Importing this module during server-side rendering is harmless: nothing
// at the top level touches the DOM.

import { collectEnvironment, trackInteraction } from './signals.js';
import { solvePuzzle } from './solver.js';
import { spawnWorker } from './spawn.js';

const SOLVE_TIMEOUT_MS = 60_000;
const WORKER_SILENCE_MS = 5_000; // a worker that says nothing this long is not coming
const EXPIRY_MARGIN_MS = 3_000;
const DEFAULT_TTL_S = 60;
const MAX_TTL_S = 600;
const MAX_FAILURES = 3;
// VERSION travels with every challenge request so the console can count
// widgets too old for the newest kinds; keep it equal to package.json.
export const VERSION = '0.3.0';
const COOLDOWN_MS = 10_000;
const MAX_POINTS = 400; // the gateway's slider.MaxPoints
const MAX_GRID_CLICKS = 24; // the gateway's grid.MaxClicks
// Kinds answered with ordered clicks on the picture.
const CLICK_KINDS = ['click_text', 'click_order', 'click_icon', 'click_shape', 'clock', 'spatial'];
// Kinds answered by selecting cells of a grid, then confirming.
const GRID_KINDS = ['grid_count', 'mirror'];
const SAMPLE_MS = 8; // minimum spacing of trajectory samples
const PROGRESS_MS = 100; // minimum spacing of progress repaints

// Pointer activity is tracked from page load; environment signals are read at
// activation, when the page is certainly visible.
const interaction = globalThis.document ? trackInteraction() : null;

// Sizing and colour are CSS custom properties, so a host page or app can set
// them on the element (they inherit into the shadow root):
//   --botguard-width         max width of the widget and its panel (360px)
//   --botguard-image-height  max height of puzzle images (unlimited, floor
//                            100px so puzzles stay readable); images keep
//                            their aspect ratio and narrow to fit
//   --botguard-accent        accent colour (#0969da)
// The width / image-height / accent attributes set the same properties, and
// size="compact" tightens spacing for H5 pages and app WebViews.
const STYLE = `
:host { --bg-accent: var(--botguard-accent, #0969da); --bg-img-h: max(100px, var(--botguard-image-height, 9999px));
  display: block; width: 100%; max-width: var(--botguard-width, 360px); box-sizing: border-box;
  font: 14px/1.5 -apple-system, "PingFang SC", "Microsoft YaHei", sans-serif; }
*, *::before, *::after { box-sizing: border-box; }
.box { display: flex; align-items: center; gap: 12px; padding: 9px 14px; border: 1px solid #d0d7de;
  border-radius: 8px; background: #f6f8fa; cursor: pointer; user-select: none; min-height: 44px; }
.box[data-state="solving"], .box[data-state="loading"] { cursor: progress; }
.box[data-state="puzzle"], .box[data-state="cooldown"] { cursor: default; }
.box[data-state="verified"] { cursor: default; border-color: #1a7f37; background: #f0fff4; }
.box[data-state="error"], .box[data-state="cooldown"] { border-color: #cf222e; background: #fff5f5; }
.mark { width: 22px; height: 22px; flex: none; border: 2px solid #8c959f; border-radius: 50%; display: grid; place-items: center; }
[data-state="loading"] .mark, [data-state="solving"] .mark { border-color: var(--bg-accent); border-top-color: transparent; animation: spin .8s linear infinite; }
[data-state="puzzle"] .mark { border-color: var(--bg-accent); }
[data-state="verified"] .mark { border-color: #1a7f37; background: #1a7f37; color: #fff; }
[data-state="verified"] .mark::after { content: "✓"; font-size: 14px; font-weight: 700; }
[data-state="error"] .mark, [data-state="cooldown"] .mark { border-color: #cf222e; color: #cf222e; }
[data-state="error"] .mark::after, [data-state="cooldown"] .mark::after { content: "!"; font-weight: 700; }
.text { flex: 1; min-width: 0; }
.title { color: #1f2328; }
.sub { color: #656d76; font-size: 12px; line-height: 1.4; }
.bar { height: 4px; margin-top: 6px; border-radius: 2px; background: #d0d7de; overflow: hidden; display: none; }
[data-state="solving"] .bar { display: block; }
.bar > i { display: block; height: 100%; width: 0; background: var(--bg-accent); transition: width .15s; }
.brand { font-size: 11px; color: #8c959f; flex: none; }
.panel { position: relative; margin-top: 6px; padding: 8px; border: 1px solid #d0d7de; border-radius: 8px; background: #fff; }
.panel:focus { outline: none; }
.panel.shake { animation: shake .4s; }
/* The panel header repeats the status line; only popup mode, which hides the box, shows it. */
.head { display: none; align-items: center; gap: 10px; }
.panel[data-view="puzzle"] .head { margin-bottom: 8px; }
.panel[data-view="status"] .body { display: none; }
.retry { display: none; flex: none; border: 1px solid var(--bg-accent); border-radius: 6px; background: none; color: var(--bg-accent);
  cursor: pointer; padding: 4px 12px; font: inherit; }
.panel[data-state="error"] .retry { display: block; }
/* Images keep their aspect ratio: capping the height narrows them instead. */
.stage, .track { width: min(100%, calc(var(--bg-img-h) * 2)); margin-left: auto; margin-right: auto; }
.stage { position: relative; aspect-ratio: 2 / 1; overflow: hidden; border-radius: 4px; background: #eaeef2; touch-action: none; }
.stage img { position: absolute; display: block; user-select: none; -webkit-user-drag: none; pointer-events: none; }
.stage .bg { top: 0; left: 0; width: 100%; height: 100%; }
.stage .piece { left: 0; filter: drop-shadow(0 0 2px rgba(0,0,0,.6)); }
/* Rotate: the same drag turns a disc instead of moving a piece. */
.stage[data-kind="rotate"], .track[data-kind="rotate"] { width: min(100%, calc(var(--bg-img-h) * 1.6)); }
.stage[data-kind="rotate"] { aspect-ratio: 8 / 5; }
.stage .disc { border-radius: 50%; box-shadow: 0 0 0 2px rgba(255,255,255,.85), 0 3px 10px rgba(0,0,0,.35); will-change: transform; }
.track { position: relative; height: 34px; margin-top: 8px; border-radius: 17px; background: #eaeef2; touch-action: none; }
.track .fill { position: absolute; left: 0; top: 0; bottom: 0; border-radius: 17px; background: var(--bg-accent); opacity: .15; }
.track .hint { position: absolute; top: 0; right: 0; bottom: 0; left: 0; display: grid; place-items: center; color: #656d76; font-size: 13px;
  pointer-events: none; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; padding-left: 36px; }
.track .handle { position: absolute; top: 0; left: 0; height: 100%; min-width: 36px; border-radius: 17px; background: var(--bg-accent); color: #fff;
  display: grid; place-items: center; cursor: grab; touch-action: none; box-shadow: 0 1px 3px rgba(0,0,0,.3); }
.track .handle:focus-visible { outline: 3px solid var(--bg-accent); outline-offset: 2px; }
.track .handle.dragging { cursor: grabbing; }
.foot { display: flex; justify-content: space-between; align-items: center; margin-top: 4px; font-size: 12px; }
.foot button { border: 0; background: none; color: var(--bg-accent); cursor: pointer; padding: 3px 0; font: inherit; }
/* No reserved line: an error appears with a shake, so the shift is expected. */
.msg { font-size: 12px; line-height: 1.4; color: #cf222e; margin-top: 4px; }
.msg:empty { display: none; }
.prompt { font-size: 14px; color: #1f2328; margin-bottom: 8px; font-weight: 600; }
.cstage { position: relative; width: min(100%, calc(var(--bg-img-h) * 1.6)); margin: 0 auto; aspect-ratio: 8 / 5; overflow: hidden;
  border-radius: 4px; background: #eaeef2; cursor: crosshair; touch-action: manipulation; }
.cstage img { position: absolute; top: 0; left: 0; width: 100%; height: 100%; user-select: none; -webkit-user-drag: none; pointer-events: none; }
.cstage .mark-dot { position: absolute; width: 24px; height: 24px; margin: -12px 0 0 -12px; border-radius: 50%; background: var(--bg-accent);
  color: #fff; font: 600 13px/24px sans-serif; text-align: center; box-shadow: 0 0 0 2px #fff; pointer-events: none; }
.foot .left button { margin-right: 12px; }
.prompt-img { display: block; height: 30px; margin: -2px 0 8px; border-radius: 6px; }
.prompt-img.ref { height: 56px; }
/* Grid kinds: cells over the picture toggle on click; Confirm submits. */
.cstage .cells { position: absolute; top: 0; right: 0; bottom: 0; left: 0; display: grid; }
.cstage .cell { position: relative; border-radius: 3px; }
.cstage .cell.on { box-shadow: inset 0 0 0 3px var(--bg-accent); background: rgba(9, 105, 218, .16); }
.cstage .cell.on::after { content: "✓"; position: absolute; top: 3px; right: 3px; width: 18px; height: 18px; border-radius: 50%;
  background: var(--bg-accent); color: #fff; font: 700 12px/18px sans-serif; text-align: center; box-shadow: 0 0 0 2px #fff; }
.confirm { display: block; width: min(100%, calc(var(--bg-img-h) * 1.6)); margin: 8px auto 0; padding: 7px 0; border: 0; border-radius: 6px;
  background: var(--bg-accent); color: #fff; font: inherit; cursor: pointer; }
.confirm:disabled { opacity: .45; cursor: default; }
/* Jigsaw: tiles cut from one image; the dragged one follows the pointer. */
.jstage { position: relative; width: min(100%, calc(var(--bg-img-h) * 1.6)); margin: 0 auto; aspect-ratio: 8 / 5; overflow: hidden;
  border-radius: 4px; background: #eaeef2; touch-action: none; user-select: none; display: grid; }
.jstage .tile { background-repeat: no-repeat; cursor: grab; box-shadow: inset 0 0 0 .5px rgba(255,255,255,.55); touch-action: none; }
.jstage .tile.lifted { position: relative; z-index: 2; cursor: grabbing; box-shadow: 0 6px 18px rgba(0,0,0,.35), 0 0 0 2px #fff; transition: none; }
.jstage .tile.over { box-shadow: inset 0 0 0 3px var(--bg-accent); }
[hidden] { display: none !important; }

/* Older Safari: no aspect-ratio before 15, no color-mix() before 16.2. */
@supports not (aspect-ratio: 1 / 1) {
  .stage::before, .cstage::before { content: ""; display: block; }
  .stage::before { padding-top: 50%; }
  .stage[data-kind="rotate"]::before { padding-top: 62.5%; }
  .cstage::before { padding-top: 62.5%; }
  .jstage { height: calc(min(100vw, var(--bg-img-h) * 1.6) * .625); }
}
@supports (background: color-mix(in srgb, red 50%, white)) {
  .track .fill { background: color-mix(in srgb, var(--bg-accent) 15%, white); opacity: 1; }
  .track .handle:focus-visible { outline-color: color-mix(in srgb, var(--bg-accent) 50%, white); outline-offset: 0; }
}

/* Compact: for H5 pages and app WebViews where vertical space is tight. */
:host([size="compact"]) { font-size: 13px; }
:host([size="compact"]) .box { padding: 6px 10px; gap: 8px; min-height: 36px; }
:host([size="compact"]) .mark { width: 18px; height: 18px; }
:host([size="compact"]) .sub, :host([size="compact"]) .brand { display: none; }
:host([size="compact"]) .panel { padding: 8px; margin-top: 6px; }
:host([size="compact"]) .track { height: 30px; margin-top: 6px; }
:host([size="compact"]) .prompt { font-size: 13px; margin-bottom: 6px; }
:host([size="compact"]) .foot { margin-top: 2px; }
:host([size="compact"]) .msg { margin-top: 2px; }

/* Popup: centred without transforms (the shake animation owns transform),
   inside the safe area, never taller than the viewport; images shrink on
   short screens such as landscape phones or small app WebViews. */
:host([mode="popup"]) { --bg-img-h: max(100px, min(var(--botguard-image-height, 9999px), 38vh)); }
:host([mode="popup"]) .box { display: none; }
:host([mode="popup"]) .head { display: flex; }
:host([mode="popup"]) .panel { position: fixed; z-index: 2147483001; margin: auto; height: fit-content; top: 12px; right: 12px; bottom: 12px; left: 12px;
  inset: max(12px, env(safe-area-inset-top)) max(12px, env(safe-area-inset-right)) max(12px, env(safe-area-inset-bottom)) max(12px, env(safe-area-inset-left));
  width: min(var(--botguard-width, 360px), calc(100vw - 24px)); max-height: calc(100vh - 24px); max-height: calc(100dvh - 24px);
  overflow: auto; padding-top: 28px; box-shadow: 0 12px 40px rgba(0,0,0,.3); }
.backdrop { display: none; }
:host([mode="popup"]) .panel:not([hidden]) + .backdrop { display: block; position: fixed; top: 0; right: 0; bottom: 0; left: 0; z-index: 2147483000;
  background: rgba(0,0,0,.35); }
.close { display: none; position: absolute; right: 6px; top: 2px; border: 0; background: none; font-size: 20px; line-height: 24px;
  padding: 2px 6px; color: #656d76; cursor: pointer; }
:host([mode="popup"]) .close { display: block; }
@keyframes spin { to { transform: rotate(360deg); } }
@keyframes shake { 0%, 100% { transform: none; } 25% { transform: translateX(-6px); } 75% { transform: translateX(6px); } }
`;

const TEMPLATE = `
  <div class="box" part="box" role="button" tabindex="0" data-state="idle">
    <span class="mark"></span>
    <span class="text"><div class="title"></div><div class="sub"></div><div class="bar"><i></i></div></span>
    <span class="brand">BotGuard</span>
  </div>
  <div class="panel" part="panel" hidden role="dialog" aria-label="人机验证" tabindex="-1" data-view="status">
    <button type="button" class="close" aria-label="关闭">×</button>
    <div class="head"><span class="mark"></span>
      <span class="text"><div class="title" aria-live="polite"></div><div class="sub"></div><div class="bar"><i></i></div></span>
      <button type="button" class="retry">重试</button></div>
    <div class="body">
      <div class="slider-ui">
        <div class="stage"><img class="bg" alt="拼图背景"><img class="piece" alt="拼图块"><img class="disc" alt="需要转正的圆盘" hidden></div>
        <div class="track"><div class="fill"></div><div class="hint">向右拖动，补全拼图</div>
          <div class="handle" role="slider" tabindex="0" aria-label="拖动滑块完成拼图，也可以用左右方向键移动、回车确认" aria-valuemin="0">➜</div></div>
      </div>
      <div class="click-ui" hidden>
        <div class="prompt" aria-live="polite"></div>
        <img class="prompt-img" alt="按顺序要点击的图标" hidden>
        <div class="cstage"><img class="cimg" alt="点选图片"><div class="marks"></div><div class="cells" hidden></div></div>
        <button type="button" class="confirm" hidden disabled>确认</button>
      </div>
      <div class="jigsaw-ui" hidden>
        <div class="jstage" role="application" aria-label="拖动一块拼图到另一块上交换位置"></div>
      </div>
      <div class="msg" aria-live="polite"></div>
      <div class="foot"><span class="left"><button type="button" class="clear" hidden>重置</button><button type="button" class="refresh">换一张</button></span>
        <button type="button" class="fallback">无法完成验证？</button></div>
    </div>
  </div>
  <div class="backdrop"></div>`;

function expectedAttempts(targetHex) {
  // 2^256 / target, estimated from the leading 48 bits; good enough for a progress bar.
  const lead = parseInt(targetHex.slice(0, 12), 16);
  return lead === 0 ? Infinity : Math.max(1, Math.round(2 ** 48 / lead));
}

// transport, when set, sends the request instead of fetch: ({ url, body })
// => Promise<{ status, data }>, data being the response body, parsed or not.
// A uni-app App uses it to send from its logic layer, because its WebView
// has no usable origin (see README, "uni-app App 端").
async function postJSON(url, body, transport) {
  let status = 0;
  let json = null;
  try {
    if (transport) {
      const res = await transport({ url, body: JSON.stringify(body) });
      status = Number(res && res.status) || 0;
      json = res && res.data;
      if (typeof json === 'string') {
        try { json = JSON.parse(json); } catch { json = null; }
      }
    } else {
      // text/plain keeps this a CORS "simple request": no preflight round trip.
      const resp = await fetch(url, { method: 'POST', headers: { 'Content-Type': 'text/plain' }, body: JSON.stringify(body), credentials: 'omit' });
      status = resp.status;
      try { json = await resp.json(); } catch { /* handled below */ }
    }
  } catch {
    throw Object.assign(new Error('无法连接验证服务（网络错误，或当前来源未被允许）'), { code: 'NETWORK' });
  }
  if (!(status >= 200 && status < 300) || !json || json.code !== 'OK') {
    throw Object.assign(new Error((json && json.message) || `验证服务返回 HTTP ${status}`), { code: (json && json.code) || `HTTP_${status}`, status });
  }
  return json.data;
}

// Milliseconds the token stays usable. The relative expires_in does not
// depend on the client clock; expires_at is the fallback for gateways that
// do not send it, and an implausible result (a clock far off) means the
// default lifetime.
function tokenTTL(sol) {
  let s = Number(sol.expires_in);
  if (!(s > 0 && s <= MAX_TTL_S)) s = sol.expires_at - Date.now() / 1000;
  if (!(s > 0 && s <= MAX_TTL_S)) s = DEFAULT_TTL_S;
  return Math.max(0, s * 1000 - EXPIRY_MARGIN_MS);
}

const round1 = (v) => Math.round(v * 10) / 10;

// Accepts "320", "320px", "100%", "20rem": bare numbers mean pixels.
function cssLength(v) {
  return /^\d+(\.\d+)?$/.test(v) ? `${v}px` : v;
}

// Kind lists: "slider, rotate" (or "*"); a ladder is tiers separated by ";",
// "invisible; slider, rotate; spatial". Empty entries drop out.
const kindList = (s) => String(s || '').split(/[\s,]+/).filter(Boolean);
const kindLevels = (s) => String(s || '').split(';').map(kindList).filter((t) => t.length);

/**
 * The challenge request's kind selection from the kinds / levels attributes:
 * one kind or several to pick from at random ("*" for every kind the action
 * offers), or a ladder the gateway walks with the user's behaviour. The
 * gateway only honours kinds the action offers, and a suspicious client
 * still gets the hardest one.
 */
function selection(el) {
  const levels = kindLevels(el.getAttribute('levels'));
  if (levels.length) return { levels };
  const kinds = kindList(el.getAttribute('kinds'));
  return kinds.length ? { kinds } : {};
}

// init() options accept arrays as well as the attribute strings.
const kindsAttr = (v) => (Array.isArray(v) ? v.join(',') : String(v));
const levelsAttr = (v) => (Array.isArray(v) ? v.map((t) => (Array.isArray(t) ? t.join(',') : String(t))).join(';') : String(v));

const errorWith = (message, code, extra) => Object.assign(new Error(message), { code, ...extra });
const cancelled = () => errorWith('已取消验证', 'CANCELLED');

// One verification attempt. Whatever it starts (workers, timers, puzzle
// listeners) registers its cleanup here, so ending the attempt stops exactly
// its own work: a late timer of an abandoned attempt cannot touch a newer one.
function newRound() {
  const cleanups = new Set();
  const round = {
    live: true,
    /** Runs fn when the round ends (now, if it already has); returns an undo. */
    defer(fn) {
      if (!round.live) {
        fn();
        return () => {};
      }
      cleanups.add(fn);
      return () => cleanups.delete(fn);
    },
    /** setTimeout that is cleared when the round ends; returns a cancel. */
    after(ms, fn) {
      const id = setTimeout(() => {
        undo();
        if (round.live) fn();
      }, ms);
      const undo = round.defer(() => clearTimeout(id));
      return () => {
        clearTimeout(id);
        undo();
      };
    },
    end() {
      if (!round.live) return;
      round.live = false;
      for (const fn of cleanups) fn();
      cleanups.clear();
    },
  };
  return round;
}

// Constructed stylesheets are not subject to the page's CSP style-src, so a
// strict CSP needs nothing extra. Browsers without them get a <style>
// element carrying the page's nonce.
let sharedSheet = null;

function adoptStyle(root, nonce) {
  try {
    if ('adoptedStyleSheets' in root && typeof CSSStyleSheet === 'function' && 'replaceSync' in CSSStyleSheet.prototype) {
      if (!sharedSheet) {
        const sheet = new CSSStyleSheet();
        sheet.replaceSync(STYLE);
        sharedSheet = sheet;
      }
      root.adoptedStyleSheets = [sharedSheet];
      return;
    }
  } catch { /* e.g. a sheet from another document: use a <style> element */ }
  const style = document.createElement('style');
  if (nonce) style.setAttribute('nonce', nonce);
  style.textContent = STYLE;
  root.prepend(style);
}

// Outside a browser (server-side rendering) there is no HTMLElement; the
// class is still defined so importing never throws, but it is not registered.
const ElementBase = globalThis.HTMLElement ?? class {};

export class BotGuardWidget extends ElementBase {
  static observedAttributes = ['width', 'image-height', 'accent', 'endpoint', 'app-key', 'action', 'field-name'];

  /** Optional replacement for fetch, see postJSON. */
  transport = null;

  attributeChangedCallback(name, old, value) {
    const prop = { width: '--botguard-width', 'image-height': '--botguard-image-height', accent: '--botguard-accent' }[name];
    if (prop) {
      if (value) this.style.setProperty(prop, name === 'accent' ? value : cssLength(value));
      else this.style.removeProperty(prop);
      return;
    }
    if (!this.#root || old === value) return;
    if (name === 'field-name') this.#input.name = value || 'botguard_token';
    // A token, or an attempt under way, belongs to the old endpoint, app or
    // action and would be rejected: start over.
    if (this.#token || ['loading', 'solving', 'puzzle'].includes(this.#box.dataset.state)) this.reset();
  }

  #root;
  #box;
  #panel;
  #input;
  #round = newRound();
  #pending = null; // the verify() promise every caller shares, with its settle functions
  #view = null; // 'puzzle' while a puzzle is on screen
  #dialogOpen = false; // popup mode: the dialog is up for the current attempt
  #returnFocus = null; // popup mode: where focus goes back to when the dialog closes
  #token = '';
  #tokenDeadline = 0; // performance.now() when the token stops being usable
  #expiryTimer = 0;
  #failures = 0;
  #cooldownUntil = 0; // performance.now() when the cooldown ends; 0 = none
  #cooldownTimer = 0;

  connectedCallback() {
    if (this.#root) return this.#reconnected();
    this.#root = this.attachShadow({ mode: 'open' });
    this.#root.innerHTML = TEMPLATE;
    adoptStyle(this.#root, this.nonce || this.getAttribute('nonce') || '');
    this.#box = this.#root.querySelector('.box');
    this.#panel = this.#root.querySelector('.panel');
    this.#box.addEventListener('click', (e) => this.start({ trigger: e }));
    this.#box.addEventListener('keydown', (e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), this.start({ trigger: e })));
    this.#root.querySelector('.refresh').addEventListener('click', (e) => this.#begin({ trigger: e }));
    this.#root.querySelector('.fallback').addEventListener('click', (e) => this.#begin({ trigger: e, fallback: true }));
    this.#root.querySelector('.retry').addEventListener('click', (e) => {
      this.#panel.focus({ preventScroll: true }); // the button hides itself; keep focus in the dialog
      this.start({ trigger: e });
    });
    this.#root.querySelector('.close').addEventListener('click', () => this.#close());
    this.#panel.addEventListener('keydown', (e) => this.#dialogKey(e));
    // The hidden field lives in light DOM so the surrounding form submits it.
    this.#input = document.createElement('input');
    this.#input.type = 'hidden';
    this.#input.name = this.getAttribute('field-name') || 'botguard_token';
    this.appendChild(this.#input);
    this.#idle();
    if (this.hasAttribute('auto')) this.start();
  }

  // Inserted again after a removal, which abandoned any attempt in progress.
  #reconnected() {
    if (this.#token) return this.#armExpiry();
    const left = this.#cooldownUntil - performance.now();
    if (left > 0) return this.#startCooldown(left);
    if (this.#box.dataset.state === 'cooldown') {
      this.#failures = 0;
      this.#cooldownUntil = 0;
      this.#idle();
    }
    if (this.hasAttribute('auto')) this.start();
  }

  disconnectedCallback() {
    // Whatever was in progress is abandoned: callers waiting in verify() hear
    // about it, and the widget is usable again if it is inserted again.
    this.#cancel();
    clearTimeout(this.#expiryTimer);
    clearTimeout(this.#cooldownTimer);
    const state = this.#box.dataset.state;
    if (state === 'verified' || state === 'cooldown') this.#render();
    else this.#idle();
    this.#settle(cancelled());
  }

  get token() {
    return this.#token;
  }

  /** Clears the token, e.g. after the server rejected it or the form was submitted. */
  reset() {
    if (!this.#root) return this.#setToken('');
    this.#cancel();
    this.#failures = 0;
    this.#cooldownUntil = 0;
    clearTimeout(this.#cooldownTimer);
    this.#setToken('');
    this.#idle();
    this.#settle(cancelled());
  }

  async start(opts = {}) {
    if (!this.#root) return;
    const state = this.#box.dataset.state;
    if (!['idle', 'error'].includes(state)) return;
    this.#begin(opts);
  }

  /**
   * Resolves with a token, starting verification if needed; rejects on
   * errors, cancellation (popup closed, reset(), element removed: code
   * CANCELLED) or too many failed answers (TOO_MANY_FAILURES, also at once
   * during the cooldown). Calls while one is pending share its promise. This
   * is what popup mode and bound submit buttons use.
   */
  verify() {
    if (this.#token) return Promise.resolve(this.#token);
    if (this.#cooldownUntil > performance.now()) return Promise.reject(this.#tooMany());
    if (!this.#root) return Promise.reject(errorWith('组件尚未插入页面', 'NOT_CONNECTED'));
    if (!this.#pending) {
      const pending = {};
      pending.promise = new Promise((resolve, reject) => Object.assign(pending, { resolve, reject }));
      this.#pending = pending;
      this.start(); // no-op if an attempt is already under way: it settles this promise
    }
    return this.#pending.promise;
  }

  #settle(err, token) {
    const pending = this.#pending;
    if (!pending) return;
    this.#pending = null;
    if (err) pending.reject(err);
    else pending.resolve(token);
  }

  // Ends the current attempt and takes its UI down; callers then set the state.
  #cancel() {
    this.#round.end();
    this.#view = null;
    this.#dialogOpen = false;
  }

  // The popup's × button and Esc.
  #close() {
    if (this.#cooldownUntil > performance.now()) {
      // Closing puts the dialog away but does not end the cooldown.
      this.#dialogOpen = false;
      this.#render();
    } else {
      this.reset();
    }
    this.#emit('botguard:cancel', {});
  }

  async #begin({ trigger = null, fallback = false } = {}) {
    this.#round.end();
    const round = (this.#round = newRound());
    const endpoint = (this.getAttribute('endpoint') || '').replace(/\/+$/, '');
    const appKey = this.getAttribute('app-key') || '';
    const action = this.getAttribute('action') || '';
    if (this.getAttribute('mode') === 'popup') this.#dialogOpen = true;
    if (!globalThis.isSecureContext || !globalThis.crypto?.subtle) {
      return this.#fail(round, errorWith('需要在 HTTPS 或 localhost 页面中使用', 'INSECURE_CONTEXT'));
    }
    try {
      this.#show('loading', fallback ? '正在准备无障碍验证…' : '正在获取验证…', '');
      const env = await collectEnvironment().catch(() => null);
      if (!round.live) return;
      const signals = { ...(env || {}), ...(interaction ? interaction.snapshot(trigger) : {}) };
      const t0 = performance.now();
      const ch = await postJSON(`${endpoint}/v1/challenges`, { app_key: appKey, action, signals, fallback, widget: VERSION, ...selection(this) }, this.transport);
      if (!round.live) return;
      const perPuzzle = expectedAttempts(ch.params.target);
      this.#emit('botguard:challenge', { kind: ch.kind });
      this.#trace('challenge', { ms: Math.round(performance.now() - t0), kind: ch.kind, challenge_id: ch.challenge_id, algorithm: ch.algorithm,
        puzzles: ch.params.puzzles, target: ch.params.target, expected_attempts: ch.params.puzzles * perPuzzle, expires_at: ch.expires_at });

      // Proof of work always runs; for a puzzle it runs while the user works on it.
      const t1 = performance.now();
      // Rotate is answered like the slider: a drag distance plus its path.
      const slider = ch.kind === 'slider' || ch.kind === 'rotate';
      const clicking = CLICK_KINDS.includes(ch.kind);
      const swapping = ch.kind === 'jigsaw';
      const selecting = GRID_KINDS.includes(ch.kind);
      const interactive = slider || clicking || swapping || selecting;
      if (!interactive) this.#view = null;
      const work = this.#solve(round, ch, perPuzzle, interactive);
      work.catch(() => {});
      let answer;
      let drag = {};
      if (slider) {
        const result = await this.#puzzle(round, ch.kind, ch.kind === 'rotate' ? ch.rotate : ch.slider);
        if (!result || !round.live) return;
        ({ answer, drag } = result);
        this.#trace('drag', { points: answer.trajectory.length, ms: Math.round(answer.trajectory.at(-1)[0]), x: answer.x, pointer_type: drag.pointer_type });
        this.#show('loading', '正在校验…', '');
      } else if (swapping) {
        const result = await this.#jigsaw(round, ch.jigsaw);
        if (!result || !round.live) return;
        ({ answer, drag } = result);
        this.#trace('drag', { points: answer.trajectory.length, ms: Math.round(answer.trajectory.at(-1)[0]), swap: answer.swap, pointer_type: drag.pointer_type });
        this.#show('loading', '正在校验…', '');
      } else if (clicking) {
        const result = await this.#clicks(round, ch.click);
        if (!result || !round.live) return;
        ({ answer, drag } = result);
        this.#trace('clicks', { count: answer.clicks.length, ms: Math.round(answer.clicks.at(-1)[0]), moves_between: answer.moves_between, pointer_type: drag.pointer_type });
        this.#show('loading', '正在校验…', '');
      } else if (selecting) {
        const result = await this.#grid(round, ch.grid);
        if (!result || !round.live) return;
        ({ answer, drag } = result);
        this.#trace('cells', { clicks: answer.clicks.length, ms: Math.round(answer.clicks.at(-1)[0]), moves_between: answer.moves_between, pointer_type: drag.pointer_type });
        this.#show('loading', '正在校验…', '');
      } else if (ch.kind === 'fallback') {
        this.#show('solving', '正在进行无障碍验证，需要几秒…', '');
      }
      const { nonces, attempts, workers, ms: solveMs } = await work;
      if (!round.live) return;
      this.#trace('solve', { ms: solveMs, attempts, workers, nonces });

      const t2 = performance.now();
      const sol = await postJSON(`${endpoint}/v1/solutions`, {
        app_key: appKey, challenge: ch.challenge, nonces, answer, signals: { ...signals, ...drag },
      }, this.transport);
      if (!round.live) return;
      this.#trace('solution', { ms: Math.round(performance.now() - t2), token: sol.token, expires_at: sol.expires_at, expires_in: sol.expires_in });

      round.end();
      this.#failures = 0;
      this.#view = null;
      this.#dialogOpen = false;
      this.#setToken(sol.token, tokenTTL(sol));
      const how = ch.kind === 'rotate' ? '旋转对齐完成' : swapping ? '拼图复原完成' : slider ? '拼图完成' : clicking ? '点选完成'
        : selecting ? '选择完成' : ch.kind === 'fallback' ? '无障碍验证完成' : `本机计算 ${attempts.toLocaleString()} 次`;
      const took = interactive ? performance.now() - t1 : solveMs;
      this.#show('verified', '验证通过', `${how}，用时 ${(took / 1000).toFixed(2)} 秒`);
      this.#emit('botguard:verified', { token: sol.token, expiresAt: sol.expires_at, expiresIn: sol.expires_in, kind: ch.kind, attempts, solveMs });
      this.#settle(null, sol.token);
    } catch (err) {
      if (!round.live) return;
      if (err.code === 'ANSWER_INVALID') return this.#answerRejected(round, err);
      this.#fail(round, err);
    }
  }

  #answerRejected(round, err) {
    this.#failures++;
    this.#trace('error', { code: err.code, message: err.message, status: err.status, failures: this.#failures });
    this.#emit('botguard:error', { code: err.code, message: err.message, status: err.status });
    this.#panel.classList.remove('shake');
    void this.#panel.offsetWidth;
    this.#panel.classList.add('shake');
    if (this.#failures >= MAX_FAILURES) {
      round.end();
      this.#view = null;
      this.#root.querySelector('.msg').textContent = '';
      this.#startCooldown(COOLDOWN_MS);
      const tooMany = this.#tooMany();
      this.#emit('botguard:error', { code: tooMany.code, message: tooMany.message });
      this.#settle(tooMany);
      return;
    }
    this.#root.querySelector('.msg').textContent = !this.#root.querySelector('.jigsaw-ui').hidden ? '交换的两块不对，请再试一次'
      : this.#root.querySelector('.click-ui').hidden ? '没有对准，请再试一次'
        : !this.#root.querySelector('.cells').hidden ? '选择不正确，请再试一次' : '点击位置或顺序不正确，请再试一次';
    // The next puzzle replaces this one in place; the panel stays up meanwhile.
    round.after(900, () => this.#begin({}));
  }

  #startCooldown(ms) {
    this.#cooldownUntil = performance.now() + ms;
    this.#show('cooldown', `失败次数过多，请 ${Math.ceil(ms / 1000)} 秒后重试`, '');
    clearTimeout(this.#cooldownTimer);
    this.#cooldownTimer = setTimeout(() => {
      this.#cooldownUntil = 0;
      this.#failures = 0;
      this.#dialogOpen = false;
      this.#idle();
    }, ms);
  }

  #tooMany() {
    const s = Math.max(1, Math.ceil((this.#cooldownUntil - performance.now()) / 1000));
    return errorWith(`失败次数过多，请 ${s} 秒后重试`, 'TOO_MANY_FAILURES');
  }

  // Shows a drag puzzle (the jigsaw slider, or the rotate disc) and resolves
  // with the answer when the user lets go (or presses Enter); resolves null
  // if the round ends first. Both answer with the drag distance x.
  #puzzle(round, kind, s) {
    const stage = this.#root.querySelector('.stage');
    const track = this.#root.querySelector('.track');
    const bg = this.#root.querySelector('.bg');
    const piece = this.#root.querySelector('.piece');
    const disc = this.#root.querySelector('.disc');
    const handle = this.#root.querySelector('.handle');
    const fill = this.#root.querySelector('.fill');
    const msg = this.#root.querySelector('.msg');
    const turning = kind === 'rotate';
    stage.dataset.kind = track.dataset.kind = kind;
    bg.src = s.background;
    piece.hidden = turning;
    disc.hidden = !turning;
    if (turning) disc.src = s.disc;
    else piece.src = s.piece;
    msg.textContent = '';
    // Short enough for a 190 px track in compact mode on a phone.
    this.#root.querySelector('.hint').textContent = turning ? '向右拖动，转正圆盘' : '向右拖动，补全拼图';
    handle.setAttribute('aria-label', turning
      ? '拖动滑块转动圆盘，也可以用左右方向键转动、回车确认'
      : '拖动滑块完成拼图，也可以用左右方向键移动、回车确认');
    this.#root.querySelector('.slider-ui').hidden = false;
    this.#root.querySelector('.click-ui').hidden = true;
    this.#root.querySelector('.jigsaw-ui').hidden = true;
    this.#root.querySelector('.clear').hidden = true;
    this.#view = 'puzzle';
    if (turning) this.#show('puzzle', '请转正中间的圆盘', '拖动滑块，让圆盘里的线条和外圈接上');
    else this.#show('puzzle', '请完成下方拼图', '拖动滑块，使拼图块与缺口对齐');

    // The handle is as wide as the piece (slider) or the rest of the image
    // beyond the track (rotate), so it reaches the end exactly at maxX.
    const maxX = turning ? s.track : s.width - s.piece_size;
    const handleW = s.width - maxX;
    let scale = 0;
    // Measured again at every grab and key press: the panel may have been
    // hidden (width 0) or resized (rotation, a popup on a small screen) since.
    const measure = () => {
      scale = stage.clientWidth / s.width;
      handle.style.width = `${handleW * scale}px`;
      if (turning) {
        disc.style.width = disc.style.height = `${2 * s.radius * scale}px`;
        disc.style.left = `${(s.cx - s.radius) * scale}px`;
        disc.style.top = `${(s.cy - s.radius) * scale}px`;
      } else {
        piece.style.width = piece.style.height = `${s.piece_size * scale}px`;
        piece.style.top = `${s.piece_y * scale}px`;
      }
    };
    handle.setAttribute('aria-valuemax', String(maxX));
    if (!turning) handle.removeAttribute('aria-valuetext');

    return new Promise((resolve) => {
      let points = [];
      let gap = SAMPLE_MS;
      let x = 0;
      let drag = null;
      let keys = null;
      let untrusted = false;
      const clamp = (v) => Math.max(0, Math.min(maxX, v));
      const place = () => {
        handle.style.left = `${x * scale}px`;
        if (turning) {
          const deg = (x / maxX) * 360;
          disc.style.transform = `rotate(${deg.toFixed(2)}deg)`;
          handle.setAttribute('aria-valuetext', `${Math.round(deg)} 度`);
        } else {
          piece.style.left = `${x * scale}px`;
        }
        fill.style.width = `${(x + handleW / 2) * scale}px`;
        handle.setAttribute('aria-valuenow', String(Math.round(x)));
      };
      const startOver = () => {
        x = 0;
        points = [];
        gap = SAMPLE_MS;
        place();
      };
      // Samples closer than `gap` are skipped, except the release point,
      // which must be last: the gateway checks it against the submitted x.
      // A long drag that fills the buffer keeps every other sample and
      // halves the rate, so the path stays whole instead of losing its end.
      const record = (t, y, final = false) => {
        const p = [round1(t), round1(x), round1(y)];
        const last = points.at(-1);
        if (last && p[0] - last[0] < gap) {
          if (!final) return;
          if (points.length > 1) {
            points[points.length - 1] = p;
            return;
          }
        }
        if (points.length >= MAX_POINTS) {
          points = points.filter((_, i) => i % 2 === 0);
          gap *= 2;
        }
        points.push(p);
      };
      const finish = (pointerType) => {
        cleanup();
        if (!Number.isFinite(x)) x = 0;
        resolve({ answer: { x: round1(x), trajectory: points }, drag: { pointer_type: pointerType, untrusted_event: untrusted } });
      };
      const down = (e) => {
        if (drag || e.button > 0 || e.isPrimary === false) return;
        measure();
        if (!(scale > 0)) return; // not laid out: nothing sensible to drag
        e.preventDefault();
        try { handle.setPointerCapture(e.pointerId); } catch { /* pointer already gone */ }
        handle.classList.add('dragging');
        untrusted ||= !e.isTrusted;
        // A drag after arrow keys starts from the beginning: the gateway
        // wants every trajectory to start at x = 0.
        keys = null;
        startOver();
        msg.textContent = '';
        drag = { id: e.pointerId, x0: e.clientX, y0: e.clientY, t0: e.timeStamp, type: e.pointerType || 'mouse' };
        record(0, 0);
      };
      const move = (e) => {
        if (!drag || e.pointerId !== drag.id) return;
        untrusted ||= !e.isTrusted;
        const samples = e.getCoalescedEvents ? e.getCoalescedEvents() : [e];
        for (const p of samples.length ? samples : [e]) {
          x = clamp((p.clientX - drag.x0) / scale);
          record(p.timeStamp - drag.t0, p.clientY - drag.y0);
        }
        place();
      };
      const up = (e) => {
        if (!drag || e.pointerId !== drag.id) return;
        untrusted ||= !e.isTrusted;
        x = clamp((e.clientX - drag.x0) / scale);
        record(e.timeStamp - drag.t0, e.clientY - drag.y0, true);
        place();
        handle.classList.remove('dragging');
        finish(drag.type);
      };
      // The browser took the pointer (a scroll, a system gesture): that is
      // not the user letting go, so put the piece back for another try.
      const cancel = (e) => {
        if (!drag || e.pointerId !== drag.id) return;
        drag = null;
        handle.classList.remove('dragging');
        startOver();
        msg.textContent = '拖动被打断，请重新拖动滑块';
      };
      const key = (e) => {
        if (drag) return;
        untrusted ||= !e.isTrusted;
        if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') {
          e.preventDefault();
          measure();
          if (!keys) {
            keys = { t0: e.timeStamp };
            startOver();
            record(0, 0);
          }
          x = clamp(x + (e.key === 'ArrowRight' ? 1 : -1) * (e.shiftKey ? 10 : 2));
          place();
          record(e.timeStamp - keys.t0, 0);
        } else if (e.key === 'Enter' && keys) {
          e.preventDefault();
          record(e.timeStamp - keys.t0, 0, true);
          finish('keyboard');
        }
      };
      const cleanup = () => {
        handle.removeEventListener('pointerdown', down);
        handle.removeEventListener('pointermove', move);
        handle.removeEventListener('pointerup', up);
        handle.removeEventListener('pointercancel', cancel);
        handle.removeEventListener('keydown', key);
        handle.classList.remove('dragging');
      };
      measure();
      place();
      handle.addEventListener('pointerdown', down);
      handle.addEventListener('pointermove', move);
      handle.addEventListener('pointerup', up);
      handle.addEventListener('pointercancel', cancel);
      handle.addEventListener('keydown', key);
      round.defer(() => {
        cleanup();
        resolve(null);
      });
    });
  }

  // Shows the click puzzle and resolves once the user has clicked as many
  // points as there are targets; resolves null if the round ends first.
  #clicks(round, c) {
    const stage = this.#root.querySelector('.cstage');
    const img = this.#root.querySelector('.cimg');
    const marks = this.#root.querySelector('.marks');
    const clear = this.#root.querySelector('.clear');
    this.#root.querySelector('.slider-ui').hidden = true;
    this.#root.querySelector('.jigsaw-ui').hidden = true;
    this.#root.querySelector('.click-ui').hidden = false;
    this.#root.querySelector('.cells').hidden = true;
    this.#root.querySelector('.confirm').hidden = true;
    this.#root.querySelector('.prompt').textContent = c.prompt;
    const promptImg = this.#root.querySelector('.prompt-img');
    promptImg.classList.remove('ref');
    promptImg.alt = '按顺序要点击的图标';
    promptImg.hidden = !c.prompt_image;
    if (c.prompt_image) promptImg.src = c.prompt_image;
    this.#root.querySelector('.msg').textContent = '';
    clear.hidden = false;
    img.src = c.image;
    marks.replaceChildren();
    this.#view = 'puzzle';
    this.#show('puzzle', '请按提示依次点击', `共 ${c.targets} 处`);
    const shownAt = performance.now();

    return new Promise((resolve) => {
      let clicks = [];
      let movesBetween = [];
      let moves = 0;
      let pointerType = '';
      let untrusted = false;
      let done = false;
      const reset = () => {
        clicks = [];
        movesBetween = [];
        marks.replaceChildren();
      };
      const move = (e) => {
        untrusted ||= !e.isTrusted;
        moves++;
      };
      const down = (e) => {
        if (done || clicks.length >= c.targets) return;
        const rect = stage.getBoundingClientRect();
        const scale = rect.width / c.width;
        if (!(scale > 0)) return;
        e.preventDefault();
        untrusted ||= !e.isTrusted;
        pointerType ||= e.pointerType || 'mouse';
        const x = (e.clientX - rect.left) / scale;
        const y = (e.clientY - rect.top) / scale;
        clicks.push([round1(e.timeStamp - shownAt), round1(x), round1(y)]);
        movesBetween.push(moves);
        moves = 0;
        const dot = document.createElement('div');
        dot.className = 'mark-dot';
        dot.textContent = String(clicks.length);
        dot.style.left = `${(x / c.width) * 100}%`;
        dot.style.top = `${(y / c.height) * 100}%`;
        marks.appendChild(dot);
        if (clicks.length === c.targets) {
          done = true;
          // Let the last mark show before the panel turns into "checking".
          round.after(250, () => {
            cleanup();
            resolve({ answer: { clicks, moves_between: movesBetween }, drag: { pointer_type: pointerType || 'mouse', untrusted_event: untrusted } });
          });
        }
      };
      const cleanup = () => {
        stage.removeEventListener('pointermove', move);
        stage.removeEventListener('pointerdown', down);
        clear.removeEventListener('click', reset);
      };
      stage.addEventListener('pointermove', move);
      stage.addEventListener('pointerdown', down);
      clear.addEventListener('click', reset);
      round.defer(() => {
        cleanup();
        resolve(null);
      });
    });
  }

  // Shows a select-the-cells puzzle and resolves when the user confirms a
  // selection; resolves null if the round ends first. The answer is every
  // toggle as a click ([ms since shown, x, y] in image coordinates) plus the
  // pointer moves before each: the gateway replays the toggles.
  #grid(round, g) {
    const stage = this.#root.querySelector('.cstage');
    const cellsEl = this.#root.querySelector('.cells');
    const confirm = this.#root.querySelector('.confirm');
    const clear = this.#root.querySelector('.clear');
    this.#root.querySelector('.slider-ui').hidden = true;
    this.#root.querySelector('.jigsaw-ui').hidden = true;
    this.#root.querySelector('.click-ui').hidden = false;
    this.#root.querySelector('.marks').replaceChildren();
    this.#root.querySelector('.prompt').textContent = g.prompt;
    const promptImg = this.#root.querySelector('.prompt-img');
    promptImg.classList.add('ref');
    promptImg.alt = '示例图形';
    promptImg.hidden = !g.prompt_image;
    if (g.prompt_image) promptImg.src = g.prompt_image;
    this.#root.querySelector('.cimg').src = g.image;
    this.#root.querySelector('.msg').textContent = '';
    clear.hidden = false;
    cellsEl.hidden = false;
    confirm.hidden = false;
    confirm.disabled = true;
    cellsEl.style.gridTemplateColumns = `repeat(${g.cols}, 1fr)`;
    cellsEl.style.gridTemplateRows = `repeat(${g.rows}, 1fr)`;
    const cells = [];
    cellsEl.replaceChildren();
    for (let i = 0; i < g.rows * g.cols; i++) {
      const el = document.createElement('div');
      el.className = 'cell';
      cells.push(el);
      cellsEl.appendChild(el);
    }
    this.#view = 'puzzle';
    this.#show('puzzle', '请按提示选出所有符合的格子', '选好后点“确认”');
    const shownAt = performance.now();

    return new Promise((resolve) => {
      let clicks = [];
      let movesBetween = [];
      let moves = 0;
      let pointerType = '';
      let untrusted = false;
      const selected = () => cells.filter((el) => el.classList.contains('on')).length;
      const reset = () => {
        clicks = [];
        movesBetween = [];
        cells.forEach((el) => el.classList.remove('on'));
        confirm.disabled = true;
      };
      const move = (e) => {
        untrusted ||= !e.isTrusted;
        moves++;
      };
      const down = (e) => {
        if (e.button > 0 || e.isPrimary === false || clicks.length >= MAX_GRID_CLICKS) return;
        const rect = stage.getBoundingClientRect();
        const scale = rect.width / g.width;
        if (!(scale > 0)) return;
        const x = (e.clientX - rect.left) / scale, y = (e.clientY - rect.top) / scale;
        if (!(x >= 0 && y >= 0 && x < g.width && y < g.height)) return;
        e.preventDefault();
        untrusted ||= !e.isTrusted;
        pointerType ||= e.pointerType || 'mouse';
        clicks.push([round1(e.timeStamp - shownAt), round1(x), round1(y)]);
        movesBetween.push(moves);
        moves = 0;
        const i = Math.floor((y * g.rows) / g.height) * g.cols + Math.floor((x * g.cols) / g.width);
        cells[i].classList.toggle('on');
        confirm.disabled = selected() === 0;
      };
      const submit = () => {
        if (!selected()) return;
        cleanup();
        resolve({ answer: { clicks, moves_between: movesBetween }, drag: { pointer_type: pointerType || 'mouse', untrusted_event: untrusted } });
      };
      const cleanup = () => {
        stage.removeEventListener('pointermove', move);
        stage.removeEventListener('pointerdown', down);
        clear.removeEventListener('click', reset);
        confirm.removeEventListener('click', submit);
        confirm.disabled = true;
      };
      stage.addEventListener('pointermove', move);
      stage.addEventListener('pointerdown', down);
      clear.addEventListener('click', reset);
      confirm.addEventListener('click', submit);
      round.defer(() => {
        cleanup();
        resolve(null);
      });
    });
  }

  // Shows the jigsaw and resolves when one tile is dropped on another: the
  // two tiles swap on screen and the answer is the pair plus the drag path
  // in image coordinates. Resolves null if the round ends first.
  #jigsaw(round, s) {
    const stage = this.#root.querySelector('.jstage');
    const msg = this.#root.querySelector('.msg');
    this.#root.querySelector('.slider-ui').hidden = true;
    this.#root.querySelector('.click-ui').hidden = true;
    this.#root.querySelector('.jigsaw-ui').hidden = false;
    this.#root.querySelector('.clear').hidden = true;
    msg.textContent = '';
    this.#view = 'puzzle';
    this.#show('puzzle', '请把错位的两块交换回来', '拖动其中一块，放到另一块上');
    stage.style.gridTemplateColumns = `repeat(${s.cols}, 1fr)`;
    stage.style.gridTemplateRows = `repeat(${s.rows}, 1fr)`;
    const tiles = [];
    const paint = (el, index) => {
      const c = index % s.cols, r = Math.floor(index / s.cols);
      el.style.backgroundImage = `url("${s.image}")`;
      el.style.backgroundSize = `${s.cols * 100}% ${s.rows * 100}%`;
      el.style.backgroundPosition = `${s.cols > 1 ? (c / (s.cols - 1)) * 100 : 0}% ${s.rows > 1 ? (r / (s.rows - 1)) * 100 : 0}%`;
    };
    stage.replaceChildren();
    for (let i = 0; i < s.rows * s.cols; i++) {
      const el = document.createElement('div');
      el.className = 'tile';
      paint(el, i);
      tiles.push(el);
      stage.appendChild(el);
    }

    return new Promise((resolve) => {
      let drag = null;
      let points = [];
      let gap = SAMPLE_MS;
      let untrusted = false;
      // Image coordinates of a client point, and the tile under it.
      const locate = (e) => {
        const rect = stage.getBoundingClientRect();
        const scale = rect.width / s.width;
        if (!(scale > 0)) return null;
        const x = (e.clientX - rect.left) / scale, y = (e.clientY - rect.top) / scale;
        const inside = x >= 0 && y >= 0 && x < s.width && y < s.height;
        const tile = inside ? Math.floor(y / (s.height / s.rows)) * s.cols + Math.floor(x / (s.width / s.cols)) : -1;
        return { x, y, tile };
      };
      const record = (t, p, final = false) => {
        const pt = [round1(t), round1(p.x), round1(p.y)];
        const last = points.at(-1);
        if (last && pt[0] - last[0] < gap) {
          if (!final) return;
          if (points.length > 1) {
            points[points.length - 1] = pt;
            return;
          }
        }
        if (points.length >= MAX_POINTS) {
          points = points.filter((_, i) => i % 2 === 0);
          gap *= 2;
        }
        points.push(pt);
      };
      let over = -1;
      const hover = (tile) => {
        if (over === tile) return;
        if (over >= 0) tiles[over].classList.remove('over');
        over = tile !== drag?.from ? tile : -1;
        if (over >= 0) tiles[over].classList.add('over');
      };
      const settle = () => {
        if (drag) {
          const el = tiles[drag.from];
          el.classList.remove('lifted');
          el.style.transform = '';
        }
        hover(-1);
        drag = null;
      };
      const down = (e) => {
        if (drag || e.button > 0 || e.isPrimary === false) return;
        const p = locate(e);
        if (!p || p.tile < 0) return;
        e.preventDefault();
        try { stage.setPointerCapture(e.pointerId); } catch { /* pointer already gone */ }
        untrusted ||= !e.isTrusted;
        msg.textContent = '';
        points = [];
        gap = SAMPLE_MS;
        drag = { id: e.pointerId, from: p.tile, x0: e.clientX, y0: e.clientY, t0: e.timeStamp, type: e.pointerType || 'mouse' };
        tiles[p.tile].classList.add('lifted');
        record(0, p);
      };
      const move = (e) => {
        if (!drag || e.pointerId !== drag.id) return;
        untrusted ||= !e.isTrusted;
        const samples = e.getCoalescedEvents ? e.getCoalescedEvents() : [e];
        let p = null;
        for (const sample of samples.length ? samples : [e]) {
          p = locate(sample);
          if (p) record(sample.timeStamp - drag.t0, p);
        }
        tiles[drag.from].style.transform = `translate(${e.clientX - drag.x0}px, ${e.clientY - drag.y0}px)`;
        if (p) hover(p.tile);
      };
      const up = (e) => {
        if (!drag || e.pointerId !== drag.id) return;
        untrusted ||= !e.isTrusted;
        const p = locate(e);
        const from = drag.from, type = drag.type;
        if (p) record(e.timeStamp - drag.t0, p, true);
        settle();
        if (!p || p.tile < 0 || p.tile === from) {
          msg.textContent = '请把这一块拖到另一块上';
          return;
        }
        // Show the swap before checking, like the real puzzle would.
        paint(tiles[from], p.tile);
        paint(tiles[p.tile], from);
        cleanup();
        resolve({ answer: { swap: [from, p.tile], trajectory: points }, drag: { pointer_type: type, untrusted_event: untrusted } });
      };
      const cancel = (e) => {
        if (!drag || e.pointerId !== drag.id) return;
        settle();
        msg.textContent = '拖动被打断，请重新拖动';
      };
      const cleanup = () => {
        stage.removeEventListener('pointerdown', down);
        stage.removeEventListener('pointermove', move);
        stage.removeEventListener('pointerup', up);
        stage.removeEventListener('pointercancel', cancel);
      };
      stage.addEventListener('pointerdown', down);
      stage.addEventListener('pointermove', move);
      stage.addEventListener('pointerup', up);
      stage.addEventListener('pointercancel', cancel);
      round.defer(() => {
        cleanup();
        settle();
        resolve(null);
      });
    });
  }

  // Proof of work in Web Workers, or on the main thread when workers are
  // unavailable or silent. It belongs to the round: ending the round stops
  // its workers and the main-thread loop and rejects with CANCELLED; the
  // timeout stops them too.
  #solve(round, ch, perPuzzle, quiet) {
    const k = ch.params.puzzles;
    const count = Math.max(1, Math.min(k, navigator.hardwareConcurrency || 2, 8));
    const nonces = new Array(k);
    const expected = k * perPuzzle;
    let solved = 0;
    let attempts = 0;
    let paintedAt = -Infinity;
    const t0 = performance.now();
    const progress = (force = false) => {
      if (quiet || this.#box.dataset.state !== 'solving') return;
      const now = performance.now();
      if (!force && now - paintedAt < PROGRESS_MS) return;
      paintedAt = now;
      this.#progress(solved, k, Math.min(1, (solved / k + attempts / expected) / 2));
    };
    if (!quiet) this.#show('solving', `正在验证 0/${k}`, '');
    progress(true);

    return new Promise((resolve, reject) => {
      if (!round.live) return reject(cancelled());
      let over = false;
      let pool = []; // { worker, revoke }
      let mainThread = false;
      let heard = false;
      const stops = [];
      const stopWorkers = () => {
        for (const { worker, revoke } of pool) {
          worker.postMessage({ type: 'stop' });
          worker.terminate();
          revoke();
        }
        pool = [];
      };
      const finish = (fn) => {
        if (over) return;
        over = true;
        stopWorkers();
        for (const stop of stops) stop();
        fn();
      };
      const fail = (message, code) => finish(() => reject(errorWith(message, code)));
      const done = (workers) => finish(() => resolve({ nonces, attempts, workers, ms: Math.round(performance.now() - t0) }));
      stops.push(round.defer(() => fail('已取消', 'CANCELLED')));
      stops.push(round.after(SOLVE_TIMEOUT_MS, () => fail('验证计算超时', 'TIMEOUT')));

      const inline = () => {
        // Workers unavailable (e.g. a CSP without blob: for a cross-origin
        // script) or silent: solve on the main thread. Slower, but never
        // stuck, and it stops with the round or the timeout like workers do.
        if (mainThread || over) return;
        mainThread = true;
        stopWorkers();
        attempts = 0;
        (async () => {
          for (let i = 0; i < k; i++) {
            const r = await solvePuzzle(ch.algorithm, ch.challenge_id, i, ch.params.target, {
              shouldStop: () => over,
              onAttempts: (n) => {
                attempts += n;
                progress();
              },
            });
            if (!r) return; // stopped: whoever stopped it has settled
            nonces[i] = r.nonce;
            solved++;
            progress(true);
          }
          done(0);
        })().catch((err) => fail(String(err && err.message || err), 'SOLVER'));
      };

      for (let w = 0; w < count; w++) {
        const spawned = spawnWorker();
        if (!spawned) return inline();
        pool.push(spawned);
        const { worker } = spawned;
        worker.onmessage = ({ data }) => {
          if (over || mainThread) return;
          heard = true;
          spawned.revoke(); // it has loaded: the blob URL is no longer needed
          if (data.type === 'progress') {
            attempts += data.attempts;
          } else if (data.type === 'solved') {
            nonces[data.index] = data.nonce;
            solved++;
          } else if (data.type === 'error') {
            return fail(data.message, 'SOLVER');
          }
          progress(solved === k);
          if (solved === k) done(count);
        };
        worker.onerror = (e) => {
          if (over || mainThread) return;
          e?.preventDefault?.();
          if (solved === 0 && attempts === 0) inline();
          else fail('Worker 出错', 'SOLVER');
        };
        const indices = [];
        for (let i = w; i < k; i += count) indices.push(i);
        worker.postMessage({ type: 'solve', algorithm: ch.algorithm, challengeId: ch.challenge_id, target: ch.params.target, indices });
      }
      // A worker that never loads (blocked module import, a CSP that
      // silently drops it) may raise no error at all.
      stops.push(round.after(WORKER_SILENCE_MS, () => heard || inline()));
    });
  }

  #fail(round, err) {
    if (!round.live) return;
    round.end();
    this.#view = null;
    this.#setToken('');
    const code = err.code || 'ERROR';
    this.#show('error', `${err.message}，点击重试`, code);
    this.#trace('error', { code, message: err.message, status: err.status });
    this.#emit('botguard:error', { code, message: err.message, status: err.status });
    this.#settle(errorWith(err.message, code, { status: err.status }));
  }

  #idle() {
    this.#show('idle', '点击进行人机验证', '大多数情况下无需操作，必要时会出现拼图或点选题');
  }

  #setToken(token, ttl = 0) {
    this.#token = token;
    if (this.#input) this.#input.value = token;
    clearTimeout(this.#expiryTimer);
    this.#tokenDeadline = token ? performance.now() + ttl : 0;
    if (token && this.isConnected) this.#armExpiry();
  }

  #armExpiry() {
    clearTimeout(this.#expiryTimer);
    this.#expiryTimer = setTimeout(() => {
      this.#setToken('');
      this.#show('idle', '验证已过期，点击重新验证', '');
      this.#emit('botguard:expired', {});
    }, Math.max(0, this.#tokenDeadline - performance.now()));
  }

  #show(state, title, sub) {
    this.#box.dataset.state = this.#panel.dataset.state = state;
    for (const el of this.#root.querySelectorAll('.title')) el.textContent = title;
    for (const el of this.#root.querySelectorAll('.sub')) el.textContent = sub;
    this.#box.setAttribute('aria-label', title);
    this.#render();
  }

  // Shows or hides the panel. Embed mode shows it only while a puzzle is up;
  // popup mode shows it for the whole attempt, so loading, progress and
  // errors are visible too, and manages focus like a modal dialog.
  #render() {
    const popup = this.getAttribute('mode') === 'popup';
    const open = this.isConnected && (popup ? this.#dialogOpen : this.#view === 'puzzle');
    this.#panel.dataset.view = this.#view || 'status';
    if (popup) this.#panel.setAttribute('aria-modal', 'true');
    else this.#panel.removeAttribute('aria-modal');
    if (open === !this.#panel.hidden) return;
    const focusInside = this.#panel.contains(this.#root.activeElement);
    this.#panel.hidden = !open;
    if (open) {
      if (!popup) return;
      const active = document.activeElement;
      this.#returnFocus = active && active !== this && active !== document.body ? active : null;
      this.#panel.focus({ preventScroll: true });
      return;
    }
    const back = popup ? this.#returnFocus : this.#box;
    this.#returnFocus = null;
    // Popup: also when focus fell to <body> because what held it went away.
    const lost = popup && (!document.activeElement || document.activeElement === document.body);
    if ((focusInside || lost) && back?.isConnected) back.focus({ preventScroll: true });
  }

  // Popup dialog keys: Esc closes, Tab stays inside.
  #dialogKey(e) {
    if (this.getAttribute('mode') !== 'popup') return;
    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      this.#close();
    } else if (e.key === 'Tab') {
      const items = [...this.#panel.querySelectorAll('button, [tabindex="0"]')].filter((el) => el.getClientRects().length);
      if (!items.length) return;
      const current = this.#root.activeElement;
      if (e.shiftKey && (current === items[0] || current === this.#panel)) {
        e.preventDefault();
        items.at(-1).focus();
      } else if (!e.shiftKey && current === items.at(-1)) {
        e.preventDefault();
        items[0].focus();
      }
    }
  }

  #progress(solved, k, fraction) {
    const title = `正在验证 ${solved}/${k}`;
    for (const el of this.#root.querySelectorAll('.title')) if (el.textContent !== title) el.textContent = title;
    for (const el of this.#root.querySelectorAll('.bar > i')) el.style.width = `${Math.round(fraction * 100)}%`;
  }

  #emit(name, detail) {
    this.dispatchEvent(new CustomEvent(name, { detail, bubbles: true, composed: true }));
  }

  #trace(step, detail) {
    this.#emit('botguard:trace', { step, ...detail });
  }
}

if (globalThis.customElements && !customElements.get('botguard-widget')) customElements.define('botguard-widget', BotGuardWidget);

// Resolves a selector or element option; a selector that matches nothing is
// a mistake worth reporting, not a reason to fall back silently.
function resolveElement(target, option) {
  if (!target) return null;
  if (typeof target !== 'string') return target;
  let el = null;
  try {
    el = document.querySelector(target);
  } catch {
    throw new Error(`BotGuard.init: ${option} 不是有效的选择器：${target}`);
  }
  if (!el) throw new Error(`BotGuard.init: 找不到 ${option} 指定的元素：${target}`);
  return el;
}

/**
 * Function-style setup, modelled on Yidun's initNECaptcha. Options:
 *   element   container (selector or node) for embed mode
 *   endpoint, appKey, action, fieldName
 *   mode      "embed" (default) or "popup"
 *   bindTo    popup mode: a submit button; submitting its form (any submit
 *             button, or Enter in a field) verifies first, then submits with
 *             the token and clears it. A button outside a form is guarded at
 *             its click instead, and the click is replayed after verifying.
 *   width       max width, e.g. 320 or "100%" (default 360px)
 *   imageHeight max puzzle image height in px; images narrow to keep ratio
 *   size        "compact" for H5 pages and app WebViews
 *   accent      accent colour
 *   nonce       CSP nonce for the widget's <style>, needed only by browsers
 *               without constructable stylesheets
 *   kinds       kinds to ask for: ['slider'] fixed, ['slider', 'rotate'] at
 *               random, ['*'] every kind the action offers
 *   levels      a kind ladder, easiest first, e.g. [['invisible'],
 *               ['slider', 'rotate'], ['spatial']]: failures move the user
 *               up a tier, passes in a row move them down
 *   onVerify(err, ret)  ret = { token, kind }
 * Returns { verify(), reset(), destroy(), token, element }.
 */
export function init(config) {
  if (!globalThis.document) throw new Error('BotGuard.init 只能在浏览器中调用');
  const button = resolveElement(config.bindTo, 'bindTo');
  // The hidden token field must sit inside the form that gets submitted.
  const host = resolveElement(config.element, 'element') || button?.form || document.body;
  if (!host) throw new Error('BotGuard.init: 页面还没有 <body>，请在 DOM 就绪后调用');

  const w = document.createElement('botguard-widget');
  w.setAttribute('endpoint', config.endpoint || '');
  w.setAttribute('app-key', config.appKey || '');
  w.setAttribute('action', config.action || '');
  if (config.fieldName) w.setAttribute('field-name', config.fieldName);
  const mode = config.mode === 'popup' ? 'popup' : 'embed';
  w.setAttribute('mode', mode);
  if (config.auto) w.setAttribute('auto', '');
  // Sizing for H5 pages and app WebViews: see the CSS custom properties above.
  if (config.width != null) w.setAttribute('width', String(config.width));
  if (config.imageHeight != null) w.setAttribute('image-height', String(config.imageHeight));
  if (config.size) w.setAttribute('size', config.size);
  if (config.accent) w.setAttribute('accent', config.accent);
  if (config.nonce) w.setAttribute('nonce', config.nonce);
  if (config.kinds != null) w.setAttribute('kinds', kindsAttr(config.kinds));
  if (config.levels != null) w.setAttribute('levels', levelsAttr(config.levels));
  if (typeof config.transport === 'function') w.transport = config.transport;
  host.appendChild(w);

  w.addEventListener('botguard:verified', (e) => config.onVerify?.(null, { token: e.detail.token, kind: e.detail.kind }));
  w.addEventListener('botguard:error', (e) => e.detail.code !== 'ANSWER_INVALID' && config.onVerify?.(e.detail, null));

  const unbind = button ? bindSubmit(w, button) : () => {};
  return {
    element: w,
    get token() { return w.token; },
    verify: () => w.verify(),
    reset: () => w.reset(),
    destroy() {
      unbind();
      w.remove();
    },
  };
}

// Yidun's "bind" mode. A button in a form is guarded at the form's submit
// event, so Enter in a field and other submit buttons are covered and the
// page's own click handlers still run; a button outside any form is guarded
// at its click. However often the user clicks, one verification runs and the
// intercepted submission is replayed once, with the token.
function bindSubmit(w, button) {
  const form = button.form;
  let waiting = false; // a verify() is under way for an intercepted submission
  let retryable = false; // popup: it failed, but the user may retry from the dialog
  let replay = null;

  const intercept = (then) => {
    retryable = false;
    replay = then;
    if (waiting) return;
    waiting = true;
    w.verify().then(() => {
      waiting = false;
      replay();
    }, (err) => {
      waiting = false;
      retryable = w.getAttribute('mode') === 'popup' && err.code !== 'CANCELLED' && err.code !== 'TOO_MANY_FAILURES';
    });
  };
  // Tokens are single-use: clear it once the submission (and the page's own
  // handlers) have read it, not before.
  const passed = () => setTimeout(() => w.reset(), 0);

  const onVerified = () => {
    if (!retryable || waiting) return;
    retryable = false;
    replay();
  };
  const onCancel = () => { retryable = false; };
  w.addEventListener('botguard:verified', onVerified);
  w.addEventListener('botguard:cancel', onCancel);

  let target;
  let type;
  let listener;
  if (form) {
    [target, type] = [form, 'submit'];
    listener = (e) => {
      if (w.token) return passed();
      // Not verified yet: nothing else may act on this submission.
      e.preventDefault();
      e.stopImmediatePropagation();
      const submitter = 'submitter' in e ? e.submitter : button;
      intercept(() => submitForm(form, submitter));
    };
  } else {
    [target, type] = [button, 'click'];
    listener = (e) => {
      if (w.token) return passed();
      e.preventDefault();
      e.stopImmediatePropagation();
      intercept(() => button.click());
    };
  }
  target.addEventListener(type, listener, true);
  return () => {
    target.removeEventListener(type, listener, true);
    w.removeEventListener('botguard:verified', onVerified);
    w.removeEventListener('botguard:cancel', onCancel);
  };
}

// Replays a submission with constraint validation and the submit event, as a
// click would. Safari before 16 has no requestSubmit(), so a submit button
// (the original one, or a temporary hidden one) is clicked instead.
function submitForm(form, submitter) {
  if (submitter && (submitter.form !== form || !submitter.isConnected)) submitter = null;
  if (typeof form.requestSubmit === 'function') return submitter ? form.requestSubmit(submitter) : form.requestSubmit();
  if (submitter) return submitter.click();
  const temp = document.createElement('input');
  temp.type = 'submit';
  temp.hidden = true;
  form.appendChild(temp);
  temp.click();
  temp.remove();
}

/** Same signature as Yidun's initNECaptcha(config, onload, onerror). */
export function initBotGuard(config, onload, onerror) {
  try {
    const instance = init(config);
    onload?.(instance);
    return instance;
  } catch (err) {
    onerror?.(err);
    return null;
  }
}

if (globalThis.window) {
  // Calls queued by the async-loading stub (see load.js) before this ran.
  const queued = globalThis.initBotGuard?.q;
  globalThis.BotGuard = { init, initBotGuard };
  globalThis.initBotGuard = initBotGuard;
  if (Array.isArray(queued)) for (const args of queued) initBotGuard(...args);
}
