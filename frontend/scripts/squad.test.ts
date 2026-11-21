// Your Squad logic: run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  blipSpot,
  canAsk,
  mergeFound,
  minutesLeft,
  prettyFingerprint,
  safeURL,
  shortFingerprint,
  statusOf,
  type FoundServer,
  type Squad,
} from '../src/lib/squad.ts';

const found = (id: string, over: Partial<FoundServer> = {}): FoundServer => ({
  nocap_id: id,
  name: id,
  hostname: '',
  addresses: ['192.168.1.2'],
  url: 'https://192.168.1.2:8443',
  version: '',
  setup: 'done',
  ...over,
});

const squad = (over: Partial<Squad> = {}): Squad => ({
  self: { nocap_id: 'NC-SELF', name: 'me', version: '', fingerprint: '', can_link: true },
  linked: [],
  incoming: [],
  outgoing: [],
  ...over,
});

const ask = (id: string, state: 'pending' | 'declined' = 'pending') => ({
  id: 'r' + id,
  nocap_id: id,
  name: id,
  url: '',
  address: '',
  version: '',
  fingerprint: '',
  state,
  created_at: '',
  expires_at: '',
});

test('statusOf: linked beats asking beats asked', () => {
  const s = found('NC-A');
  assert.equal(statusOf(s, null), 'none');
  assert.equal(statusOf(found('NC-A', { setup: 'new' }), null), 'new');
  assert.equal(statusOf(s, squad({ outgoing: [ask('NC-A')] })), 'sent');
  assert.equal(statusOf(s, squad({ outgoing: [ask('NC-A', 'declined')] })), 'declined');
  assert.equal(statusOf(s, squad({ incoming: [ask('NC-A')], outgoing: [ask('NC-A')] })), 'incoming');
  const linked = { nocap_id: 'NC-A', name: 'A', url: '', fingerprint: '', version: '', online: true, linked_by: '', created_at: '' };
  assert.equal(statusOf(s, squad({ linked: [linked], incoming: [ask('NC-A')] })), 'linked');
  assert.equal(statusOf(found('NC-B'), squad({ linked: [linked] })), 'none');
});

test('canAsk: only when nothing is going on (or it was declined)', () => {
  assert.ok(canAsk('none'));
  assert.ok(canAsk('declined'));
  for (const st of ['linked', 'incoming', 'sent', 'new'] as const) assert.ok(!canAsk(st));
});

test('mergeFound keeps order, adds new ones last and marks them fresh', () => {
  const a = mergeFound([], [found('NC-B'), found('NC-A')]);
  assert.deepEqual(a.list.map((s) => s.nocap_id), ['NC-B', 'NC-A']);
  assert.deepEqual(a.fresh, ['NC-B', 'NC-A']);
  const b = mergeFound(a.list, [found('NC-A', { version: '2' }), found('NC-C'), found('NC-B')]);
  assert.deepEqual(b.list.map((s) => s.nocap_id), ['NC-B', 'NC-A', 'NC-C']);
  assert.deepEqual(b.fresh, ['NC-C']);
  assert.equal(b.list[1].version, '2');
  // Gone from the network: gone from the list.
  assert.deepEqual(mergeFound(b.list, [found('NC-C')]).list.map((s) => s.nocap_id), ['NC-C']);
});

test('blipSpot: stable, inside the radar, away from the centre', () => {
  for (const id of ['NC-AAAA', 'NC-BBBB', 'NC-123', 'NC-x']) {
    for (let i = 0; i < 6; i++) {
      const p = blipSpot(id, i);
      assert.deepEqual(blipSpot(id, i), p);
      const d = Math.hypot(p.x - 50, p.y - 50);
      assert.ok(d >= 19 && d <= 47, `${id}/${i}: ${d}`);
    }
  }
  assert.notDeepEqual(blipSpot('NC-AAAA', 0), blipSpot('NC-BBBB', 0));
});

test('fingerprints read in pairs', () => {
  const fp = 'ab'.repeat(32);
  assert.equal(prettyFingerprint(fp).split(':').length, 32);
  assert.ok(prettyFingerprint(fp).startsWith('AB:AB'));
  assert.equal(shortFingerprint('0123456789abcdef'.repeat(4)), '01:23:45:67…89:AB:CD:EF');
  assert.equal(shortFingerprint('0a0b'), '0A:0B');
  assert.equal(prettyFingerprint(''), '');
});

test('minutesLeft', () => {
  const now = Date.parse('2026-11-21T10:00:00Z');
  assert.equal(minutesLeft('2026-11-21T10:10:00Z', now), 10);
  assert.equal(minutesLeft('2026-11-21T10:00:05Z', now), 1);
  assert.equal(minutesLeft('2026-11-21T09:59:00Z', now), 0);
  assert.equal(minutesLeft('nonsense', now), 0);
});

test('safeURL only lets http(s) out', () => {
  assert.equal(safeURL('https://192.168.1.2:8443'), 'https://192.168.1.2:8443');
  assert.equal(safeURL('javascript:alert(1)'), '');
});
