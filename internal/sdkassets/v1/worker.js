// Module worker: solves the puzzle indices it is given and posts each result.
import { solvePuzzle } from './solver.js';

let stopped = false;

self.onmessage = async (event) => {
  const msg = event.data;
  if (msg.type === 'stop') {
    stopped = true;
    return;
  }
  if (msg.type !== 'solve') return;
  try {
    for (const index of msg.indices) {
      const result = await solvePuzzle(msg.algorithm, msg.challengeId, index, msg.target, {
        shouldStop: () => stopped,
        onAttempts: (n) => n > 0 && self.postMessage({ type: 'progress', attempts: n }),
      });
      if (result === null) return;
      self.postMessage({ type: 'solved', index, nonce: result.nonce });
    }
  } catch (err) {
    self.postMessage({ type: 'error', message: String(err && err.message || err) });
  }
};
