import { useCallback, useEffect, useRef, useState } from 'react';
import { rdpSocketURL, rdpStatus, rdpTicket } from '../../api/rdp';
import { Icon } from '../../components/Icon';
import { useT } from '../../i18n';
import { Guacamole, type GuacamoleTypes } from '../../lib/guacamole';
import { useWM, type WinState } from '../../state/windows';
import { CredentialsPrompt, type SessionStart } from './RemoteHome';
import { SPECIAL_KEYS, classifyFailure, keysymFor, optionsFor, qualityOf, type FailKind } from './rdModel';

// One remote session: alfad relays the Guacamole protocol between this client
// and guacd, which speaks RDP or VNC to the computer. The session lives as
// long as this window does; minimising keeps it connected.

type Stage = 'engine' | 'ticket' | 'connecting' | 'waiting' | 'connected' | 'failed' | 'closed';
type Panel = 'keys' | 'clip' | null;

const STAGES: Stage[] = ['engine', 'ticket', 'connecting', 'waiting'];
const BAR_HIDE_MS = 2500;

export function RemoteSession({ start, win, onExit }: { start: SessionStart; win: WinState; onExit: () => void }) {
  const t = useT();
  const root = useRef<HTMLDivElement>(null);
  const box = useRef<HTMLDivElement>(null);
  const typer = useRef<HTMLTextAreaElement>(null);
  const clientRef = useRef<GuacamoleTypes.Client | null>(null);
  const keyboardRef = useRef<GuacamoleTypes.Keyboard | null>(null);

  const { form } = start;
  const opts = optionsFor(form.protocol, form.options);
  const name = form.name.trim() || form.hostname;
  const clipIn = opts.clipboard !== 'off' && opts.clipboard !== 'to-remote'; // computer → here
  const clipOut = opts.clipboard !== 'off' && opts.clipboard !== 'from-remote'; // here → computer

  const [stage, setStage] = useState<Stage>('engine');
  const [engineMsg, setEngineMsg] = useState('');
  const [fail, setFail] = useState<{ kind: FailKind; detail: string } | null>(null);
  const [attempt, setAttempt] = useState(0);
  const [trustCert, setTrustCert] = useState(!!opts.ignore_cert);
  const [password, setPassword] = useState(form.password);
  const [username, setUsername] = useState(form.username);
  const [askPw, setAskPw] = useState(false);

  const [barShown, setBarShown] = useState(true);
  const [pinned, setPinned] = useState(false);
  const [hoverBar, setHoverBar] = useState(false);
  const [panel, setPanel] = useState<Panel>(null);
  const [fullscreen, setFullscreen] = useState(false);
  const [rtt, setRtt] = useState<number | null>(null);
  const [unstable, setUnstable] = useState(false);
  const [clip, setClip] = useState('');
  const hideTimer = useRef(0);

  // ---------- the session ----------
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    let cancelled = false;
    let client: GuacamoleTypes.Client | null = null;
    let keyboard: GuacamoleTypes.Keyboard | null = null;
    let wasConnected = false;
    let ro: ResizeObserver | null = null;
    let failed = false;
    const fixed = opts.size_mode === 'fixed';

    const fit = () => {
      if (!client) return;
      const d = client.getDisplay();
      const w = d.getWidth();
      const h = d.getHeight();
      if (!w || !h || !el.clientWidth || !el.clientHeight) return;
      const s = Math.min(el.clientWidth / w, el.clientHeight / h);
      d.scale(fixed ? Math.min(s, 1) : s);
    };
    const failWith = (code: number | undefined, message: string) => {
      if (cancelled || failed) return;
      failed = true;
      const kind = wasConnected && code !== 769 ? 'closed' : classifyFailure(code, message, form.protocol, trustCert);
      setFail({ kind, detail: message });
      setStage(kind === 'closed' ? 'closed' : 'failed');
    };

    setFail(null);
    (async () => {
      // 1. The engine (guacd): the first run may download or install it.
      setStage('engine');
      for (;;) {
        const s = await rdpStatus();
        if (cancelled) return;
        if (!s.ok) return failWith(undefined, s.error || '');
        if (s.data.ready) break;
        if (s.data.phase === 'error') return failWith(513, `engine: ${s.data.message}`);
        setEngineMsg(s.data.message);
        await new Promise((r) => setTimeout(r, 1500));
      }

      // 2. Trade the details for a single-use ticket (passwords stay server-side).
      setStage('ticket');
      const tk = await rdpTicket({
        ...opts,
        ignore_cert: trustCert,
        protocol: form.protocol,
        hostname: form.hostname,
        port: form.port,
        username,
        password,
        domain: form.protocol === 'rdp' ? form.domain : '',
        connection_id: start.connectionId,
        gateway_password: form.gatewayPassword || undefined,
        width: Math.max(640, Math.round(el.clientWidth || window.innerWidth)),
        height: Math.max(480, Math.round(el.clientHeight || window.innerHeight)),
      });
      if (cancelled) return;
      if (!tk.ok) return failWith(undefined, tk.error || '');

      // 3. The tunnel, the display, mouse, touch and keyboard.
      setStage('connecting');
      const tunnel = new Guacamole.WebSocketTunnel(rdpSocketURL());
      tunnel.onstatechange = (st) => {
        if (cancelled) return;
        setUnstable(st === Guacamole.Tunnel.State.UNSTABLE);
        // Closed without an error first (onerror runs before this when there is one).
        if (st === Guacamole.Tunnel.State.CLOSED) failWith(517, 'closed');
      };
      client = new Guacamole.Client(tunnel);
      clientRef.current = client;
      const display = client.getDisplay();
      const surface = display.getElement();
      surface.classList.add('rdp-surface');
      el.appendChild(surface);
      display.onresize = fit;

      client.onstatechange = (state: number) => {
        if (cancelled) return;
        if (state === 2) setStage('waiting');
        if (state === 3) {
          wasConnected = true;
          setStage('connected');
          el.focus();
          fit();
        } else if (state === 5 && !failed) {
          failWith(wasConnected ? 517 : undefined, 'closed');
        }
      };
      client.onerror = (status) => failWith(status.code, status.message || '');

      if (clipIn) {
        client.onclipboard = (stream, mimetype) => {
          if (!/^text\//.test(mimetype)) return;
          const reader = new Guacamole.StringReader(stream);
          let text = '';
          reader.ontext = (s) => (text += s);
          reader.onend = () => {
            setClip(text);
            void navigator.clipboard?.writeText(text).catch(() => undefined);
          };
        };
      }

      const mouse = new Guacamole.Mouse(surface);
      mouse.onEach(['mousedown', 'mousemove', 'mouseup'], (e) => client?.sendMouseState((e as GuacamoleTypes.Mouse.Event).state, true));
      const touch = new Guacamole.Mouse.Touchscreen(surface);
      touch.onEach(['mousedown', 'mousemove', 'mouseup'], (e) => client?.sendMouseState((e as GuacamoleTypes.Mouse.Event).state, true));

      keyboard = new Guacamole.Keyboard(el);
      keyboardRef.current = keyboard;
      keyboard.onkeydown = (keysym) => {
        client?.sendKeyEvent(1, keysym);
        return false;
      };
      keyboard.onkeyup = (keysym) => client?.sendKeyEvent(0, keysym);

      // Follow the window (dynamic resolution); skip while hidden.
      let timer = 0;
      ro = new ResizeObserver(() => {
        window.clearTimeout(timer);
        timer = window.setTimeout(() => {
          if (!client || !el.clientWidth || !el.clientHeight) return;
          if (!fixed) client.sendSize(Math.round(el.clientWidth), Math.round(el.clientHeight));
          fit();
        }, 200);
      });
      ro.observe(el);

      client.connect('ticket=' + encodeURIComponent(tk.data.ticket));
    })();

    return () => {
      cancelled = true;
      ro?.disconnect();
      if (keyboard) {
        keyboard.onkeydown = null;
        keyboard.onkeyup = null;
        keyboard.reset();
      }
      keyboardRef.current = null;
      client?.disconnect();
      clientRef.current = null;
      el.replaceChildren();
    };
    // Each attempt is one session; its inputs are read when it starts.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [attempt]);

  const connected = stage === 'connected';

  // ---------- full screen ----------
  useEffect(() => {
    const on = () => setFullscreen(document.fullscreenElement === root.current);
    document.addEventListener('fullscreenchange', on);
    return () => document.removeEventListener('fullscreenchange', on);
  }, []);
  // "Full screen" sessions start full screen (when the browser lets them).
  useEffect(() => {
    if (opts.size_mode === 'fullscreen') void root.current?.requestFullscreen?.().catch(() => undefined);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const toggleFullscreen = () => {
    if (document.fullscreenElement) void document.exitFullscreen().catch(() => undefined);
    else void root.current?.requestFullscreen?.().catch(() => undefined);
  };

  // ---------- connection quality ----------
  useEffect(() => {
    if (!connected) return;
    let alive = true;
    const probe = async () => {
      const t0 = performance.now();
      const r = await rdpStatus();
      if (alive) setRtt(r.ok ? performance.now() - t0 : null);
    };
    void probe();
    const id = window.setInterval(probe, 5000);
    return () => {
      alive = false;
      window.clearInterval(id);
    };
  }, [connected]);
  const quality = qualityOf(rtt, unstable);

  // ---------- the connection bar: pinned, or shown on demand ----------
  const reveal = useCallback(() => {
    setBarShown(true);
    window.clearTimeout(hideTimer.current);
    hideTimer.current = window.setTimeout(() => setBarShown(false), BAR_HIDE_MS);
  }, []);
  useEffect(() => {
    if (connected) reveal();
    return () => window.clearTimeout(hideTimer.current);
  }, [connected, reveal]);
  const barVisible = !connected || pinned || barShown || hoverBar || panel !== null;

  // ---------- keys, clipboard, focus ----------
  const sendKeys = (keys: number[]) => {
    const c = clientRef.current;
    if (!c) return;
    keys.forEach((k) => c.sendKeyEvent(1, k));
    [...keys].reverse().forEach((k) => c.sendKeyEvent(0, k));
    box.current?.focus();
  };
  const typeText = (text: string) => {
    const c = clientRef.current;
    if (!c) return;
    for (const ch of text) {
      const k = keysymFor(ch);
      if (k == null) continue;
      c.sendKeyEvent(1, k);
      c.sendKeyEvent(0, k);
    }
  };
  const sendClip = (text: string) => {
    const c = clientRef.current;
    if (!c || !clipOut) return;
    const w = new Guacamole.StringWriter(c.createClipboardStream('text/plain'));
    w.sendText(text);
    w.sendEnd();
  };
  // Back in the session: offer this device's clipboard (when allowed without asking).
  const syncLocalClip = async () => {
    if (!clipOut || !navigator.clipboard?.readText) return;
    try {
      const p = await navigator.permissions?.query({ name: 'clipboard-read' as PermissionName });
      if (p && p.state !== 'granted') return;
      const text = await navigator.clipboard.readText();
      if (text && text !== clip) {
        setClip(text);
        sendClip(text);
      }
    } catch {
      /* not allowed here */
    }
  };

  const disconnect = () => {
    if (document.fullscreenElement) void document.exitFullscreen().catch(() => undefined);
    clientRef.current?.disconnect();
    onExit();
  };
  const retry = (p?: Partial<{ trust: boolean; password: string; username: string }>) => {
    if (p?.trust !== undefined) setTrustCert(p.trust);
    if (p?.password !== undefined) setPassword(p.password);
    if (p?.username !== undefined) setUsername(p.username);
    setPanel(null);
    setAttempt((a) => a + 1);
  };

  const stageIndex = STAGES.indexOf(stage);
  const busy = stageIndex >= 0;

  return (
    <div
      ref={root}
      className={`rd-session${fullscreen ? ' full' : ''}`}
      onPointerMove={(e) => {
        if (!connected || pinned) return;
        const top = (root.current?.getBoundingClientRect().top ?? 0) + 36;
        if (e.clientY < top) reveal();
      }}
    >
      <div
        ref={box}
        className="rdp-display rd-display"
        tabIndex={0}
        aria-label={t('rd.session.display', { name })}
        onPointerDown={() => {
          setPanel(null);
          box.current?.focus();
        }}
        onFocus={() => void syncLocalClip()}
        onBlur={() => keyboardRef.current?.reset()}
      />

      {connected && !barVisible && (
        <button type="button" className="rd-bar-handle" aria-label={t('rd.bar.show')} title={t('rd.bar.show')} onClick={reveal} onPointerEnter={reveal}>
          <Icon name="chevronDown" size={14} />
        </button>
      )}

      {connected && (
        <div className={`rd-bar${barVisible ? '' : ' hidden'}`} role="toolbar" aria-label={t('rd.bar.label')} onPointerEnter={() => setHoverBar(true)} onPointerLeave={() => setHoverBar(false)}>
          <button type="button" className={`rd-bar-btn${pinned ? ' on' : ''}`} aria-pressed={pinned} title={pinned ? t('rd.bar.unpin') : t('rd.bar.pin')} aria-label={pinned ? t('rd.bar.unpin') : t('rd.bar.pin')} onClick={() => setPinned((v) => !v)}>
            <Icon name="pin" size={14} />
          </button>
          <span className={`rd-quality ${quality}`} title={t(`rd.quality.${quality}`) + (rtt != null ? ` · ${Math.round(rtt)} ms` : '')}>
            <i />
            <i />
            <i />
          </span>
          <b className="rd-bar-name" title={`${name} (${form.hostname})`}>
            {name}
          </b>
          <span className="rd-bar-sep" />
          <button type="button" className="rd-bar-btn wide" title={t('rd.keys.ctrlAltDel')} onClick={() => sendKeys(SPECIAL_KEYS[0].keys)}>
            <span className="rd-label">Ctrl+Alt+Del</span>
            <Icon name="key" size={14} className="rd-icon-only" />
          </button>
          <button type="button" className={`rd-bar-btn${panel === 'keys' ? ' on' : ''}`} title={t('rd.bar.keyboard')} aria-label={t('rd.bar.keyboard')} aria-expanded={panel === 'keys'} onClick={() => setPanel((p) => (p === 'keys' ? null : 'keys'))}>
            <Icon name="grid" size={15} />
          </button>
          {(clipIn || clipOut) && (
            <button type="button" className={`rd-bar-btn${panel === 'clip' ? ' on' : ''}`} title={t('rd.bar.clipboard')} aria-label={t('rd.bar.clipboard')} aria-expanded={panel === 'clip'} onClick={() => setPanel((p) => (p === 'clip' ? null : 'clip'))}>
              <Icon name="clipboard" size={15} />
            </button>
          )}
          <button type="button" className="rd-bar-btn" title={fullscreen ? t('rd.bar.exitFull') : t('rd.bar.full')} aria-label={fullscreen ? t('rd.bar.exitFull') : t('rd.bar.full')} onClick={toggleFullscreen}>
            <Icon name={fullscreen ? 'fullscreenExit' : 'fullscreen'} size={15} />
          </button>
          <button type="button" className="rd-bar-btn" title={t('rd.bar.minimize')} aria-label={t('rd.bar.minimize')} onClick={() => {
            if (document.fullscreenElement) void document.exitFullscreen().catch(() => undefined);
            useWM.getState().minimize(win.id);
          }}>
            <Icon name="minimize" size={15} />
          </button>
          <button type="button" className="rd-bar-btn bad" title={t('rd.bar.disconnect')} aria-label={t('rd.bar.disconnect')} onClick={disconnect}>
            <Icon name="power" size={15} />
          </button>

          {panel === 'keys' && (
            <div className="rd-panel" role="dialog" aria-label={t('rd.bar.keyboard')}>
              <div className="rd-keys">
                {SPECIAL_KEYS.map((k) => (
                  <button key={k.id} type="button" className="ghost small" onClick={() => sendKeys(k.keys)}>
                    {t(`rd.keys.${k.id}`)}
                  </button>
                ))}
              </div>
              <label className="rd-field">
                <span>{t('rd.keys.type')}</span>
                <textarea
                  ref={typer}
                  className="rd-typer"
                  rows={2}
                  placeholder={t('rd.keys.typeHint')}
                  autoCapitalize="off"
                  autoCorrect="off"
                  spellCheck={false}
                  onKeyDown={(e) => {
                    if (e.key === 'Backspace' && !e.currentTarget.value) sendKeys([0xff08]);
                    if (e.key === 'Enter' && !e.shiftKey && !e.currentTarget.value) {
                      e.preventDefault();
                      sendKeys([0xff0d]);
                    }
                  }}
                  onInput={(e) => {
                    const v = e.currentTarget.value;
                    if (v) {
                      typeText(v);
                      e.currentTarget.value = '';
                    }
                  }}
                />
              </label>
            </div>
          )}
          {panel === 'clip' && (
            <ClipPanel text={clip} canSend={clipOut} canReceive={clipIn} onChange={setClip} onSend={(v) => sendClip(v)} />
          )}
        </div>
      )}

      {!connected && (
        <div className="rd-stage">
          <div className="rd-progress" role="status" aria-live="polite">
            {busy ? (
              <>
                <div className="spinner" />
                <b>{t('rd.session.connectingTo', { name })}</b>
                <ol className="rd-steps">
                  {STAGES.map((s, i) => (
                    <li key={s} className={i < stageIndex ? 'done' : i === stageIndex ? 'now' : ''}>
                      <Icon name={i < stageIndex ? 'check' : 'chevronRight'} size={12} /> {t(`rd.stage.${s}`)}
                    </li>
                  ))}
                </ol>
                {stage === 'engine' && engineMsg && <span className="muted small">{engineMsg}</span>}
                <button type="button" className="ghost" onClick={disconnect}>
                  {t('rd.cancel')}
                </button>
              </>
            ) : (
              <FailCard
                kind={fail?.kind ?? 'other'}
                detail={fail?.detail ?? ''}
                name={name}
                host={form.hostname}
                onBack={disconnect}
                onRetry={() => retry()}
                onTrust={() => retry({ trust: true })}
                onPassword={() => setAskPw(true)}
              />
            )}
          </div>
        </div>
      )}

      {askPw && (
        <CredentialsPrompt
          form={{ ...form, username }}
          canRemember={false}
          retry
          onCancel={() => setAskPw(false)}
          onConnect={(f, pw) => {
            setAskPw(false);
            retry({ password: pw, username: f.username });
          }}
        />
      )}
    </div>
  );
}

function FailCard({
  kind,
  detail,
  name,
  host,
  onBack,
  onRetry,
  onTrust,
  onPassword,
}: {
  kind: FailKind;
  detail: string;
  name: string;
  host: string;
  onBack: () => void;
  onRetry: () => void;
  onTrust: () => void;
  onPassword: () => void;
}) {
  const t = useT();
  const shownDetail = detail && detail !== 'closed' && !detail.startsWith('engine: ') ? detail : detail.replace(/^engine: /, '');
  return (
    <>
      <span className={`rd-fail-ico ${kind}`} aria-hidden>
        <Icon name={kind === 'auth' ? 'key' : kind === 'cert' ? 'shield' : kind === 'closed' ? 'desktop' : 'alert'} size={24} />
      </span>
      <b>{t(`rd.fail.${kind}`, { name })}</b>
      <p className="muted small">{t(`rd.fail.${kind}Hint`, { name, host })}</p>
      {kind === 'cert' && <p className="rd-warn small">{t('rd.fail.certWarn')}</p>}
      {shownDetail && kind !== 'closed' && <p className="rd-detail tiny">{shownDetail}</p>}
      <div className="rd-sheet-actions">
        <button type="button" className="ghost" onClick={onBack}>
          {t('rd.back')}
        </button>
        {kind === 'auth' ? (
          <button type="button" className="rd-connect" onClick={onPassword}>
            {t('rd.fail.newPassword')}
          </button>
        ) : kind === 'cert' ? (
          <button type="button" className="rd-connect" onClick={onTrust}>
            {t('rd.fail.connectAnyway')}
          </button>
        ) : (
          <button type="button" className="rd-connect" onClick={onRetry}>
            <Icon name="restart" size={14} /> {kind === 'closed' ? t('rd.reconnect') : t('rd.retry')}
          </button>
        )}
      </div>
    </>
  );
}

function ClipPanel({
  text,
  canSend,
  canReceive,
  onChange,
  onSend,
}: {
  text: string;
  canSend: boolean;
  canReceive: boolean;
  onChange: (v: string) => void;
  onSend: (v: string) => void;
}) {
  const t = useT();
  const [sent, setSent] = useState(false);
  return (
    <div className="rd-panel" role="dialog" aria-label={t('rd.bar.clipboard')}>
      <span className="muted small">{canReceive ? t('rd.clip.hint') : t('rd.clip.hintSendOnly')}</span>
      <textarea
        className="rd-typer"
        rows={4}
        value={text}
        readOnly={!canSend}
        placeholder={t('rd.clip.empty')}
        onChange={(e) => {
          onChange(e.target.value);
          setSent(false);
        }}
      />
      <div className="rd-sheet-actions">
        {canReceive && (
          <button type="button" className="ghost small" disabled={!text} onClick={() => void navigator.clipboard?.writeText(text).catch(() => undefined)}>
            <Icon name="copy" size={13} /> {t('rd.clip.copyHere')}
          </button>
        )}
        {canSend && (
          <button
            type="button"
            className="small"
            disabled={!text}
            onClick={() => {
              onSend(text);
              setSent(true);
            }}
          >
            <Icon name={sent ? 'check' : 'send'} size={13} /> {sent ? t('rd.clip.sent') : t('rd.clip.send')}
          </button>
        )}
      </div>
    </div>
  );
}
