// What a picture window from Photos steps through: run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { decodeSet, encodeSet, keysSet, MAX_KEYS, pickSet, type SetPhoto } from '../src/lib/photoSet.ts';

const now = Date.parse('2026-11-22T12:00:00Z');
const p = (path: string, extra: Partial<SetPhoto> = {}): SetPhoto => ({ root: 'home', path, mod_time: '2026-11-20T10:00:00Z', ...extra });
const items = [
  p('/Photos/a.jpg', { favorite: true }),
  p('/Photos/b.mp4', { video: true, mod_time: '2026-11-21T10:00:00Z' }),
  p('/Photos/old.jpg', { mod_time: '2025-01-01T00:00:00Z' }),
  p('/Photos/Trip/beach.jpg', { mod_time: '2026-11-19T10:00:00Z' }),
];
const albums = [{ id: 7, items: [{ root: 'home', path: '/Photos/old.jpg' }, { root: 'home', path: '/Photos/Trip/beach.jpg' }] }];
const names = (l: SetPhoto[]) => l.map((x) => x.path.split('/').pop());

test('a view of the library picks the same pictures Photos shows', () => {
  assert.deepEqual(names(pickSet({ view: 'library' }, items, albums, [], now)), ['a.jpg', 'b.mp4', 'old.jpg', 'beach.jpg']);
  assert.deepEqual(names(pickSet({ view: 'favorites' }, items, albums, [], now)), ['a.jpg']);
  assert.deepEqual(names(pickSet({ view: 'videos' }, items, albums, [], now)), ['b.mp4']);
  // Recent: the last 30 days, newest first.
  assert.deepEqual(names(pickSet({ view: 'recent' }, items, albums, [], now)), ['b.mp4', 'a.jpg', 'beach.jpg']);
  assert.deepEqual(names(pickSet({ view: 'album', album: 7 }, items, albums, [], now)), ['old.jpg', 'beach.jpg']);
  assert.deepEqual(pickSet({ view: 'album', album: 99 }, items, albums, [], now), []);
  const bin = [p('/.trash/x.jpg', { trash: { id: 1 } })];
  assert.deepEqual(names(pickSet({ view: 'trash' }, items, albums, bin, now)), ['x.jpg']);
  // A search narrows any view.
  assert.deepEqual(names(pickSet({ view: 'library', q: 'TRIP' }, items, albums, [], now)), ['beach.jpg']);
});

test('a fixed list (a memory) keeps its order and drops pictures that are gone', () => {
  const set = { keys: [{ root: 'home', path: '/Photos/Trip/beach.jpg' }, { root: 'usb', path: '/gone.jpg' }, { root: 'home', path: '/Photos/a.jpg' }] };
  assert.deepEqual(names(pickSet(set, items, albums, [], now)), ['beach.jpg', 'a.jpg']);
});

test('long lists keep the pictures around the one opened', () => {
  const list = Array.from({ length: 200 }, (_, i) => ({ root: 'home', path: `/p${i}.jpg` }));
  const mid = keysSet(list, 120);
  assert.ok('keys' in mid.set);
  if (!('keys' in mid.set)) return;
  assert.equal(mid.set.keys.length, MAX_KEYS);
  assert.equal(mid.set.keys[mid.index].path, '/p120.jpg');
  const start = keysSet(list, 3);
  if ('keys' in start.set) assert.equal(start.set.keys[start.index].path, '/p3.jpg');
  const end = keysSet(list, 199);
  if ('keys' in end.set) assert.equal(end.set.keys[end.index].path, '/p199.jpg');
  const short = keysSet(list.slice(0, 5), 4);
  if ('keys' in short.set) assert.equal(short.set.keys.length, 5);
  assert.equal(short.index, 4);
  // Small enough for a window's props.
  assert.ok(encodeSet(mid.set).length < 8192);
});

test('sets survive the trip through window props, and junk is refused', () => {
  for (const set of [{ view: 'album' as const, album: 7, q: 'x' }, { view: 'library' as const }, { keys: [{ root: 'a', path: '/b' }] }]) {
    assert.deepEqual(decodeSet(encodeSet(set)), set);
  }
  assert.equal(decodeSet(undefined), null);
  assert.equal(decodeSet('not json'), null);
  assert.equal(decodeSet('{"view":"everything"}'), null);
  assert.equal(decodeSet('42'), null);
  assert.deepEqual(decodeSet('{"keys":[{"root":"a","path":"/b","x":1},{"root":3}]}'), { keys: [{ root: 'a', path: '/b' }] });
});
