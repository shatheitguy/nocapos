import { useCallback, useEffect, useRef, useState, type KeyboardEvent as RKeyboardEvent, type ReactNode } from 'react';
import { baseName, fileApi, fileKind, fileTicket, joinPath, parentPath, rawUrl, type FileEntry } from '../api/files';
import { Icon } from '../components/Icon';
import { useT } from '../i18n';
import { srtToVtt, subtitlesFor } from '../lib/subtitles';
import { userKey } from '../state/prefs';
import { useWM, type WinState } from '../state/windows';
import { fmtDuration } from './PhotosCommon';

export interface SubtitleTrack {
  label: string;
  lang?: string;
  /** A WebVTT link (a blob made from the file). */
  url: string;
}

const SPEEDS = [0.5, 0.75, 1, 1.25, 1.5, 1.75, 2];

// Where each video was left, per person (a convenience kept in this browser).
const posKey = (k: string) => userKey(`media.pos.${k}`);
function loadPos(k: string): number {
  try {
    return Number(localStorage.getItem(posKey(k))) || 0;
  } catch {
    return 0;
  }
}
function savePos(k: string, s: number) {
  try {
    if (s > 0) localStorage.setItem(posKey(k), String(Math.floor(s)));
    else localStorage.removeItem(posKey(k));
  } catch {
    /* not saved */
  }
}

type AudioTrackList = { length: number; [i: number]: { enabled: boolean; label: string; language: string } };

/**
 * The NoCap video player: play/pause, seek, volume, speed, subtitles, audio
 * tracks (where the browser exposes them), picture-in-picture, full screen,
 * keyboard shortcuts, and it resumes where it was left.
 */
