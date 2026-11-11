import { useEffect, useRef, useState } from 'react';
import { hostAccessApi } from '../api/hostaccess';
import { rdpSocketURL, rdpStatus } from '../api/rdp';
import { Icon } from '../components/Icon';
import { useT } from '../i18n';
import { Guacamole, type GuacamoleTypes } from '../lib/guacamole';

// NoCap Desktop in a window: always exactly what the server's own monitor
// shows, the sign-in screen included. Signing in here signs in on the
// monitor (it's the same sign-in screen), so the window and the monitor are
// one session. alfad attaches a screen server to whatever session the
// monitor has (RDP for Wayland sessions, VNC for X11 and NoCapOS full
// screen, all on 127.0.0.1 only) and hands out a single-use ticket; the
// Remote Desktop engine (guacd) does the rest over the same WebSocket as
// Remote Desktop and Virtual Desk. When the monitor's session changes (a
// sign-in or sign-out), that server stops and this window follows the
// screen to the new session on its own.

type Phase = 'engine' | 'connecting' | 'switching' | 'connected' | 'closed' | 'error';

/** How many times in a row the window follows the screen before giving up. */
const MAX_FOLLOW = 30;
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

export function NocapDesktop() {
  const t = useT();
  const box = useRef<HTMLDivElement>(null);
  const clientRef = useRef<GuacamoleTypes.Client | null>(null);
  const follows = useRef(0);
  const [phase, setPhase] = useState<Phase>('engine');
  const [message, setMessage] = useState('');
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const el = box.current;
    if (!el) return;
    let cancelled = false;
    let client: GuacamoleTypes.Client | null = null;
    let keyboard: GuacamoleTypes.Keyboard | null = null;
    let wasConnected = false;
    let followed = false;
    let ro: ResizeObserver | null = null;
    let resizeTimer: number | undefined;
    let followTimer: number | undefined;

    const fit = () => {
      if (!client) return;
      const d = client.getDisplay();
      const w = d.getWidth();
      const h = d.getHeight();
      if (!w || !h || !el.clientWidth || !el.clientHeight) return;
      d.scale(Math.min(el.clientWidth / w, el.clientHeight / h, 1));
    };

    // The screen's session changed (or is changing): connect again, to
    // whatever the screen shows now.
    const follow = () => {
      if (cancelled || followed) return false;
      if (follows.current >= MAX_FOLLOW) return false;
      followed = true;
      follows.current += 1;
      setPhase('switching');
      setMessage(t('nd.win.switching'));
      followTimer = window.setTimeout(() => setAttempt((a) => a + 1), 1500);
      return true;
    };

    (async () => {
      if (follows.current === 0) {
        setPhase('engine');
        setMessage(t('nd.win.engine'));
      }
      // 1. The Remote Desktop engine (the first run may install it).
      for (;;) {
        const s = await rdpStatus();
        if (cancelled) return;
        if (!s.ok) {
          setPhase('error');
          setMessage(s.error || '');
          return;
        }
        if (s.data.ready) break;
        if (s.data.phase === 'error') {
          setPhase('error');
          setMessage(s.data.message);
          return;
        }
        await sleep(1500);
      }
      // 2. A screen server on whatever the screen shows, and a ticket for it.
      const width = Math.max(640, Math.round(el.clientWidth));
      const height = Math.max(480, Math.round(el.clientHeight));
      let v = await hostAccessApi.view(width, height, Math.round(96 * (window.devicePixelRatio || 1)));
      // 503: the screen is between sessions for a moment (signing in or out).
      while (!cancelled && !v.ok && v.status === 503 && follows.current < MAX_FOLLOW) {
        follows.current += 1;
        setPhase('switching');
        setMessage(t('nd.win.switching'));
        await sleep(2000);
        if (cancelled) return;
        v = await hostAccessApi.view(width, height, Math.round(96 * (window.devicePixelRatio || 1)));
      }
      if (cancelled) return;
      if (!v.ok) {
        setPhase('error');
        setMessage(v.error || '');
        return;
      }

      // 3. Display, mouse and keyboard.
      setPhase('connecting');
      setMessage(t('nd.win.connecting'));
      client = new Guacamole.Client(new Guacamole.WebSocketTunnel(rdpSocketURL()));
      clientRef.current = client;
      const display = client.getDisplay();
      const surface = display.getElement();
      surface.classList.add('rdp-surface');
      el.appendChild(surface);
      display.onresize = fit;
      client.onstatechange = (state: number) => {
        if (cancelled) return;
        if (state === 3) {
          wasConnected = true;
          follows.current = 0;
          setPhase('connected');
          setMessage(t(v.data.sign_in ? 'nd.win.signIn' : 'nd.win.connected'));
          el.focus();
          fit();
        } else if (state === 5) {
          // The screen server stops when the screen's session changes.
          if (wasConnected && follow()) return;
          setPhase((p) => (p === 'error' || p === 'switching' ? p : 'closed'));
          if (wasConnected) setMessage(t('nd.win.closedBody'));
        }
      };
      client.onerror = (status) => {
        if (cancelled) return;
        if (wasConnected && follow()) return;
        if (followed) return;
        setPhase('error');
        setMessage(status.message || '');
      };
      const mouse = new Guacamole.Mouse(surface);
      mouse.onEach(['mousedown', 'mousemove', 'mouseup'], (e) => {
        client?.sendMouseState((e as GuacamoleTypes.Mouse.Event).state, true);
      });
      keyboard = new Guacamole.Keyboard(el);
      keyboard.onkeydown = (keysym) => {
        client?.sendKeyEvent(1, keysym);
        return false;
      };
      keyboard.onkeyup = (keysym) => client?.sendKeyEvent(0, keysym);
      // RDP follows the window's size; VNC is scaled.
      ro = new ResizeObserver(() => {
        fit();
        window.clearTimeout(resizeTimer);
        resizeTimer = window.setTimeout(() => {
          if (v.data.protocol === 'rdp' && el.clientWidth > 0) client?.sendSize(Math.round(el.clientWidth), Math.round(el.clientHeight));
        }, 400);
      });
      ro.observe(el);
      client.connect('ticket=' + encodeURIComponent(v.data.ticket));
    })();

    return () => {
      cancelled = true;
      ro?.disconnect();
      window.clearTimeout(resizeTimer);
      window.clearTimeout(followTimer);
      if (keyboard) {
        keyboard.onkeydown = null;
        keyboard.onkeyup = null;
        keyboard.reset();
      }
      client?.disconnect();
      clientRef.current = null;
      el.replaceChildren();
    };
    // t only changes with the voice; reconnecting for that isn't wanted.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [attempt]);

  const reconnect = () => {
    follows.current = 0;
    setAttempt((a) => a + 1);
  };
  const ctrlAltDel = () => {
    const c = clientRef.current;
    if (!c) return;
    const keys = [0xffe3, 0xffe9, 0xffff];
    keys.forEach((k) => c.sendKeyEvent(1, k));
    [...keys].reverse().forEach((k) => c.sendKeyEvent(0, k));
  };
  const fullscreen = () => box.current?.parentElement?.requestFullscreen?.();
  const busy = phase === 'engine' || phase === 'connecting' || phase === 'switching';

  return (
    <div className="rdp vd-screen">
      <div className="rdp-bar">
        <span className="rdp-title">
          <span className={`status-dot ${phase === 'connected' ? 'on' : 'off'}`} /> {t('nd.win.title')}
          <span className="muted small"> · {message}</span>
        </span>
        <span className="spacer" />
        <button type="button" className="small" onClick={ctrlAltDel} disabled={phase !== 'connected'} title={t('nd.win.cad')}>
          Ctrl+Alt+Del
        </button>
        <button type="button" className="small" onClick={reconnect} title={t('nd.win.reconnect')}>
          <Icon name="restart" size={15} />
        </button>
        <button type="button" className="small" onClick={fullscreen} title={t('nd.win.fullscreen')}>
          <Icon name="maximize" size={14} />
        </button>
      </div>
      <div className="rdp-view">
        <div ref={box} className="rdp-display" tabIndex={0} onPointerDown={() => box.current?.focus()} />
        {phase !== 'connected' && (
          <div className="rdp-overlay">
            {busy ? <div className="spinner" /> : <Icon name="desktop" size={30} />}
            <b>{busy ? message : phase === 'closed' ? t('nd.win.closed') : t('nd.win.error')}</b>
            {!busy && <p className="muted small">{message}</p>}
            {!busy && (
              <div className="rdp-actions">
                <button type="button" onClick={reconnect}>
                  <Icon name="restart" size={14} /> {t('nd.win.reconnect')}
                </button>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
