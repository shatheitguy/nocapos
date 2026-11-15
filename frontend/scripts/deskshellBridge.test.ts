// NoCap Shell: the bridge client and the surfaces' pure helpers. Run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { BridgeError, createBridge } from '../src/deskshell/bridge.ts';
import {
  appForWindow,
  buildRoster,
  dockClick,
  dockEntries,
  folderOf,
  groupNotifications,
  initials,
  moveSelection,
  paginate,
  pushBanner,
  reorder,
  searchApps,
  togglePin,
  toMs,
  age,
} from '../src/deskshell/logic.ts';
import type { AppInfo, NotifyItem, WindowInfo } from '../src/deskshell/protocol.ts';

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

// ---------- dock ----------

const app = (id: string, name: string, categories: string[] = [], extra: Partial<AppInfo> = {}): AppInfo => ({
  id,
  name,
  comment: '',
  icon: '',
  categories,
  keywords: [],
  ...extra,
});
const win = (id: number, appId: string, extra: Partial<WindowInfo> = {}): WindowInfo => ({
  id,
  appId,
  title: appId,
  active: false,
  minimized: false,
  maximized: false,
  ...extra,
});

const APPS = [
  app('files.desktop', 'Files', ['Utility', 'FileManager']),
  app('org.example.Web.desktop', 'Web', ['Network', 'WebBrowser']),
  app('term.desktop', 'Terminal', ['System', 'TerminalEmulator']),
  app('calc.desktop', 'Calculator', ['Utility']),
];

test('appForWindow matches desktop ids loosely', () => {
  assert.equal(appForWindow('files', APPS)?.id, 'files.desktop');
  assert.equal(appForWindow('FILES.desktop', APPS)?.id, 'files.desktop');
  assert.equal(appForWindow('org.example.Web', APPS)?.id, 'org.example.Web.desktop');
  assert.equal(appForWindow('web', APPS)?.id, 'org.example.Web.desktop');
  assert.equal(appForWindow('mystery', APPS), null);
  assert.equal(appForWindow('', APPS), null);
});

test('dockEntries: pinned in order, then running apps that are not pinned', () => {
  const windows = [win(1, 'calc', { active: true }), win(2, 'files'), win(3, 'files', { minimized: true }), win(4, 'mystery')];
  const e = dockEntries(['term.desktop', 'files.desktop', 'gone.desktop', 'files.desktop'], windows, APPS);
  assert.deepEqual(
    e.map((x) => [x.id, x.pinned, x.windows.length, x.active]),
    [
      ['term.desktop', true, 0, false],
      ['files.desktop', true, 2, false],
      ['calc.desktop', false, 1, true],
      ['window:mystery', false, 1, false],
    ],
  );
  // Before apps.list answers, pins are kept as they are.
  assert.deepEqual(dockEntries(['a.desktop'], [], []).map((x) => x.id), ['a.desktop']);
});

test('dockClick: launch, focus, or minimize the active window', () => {
  const [term, files, calc] = dockEntries(['term.desktop', 'files.desktop'], [win(1, 'calc', { active: true }), win(2, 'files', { minimized: true }), win(3, 'files')], APPS);
  assert.deepEqual(dockClick(term), { kind: 'launch', id: 'term.desktop' });
  assert.deepEqual(dockClick(files), { kind: 'focus', window: win(3, 'files') });
  assert.deepEqual(dockClick(calc), { kind: 'minimize', window: win(1, 'calc', { active: true }) });
  const [lonely] = dockEntries([], [win(9, 'mystery', { minimized: true })], APPS);
  assert.deepEqual(dockClick(lonely), { kind: 'focus', window: win(9, 'mystery', { minimized: true }) });
});

test('reorder and togglePin', () => {
  assert.deepEqual(reorder(['a', 'b', 'c', 'd'], 0, 2), ['b', 'c', 'a', 'd']);
  assert.deepEqual(reorder(['a', 'b', 'c', 'd'], 3, 0), ['d', 'a', 'b', 'c']);
  assert.deepEqual(reorder(['a', 'b'], 5, 0), ['a', 'b']);
  assert.deepEqual(reorder(['a', 'b', 'c'], 0, 99), ['b', 'c', 'a']);
  assert.deepEqual(togglePin(['a', 'b'], 'a'), ['b']);
  assert.deepEqual(togglePin(['a'], 'b'), ['a', 'b']);
  assert.equal(togglePin(Array.from({ length: 24 }, (_, i) => `x${i}`), 'new').length, 24);
});

// ---------- roster ----------

test('folderOf picks the folder by category priority', () => {
  assert.equal(folderOf(app('g', 'Chess', ['Game', 'Network'])), 'games');
  assert.equal(folderOf(app('s', 'Settings', ['GTK', 'Settings', 'System'])), 'system');
  assert.equal(folderOf(app('v', 'Videos', ['AudioVideo', 'Video'])), 'media');
  assert.equal(folderOf(app('w', 'Writer', ['Office', 'WordProcessor'])), 'office');
  assert.equal(folderOf(app('u', 'Calc', ['Utility'])), 'utilities');
  assert.equal(folderOf(app('x', 'IDE', ['Development'])), null);
});