export function VideoPlayer({
  src,
  poster,
  title,
  autoPlay,
  subtitles = [],
  resumeKey,
  onEnded,
  onPrev,
  onNext,
  extra,
}: {
  src: string;
  poster?: string;
  title?: string;
  autoPlay?: boolean;
  subtitles?: SubtitleTrack[];
  /** Remember the position under this key. */
  resumeKey?: string;
  onEnded?: () => void;
  onPrev?: () => void;
  onNext?: () => void;
  /** More buttons for the right of the bar (e.g. the playlist). */
  extra?: ReactNode;
}) {
  const t = useT();
  const box = useRef<HTMLDivElement>(null);
  const v = useRef<HTMLVideoElement>(null);
  const [playing, setPlaying] = useState(false);
  const [time, setTime] = useState(0);
  const [dur, setDur] = useState(0);
  const [buffered, setBuffered] = useState(0);
  const [volume, setVolume] = useState(() => {
    try {
      return Number(localStorage.getItem('media.volume') ?? 1);
    } catch {
      return 1;
    }
  });
  const [muted, setMuted] = useState(false);
  const [speed, setSpeed] = useState(1);
  const [sub, setSub] = useState(-1);
  const [menu, setMenu] = useState<'speed' | 'subs' | 'audio' | null>(null);
  const [audioTracks, setAudioTracks] = useState<{ label: string; on: boolean }[]>([]);
  const [full, setFull] = useState(false);
  const [idle, setIdle] = useState(false);
  const [hover, setHover] = useState<{ x: number; t: number } | null>(null);
  const [flash, setFlash] = useState<string | null>(null);
  const [error, setError] = useState(false);
  const wake = useRef(0);

  // Subtitles: the first one is on by default, matching the video's name.
  useEffect(() => setSub(subtitles.length ? 0 : -1), [subtitles]);
  useEffect(() => {
    const el = v.current;
    if (!el) return;
    for (let i = 0; i < el.textTracks.length; i++) el.textTracks[i].mode = i === sub ? 'showing' : 'disabled';
  }, [sub, subtitles]);

  useEffect(() => {
    const el = v.current;
    if (!el) return;
    el.volume = Math.max(0, Math.min(1, volume));
    el.muted = muted;
    try {
      localStorage.setItem('media.volume', String(volume));
    } catch {
      /* not saved */
    }
  }, [volume, muted]);
  useEffect(() => {
    if (v.current) v.current.playbackRate = speed;
  }, [speed, src]);

  useEffect(() => {
    setError(false);
    setTime(0);
    setDur(0);
  }, [src]);

  // Save the position now and then, and when leaving.
  useEffect(() => {
    if (!resumeKey) return;
    const el = v.current;
    const save = () => el && el.duration && savePos(resumeKey, el.currentTime > 5 && el.currentTime < el.duration - 10 ? el.currentTime : 0);
    const timer = window.setInterval(save, 5000);
    return () => {
      window.clearInterval(timer);
      save();
    };
  }, [resumeKey, src]);

  const poke = useCallback(() => {
    setIdle(false);
    window.clearTimeout(wake.current);
    wake.current = window.setTimeout(() => setIdle(true), 2500);
  }, []);
  useEffect(() => () => window.clearTimeout(wake.current), []);

  useEffect(() => {
    const f = () => setFull(document.fullscreenElement === box.current);
    document.addEventListener('fullscreenchange', f);
    return () => document.removeEventListener('fullscreenchange', f);
  }, []);

  const show = (s: string) => {
    setFlash(s);
    window.setTimeout(() => setFlash((x) => (x === s ? null : x)), 650);
  };
  const toggle = () => {
    const el = v.current;
    if (!el) return;
    if (el.paused) void el.play().catch(() => undefined);
    else el.pause();
  };
  const seekBy = (d: number) => {
    const el = v.current;
    if (!el) return;
    el.currentTime = Math.max(0, Math.min(el.duration || 0, el.currentTime + d));
    show(d > 0 ? `+${d}s` : `${d}s`);
  };
  const toggleFull = () => {
    if (document.fullscreenElement) void document.exitFullscreen();
    else void box.current?.requestFullscreen?.().catch(() => undefined);
  };
  const pip = async () => {
    const el = v.current;
    if (!el) return;
    try {
      if (document.pictureInPictureElement) await document.exitPictureInPicture();
      else await el.requestPictureInPicture();
    } catch {
      /* not allowed here */
    }
  };
  const setAudio = (i: number) => {
    const list = (v.current as unknown as { audioTracks?: AudioTrackList })?.audioTracks;
    if (!list) return;
    for (let j = 0; j < list.length; j++) list[j].enabled = j === i;
    setAudioTracks(Array.from({ length: list.length }, (_, j) => ({ label: list[j].label || list[j].language || `${j + 1}`, on: list[j].enabled })));
  };

  const onKey = (e: RKeyboardEvent) => {
    if (e.ctrlKey || e.metaKey || e.altKey || e.target instanceof HTMLInputElement) return;
    const el = v.current;
    if (!el) return;
    const k = e.key;
    if (k === ' ' || k === 'k') toggle();
    else if (k === 'ArrowLeft') seekBy(-5);
    else if (k === 'ArrowRight') seekBy(5);
    else if (k === 'j') seekBy(-10);
    else if (k === 'l') seekBy(10);
    else if (k === 'ArrowUp') setVolume((x) => Math.min(1, +(x + 0.1).toFixed(2)));
    else if (k === 'ArrowDown') setVolume((x) => Math.max(0, +(x - 0.1).toFixed(2)));
    else if (k === 'm') setMuted((x) => !x);
    else if (k === 'f') toggleFull();
    else if (k === 'p') void pip();
    else if (k === 'c' && subtitles.length) setSub((s) => (s + 1 < subtitles.length ? s + 1 : -1));
    else if (k === '>' || k === '.') setSpeed((s) => SPEEDS[Math.min(SPEEDS.length - 1, SPEEDS.indexOf(s) + 1)] ?? s);
    else if (k === '<' || k === ',') setSpeed((s) => SPEEDS[Math.max(0, SPEEDS.indexOf(s) - 1)] ?? s);
    else if (k === 'N' && onNext) onNext();
    else if (k === 'P' && onPrev) onPrev();
    else if (/^[0-9]$/.test(k) && el.duration) el.currentTime = (Number(k) / 10) * el.duration;
    else return;
    e.preventDefault();
    e.stopPropagation();
    poke();
  };

  const pct = dur ? (time / dur) * 100 : 0;
  const calm = playing && idle && !menu;
  const canPip = typeof document !== 'undefined' && 'pictureInPictureEnabled' in document && document.pictureInPictureEnabled;

  return (
    <div
      ref={box}
      className={`vp${calm ? ' calm' : ''}${full ? ' full' : ''}`}
      tabIndex={0}
      onKeyDown={onKey}
      onPointerMove={poke}
      aria-label={title}
    >
      <video
        ref={v}
        src={src}
        poster={poster}
        autoPlay={autoPlay}
        playsInline
        preload="metadata"
        onClick={toggle}
        onDoubleClick={toggleFull}
        onPlay={() => {
          setPlaying(true);
          poke();
        }}
        onPause={() => setPlaying(false)}
        onTimeUpdate={(e) => setTime(e.currentTarget.currentTime)}
        onProgress={(e) => {
          const b = e.currentTarget.buffered;
          if (b.length) setBuffered(b.end(b.length - 1));
        }}
        onLoadedMetadata={(e) => {
          const el = e.currentTarget;
          setDur(el.duration);
          el.playbackRate = speed;
          const at = resumeKey ? loadPos(resumeKey) : 0;
          if (at > 10 && at < el.duration - 15) {
            el.currentTime = at;
            show(t('media.v.resumed', { time: fmtDuration(at) }));
          }
          const list = (el as unknown as { audioTracks?: AudioTrackList }).audioTracks;
          if (list && list.length > 1) setAudioTracks(Array.from({ length: list.length }, (_, j) => ({ label: list[j].label || list[j].language || `${j + 1}`, on: list[j].enabled })));
          else setAudioTracks([]);
        }}
        onEnded={() => {
          if (resumeKey) savePos(resumeKey, 0);
          onEnded?.();
        }}
        onError={() => setError(true)}
      >
        {subtitles.map((s, i) => (
          <track key={s.url} kind="subtitles" src={s.url} label={s.label} srcLang={s.lang} default={i === 0} />
        ))}
      </video>
      {error && (
        <div className="vp-error">
          <Icon name="alert" size={28} />
          <p>{t('media.v.cantPlay')}</p>
        </div>
      )}
      {flash && <div className="vp-flash">{flash}</div>}
      {!playing && !error && (
        <button type="button" className="vp-big" aria-label={t('media.play')} onClick={toggle}>
          <Icon name="play" size={34} />
        </button>
      )}
      <div className="vp-bar" onPointerDown={(e) => e.stopPropagation()}>
        <div
          className="vp-seek"
          onPointerMove={(e) => {
            const r = e.currentTarget.getBoundingClientRect();
            const f = Math.max(0, Math.min(1, (e.clientX - r.left) / r.width));
            setHover({ x: f * 100, t: f * dur });
          }}
          onPointerLeave={() => setHover(null)}
        >
          <div className="vp-track">
            <span className="vp-buf" style={{ width: `${dur ? (buffered / dur) * 100 : 0}%` }} />
            <span className="vp-done" style={{ width: `${pct}%` }} />
          </div>
          <input
            type="range"
            min={0}
            max={dur || 0}
            step={0.1}
            value={time}
            aria-label={t('media.seek')}
            onChange={(e) => {
              if (v.current) v.current.currentTime = Number(e.target.value);
            }}
          />
          {hover && dur > 0 && (
            <span className="vp-tip" style={{ left: `${hover.x}%` }}>
              {fmtDuration(hover.t)}
            </span>
          )}
        </div>
        <div className="vp-row">
          {onPrev && (
            <button type="button" className="vp-btn" aria-label={t('media.prev')} title={`${t('media.prev')} (Shift+P)`} onClick={onPrev}>
              <Icon name="skipBack" size={17} />
            </button>
          )}
          <button type="button" className="vp-btn main" aria-label={playing ? t('media.pause') : t('media.play')} title={`${playing ? t('media.pause') : t('media.play')} (K)`} onClick={toggle}>
            <Icon name={playing ? 'pause' : 'play'} size={19} />
          </button>
          {onNext && (
            <button type="button" className="vp-btn" aria-label={t('media.next')} title={`${t('media.next')} (Shift+N)`} onClick={onNext}>
              <Icon name="skipForward" size={17} />
            </button>
          )}
          <div className="vp-vol">
            <button type="button" className="vp-btn" aria-label={muted ? t('media.unmute') : t('media.mute')} title={`${muted ? t('media.unmute') : t('media.mute')} (M)`} onClick={() => setMuted((x) => !x)}>
              <Icon name={muted || volume === 0 ? 'mute' : 'volume'} size={17} />
            </button>
            <input type="range" min={0} max={1} step={0.05} value={muted ? 0 : volume} aria-label={t('media.volume')} onChange={(e) => {
              setVolume(Number(e.target.value));
              setMuted(false);
            }} />
          </div>
          <span className="vp-time">
            {fmtDuration(time)} / {dur ? fmtDuration(dur) : '–:––'}
          </span>
          <span className="vp-spacer" />
          {title && <span className="vp-title">{title}</span>}
          <div className="vp-menuwrap">
            <button type="button" className={`vp-btn text${speed !== 1 ? ' on' : ''}`} aria-label={t('media.v.speed')} title={t('media.v.speed')} onClick={() => setMenu((m) => (m === 'speed' ? null : 'speed'))}>
              {speed}×
            </button>
            {menu === 'speed' && (
              <div className="vp-menu" role="menu">
                {SPEEDS.map((s) => (
                  <button key={s} type="button" role="menuitemradio" aria-checked={s === speed} className={s === speed ? 'on' : ''} onClick={() => {
                    setSpeed(s);
                    setMenu(null);
                  }}>
                    {s === 1 ? t('media.v.normal') : `${s}×`}
                  </button>
                ))}
              </div>
            )}
          </div>
          {subtitles.length > 0 && (
            <div className="vp-menuwrap">
              <button type="button" className={`vp-btn${sub >= 0 ? ' on' : ''}`} aria-label={t('media.v.subtitles')} title={`${t('media.v.subtitles')} (C)`} onClick={() => setMenu((m) => (m === 'subs' ? null : 'subs'))}>
                <Icon name="subtitles" size={17} />
              </button>
              {menu === 'subs' && (
                <div className="vp-menu" role="menu">
                  <button type="button" role="menuitemradio" aria-checked={sub < 0} className={sub < 0 ? 'on' : ''} onClick={() => {
                    setSub(-1);
                    setMenu(null);
                  }}>
                    {t('media.v.subsOff')}
                  </button>
                  {subtitles.map((s, i) => (
                    <button key={s.url} type="button" role="menuitemradio" aria-checked={sub === i} className={sub === i ? 'on' : ''} onClick={() => {
                      setSub(i);
                      setMenu(null);
                    }}>
                      {s.label}
                    </button>
                  ))}
                </div>
              )}
            </div>
          )}
          {audioTracks.length > 1 && (
            <div className="vp-menuwrap">
              <button type="button" className="vp-btn" aria-label={t('media.v.audioTrack')} title={t('media.v.audioTrack')} onClick={() => setMenu((m) => (m === 'audio' ? null : 'audio'))}>
                <Icon name="sound" size={17} />
              </button>
              {menu === 'audio' && (
                <div className="vp-menu" role="menu">
                  {audioTracks.map((a, i) => (
                    <button key={i} type="button" role="menuitemradio" aria-checked={a.on} className={a.on ? 'on' : ''} onClick={() => {
                      setAudio(i);
                      setMenu(null);
                    }}>
                      {a.label}
                    </button>
                  ))}
                </div>
              )}
            </div>
          )}
          {canPip && (
            <button type="button" className="vp-btn" aria-label={t('media.v.pip')} title={`${t('media.v.pip')} (P)`} onClick={() => void pip()}>
              <Icon name="pip" size={17} />
            </button>
          )}
          {extra}
          <button type="button" className="vp-btn" aria-label={full ? t('photos.v.exitFull') : t('photos.v.full')} title={`${full ? t('photos.v.exitFull') : t('photos.v.full')} (F)`} onClick={toggleFull}>
            <Icon name={full ? 'fullscreenExit' : 'fullscreen'} size={17} />
          </button>
        </div>
      </div>
    </div>
  );
}

