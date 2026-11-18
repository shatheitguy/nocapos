// Sticky Notes logic: run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  applyPatch,
  blankNote,
  blocksFromPlain,
  clampDesk,
  displayTitle,
  fromLocalInput,
  isEmptyNote,
  lineShortcut,
  matches,
  mergeNote,
  nextDeskSpot,
  noteBytes,
  normalizeSpans,
  plainText,
  reminderPresets,
  safeHref,
  sameBlocks,
  toLocalInput,
  todoProgress,
  toggleTodo,
  visibleNotes,
  type Block,
  type Note,
} from '../src/apps/notes/noteDoc.ts';

const note = (over: Partial<Note>): Note => ({ ...blankNote(1000), ...over });

test('spans: empty runs go, neighbours with the same marks join, newlines flatten', () => {
  assert.deepEqual(normalizeSpans([{ text: 'a' }, { text: '' , bold: true }, { text: 'b', bold: false }, { text: 'c', bold: true }, { text: 'd\ne', bold: true }]), [
    { text: 'ab' },
    { text: 'cd e', bold: true },
  ]);
  assert.deepEqual(normalizeSpans([{ text: 'x', href: 'https://a.b' }, { text: 'y', href: 'https://c.d' }]).length, 2);
});

test('plain text round trip with bullets and checklists', () => {
  const blocks: Block[] = [
    { type: 'p', spans: [{ text: 'Groceries ' }, { text: 'today', bold: true }] },
    { type: 'li', spans: [{ text: 'milk' }] },
    { type: 'todo', done: true, spans: [{ text: 'eggs' }] },
    { type: 'todo', spans: [{ text: 'bread' }] },
  ];
  const text = plainText(blocks);
  assert.equal(text, 'Groceries today\n- milk\n[x] eggs\n[ ] bread');
  const back = blocksFromPlain(text + '\n\n');
  assert.equal(back.length, 4);
  assert.equal(back[1].type, 'li');
  assert.equal(back[2].done, true);
  assert.equal(plainText(back), text);
  assert.deepEqual(blocksFromPlain(''), [{ type: 'p', spans: [] }]);
});

test('line shortcuts', () => {
  assert.deepEqual(lineShortcut('- milk'), { type: 'li', strip: 2 });
  assert.deepEqual(lineShortcut('* milk'), { type: 'li', strip: 2 });
  assert.deepEqual(lineShortcut('[] milk'), { type: 'todo', done: false, strip: 3 });
  assert.deepEqual(lineShortcut('[ ] milk'), { type: 'todo', done: false, strip: 4 });
  assert.deepEqual(lineShortcut('[x] milk'), { type: 'todo', done: true, strip: 4 });
  assert.equal(lineShortcut('-milk'), null);
  assert.equal(lineShortcut('milk - eggs'), null);
});

test('checklists toggle and count', () => {
  const b: Block[] = [{ type: 'todo', spans: [] }, { type: 'p', spans: [] }, { type: 'todo', done: true, spans: [] }];
  assert.deepEqual(todoProgress(b), { done: 1, total: 2 });
  const t = toggleTodo(b, 0);
  assert.equal(t[0].done, true);
  assert.equal(b[0].done, undefined, 'the original is left alone');
  assert.equal(toggleTodo(b, 1)[1].done, undefined, 'paragraphs have no box');
  assert.ok(sameBlocks(b, [{ type: 'todo', spans: [] }, { type: 'p', spans: [{ text: '' }] }, { type: 'todo', done: true, spans: [] }]));
});

test('links: only web and mail links, bare domains become https', () => {
  assert.equal(safeHref('example.org/x'), 'https://example.org/x');
  assert.equal(safeHref(' https://nocap.local '), 'https://nocap.local');
  assert.equal(safeHref('me@example.org'), 'mailto:me@example.org');
  assert.equal(safeHref('javascript:alert(1)'), null);
  assert.equal(safeHref('data:text/html,hi'), null);
  assert.equal(safeHref('file:///etc/passwd'), null);
  assert.equal(safeHref(''), null);
});

