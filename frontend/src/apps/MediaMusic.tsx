import { useEffect, useMemo, useRef, useState, type CSSProperties, type DragEvent, type KeyboardEvent as RKeyboardEvent } from 'react';
import { baseName, fileApi, fileKind, fileTicket, joinPath, parentPath, rawUrl, type FileEntry } from '../api/files';
import { Icon } from '../components/Icon';
import { useT } from '../i18n';
import { id3Size, readId3, type Tags } from '../lib/id3';
import {
  coverLetter,
  enqueue,
  moveSong,
  nextIndex,
  prevIndex,
  removeSong,
  sameSong,
  shuffleUpcoming,
  songStem,
  unshuffle,
  type Queue,
  type Repeat,
  type Song,
} from '../lib/mediaOpen';
import { usePrefs } from '../state/prefs';
import { useWM, type WinState } from '../state/windows';
import { fmtDuration } from './PhotosCommon';

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

const byName = (a: { name: string }, b: { name: string }) => a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: 'base' });
const songKey = (s: Pick<Song, 'root' | 'path'>) => `${s.root}:${s.path}`;
/** Pictures that stand for a folder's album. */
const FOLDER_ART = /^(cover|folder|front|album|albumart)\.(jpe?g|png|webp)$/i;

/** A stable hue for a song without art, so its tile is always the same. */
function hueOf(s: string): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0;
  return h % 360;
}

/** A link to a song's file (tickets are per folder, cached by fileTicket). */
async function songUrl(s: Pick<Song, 'root' | 'path'>): Promise<string | null> {
  const tk = await fileTicket(s.root, parentPath(s.path));
  return tk ? rawUrl(s.root, s.path, tk) : null;
}

/**
 * The live visualizer: the song's sound as bars in a ring around the art.
 * Only for audio from this page's own server (the browser silences other
 * audio routed through Web Audio), only once the audio engine is allowed to
 * run, and never with reduced motion.
 */
