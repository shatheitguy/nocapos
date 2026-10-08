import { useEffect, useRef, useState } from 'react';
import { Terminal as XTerm, type ITheme } from '@xterm/xterm';
import { CanvasAddon } from '@xterm/addon-canvas';
import { FitAddon } from '@xterm/addon-fit';
import { WebglAddon } from '@xterm/addon-webgl';
import '@xterm/xterm/css/xterm.css';
import { openTerminal, terminalApi, type TerminalInfo, type TerminalSocket } from '../api/terminal';
import { Icon } from '../components/Icon';
import { usePrefs } from '../state/prefs';
import type { WinState } from '../state/windows';

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

  const hostRoot = isHost && !!info?.host_root;

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

  useEffect(() => {
    const el = mountRef.current;
    if (!el) return;

    const prefs = usePrefs.getState();
    const term = new XTerm({
      cursorBlink: true,
      fontFamily: '"Cascadia Code", "JetBrains Mono", ui-monospace, Consolas, monospace',
      fontSize: prefs.termFontSize,
      theme: xtermTheme(prefs.uiTheme, themeAccent),
      scrollback: 5000,
      allowProposedApi: true,
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(el);
    // Draw on a GPU canvas. xterm's default DOM renderer injects a <style> tag
    // for fonts and colors, which NoCapOS's CSP (style-src 'self') blocks.
    const useCanvas = () => {
      try {
        term.loadAddon(new CanvasAddon());
      } catch {
        /* DOM renderer as a last resort */
      }
    };
    try {
      const webgl = new WebglAddon();
      webgl.onContextLoss(() => {
        webgl.dispose();
        useCanvas();
      });
      term.loadAddon(webgl);
    } catch {
      useCanvas();
    }
    fit.fit();
    term.focus();
    termRef.current = term;
    fitRef.current = fit;

    let disposed = false;

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

    (async () => {
      const t = await terminalApi.ticket(target, term.cols, term.rows);
      if (disposed) return;
      if (!t.ok) {
        setStatus('closed');
        setError(t.error ?? 'Could not start the terminal');
        return;
      }
      const sock = openTerminal(t.data.ticket, {
        onOpen: () => {
          setStatus('open');
          sockRef.current?.resize(term.cols, term.rows);
        },
        onData: (text) => {
          term.write(text);
          outTail.current = (outTail.current + text).slice(-240);
        },
        onClose: (reason) => {
          setStatus('closed');
          if (reason) setError(reason);
          term.write('\r\n\x1b[90m[session ended]\x1b[0m\r\n');
        },
      });
      sockRef.current = sock;
      term.onData((d) => {
        track(d);
        sock.send(d);
      });
    })();

    const onResize = () => {
      try {
        fit.fit();
        sockRef.current?.resize(term.cols, term.rows);
      } catch {
        /* element not laid out yet */
      }
    };
    const ro = new ResizeObserver(onResize);
    ro.observe(el);

    return () => {
      disposed = true;
      ro.disconnect();
      sockRef.current?.close();
      sockRef.current = null;
      termRef.current = null;
      term.dispose();
      window.clearTimeout(flashTimer.current);
    };
    // Re-mount only when the target changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [target]);

  // Live font size / theme changes.
  useEffect(() => {
    const term = termRef.current;
    if (!term) return;
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
            title={hostRoot ? 'Commands here run on the host machine as root' : 'Commands here run on the host machine'}
          >
            <Icon name={hostRoot ? 'shield' : 'monitor'} size={12} />
            {hostRoot ? 'HOST BARE-METAL · ROOT' : `HOST · ${info?.host_user || 'user'}`}
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
          <div className="terminal-mount" ref={mountRef} />
          {crt && <div className="crt-overlay" aria-hidden />}
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
