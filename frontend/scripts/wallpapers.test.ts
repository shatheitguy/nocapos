// Wallpaper fallback: run with `npm test` (node's test runner, types stripped).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { isCustomWallpaper, pickWallpaper } from '../src/lib/wallpaperPick.ts';

const BUILT_IN = ['tide', 'nebula', 'midnight'];

test('a built-in wallpaper is kept', () => {
  assert.equal(pickWallpaper('nebula', BUILT_IN, []), 'nebula');
});

test('retired wallpapers fall back to the first default', () => {
  for (const old of ['blaze', 'aurora', 'nocap', 'graphite', 'signal']) {
    assert.equal(pickWallpaper(old, BUILT_IN, []), 'tide', old);
  }
});

test('missing or empty choices fall back to the first default', () => {
  assert.equal(pickWallpaper(undefined, BUILT_IN, []), 'tide');
  assert.equal(pickWallpaper('', BUILT_IN, []), 'tide');
});

test('your own pictures are kept while they are stored', () => {
  assert.equal(pickWallpaper('custom', BUILT_IN, ['custom']), 'custom');
  assert.equal(pickWallpaper('custom-abc', BUILT_IN, ['custom', 'custom-abc']), 'custom-abc');
});

test('a deleted picture of your own falls back to the first default', () => {
  assert.equal(pickWallpaper('custom-abc', BUILT_IN, ['custom']), 'tide');
  assert.equal(pickWallpaper('custom', BUILT_IN, []), 'tide');
});

test('only added pictures count as custom', () => {
  assert.ok(isCustomWallpaper('custom'));
  assert.ok(isCustomWallpaper('custom-x1'));
  assert.ok(!isCustomWallpaper('tide'));
  assert.ok(!isCustomWallpaper('customs'));
});
