// "Open with" choices and per-type defaults: run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { cleanDefaults, defaultFor, extOf, handlersFor, setAvailableHandlers, typeKey, withDefault } from '../src/lib/openWith.ts';

test('extensions', () => {
  assert.equal(extOf('Holiday.JPG'), 'jpg');
  assert.equal(extOf('archive.tar.gz'), 'gz');
  assert.equal(extOf('Makefile'), '');
  assert.equal(extOf('.env'), '');
  assert.equal(extOf('trailing.'), '');
  assert.equal(typeKey('notes', 'file'), ':file');
});

test('apps per kind, best first, and only the installed ones', () => {
  setAvailableHandlers(['photo', 'video', 'music', 'text', 'quicklook', 'browser', 'archive']);
  assert.deepEqual(handlersFor('image'), ['photo', 'quicklook', 'browser']);
  assert.equal(handlersFor('video')[0], 'video');
  assert.equal(handlersFor('audio')[0], 'music');
  assert.deepEqual(handlersFor('folder'), []);
  setAvailableHandlers(['quicklook', 'text']);
  assert.deepEqual(handlersFor('video'), ['quicklook']);
  setAvailableHandlers(['photo', 'video', 'music', 'text', 'quicklook', 'browser', 'archive']);
});

test('defaults are per type and must still fit the file', () => {
  let prefs = withDefault({}, 'a.mp4', 'video', 'quicklook');
  assert.equal(defaultFor('b.MP4', 'video', prefs), 'quicklook');
  assert.equal(defaultFor('b.mkv', 'video', prefs), null);
  prefs = withDefault(prefs, 'a.mp4', 'video', null);
  assert.deepEqual(prefs, {});
  // A default that doesn't fit the kind (e.g. edited by hand) is ignored.
  assert.equal(defaultFor('x.png', 'image', { png: 'music' }), null);
});

test('saved defaults are cleaned', () => {
  assert.deepEqual(cleanDefaults(null), {});
  assert.deepEqual(cleanDefaults([1, 2]), {});
  assert.deepEqual(cleanDefaults({ jpg: 'photo', mp4: 'nope', txt: 3, [`${'x'.repeat(30)}`]: 'text' }), { jpg: 'photo' });
});
