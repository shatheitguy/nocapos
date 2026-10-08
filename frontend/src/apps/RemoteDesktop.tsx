import { useEffect, useRef, useState } from 'react';
import { rdpSocketURL, rdpStatus, rdpTicket } from '../api/rdp';
import { Icon } from '../components/Icon';
import { Guacamole, type GuacamoleTypes } from '../lib/guacamole';

// Real RDP: alfad relays the Guacamole protocol between this client and guacd,
// which speaks RDP to the target PC. The session lives as long as this window
// does — minimising keeps it mounted, so it stays connected in the background.

const KEY = 'alfa.rdp.hosts';

interface Host {
  id: string;
  name: string;
  hostname: string;
  port: number;
  username: string;
  domain: string;
  security: 'any' | 'nla' | 'tls' | 'rdp';
  ignoreCert: boolean;
}

function loadHosts(): Host[] {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) ?? '[]');
    return Array.isArray(v) ? v : [];
  } catch {
    return [];
  }
}
function saveHosts(list: Host[]) {
  try {
    localStorage.setItem(KEY, JSON.stringify(list));
  } catch {
    /* ignore */
  }
}

const blank = (): Omit<Host, 'id'> => ({
  name: '',
  hostname: '',
  port: 3389,
  username: '',
  domain: '',
  security: 'any',
  ignoreCert: true,
});

export function RemoteDesktop() {
  const [hosts, setHosts] = useState<Host[]>(loadHosts);
  const [editing, setEditing] = useState<(Omit<Host, 'id'> & { id?: string }) | null>(null);
  const [askFor, setAskFor] = useState<Host | null>(null);
  const [session, setSession] = useState<{ host: Host; password: string } | null>(null);

  useEffect(() => saveHosts(hosts), [hosts]);

  const saveHost = (h: Omit<Host, 'id'> & { id?: string }) => {
    const host: Host = { ...h, id: h.id ?? Date.now().toString(36), name: h.name.trim() || h.hostname.trim() };
    setHosts((l) => (h.id ? l.map((x) => (x.id === h.id ? host : x)) : [...l, host]));
    setEditing(null);
    return host;
  };

  if (session) {
    return <RdpSession host={session.host} password={session.password} onClose={() => setSession(null)} />;
  }

  return (
    <div className="rdp rdp-manager">
      <div className="rdp-head">
        <div className="rdp-logo">
          <Icon name="desktop" size={26} />
        </div>
        <div>
          <h2 className="no-margin">Remote Desktop</h2>
          <p className="muted small no-margin">Connect to Windows or Linux PCs over RDP. Sessions stay connected in the background.</p>
        </div>
        <span className="spacer" />
        <button type="button" className="small" onClick={() => setEditing(blank())}>
          <Icon name="plus" size={14} /> New connection
        </button>
      </div>

      {hosts.length === 0 && !editing && (
        <div className="rdp-empty glass">
          <Icon name="desktop" size={30} />
          <b>No saved PCs yet</b>
          <p className="muted small">Add a PC by its IP address or name, e.g. 192.168.1.20. Remote Desktop must be enabled on it.</p>
          <button type="button" onClick={() => setEditing(blank())}>
            <Icon name="plus" size={14} /> Add a PC
          </button>
        </div>
      )}

      {hosts.length > 0 && (
        <div className="rdp-list">
          {hosts.map((h) => (
            <div key={h.id} className="rdp-card">
              <span className="rdp-card-ico">
                <Icon name="desktop" size={18} />
              </span>
              <div className="rdp-card-text">
                <b>{h.name}</b>
                <span className="muted small">
                  {h.username ? `${h.domain ? h.domain + '\\' : ''}${h.username} @ ` : ''}
                  {h.hostname}
                  {h.port !== 3389 ? `:${h.port}` : ''}
                </span>
              </div>
              <button type="button" className="small" onClick={() => setAskFor(h)}>
                Connect
              </button>
              <button type="button" className="icon-btn neutral" title="Edit" onClick={() => setEditing(h)}>
                <Icon name="pencil" size={15} />
              </button>
              <button type="button" className="icon-btn" title="Remove" onClick={() => setHosts((l) => l.filter((x) => x.id !== h.id))}>
                <Icon name="trash" size={15} />
              </button>
            </div>
          ))}
        </div>
      )}

      {editing && <HostForm initial={editing} onCancel={() => setEditing(null)} onSave={(h) => setAskFor(saveHost(h))} />}

      {askFor && (
        <PasswordPrompt
          host={askFor}
          onCancel={() => setAskFor(null)}
          onConnect={(password) => {
            setSession({ host: askFor, password });
            setAskFor(null);
          }}
        />
      )}
    </div>
  );
}

