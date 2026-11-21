// Middle-clicking an app icon: run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { MIDDLE, middleAction } from '../src/lib/middleClick.ts';

test('middle-click opens another window of an app that allows several', () => {
  assert.equal(middleAction(MIDDLE, true), 'new');
});

test('middle-click brings a single-window app forward', () => {
  assert.equal(middleAction(MIDDLE, false), 'focus');
});

test('other buttons are left to the icon', () => {
  assert.equal(middleAction(0, true), null); // left: the click handler
  assert.equal(middleAction(2, true), null); // right: the menu
  assert.equal(middleAction(3, false), null); // back
});
