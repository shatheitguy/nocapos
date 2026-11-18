// NoCap Desktop's front window (menu bar slides away): run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { frontMaximized, type FrontWindow } from '../src/native/frontWindow.ts';

const win = (id: string, o: Partial<FrontWindow> = {}): FrontWindow => ({ id, active: false, minimized: false, maximized: false, ...o });

test('an active maximized window hides the menu bar', () => {
  assert.deepEqual(frontMaximized([win('a'), win('b', { active: true, maximized: true })], null), { maxed: true, front: 'b' });
});

test('an active window that is not maximized keeps it', () => {
  assert.equal(frontMaximized([win('a', { maximized: true }), win('b', { active: true })], 'a').maxed, false);
});

test('no windows, nothing active and nothing before: shown', () => {
  assert.deepEqual(frontMaximized([], null), { maxed: false, front: null });
  assert.equal(frontMaximized([win('a', { maximized: true })], null).maxed, false);
});

test('while nothing is active, the last active window still counts', () => {
  assert.deepEqual(frontMaximized([win('a', { maximized: true })], 'a'), { maxed: true, front: 'a' });
});

test('the last active window closed or minimized: shown again', () => {
  assert.deepEqual(frontMaximized([win('b')], 'a'), { maxed: false, front: null });
  assert.deepEqual(frontMaximized([win('a', { maximized: true, minimized: true })], 'a'), { maxed: false, front: null });
});

test('restoring the window brings the menu bar back', () => {
  assert.deepEqual(frontMaximized([win('a', { active: true })], 'a'), { maxed: false, front: 'a' });
});