function HostForm({
  initial,
  onCancel,
  onSave,
}: {
  initial: Omit<Host, 'id'> & { id?: string };
  onCancel: () => void;
  onSave: (h: Omit<Host, 'id'> & { id?: string }) => void;
}) {
  const [f, setF] = useState(initial);
  const [more, setMore] = useState(false);
  return (
    <form
      className="rdp-form glass"
      onSubmit={(e) => {
        e.preventDefault();
        if (f.hostname.trim()) onSave({ ...f, hostname: f.hostname.trim() });
      }}
    >
      <b>{initial.id ? 'Edit connection' : 'New connection'}</b>
      <label className="row-field">
        <span className="muted small">PC address</span>
        <input autoFocus required value={f.hostname} placeholder="192.168.1.20" onChange={(e) => setF({ ...f, hostname: e.target.value })} />
      </label>
      <label className="row-field">
        <span className="muted small">User name</span>
        <input value={f.username} placeholder="Administrator" autoComplete="off" onChange={(e) => setF({ ...f, username: e.target.value })} />
      </label>
      <label className="row-field">
        <span className="muted small">Display name (optional)</span>
        <input value={f.name} placeholder="Office PC" onChange={(e) => setF({ ...f, name: e.target.value })} />
      </label>
      <button type="button" className="ghost small rdp-more" onClick={() => setMore((v) => !v)}>
        <Icon name={more ? 'chevronUp' : 'chevronDown'} size={14} /> Advanced
      </button>
      {more && (
        <>
          <div className="rdp-row">
            <label className="row-field">
              <span className="muted small">Port</span>
              <input type="number" min={1} max={65535} value={f.port} onChange={(e) => setF({ ...f, port: Number(e.target.value) || 3389 })} />
            </label>
            <label className="row-field">
              <span className="muted small">Domain</span>
              <input value={f.domain} placeholder="(none)" onChange={(e) => setF({ ...f, domain: e.target.value })} />
            </label>
          </div>
          <label className="row-field">
            <span className="muted small">Security</span>
            <select className="select" value={f.security} onChange={(e) => setF({ ...f, security: e.target.value as Host['security'] })}>
              <option value="any">Automatic</option>
              <option value="nla">NLA (Windows default)</option>
              <option value="tls">TLS</option>
              <option value="rdp">Standard RDP</option>
            </select>
          </label>
          <label className="rdp-check">
            <input type="checkbox" checked={f.ignoreCert} onChange={(e) => setF({ ...f, ignoreCert: e.target.checked })} />
            <span className="small">Trust the PC's self-signed certificate</span>
          </label>
        </>
      )}
      <div className="rdp-actions">
        <button type="button" className="ghost" onClick={onCancel}>
          Cancel
        </button>
        <button type="submit">Save & connect</button>
      </div>
    </form>
  );
}

function PasswordPrompt({ host, onCancel, onConnect }: { host: Host; onCancel: () => void; onConnect: (pw: string) => void }) {
  const [pw, setPw] = useState('');
  return (
    <div className="rdp-modal" onPointerDown={(e) => e.target === e.currentTarget && onCancel()}>
      <form
        className="rdp-form glass"
        onSubmit={(e) => {
          e.preventDefault();
          onConnect(pw);
        }}
      >
        <b>Connect to {host.name}</b>
        <span className="muted small">
          {host.username ? `${host.domain ? host.domain + '\\' : ''}${host.username} @ ` : ''}
          {host.hostname}
        </span>
        <label className="row-field">
          <span className="muted small">Password</span>
          <input autoFocus type="password" value={pw} autoComplete="off" onChange={(e) => setPw(e.target.value)} />
        </label>
        <p className="muted small no-margin">The password is sent once to start the session and is never stored.</p>
        <div className="rdp-actions">
          <button type="button" className="ghost" onClick={onCancel}>
            Cancel
          </button>
          <button type="submit">Connect</button>
        </div>
      </form>
    </div>
  );
}

type Phase = 'engine' | 'connecting' | 'connected' | 'closed' | 'error';

