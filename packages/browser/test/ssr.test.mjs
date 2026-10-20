// node --test packages/browser/test/*.test.mjs
// Runs in plain Node, without jsdom: frameworks import the widget while
// rendering on the server, where there is no DOM.
import { test } from 'node:test';
import assert from 'node:assert/strict';

test('importing the widget without a DOM does not throw', async () => {
  assert.equal(typeof globalThis.document, 'undefined');
  assert.equal(typeof globalThis.HTMLElement, 'undefined');
  const m = await import('../src/botguard.js');
  assert.equal(typeof m.BotGuardWidget, 'function');
  assert.equal(typeof m.init, 'function');
  assert.equal(typeof m.initBotGuard, 'function');
  assert.throws(() => m.init({}), /只能在浏览器/);
});
