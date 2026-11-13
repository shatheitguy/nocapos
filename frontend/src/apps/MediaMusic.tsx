import { useEffect, useMemo, useRef, useState, type KeyboardEvent as RKeyboardEvent } from 'react';
import { baseName, fileApi, fileKind, fileTicket, joinPath, parentPath, rawUrl, type FileEntry } from '../api/files';
import { Icon } from '../components/Icon';
import { useT } from '../i18n';
import { id3Size, readId3, type Tags } from '../lib/id3';
import { useWM, type WinState } from '../state/windows';
import { fmtDuration } from './PhotosCommon';

type Repeat = 'off' | 'all' | 'one';

interface Song {
  name: string;
  path: string;
}

/** Reads the tags at the start of a song (two small range requests at most). */
async function fetchTags(url: string): Promise<Tags> {
  try {
    const head = await fetch(url, { headers: { Range: 'bytes=0-9' } });
    if (!head.ok) return {};
    const first = new Uint8Array(await head.arrayBuffer()).subarray(0, 10);
    const size = id3Size(first);
    // Covers can be large; skip tags over 4 MB.
    if (!size || size > 4 << 20) return {};
    const r = await fetch(url, { headers: { Range: `bytes=0-${size - 1}` } });
    if (!r.ok) return {};
    return readId3(new Uint8Array(await r.arrayBuffer()));
  } catch {
    return {};
  }
}

const stem = (n: string) => n.replace(/\.[^.]+$/, '');

/** A random order of 0..n-1 that starts with `first`. */
function shuffled(n: number, first: number): number[] {
  const rest = Array.from({ length: n }, (_, i) => i).filter((i) => i !== first);
  for (let i = rest.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [rest[i], rest[j]] = [rest[j], rest[i]];
  }
  return first >= 0 && first < n ? [first, ...rest] : rest;
}

/**
 * The NoCap music player: the songs in a file's folder as the queue, cover
 * art and names from the file's tags, shuffle, repeat, and it keeps playing
 * while the window is minimized.
 */
