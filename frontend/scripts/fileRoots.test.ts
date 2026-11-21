// Which locations a Files pane may stay in: run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { isAppData, isHomeDir, opensUnlisted, paneRoot } from '../src/lib/fileRoots.ts';

// What an administrator's location list looks like: the drives and Homes,
// but not their own home folder.
const adminRoots = [{ id: 'storage' }, { id: 'system' }, { id: 'homes' }];

test('an administrator sent to their Received folder stays in their home', () => {
  // The Drops folder is <home>/Drops: the pane must not fall back to the
  // main drive (which would look for /Drops there and find nothing).
  assert.equal(paneRoot(adminRoots, 'homedir-sha'), 'homedir-sha');
});

test('listed locations and App data stay put', () => {
  assert.equal(paneRoot(adminRoots, 'system'), 'system');
  assert.equal(paneRoot(adminRoots, 'appdata'), 'appdata');
  assert.equal(paneRoot(adminRoots, 'app:media'), 'app:media');
});

test('a location that is gone falls back to the first one', () => {
  assert.equal(paneRoot(adminRoots, 'net:old'), 'storage');
  assert.equal(paneRoot(adminRoots, 'share-9'), 'storage');
});

test('nothing changes while the list is loading or empty', () => {
  assert.equal(paneRoot(null, 'homedir-sha'), 'homedir-sha');
  assert.equal(paneRoot([], 'net:old'), 'net:old');
  assert.equal(paneRoot(adminRoots, ''), '');
});

test('kinds of unlisted locations', () => {
  assert.ok(isHomeDir('homedir-alex'));
  assert.ok(!isHomeDir('homes'));
  assert.ok(isAppData('appdata') && isAppData('app:x') && !isAppData('apps'));
  assert.ok(opensUnlisted('homedir-alex') && opensUnlisted('app:x') && !opensUnlisted('storage'));
});
