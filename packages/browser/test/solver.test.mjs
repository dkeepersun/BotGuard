// node --test packages/browser/test
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { check, hexToBytes, solvePuzzle } from '../src/solver.js';

const fixtures = JSON.parse(await readFile(new URL('../../../api/fixtures/pow-sha256-target-v1.json', import.meta.url), 'utf8'));

// The browser must accept exactly what the Go gateway accepts.
async function verify(c) {
  if (c.algorithm !== 'sha256-target-v1') return false;
  if (c.nonces.length !== c.params.puzzles) return false;
  const target = hexToBytes(c.params.target);
  for (let i = 0; i < c.nonces.length; i++) {
    const n = c.nonces[i];
    if (!/^(0|[1-9][0-9]{0,19})$/.test(n)) return false;
    if (!(await check(c.algorithm, c.challenge_id, i, n, target))) return false;
  }
  return true;
}

for (const c of fixtures) {
  test(`fixture: ${c.name}`, async () => {
    assert.equal(await verify(c), c.valid);
  });
}

test('solver finds the same smallest nonces as the Go reference', async () => {
  const c = fixtures.find((f) => f.name.startsWith('solved'));
  const nonces = [];
  for (let i = 0; i < c.params.puzzles; i++) {
    nonces.push((await solvePuzzle(c.algorithm, c.challenge_id, i, c.params.target)).nonce);
  }
  assert.deepEqual(nonces, c.nonces);
});

test('solver can be cancelled', async () => {
  const hard = '0000000000000000000000000000000000000000000000000000000000000001';
  assert.equal(await solvePuzzle('sha256-target-v1', 'x', 0, hard, { shouldStop: () => true }), null);
});
