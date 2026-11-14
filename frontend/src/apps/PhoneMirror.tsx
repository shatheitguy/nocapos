import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { mirrorApi, mirrorSocketURL, type MirrorLink, type MirrorPhone } from '../api/mirror';
import { Icon, type IconName } from '../components/Icon';
import { useT } from '../i18n';
import { nextDelay } from '../lib/reconnect';
import { h264Supported, PhoneStream, unrotate, type PhoneMessage } from '../lib/phoneStream';
import { toast } from '../state/toasts';

// A linked device's live screen in a frame of its shape (phone, tablet or
// computer; turned sideways when the picture is). Opening it asks the
// device to start; the device asks there first (NoCap OS, then Android's
// own screen-sharing prompt), every time. Click = tap, drag = swipe, wheel =
// scroll, typing types; Back / Home / Recents, volume, rotate the view,
// screenshot and full screen in the bar below.

export type DeviceForm = 'phone' | 'tablet' | 'computer';

type Conn = 'connecting' | 'open' | 'reconnecting' | 'gone';

interface Live {
  online: boolean;
  state: string;
  control: boolean;
  link: MirrorLink;
}

const SPECIAL: Record<string, string> = {
  Enter: 'Enter',
  Backspace: 'Backspace',
  Delete: 'Delete',
  Tab: 'Tab',
  ArrowLeft: 'ArrowLeft',
  ArrowRight: 'ArrowRight',
  ArrowUp: 'ArrowUp',
  ArrowDown: 'ArrowDown',
};

