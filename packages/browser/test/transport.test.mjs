// node --test packages/browser/test/*.test.mjs
// The transport option: a uni-app App sends the widget's requests from its
// logic layer, because its WebView has no usable origin.
import { test, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import { flush, gateway, init, setup, solveAll, teardown, widget } from './harness.mjs';

beforeEach(setup);
afterEach(teardown);

// Answers like a uni.request success callback: { status, data }.
function recordingTransport(answer) {
  const sent = [];
  const transport = async ({ url, body }) => {
    sent.push({ url, body: JSON.parse(body) });
    return answer(url, sent.length);
  };
  return { sent, transport };
}

const ok = (data) => ({ status: 200, data: { code: 'OK', data } });

test('requests go through the transport instead of fetch', async () => {
  const { sent, transport } = recordingTransport((url, n) =>
    url.endsWith('/v1/challenges') ? ok(gateway.challenge()) : { status: 200, data: JSON.stringify({ code: 'OK', data: { token: `app${n}`, expires_in: 60 } }) });
  const el = widget();
  el.transport = transport;
  const token = el.verify();
  await flush();
  await solveAll();
  assert.equal(await token, 'app2', 'a string body is parsed as JSON');
  assert.deepEqual(sent.map((s) => s.url), ['https://gw.example/v1/challenges', 'https://gw.example/v1/solutions']);
  assert.equal(sent[0].body.app_key, 'app_test');
  assert.equal(gateway.calls.length, 0, 'fetch is not used');
});

test('gateway errors from the transport keep their code', async () => {
  const { transport } = recordingTransport(() => ({ status: 403, data: { code: 'ORIGIN_NOT_ALLOWED', message: '来源不允许' } }));
  const el = widget();
  el.transport = transport;
  await assert.rejects(el.verify(), (err) => err.code === 'ORIGIN_NOT_ALLOWED' && err.status === 403);
});

test('a transport that throws or returns garbage is a network or HTTP error', async () => {
  const el = widget();
  el.transport = async () => { throw new Error('request:fail'); };
  await assert.rejects(el.verify(), (err) => err.code === 'NETWORK');
  el.reset();
  el.transport = async () => ({ status: 502, data: '<html>bad gateway</html>' });
  await assert.rejects(el.verify(), (err) => err.code === 'HTTP_502');
});

test('init() passes the transport to the widget', () => {
  const transport = async () => ok({});
  const ins = init({ endpoint: 'https://gw.example', appKey: 'app_test', action: 'login', mode: 'popup', transport });
  assert.equal(ins.element.transport, transport);
  ins.destroy();
});
