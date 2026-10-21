// node --test packages/browser/test/*.test.mjs
// The legacy build (dist/botguard.legacy.js) is what webpack 4 bundles, e.g.
// HBuilderX / uni-app Vue 2 H5 pages. Builds it fresh, runs a verification
// through it, and runs its inlined worker against the shared PoW fixtures.
import { test, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import vm from 'node:vm';

const root = new URL('../', import.meta.url);
execFileSync(process.execPath, [fileURLToPath(new URL('scripts/build-legacy.mjs', root))], { stdio: 'ignore' });
const legacyFile = new URL('dist/botguard.legacy.js', root);
const code = await readFile(legacyFile, 'utf8');
const pkg = JSON.parse(await readFile(new URL('package.json', root), 'utf8'));

// jsdom has no blob URLs: record what the widget hands to the worker.
const blobs = new Map();
URL.createObjectURL = (blob) => {
  const url = `blob:https://site.example/${blobs.size + 1}`;
  blobs.set(url, blob);
  return url;
};
URL.revokeObjectURL = (url) => blobs.delete(url);
const workerSources = [];
const RealBlob = globalThis.Blob;

globalThis.BOTGUARD_TEST_ENTRY = legacyFile.href;
const { setup, teardown, widget, solveAll, workers, flush } = await import('./harness.mjs');

beforeEach(() => {
  setup();
  globalThis.Blob = class extends RealBlob {
    constructor(parts, opts) {
      super(parts, opts);
      workerSources.push(parts.join(''));
    }
  };
});
afterEach(() => {
  globalThis.Blob = RealBlob;
  teardown();
});

test('package.json sends bundlers without "exports" to the legacy build', () => {
  assert.equal(pkg.main, './dist/botguard.legacy.js');
  assert.equal(pkg.module, './dist/botguard.legacy.js');
  assert.equal(pkg.exports['.'], './src/botguard.js');
  assert.ok(pkg.sideEffects.includes('./dist/botguard.legacy.js'));
});

test('legacy build has no syntax webpack 4 cannot parse', () => {
  const body = code.replace(/^(\/\/.*\n)+/, ''); // the banner mentions import.meta
  assert.doesNotMatch(body, /import\.meta/);
  assert.doesNotMatch(body, /\?\?|\?\.[a-zA-Z_$[(]/, 'nullish / optional chaining must be lowered');
  assert.doesNotMatch(body, /^\s+(static\s+)?#[a-zA-Z_]/m, 'private class members must be lowered');
});

test('legacy build verifies with a classic worker started from a blob', async () => {
  const el = widget();
  const token = el.verify();
  await flush();
  assert.ok(workers.length > 0);
  for (const w of workers) {
    assert.match(w.url, /^blob:/);
    assert.equal(w.opts, undefined, 'a blob worker with inlined code is a classic worker');
  }
  await solveAll();
  assert.equal(await token, el.token);
  assert.ok(el.token);
});

test('inlined worker solves the shared PoW fixtures like the Go gateway', async () => {
  const el = widget();
  el.verify().catch(() => {});
  await flush();
  const source = workerSources.find((s) => s.includes('onmessage'));
  assert.ok(source, 'worker source was handed to a Blob');

  const fixtures = JSON.parse(await readFile(new URL('../../api/fixtures/pow-sha256-target-v1.json', root), 'utf8'));
  const c = fixtures.find((f) => f.name.startsWith('solved'));
  const messages = [];
  const self = { postMessage: (m) => messages.push(m) };
  vm.runInNewContext(source, { self, crypto: globalThis.crypto, TextEncoder, Uint8Array, DataView, Promise, Math, String, Number, Error, parseInt, Object, Array, JSON });
  await self.onmessage({
    data: { type: 'solve', algorithm: c.algorithm, challengeId: c.challenge_id, target: c.params.target, indices: [...c.nonces.keys()] },
  });
  const solved = messages.filter((m) => m.type === 'solved').map((m) => m.nonce);
  assert.deepEqual(solved, c.nonces);
});
