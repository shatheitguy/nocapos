import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { Terminal as XTerm, type ITheme } from '@xterm/xterm';
import { CanvasAddon } from '@xterm/addon-canvas';
import { FitAddon } from '@xterm/addon-fit';
import { WebglAddon } from '@xterm/addon-webgl';
import '@xterm/xterm/css/xterm.css';
import { openTerminal, terminalApi, type TerminalInfo, type TerminalSocket } from '../api/terminal';
import { Icon } from '../components/Icon';
import { usePrefs } from '../state/prefs';
import { useWM, type WinState } from '../state/windows';

type Status = 'connecting' | 'open' | 'closed';

interface HistoryItem {
  cmd: string;
  at: number;
  privileged: boolean; // ran as root on the host, or via sudo/su
}

const MIN_FONT = 10;
const MAX_FONT = 24;

/** Terminal colors that follow the desktop theme. */
function xtermTheme(uiTheme: string, accent: string): ITheme {
  if (uiTheme === 'cyberdeck') {
    return { background: '#02060d', foreground: '#c9f7ff', cursor: accent, cursorAccent: '#02060d', selectionBackground: '#00f3ff40' };
  }
  return { background: '#0c1018', foreground: '#d7dde8', cursor: accent, selectionBackground: 'rgba(232, 35, 43, 0.28)' };
}

// Strip ANSI escapes so prompt text can be inspected.
// eslint-disable-next-line no-control-regex
const ANSI = /\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07]*(\x07|\x1b\\)|\x1b[@-_]/g;
// A password prompt was the last thing printed — don't record what's typed next.
const SECRET_PROMPT = /(password|passphrase|passcode|pin)[^\n]*:\s*$/i;
const PRIVILEGED = /^(sudo|su|doas)\b/;

let webgl2: boolean | undefined;
/** Whether this browser can draw with WebGL 2 at all (checked once). */
function hasWebgl2() {
  if (webgl2 === undefined) {
    try {
      webgl2 = !!document.createElement('canvas').getContext('webgl2');
    } catch {
      webgl2 = false;
    }
  }
  return webgl2;
}

const TERM_FONT = '"Cascadia Code", "JetBrains Mono", ui-monospace, Consolas, monospace';

/**
 * Creates and opens an xterm in `host`, drawing on the GPU when it can. The
 * renderer is chosen before the terminal opens, so xterm never paints a first
 * frame with its DOM renderer (whose injected <style> NoCapOS's CSP blocks)
 * and then repaints. If WebGL fails to start, or its context is lost later
 * (GPU reset, too many contexts), the 2D canvas renderer takes over.
 */
function openXterm(host: HTMLElement, theme: ITheme, fontSize: number): XTerm {
  const make = (gpu: boolean) => {
    const term = new XTerm({ cursorBlink: true, fontFamily: TERM_FONT, fontSize, theme, scrollback: 5000, allowProposedApi: true });
    if (gpu) {
      const webgl = new WebglAddon();
      webgl.onContextLoss(() => {
        webgl.dispose();
        try {
          term.loadAddon(new CanvasAddon());
        } catch {
          /* DOM renderer as a last resort */
        }
      });
      term.loadAddon(webgl);
    } else {
      try {
        term.loadAddon(new CanvasAddon());
      } catch {
        /* DOM renderer as a last resort */
      }
    }
    try {
      term.open(host);
    } catch (e) {
      term.dispose();
      throw e;
    }
    return term;
  };
  if (hasWebgl2()) {
    try {
      return make(true);
    } catch {
      host.replaceChildren();
      webgl2 = false;
    }
  }
  return make(false);
}

/**
 * Xterm.js terminal wired to a PTY in alfad. The target is in window props:
 *   target = "host" or "container:<id>"
 */
