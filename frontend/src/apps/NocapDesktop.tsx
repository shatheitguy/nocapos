import { useEffect, useRef, useState } from 'react';
import { hostAccessApi } from '../api/hostaccess';
import { rdpSocketURL, rdpStatus } from '../api/rdp';
import { Icon } from '../components/Icon';
import { useT } from '../i18n';
import { Guacamole, type GuacamoleTypes } from '../lib/guacamole';

// NoCap Desktop in a window: exactly what the server's own monitor shows.
// alfad gets the desktop's remote screen ready (RDP for the full desktop,
// VNC for NoCapOS full screen, both on 127.0.0.1 only) and hands out a
// single-use ticket; the Remote Desktop engine (guacd) does the rest over
// the same WebSocket as Remote Desktop and Virtual Desk.

type Phase = 'engine' | 'connecting' | 'connected' | 'closed' | 'error';

export function NocapDesktop() {
  const t = useT();
  const box = useRef<HTMLDivElement>(null);
  const clientRef = useRef<GuacamoleTypes.Client | null>(null);
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
    let ro: ResizeObserver | null = null;
    let resizeTimer: number | undefined;

    const fit = () => {
      if (!client) return;
      const d = client.getDisplay();
      const w = d.getWidth();
      const h = d.getHeight();
      if (!w || !h || !el.clientWidth || !el.clientHeight) return;
      d.scale(Math.min(el.clientWidth / w, el.clientHeight / h, 1));
    };

    (async () => {
      setPhase('engine');
      setMessage(t('nd.win.engine'));
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
        await new Promise((r) => setTimeout(r, 1500));
      }
      // 2. The desktop's remote screen and a ticket for it.
      const width = Math.max(640, Math.round(el.clientWidth));
      const height = Math.max(480, Math.round(el.clientHeight));
      const v = await hostAccessApi.view(width, height, Math.round(96 * (window.devicePixelRatio || 1)));
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
          setPhase('connected');
          setMessage(t('nd.win.connected'));
          el.focus();
          fit();
        } else if (state === 5) {
          setPhase((p) => (p === 'error' ? p : 'closed'));
          if (wasConnected) setMessage(t('nd.win.closedBody'));
        }
      };
      client.onerror = (status) => {
        if (cancelled) return;
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
      // The full desktop follows the window's size (RDP); VNC is scaled.
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

  const ctrlAltDel = () => {
    const c = clientRef.current;
    if (!c) return;
    const keys = [0xffe3, 0xffe9, 0xffff];
    keys.forEach((k) => c.sendKeyEvent(1, k));
    [...keys].reverse().forEach((k) => c.sendKeyEvent(0, k));
  };
  const fullscreen = () => box.current?.parentElement?.requestFullscreen?.();
  const busy = phase === 'engine' || phase === 'connecting';

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
        <button type="button" className="small" onClick={() => setAttempt((a) => a + 1)} title={t('nd.win.reconnect')}>
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
                <button type="button" onClick={() => setAttempt((a) => a + 1)}>
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
