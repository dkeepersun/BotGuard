// node --test packages/browser/test/*.test.mjs
import { test, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import { challenges, flush, init, setup, solveAll, teardown, tick } from './harness.mjs';

beforeEach(setup);
afterEach(teardown);

const config = { mode: 'popup', endpoint: 'https://gw.example', appKey: 'app_test', action: 'login' };

// A login form whose own submit handler records what would be sent.
function page() {
  document.body.innerHTML = `<form id="f"><input name="user" required value="alice">
    <button id="go" type="submit">登录</button><button id="alt" type="submit" name="via" value="sms">短信登录</button></form>`;
  const form = document.getElementById('f');
  const sent = [];
  const clicks = [];
  form.addEventListener('submit', (e) => {
    e.preventDefault();
    sent.push({ token: new FormData(form).get('botguard_token'), submitter: e.submitter?.id ?? null });
  });
  for (const id of ['go', 'alt']) document.getElementById(id).addEventListener('click', () => clicks.push(id));
  return { form, sent, clicks, go: document.getElementById('go'), alt: document.getElementById('alt') };
}

test('double-clicking the bound button submits once, with the token', async () => {
  const { go, sent, clicks } = page();
  const bg = init({ ...config, bindTo: go });
  go.click();
  await flush();
  go.click();
  await flush();
  assert.equal(challenges().length, 1, 'one verification');
  assert.deepEqual(sent, [], 'nothing goes out unverified');
  await solveAll();
  assert.equal(sent.length, 1);
  assert.equal(sent[0].token, 'tok2');
  assert.equal(sent[0].submitter, 'go');
  assert.deepEqual(clicks, ['go', 'go'], "the page's click handlers still run");
  assert.equal(bg.token, 'tok2', 'still readable right after the submission');
  tick(0);
  assert.equal(bg.token, '', 'then cleared: tokens are single-use');
});

test('Enter in a field and other submit buttons are guarded too', async () => {
  const { form, alt, sent } = page();
  init({ ...config, bindTo: '#go' });
  form.requestSubmit(); // what pressing Enter in a field does
  await flush();
  alt.click();
  await flush();
  assert.equal(challenges().length, 1);
  await solveAll();
  assert.equal(sent.length, 1);
  assert.equal(sent[0].token, 'tok2');
  assert.equal(sent[0].submitter, 'alt', 'replayed with the latest submitter');
});

test('a submission that fails validation keeps the token', async () => {
  const { form, go, sent } = page();
  const bg = init({ ...config, bindTo: go });
  bg.verify();
  await flush();
  await solveAll();
  assert.equal(bg.token, 'tok2');
  form.elements.user.value = '';
  go.click(); // the browser refuses: required field empty, no submit event
  tick(0);
  assert.equal(sent.length, 0);
  assert.equal(bg.token, 'tok2');
  form.elements.user.value = 'alice';
  go.click();
  assert.deepEqual(sent, [{ token: 'tok2', submitter: 'go' }]);
  tick(0);
  assert.equal(bg.token, '');
});

test('without requestSubmit (iOS < 16) the submitter is clicked instead', async () => {
  const { form, go, sent } = page();
  form.requestSubmit = undefined;
  init({ ...config, bindTo: go });
  go.click();
  await flush();
  await solveAll();
  assert.deepEqual(sent, [{ token: 'tok2', submitter: 'go' }]);
});

test('a bound button outside a form is guarded at its click and replayed once', async () => {
  document.body.innerHTML = '<button id="send" type="button">发送验证码</button>';
  const send = document.getElementById('send');
  const seen = [];
  let bg;
  send.addEventListener('click', () => seen.push(bg.token));
  bg = init({ ...config, bindTo: send });
  send.click();
  send.click();
  await flush();
  assert.deepEqual(seen, []);
  await solveAll();
  assert.deepEqual(seen, ['tok2']);
  tick(0);
  assert.equal(bg.token, '');
});

test('closing the popup cancels without submitting; the next click verifies again', async () => {
  const { go, sent } = page();
  const bg = init({ ...config, bindTo: go });
  go.click();
  await flush();
  bg.element.shadowRoot.querySelector('.close').click();
  await solveAll();
  assert.equal(sent.length, 0);
  go.click();
  await flush();
  await solveAll();
  assert.equal(sent.length, 1);
  assert.equal(challenges().length, 2);
});

test('destroy() removes the guard and the widget', async () => {
  const { go, sent } = page();
  const bg = init({ ...config, bindTo: go });
  bg.destroy();
  assert.equal(bg.element.isConnected, false);
  go.click();
  assert.equal(sent.length, 1);
  assert.equal(sent[0].token, null);
});