test('buildRoster makes folders only for two or more apps, then the rest A-Z', () => {
  const r = buildRoster([
    app('chess', 'Chess', ['Game']),
    app('mines', 'Mines', ['Game']),
    app('mail', 'Mail', ['Network', 'Email']),
    app('zed', 'zed', ['Development']),
    app('alpha', 'Alpha', []),
    app('calc', 'Calculator', ['Utility']),
    app('edit', 'Editor', ['Utility', 'TextEditor']),
  ]);
  assert.deepEqual(
    r.map((e) => (e.kind === 'folder' ? `folder:${e.id}:${e.apps.map((a) => a.name).join('+')}` : e.app.name)),
    ['folder:games:Chess+Mines', 'folder:utilities:Calculator+Editor', 'Alpha', 'Mail', 'zed'],
  );
});

test('searchApps ranks name prefix, then word prefix, then keywords and descriptions', () => {
  const apps = [
    app('1', 'Text Editor', [], { keywords: ['notepad'] }),
    app('2', 'Terminal', [], { comment: 'Run commands in a text shell' }),
    app('3', 'Contacts'),
    app('4', 'Paint', [], { keywords: ['draw'] }),
  ];
  assert.deepEqual(searchApps(apps, 'te').map((a) => a.name), ['Terminal', 'Text Editor']);
  assert.deepEqual(searchApps(apps, 'text').map((a) => a.name), ['Text Editor', 'Terminal']);
  assert.deepEqual(searchApps(apps, 'DRAW').map((a) => a.name), ['Paint']);
  assert.deepEqual(searchApps(apps, 'text edi').map((a) => a.name), ['Text Editor']);
  assert.deepEqual(searchApps(apps, '   '), []);
  assert.deepEqual(searchApps(apps, 'zzz'), []);
});

test('paginate and moveSelection', () => {
  assert.deepEqual(paginate([1, 2, 3, 4, 5], 2), [[1, 2], [3, 4], [5]]);
  assert.deepEqual(paginate([], 4), [[]]);
  // 10 icons, 3 columns, 6 per page.
  assert.equal(moveSelection(-1, 'ArrowRight', 10, 3, 6), 0);
  assert.equal(moveSelection(0, 'ArrowRight', 10, 3, 6), 1);
  assert.equal(moveSelection(0, 'ArrowLeft', 10, 3, 6), 0);
  assert.equal(moveSelection(1, 'ArrowDown', 10, 3, 6), 4);
  assert.equal(moveSelection(8, 'ArrowDown', 10, 3, 6), 9);
  assert.equal(moveSelection(4, 'ArrowUp', 10, 3, 6), 1);
  assert.equal(moveSelection(2, 'PageDown', 10, 3, 6), 6);
  assert.equal(moveSelection(7, 'PageDown', 10, 3, 6), 6);
  assert.equal(moveSelection(7, 'PageUp', 10, 3, 6), 0);
  assert.equal(moveSelection(3, 'End', 10, 3, 6), 9);
  assert.equal(moveSelection(3, 'x', 10, 3, 6), 3);
  assert.equal(moveSelection(3, 'ArrowRight', 0, 3, 6), -1);
});

// ---------- notifications ----------

const note = (id: string, app: string, time: number, source: NotifyItem['source'] = 'app'): NotifyItem => ({
  id,
  source,
  app,
  icon: '',
  title: id,
  body: '',
  time,
  urgency: 1,
  actions: [],
  read: false,
});

test('groupNotifications groups by app, newest group and newest item first', () => {
  const g = groupNotifications([note('a', 'Mail', 100), note('b', 'Chat', 300), note('c', 'mail', 200), note('d', 'NoCapOS', 50, 'nocapos'), note('e', 'Other', 40, 'nocapos')]);
  assert.deepEqual(
    g.map((x) => [x.key, x.items.map((n) => n.id)]),
    [
      ['app:chat', ['b']],
      ['app:mail', ['c', 'a']],
      ['nocapos', ['d', 'e']],
    ],
  );
});

test('pushBanner keeps three, newest first, without duplicates', () => {
  let list: { id: number }[] = [];
  for (const id of [1, 2, 3, 4]) list = pushBanner(list, { id });
  assert.deepEqual(list.map((n) => n.id), [4, 3, 2]);
  assert.deepEqual(pushBanner(list, { id: 2 }).map((n) => n.id), [2, 4, 3]);
});

test('times: seconds or milliseconds, and short ages', () => {
  assert.equal(toMs(1_700_000_000), 1_700_000_000_000);
  assert.equal(toMs(1_700_000_000_000), 1_700_000_000_000);
  const now = 1_700_000_000_000;
  assert.deepEqual(age(now - 5_000, now), { unit: 'now' });
  assert.deepEqual(age(now - 5 * 60_000, now), { unit: 'min', n: 5 });
  assert.deepEqual(age(now - 3 * 3600_000, now), { unit: 'hour', n: 3 });
  assert.equal(age(now - 2 * 86400_000, now), null);
});

test('initials', () => {
  assert.equal(initials('Alex Rivera'), 'AR');
  assert.equal(initials('sam'), 'S');
  assert.equal(initials('mary-jane watson'), 'MW');
  assert.equal(initials('  '), '?');
});
