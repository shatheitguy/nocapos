// The photo editor's maths: run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  applyMatrix,
  aspectCrop,
  autoLevels,
  clampCrop,
  colorMatrix,
  compose,
  editedName,
  FILTERS,
  histogram,
  IDENTITY,
  isUnchanged,
  NEW_EDIT,
  NO_ADJUST,
  outputSize,
  record,
  redo,
  saveType,
  startHistory,
  straightenScale,
  turnedSize,
  undo,
} from '../src/lib/photoEdit.ts';

const near = (a: number, b: number, eps = 1e-6) => assert.ok(Math.abs(a - b) < eps, `${a} ≉ ${b}`);
const px = (r: number, g: number, b: number) => [r, g, b, 255];

test('no change is the identity', () => {
  assert.deepEqual(colorMatrix(NEW_EDIT), IDENTITY);
  assert.ok(isUnchanged(NEW_EDIT));
  assert.ok(!isUnchanged({ ...NEW_EDIT, turns: 1 }));
  assert.ok(isUnchanged({ ...NEW_EDIT, turns: 4 }));
  assert.ok(!isUnchanged({ ...NEW_EDIT, adjust: { ...NO_ADJUST, warmth: 5 } }));
  const p = px(10, 120, 250);
  applyMatrix(p, IDENTITY);
  assert.deepEqual(p, px(10, 120, 250));
});

test('compose applies the first matrix first', () => {
  const double = [2, 0, 0, 0, 0, 0, 2, 0, 0, 0, 0, 0, 2, 0, 0, 0, 0, 0, 1, 0];
  const plus = [1, 0, 0, 0, 0.1, 0, 1, 0, 0, 0.1, 0, 0, 1, 0, 0.1, 0, 0, 0, 1, 0];
  // (x * 2) + 0.1, not (x + 0.1) * 2
  const m = compose(double, plus);
  near(m[0], 2);
  near(m[4], 0.1);
  const n = compose(plus, double);
  near(n[4], 0.2);
});

test('sliders move pixels the right way', () => {
  const grey = () => px(128, 128, 128);
  const run = (adjust: Partial<typeof NO_ADJUST>) => {
    const p = grey();
    applyMatrix(p, colorMatrix({ ...NEW_EDIT, adjust: { ...NO_ADJUST, ...adjust } }));
    return p;
  };
  assert.ok(run({ brightness: 50 })[0] > 128);
  assert.ok(run({ brightness: -50 })[0] < 128);
  // Contrast pivots on mid-grey.
  const c = run({ contrast: 60 });
  assert.ok(Math.abs(c[0] - 128) <= 1);
  const light = px(200, 200, 200);
  applyMatrix(light, colorMatrix({ ...NEW_EDIT, adjust: { ...NO_ADJUST, contrast: 60 } }));
  assert.ok(light[0] > 200);
  const warm = run({ warmth: 80 });
  assert.ok(warm[0] > warm[2]);
  // Saturation leaves greys grey and pushes colours apart.
  const s = run({ saturation: 100 });
  assert.equal(s[0], s[2]);
  const red = px(200, 100, 100);
  applyMatrix(red, colorMatrix({ ...NEW_EDIT, adjust: { ...NO_ADJUST, saturation: 100 } }));
  assert.ok(red[0] - red[1] > 100);
  const mono = px(200, 100, 50);
  applyMatrix(mono, colorMatrix({ ...NEW_EDIT, adjust: { ...NO_ADJUST, saturation: -100 } }));
  assert.equal(mono[0], mono[1]);
  assert.equal(mono[1], mono[2]);
});

test('filters are matrices that keep alpha', () => {
  for (const [id, m] of Object.entries(FILTERS)) {
    assert.equal(m.length, 20, id);
    assert.deepEqual(m.slice(15), [0, 0, 0, 1, 0], id);
  }
  const p = px(200, 120, 60);
  applyMatrix(p, colorMatrix({ ...NEW_EDIT, filter: 'mono' }));
  assert.equal(p[0], p[2]);
});

