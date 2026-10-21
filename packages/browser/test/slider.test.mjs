// node --test packages/browser/test/*.test.mjs
import { test, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import { $, flush, gateway, layout, setup, solveAll, solutions, state, teardown, widget } from './harness.mjs';

beforeEach(setup);
afterEach(teardown);

// Puzzle is 320 px wide and so is the stage: one client pixel = one puzzle pixel.
async function slider() {
  gateway.kind = 'slider';
  const el = widget();
  el.verify().catch(() => {});
  await flush();
  assert.equal(state(el), 'puzzle');
  const handle = $(el, '.handle');
  const pointer = (type, { id = 1, x = 0, y = 0, t = 0, button = 0 } = {}) => {
    const e = new PointerEvent(type, { pointerId: id, clientX: x, clientY: y, pointerType: 'mouse', button, isPrimary: id === 1 || id === 2, bubbles: true, cancelable: true });
    Object.defineProperty(e, 'timeStamp', { value: t });
    handle.dispatchEvent(e);
  };
  const key = (k, t) => {
    const e = new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true });
    Object.defineProperty(e, 'timeStamp', { value: t });
    handle.dispatchEvent(e);
  };
  return { el, handle, pointer, key };
}

// An eased human-ish drag to `to` puzzle pixels, one event per `step` ms.
function drag(pointer, { to, ms, step = 1, id = 1 }) {
  pointer('pointerdown', { id, x: 0, y: 0, t: 0 });
  let x = 0;
  for (let t = step; t <= ms; t += step) {
    const p = t / ms;
    x = to * (p < 0.5 ? 2 * p * p : 1 - (-2 * p + 2) ** 2 / 2);
    pointer('pointermove', { id, x, y: Math.sin(t / 37) * 3, t });
  }
  pointer('pointerup', { id, x: to, y: 1, t: ms + 4 });
}

function checkTrajectory(answer) {
  const tr = answer.trajectory;
  assert.ok(tr.length >= 8 && tr.length <= 400, `points: ${tr.length}`);
  assert.deepEqual(tr[0], [0, 0, 0]);
  assert.equal(tr.at(-1)[1], answer.x, 'ends where the piece was released');
  const total = Math.abs(tr.at(-1)[1] - tr[0][1]);
  for (let i = 1; i < tr.length; i++) {
    assert.ok(tr[i][0] >= tr[i - 1][0], 'time never goes back');
    assert.ok(Math.abs(tr[i][1] - tr[i - 1][1]) <= total * 0.5, 'no teleport');
  }
}

test('a 1000 Hz drag keeps its release point within 400 samples', async () => {
  const { pointer } = await slider();
  drag(pointer, { to: 180, ms: 1500 });
  await solveAll();
  const { answer } = solutions()[0].body;
  assert.equal(answer.x, 180);
  checkTrajectory(answer);
  const gaps = answer.trajectory.slice(1, -1).map((p, i) => p[0] - answer.trajectory[i][0]);
  assert.ok(gaps.every((g) => g >= 8), 'samples are at least 8 ms apart');
});

test('a long slow drag is thinned instead of cut off', async () => {
  const { pointer } = await slider();
  drag(pointer, { to: 200, ms: 8000 });
  await solveAll();
  const { answer } = solutions()[0].body;
  assert.equal(answer.x, 200);
  checkTrajectory(answer);
  assert.ok(answer.trajectory.at(-2)[0] > 7000, 'the end of the drag is still there');
});

test('pointercancel puts the piece back instead of submitting', async () => {
  const { el, handle, pointer } = await slider();
  pointer('pointerdown', { x: 0, t: 0 });
  pointer('pointermove', { x: 50, t: 100 });
  pointer('pointermove', { x: 100, t: 200 });
  pointer('pointercancel', { x: 100, t: 210 });
  await flush();
  assert.equal(solutions().length, 0);
  assert.equal(handle.getAttribute('aria-valuenow'), '0');
  assert.match($(el, '.msg').textContent, /重新拖动/);
  // A fresh drag then goes through.
  drag(pointer, { to: 150, ms: 600, step: 10, id: 2 });
  await solveAll();
  assert.equal(solutions().length, 1);
  assert.equal(solutions()[0].body.answer.x, 150);
});

