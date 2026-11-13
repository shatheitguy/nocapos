// Justified photo rows: run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { clampRatio, justify } from '../src/lib/justify.ts';

const near = (a: number, b: number) => Math.abs(a - b) < 0.01;

test('every full row fills the width exactly', () => {
  const ratios = [1.5, 0.75, 1, 1.33, 1.78, 0.56, 1, 1.5, 1.5, 0.66, 1.2, 2];
  const rows = justify(ratios, 1000, 200, 4);
  assert.ok(rows.length > 1);
  for (const r of rows.slice(0, -1)) {
    const last = r.widths.length - 1;
    assert.ok(near(r.lefts[last] + r.widths[last], 1000), `row ${r.start}-${r.end} ends at ${r.lefts[last] + r.widths[last]}`);
  }
  // Every item is placed once, in order.
  assert.equal(rows[0].start, 0);
  for (let i = 1; i < rows.length; i++) assert.equal(rows[i].start, rows[i - 1].end);
  assert.equal(rows[rows.length - 1].end, ratios.length);
});

test('rows stay near the target height', () => {
  const ratios = Array.from({ length: 200 }, (_, i) => [1.5, 0.75, 1, 1.78][i % 4]);
  for (const r of justify(ratios, 1200, 180, 3).slice(0, -1)) assert.ok(r.height > 110 && r.height < 270, `height ${r.height}`);
});

test('the last row is not stretched', () => {
  const rows = justify([1, 1], 1000, 200, 4);
  assert.equal(rows.length, 1);
  assert.equal(rows[0].height, 200);
  assert.equal(rows[0].widths[0], 200);
});

test('a very wide picture gets a row of its own', () => {
  const rows = justify([2.6, 1, 1], 300, 200, 4);
  assert.equal(rows[0].end, 1);
});

test('odd shapes are clamped and bad ones count as square', () => {
  assert.equal(clampRatio(10), 2.6);
  assert.equal(clampRatio(0.1), 0.5);
  assert.equal(clampRatio(NaN), 1);
  assert.equal(clampRatio(0), 1);
  assert.deepEqual(justify([], 500, 100, 2), []);
  assert.deepEqual(justify([1], 0, 100, 2), []);
});