function useVisualizer(audio: React.RefObject<HTMLAudioElement | null>, canvas: React.RefObject<HTMLCanvasElement | null>, playing: boolean, enabled: boolean) {
  const graph = useRef<{ ctx: AudioContext; analyser: AnalyserNode } | null>(null);
  const [live, setLive] = useState(false);

  // Build the graph on the first play, once the engine is running.
  useEffect(() => {
    const el = audio.current;
    if (!enabled || !playing || !el || graph.current) return;
    let src = '';
    try {
      src = new URL(el.currentSrc || el.src, location.href).origin;
    } catch {
      return;
    }
    if (src !== location.origin) return;
    const AC = window.AudioContext ?? (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
    if (!AC) return;
    const ctx = new AC();
    void ctx
      .resume()
      .then(() => {
        // Routed through Web Audio only when it can play, or the song goes quiet.
        if (ctx.state !== 'running' || graph.current) return void ctx.close();
        const node = ctx.createMediaElementSource(el);
        const analyser = ctx.createAnalyser();
        analyser.fftSize = 128;
        analyser.smoothingTimeConstant = 0.8;
        node.connect(analyser);
        analyser.connect(ctx.destination);
        graph.current = { ctx, analyser };
        setLive(true);
      })
      .catch(() => void ctx.close());
  }, [audio, playing, enabled]);

  useEffect(() => () => void graph.current?.ctx.close(), []);

  // Draw while playing; paused, the ring rests.
  useEffect(() => {
    const c = canvas.current;
    const g = graph.current;
    if (!c || !g || !live) return;
    const draw2d = c.getContext('2d');
    if (!draw2d) return;
    const bins = new Uint8Array(g.analyser.frequencyBinCount);
    let raf = 0;
    const accent = getComputedStyle(c).getPropertyValue('--mp-ring').trim() || '#fff';
    const frame = () => {
      const dpr = window.devicePixelRatio || 1;
      const w = c.clientWidth;
      if (c.width !== Math.round(w * dpr)) {
        c.width = Math.round(w * dpr);
        c.height = Math.round(w * dpr);
      }
      draw2d.setTransform(dpr, 0, 0, dpr, 0, 0);
      draw2d.clearRect(0, 0, w, w);
      if (playing && enabled) g.analyser.getByteFrequencyData(bins);
      else bins.fill(0);
      // Bars standing out from the art's edges, all the way round: the low
      // notes at the top centre, mirrored left and right.
      const n = 72;
      const cx = w / 2;
      const half = w / 2 / 1.28 + w * 0.012;
      const span = w * 0.1;
      draw2d.lineCap = 'round';
      draw2d.lineWidth = Math.max(2, w / 140);
      draw2d.strokeStyle = accent;
      for (let i = 0; i < n; i++) {
        const k = i <= n / 2 ? i : n - i;
        const b = bins[Math.min(bins.length - 1, Math.floor((k / (n / 2)) * bins.length * 0.75))] / 255;
        const a = (i / n) * Math.PI * 2 - Math.PI / 2;
        const dx = Math.cos(a);
        const dy = Math.sin(a);
        const m = Math.max(Math.abs(dx), Math.abs(dy));
        const x = cx + (dx / m) * half;
        const y = cx + (dy / m) * half;
        const [nx, ny] = Math.abs(dx) > Math.abs(dy) ? [Math.sign(dx), 0] : [0, Math.sign(dy)];
        const len = 2 + b * b * span;
        draw2d.globalAlpha = 0.3 + b * 0.65;
        draw2d.beginPath();
        draw2d.moveTo(x, y);
        draw2d.lineTo(x + nx * len, y + ny * len);
        draw2d.stroke();
      }
      draw2d.globalAlpha = 1;
      if (playing && enabled) raf = requestAnimationFrame(frame);
    };
    frame();
    return () => cancelAnimationFrame(raf);
  }, [canvas, live, playing, enabled]);

  return live;
}

/**
 * The NoCap music player, in a window of its own: big art over a blurred
 * wash of it, a live ring visualizer, a queue you can reorder, and a mini
 * player when the window is small. Songs opened while it plays join its
 * queue. It keeps playing while the window is minimized or moved.
 */
export function MusicPlayerApp({ win }: { win: WinState }) {
  const t = useT();
  const reduceMotion = usePrefs((s) => s.reduceMotion);
  const audio = useRef<HTMLAudioElement>(null);
  const ring = useRef<HTMLCanvasElement>(null);
  const [queue, setQueue] = useState<Queue | null>(null);
  const [base, setBase] = useState<Song[]>([]);
  const [error, setError] = useState('');
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
  const [folderArt, setFolderArt] = useState<Record<string, string>>({});
  const [url, setUrl] = useState('');
  const [failed, setFailed] = useState(false);
  const [showQueue, setShowQueue] = useState(true);
  const [drag, setDrag] = useState<{ from: number; over: number } | null>(null);
  const [osMotion, setOsMotion] = useState(() => typeof matchMedia !== 'undefined' && matchMedia('(prefers-reduced-motion: reduce)').matches);
  useEffect(() => {
    if (typeof matchMedia === 'undefined') return;
    const m = matchMedia('(prefers-reduced-motion: reduce)');
    const on = () => setOsMotion(m.matches);
    m.addEventListener('change', on);
    return () => m.removeEventListener('change', on);
  }, []);
  const calm = reduceMotion || osMotion;

  // The folder of the first song is the queue; later songs join it.
  const root = win.props?.root ?? '';
  const start = win.props?.path ?? '';
  const stamp = win.props?.at ?? '';
  const loaded = useRef(false);
  useEffect(() => {
    if (!start) return;
    const song: Song = { root, path: start, name: baseName(start) };
    if (loaded.current) {
      setQueue((q) => (q ? enqueue(q, song) : q));
      setBase((b) => (b.some((s) => sameSong(s, song)) ? b : [...b, song]));
      setFailed(false);
      return;
    }
    let live = true;
    const dir = parentPath(start);
    void fileApi.list(root, dir).then((r) => {
      if (!live) return;
      if (!r.ok) return setError(r.error ?? t('media.openFail'));
      loaded.current = true;
      const songs = r.data.entries
        .filter((e: FileEntry) => !e.dir && fileKind(e.name) === 'audio')
        .sort(byName)
        .map((e) => ({ root, path: joinPath(dir, e.name), name: e.name }));
      if (!songs.some((s) => s.path === start)) songs.unshift(song);
      setBase(songs);
      setQueue({ songs, at: Math.max(0, songs.findIndex((s) => s.path === start)) });
      const art = r.data.entries.find((e) => !e.dir && FOLDER_ART.test(e.name));
      if (art) void fileTicket(root, dir).then((tk) => live && tk && setFolderArt((m) => ({ ...m, [`${root}:${dir}`]: rawUrl(root, joinPath(dir, art.name), tk) })));
    });
    return () => {
      live = false;
    };
    // `stamp` changes on every open, so the same song opened again plays again.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [root, start, stamp]);

  const song = queue && queue.at >= 0 ? queue.songs[queue.at] : undefined;

  // The playing song's link.
  useEffect(() => {
    if (!song) return setUrl('');
    let live = true;
    void songUrl(song).then((u) => live && setUrl(u ?? ''));
    return () => {
      live = false;
    };
  }, [song]);

  // Tags for the playing song and the next few.
  useEffect(() => {
    if (!queue) return;
    const want = queue.songs.slice(Math.max(0, queue.at), queue.at + 4).filter((s) => !(songKey(s) in tags));
    if (!want.length) return;
    let live = true;
    for (const s of want) {
      void songUrl(s).then((u) => (u ? fetchTags(u) : {})).then((tg) => live && setTags((m) => ({ ...m, [songKey(s)]: tg })));
    }
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [queue]);

  const cur = song ? tags[songKey(song)] : undefined;
  const embedded = useMemo(() => (cur?.picture ? URL.createObjectURL(new Blob([cur.picture.data as BlobPart], { type: cur.picture.mime })) : null), [cur]);
  useEffect(
    () => () => {
      if (embedded) URL.revokeObjectURL(embedded);
    },
    [embedded],
  );
  const cover = embedded ?? (song ? folderArt[`${song.root}:${parentPath(song.path)}`] ?? null : null);
  const title = cur?.title || (song ? songStem(song.name) : '');
  const byline = [cur?.artist, cur?.album].filter(Boolean).join(' · ');

  // The window is named after the song.
  useEffect(() => {
    const name = title || baseName(start);
    if (name && win.title !== name) useWM.setState((s) => ({ windows: s.windows.map((w) => (w.id === win.id ? { ...w, title: name } : w)) }));
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

  const play = (i: number) => {
    setFailed(false);
    setQueue((q) => (q ? { ...q, at: i } : q));
  };
  const next = (byHand = false) => {
    if (!queue) return;
    const n = nextIndex(queue, repeat, byHand);
    if (n === null) setPlaying(false);
    else if (n === queue.at && audio.current) {
      audio.current.currentTime = 0;
      void audio.current.play().catch(() => undefined);
    } else play(n);
  };
  const prev = () => {
    const el = audio.current;
    if (el && el.currentTime > 3) {
      el.currentTime = 0;
      return;
    }
    if (!queue) return;
    const p = prevIndex(queue);
    if (p !== null) play(p);
  };
  const toggle = () => {
    const el = audio.current;
    if (!el) return;
    if (el.paused) void el.play().catch(() => undefined);
    else el.pause();
  };
  const seekBy = (d: number) => {
    const el = audio.current;
    if (el) el.currentTime = Math.max(0, Math.min(el.duration || 0, el.currentTime + d));
  };
  const toggleShuffle = () => {
    if (!queue) return;
    const on = !shuffle;
    setShuffle(on);
    setQueue(on ? shuffleUpcoming(queue) : unshuffle(queue, base));
  };
  const cycleRepeat = () => setRepeat((r) => (r === 'off' ? 'all' : r === 'all' ? 'one' : 'off'));

  // The device's media keys and lock screen.
  const handlers = useRef({ toggle, next, prev, seekBy });
  handlers.current = { toggle, next, prev, seekBy };
  useEffect(() => {
    const ms = typeof navigator !== 'undefined' ? navigator.mediaSession : undefined;
    if (!ms || !song) return;
    try {
      ms.metadata = new MediaMetadata({
        title,
        artist: cur?.artist ?? '',
        album: cur?.album ?? '',
        artwork: cover ? [{ src: cover, ...(embedded && cur?.picture ? { type: cur.picture.mime } : {}) }] : [],
      });
      ms.setActionHandler('play', () => void audio.current?.play());
      ms.setActionHandler('pause', () => audio.current?.pause());
      ms.setActionHandler('previoustrack', () => handlers.current.prev());
      ms.setActionHandler('nexttrack', () => handlers.current.next(true));
      ms.setActionHandler('seekbackward', (d) => handlers.current.seekBy(-(d.seekOffset ?? 10)));
      ms.setActionHandler('seekforward', (d) => handlers.current.seekBy(d.seekOffset ?? 10));
      ms.setActionHandler('seekto', (d) => {
        if (audio.current && d.seekTime !== undefined) audio.current.currentTime = d.seekTime;
      });
    } catch {
      /* not supported */
    }
  }, [song, title, cur, cover, embedded]);
  useEffect(() => {
    const ms = typeof navigator !== 'undefined' ? navigator.mediaSession : undefined;
    if (!ms) return;
    ms.playbackState = playing ? 'playing' : 'paused';
    try {
      if (dur && Number.isFinite(dur)) ms.setPositionState({ duration: dur, position: Math.min(time, dur), playbackRate: 1 });
    } catch {
      /* not supported */
    }
  }, [playing, time, dur]);
  // Closing the player clears what the system shows as playing.
  useEffect(
    () => () => {
      const ms = typeof navigator !== 'undefined' ? navigator.mediaSession : undefined;
      if (ms) {
        ms.metadata = null;
        ms.playbackState = 'none';
      }
    },
    [],
  );

  const visual = useVisualizer(audio, ring, playing, !calm);

  const onKey = (e: RKeyboardEvent) => {
    if (e.ctrlKey || e.metaKey || e.altKey || e.target instanceof HTMLInputElement) return;
    const k = e.key;
    if (k === ' ' || k === 'k') toggle();
    else if (k === 'ArrowLeft') seekBy(-5);
    else if (k === 'ArrowRight') seekBy(5);
    else if (k === 'ArrowUp') setVolume((x) => Math.min(1, +(x + 0.1).toFixed(2)));
    else if (k === 'ArrowDown') setVolume((x) => Math.max(0, +(x - 0.1).toFixed(2)));
    else if (k === 'm') setMuted((x) => !x);
    else if (k === 's') toggleShuffle();
    else if (k === 'r') cycleRepeat();
    else if (k === 'q') setShowQueue((x) => !x);
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
  if (!queue)
    return (
      <div className="media-empty">
        <span className="spinner" />
      </div>
    );
  if (!queue.songs.length)
    return (
      <div className="media-empty">
        <p>{t('media.m.noSongs', { name: baseName(start) })}</p>
      </div>
    );

  const repeatLabel = repeat === 'one' ? t('media.m.repeatOne') : repeat === 'all' ? t('media.m.repeatAll') : t('media.m.repeatOff');
  const hue = hueOf(title || song?.name || '');
  const art = (
    <div className={`mp-art${playing ? ' playing' : ''}`} style={{ '--mp-hue': hue } as CSSProperties}>
      <canvas ref={ring} className="mp-ring" aria-hidden="true" hidden={!visual} />
      <div className="mp-cover">{cover ? <img src={cover} alt="" draggable={false} /> : <span className="mp-letter">{coverLetter(title)}</span>}</div>
    </div>
  );

  const dropAt = (e: DragEvent, i: number) => {
    e.preventDefault();
    if (drag) setQueue((q) => (q ? moveSong(q, drag.from, i) : q));
    setDrag(null);
  };

  return (
    <div className={`mp-app${calm ? ' calm' : ''}`} tabIndex={0} onKeyDown={onKey}>
      <audio
        ref={audio}
        src={url || undefined}
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
        onError={() => url && setFailed(true)}
      />
      <div className={`mp-body${showQueue ? ' with-queue' : ''}`}>
      <div className="mp-wash" aria-hidden="true" style={cover ? { backgroundImage: `url("${cover}")` } : ({ '--mp-hue': hue } as CSSProperties)} />
      <section className="mp-now" aria-label={t('media.m.nowPlaying')}>
        {art}
        <div className="mp-meta">
          <h2 className="ellipsis" title={title}>
            {title}
          </h2>
          <p className="ellipsis">{byline || t('media.m.unknownArtist')}</p>
          {failed && <p className="mp-fail">{t('media.m.cantPlay')}</p>}
        </div>
        <div className="mp-seek">
          <span>{fmtDuration(time)}</span>
          <input
            type="range"
            min={0}
            max={dur || 0}
            step={0.1}
            value={time}
            aria-label={t('media.seek')}
            style={{ '--p': `${dur ? (time / dur) * 100 : 0}%` } as CSSProperties}
            onChange={(e) => {
              if (audio.current) audio.current.currentTime = Number(e.target.value);
            }}
          />
          <span>{dur ? `-${fmtDuration(Math.max(0, dur - time))}` : '–:––'}</span>
        </div>
        <div className="mp-controls">
          <button type="button" className={`mp-btn small${shuffle ? ' on' : ''}`} aria-pressed={shuffle} aria-label={t('media.m.shuffle')} title={`${t('media.m.shuffle')} (S)`} onClick={toggleShuffle}>
            <Icon name="shuffle" size={17} />
          </button>
          <button type="button" className="mp-btn" aria-label={t('media.prev')} title={`${t('media.prev')} (Shift+P)`} onClick={prev}>
            <Icon name="skipBack" size={22} />
          </button>
          <button type="button" className="mp-btn main" aria-label={playing ? t('media.pause') : t('media.play')} title={`${playing ? t('media.pause') : t('media.play')} (Space)`} onClick={toggle}>
            <Icon name={playing ? 'pause' : 'play'} size={28} />
          </button>
          <button type="button" className="mp-btn" aria-label={t('media.next')} title={`${t('media.next')} (Shift+N)`} onClick={() => next(true)}>
            <Icon name="skipForward" size={22} />
          </button>
          <button type="button" className={`mp-btn small${repeat !== 'off' ? ' on' : ''}`} aria-label={repeatLabel} title={`${repeatLabel} (R)`} onClick={cycleRepeat}>
            <Icon name="repeat" size={17} />
            {repeat === 'one' && <span className="mp-one">1</span>}
          </button>
        </div>
        <div className="mp-under">
          <div className="mp-vol">
            <button type="button" className="mp-btn small" aria-label={muted ? t('media.unmute') : t('media.mute')} title={`${muted ? t('media.unmute') : t('media.mute')} (M)`} onClick={() => setMuted((x) => !x)}>
              <Icon name={muted || volume === 0 ? 'mute' : 'volume'} size={16} />
            </button>
            <input
              type="range"
              min={0}
              max={1}
              step={0.05}
              value={muted ? 0 : volume}
              aria-label={t('media.volume')}
              style={{ '--p': `${(muted ? 0 : volume) * 100}%` } as CSSProperties}
              onChange={(e) => {
                setVolume(Number(e.target.value));
                setMuted(false);
              }}
            />
          </div>
          <button type="button" className={`mp-btn small${showQueue ? ' on' : ''}`} aria-pressed={showQueue} aria-label={t('media.m.queue')} title={`${t('media.m.queue')} (Q)`} onClick={() => setShowQueue((x) => !x)}>
            <Icon name="queue" size={17} />
          </button>
        </div>
      </section>
      {showQueue && (
        <aside className="mp-queue" aria-label={t('media.m.queue')}>
          <h3>
            {t('media.m.queue')} <span>{queue.songs.length}</span>
          </h3>
          <ol onDragOver={(e) => drag && e.preventDefault()}>
            {queue.songs.map((s, i) => {
              const tg = tags[songKey(s)];
              const on = i === queue.at;
              return (
                <li
                  key={songKey(s)}
                  className={`${on ? 'on' : ''}${drag && drag.over === i && drag.from !== i ? (drag.from < i ? ' drop-after' : ' drop-before') : ''}${drag?.from === i ? ' dragging' : ''}`}
                  draggable
                  onDragStart={(e) => {
                    e.dataTransfer.effectAllowed = 'move';
                    e.dataTransfer.setData('text/plain', s.name);
                    setDrag({ from: i, over: i });
                  }}
                  onDragEnter={() => drag && setDrag({ ...drag, over: i })}
                  onDragOver={(e) => drag && e.preventDefault()}
                  onDrop={(e) => dropAt(e, i)}
                  onDragEnd={() => setDrag(null)}
                >
                  <button type="button" className="mp-q-song" aria-current={on ? 'true' : undefined} onClick={() => play(i)} title={t('media.m.dragHint')}>
                    <span className="mp-q-n">{on && playing ? <span className="mp-eq" aria-hidden="true"><i /><i /><i /></span> : i + 1}</span>
                    <span className="mp-q-text">
                      <span className="ellipsis">{tg?.title || songStem(s.name)}</span>
                      {tg?.artist && <small className="ellipsis">{tg.artist}</small>}
                    </span>
                  </button>
                  <button type="button" className="mp-q-remove" aria-label={t('media.m.remove')} title={t('media.m.remove')} onClick={() => setQueue((q) => (q ? removeSong(q, i) : q))}>
                    <Icon name="trash" size={13} />
                  </button>
                </li>
              );
            })}
          </ol>
          {queue.at === queue.songs.length - 1 && <p className="mp-q-empty">{repeat === 'all' && queue.songs.length > 1 ? t('media.m.loops') : t('media.m.queueEnd')}</p>}
        </aside>
      )}
      </div>
    </div>
  );
}
