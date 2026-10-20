// node --test packages/browser/test/*.test.mjs
import { test, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import { $, challenges, flush, gateway, init, liveWorkers, setup, solveAll, solutions, state, teardown, tick, widget, workers } from './harness.mjs';

beforeEach(setup);
afterEach(teardown);

test("an abandoned attempt's timeout does not stop the next attempt's workers", async () => {
  const el = widget();
  el.start();
  await flush();
  const first = liveWorkers();
  assert.ok(first.length > 0);
  tick(30_000);
  el.reset();
  assert.ok(first.every((w) => w.terminated), 'reset stops the first attempt');
  el.start();
  await flush();
  const second = liveWorkers();
  tick(30_000); // the first attempt's 60 s timeout would fire now
  assert.ok(second.every((w) => !w.terminated));
  assert.equal(state(el), 'solving');
  await solveAll();
  assert.equal(state(el), 'verified');
  assert.equal(el.token, 'tok3');
});

test('the solve timeout stops its own workers and reports TIMEOUT', async () => {
  const el = widget();
  const p = el.verify();
  p.catch(() => {}); // observed below, after the timeout
  await flush();
  const running = liveWorkers();
  tick(60_000);
  await flush();
  assert.ok(running.every((w) => w.terminated));
  await assert.rejects(p, { code: 'TIMEOUT' });
  assert.equal(state(el), 'error');
});

test('silent workers fall back to the main thread', async () => {
  globalThis.Worker.silent = true;
  const el = widget();
  const p = el.verify();
  await flush();
  tick(5_000);
  assert.ok(workers.every((w) => w.terminated));
  assert.equal(await p, 'tok2');
  assert.deepEqual(solutions()[0].body.nonces, ['0', '0']);
});

test('verify() calls share one promise and one attempt', async () => {
  const el = widget();
  const a = el.verify();
  const b = el.verify();
  assert.equal(a, b);
  await flush();
  el.verify();
  await solveAll();
  assert.equal(await a, 'tok2');
  assert.equal(challenges().length, 1);
  assert.equal(await el.verify(), 'tok2', 'verified: resolves at once');
});

test('reset() rejects a pending verify() with CANCELLED', async () => {
  const el = widget();
  const p = el.verify();
  await flush();
  el.reset();
  await assert.rejects(p, { code: 'CANCELLED' });
  assert.equal(state(el), 'idle');
});

test('three wrong answers reject verify() and the cooldown rejects at once', async () => {
  gateway.solution = () => ({ error: 'ANSWER_INVALID' });
  const el = widget();
  const p = el.verify();
  let settled = false;
  p.catch(() => {}).finally(() => { settled = true; });
  for (let i = 0; i < 3; i++) {
    await flush();
    await solveAll();
    if (i < 2) {
      assert.equal(settled, false, 'a wrong answer alone is retried');
      tick(900);
    }
  }
  await assert.rejects(p, { code: 'TOO_MANY_FAILURES' });
  assert.equal(state(el), 'cooldown');
  await assert.rejects(el.verify(), { code: 'TOO_MANY_FAILURES' });
  tick(10_000);
  assert.equal(state(el), 'idle');
  gateway.solution = () => ({ token: 'ok', expires_in: 120 });
  const again = el.verify();
  await flush();
  await solveAll();
  assert.equal(await again, 'ok');
});

test('removing the widget rejects verify() and it works again once re-inserted', async () => {
  const el = widget();
  const p = el.verify();
  await flush();
  const running = liveWorkers();
  el.remove();
  await assert.rejects(p, { code: 'CANCELLED' });
  assert.ok(running.every((w) => w.terminated));
  document.body.appendChild(el);
  assert.equal(state(el), 'idle');
  const again = el.verify();
  await flush();
  await solveAll();
  assert.equal(await again, 'tok3');
});

test('the expiry timer is re-armed when the widget is re-inserted', async () => {
  const el = widget();
  let expired = 0;
  el.addEventListener('botguard:expired', () => expired++);
  el.verify();
  await flush();
  await solveAll();
  assert.equal(el.token, 'tok2');
  el.remove();
  document.body.appendChild(el);
  tick(100_000);
  assert.equal(el.token, 'tok2');
  tick(17_000); // 120 s minus the 3 s margin
  assert.equal(expired, 1);
  assert.equal(el.token, '');
  assert.equal(state(el), 'idle');
});

test('a client clock running ahead does not expire the token at once', async () => {
  // Server time is 60 s behind: expires_at is "now" by the client's clock.
  gateway.solution = () => ({ token: 'skewed', expires_at: Math.floor(Date.now() / 1000), expires_in: 60 });
  const el = widget();
  el.verify();
  await flush();
  await solveAll();
  tick(56_000);
  assert.equal(el.token, 'skewed', 'expires_in wins over expires_at');
  tick(1_000);
  assert.equal(el.token, '');
});

test('without expires_in an implausible expires_at falls back to 60 s', async () => {
  gateway.solution = () => ({ token: 'old-gateway', expires_at: Math.floor(Date.now() / 1000) - 30 });
  const el = widget();
  el.verify();
  await flush();
  await solveAll();
  tick(56_000);
  assert.equal(el.token, 'old-gateway');
  tick(1_000);
  assert.equal(el.token, '');
});

test('changing action, app-key or field-name drops the token', async () => {
  const el = widget();
  el.verify();
  await flush();
  await solveAll();
  assert.ok(el.token);
  el.setAttribute('action', 'register');
  assert.equal(el.token, '');
  assert.equal(state(el), 'idle');
  el.setAttribute('field-name', 'captcha');
  assert.equal(el.querySelector('input[type=hidden]').name, 'captcha');
});

test('popup mode shows loading, errors and moves focus like a dialog', async () => {
  gateway.challenge = () => ({ error: 'APP_DISABLED', message: '应用已停用', status: 403 });
  const trigger = document.createElement('button');
  document.body.appendChild(trigger);
  trigger.focus();
  const el = widget({ mode: 'popup' });
  const panel = $(el, '.panel');
  const p = el.verify();
  p.catch(() => {});
  assert.equal(panel.hidden, false, 'shown while loading');
  assert.equal(panel.dataset.state, 'loading');
  assert.equal(panel.getAttribute('aria-modal'), 'true');
  assert.equal(el.shadowRoot.activeElement, panel);
  await flush();
  await assert.rejects(p, { code: 'APP_DISABLED' });
  assert.equal(panel.hidden, false, 'the error stays visible');
  assert.equal(panel.dataset.state, 'error');
  assert.match($(el, '.head .title').textContent, /应用已停用/);
  let cancelled = 0;
  el.addEventListener('botguard:cancel', () => cancelled++);
  panel.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
  assert.equal(panel.hidden, true);
  assert.equal(cancelled, 1);
  assert.equal(document.activeElement, trigger, 'focus returns to the trigger');
});

test('init() reports a selector that matches nothing', () => {
  assert.throws(() => init({ element: '#missing', endpoint: 'x', appKey: 'a', action: 'login' }), /找不到 element/);
  assert.throws(() => init({ mode: 'popup', bindTo: '#nope', endpoint: 'x', appKey: 'a', action: 'login' }), /找不到 bindTo/);
});

test('the stylesheet carries the nonce when it has to be a <style> element', () => {
  const el = widget({ nonce: 'abc123' });
  const style = el.shadowRoot.querySelector('style');
  assert.ok(style, 'jsdom has no constructable stylesheets');
  assert.equal(style.getAttribute('nonce'), 'abc123');
});
