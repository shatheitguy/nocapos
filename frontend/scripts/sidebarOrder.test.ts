// Files sidebar order and hidden places: run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { arrangeFavs, arrangePlaces, cleanLayout, dropIndex, moveTo, setHidden, shownPlaces, step } from '../src/lib/sidebarOrder.ts';

test('arrangePlaces keeps the saved order and puts new places in their usual spot', () => {
  const usual = ['home', 'recents', 'drops', 'appdata', 'root:system'];
  assert.deepEqual(arrangePlaces([], usual), usual);
  assert.deepEqual(arrangePlaces(['recents', 'home', 'appdata', 'root:system'], usual), ['recents', 'drops', 'home', 'appdata', 'root:system']);
  // Places that are gone (e.g. not an admin any more) drop out; duplicates too.
  assert.deepEqual(arrangePlaces(['appdata', 'home', 'home', 'root:pool'], ['home', 'recents']), ['home', 'recents']);
  // A new first place goes first.
  assert.deepEqual(arrangePlaces(['recents', 'home'], ['new', 'home', 'recents']), ['new', 'recents', 'home']);
});

test('arrangeFavs keeps their own order and adds newly pinned ones at the end', () => {
  assert.deepEqual(arrangeFavs(['b', 'a'], ['a', 'b', 'c']), ['b', 'a', 'c']);
  assert.deepEqual(arrangeFavs(['gone', 'b'], ['a', 'b']), ['b', 'a']);
});

test('moveTo and step reorder', () => {
  assert.deepEqual(moveTo(['a', 'b', 'c', 'd'], 'a', 2), ['b', 'c', 'a', 'd']);
  assert.deepEqual(moveTo(['a', 'b', 'c'], 'c', 0), ['c', 'a', 'b']);
  assert.deepEqual(moveTo(['a', 'b'], 'x', 0), ['a', 'b']);
  assert.deepEqual(step(['a', 'b', 'c'], 'b', -1), ['b', 'a', 'c']);
  assert.deepEqual(step(['a', 'b', 'c'], 'a', -1), ['a', 'b', 'c']);
  assert.deepEqual(step(['a', 'b', 'c'], 'c', 1), ['a', 'b', 'c']);
  // Hidden places are skipped over and keep their slot.
  assert.deepEqual(step(['a', 'h', 'b'], 'b', -1, ['h']), ['b', 'h', 'a']);
});

test('hidden places', () => {
  assert.deepEqual(setHidden([], 'recents', true), ['recents']);
  assert.deepEqual(setHidden(['recents', 'drops'], 'recents', false), ['drops']);
  assert.deepEqual(setHidden(['recents'], 'recents', true), ['recents']);
  assert.deepEqual(shownPlaces(['home', 'recents', 'appdata'], ['recents']), ['home', 'appdata']);
});

test('cleanLayout drops anything unexpected', () => {
  assert.deepEqual(cleanLayout(null), { order: [], hidden: [], favs: [] });
  assert.deepEqual(cleanLayout({ order: ['home', 3, 'home', ''], hidden: 'x', favs: ['a:/b'] }), { order: ['home'], hidden: [], favs: ['a:/b'] });
});

test('dropIndex follows the pointer past item centres', () => {
  assert.equal(dropIndex(5, [10, 30, 50]), 0);
  assert.equal(dropIndex(31, [10, 30, 50]), 2);
  assert.equal(dropIndex(99, [10, 30, 50]), 3);
});
