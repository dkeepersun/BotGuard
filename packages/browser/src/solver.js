// PoW solver for sha256-target-v1 (api/contract-v1.md). Pure functions so the
// same code runs in the Web Worker and in Node tests against shared fixtures.

export const ALGORITHM = 'sha256-target-v1';

const encoder = new TextEncoder();

export function hexToBytes(hex) {
  if (!/^[0-9a-f]{64}$/.test(hex)) throw new Error('target must be 64 lowercase hex chars');
  const out = new Uint8Array(32);
  for (let i = 0; i < 32; i++) out[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
  return out;
}

export function proofInput(algorithm, challengeId, index, nonce) {
  return encoder.encode(`BG1\n${algorithm}\n${challengeId}\n${index}\n${nonce}`);
}

// Big-endian comparison: digest < target.
export function below(digest, target) {
  for (let i = 0; i < 32; i++) {
    if (digest[i] !== target[i]) return digest[i] < target[i];
  }
  return false;
}

async function sha256(bytes) {
  return new Uint8Array(await crypto.subtle.digest('SHA-256', bytes));
}

export async function check(algorithm, challengeId, index, nonce, target) {
  return below(await sha256(proofInput(algorithm, challengeId, index, nonce)), target);
}

// Finds the smallest valid nonce for one puzzle. shouldStop is polled so a
// reset or timeout can cancel; onAttempts reports progress in batches.
export async function solvePuzzle(algorithm, challengeId, index, targetHex, { shouldStop = () => false, onAttempts = () => {} } = {}) {
  if (algorithm !== ALGORITHM) throw new Error(`unsupported algorithm ${algorithm}`);
  const target = hexToBytes(targetHex);
  let reported = 0;
  for (let n = 0; ; n++) {
    if ((n & 255) === 0) {
      if (shouldStop()) return null;
      onAttempts(n - reported);
      reported = n;
    }
    if (await check(algorithm, challengeId, index, String(n), target)) {
      onAttempts(n + 1 - reported);
      return { nonce: String(n), attempts: n + 1 };
    }
  }
}