export function MusicPlayerApp({ win }: { win: WinState }) {
  const t = useT();
  const root = win.props?.root ?? '';
  const start = win.props?.path ?? '';
  const dir = parentPath(start);
  const audio = useRef<HTMLAudioElement>(null);
  const [songs, setSongs] = useState<Song[] | null>(null);
  const [ticket, setTicket] = useState<string | null>(null);
  const [error, setError] = useState('');
  const [at, setAt] = useState(0);
  const [order, setOrder] = useState<number[]>([]);
  const [shuffle, setShuffle] = useState(false);
  const [repeat, setRepeat] = useState<Repeat>('all');
  const [playing, setPlaying] = useState(false);
  const [time, setTime] = useState(0);
  const [dur, setDur] = useState(0);
  const [volume, setVolume] = useState(() => {
    try {
      return Number(localStorage.getItem('media.volume') ?? 1);
    } catch {
      return 1;
    }
  });
  const [muted, setMuted] = useState(false);
  const [tags, setTags] = useState<Record<string, Tags>>({});
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let live = true;
    void Promise.all([fileApi.list(root, dir), fileTicket(root, dir)]).then(([r, tk]) => {
      if (!live) return;
      if (!r.ok || !tk) return setError(r.error ?? t('media.openFail'));
      const list = r.data.entries
        .filter((e: FileEntry) => !e.dir && fileKind(e.name) === 'audio')
        .sort((a, b) =>
          a.name.localeCompare(b.name, undefined, {
            numeric: true,
            sensitivity: 'base',
          }),
        )
        .map((e) => ({ name: e.name, path: joinPath(dir, e.name) }));
      setTicket(tk);
      setSongs(list);
      const i = Math.max(
        0,
        list.findIndex((s) => s.path === start),
      );
      setAt(i);
      setOrder(list.map((_, j) => j));
    });
    return () => {
      live = false;
    };
  }, [root, dir, start, t]);

  const song = songs?.[at];
  const url = song && ticket ? rawUrl(root, song.path, ticket) : '';

  // Tags for the song on show and the next few in the queue.
  useEffect(() => {
    if (!songs || !ticket) return;
    const pos = order.indexOf(at);
    const want = [at, ...order.slice(pos + 1, pos + 4)].map((i) => songs[i]).filter((s): s is Song => !!s && !(s.path in tags));
    if (!want.length) return;
    let live = true;
    for (const s of want) {
      void fetchTags(rawUrl(root, s.path, ticket)).then((tg) => live && setTags((m) => ({ ...m, [s.path]: tg })));
    }
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [at, songs, ticket, order]);

  const cur = song ? tags[song.path] : undefined;
  const cover = useMemo(
    () =>
      cur?.picture
        ? URL.createObjectURL(
            new Blob([cur.picture.data as BlobPart], {
              type: cur.picture.mime,
            }),
          )
        : null,
    [cur],
  );
  useEffect(
    () => () => {
      if (cover) URL.revokeObjectURL(cover);
    },
    [cover],
  );

  const title = cur?.title || (song ? stem(song.name) : '');
  useEffect(() => {
    const name = title || baseName(start);
    if (win.title !== name)
      useWM.setState((s) => ({
        windows: s.windows.map((w) => (w.id === win.id ? { ...w, title: name } : w)),
      }));
  }, [title, start, win.id, win.title]);

  useEffect(() => {
    const el = audio.current;
    if (!el) return;
    el.volume = Math.max(0, Math.min(1, volume));
    el.muted = muted;
    try {
      localStorage.setItem('media.volume', String(volume));
    } catch {
      /* not saved */
    }
  }, [volume, muted, url]);

  // The device's media keys and lock screen controls.
  useEffect(() => {
    const ms = typeof navigator !== 'undefined' ? navigator.mediaSession : undefined;
    if (!ms || !song) return;
    try {
      ms.metadata = new MediaMetadata({
        title,
        artist: cur?.artist ?? '',
        album: cur?.album ?? '',
        artwork: cover ? [{ src: cover, type: cur?.picture?.mime }] : [],
      });
      ms.setActionHandler('play', () => void audio.current?.play());
      ms.setActionHandler('pause', () => audio.current?.pause());
      ms.setActionHandler('previoustrack', () => prev());
      ms.setActionHandler('nexttrack', () => next(true));
    } catch {
      /* not supported */
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [song, title, cur, cover]);

  const step = (d: 1 | -1, wrap: boolean): number | null => {
    if (!songs?.length) return null;
    const pos = order.indexOf(at);
    const n = pos + d;
    if (n >= 0 && n < order.length) return order[n];
    return wrap ? order[(n + order.length) % order.length] : null;
  };
  const play = (i: number) => {
    setFailed(false);
    setAt(i);
  };
  const next = (byHand = false) => {
    const n = step(1, byHand || repeat === 'all');
    if (n !== null) play(n);
    else setPlaying(false);
  };
  const prev = () => {
    const el = audio.current;
    if (el && el.currentTime > 3) {
      el.currentTime = 0;
      return;
    }
    const p = step(-1, true);
    if (p !== null) play(p);
  };
  const toggle = () => {
    const el = audio.current;
    if (!el) return;
    if (el.paused) void el.play().catch(() => undefined);
    else el.pause();
  };
  const toggleShuffle = () => {
    if (!songs) return;
    const on = !shuffle;
    setShuffle(on);
    setOrder(on ? shuffled(songs.length, at) : songs.map((_, j) => j));
  };

  const onKey = (e: RKeyboardEvent) => {
    if (e.ctrlKey || e.metaKey || e.altKey || e.target instanceof HTMLInputElement) return;
    const el = audio.current;
    if (!el) return;
    const k = e.key;
    if (k === ' ' || k === 'k') toggle();
    else if (k === 'ArrowLeft') el.currentTime = Math.max(0, el.currentTime - 5);
    else if (k === 'ArrowRight') el.currentTime = Math.min(el.duration || 0, el.currentTime + 5);
    else if (k === 'ArrowUp') setVolume((x) => Math.min(1, +(x + 0.1).toFixed(2)));
    else if (k === 'ArrowDown') setVolume((x) => Math.max(0, +(x - 0.1).toFixed(2)));
    else if (k === 'm') setMuted((x) => !x);
    else if (k === 's') toggleShuffle();
    else if (k === 'r') setRepeat((r) => (r === 'off' ? 'all' : r === 'all' ? 'one' : 'off'));
    else if (k === 'N') next(true);
    else if (k === 'P') prev();
    else return;
    e.preventDefault();
  };

  if (error)
    return (
      <div className="media-empty">
        <p>{error}</p>
      </div>
    );
  if (!songs || !ticket)
    return (
      <div className="media-empty">
        <span className="spinner" />
      </div>
    );
  if (!songs.length)
    return (
      <div className="media-empty">
        <p>{t('media.m.noSongs', { name: baseName(start) })}</p>
      </div>
    );

  const queue = order.slice(order.indexOf(at) + 1);
  const repeatLabel = repeat === 'one' ? t('media.m.repeatOne') : repeat === 'all' ? t('media.m.repeatAll') : t('media.m.repeatOff');

  return (
    <div className="mp-app" tabIndex={0} onKeyDown={onKey}>
      <div className="mp-inner">
        <audio
          ref={audio}
          src={url}
          autoPlay
          preload="metadata"
          loop={repeat === 'one'}
          onPlay={() => setPlaying(true)}
          onPause={() => setPlaying(false)}
          onTimeUpdate={(e) => setTime(e.currentTarget.currentTime)}
          onLoadedMetadata={(e) => {
            setDur(e.currentTarget.duration);
            setTime(0);
          }}
          onEnded={() => next()}
          onError={() => setFailed(true)}
        />
        <div className="mp-backdrop" style={cover ? { backgroundImage: `url("${cover}")` } : undefined} aria-hidden />
        <section className="mp-now">
          <div className={`mp-cover${playing ? ' spin' : ''}`}>{cover ? <img src={cover} alt="" /> : <Icon name="music" size={64} />}</div>
          <div className="mp-meta">
            <h2 className="ellipsis" title={title}>
              {title}
            </h2>
            <p className="ellipsis">{[cur?.artist, cur?.album].filter(Boolean).join(' · ') || t('media.m.unknownArtist')}</p>
            {failed && <p className="mp-fail">{t('media.m.cantPlay')}</p>}
          </div>
          <div className="mp-seek">
            <input
              type="range"
              min={0}
              max={dur || 0}
              step={0.1}
              value={time}
              aria-label={t('media.seek')}
              style={{ ['--p' as string]: `${dur ? (time / dur) * 100 : 0}%` }}
              onChange={(e) => {
                if (audio.current) audio.current.currentTime = Number(e.target.value);
              }}
            />
            <span>{fmtDuration(time)}</span>
            <span>{dur ? `-${fmtDuration(Math.max(0, dur - time))}` : '–:––'}</span>
          </div>
          <div className="mp-controls">
            <button type="button" className={`mp-btn${shuffle ? ' on' : ''}`} aria-pressed={shuffle} aria-label={t('media.m.shuffle')} title={`${t('media.m.shuffle')} (S)`} onClick={toggleShuffle}>
              <Icon name="shuffle" size={18} />
            </button>
            <button type="button" className="mp-btn" aria-label={t('media.prev')} title={`${t('media.prev')} (Shift+P)`} onClick={prev}>
              <Icon name="skipBack" size={22} />
            </button>
            <button type="button" className="mp-btn main" aria-label={playing ? t('media.pause') : t('media.play')} title={`${playing ? t('media.pause') : t('media.play')} (K)`} onClick={toggle}>
              <Icon name={playing ? 'pause' : 'play'} size={26} />
            </button>
            <button type="button" className="mp-btn" aria-label={t('media.next')} title={`${t('media.next')} (Shift+N)`} onClick={() => next(true)}>
              <Icon name="skipForward" size={22} />
            </button>
            <button
              type="button"
              className={`mp-btn${repeat !== 'off' ? ' on' : ''}`}
              aria-label={repeatLabel}
              title={`${repeatLabel} (R)`}
              onClick={() => setRepeat((r) => (r === 'off' ? 'all' : r === 'all' ? 'one' : 'off'))}
            >
              <Icon name="repeat" size={18} />
              {repeat === 'one' && <span className="mp-one">1</span>}
            </button>
          </div>
          <div className="mp-vol">
            <button type="button" className="mp-btn" aria-label={muted ? t('media.unmute') : t('media.mute')} onClick={() => setMuted((x) => !x)}>
              <Icon name={muted || volume === 0 ? 'mute' : 'volume'} size={16} />
            </button>
            <input
              type="range"
              min={0}
              max={1}
              step={0.05}
              value={muted ? 0 : volume}
              aria-label={t('media.volume')}
              style={{ ['--p' as string]: `${(muted ? 0 : volume) * 100}%` }}
              onChange={(e) => {
                setVolume(Number(e.target.value));
                setMuted(false);
              }}
            />
          </div>
        </section>
        <aside className="mp-queue" aria-label={t('media.m.upNext')}>
          <h3>{t('media.m.upNext')}</h3>
          {queue.length ? (
            <ol>
              {queue.map((i) => {
                const s = songs[i];
                const tg = tags[s.path];
                return (
                  <li key={s.path}>
                    <button type="button" onClick={() => play(i)}>
                      <Icon name="music" size={14} />
                      <span className="mp-q-text">
                        <span className="ellipsis">{tg?.title || stem(s.name)}</span>
                        {tg?.artist && <small className="ellipsis">{tg.artist}</small>}
                      </span>
                    </button>
                  </li>
                );
              })}
            </ol>
          ) : (
            <p className="mp-q-empty">{repeat === 'all' && songs.length > 1 ? t('media.m.loops') : t('media.m.queueEnd')}</p>
          )}
        </aside>
      </div>
    </div>
  );
}
