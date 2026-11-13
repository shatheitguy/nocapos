// Setup wizard checks: run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { hostnameProblem, isV6, networkProblem, nextOrigin, type NetCheck } from '../src/lib/setupCheck.ts';

test('host names follow RFC 1123', () => {
  for (const ok of ['nocap', 'living-room-nas', 'NAS2', 'a', 'server.home.arpa', 'x'.repeat(63)]) assert.equal(hostnameProblem(ok), '', ok);
  assert.equal(hostnameProblem(''), 'wiz.hostLen');
  assert.equal(hostnameProblem('x'.repeat(254)), 'wiz.hostLen');
  for (const bad of ['-nas', 'nas-', 'my_nas', 'my nas', 'nás', 'a..b', 'x'.repeat(64)]) assert.equal(hostnameProblem(bad), 'wiz.hostChars', bad);
  assert.equal(hostnameProblem('1234'), 'wiz.hostDigits');
  assert.equal(hostnameProblem('10.0.0.1'), 'wiz.hostDigits');
});

test('IPv6 addresses', () => {
  for (const ok of ['::1', 'fe80::1', 'fd00::50', '2001:db8:0:0:0:0:0:1', '::']) assert.ok(isV6(ok), ok);
  for (const bad of ['1.2.3.4', 'fd00:::1', '1:2:3:4:5:6:7:8:9', 'fd00::1::2', 'g::1', '']) assert.ok(!isV6(bad), bad);
});

const net = (p: Partial<NetCheck>): NetCheck => ({ v4: { method: 'auto', dns: [] }, v6mode: 'keep', v6: { dns: [] }, ...p });

test('network settings', () => {
  assert.equal(networkProblem(net({})), '');
  assert.equal(networkProblem(net({ v4: { method: 'manual', address: '192.168.1.50/24', gateway: '192.168.1.1', dns: ['1.1.1.1'] } })), '');
  assert.equal(networkProblem(net({ v4: { method: 'manual', address: '192.168.1.50', dns: [] } })), 'wiz.netBadV4');
  assert.equal(networkProblem(net({ v4: { method: 'manual', address: '192.168.1.300/24', dns: [] } })), 'wiz.netBadV4');
  assert.equal(networkProblem(net({ v4: { method: 'manual', address: '192.168.1.5/24', gateway: 'router', dns: [] } })), 'wiz.netBadGw4');
  assert.equal(networkProblem(net({ v4: { method: 'auto', dns: ['fd00::1'] } })), 'wiz.netBadDns4');
  assert.equal(networkProblem(net({ v6mode: 'manual', v6: { address: 'fd00::50/64', gateway: 'fe80::1', dns: ['fd00::1'] } })), '');
  assert.equal(networkProblem(net({ v6mode: 'manual', v6: { address: 'fd00::50/129', dns: [] } })), 'wiz.netBadV6');
  assert.equal(networkProblem(net({ v6mode: 'auto', v6: { dns: ['1.1.1.1'] } })), 'wiz.netBadDns6');
  assert.equal(networkProblem(net({ v6mode: 'keep', v6: { dns: ['junk'] } })), ''); // not sent
});

test('where the browser goes after a change', () => {
  const at = (hostname: string) => ({ hostname, port: '', protocol: 'https:' });
  const v4 = net({ v4: { method: 'manual', address: '192.168.1.60/24', dns: [] } });
  assert.equal(nextOrigin({ hostname: '', net: v4 }, 'nocap', at('192.168.1.20')), 'https://192.168.1.60');
  assert.equal(nextOrigin({ hostname: '', net: v4 }, 'nocap', at('192.168.1.60')), null);
  assert.equal(nextOrigin({ hostname: '', net: v4 }, 'nocap', at('nocap.local')), null); // the name still works
  assert.equal(nextOrigin({ hostname: 'den', net: null }, 'nocap', at('nocap.local')), 'https://den.local');
  assert.equal(nextOrigin({ hostname: 'den', net: v4 }, 'nocap', at('127.0.0.1')), null); // the server's own screen
  assert.equal(nextOrigin({ hostname: 'den', net: null }, 'nocap', { hostname: '127.0.0.1', port: '8780', protocol: 'http:' }), null);
});
