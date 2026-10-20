// node --test packages/browser/test
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { collectEnvironment, trackInteraction } from '../src/signals.js';

const desktopChrome = {
  userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36',
  platform: 'MacIntel', languages: ['zh-CN', 'en'], plugins: { length: 5 }, maxTouchPoints: 0, webdriver: false,
};

test('a normal desktop browser raises no flags', async () => {
  const env = await collectEnvironment({ outerWidth: 1440, outerHeight: 900 }, desktopChrome, null);
  for (const [k, v] of Object.entries(env)) {
    if (k !== 'user_agent') assert.equal(v, false, k);
  }
});

test('automation traits are reported', async () => {
  const nav = { ...desktopChrome, webdriver: true, userAgent: desktopChrome.userAgent.replace('Chrome', 'HeadlessChrome'), languages: [], plugins: { length: 0 } };
  const env = await collectEnvironment({ outerWidth: 0, outerHeight: 0, __playwright__binding__: {} }, nav, null);
  assert.equal(env.webdriver, true);
  assert.equal(env.headless_ua, true);
  assert.equal(env.zero_outer, true);
  assert.equal(env.no_languages, true);
  assert.equal(env.desktop_no_plugins, true);
  assert.equal(env.automation_globals, true);
});

test('mobile browsers without plugins are not flagged', async () => {
  const nav = { ...desktopChrome, userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0) Mobile/15E148', platform: 'iPhone', plugins: { length: 0 }, maxTouchPoints: 5 };
  const env = await collectEnvironment({ outerWidth: 390, outerHeight: 844 }, nav, null);
  assert.equal(env.desktop_no_plugins, false);
  assert.equal(env.ua_platform_mismatch, false);
});

test('a UA that lies about its platform is flagged', async () => {
  const nav = { ...desktopChrome, userAgent: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/140', platform: 'Linux x86_64' };
  assert.equal((await collectEnvironment({ outerWidth: 1, outerHeight: 1 }, nav, null)).ua_platform_mismatch, true);
});

test('interaction tracker counts trusted moves and classifies the trigger', () => {
  const listeners = {};
  const doc = { addEventListener: (t, fn) => (listeners[t] = fn), removeEventListener: () => {} };
  const tracker = trackInteraction(doc, () => 1234.4);
  listeners.pointermove({ isTrusted: true });
  listeners.pointermove({ isTrusted: true });
  listeners.pointermove({ isTrusted: false });
  assert.deepEqual(tracker.snapshot({ type: 'click', isTrusted: true, pointerType: 'mouse' }),
    { ms_to_click: 1234, pointer_moves_before_click: 2, untrusted_event: false, pointer_type: 'mouse' });
  assert.equal(tracker.snapshot({ type: 'keydown', isTrusted: true }).pointer_type, 'keyboard');
  assert.equal(tracker.snapshot({ type: 'click', isTrusted: false }).untrusted_event, true);
});
