// Failover page logic: run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { changedFields, syncHealth, type FailoverPair, type SyncConflict } from '../src/lib/failover.ts';

const now = Date.parse('2026-11-22T10:00:00Z');
const pair = (over: Partial<FailoverPair> = {}): FailoverPair => ({
  nocap_id: 'b',
  name: 'B',
  url: 'https://b:8443',
  online: true,
  state: 'active',
  primary: 'self',
  paused: false,
  created_at: '2026-11-22T09:00:00Z',
  last_ok: '2026-11-22T09:59:55Z',
  failures: 0,
  queued: 0,
  conflicts: 0,
  categories: [],
  ...over,
});

test('syncHealth: states before active pass through', () => {
  assert.equal(syncHealth(pair({ state: 'asked' }), now), 'asked');
  assert.equal(syncHealth(pair({ state: 'asking' }), now), 'asking');
  assert.equal(syncHealth(pair({ state: 'starting' }), now), 'starting');
});

test('syncHealth: paused, failing, behind, ok', () => {
  assert.equal(syncHealth(pair(), now), 'ok');
  assert.equal(syncHealth(pair({ paused: true, failures: 9 }), now), 'paused');
  assert.equal(syncHealth(pair({ failures: 3 }), now), 'failing');
  assert.equal(syncHealth(pair({ failures: 2 }), now), 'ok');
  assert.equal(syncHealth(pair({ last_ok: '2026-11-22T09:58:00Z' }), now), 'behind');
  assert.equal(syncHealth(pair({ last_ok: undefined }), now), 'behind');
});

test('changedFields: only what differs, sorted', () => {
  const c: SyncConflict = {
    id: 1,
    table: 'users',
    category: 'accounts',
    key: '"u1"',
    mine: { username: 'sam', role: 'admin', full_name: 'Sam' },
    theirs: { username: 'sam', role: 'user', theme: 'dark' },
    winner: 'theirs',
    created_at: '',
  };
  assert.deepEqual(changedFields(c), ['full_name', 'role', 'theme']);
  assert.deepEqual(changedFields({ ...c, mine: null }), ['role', 'theme', 'username']);
});
