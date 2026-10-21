// Risk signals for BotGuard (docs/challenge-types.md §5). Only derived
// booleans and counters leave the browser — no raw fingerprints. Every one of
// these can be forged by a determined attacker; they exist to make cheap
// automation visible, and the server weighs them alongside facts it observes
// itself.

const AUTOMATION_GLOBALS = [
  '__playwright__binding__', '__pwInitScripts', '_phantom', 'callPhantom', '__nightmare',
  'domAutomation', 'domAutomationController', '__selenium_unwrapped', '__webdriver_evaluate',
  '__driver_evaluate', '__webdriver_script_fn', '__fxdriver_unwrapped',
];

const MOBILE = /Mobi|Android|iPhone|iPad|iPod/i;

let rendererCache = null;

// Creating a WebGL context is the only costly probe, so it runs once per page.
function softwareRenderer(doc) {
  if (rendererCache === null) rendererCache = probeRenderer(doc);
  return rendererCache;
}

function probeRenderer(doc) {
  try {
    const gl = doc.createElement('canvas').getContext('webgl');
    if (!gl) return false;
    const ext = gl.getExtension('WEBGL_debug_renderer_info');
    const renderer = ext ? gl.getParameter(ext.UNMASKED_RENDERER_WEBGL) : gl.getParameter(gl.RENDERER);
    return /SwiftShader|llvmpipe|softpipe|Software Rasterizer/i.test(String(renderer));
  } catch {
    return false;
  }
}

function platformMismatch(nav) {
  const ua = nav.userAgent || '';
  const platform = nav.platform || '';
  if (/Windows/.test(ua) && platform && !/Win/i.test(platform)) return true;
  if (/Macintosh/.test(ua) && platform && !/Mac/i.test(platform)) return true;
  if (/iPhone|Android/.test(ua) && nav.maxTouchPoints === 0) return true;
  return false;
}

// Headless Chrome reports notification permission "denied" while the
// Permissions API says "prompt"; real browsers agree with themselves.
async function permissionMismatch(win, nav) {
  try {
    if (!win.Notification || !nav.permissions?.query) return false;
    const status = await nav.permissions.query({ name: 'notifications' });
    return win.Notification.permission === 'denied' && status.state === 'prompt';
  } catch {
    return false;
  }
}

/**
 * Environment signals. Call at activation, not at page load: a page loaded in
 * a background tab or a not-yet-shown WebView reports a 0×0 outer window.
 */
export async function collectEnvironment(win = globalThis.window, nav = globalThis.navigator, doc = globalThis.document) {
  const ua = nav.userAgent || '';
  const docKeys = doc ? Object.keys(doc) : [];
  return {
    webdriver: nav.webdriver === true,
    headless_ua: /HeadlessChrome|PhantomJS/i.test(ua),
    software_gl: doc ? softwareRenderer(doc) : false,
    zero_outer: win.outerWidth === 0 || win.outerHeight === 0,
    desktop_no_plugins: !MOBILE.test(ua) && !!nav.plugins && nav.plugins.length === 0,
    no_languages: !nav.languages || nav.languages.length === 0,
    ua_platform_mismatch: platformMismatch(nav),
    permission_mismatch: await permissionMismatch(win, nav),
    automation_globals: AUTOMATION_GLOBALS.some((k) => k in win) || docKeys.some((k) => /^\$(cdc|wdc)_/.test(k)),
    user_agent: ua.slice(0, 512),
  };
}

/**
 * Counts pointer movement on the page until the widget is activated, so the
 * server can tell a click that arrived from nowhere from one a hand made.
 */
export function trackInteraction(doc = globalThis.document, now = () => performance.now()) {
  let moves = 0;
  // iOS WebKit synthesises the click after a tap with an empty or "mouse"
  // pointerType; the pointerdown that started it carries the real one.
  let lastDown = { type: '', at: -Infinity };
  const onMove = (e) => {
    if (e.isTrusted) moves++;
  };
  const onDown = (e) => {
    if (e.isTrusted) lastDown = { type: e.pointerType || '', at: now() };
  };
  doc.addEventListener('pointermove', onMove, { passive: true, capture: true });
  doc.addEventListener('pointerdown', onDown, { passive: true, capture: true });
  return {
    /** Snapshot at activation. trigger is the click/keydown event. */
    snapshot(trigger) {
      const keyboard = trigger && trigger.type === 'keydown';
      const recentTouch = (lastDown.type === 'touch' || lastDown.type === 'pen') && now() - lastDown.at < 1000;
      return {
        ms_to_click: Math.round(now()),
        pointer_moves_before_click: moves,
        untrusted_event: !!trigger && trigger.isTrusted === false,
        pointer_type: keyboard ? 'keyboard' : recentTouch ? lastDown.type : (trigger && trigger.pointerType) || 'mouse',
      };
    },
    stop() {
      doc.removeEventListener('pointermove', onMove, { capture: true });
      doc.removeEventListener('pointerdown', onDown, { capture: true });
    },
  };
}
