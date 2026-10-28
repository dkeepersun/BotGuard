// node --test packages/browser/test/*.test.mjs
// The display modes: embed (default), float (trigger), inline (embedded,
// starts by itself) and popup.
import { test, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import { $, challenges, flush, gateway, init, setup, solveAll, state, teardown, tick, widget } from './harness.mjs';

beforeEach(() => {
  setup();
  gateway.kind = 'slider';
});
afterEach(teardown);

const pointer = (el, type, pointerType = 'mouse') => {
  const e = new window.Event(type);
  Object.defineProperty(e, 'pointerType', { value: pointerType });
  el.dispatchEvent(e);
};
const panelShown = (el) => !$(el, '.panel').hidden;

test('float: hovering starts and shows the puzzle over the page; leaving hides it', async () => {
  const el = widget({ mode: 'float' });
  assert.equal(challenges().length, 0, 'nothing loads before the pointer arrives');
  pointer(el, 'pointerenter');
  await flush();
  assert.equal(state(el), 'puzzle');
  assert.ok(panelShown(el));
  assert.ok(['above', 'below'].includes($(el, '.panel').dataset.place));
  pointer(el, 'pointerleave');
  tick(299);
  assert.ok(panelShown(el), 'a short trip across the gap does not close it');
  tick(1);
  assert.ok(!panelShown(el));
  assert.equal(state(el), 'puzzle', 'the puzzle waits for the user to come back');
  pointer(el, 'pointerenter');
  await flush();
  assert.ok(panelShown(el));
  assert.equal(challenges().length, 1, 'coming back reuses the puzzle');
});

test('float: a drag in progress keeps the panel; Esc and a tap elsewhere hide it', async () => {
  const el = widget({ mode: 'float' });
  pointer(el, 'pointerenter');
  await flush();
  const handle = $(el, '.handle');
  handle.classList.add('dragging');
  pointer(el, 'pointerleave');
  tick(1000);
  assert.ok(panelShown(el), 'still dragging');
  handle.classList.remove('dragging');
  tick(300);
  assert.ok(!panelShown(el));
  // Keyboard: focus shows it, Esc hides it.
  el.dispatchEvent(new window.FocusEvent('focusin', { bubbles: true }));
  assert.ok(panelShown(el));
  $(el, '.panel').dispatchEvent(new window.KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
  tick(0);
  assert.ok(!panelShown(el));
  // Touch: a tap on the box shows it, a tap elsewhere hides it.
  $(el, '.box').click();
  await flush();
  assert.ok(panelShown(el));
  pointer(document.body, 'pointerdown', 'touch');
  tick(0);
  assert.ok(!panelShown(el));
});

test('inline: starts by itself, marks the start as automatic, and reloads after reset()', async () => {
  const el = widget({ mode: 'inline' });
  await flush();
  assert.equal(challenges().length, 1);
  assert.equal(challenges()[0].body.signals.activation, 'auto');
  assert.equal(state(el), 'puzzle');
  assert.ok(panelShown(el));
  el.reset();
  await flush();
  assert.equal(challenges().length, 2, 'the next puzzle loads by itself');
  assert.equal(state(el), 'puzzle');
});

test('embed waits for a click; neither it nor the auto attribute is marked automatic', async () => {
  const el = widget();
  await flush();
  assert.equal(challenges().length, 0);
  $(el, '.box').click();
  await flush();
  assert.equal(challenges()[0].body.signals.activation, undefined);
  assert.ok(panelShown(el));
  widget({ auto: '' });
  await flush();
  assert.equal(challenges().length, 2);
  assert.equal(challenges()[1].body.signals.activation, undefined, 'judged as before');
});

test('init() accepts the four modes and falls back to embed', () => {
  const host = document.createElement('div');
  document.body.appendChild(host);
  for (const mode of ['float', 'inline', 'popup', 'embed']) {
    assert.equal(init({ element: host, endpoint: 'https://gw.example', appKey: 'app_test', action: 'login', mode }).element.getAttribute('mode'), mode);
  }
  assert.equal(init({ element: host, endpoint: 'https://gw.example', appKey: 'app_test', action: 'login', mode: 'weird' }).element.getAttribute('mode'), 'embed');
});

test('float: verify() from a bound button shows the panel without hovering', async () => {
  const el = widget({ mode: 'float' });
  const p = el.verify();
  p.catch(() => {});
  await flush();
  assert.equal(state(el), 'puzzle');
  assert.ok(panelShown(el));
});

test('inline: the next attempt starts by itself after the cooldown and after the token expires', async () => {
  gateway.kind = 'invisible'; // answered by the proof of work alone
  gateway.solution = () => ({ error: 'ANSWER_INVALID' });
  const el = widget({ mode: 'inline' });
  for (let i = 0; i < 3; i++) {
    await flush();
    await solveAll();
    if (i < 2) tick(900);
  }
  assert.equal(state(el), 'cooldown');
  const before = challenges().length;
  gateway.solution = () => ({ token: 'ok', expires_in: 120 });
  tick(10_000);
  await flush();
  assert.equal(challenges().length, before + 1);
  await solveAll();
  assert.equal(el.token, 'ok');
  let expired = 0;
  el.addEventListener('botguard:expired', () => expired++);
  tick(120_000);
  await flush();
  assert.equal(expired, 1);
  assert.equal(challenges().length, before + 2);
  assert.equal(challenges().at(-1).body.signals.activation, 'auto');
});

test('switching the mode attribute at run time: to inline loads a puzzle, to float hides the panel', async () => {
  const el = widget();
  await flush();
  assert.equal(challenges().length, 0);
  el.setAttribute('mode', 'inline');
  await flush();
  assert.equal(challenges().length, 1);
  assert.equal(challenges()[0].body.signals.activation, 'auto');
  assert.ok(panelShown(el));
  el.setAttribute('mode', 'float');
  assert.ok(!panelShown(el), 'float waits for the pointer');
  pointer(el, 'pointerenter');
  await flush();
  assert.ok(panelShown(el));
  assert.equal(challenges().length, 1, 'the puzzle under way is kept');
  el.setAttribute('mode', 'popup');
  assert.ok(panelShown(el), 'the dialog shows the puzzle under way');
  el.setAttribute('mode', 'embed');
  assert.ok(panelShown(el));
  assert.equal(challenges().length, 1);
});

test('float: verify() cancels a hide still pending; Esc elsewhere on the page hides it', async () => {
  const el = widget({ mode: 'float' });
  pointer(el, 'pointerenter');
  await flush();
  pointer(el, 'pointerleave');
  el.verify().catch(() => {});
  tick(1000);
  assert.ok(panelShown(el), 'verify() keeps it up');
  document.body.dispatchEvent(new window.KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
  tick(0);
  assert.ok(!panelShown(el));
});

test('float: passing over the box again after an error does not resend', async () => {
  gateway.challenge = () => ({ error: 'RATE_LIMITED' });
  const el = widget({ mode: 'float' });
  pointer(el, 'pointerenter');
  await flush();
  assert.equal(state(el), 'error');
  pointer(el, 'pointerleave');
  pointer(el, 'pointerenter');
  await flush();
  assert.equal(challenges().length, 1);
});

test('inline: a puzzle left alone is replaced shortly before it expires', async () => {
  const el = widget({ mode: 'inline' });
  await flush();
  assert.equal(challenges().length, 1);
  tick(109_000); // expires in 120 s; replaced 10 s before
  await flush();
  assert.equal(challenges().length, 1);
  tick(1_000);
  await flush();
  assert.equal(challenges().length, 2);
  assert.equal(challenges()[1].body.signals.activation, 'auto');
  assert.equal(state(el), 'puzzle');
});
