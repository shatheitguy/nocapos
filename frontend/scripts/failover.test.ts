// Failover page logic: run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { changedFields, filesChoice, syncHealth, takeoverPhase, type Takeover, type FailoverPair, type SyncConflict } from '../src/lib/failover.ts';

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

test('filesChoice: only switched-on locations, never "always" ones, sane limit and window', () => {
  const f = {
    locations: [
      { id: 'drive', name: 'NoCap Drive', kind: 'drive', on: true, size: 1, files: 1, queued: 0, queued_bytes: 0, received: 0 },
      { id: 'appdata', name: 'App data', kind: 'appdata', on: false, size: 1, files: 1, queued: 0, queued_bytes: 0, received: 0 },
      { id: 'config', name: 'Containers', kind: 'config', on: true, always: true, size: 1, files: 1, queued: 0, queued_bytes: 0, received: 0 },
    ],
    limit_kbps: 0,
    window_from: '',
    window_to: '',
    in_window: true,
  } as const;
  const c = filesChoice({ ...f, locations: [...f.locations] }, { appdata: true, drive: false }, '-5', '01:00', '');
  assert.deepEqual(c, { files: ['appdata'], limit_kbps: 0, window_from: '', window_to: '' });
  const d = filesChoice({ ...f, locations: [...f.locations] }, {}, '512.4', '22:00', '06:00');
  assert.deepEqual(d, { files: ['drive'], limit_kbps: 512, window_from: '22:00', window_to: '06:00' });
});

test('takeoverPhase: from each side', () => {
  const t = (over: Partial<Takeover>): Takeover => ({
    role: 'standby', running: false, peer_running: true, peer_up: true, auto: true, after_min: 5, vip: '', vip_iface: '', vip_held: false, can_hand_back: false, busy: false, ...over,
  });
  assert.equal(takeoverPhase(t({})), 'normal');
  assert.equal(takeoverPhase(t({ peer_up: false })), 'primary_down');
  assert.equal(takeoverPhase(t({ running: true })), 'took_over');
  assert.equal(takeoverPhase(t({ role: 'primary', running: true })), 'normal');
  assert.equal(takeoverPhase(t({ role: 'primary', running: true, peer_up: false })), 'standby_down');
  assert.equal(takeoverPhase(t({ role: 'primary', running: false })), 'stood_back');
});