test('events from a second pointer do not move the piece', async () => {
  const { pointer } = await slider();
  pointer('pointerdown', { id: 1, x: 0, t: 0 });
  pointer('pointermove', { id: 1, x: 40, t: 100 });
  pointer('pointermove', { id: 7, x: 300, t: 120 });
  pointer('pointerup', { id: 7, x: 300, t: 130 });
  pointer('pointermove', { id: 1, x: 60, t: 300 });
  pointer('pointerup', { id: 1, x: 60, t: 400 });
  await solveAll();
  assert.equal(solutions()[0].body.answer.x, 60);
});

test('dragging with the mouse still works after using the arrow keys', async () => {
  const { pointer, key } = await slider();
  for (let i = 0; i < 5; i++) key('ArrowRight', i * 50);
  drag(pointer, { to: 120, ms: 700, step: 10 });
  await solveAll();
  const body = solutions()[0].body;
  assert.equal(body.answer.x, 120);
  assert.equal(body.signals.pointer_type, 'mouse');
  checkTrajectory(body.answer);
});

test('the keyboard path submits a trajectory ending at the piece', async () => {
  const { key } = await slider();
  for (let i = 0; i < 20; i++) key('ArrowRight', i * 40);
  key('Enter', 820);
  await solveAll();
  const { answer, signals } = solutions()[0].body;
  assert.equal(answer.x, 40);
  assert.equal(signals.pointer_type, 'keyboard');
  assert.equal(answer.trajectory.at(-1)[1], 40);
});

test('a drag on a stage with no width is ignored, and the scale is measured at each grab', async () => {
  layout.stageWidth = 0;
  const { pointer } = await slider();
  pointer('pointerdown', { x: 0, t: 0 });
  pointer('pointermove', { x: 50, t: 100 });
  pointer('pointerup', { x: 50, t: 400 });
  await solveAll();
  assert.equal(solutions().length, 0, 'nothing (and no NaN) submitted');
  layout.stageWidth = 160; // half size now: one client pixel = two puzzle pixels
  drag(pointer, { to: 60, ms: 600, step: 10 });
  await solveAll();
  const { answer } = solutions()[0].body;
  assert.equal(answer.x, 120);
  assert.ok(answer.trajectory.every((p) => p.every(Number.isFinite)));
});

test('rotate turns the disc with the drag and answers with the drag distance', async () => {
  gateway.kind = 'rotate';
  const el = widget();
  el.verify().catch(() => {});
  await flush();
  assert.equal(state(el), 'puzzle');
  const stage = $(el, '.stage');
  const disc = $(el, '.disc');
  const handle = $(el, '.handle');
  assert.equal(stage.dataset.kind, 'rotate');
  assert.equal(disc.hidden, false);
  assert.equal($(el, '.piece').hidden, true);
  assert.equal(disc.style.width, '136px');
  assert.equal(disc.style.left, '92px');
  assert.equal(handle.style.width, '40px', 'the handle covers what the track does not');
  assert.equal(handle.getAttribute('aria-valuemax'), '280');
  const pointer = (type, x, t) => {
    const e = new PointerEvent(type, { pointerId: 1, clientX: x, clientY: 0, pointerType: 'mouse', button: 0, isPrimary: true, bubbles: true, cancelable: true });
    Object.defineProperty(e, 'timeStamp', { value: t });
    handle.dispatchEvent(e);
  };
  pointer('pointerdown', 0, 0);
  for (let t = 10; t <= 600; t += 10) pointer('pointermove', 140 * (t / 600), t);
  assert.equal(disc.style.transform, 'rotate(180.00deg)', '140 of 280 px is half a turn');
  assert.equal(handle.getAttribute('aria-valuetext'), '180 度');
  pointer('pointerup', 140, 610);
  await solveAll();
  const { answer } = solutions()[0].body;
  assert.equal(answer.x, 140);
  checkTrajectory(answer);
  // The next slider puzzle in the same widget is a jigsaw again.
  gateway.kind = 'slider';
  el.reset();
  el.verify().catch(() => {});
  await flush();
  assert.equal(stage.dataset.kind, 'slider');
  assert.equal(disc.hidden, true);
  assert.equal(handle.hasAttribute('aria-valuetext'), false);
});