const VIDEO_NAME = (e: FileEntry) => !e.dir && fileKind(e.name) === 'video';

/** The video player window: a file from Files, with the rest of its folder as the playlist. */
export function VideoPlayerApp({ win }: { win: WinState }) {
  const t = useT();
  const root = win.props?.root ?? '';
  const [path, setPath] = useState(win.props?.path ?? '');
  const dir = parentPath(path);
  const [entries, setEntries] = useState<FileEntry[] | null>(null);
  const [ticket, setTicket] = useState<string | null>(null);
  const [subs, setSubs] = useState<SubtitleTrack[]>([]);
  const [list, setList] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    let live = true;
    void Promise.all([fileApi.list(root, dir), fileTicket(root, dir)]).then(([r, tk]) => {
      if (!live) return;
      if (!r.ok || !tk) return setError(r.error ?? t('media.openFail'));
      setTicket(tk);
      setEntries(r.data.entries);
    });
    return () => {
      live = false;
    };
  }, [root, dir, t]);

  const playlist = (entries ?? []).filter(VIDEO_NAME).sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: 'base' }));
  const at = playlist.findIndex((e) => joinPath(dir, e.name) === path);
  const name = baseName(path);

  // Subtitles with the same name (movie.srt, movie.en.vtt) are picked up by themselves.
  useEffect(() => {
    if (!entries) return;
    let live = true;
    const made: string[] = [];
    const found = subtitlesFor(name, entries.filter((e) => !e.dir).map((e) => e.name)).slice(0, 8);
    void Promise.all(
      found.map(async (f): Promise<SubtitleTrack | null> => {
        const r = await fileApi.readText(root, joinPath(dir, f.name));
        if (!r.ok) return null;
        const url = URL.createObjectURL(new Blob([srtToVtt(r.data.content)], { type: 'text/vtt' }));
        made.push(url);
        return { label: f.label, lang: f.lang, url };
      }),
    ).then((tracks) => live && setSubs(tracks.filter((x): x is SubtitleTrack => !!x)));
    return () => {
      live = false;
      made.forEach((u) => URL.revokeObjectURL(u));
    };
  }, [entries, root, dir, name]);

  useEffect(() => {
    if (win.title !== name) useWM.setState((s) => ({ windows: s.windows.map((w) => (w.id === win.id ? { ...w, title: name } : w)) }));
  }, [name, win.id, win.title]);

  const go = (i: number) => playlist[i] && setPath(joinPath(dir, playlist[i].name));

  if (error) return <div className="media-empty"><p>{error}</p></div>;
  if (!ticket) return <div className="media-empty"><span className="spinner" /></div>;

  return (
    <div className={`vp-app${list ? ' with-list' : ''}`}>
      <VideoPlayer
        key={path}
        src={rawUrl(root, path, ticket)}
        title={name}
        autoPlay
        subtitles={subs}
        resumeKey={`${root}:${path}`}
        onEnded={at >= 0 && at < playlist.length - 1 ? () => go(at + 1) : undefined}
        onPrev={at > 0 ? () => go(at - 1) : undefined}
        onNext={at >= 0 && at < playlist.length - 1 ? () => go(at + 1) : undefined}
        extra={
          playlist.length > 1 ? (
            <button type="button" className={`vp-btn${list ? ' on' : ''}`} aria-pressed={list} aria-label={t('media.playlist')} title={t('media.playlist')} onClick={() => setList((x) => !x)}>
              <Icon name="queue" size={17} />
            </button>
          ) : null
        }
      />
      {list && (
        <aside className="vp-list" aria-label={t('media.playlist')}>
          <h3>{t('media.playlist')}</h3>
          <ol>
            {playlist.map((e, i) => (
              <li key={e.name}>
                <button type="button" className={i === at ? 'on' : ''} aria-current={i === at ? 'true' : undefined} onClick={() => go(i)}>
                  <span className="vp-n">{i === at ? <Icon name="play" size={11} /> : i + 1}</span>
                  <span className="ellipsis">{e.name}</span>
                </button>
              </li>
            ))}
          </ol>
        </aside>
      )}
    </div>
  );
}