export function Mirror({ phone, form, menu, onGone }: { phone: MirrorPhone; form: DeviceForm; menu?: ReactNode; onGone: () => void }) {
  const t = useT();
  const rootRef = useRef<HTMLDivElement>(null);
  const stageRef = useRef<HTMLDivElement>(null);
  const screenRef = useRef<HTMLDivElement>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const streamRef = useRef<PhoneStream | null>(null);
  const [conn, setConn] = useState<Conn>('connecting');
  const [live, setLive] = useState<Live>({
    online: !!phone.mirror?.online,
    state: phone.mirror?.state ?? '',
    control: phone.mirror?.control ?? true,
    link: phone.mirror?.link ?? '',
  });
  const [pic, setPic] = useState<{ w: number; h: number } | null>(null);
  const [jpeg, setJpeg] = useState(false);
  const [rot, setRot] = useState(0);
  const [stage, setStage] = useState({ w: 0, h: 0 });
  const [latency, setLatency] = useState<number | null>(null);
  const [full, setFull] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const rotRef = useRef(0);
  rotRef.current = rot;

  const send = useCallback((m: Record<string, unknown>) => streamRef.current?.send(m), []);

  // Connect (and reconnect with a fresh ticket, backing off) while open.
  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    let stopped = false;
    let delay = 1000;
    let timer = 0;
    let pinger = 0;
    let everOpen = false;

    const connect = async () => {
      if (stopped) return;
      const wantJpeg = !(await h264Supported());
      if (stopped) return;
      setJpeg(wantJpeg);
      const r = await mirrorApi.ticket(phone.id, wantJpeg);
      if (stopped) return;
      if (!r.ok) {
        if (r.status === 404 || r.status === 403) {
          setConn('gone');
          return;
        }
        setConn(everOpen ? 'reconnecting' : 'connecting');
        delay = nextDelay(delay);
        timer = window.setTimeout(() => void connect(), delay);
        return;
      }
      const s = new PhoneStream(mirrorSocketURL(r.data.ticket), canvas, {
        onOpen: () => {
          everOpen = true;
          delay = 1000;
          setConn('open');
          window.clearInterval(pinger);
          pinger = window.setInterval(() => s.send({ t: 'ping', id: Date.now() }), 10_000);
          s.send({ t: 'ping', id: Date.now() });
        },
        onMessage: (m) => onMessage(m),
        onPicture: (w, h) => setPic({ w, h }),
        onClose: () => {
          window.clearInterval(pinger);
          if (streamRef.current === s) streamRef.current = null;
          if (stopped) return;
          setConn('reconnecting');
          setLatency(null);
          delay = nextDelay(delay);
          timer = window.setTimeout(() => void connect(), delay);
        },
      });
      streamRef.current = s;
    };

    const onMessage = (m: PhoneMessage) => {
      switch (m.t) {
        case 'hello':
          setLive({
            online: !!m.online,
            state: String(m.state ?? ''),
            control: m.control !== false,
            link: (m.link as MirrorLink) ?? '',
          });
          break;
        case 'phone':
          setLive((l) => ({ ...l, online: !!m.online, link: m.link != null ? (m.link as MirrorLink) : l.link, ...(m.online ? {} : { state: '' }) }));
          if (!m.online) setPic(null);
          break;
        case 'state':
          setLive((l) => ({ ...l, state: String(m.state ?? '') }));
          break;
        case 'meta':
          if (typeof m.control === 'boolean') setLive((l) => ({ ...l, control: m.control as boolean }));
          break;
        case 'pong':
          if (typeof m.id === 'number') setLatency(Math.max(0, Date.now() - m.id));
          break;
      }
    };

    setConn('connecting');
    void connect();
    return () => {
      stopped = true;
      window.clearTimeout(timer);
      window.clearInterval(pinger);
      streamRef.current?.close();
      streamRef.current = null;
    };
  }, [phone.id, attempt]);

  // Fit the frame into the window.
  useLayoutEffect(() => {
    const el = stageRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setStage({ w: el.clientWidth, h: el.clientHeight }));
    ro.observe(el);
    setStage({ w: el.clientWidth, h: el.clientHeight });
    return () => ro.disconnect();
  }, []);

  useEffect(() => {
    const f = () => setFull(document.fullscreenElement === rootRef.current);
    document.addEventListener('fullscreenchange', f);
    return () => document.removeEventListener('fullscreenchange', f);
  }, []);

  // ---------- input ----------

  /** A pointer position as a point on the phone's picture (0..1). */
  const point = useCallback((clientX: number, clientY: number): [number, number] => {
    const r = screenRef.current?.getBoundingClientRect();
    if (!r || !r.width || !r.height) return [0.5, 0.5];
    return unrotate((clientX - r.left) / r.width, (clientY - r.top) / r.height, rotRef.current);
  }, []);

  const press = useRef<{ x: number; y: number; cx: number; cy: number; at: number; moved: boolean; done: boolean; id: number } | null>(null);
  const longTimer = useRef(0);

  const onPointerDown = (e: React.PointerEvent) => {
    screenRef.current?.focus({ preventScroll: true });
    if (e.button !== 0) return;
    e.preventDefault();
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    const [x, y] = point(e.clientX, e.clientY);
    press.current = { x, y, cx: e.clientX, cy: e.clientY, at: performance.now(), moved: false, done: false, id: e.pointerId };
    window.clearTimeout(longTimer.current);
    longTimer.current = window.setTimeout(() => {
      const p = press.current;
      if (p && !p.moved && !p.done) {
        p.done = true;
        send({ t: 'long', x: p.x, y: p.y });
      }
    }, 500);
  };
  const onPointerMove = (e: React.PointerEvent) => {
    const p = press.current;
    if (!p || p.id !== e.pointerId) return;
    if (Math.hypot(e.clientX - p.cx, e.clientY - p.cy) >= 8) p.moved = true;
  };
  const onPointerUp = (e: React.PointerEvent) => {
    const p = press.current;
    if (!p || p.id !== e.pointerId) return;
    press.current = null;
    window.clearTimeout(longTimer.current);
    if (p.done) return;
    const dt = performance.now() - p.at;
    const [x2, y2] = point(e.clientX, e.clientY);
    if (!p.moved) send({ t: 'tap', x: p.x, y: p.y });
    else send({ t: 'swipe', x1: p.x, y1: p.y, x2, y2, ms: Math.round(Math.min(3000, Math.max(50, dt))) });
  };
  const onPointerCancel = () => {
    press.current = null;
    window.clearTimeout(longTimer.current);
  };

  // Wheel: summed up and sent every 50 ms (non-passive, so the window doesn't scroll).
  useEffect(() => {
    const el = screenRef.current;
    if (!el) return;
    let dx = 0;
    let dy = 0;
    let at: [number, number] = [0.5, 0.5];
    let timer = 0;
    const flush = () => {
      timer = 0;
      if (Math.abs(dx) < 0.01 && Math.abs(dy) < 0.01) return;
      // The view may be rotated: turn the scroll with it.
      let sx = dx;
      let sy = dy;
      switch (rotRef.current % 4) {
        case 1:
          [sx, sy] = [sy, -sx];
          break;
        case 2:
          [sx, sy] = [-sx, -sy];
          break;
        case 3:
          [sx, sy] = [-sy, sx];
          break;
      }
      streamRef.current?.send({ t: 'scroll', x: at[0], y: at[1], dx: round2(sx), dy: round2(sy) });
      dx = 0;
      dy = 0;
    };
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const unit = e.deltaMode === 1 ? 1 / 3 : e.deltaMode === 2 ? 10 : 1 / 100;
      dx += e.deltaX * unit;
      dy += e.deltaY * unit;
      at = point(e.clientX, e.clientY);
      if (!timer) timer = window.setTimeout(flush, 50);
    };
    el.addEventListener('wheel', onWheel, { passive: false });
    return () => {
      el.removeEventListener('wheel', onWheel);
      window.clearTimeout(timer);
    };
  }, [point]);

  // Typing: printable keys are batched into one "text" message.
  const typed = useRef('');
  const typeTimer = useRef(0);
  const flushText = useCallback(() => {
    typeTimer.current = 0;
    const s = typed.current;
    typed.current = '';
    if (s) send({ t: 'text', s });
  }, [send]);
  const sendText = (s: string) => {
    flushText();
    // Keep each message well under the 8 KB limit.
    for (let i = 0; i < s.length; i += 1000) send({ t: 'text', s: s.slice(i, i + 1000) });
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.ctrlKey || e.metaKey || e.altKey) return; // shortcuts and paste stay the browser's
    if (e.key === 'Escape') {
      e.preventDefault();
      flushText();
      return send({ t: 'global', a: 'back' });
    }
    const k = SPECIAL[e.key];
    if (k) {
      e.preventDefault();
      flushText();
      return send({ t: 'key', k });
    }
    if (e.key.length === 1 || [...e.key].length === 1) {
      e.preventDefault();
      typed.current += e.key;
      if (typed.current.length >= 500) return flushText();
      if (!typeTimer.current) typeTimer.current = window.setTimeout(flushText, 40);
    }
  };
  const onPaste = (e: React.ClipboardEvent) => {
    const s = e.clipboardData.getData('text');
    if (!s) return;
    e.preventDefault();
    sendText(s);
  };
  useEffect(() => () => window.clearTimeout(typeTimer.current), []);

  // ---------- buttons ----------

  const global = (a: string) => send({ t: 'global', a });
  const vol = (d: number) => send({ t: 'vol', d });

  const screenshot = () => {
    const c = canvasRef.current;
    if (!c || !pic) return toast('info', t('phone.shot.none'));
    c.toBlob((blob) => {
      if (!blob) return toast('error', t('phone.shot.failed'));
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      const stamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
      a.href = url;
      a.download = `${phone.name.replace(/[\\/:*?"<>|]+/g, '_') || 'phone'}-screenshot-${stamp}.png`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      window.setTimeout(() => URL.revokeObjectURL(url), 10_000);
      toast('success', t('phone.shot.saved'));
    }, 'image/png');
  };

  const fullscreen = () => {
    if (document.fullscreenElement) void document.exitFullscreen().catch(() => undefined);
    else void rootRef.current?.requestFullscreen?.().catch(() => undefined);
  };

  const askAgain = async () => {
    const r = await mirrorApi.ask(phone.id);
    if (r.ok) {
      setLive((l) => ({ ...l, state: '' }));
      toast('success', t('phone.asked'));
    } else if (r.status === 404) setConn('gone');
    else toast('error', t('phone.askFailed'), r.error);
  };

  // ---------- layout ----------

  // Until the first picture: the shape the device says it is.
  const pw = pic?.w ?? (form === 'phone' ? 1080 : 2560);
  const ph = pic?.h ?? (form === 'phone' ? 2340 : 1600);
  const sideways = rot % 2 === 1;
  const aspect = sideways ? ph / pw : pw / ph; // shown width / height
  const bezel = form === 'computer' ? 8 : form === 'tablet' ? 16 : 12;
  const availW = Math.max(0, stage.w - bezel * 2 - 8);
  const availH = Math.max(0, stage.h - bezel * 2 - 8);
  let vw = availW;
  let vh = availW / aspect;
  if (vh > availH) {
    vh = availH;
    vw = availH * aspect;
  }
  const canvasStyle: React.CSSProperties = {
    width: sideways ? vh : vw,
    height: sideways ? vw : vh,
    transform: `translate(-50%, -50%) rotate(${rot * 90}deg)`,
  };

  // What to say over the screen, if anything.
  let overlay: { icon: IconName | 'spin'; title: string; body?: string; ask?: boolean } | null = null;
  if (conn === 'gone') overlay = { icon: 'alert', title: t('phone.st.gone'), body: t('phone.st.goneBody') };
  else if (conn === 'connecting') overlay = { icon: 'spin', title: t('phone.st.connecting') };
  else if (conn === 'reconnecting') overlay = { icon: 'spin', title: t('phone.st.reconnecting') };
  else if (live.state === 'denied') overlay = { icon: 'lock', title: t('phone.st.denied'), body: t('phone.st.deniedBody'), ask: true };
  else if (live.state === 'unsupported') overlay = { icon: 'alert', title: t('phone.st.unsupported'), body: t('phone.st.unsupportedBody') };
  else if (live.state === 'asking') overlay = { icon: 'phone', title: t('phone.st.asking'), body: t('phone.st.askingBody') };
  else if (!live.online) overlay = { icon: 'phone', title: t('phone.st.waiting'), body: t('phone.st.waitingBody'), ask: true };
  else if (live.state === 'paused') overlay = { icon: 'pause', title: t('phone.st.paused') };
  else if (live.state === 'stopping') overlay = { icon: 'spin', title: t('phone.st.stopping') };
  else if (!pic) overlay = { icon: 'spin', title: t('phone.st.starting') };

  const streaming = conn === 'open' && live.online && !overlay;
  const stateText =
    conn === 'gone'
      ? t('phone.st.gone')
      : conn !== 'open'
        ? t(conn === 'connecting' ? 'phone.st.connecting' : 'phone.st.reconnecting')
        : streaming
          ? t('phone.st.live')
          : live.state === 'asking'
            ? t('phone.st.asking')
            : live.state === 'denied'
              ? t('phone.st.denied')
              : live.online
                ? t('phone.st.starting')
                : t('phone.st.waiting');

  const tools: { icon: IconName; label: string; run: () => void; on?: boolean }[] = [
    { icon: 'chevronLeft', label: t('phone.btn.back'), run: () => global('back') },
    { icon: 'home', label: t('phone.btn.home'), run: () => global('home') },
    { icon: 'layers', label: t('phone.btn.recents'), run: () => global('recents') },
  ];
  const extras: { icon: IconName; label: string; run: () => void; on?: boolean }[] = [
    { icon: 'minimize', label: t('phone.btn.volDown'), run: () => vol(-1) },
    { icon: 'plus', label: t('phone.btn.volUp'), run: () => vol(1) },
    { icon: 'restart', label: t('phone.btn.rotate'), run: () => setRot((r) => (r + 1) % 4), on: rot !== 0 },
    { icon: 'camera', label: t('phone.btn.screenshot'), run: screenshot },
    { icon: full ? 'restore' : 'maximize', label: full ? t('phone.btn.exitFullscreen') : t('phone.btn.fullscreen'), run: fullscreen, on: full },
  ];

  return (
    <div className={`phone-app${full ? ' full' : ''}`} ref={rootRef}>
      <div className="phone-top">
        <span className={`phone-dot${streaming ? ' live' : conn === 'gone' || live.state === 'denied' ? ' bad' : ''}`} />
        <span className="phone-top-text">
          <b title={phone.name}>{phone.name}</b>
          <span className="muted small">
            {stateText}
            {streaming && latency != null ? ` · ${t('phone.latency', { ms: latency })}` : ''}
          </span>
        </span>
        <span className="spacer" />
        {live.link && (
          <span className={`phone-badge ${live.link}`} title={t(live.link === 'nearby' ? 'phone.link.nearbyHint' : 'phone.link.remoteHint')}>
            <Icon name={live.link === 'nearby' ? 'wifi' : 'globe'} size={12} />
            {t(live.link === 'nearby' ? 'phone.link.nearby' : 'phone.link.remote')}
          </span>
        )}
        {jpeg && (
          <span className="phone-badge" title={t('phone.jpegHint')}>
            {t('phone.jpeg')}
          </span>
        )}
        {menu}
      </div>

      {streaming && !live.control && (
        <div className="phone-note">
          <Icon name="info" size={14} />
          <span>{t('phone.controlOff')}</span>
        </div>
      )}

      <div className="phone-stage" ref={stageRef}>
        <div className={`phone-frame form-${form}${aspect > 1 ? ' landscape' : ''}`} style={{ padding: bezel }}>
          <div
            ref={screenRef}
            className="phone-screen"
            style={{ width: vw, height: vh }}
            tabIndex={0}
            role="application"
            aria-label={t('phone.screenLabel')}
            title={streaming ? t('phone.hint') : undefined}
            onPointerDown={onPointerDown}
            onPointerMove={onPointerMove}
            onPointerUp={onPointerUp}
            onPointerCancel={onPointerCancel}
            onContextMenu={(e) => {
              e.preventDefault();
              global('back');
            }}
            onKeyDown={onKeyDown}
            onPaste={onPaste}
          >
            <canvas ref={canvasRef} className={`phone-canvas${pic ? '' : ' empty'}`} style={canvasStyle} />
            {overlay && (
              <div className="phone-overlay" onPointerDown={(e) => e.stopPropagation()}>
                {overlay.icon === 'spin' ? <span className="phone-spin" /> : <Icon name={overlay.icon} size={28} />}
                <b>{overlay.title}</b>
                {overlay.body && <p className="small">{overlay.body}</p>}
                {overlay.ask && (
                  <button type="button" className="small" onClick={() => void askAgain()}>
                    {t('phone.askAgain')}
                  </button>
                )}
                {conn === 'gone' && (
                  <button
                    type="button"
                    className="small"
                    onClick={() => {
                      onGone();
                      setAttempt((a) => a + 1);
                    }}
                  >
                    {t('phone.retry')}
                  </button>
                )}
              </div>
            )}
          </div>
        </div>
      </div>

      <div className="phone-bar" role="toolbar" aria-label={t('phone.toolbar')}>
        <div className="phone-bar-group">
          {tools.map((b) => (
            <button key={b.label} type="button" className="phone-tool" title={b.label} aria-label={b.label} onClick={b.run} disabled={!streaming}>
              <Icon name={b.icon} size={17} />
            </button>
          ))}
        </div>
        <div className="phone-bar-group">
          {extras.map((b) => (
            <button
              key={b.label}
              type="button"
              className={`phone-tool${b.on ? ' on' : ''}`}
              title={b.label}
              aria-label={b.label}
              onClick={b.run}
              disabled={(b.icon === 'minimize' || b.icon === 'plus' || b.icon === 'camera') && !streaming}
            >
              <Icon name={b.icon} size={17} />
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}

const round2 = (n: number) => Math.round(n * 100) / 100;
