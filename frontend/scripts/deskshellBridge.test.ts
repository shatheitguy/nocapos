// NoCap Shell: the bridge client and the sign-in screen's helpers. Run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { BridgeError, createBridge } from '../src/deskshell/bridge.ts';
import { hueOf, initials } from '../src/deskshell/logic.ts';

// ---------- bridge ----------

function harness(timeoutMs = 10_000) {
  const sent: { id: number; method: string; params?: unknown }[] = [];
  const timers: { fn: () => void; ms: number; live: boolean }[] = [];
  const bridge = createBridge((json) => sent.push(JSON.parse(json)), {
    timeoutMs,
    setTimer: (fn, ms) => {
      const t = { fn, ms, live: true };
      timers.push(t);
      return t;
    },
    clearTimer: (h) => {
      (h as { live: boolean }).live = false;
    },
  });
  const fire = () => timers.filter((t) => t.live).forEach((t) => ((t.live = false), t.fn()));
  return { bridge, sent, timers, fire };
}

test('call posts {id, method, params} JSON and resolves with the matching reply', async () => {
  const { bridge, sent } = harness();
  const a = bridge.call('audio.get');
  const b = bridge.call('apps.launch', { id: 'files.desktop' });
  assert.equal(sent.length, 2);
  assert.deepEqual(sent[0], { id: sent[0].id, method: 'audio.get' });
  assert.deepEqual(sent[1], { id: sent[1].id, method: 'apps.launch', params: { id: 'files.desktop' } });
  assert.notEqual(sent[0].id, sent[1].id);
  // Replies may come in any order.
  bridge.reply(sent[1].id, true, null);
  bridge.reply(sent[0].id, true, { volume: 40 });
  assert.deepEqual(await a, { volume: 40 });
  assert.equal(await b, null);
  assert.equal(bridge.pending(), 0);
});

test('an error reply rejects with the host message', async () => {
  const { bridge, sent } = harness();
  const p = bridge.call('dock.set', { pinned: [] });
  bridge.reply(sent[0].id, false, { error: 'unknown parameter' });
  await assert.rejects(p, (e: unknown) => e instanceof BridgeError && e.method === 'dock.set' && /unknown parameter/.test(e.message));
  const q = bridge.call('nope');
  bridge.reply(sent[1].id, false, null);
  await assert.rejects(q, /refused/);
});

test('calls time out after the timeout, and a late reply is ignored', async () => {
  const { bridge, sent, timers, fire } = harness(10_000);
  const p = bridge.call('windows.list');
  assert.equal(timers[0].ms, 10_000);
  fire();
  await assert.rejects(p, /timed out/);
  assert.equal(bridge.pending(), 0);
  assert.doesNotThrow(() => bridge.reply(sent[0].id, true, []));
  // Unknown ids are ignored too.
  assert.doesNotThrow(() => bridge.reply(9999, true, null));
});

test('a reply clears its timer', async () => {
  const { bridge, sent, timers } = harness();
  const p = bridge.call('shell.info');
  bridge.reply(sent[0].id, true, { surface: 'bar' });
  await p;
  assert.equal(timers[0].live, false);
});

test('the default timeout is 10 s with real timers', async () => {
  const { CALL_TIMEOUT_MS } = await import('../src/deskshell/bridge.ts');
  assert.equal(CALL_TIMEOUT_MS, 10_000);
  const b = createBridge(() => {}, { timeoutMs: 5 });
  await assert.rejects(b.call('x'), /timed out/);
});

test('a failing post rejects the call', async () => {
  const b = createBridge(() => {
    throw new Error('no handler');
  });
  await assert.rejects(b.call('x'), /no handler/);
  assert.equal(b.pending(), 0);
});

test('events reach every listener of that name until unsubscribed', () => {
  const { bridge } = harness();
  const got: unknown[] = [];
  const off = bridge.on('notify.unread', (p) => got.push(['a', p]));
  bridge.on('notify.unread', (p) => got.push(['b', p]));
  bridge.on('other', () => got.push('wrong'));
  bridge.event('notify.unread', { count: 2 });
  off();
  bridge.event('notify.unread', { count: 3 });
  bridge.event('nobody.listens', {});
  assert.deepEqual(got, [['a', { count: 2 }], ['b', { count: 2 }], ['b', { count: 3 }]]);
});

test('a throwing listener does not stop the others', () => {
  const { bridge } = harness();
  let ok = false;
  const err = console.error;
  console.error = () => {};
  bridge.on('e', () => {
    throw new Error('boom');
  });
  bridge.on('e', () => (ok = true));
  bridge.event('e', null);
  console.error = err;
  assert.equal(ok, true);
});

// ---------- people ----------

test('initials', () => {
  assert.equal(initials('Alex Rivera'), 'AR');
  assert.equal(initials('sam'), 'S');
  assert.equal(initials('mary-jane watson'), 'MW');
  assert.equal(initials('  '), '?');
});

test('hueOf is stable and in range', () => {
  assert.equal(hueOf('sam'), hueOf('sam'));
  for (const n of ['', 'a', 'Alex Rivera']) assert.ok(hueOf(n) >= 0 && hueOf(n) < 360);
});
