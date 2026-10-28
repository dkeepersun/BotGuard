// node --test packages/browser/test/*.test.mjs
import { test, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import { $, flush, gateway, setup, solveAll, solutions, state, teardown, tick, widget } from './harness.mjs';

beforeEach(setup);
afterEach(teardown);

const pointer = (target, type, x, y, t, id = 1) => {
  const e = new PointerEvent(type, { pointerId: id, clientX: x, clientY: y, pointerType: 'touch', button: 0, isPrimary: true, bubbles: true, cancelable: true });
  Object.defineProperty(e, 'timeStamp', { value: t });
  target.dispatchEvent(e);
};

test('jigsaw: dropping one tile on another swaps them and answers with the pair and the path', async () => {
  gateway.kind = 'jigsaw';
  const el = widget();
  el.verify().catch(() => {});
  await flush();
  assert.equal(state(el), 'puzzle');
  assert.equal($(el, '.jigsaw-ui').hidden, false);
  assert.equal($(el, '.slider-ui').hidden, true);
  const stage = $(el, '.jstage');
  const tiles = stage.querySelectorAll('.tile');
  assert.equal(tiles.length, 8);
  const before1 = tiles[1].style.backgroundPosition, before6 = tiles[6].style.backgroundPosition;
  // Tile 1 is (120, 50); tile 6 is (200, 150). Dropping on itself does nothing.
  pointer(stage, 'pointerdown', 120, 50, 0);
  pointer(stage, 'pointerup', 122, 52, 300);
  assert.match($(el, '.msg').textContent, /另一块/);
  assert.equal(solutions().length, 0);
  pointer(stage, 'pointerdown', 120, 50, 1000);
  for (let i = 1; i <= 30; i++) {
    const p = i / 30, e = p * p * (3 - 2 * p);
    pointer(stage, 'pointermove', 120 + 80 * e + Math.sin(i) * 2, 50 + 100 * e, 1000 + i * 14);
  }
  assert.ok(tiles[1].classList.contains('lifted'));
  assert.ok(tiles[6].classList.contains('over'), 'the drop target is highlighted');
  pointer(stage, 'pointerup', 200, 150, 1440);
  assert.equal(tiles[1].style.backgroundPosition, before6, 'tiles swap on screen');
  assert.equal(tiles[6].style.backgroundPosition, before1);
  await solveAll();
  const { answer, signals } = solutions()[0].body;
  assert.deepEqual(answer.swap, [1, 6]);
  assert.ok(answer.trajectory.length >= 8);
  assert.deepEqual(answer.trajectory[0], [0, 120, 50]);
  assert.deepEqual(answer.trajectory.at(-1).slice(1), [200, 150]);
  assert.equal(signals.pointer_type, 'touch');
  assert.equal(state(el), 'verified');
});

test('click_icon shows the icon strip; other click kinds do not', async () => {
  gateway.kind = 'click_icon';
  const el = widget();
  el.verify().catch(() => {});
  await flush();
  const img = $(el, '.prompt-img');
  assert.equal(img.hidden, false);
  assert.equal(img.getAttribute('src'), 'data:image/png;base64,AA==');
  assert.equal($(el, '.click-ui').hidden, false);
  assert.equal($(el, '.jigsaw-ui').hidden, true);
  const stage = $(el, '.cstage');
  pointer(stage, 'pointerdown', 60, 50, 900);
  pointer(stage, 'pointerdown', 200, 120, 1800);
  tick(300); // the last mark shows briefly before submitting
  await solveAll();
  assert.equal(solutions()[0].body.answer.clicks.length, 2);

  gateway.kind = 'spatial';
  const original = gateway.challenge;
  gateway.challenge = () => ({ ...original(), click: { width: 320, height: 200, image: 'data:,', prompt: '依次点击：①…', targets: 2 } });
  el.reset();
  el.verify().catch(() => {});
  await flush();
  assert.equal(img.hidden, true);
  assert.match($(el, '.prompt').textContent, /依次点击/);

  gateway.kind = 'clock';
  gateway.challenge = () => ({ ...original(), click: { width: 320, height: 200, image: 'data:,', prompt: '请依次点击：显示 3:40 的时钟、显示 8:15 的时钟', targets: 2 } });
  el.reset();
  el.verify().catch(() => {});
  await flush();
  assert.equal($(el, '.click-ui').hidden, false);
  assert.match($(el, '.prompt').textContent, /3:40/);
  gateway.challenge = original;
});

test('grid kinds: clicks toggle cells, Confirm submits every toggle with its moves', async () => {
  gateway.kind = 'grid_count';
  const el = widget();
  el.verify().catch(() => {});
  await flush();
  assert.equal(state(el), 'puzzle');
  assert.equal($(el, '.click-ui').hidden, false);
  assert.equal($(el, '.cells').hidden, false);
  assert.equal($(el, '.prompt-img').hidden, true);
  assert.match($(el, '.prompt').textContent, /三角形/);
  const stage = $(el, '.cstage');
  const cells = $(el, '.cells').querySelectorAll('.cell');
  const confirm = $(el, '.confirm');
  assert.equal(cells.length, 9);
  assert.equal(confirm.disabled, true, 'nothing selected yet');
  pointer(stage, 'pointermove', 40, 30, 500);
  pointer(stage, 'pointerdown', 50, 30, 900); // cell 0
  pointer(stage, 'pointerdown', 160, 100, 1700); // cell 4
  pointer(stage, 'pointerdown', 165, 105, 2300); // cell 4 again: unselected
  pointer(stage, 'pointerdown', 270, 170, 3100); // cell 8
  assert.ok(cells[0].classList.contains('on') && cells[8].classList.contains('on'));
  assert.ok(!cells[4].classList.contains('on'));
  assert.equal(solutions().length, 0, 'nothing is sent before Confirm');
  confirm.click();
  await solveAll();
  const { answer } = solutions()[0].body;
  assert.equal(answer.clicks.length, 4);
  assert.deepEqual(answer.clicks.map((c) => c.slice(1)), [[50, 30], [160, 100], [165, 105], [270, 170]]);
  assert.deepEqual(answer.moves_between, [1, 0, 0, 0]);
  assert.equal(state(el), 'verified');

  // Mirror shows the reference picture; plain click kinds hide the grid again.
  gateway.kind = 'mirror';
  const original = gateway.challenge;
  gateway.challenge = () => ({ ...original(), grid: { width: 320, height: 200, image: 'data:,', prompt: '请选出所有与示例互为镜像的图形', prompt_image: 'data:image/png;base64,AA==', rows: 2, cols: 4 } });
  el.reset();
  el.verify().catch(() => {});
  await flush();
  assert.equal($(el, '.prompt-img').hidden, false);
  assert.ok($(el, '.prompt-img').classList.contains('ref'));
  assert.equal($(el, '.cells').querySelectorAll('.cell').length, 8);
  gateway.challenge = original;
  gateway.kind = 'click_text';
  el.reset();
  el.verify().catch(() => {});
  await flush();
  assert.equal($(el, '.cells').hidden, true);
  assert.equal($(el, '.confirm').hidden, true);
});