export function Terminal({ win }: { win: WinState }) {
  const target = win.props?.target ?? 'host';
  const label = win.props?.label ?? target;
  const isHost = target === 'host';
  const accent = usePrefs((s) => s.accent);
  const uiTheme = usePrefs((s) => s.uiTheme);
  const fontSize = usePrefs((s) => s.termFontSize);
  const crt = usePrefs((s) => s.termCrt);
  const setPrefs = usePrefs((s) => s.set);
  const themeAccent = getComputedStyle(document.documentElement).getPropertyValue('--accent').trim() || accent || '#e8232b';

  const mountRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<XTerm | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const sockRef = useRef<TerminalSocket | null>(null);
  const lineRef = useRef(''); // what's been typed on the current line
  const outTail = useRef(''); // recent output, to spot password prompts
  const flashTimer = useRef(0);

  const [status, setStatus] = useState<Status>('connecting');
  const [error, setError] = useState<string | null>(null);
  const [info, setInfo] = useState<TerminalInfo | null>(null);
  const [history, setHistory] = useState<HistoryItem[]>([]);
  const [showHistory, setShowHistory] = useState(false);
  const [filter, setFilter] = useState('');
  const [flash, setFlash] = useState(false);
  // Root shell: an administrator confirms their password to get one; the
  // normal host shell is their own Linux account.
  const [rootMode, setRootMode] = useState(false);
  const [askRoot, setAskRoot] = useState(false);
  const [rootPw, setRootPw] = useState('');
  const rootPwRef = useRef('');

  const hostRoot = isHost && (rootMode || !!info?.host_root);
  const shellTarget = isHost && rootMode ? 'host-root' : target;

  // Record a command and flash the safety badge if it ran with root powers.
  const record = (cmd: string) => {
    const privileged = hostRoot || PRIVILEGED.test(cmd);
    setHistory((h) => [{ cmd, at: Date.now(), privileged }, ...h.filter((x) => x.cmd !== cmd)].slice(0, 200));
    if (privileged) {
      setFlash(true);
      window.clearTimeout(flashTimer.current);
      flashTimer.current = window.setTimeout(() => setFlash(false), 1400);
    }
  };
  const recordRef = useRef(record);
  recordRef.current = record;

  useEffect(() => {
    void terminalApi.info().then((r) => r.ok && setInfo(r.data));
  }, []);

  // Created in a layout effect: the terminal is sized to its window before the
  // first paint, instead of appearing at 80×24 and jumping to fit.
  useLayoutEffect(() => {
    const el = mountRef.current;
    if (!el) return;

    const prefs = usePrefs.getState();
    const term = openXterm(el, xtermTheme(prefs.uiTheme, themeAccent), prefs.termFontSize);
    const fit = new FitAddon();
    term.loadAddon(fit);
    termRef.current = term;
    fitRef.current = fit;

    let disposed = false;
    let pending = ''; // typed before the session is up

    // Only resize when the cell grid really changes: every resize clears and
    // redraws the whole screen, and a resize that changes nothing still costs
    // a frame (the flicker while a window opened or settled).
    const refit = () => {
      const d = fit.proposeDimensions();
      if (!d || !Number.isFinite(d.cols) || !Number.isFinite(d.rows) || d.cols < 2 || d.rows < 1) return;
      if (d.cols === term.cols && d.rows === term.rows) return;
      term.resize(d.cols, d.rows);
      sockRef.current?.resize(term.cols, term.rows);
    };
    try {
      refit();
    } catch {
      /* not laid out yet; the observer below catches up */
    }
    term.focus();

    // Track the typed line so finished commands land in the history panel.
    const track = (d: string) => {
      for (const ch of d.startsWith('\x1b') ? '' : d) {
        if (ch === '\r' || ch === '\n') {
          const cmd = lineRef.current.trim();
          const secret = SECRET_PROMPT.test(outTail.current.replace(ANSI, '').trimEnd());
          if (cmd && !secret) recordRef.current(cmd);
          lineRef.current = '';
        } else if (ch === '\x7f' || ch === '\b') {
          lineRef.current = lineRef.current.slice(0, -1);
        } else if (ch === '\x03' || ch === '\x15') {
          lineRef.current = ''; // Ctrl+C / Ctrl+U
        } else if (ch >= ' ') {
          lineRef.current += ch;
        }
      }
    };

    // Keys work from the first moment: anything typed while connecting is sent once the shell is up.
    let live = false;
    const input = term.onData((d) => {
      track(d);
      if (live) sockRef.current?.send(d);
      else pending += d;
    });

    (async () => {
      const password = shellTarget === 'host-root' ? rootPwRef.current : undefined;
      rootPwRef.current = ''; // used once, never kept
      const t = await terminalApi.ticket(shellTarget, term.cols, term.rows, password);
      if (disposed) return;
      if (!t.ok) {
        setStatus('closed');
        setError(t.error ?? 'Could not start the terminal');
        if (shellTarget === 'host-root') setRootMode(false); // back to your own shell
        return;
      }
      sockRef.current = openTerminal(t.data.ticket, {
        onOpen: () => {
          live = true;
          setStatus('open');
          sockRef.current?.resize(term.cols, term.rows);
          if (pending) {
            sockRef.current?.send(pending);
            pending = '';
          }
        },
        onData: (text) => {
          term.write(text);
          outTail.current = (outTail.current + text).slice(-240);
        },
        onClose: (reason) => {
          live = false;
          setStatus('closed');
          if (reason) setError(reason);
          term.write('\r\n\x1b[90m[session ended]\x1b[0m\r\n');
        },
      });
    })();

    // Window resizes arrive in bursts; fit once per frame at most.
    let frame = 0;
    const ro = new ResizeObserver(() => {
      if (frame) return;
      frame = requestAnimationFrame(() => {
        frame = 0;
        try {
          refit();
        } catch {
          /* element not laid out yet */
        }
      });
    });
    ro.observe(el);

    return () => {
      disposed = true;
      ro.disconnect();
      cancelAnimationFrame(frame);
      input.dispose();
      sockRef.current?.close();
      sockRef.current = null;
      termRef.current = null;
      fitRef.current = null;
      term.dispose();
      window.clearTimeout(flashTimer.current);
    };
    // Re-mount only when the target (or root shell) changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [shellTarget]);

  // Bringing the window to the front puts the keyboard back in the terminal
  // (unless something inside it, like the history filter, already has focus).
  useEffect(
    () =>
      useWM.subscribe((s, prev) => {
        if (s.focused !== win.id || prev.focused === win.id) return;
        const box = mountRef.current?.closest('.terminal-app');
        if (box && !box.contains(document.activeElement)) termRef.current?.focus();
      }),
    [win.id],
  );

  // Live font size / theme changes.
  useEffect(() => {
    const term = termRef.current;
    if (!term) return;
    if (term.options.fontSize === fontSize) return;
    term.options.fontSize = fontSize;
    try {
      fitRef.current?.fit();
      sockRef.current?.resize(term.cols, term.rows);
    } catch {
      /* not laid out */
    }
  }, [fontSize]);
  useEffect(() => {
    if (termRef.current) termRef.current.options.theme = xtermTheme(uiTheme, themeAccent);
  }, [uiTheme, themeAccent]);

  const zoom = (delta: number) => setPrefs({ termFontSize: Math.min(MAX_FONT, Math.max(MIN_FONT, fontSize + delta)) });

  const insert = (cmd: string, run: boolean) => {
    const sock = sockRef.current;
    if (!sock) return;
    // Clear whatever is half-typed (Ctrl+U), then type the command.
    sock.send('\x15' + cmd + (run ? '\r' : ''));
    lineRef.current = run ? '' : cmd;
    if (run) record(cmd);
    termRef.current?.focus();
  };

  const shown = history.filter((h) => !filter || h.cmd.toLowerCase().includes(filter.toLowerCase()));

  return (
    <div className={`terminal-app ${crt ? 'crt' : ''}`}>
      <div className="terminal-bar">
        <span className={`status-dot ${status === 'open' ? 'on' : status === 'connecting' ? 'warn' : 'off'}`} />
        <span className="term-label">{label}</span>
        {isHost ? (
          <span
            className={`term-badge ${hostRoot ? 'root' : 'host'} ${flash ? 'flash' : ''}`}
            title={
              hostRoot
                ? 'Commands here run on the host machine as root'
                : `Commands here run on the host machine as ${info?.host_user || 'your account'}; use sudo for administration`
            }
          >
            <Icon name={hostRoot ? 'shield' : 'monitor'} size={12} />
            {hostRoot ? 'HOST · ROOT' : `HOST · ${info?.host_user || 'user'}`}
          </span>
        ) : (
          <span className={`term-badge container ${flash ? 'flash' : ''}`} title="Commands run inside this container">
            <Icon name="containers" size={12} /> CONTAINER
          </span>
        )}
        <span className="spacer" />
        {status === 'connecting' && <span className="muted small">connecting…</span>}
        {status === 'closed' && <span className="muted small">{error ?? 'disconnected'}</span>}
        <div className="term-tools">
          {isHost && info?.root_available && !info.host_root && (
            <button
              type="button"
              className={rootMode ? 'on' : ''}
              title={rootMode ? 'Back to a shell as your own account' : 'Open a root shell (asks for your password)'}
              aria-pressed={rootMode}
              onClick={() => (rootMode ? setRootMode(false) : setAskRoot(true))}
            >
              <Icon name="shield" size={12} /> {rootMode ? 'Exit root' : 'Root shell'}
            </button>
          )}
          <button type="button" title="Smaller text" aria-label="Smaller text" onClick={() => zoom(-1)} disabled={fontSize <= MIN_FONT}>
            A−
          </button>
          <span className="term-size" title="Font size">{fontSize}px</span>
          <button type="button" title="Larger text" aria-label="Larger text" onClick={() => zoom(1)} disabled={fontSize >= MAX_FONT}>
            A+
          </button>
          <button type="button" className={crt ? 'on' : ''} title="CRT scanlines" aria-pressed={crt} onClick={() => setPrefs({ termCrt: !crt })}>
            CRT
          </button>
          <button type="button" className={showHistory ? 'on' : ''} title="Command history" aria-pressed={showHistory} onClick={() => setShowHistory((v) => !v)}>
            <Icon name="logs" size={13} /> History{history.length ? ` (${history.length})` : ''}
          </button>
        </div>
      </div>

      <div className="terminal-body">
        <div className="terminal-screen">
          <div className="terminal-mount">
            <div className="terminal-host" ref={mountRef} />
          </div>
          {crt && <div className="crt-overlay" aria-hidden />}
          {askRoot && (
            <form
              className="term-root-prompt"
              onSubmit={(e) => {
                e.preventDefault();
                if (!rootPw) return;
                rootPwRef.current = rootPw;
                setRootPw('');
                setAskRoot(false);
                setError(null);
                setRootMode(true);
              }}
            >
              <p>
                <Icon name="shield" size={14} /> A root shell can change anything on this machine. Enter your password to open one.
              </p>
              <input
                type="password"
                value={rootPw}
                onChange={(e) => setRootPw(e.target.value)}
                placeholder="Your password"
                aria-label="Your password"
                autoComplete="current-password"
                autoFocus
              />
              <div className="term-root-actions">
                <button
                  type="button"
                  onClick={() => {
                    setAskRoot(false);
                    setRootPw('');
                  }}
                >
                  Cancel
                </button>
                <button type="submit" disabled={!rootPw}>
                  Open root shell
                </button>
              </div>
            </form>
          )}
          {isHost && status === 'closed' && info?.host_error && !rootMode && (
            <p className="term-root-prompt note">{info.host_error}</p>
          )}
        </div>

        {showHistory && (
          <aside className="term-history" aria-label="Command history">
            <div className="term-history-head">
              <input value={filter} placeholder="Filter commands" aria-label="Filter commands" onChange={(e) => setFilter(e.target.value)} />
              <button type="button" title="Clear history" disabled={!history.length} onClick={() => setHistory([])}>
                Clear
              </button>
            </div>
            {shown.length === 0 ? (
              <p className="term-history-empty">{history.length ? 'No matches.' : 'Commands you run in this session appear here. Passwords are never recorded.'}</p>
            ) : (
              <ul>
                {shown.map((h) => (
                  <li key={h.at + h.cmd} className={h.privileged ? 'privileged' : ''}>
                    <button type="button" className="term-history-cmd" title="Insert at the prompt" onClick={() => insert(h.cmd, false)}>
                      {h.privileged && <span className="term-root-tag">root</span>}
                      {h.cmd}
                    </button>
                    <button type="button" className="term-history-run" title="Run again" aria-label={`Run ${h.cmd}`} onClick={() => insert(h.cmd, true)}>
                      <Icon name="play" size={11} />
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </aside>
        )}
      </div>
    </div>
  );
}