test('auto-enhance stretches a dull picture and leaves a full one', () => {
  const dull: number[] = [];
  for (let v = 60; v <= 180; v++) for (let k = 0; k < 10; k++) dull.push(v, v, v, 255);
  const lv = autoLevels(histogram(dull));
  assert.ok(lv && lv.lo >= 60 && lv.lo < 65 && lv.hi <= 180 && lv.hi > 175, JSON.stringify(lv));
  const p = [lv!.lo, lv!.lo, lv!.lo, 255, lv!.hi, lv!.hi, lv!.hi, 255];
  applyMatrix(p, colorMatrix({ ...NEW_EDIT, levels: lv }));
  assert.equal(p[0], 0);
  assert.equal(p[4], 255);
  const full: number[] = [];
  for (let v = 0; v <= 255; v++) full.push(v, v, v, 255);
  assert.equal(autoLevels(histogram(full)), null);
  assert.equal(autoLevels(new Uint32Array(256)), null);
});

test('crop shapes', () => {
  // 1:1 on a 400×200 picture: the middle half.
  const sq = aspectCrop('1:1', 400, 200);
  near(sq.w, 0.5);
  near(sq.h, 1);
  near(sq.x, 0.25);
  // 16:9 on a tall picture turns to 9:16.
  const tall = aspectCrop('16:9', 900, 1600);
  near(tall.w, 1);
  near(tall.h, 1);
  assert.deepEqual(aspectCrop('free', 10, 10), { x: 0, y: 0, w: 1, h: 1 });
  // Clamped inside the picture and kept in ratio.
  const c = clampCrop({ x: 0.9, y: -0.2, w: 0.5, h: 0.9 }, '1:1', 400, 400);
  near(c.w, 0.5);
  near(c.h, 0.5);
  near(c.x, 0.5);
  near(c.y, 0);
  const tiny = clampCrop({ x: 0.5, y: 0.5, w: 0, h: 0 }, 'free', 100, 100);
  near(tiny.w, 0.05);
});

test('straightening and sizes', () => {
  near(straightenScale(400, 300, 0), 1);
  assert.ok(straightenScale(400, 300, 10) > 1);
  near(straightenScale(400, 300, 10), straightenScale(400, 300, -10));
  assert.deepEqual(turnedSize(400, 300, 1), { w: 300, h: 400 });
  assert.deepEqual(turnedSize(400, 300, -2), { w: 400, h: 300 });
  assert.deepEqual(outputSize(4000, 3000, { turns: 1, crop: { x: 0, y: 0, w: 0.5, h: 0.25 } }), { w: 1500, h: 1000 });
});

test('undo and redo', () => {
  let h = startHistory(1);
  h = record(h, 2);
  h = record(h, 3);
  assert.equal(h.present, 3);
  h = undo(h);
  assert.equal(h.present, 2);
  h = undo(undo(h));
  assert.equal(h.present, 1);
  h = redo(h);
  assert.equal(h.present, 2);
  // A new step drops what was undone.
  h = record(h, 9);
  assert.deepEqual(h.future, []);
  assert.equal(redo(h), h);
  assert.equal(record(h, h.present), h);
  let long = startHistory(0);
  for (let i = 1; i <= 80; i++) long = record(long, i, 50);
  assert.equal(long.past.length, 50);
});

test('names and types for saving', () => {
  assert.equal(editedName('beach.JPG'), 'beach (edited).JPG');
  assert.equal(editedName('beach (edited).jpg'), 'beach (edited).jpg');
  assert.equal(editedName('scan.tiff'), 'scan (edited).jpg');
  assert.equal(editedName('noext'), 'noext (edited).jpg');
  assert.equal(saveType('a.PNG'), 'image/png');
  assert.equal(saveType('a.webp'), 'image/webp');
  assert.equal(saveType('a.heic'), 'image/jpeg');
});
