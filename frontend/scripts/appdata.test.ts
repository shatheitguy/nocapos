// App data folder paths: run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { isSuggested, safeName, sizeText, suggestPath } from '../src/lib/appdataPaths.ts';

test('safeName makes folder names the way the server does', () => {
  assert.equal(safeName('jellyfin'), 'jellyfin');
  assert.equal(safeName('My App'), 'my-app');
  assert.equal(safeName('../../etc'), 'etc');
  assert.equal(safeName('..'), 'app');
  assert.equal(safeName(''), 'app');
  assert.equal(safeName('a/b\\c'), 'a-b-c');
  assert.equal(safeName('x'.repeat(80)), 'x'.repeat(64));
});

test('suggestPath is <root>/<owner>/<last part of the target>', () => {
  assert.equal(suggestPath('/srv/appdata', 'jellyfin', '/config'), '/srv/appdata/jellyfin/config');
  assert.equal(suggestPath('/srv/appdata/', 'Home Assistant', '/var/lib/data/'), '/srv/appdata/home-assistant/data');
  assert.equal(suggestPath('/srv/appdata', 'web', ''), '/srv/appdata/web/data');
});

test('isSuggested recognises paths it would have made', () => {
  assert.ok(isSuggested('/srv/appdata/web/config', '/srv/appdata', 'web'));
  assert.ok(isSuggested('', '/srv/appdata', 'web'));
  assert.ok(!isSuggested('/srv/media', '/srv/appdata', 'web'));
  assert.ok(!isSuggested('/srv/appdata/web/a/b', '/srv/appdata', 'web'));
});

test('sizeText', () => {
  assert.equal(sizeText(-1), '…');
  assert.equal(sizeText(512), '512 B');
  assert.equal(sizeText(1536), '1.5 KB');
  assert.equal(sizeText(5 * 1024 ** 3), '5.0 GB');
});
