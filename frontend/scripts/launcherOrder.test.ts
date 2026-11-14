// Roster icon order: run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { arrange, cellAt, moveBefore, preview, replaceKey, slotAt, toSave } from '../src/lib/launcherOrder.ts';

test('arrange keeps the saved order, adds new apps at the end and drops removed ones', () => {
  assert.deepEqual(arrange(['c', 'a', 'gone', 'b'], ['a', 'b', 'c', 'new']), ['c', 'a', 'b', 'new']);
  assert.deepEqual(arrange([], ['a', 'b']), ['a', 'b']);
  assert.deepEqual(arrange(['a', 'a', 'b'], ['b', 'a']), ['a', 'b']);
});

test('moveBefore moves in front of a key, or to the end', () => {
  assert.deepEqual(moveBefore(['a', 'b', 'c', 'd'], 'd', 'b'), ['a', 'd', 'b', 'c']);
  assert.deepEqual(moveBefore(['a', 'b', 'c', 'd'], 'a', 'd'), ['b', 'c', 'a', 'd']);
  assert.deepEqual(moveBefore(['a', 'b', 'c'], 'a', null), ['b', 'c', 'a']);
  assert.deepEqual(moveBefore(['a', 'b'], 'a', 'a'), ['b', 'a']);
  // An app coming out of a folder joins the list.
  assert.deepEqual(moveBefore(['a', 'b'], 'x', 'b'), ['a', 'x', 'b']);
  assert.deepEqual(moveBefore(['a', 'b'], 'x', 'missing'), ['a', 'b', 'x']);
});

test('replaceKey puts the replacement where the key was', () => {
  assert.deepEqual(replaceKey(['a', 'folder:f', 'b'], 'folder:f', ['x', 'y']), ['a', 'x', 'y', 'b']);
  // A new folder takes the target's place; the dragged app was elsewhere.
  assert.deepEqual(replaceKey(['a', 'b', 'c'], 'b', ['folder:n']), ['a', 'folder:n', 'c']);
  assert.deepEqual(replaceKey(['a', 'x', 'folder:f'], 'folder:f', ['x']), ['a', 'x']);
  assert.deepEqual(replaceKey(['a'], 'folder:f', ['x']), ['a', 'x']);
  assert.deepEqual(replaceKey(['a', 'folder:f'], 'folder:f', []), ['a']);
});

test('slotAt: left half lands before the icon, right half after it', () => {
  const shown = ['a', 'b', 'D', 'c'];
  assert.equal(slotAt(shown, 'D', 0, false), 'a');
  assert.equal(slotAt(shown, 'D', 0, true), 'b');
  assert.equal(slotAt(shown, 'D', 1, true), 'c'); // skips the dragged icon's own spot
  assert.equal(slotAt(shown, 'D', 2, false), undefined); // over its own spot: no change
  assert.equal(slotAt(shown, 'D', 3, true), null); // after the last icon
  assert.equal(slotAt(shown, 'D', 9, false), null); // an empty cell at the end
  assert.equal(slotAt(shown, 'D', -1, false), undefined);
});

test('a slot is stable once the icons made room', () => {
  // Pointer on the right half of "b": D lands between b and c, which puts D under the pointer.
  const base = ['D', 'a', 'b', 'c'];
  const before = slotAt(base, 'D', 2, true);
  assert.equal(before, 'c');
  const shown = preview(base, 'D', before);
  assert.deepEqual(shown, ['a', 'b', 'D', 'c']);
  assert.equal(slotAt(shown, 'D', 2, true), undefined);
  assert.equal(slotAt(shown, 'D', 2, false), undefined);
});

test('preview without a slot shows the order as it is', () => {
  assert.deepEqual(preview(['a', 'b'], 'a', undefined), ['a', 'b']);
  assert.deepEqual(preview(['a', 'b'], 'x', undefined), ['a', 'b']);
  assert.deepEqual(preview(['a', 'b'], 'x', 'a'), ['x', 'a', 'b']);
});

test('toSave keeps apps that have not loaded yet in their place', () => {
  const saved = ['a', 'store:s', 'b', 'native:n', 'gone'];
  const loading = (k: string) => k.startsWith('store:');
  assert.deepEqual(toSave(saved, ['b', 'a'], loading), ['b', 'a', 'store:s']);
  assert.deepEqual(toSave(['store:s', 'a'], ['a'], loading), ['store:s', 'a']);
  assert.deepEqual(toSave(saved, ['a', 'b'], () => false), ['a', 'b']);
});

test('cellAt finds the cell and half across pages', () => {
  const box = { left: 100, top: 50, width: 400, height: 200 }; // 4 x 2 cells of 100 x 100
  assert.deepEqual(cellAt(110, 60, box, 4, 2, 0), { index: 0, after: false });
  assert.deepEqual(cellAt(190, 60, box, 4, 2, 0), { index: 0, after: true });
  assert.deepEqual(cellAt(260, 160, box, 4, 2, 0), { index: 5, after: true });
  assert.deepEqual(cellAt(110, 60, box, 4, 2, 2), { index: 16, after: false });
  assert.equal(cellAt(90, 60, box, 4, 2, 0), null);
  assert.equal(cellAt(110, 260, box, 4, 2, 0), null);
});