test('views, search and sorting', () => {
  const all = [
    note({ id: 'a', title: 'Café plans', created_at: 1, updated_at: 50 }),
    note({ id: 'b', title: 'Pinned', pinned: true, created_at: 2, updated_at: 10 }),
    note({ id: 'c', title: 'Old stuff', archived: true, updated_at: 30 }),
    note({ id: 'd', title: 'Binned', trashed_at: 99, updated_at: 40 }),
    note({ id: 'e', title: '', text: 'buy MILK and eggs', created_at: 5, updated_at: 20 }),
  ];
  assert.deepEqual(visibleNotes(all, 'notes', '', 'edited').map((n) => n.id), ['b', 'a', 'e']);
  assert.deepEqual(visibleNotes(all, 'notes', '', 'created').map((n) => n.id), ['b', 'e', 'a']);
  assert.deepEqual(visibleNotes(all, 'archive', '', 'edited').map((n) => n.id), ['c']);
  assert.deepEqual(visibleNotes(all, 'trash', '', 'edited').map((n) => n.id), ['d']);
  assert.deepEqual(visibleNotes(all, 'notes', 'cafe', 'edited').map((n) => n.id), ['a']);
  assert.deepEqual(visibleNotes(all, 'notes', 'eggs milk', 'edited').map((n) => n.id), ['e']);
  assert.ok(!matches(all[4], 'milk bread'));
});

test('titles and emptiness', () => {
  assert.ok(isEmptyNote(blankNote(0)));
  assert.equal(displayTitle(note({ body: [{ type: 'p', spans: [] }, { type: 'p', spans: [{ text: '  first line ' }] }] })), 'first line');
  assert.equal(displayTitle(note({ title: 'x'.repeat(100) }), 10), 'xxxxxxxxx…');
  assert.ok(noteBytes(note({ title: 'é' })) > 2);
});

test('a change from elsewhere wins only when newer', () => {
  const list = [note({ id: 'a', title: 'v2', version: 2 })];
  assert.equal(mergeNote(list, note({ id: 'a', title: 'v1', version: 1 }))[0].title, 'v2');
  assert.equal(mergeNote(list, note({ id: 'a', title: 'v3', version: 3 }))[0].title, 'v3');
  assert.equal(mergeNote(list, note({ id: 'b', version: 1 })).length, 2);
});

test('patches apply like the server: moving is not editing, trashing unsticks', () => {
  const n = note({ id: 'a', updated_at: 1, desk: { x: 1, y: 2, w: 200, h: 200 } });
  assert.equal(applyPatch(n, { desk: { x: 5, y: 5, w: 200, h: 200 } }, 9).updated_at, 1);
  assert.equal(applyPatch(n, { pinned: false }, 9).updated_at, 1, 'nothing changed');
  const t = applyPatch(n, { trashed: true }, 9);
  assert.equal(t.trashed_at, 9);
  assert.equal(t.desk, undefined);
  assert.equal(t.updated_at, 9);
  const r = applyPatch({ ...n, reminded: true }, { remind_at: 500 }, 9);
  assert.equal(r.remind_at, 500);
  assert.equal(r.reminded, false);
  assert.equal(applyPatch(r, { remind_at: null }, 9).remind_at, undefined);
  assert.equal(applyPatch(n, { body: [{ type: 'li', spans: [{ text: 'x' }] }] }, 9).text, '- x');
});

test('desktop stickies stay on screen and cascade', () => {
  const area = { w: 1200, h: 800 };
  assert.deepEqual(clampDesk({ x: 5000, y: -10, w: 50, h: 5000 }, area), { x: 1040, y: 0, w: 160, h: 800 });
  const first = nextDeskSpot([], area);
  assert.deepEqual(first, { x: 920, y: 40, w: 240, h: 220 });
  const second = nextDeskSpot([first], area);
  assert.notDeepEqual(second, first);
  assert.ok(second.x < first.x && second.y > first.y);
});

test('reminder presets are in the future and the input format round trips', () => {
  const now = new Date(2026, 10, 17, 21, 15); // a Tuesday evening: no "tonight"
  const p = reminderPresets(now);
  assert.deepEqual(p.map((x) => x.key), ['later', 'tomorrow', 'nextWeek']);
  assert.ok(p.every((x) => x.at > now.getTime()));
  assert.equal(new Date(p[2].at).getDay(), 1, 'next week means Monday');
  const morning = reminderPresets(new Date(2026, 10, 17, 10, 0));
  assert.ok(morning.some((x) => x.key === 'tonight'));
  const ms = new Date(2026, 0, 2, 3, 4).getTime();
  assert.equal(toLocalInput(ms), '2026-01-02T03:04');
  assert.equal(fromLocalInput('2026-01-02T03:04'), ms);
  assert.equal(fromLocalInput('nope'), null);
});
