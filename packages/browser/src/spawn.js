// Starts the proof-of-work Worker. Kept in its own module so the legacy build
// (scripts/build-legacy.mjs, for webpack 4 and other bundlers without
// import.meta) can swap in a copy that runs the worker from an inlined source.

// Same origin (or rewritten by a bundler, which turns import.meta.url into a
// file: URL or keeps it on the page's origin): the worker can load directly.
function workerLoadsDirectly() {
  const here = new URL(import.meta.url);
  return !/^https?:$/.test(here.protocol) || here.origin === globalThis.location?.origin;
}

// Returns { worker, revoke } or null when workers are not allowed (e.g. CSP
// without blob:), in which case the caller solves on the main thread.
export function spawnWorker() {
  try {
    if (workerLoadsDirectly()) {
      // Written out literally so bundlers (Vite, webpack 5, Parcel) see the
      // worker and emit it with its imports.
      return { worker: new Worker(new URL('./worker.js', import.meta.url), { type: 'module' }), revoke() {} };
    }
    // Browsers refuse cross-origin worker URLs, so when this script is
    // served by the gateway (like Yidun's hosted load.min.js) a same-origin
    // blob worker imports the CORS-enabled worker module instead. The name
    // is a variable so bundlers leave this branch alone.
    const name = './worker.js';
    const href = new URL(name, import.meta.url).href;
    const url = URL.createObjectURL(new Blob([`import ${JSON.stringify(href)};`], { type: 'text/javascript' }));
    try {
      return { worker: new Worker(url, { type: 'module' }), revoke: () => URL.revokeObjectURL(url) };
    } catch (err) {
      URL.revokeObjectURL(url);
      throw err;
    }
  } catch {
    return null;
  }
}
