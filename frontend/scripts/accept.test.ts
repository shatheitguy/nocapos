// NoCap Desktop's file picker: which files a request takes. Run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { acceptEndings, acceptKind, acceptTest, mimeOf } from '../src/lib/accept.ts';

test('an empty accept list takes every file', () => {
  const ok = acceptTest([]);
  assert.ok(ok('notes.txt'));
  assert.ok(ok('no-ending'));
});

test('image/* takes pictures only', () => {
  const ok = acceptTest(['image/*']);
  assert.ok(ok('cat.PNG'));
  assert.ok(ok('holiday.jpeg'));
  assert.ok(!ok('song.mp3'));
  assert.ok(!ok('report.pdf'));
});

test('exact types, endings and the toolkit-style patterns WebKit hands over', () => {
  const photos = acceptTest(['image/*', 'video/*', '.heic', '*.heif']);
  assert.ok(photos('clip.mov'));
  assert.ok(photos('IMG_1.HEIC'));
  assert.ok(photos('IMG_2.heif'));
  assert.ok(!photos('a.txt'));
  const keys = acceptTest(['.pub', 'text/plain']);
  assert.ok(keys('id_ed25519.pub'));
  assert.ok(keys('readme.txt'));
  assert.ok(!keys('id_ed25519'));
  const wall = acceptTest(['image/png', 'image/jpeg', 'image/webp', 'image/gif']);
  assert.ok(wall('a.jpg'));
  assert.ok(!wall('a.bmp'));
});

test('octet-stream (any binary) takes everything', () => {
  assert.ok(acceptTest(['.db', 'application/octet-stream'])('backup.anything'));
});

test('labels', () => {
  assert.equal(acceptKind([]), 'any');
  assert.equal(acceptKind(['image/*']), 'images');
  assert.equal(acceptKind(['image/png', '*.jpg']), 'images');
  assert.equal(acceptKind(['image/*', 'video/*', '.heic']), 'media');
  assert.equal(acceptKind(['.pub', 'text/plain']), 'other');
  assert.deepEqual(acceptEndings(['.pub', '*.db', 'text/plain']), ['.pub', '.db']);
  assert.equal(mimeOf('a.tar.gz'), 'application/gzip');
  assert.equal(mimeOf('.bashrc'), '');
});
