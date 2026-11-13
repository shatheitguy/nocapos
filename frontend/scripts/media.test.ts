// Subtitles and music tags: run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { srtToVtt, subtitlesFor } from '../src/lib/subtitles.ts';
import { id3Size, readId3 } from '../src/lib/id3.ts';

test('SubRip becomes WebVTT', () => {
  const srt = '﻿1\r\n00:00:01,500 --> 00:00:03,000\r\nHello\r\nthere\r\n\r\n2\r\n00:01:02,003 --> 00:01:04,000\r\n<i>Bye</i>\r\n';
  assert.equal(srtToVtt(srt), 'WEBVTT\n\n00:00:01.500 --> 00:00:03.000\nHello\nthere\n\n00:01:02.003 --> 00:01:04.000\n<i>Bye</i>\n');
  assert.equal(srtToVtt('WEBVTT\n\n00:01.000 --> 00:02.000\nHi'), 'WEBVTT\n\n00:01.000 --> 00:02.000\nHi');
});

test('subtitle files that belong to a video', () => {
  const names = ['Movie.mkv', 'movie.srt', 'Movie.en.srt', 'Movie.Forced.vtt', 'Other.srt', 'Movie.mkv.txt'];
  const subs = subtitlesFor('Movie.mkv', names);
  assert.deepEqual(subs.map((s) => s.name), ['movie.srt', 'Movie.en.srt', 'Movie.Forced.vtt']);
  assert.equal(subs[1].lang, 'en');
  assert.equal(subs[2].label, 'Forced');
  assert.deepEqual(subtitlesFor('a.mp4', ['b.srt']), []);
});

const syncsafe = (n: number) => [(n >> 21) & 0x7f, (n >> 14) & 0x7f, (n >> 7) & 0x7f, n & 0x7f];
function frame(id: string, body: number[]) {
  const n = body.length;
  return [...id].map((c) => c.charCodeAt(0)).concat([(n >> 24) & 255, (n >> 16) & 255, (n >> 8) & 255, n & 255, 0, 0], body);
}
const latin = (s: string) => [...s].map((c) => c.charCodeAt(0));

test('ID3v2.3 title, artist, album and cover', () => {
  const pic = [0xff, 0xd8, 0xff, 0xe0, 1, 2, 3];
  const frames = [
    ...frame('TIT2', [0, ...latin('Night Drive')]),
    // UTF-16 with a byte-order mark.
    ...frame('TPE1', [1, 0xff, 0xfe, ...[...'Zoë'].flatMap((c) => [c.charCodeAt(0), 0]), 0, 0]),
    ...frame('TALB', [3, ...new TextEncoder().encode('Neon'), 0]),
    ...frame('APIC', [0, ...latin('image/jpeg'), 0, 3, ...latin('cover'), 0, ...pic]),
    0, 0, 0, 0, // padding
  ];
  const tag = new Uint8Array([0x49, 0x44, 0x33, 3, 0, 0, ...syncsafe(frames.length), ...frames, 0xff, 0xfb]);
  assert.equal(id3Size(tag), 10 + frames.length);
  const t = readId3(tag);
  assert.equal(t.title, 'Night Drive');
  assert.equal(t.artist, 'Zoë');
  assert.equal(t.album, 'Neon');
  assert.equal(t.picture?.mime, 'image/jpeg');
  assert.deepEqual([...(t.picture?.data ?? [])], pic);
});

test('files without tags, and broken tags, are fine', () => {
  assert.deepEqual(readId3(new Uint8Array([0xff, 0xfb, 0x90, 0x00])), {});
  assert.equal(id3Size(new Uint8Array(4)), 0);
  // A frame claiming to be bigger than the tag stops the reading.
  const bad = new Uint8Array([0x49, 0x44, 0x33, 3, 0, 0, 0, 0, 0, 20, ...frame('TIT2', [0, 65]).slice(0, 4), 0, 0, 9, 9, 0, 0, 0, 65]);
  assert.deepEqual(readId3(bad), {});
});
