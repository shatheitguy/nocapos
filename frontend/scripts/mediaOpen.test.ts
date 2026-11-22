// Player windows and the music queue: run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { coverLetter, enqueue, mediaOpen, moveSong, nextIndex, prevIndex, removeSong, shuffleUpcoming, unshuffle, type Queue, type Song } from '../src/lib/mediaOpen.ts';

const f = (path: string, root = 'home'): Song => ({ root, path, name: path.split('/').pop()! });

test('pictures and clips get one window per file', () => {
  const a = mediaOpen('photo', f('/Pics/a.jpg'));
  const b = mediaOpen('photo', f('/Pics/b.jpg'));
  assert.equal(a.appId, 'imageviewer');
  assert.equal(a.key, 'imageviewer:home:/Pics/a.jpg');
  assert.notEqual(a.key, b.key);
  assert.deepEqual(a.props, { root: 'home', path: '/Pics/a.jpg' });
  assert.equal(a.title, 'a.jpg');
  assert.equal(a.newWindow, true);
  // The same file again: the same key, so its window comes forward.
  assert.equal(mediaOpen('photo', f('/Pics/a.jpg')).key, a.key);
  const v = mediaOpen('video', f('/Clips/x.mp4', 'usb'));
  assert.equal(v.appId, 'videoplayer');
  assert.equal(v.key, 'videoplayer:usb:/Clips/x.mp4');
  assert.equal(mediaOpen('photo', f('/a.jpg'), { album: 'fav' }).props.album, 'fav');
  assert.equal(mediaOpen('video', f('/a.mp4'), { album: 'fav' }).props.album, undefined);
});

test('songs go to the one player unless a new window is asked for', () => {
  const s1 = mediaOpen('music', f('/Music/a.mp3'), { now: 1 });
  const s2 = mediaOpen('music', f('/Other/b.mp3'), { now: 2 });
  assert.equal(s1.appId, 'musicplayer');
  assert.equal(s1.key, 'musicplayer');
  assert.equal(s2.key, 'musicplayer');
  assert.equal(s1.newWindow, false);
  // A fresh stamp each time: the same song opened twice plays again.
  assert.notEqual(s1.props.at, mediaOpen('music', f('/Music/a.mp3'), { now: 3 }).props.at);
  const own = mediaOpen('music', f('/Music/a.mp3'), { newWindow: true });
  assert.equal(own.key, 'musicplayer:home:/Music/a.mp3');
  assert.equal(own.newWindow, true);
  assert.equal(own.props.at, undefined);
});

const q3 = (): Queue => ({ songs: [f('/a.mp3'), f('/b.mp3'), f('/c.mp3')], at: 0 });

test('enqueue plays a queued song, or adds it right after the playing one', () => {
  assert.equal(enqueue(q3(), f('/c.mp3')).at, 2);
  const q = enqueue({ ...q3(), at: 1 }, f('/x.mp3', 'usb'));
  assert.deepEqual(q.songs.map((s) => s.path), ['/a.mp3', '/b.mp3', '/x.mp3', '/c.mp3']);
  assert.equal(q.at, 2);
  // The same path on another drive is another song.
  assert.equal(enqueue(q3(), f('/a.mp3', 'usb')).songs.length, 4);
  assert.deepEqual(enqueue({ songs: [], at: -1 }, f('/a.mp3')), { songs: [f('/a.mp3')], at: 0 });
});

test('reordering keeps the playing song playing', () => {
  const q = moveSong({ ...q3(), at: 1 }, 1, 0);
  assert.deepEqual(q.songs.map((s) => s.path), ['/b.mp3', '/a.mp3', '/c.mp3']);
  assert.equal(q.at, 0);
  const r = moveSong({ ...q3(), at: 1 }, 2, 0);
  assert.deepEqual(r.songs.map((s) => s.path), ['/c.mp3', '/a.mp3', '/b.mp3']);
  assert.equal(r.at, 2);
  assert.equal(moveSong(q3(), 0, 99).songs[2].path, '/a.mp3');
  const same = q3();
  assert.equal(moveSong(same, 5, 0), same);
});

test('removing songs', () => {
  assert.deepEqual(removeSong({ ...q3(), at: 2 }, 0), { songs: [f('/b.mp3'), f('/c.mp3')], at: 1 });
  // The playing one: the next takes its place, or the one before at the end.
  assert.equal(removeSong({ ...q3(), at: 1 }, 1).songs[1].path, '/c.mp3');
  assert.equal(removeSong({ ...q3(), at: 1 }, 1).at, 1);
  assert.equal(removeSong({ ...q3(), at: 2 }, 2).at, 1);
  assert.deepEqual(removeSong({ songs: [f('/a.mp3')], at: 0 }, 0), { songs: [], at: -1 });
});

test('next and previous with repeat', () => {
  const end = { ...q3(), at: 2 };
  assert.equal(nextIndex(end, 'off', false), null);
  assert.equal(nextIndex(end, 'all', false), 0);
  assert.equal(nextIndex(end, 'off', true), 0);
  assert.equal(nextIndex({ ...q3(), at: 1 }, 'one', false), 1);
  assert.equal(nextIndex({ ...q3(), at: 1 }, 'one', true), 2);
  assert.equal(prevIndex(q3()), 2);
  assert.equal(prevIndex(end), 1);
  assert.equal(nextIndex({ songs: [], at: -1 }, 'all', true), null);
});

test('shuffle leaves what has played and the playing song alone', () => {
  const base = [f('/a.mp3'), f('/b.mp3'), f('/c.mp3'), f('/d.mp3'), f('/e.mp3')];
  let seed = 7;
  const rnd = () => ((seed = (seed * 16807) % 2147483647) / 2147483647);
  const q = shuffleUpcoming({ songs: base, at: 1 }, rnd);
  assert.deepEqual(q.songs.slice(0, 2), base.slice(0, 2));
  assert.equal(q.at, 1);
  assert.deepEqual([...q.songs].map((s) => s.path).sort(), base.map((s) => s.path));
  const back = unshuffle({ songs: [f('/d.mp3'), f('/a.mp3'), f('/c.mp3'), f('/x.mp3')], at: 0 }, base);
  assert.deepEqual(back.songs.map((s) => s.path), ['/a.mp3', '/c.mp3', '/d.mp3', '/x.mp3']);
  assert.equal(back.at, 2);
});

test('the letter on a song without art', () => {
  assert.equal(coverLetter('night drive'), 'N');
  assert.equal(coverLetter('  99 problems'), '9');
  assert.equal(coverLetter('...'), '♪');
});
