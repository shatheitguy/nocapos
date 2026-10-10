import { useEffect, useRef, useState } from 'react';
import { rdpSocketURL, rdpStatus } from '../api/rdp';
import { vmApi } from '../api/vms';
import { Icon } from '../components/Icon';
import { Guacamole, type GuacamoleTypes } from '../lib/guacamole';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import type { WinState } from '../state/windows';

// A virtual machine's screen in its own window. The machine's VNC display
// listens on 127.0.0.1 only; alfad hands guacd a single-use ticket for it and
// relays the Guacamole protocol over the same WebSocket as Remote Desktop.

type Phase = 'engine' | 'connecting' | 'connected' | 'closed' | 'error' | 'demo';

export function VmScreen({ win }: { win: WinState }) {
  const name = win.props?.vm ?? '';
  const box = useRef<HTMLDivElement>(null);
  const clientRef = useRef<GuacamoleTypes.Client | null>(null);
  const [phase, setPhase] = useState<Phase>('engine');
  const [message, setMessage] = useState('Preparing the screen…');
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const el = box.current;
    if (!el || !name) return;
    let cancelled = false;
    let client: GuacamoleTypes.Client | null = null;
    let keyboard: GuacamoleTypes.Keyboard | null = null;
    let wasConnected = false;
    let ro: ResizeObserver | null = null;

    // VNC screens have the guest's own resolution: scale it to fit the window.
    const fit = () => {
      if (!client) return;
      const d = client.getDisplay();
      const w = d.getWidth();
      const h = d.getHeight();
      if (!w || !h || !el.clientWidth || !el.clientHeight) return;
      d.scale(Math.min(el.clientWidth / w, el.clientHeight / h));
    };

    (async () => {
      setPhase('engine');
      setMessage('Preparing the screen…');
      const width = Math.max(640, Math.round(el.clientWidth));
      const height = Math.max(480, Math.round(el.clientHeight));
      // 1. Demo machines have no screen: just show why.
      const st = await vmApi.status();
      if (cancelled) return;
      if (st.ok && st.data.demo) {
        const d = await vmApi.console(name, width, height);
        if (cancelled) return;
        setPhase(d.ok ? 'demo' : 'error');
        setMessage(d.ok ? (d.data.message ?? 'There’s no screen to show.') : d.error || 'Couldn’t open the screen');
        return;
      }
      // 2. Make sure guacd is up (the first run may install or download it).
      for (;;) {
        const s = await rdpStatus();
        if (cancelled) return;
        if (!s.ok) {
          setPhase('error');
          setMessage(s.error || 'Could not reach NoCapOS');
          return;
        }
        if (s.data.ready) break;
        if (s.data.phase === 'error') {
          setPhase('error');
          setMessage(s.data.message);
          return;
        }
        setMessage(s.data.message);
        await new Promise((r) => setTimeout(r, 1500));
      }
      // A single-use ticket for this machine's screen.
      const t = await vmApi.console(name, width, height);
      if (cancelled) return;
      if (!t.ok || !t.data.ticket) {
        setPhase('error');
        setMessage(t.error || t.data?.message || 'Couldn’t open the screen');
        return;
      }
      const ticket = t.data.ticket;

      // 3. Open the tunnel and wire up display, mouse and keyboard.
      setPhase('connecting');
      setMessage(`Connecting to ${name}…`);
      const tunnel = new Guacamole.WebSocketTunnel(rdpSocketURL());
      client = new Guacamole.Client(tunnel);
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
          setMessage('Connected');
          el.focus();
          fit();
        } else if (state === 5) {
          setPhase((p) => (p === 'error' ? p : 'closed'));
          setMessage((m) => (wasConnected ? 'The screen was disconnected. The machine may have shut down.' : m));
        }
      };
      client.onerror = (status) => {
        if (cancelled) return;
        setPhase('error');
        setMessage(status.message || 'The screen connection failed');
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

      ro = new ResizeObserver(() => fit());
      ro.observe(el);
      client.connect('ticket=' + encodeURIComponent(ticket));
    })();

    return () => {
      cancelled = true;
      ro?.disconnect();
      if (keyboard) {
        keyboard.onkeydown = null;
        keyboard.onkeyup = null;
        keyboard.reset();
      }
      client?.disconnect();
      clientRef.current = null;
      el.replaceChildren();
    };
  }, [name, attempt]);

  const ctrlAltDel = () => {
    const c = clientRef.current;
    if (!c) return;
    const keys = [0xffe3, 0xffe9, 0xffff]; // Control_L, Alt_L, Delete
    keys.forEach((k) => c.sendKeyEvent(1, k));
    [...keys].reverse().forEach((k) => c.sendKeyEvent(0, k));
  };

  const fullscreen = () => box.current?.parentElement?.requestFullscreen?.();

  const forceOff = async () => {
    const ok = await confirmDialog({
      title: `Force ${name} off?`,
      message: 'Like pulling the plug: anything unsaved inside the machine is lost. Use Shut down in Virtual Desk to let it close properly.',
      confirmLabel: 'Force off',
      danger: true,
    });
    if (!ok) return;
    const r = await vmApi.power(name, 'poweroff');
    if (!r.ok) return toast('error', 'Couldn’t turn it off', r.error);
    toast('success', `${name} is off`);
  };

  const busy = phase === 'engine' || phase === 'connecting';

  return (
    <div className="rdp vd-screen">
      <div className="rdp-bar">
        <span className="rdp-title">
          <span className={`status-dot ${phase === 'connected' ? 'on' : 'off'}`} /> {name}
          <span className="muted small"> · {phase === 'demo' ? 'Demo' : message}</span>
        </span>
        <span className="spacer" />
        <button type="button" className="small" onClick={ctrlAltDel} disabled={phase !== 'connected'} title="Send Ctrl+Alt+Del">
          Ctrl+Alt+Del
        </button>
        <button type="button" className="small" onClick={() => setAttempt((a) => a + 1)} title="Reconnect">
          <Icon name="restart" size={15} />
        </button>
        <button type="button" className="small" onClick={fullscreen} title="Full screen">
          <Icon name="maximize" size={14} />
        </button>
        <button type="button" className="small danger" onClick={() => void forceOff()} title="Force off">
          <Icon name="power" size={15} />
        </button>
      </div>
      <div className="rdp-view">
        <div ref={box} className="rdp-display" tabIndex={0} onPointerDown={() => box.current?.focus()} />
        {phase !== 'connected' && (
          <div className="rdp-overlay">
            {busy ? <div className="spinner" /> : <Icon name={phase === 'demo' ? 'vm' : 'desktop'} size={30} />}
            <b>{busy ? message : phase === 'demo' ? 'Demo — no screen' : phase === 'closed' ? 'Disconnected' : 'Couldn’t show the screen'}</b>
            {!busy && <p className="muted small">{message}</p>}
            {!busy && phase !== 'demo' && (
              <div className="rdp-actions">
                <button type="button" onClick={() => setAttempt((a) => a + 1)}>
                  <Icon name="restart" size={14} /> Reconnect
                </button>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
