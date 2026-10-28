// node --test packages/browser/test/*.test.mjs
import { test, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import { $, challenges, flush, gateway, init, setup, state, teardown, widget } from './harness.mjs';

const { MESSAGES, language } = await import(globalThis.BOTGUARD_TEST_ENTRY || '../src/botguard.js');

beforeEach(setup);
afterEach(teardown);

test('language() maps tags to the supported languages', () => {
  for (const [tag, want] of [['', 'zh-CN'], ['zh', 'zh-CN'], ['zh-Hans-CN', 'zh-CN'], ['zh-TW', 'zh-TW'], ['zh-HK', 'zh-TW'],
    ['en-US', 'en'], ['ja', 'en']]) {
    assert.equal(language(tag), want, tag);
  }
});

test('every language has every key', () => {
  for (const lang of ['zh-TW', 'en']) {
    for (const key of Object.keys(MESSAGES['zh-CN'])) assert.ok(MESSAGES[lang][key], `${lang} lacks ${key}`);
  }
});

test('lang sets the texts, follows changes and travels with the challenge request', async () => {
  const el = widget({ lang: 'en' });
  assert.equal($(el, '.box .title').textContent, MESSAGES.en.idle);
  assert.equal($(el, '.refresh').textContent, 'New puzzle');
  assert.equal($(el, '.panel').getAttribute('aria-label'), 'Human verification');
  el.setAttribute('lang', 'zh-TW');
  assert.equal($(el, '.box .title').textContent, MESSAGES['zh-TW'].idle);
  el.start();
  await flush();
  assert.equal(challenges()[0].body.lang, 'zh-TW');
  // No lang: Simplified Chinese, as before.
  const plain = widget();
  assert.equal($(plain, '.box .title').textContent, '点击进行人机验证');
});

test('texts replace built-in texts, for every language or one', () => {
  const el = widget({ lang: 'en' });
  el.texts = { idle: 'Prove you are human', en: { refresh: 'Another one' }, 'zh-CN': { refresh: '再来一张' } };
  assert.equal($(el, '.box .title').textContent, 'Prove you are human');
  assert.equal($(el, '.refresh').textContent, 'Another one');
  el.setAttribute('lang', 'zh-CN');
  assert.equal($(el, '.refresh').textContent, '再来一张');
});

test('known error codes are translated, unknown ones keep the server message', async () => {
  gateway.challenge = () => ({ error: 'RATE_LIMITED', status: 429, message: '请求过于频繁，请稍后再试' });
  const el = widget({ lang: 'en' });
  await assert.rejects(el.verify(), { code: 'RATE_LIMITED', message: MESSAGES.en.RATE_LIMITED });
  assert.equal(state(el), 'error');
  assert.match($(el, '.box .title').textContent, /^Too many requests/);
  gateway.challenge = () => ({ error: 'SOMETHING_NEW', status: 400, message: '新的错误' });
  const other = widget({ lang: 'en' });
  await assert.rejects(other.verify(), { code: 'SOMETHING_NEW', message: '新的错误' });
});

test('init() passes lang, theme, radius and texts', () => {
  const host = document.createElement('div');
  document.body.appendChild(host);
  const bg = init({ element: host, endpoint: 'https://gw.example', appKey: 'app_test', action: 'login',
    lang: 'en', theme: 'dark', radius: 12, texts: { idle: 'Hi' } });
  const el = bg.element;
  assert.equal(el.getAttribute('lang'), 'en');
  assert.equal(el.getAttribute('theme'), 'dark');
  assert.equal(el.style.getPropertyValue('--botguard-radius'), '12px');
  assert.equal($(el, '.box .title').textContent, 'Hi');
});

// The appearance set in the console is cached per gateway and app for the
// page's lifetime, so each test below uses its own app key.
test('the console appearance applies where the page set nothing', async () => {
  gateway.ui = () => ({ lang: 'en', theme: 'dark', accent: '#ff6a00', radius: 4, texts: { idle: 'Hello there' } });
  const el = widget({ 'app-key': 'app_ui1' });
  await flush();
  assert.equal($(el, '.box .title').textContent, 'Hello there');
  assert.equal($(el, '.refresh').textContent, 'New puzzle');
  assert.equal(el.getAttribute('theme'), 'dark');
  assert.equal(el.style.getPropertyValue('--botguard-accent'), '#ff6a00');
  assert.equal(el.style.getPropertyValue('--botguard-radius'), '4px');
  el.start();
  await flush();
  assert.equal(challenges()[0].body.lang, 'en');
  // A second widget of the same app reuses the first fetch.
  widget({ 'app-key': 'app_ui1' });
  await flush();
  assert.equal(gateway.uiCalls.length, 1);
});

test("the page's own settings win over the console's", async () => {
  gateway.ui = () => ({ lang: 'en', theme: 'dark', accent: '#ff6a00', texts: { idle: 'Hello there' } });
  const el = widget({ 'app-key': 'app_ui2', lang: 'zh-TW', theme: 'light', accent: '#00aa00' });
  el.texts = { idle: '頁面自己的文案' };
  await flush();
  assert.equal($(el, '.box .title').textContent, '頁面自己的文案');
  assert.equal($(el, '.refresh').textContent, MESSAGES['zh-TW'].refresh);
  assert.equal(el.getAttribute('theme'), 'light');
  assert.equal(el.style.getPropertyValue('--botguard-accent'), '#00aa00');
});

test('remote-ui="off" and a failed fetch leave the defaults', async () => {
  const off = widget({ 'app-key': 'app_ui3', 'remote-ui': 'off' });
  await flush();
  assert.equal(gateway.uiCalls.length, 0);
  assert.equal($(off, '.box .title').textContent, MESSAGES['zh-CN'].idle);
  gateway.ui = () => ({ error: 'ORIGIN_NOT_ALLOWED' });
  const failed = widget({ 'app-key': 'app_ui4' });
  await flush();
  assert.equal(gateway.uiCalls.length, 1);
  assert.equal($(failed, '.box .title').textContent, MESSAGES['zh-CN'].idle);
  assert.equal(failed.getAttribute('theme'), null);
});

test("the console's texts show only in the console's language", async () => {
  gateway.ui = () => ({ texts: { idle: '点击完成安全验证' } });
  const zh = widget({ 'app-key': 'app_ui5' });
  const en = widget({ 'app-key': 'app_ui5', lang: 'en' });
  await flush();
  assert.equal($(zh, '.box .title').textContent, '点击完成安全验证');
  assert.equal($(en, '.box .title').textContent, MESSAGES.en.idle);
});
