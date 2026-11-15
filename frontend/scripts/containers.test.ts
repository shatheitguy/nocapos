// Containers app helpers: run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  crumbs,
  deviceFlag,
  groupContainers,
  joinPath,
  levelShown,
  localName,
  logLevel,
  parentPath,
  splitTag,
  sumBy,
  validName,
  validPath,
  visibleRange,
} from '../src/lib/containers.ts';

test('containers are grouped by project, standalone last', () => {
  const groups = groupContainers([
    { id: '1', name: 'web', state: 'running' },
    { id: '2', name: 'shop-db-1', state: 'running', project: 'shop' },
    { id: '3', name: 'shop-api-1', state: 'exited', project: 'shop' },
    { id: '4', name: 'blog-1', state: 'running', project: 'blog' },
    { id: '5', name: 'adminer', state: 'exited' },
  ]);
  assert.deepEqual(groups.map((g) => g.project), ['blog', 'shop', '']);
  assert.deepEqual(groups[1].items.map((c) => c.name), ['shop-api-1', 'shop-db-1']);
  assert.equal(groups[0].state, 'running');
  assert.equal(groups[1].state, 'partial');
  assert.equal(groups[2].running, 1);
  assert.equal(groupContainers([{ id: 'x', name: 'x', state: 'exited', project: 'p' }])[0].state, 'stopped');
  assert.equal(sumBy([{ v: 1 }, { v: undefined }, { v: 2.5 }], (x) => x.v), 3.5);
});

test('log levels and the level filter', () => {
  assert.equal(logLevel('2024-01-01 ERROR could not connect'), 'error');
  assert.equal(logLevel('level=warn msg="slow"'), 'warn');
  assert.equal(logLevel('{"level":"info","msg":"up"}'), 'info');
  assert.equal(logLevel('[debug] cache hit'), 'debug');
  assert.equal(logLevel('Traceback (most recent call last):'), 'error');
  assert.equal(logLevel('listening on :80'), 'other');
  assert.equal(logLevel('listening on :80', 'stderr'), 'warn');
  assert.equal(logLevel('terrorist'), 'other', 'whole words only');
  assert.ok(levelShown('error', 'warn'));
  assert.ok(!levelShown('info', 'warn'));
  assert.ok(!levelShown('debug', 'info'));
  assert.ok(levelShown('other', 'info'));
  assert.ok(levelShown('debug', 'all'));
});

test('paths inside a container', () => {
  assert.equal(joinPath('/', 'etc'), '/etc');
  assert.equal(joinPath('/etc/', 'hosts'), '/etc/hosts');
  assert.equal(parentPath('/etc/ssl/certs'), '/etc/ssl');
  assert.equal(parentPath('/etc'), '/');
  assert.equal(parentPath('/'), '/');
  assert.deepEqual(crumbs('/etc/ssl'), [['/', '/'], ['etc', '/etc'], ['ssl', '/etc/ssl']]);
  assert.ok(validPath('/var/log'));
  assert.ok(!validPath('var/log'));
  assert.ok(!validPath('/etc/../root'));
  assert.ok(!validPath('/a\u0000b'));
  assert.ok(validName('notes.txt'));
  for (const bad of ['', '.', '..', 'a/b', 'a\\b', 'x\ny']) assert.ok(!validName(bad), bad);
});

test('local names from the nocap.domain label', () => {
  assert.equal(localName('api.myapp'), 'api.myapp.nocap.local');
  assert.equal(localName('API.MyApp.nocap.local'), 'api.myapp.nocap.local');
  for (const bad of ['', 'a..b', '-x', 'under_score', '*.x', 'a.b.c.d', 'www', 'nocap.local']) assert.equal(localName(bad), null, bad);
});

test('only the visible rows of a long list are drawn', () => {
  assert.deepEqual(visibleRange(1000, 40, 0, 400, 5), [0, 15]);
  assert.deepEqual(visibleRange(1000, 40, 4000, 400, 5), [95, 115]);
  assert.deepEqual(visibleRange(10, 40, 0, 4000), [0, 10]);
  assert.deepEqual(visibleRange(0, 40, 0, 400), [0, 0]);
});

test('image tags and device lines', () => {
  assert.deepEqual(splitTag('nginx:1.27'), ['nginx', '1.27']);
  assert.deepEqual(splitTag('localhost:5000/app'), ['localhost:5000/app', 'latest']);
  assert.equal(deviceFlag('/dev/ttyUSB0'), '/dev/ttyUSB0');
  assert.equal(deviceFlag('/dev/serial/by-id/usb-x', '/dev/ttyUSB0'), '/dev/serial/by-id/usb-x:/dev/ttyUSB0');
});
