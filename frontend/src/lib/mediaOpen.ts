// How a picture, clip or song opens: in a window of the matching player app.
// Pure (no DOM, no stores), so it is tested on its own (scripts/mediaOpen.test.ts).

export type MediaHandler = 'photo' | 'video' | 'music';

export interface MediaFile {
  root: string;
  path: string;
  name: string;
}

export interface MediaOpenRequest {
  appId: 'imageviewer' | 'videoplayer' | 'musicplayer';
  title: string;
  /** Windows with the same key are reused: the same file focuses its window. */
  key: string;
  props: Record<string, string>;
  /** NoCap Desktop: a real window of its own instead of reusing the app's one. */
  newWindow: boolean;
}

export interface MediaOpenOptions {
  /** Middle-click or "New Window": a song gets a player of its own. */
  newWindow?: boolean;
  /** Pictures: step through these instead of the file's folder (an album, a search). */
  album?: string;
  /** Changes on every open, so opening the same song again plays it again. */
  now?: number;
}

const APP: Record<MediaHandler, MediaOpenRequest['appId']> = { photo: 'imageviewer', video: 'videoplayer', music: 'musicplayer' };

/** The music player that songs go to unless another window is asked for. */
export const MAIN_PLAYER = 'musicplayer';

/**
 * One window per picture or clip (opening it again brings that window
 * forward; different files open side by side). Songs all go to one player,
 * whose queue they join, unless a new window is asked for.
 */
export function mediaOpen(kind: MediaHandler, f: MediaFile, opts: MediaOpenOptions = {}): MediaOpenRequest {
  const appId = APP[kind];
  const props: Record<string, string> = { root: f.root, path: f.path };
  if (kind === 'music') {
    if (opts.newWindow) return { appId, title: f.name, key: `${appId}:${f.root}:${f.path}`, props, newWindow: true };
    props.at = String(opts.now ?? Date.now());
    return { appId, title: f.name, key: MAIN_PLAYER, props, newWindow: false };
  }
  if (kind === 'photo' && opts.album) props.album = opts.album;
  return { appId, title: f.name, key: `${appId}:${f.root}:${f.path}`, props, newWindow: true };
}

// ---------- the music queue ----------

export interface Song {
  root: string;
  path: string;
  name: string;
}

export interface Queue {
  songs: Song[];
  /** The song playing (index into songs), -1 when empty. */
  at: number;
}

export type Repeat = 'off' | 'all' | 'one';

export const sameSong = (a: Pick<Song, 'root' | 'path'>, b: Pick<Song, 'root' | 'path'>) => a.root === b.root && a.path === b.path;

/** A song opened into a player that's already playing: play it if queued, else queue it next and play it. */
export function enqueue(q: Queue, song: Song): Queue {
  const i = q.songs.findIndex((s) => sameSong(s, song));
  if (i >= 0) return { ...q, at: i };
  const pos = q.at < 0 ? q.songs.length : q.at + 1;
  const songs = [...q.songs.slice(0, pos), song, ...q.songs.slice(pos)];
  return { songs, at: pos };
}

/** Drag a song from one place in the queue to another; the playing song keeps playing. */
export function moveSong(q: Queue, from: number, to: number): Queue {
  if (from === to || from < 0 || from >= q.songs.length) return q;
  const t = Math.max(0, Math.min(q.songs.length - 1, to));
  const songs = [...q.songs];
  const [s] = songs.splice(from, 1);
  songs.splice(t, 0, s);
  const playing = q.songs[q.at];
  return { songs, at: playing ? songs.indexOf(playing) : q.at };
}

/** Take a song out of the queue. Removing the playing one moves on to the next (or the one before, at the end). */
export function removeSong(q: Queue, i: number): Queue {
  if (i < 0 || i >= q.songs.length) return q;
  const songs = q.songs.filter((_, j) => j !== i);
  let at = q.at;
  if (i < q.at) at--;
  else if (i === q.at) at = Math.min(q.at, songs.length - 1);
  return { songs, at: songs.length ? at : -1 };
}

/** The song after this one: null at the end unless repeating (or skipped by hand, which wraps). */
export function nextIndex(q: Queue, repeat: Repeat, byHand: boolean): number | null {
  if (!q.songs.length) return null;
  if (repeat === 'one' && !byHand) return q.at;
  const n = q.at + 1;
  if (n < q.songs.length) return n;
  return repeat === 'all' || byHand ? 0 : null;
}

/** The song before this one (wraps to the end). */
export function prevIndex(q: Queue): number | null {
  if (!q.songs.length) return null;
  return q.at > 0 ? q.at - 1 : q.songs.length - 1;
}

/** Shuffle what's still to come; the playing song stays where it is and keeps playing. */
export function shuffleUpcoming(q: Queue, random: () => number = Math.random): Queue {
  const head = q.songs.slice(0, q.at + 1);
  const rest = q.songs.slice(q.at + 1);
  for (let i = rest.length - 1; i > 0; i--) {
    const j = Math.floor(random() * (i + 1));
    [rest[i], rest[j]] = [rest[j], rest[i]];
  }
  return { songs: [...head, ...rest], at: q.at };
}

/** Back to name order (the order songs were queued in `base`), still on the same song. */
export function unshuffle(q: Queue, base: Song[]): Queue {
  const extra = q.songs.filter((s) => !base.some((b) => sameSong(b, s)));
  const kept = base.filter((b) => q.songs.some((s) => sameSong(s, b)));
  const songs = [...kept, ...extra];
  const playing = q.songs[q.at];
  return { songs, at: playing ? songs.findIndex((s) => sameSong(s, playing)) : -1 };
}

/** "Artist – Title.mp3" → "Artist – Title". */
export const songStem = (n: string) => n.replace(/\.[^.]+$/, '');

/** The letter on a song without cover art. */
export function coverLetter(title: string): string {
  const m = title.match(/[\p{L}\p{N}]/u);
  return m ? m[0].toUpperCase() : '♪';
}
