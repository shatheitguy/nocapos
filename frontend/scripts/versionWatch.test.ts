import { test } from 'node:test';
import assert from 'node:assert/strict';
import { shouldReload } from '../src/native/versionCheck.ts';

test('a new server version reloads', () => {
  assert.equal(shouldReload('0.14.17', '0.14.18'), true);
});
test('the same version, or no answer (the server restarting), does not', () => {
  assert.equal(shouldReload('0.14.17', '0.14.17'), false);
  assert.equal(shouldReload('0.14.17', null), false);
  assert.equal(shouldReload(null, '0.14.18'), false);
});
