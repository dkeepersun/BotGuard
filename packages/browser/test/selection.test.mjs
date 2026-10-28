// node --test packages/browser/test/*.test.mjs
import { test, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { challenges, flush, init, setup, teardown, widget } from './harness.mjs';

beforeEach(setup);
afterEach(teardown);

const firstBody = async (el) => {
  el.verify().catch(() => {});
  await flush();
  return challenges()[0].body;
};

test('no kinds or levels: the request leaves the choice to the gateway', async () => {
  const body = await firstBody(widget());
  assert.equal('kinds' in body, false);
  assert.equal('levels' in body, false);
});

test('kinds attribute asks for one kind, several, or all', async () => {
  assert.deepEqual((await firstBody(widget({ kinds: ' slider, rotate ' }))).kinds, ['slider', 'rotate']);
  teardown();
  setup();
  assert.deepEqual((await firstBody(widget({ kinds: '*' }))).kinds, ['*']);
});

test('levels attribute sends the ladder and wins over kinds', async () => {
  const body = await firstBody(widget({ kinds: 'slider', levels: 'invisible; slider,rotate;; spatial' }));
  assert.deepEqual(body.levels, [['invisible'], ['slider', 'rotate'], ['spatial']]);
  assert.equal('kinds' in body, false);
});

test('init() takes kinds and levels as arrays', async () => {
  const a = init({ element: document.body, endpoint: 'https://gw.example', appKey: 'app_test', action: 'login', kinds: ['click_text', 'spatial'] });
  assert.equal(a.element.getAttribute('kinds'), 'click_text,spatial');
  const b = init({ element: document.body, endpoint: 'https://gw.example', appKey: 'app_test', action: 'login',
    levels: [['invisible'], ['slider', 'rotate'], ['spatial']] });
  assert.equal(b.element.getAttribute('levels'), 'invisible;slider,rotate;spatial');
  assert.deepEqual((await firstBody(b.element)).levels, [['invisible'], ['slider', 'rotate'], ['spatial']]);
});

test('every challenge request carries the widget version, equal to package.json', async () => {
  const { VERSION } = await import('../src/botguard.js');
  const pkg = JSON.parse(await readFile(new URL('../package.json', import.meta.url), 'utf8'));
  assert.equal(VERSION, pkg.version);
  assert.equal((await firstBody(widget())).widget, VERSION);
});