const STATE_LABEL: Record<number, string> = {
  0: 'Idle',
  1: 'Connecting…',
  2: 'Waiting for the PC…',
  3: 'Connected',
  4: 'Disconnecting…',
  5: 'Disconnected',
};

function RdpSession({ host, password, onClose }: { host: Host; password: string; onClose: () => void }) {
  const box = useRef<HTMLDivElement>(null);
  const clientRef = useRef<GuacamoleTypes.Client | null>(null);
  const [phase, setPhase] = useState<Phase>('engine');
  const [message, setMessage] = useState('Preparing the remote desktop engine…');
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const el = box.current;
    if (!el) return;
    let cancelled = false;
    let client: GuacamoleTypes.Client | null = null;
    let keyboard: GuacamoleTypes.Keyboard | null = null;
    let wasConnected = false;
    let ro: ResizeObserver | null = null;

    const fit = () => {
      if (!client) return;
      const d = client.getDisplay();
      const w = d.getWidth();
      const h = d.getHeight();
      if (!w || !h || !el.clientWidth || !el.clientHeight) return;
      d.scale(Math.min(el.clientWidth / w, el.clientHeight / h));
    };

    (async () => {
      // 1. Make sure the engine (guacd) is up — the first run may install or download it.
      setPhase('engine');
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

      // 2. Trade the credentials for a single-use ticket.
      setPhase('connecting');
      setMessage(`Connecting to ${host.hostname}…`);
      const width = Math.max(640, Math.round(el.clientWidth));
      const height = Math.max(480, Math.round(el.clientHeight));
      const t = await rdpTicket({
        hostname: host.hostname,
        port: host.port,
        username: host.username,
        password,
        domain: host.domain,
        security: host.security,
        ignore_cert: host.ignoreCert,
        width,
        height,
        dpi: 96,
      });
      if (cancelled) return;
      if (!t.ok) {
        setPhase('error');
        setMessage(t.error || 'Could not start the session');
        return;
      }

      // 3. Open the Guacamole tunnel and wire up display, mouse and keyboard.
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
          setMessage((m) => (wasConnected ? 'The session was disconnected' : m));
        } else {
          setMessage(STATE_LABEL[state] ?? '');
        }
      };
      client.onerror = (status) => {
        if (cancelled) return;
        setPhase('error');
        setMessage(status.message || 'The remote desktop connection failed');
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

      // Follow the window size; skip while hidden (minimised windows report 0).
      let timer = 0;
      ro = new ResizeObserver(() => {
        window.clearTimeout(timer);
        timer = window.setTimeout(() => {
          if (!client || !el.clientWidth || !el.clientHeight) return;
          client.sendSize(Math.round(el.clientWidth), Math.round(el.clientHeight));
          fit();
        }, 250);
      });
      ro.observe(el);

      client.connect('ticket=' + encodeURIComponent(t.data.ticket));
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
  }, [host, password, attempt]);

  const ctrlAltDel = () => {
    const c = clientRef.current;
    if (!c) return;
    const keys = [0xffe3, 0xffe9, 0xffff]; // Control_L, Alt_L, Delete
    keys.forEach((k) => c.sendKeyEvent(1, k));
    [...keys].reverse().forEach((k) => c.sendKeyEvent(0, k));
  };

  const fullscreen = () => box.current?.parentElement?.requestFullscreen?.();
  const busy = phase === 'engine' || phase === 'connecting';

  return (
    <div className="rdp">
      <div className="rdp-bar">
        <button type="button" className="ghost small" onClick={onClose} title="Back to connections">
          <Icon name="chevronLeft" size={16} /> Connections
        </button>
        <span className="rdp-title">
          <span className={`status-dot ${phase === 'connected' ? 'on' : 'off'}`} /> {host.name}
          <span className="muted small"> · {message}</span>
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
      </div>
      <div className="rdp-view">
        <div ref={box} className="rdp-display" tabIndex={0} onPointerDown={() => box.current?.focus()} />
        {phase !== 'connected' && (
          <div className="rdp-overlay">
            {busy ? <div className="spinner" /> : <Icon name="desktop" size={30} />}
            <b>{busy ? message : phase === 'closed' ? 'Disconnected' : 'Could not connect'}</b>
            {!busy && <p className="muted small">{message}</p>}
            {!busy && (
              <div className="rdp-actions">
                <button type="button" className="ghost" onClick={onClose}>
                  Back
                </button>
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
