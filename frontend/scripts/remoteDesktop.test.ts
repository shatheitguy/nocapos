// Remote Desktop logic: run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPEEDS,
  addHistory,
  blankForm,
  classifyFailure,
  keysymFor,
  optionsFor,
  qualityOf,
  sortComputers,
  speedOf,
  splitHost,
  userLabel,
  validHost,
  withSpeed,
} from '../src/apps/remote/rdModel.ts';

test('the Computer field takes host, host:port and IPv6', () => {
  assert.deepEqual(splitHost(' pc.local '), { host: 'pc.local' });
  assert.deepEqual(splitHost('10.0.0.5:3390'), { host: '10.0.0.5', port: 3390 });
  assert.deepEqual(splitHost('[fe80::1]:5901'), { host: 'fe80::1', port: 5901 });
  assert.deepEqual(splitHost('fe80::1'), { host: 'fe80::1' });
  assert.deepEqual(splitHost('pc:99999'), { host: 'pc:99999' });
  assert.ok(validHost('pc.local'));
  assert.ok(!validHost('a b'));
  assert.ok(!validHost('x/y'));
  assert.ok(!validHost(''));
});

test('speed presets set the experience switches, and are recognised again', () => {
  const lan = withSpeed(blankForm().options, 'lan');
  assert.equal(speedOf(lan), 'lan');
  assert.equal(lan.wallpaper, true);
  const low = withSpeed(lan, 'low');
  assert.equal(speedOf(low), 'low');
  assert.ok(Object.values(SPEEDS.low).every((v) => v === false));
  assert.equal(speedOf({ ...low, wallpaper: true }), 'custom');
  assert.equal(speedOf(blankForm().options), 'broadband');
});

test('VNC drops what only RDP has', () => {
  const o = optionsFor('vnc', { ...withSpeed(blankForm().options, 'lan'), gateway_host: 'gw', audio: true, color_depth: 32, security: 'nla' });
  assert.equal(o.gateway_host, undefined);
  assert.equal(o.audio, undefined);
  assert.equal(o.security, undefined);
  assert.equal(o.wallpaper, undefined);
  assert.equal(o.color_depth, 0);
  assert.equal(optionsFor('rdp', { audio: true }).audio, true);
});

test('saved computers sort and search', () => {
  const list = [
    { name: 'Office', hostname: '10.0.0.20', username: 'me', last_used: 5 },
    { name: 'attic', hostname: '10.0.0.3', username: 'kid' },
    { name: 'Lab 10', hostname: 'lab.local', username: '', last_used: 9 },
  ];
  assert.deepEqual(sortComputers(list, 'name', '').map((c) => c.name), ['attic', 'Lab 10', 'Office']);
  assert.deepEqual(sortComputers(list, 'recent', '').map((c) => c.name), ['Lab 10', 'Office', 'attic']);
  assert.deepEqual(sortComputers(list, 'host', '').map((c) => c.name), ['attic', 'Office', 'Lab 10']);
  assert.deepEqual(sortComputers(list, 'name', 'KID').map((c) => c.name), ['attic']);
  assert.equal(userLabel('me', 'CORP'), 'CORP\\me');
  assert.equal(userLabel('', 'CORP'), '');
});

test('history keeps the newest address once', () => {
  assert.deepEqual(addHistory(['a', 'B', 'c'], 'b'), ['b', 'a', 'c']);
  assert.deepEqual(addHistory(['a'], '  '), ['a']);
  assert.equal(addHistory(Array.from({ length: 20 }, (_, i) => `h${i}`), 'new').length, 12);
});

test('failures in plain words', () => {
  assert.equal(classifyFailure(769, 'Authentication failure (invalid credentials?)', 'rdp', false), 'auth');
  assert.equal(classifyFailure(519, 'DNS lookup failed (incorrect hostname?)', 'rdp', false), 'unreachable');
  assert.equal(classifyFailure(520, 'Server refused connection (wrong security type?)', 'rdp', false), 'cert');
  assert.equal(classifyFailure(520, 'Server refused connection (wrong security type?)', 'rdp', true), 'security');
  assert.equal(classifyFailure(515, 'TLS handshake failed', 'rdp', false), 'cert');
  assert.equal(classifyFailure(520, 'Server refused connection', 'vnc', false), 'unreachable');
  assert.equal(classifyFailure(513, 'engine: could not download', 'rdp', false), 'engine');
  assert.equal(classifyFailure(517, 'closed', 'rdp', true), 'closed');
  assert.equal(classifyFailure(undefined, 'something odd', 'vnc', false), 'other');
});

test('quality and keysyms', () => {
  assert.equal(qualityOf(40, false), 'good');
  assert.equal(qualityOf(150, false), 'fair');
  assert.equal(qualityOf(400, false), 'poor');
  assert.equal(qualityOf(40, true), 'poor');
  assert.equal(qualityOf(null, false), 'unknown');
  assert.equal(keysymFor('a'), 0x61);
  assert.equal(keysymFor('é'), 0xe9);
  assert.equal(keysymFor('\n'), 0xff0d);
  assert.equal(keysymFor('€'), 0x01000000 | 0x20ac);
});
