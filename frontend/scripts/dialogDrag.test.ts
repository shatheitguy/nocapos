// Moving dialogs by their title row: run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { clampOffset } from '../src/components/dialogDrag.ts';

const area = { left: 0, top: 50, right: 1000, bottom: 700 };
const home = { left: 300, top: 200, right: 700, bottom: 500 };

test('a dialog moves freely inside its window', () => {
  assert.deepEqual(clampOffset(home, area, 120, -40), { x: 120, y: -40 });
});

test('a dialog stops at the window edges', () => {
  assert.deepEqual(clampOffset(home, area, -900, -900), { x: -300, y: -150 });
  assert.deepEqual(clampOffset(home, area, 900, 900), { x: 300, y: 200 });
});

test('a dialog bigger than its window stays centred on that axis', () => {
  const tall = { left: 300, top: 0, right: 700, bottom: 800 };
  assert.equal(clampOffset(tall, area, 0, 300).y, -25);
});
