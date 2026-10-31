import { useEffect, useRef, useState } from 'react';
import { api } from '../api/client';
import { LogoMark } from '../components/Logo';
import { Icon } from '../components/Icon';
import { closeUpdate, forgetUpdate, openUpdate, useUpdate, vtag, type UpdateRunStatus, type UpdateStage } from '../state/update';

// Full-screen while NoCapOS updates itself. It keeps going through the
// restart: when the API stops answering it waits for the server to come
// back, then asks how the update ended.

const POLL_MS = 2000;
const SLOW_MS = 10 * 60_000; // down this long: say so, keep trying
const RELOAD_S = 3;

const STAGE_TEXT: Record<UpdateStage, string> = {
  download: 'Grabbing the update…',
  verify: 'Making sure it’s legit…',
  packages: 'Syncing system packages…',
  install: 'Leveling up NoCapOS…',
  restart: 'Wrapping up…',
  done: 'All done',
  failed: 'Update flopped',
};

const STEPS: { id: UpdateStage; label: string }[] = [
  { id: 'download', label: 'Download' },
  { id: 'packages', label: 'Packages' },
  { id: 'install', label: 'Install' },
  { id: 'restart', label: 'Restart' },
];
const ORDER: UpdateStage[] = ['download', 'verify', 'packages', 'install', 'restart', 'done'];

const same = (a?: string, b?: string) => !!a && !!b && a.replace(/^v/, '') === b.replace(/^v/, '');

/** The public "is anybody home" check: answers without a session. */
async function serverUp(): Promise<boolean> {
  try {
    const res = await fetch('/api/v1/auth/status', { cache: 'no-store', credentials: 'same-origin' });
    return res.ok;
  } catch {
    return false;
  }
}

export function UpdateOverlay() {
  const phase = useUpdate((s) => s.phase);
  const from = useUpdate((s) => s.from);
  const target = useUpdate((s) => s.target);
  const status = useUpdate((s) => s.status);
  const percent = useUpdate((s) => s.percent);
  const [slow, setSlow] = useState(false);
  const [countdown, setCountdown] = useState(RELOAD_S);
  const [retrying, setRetrying] = useState(false);
  const [retryError, setRetryError] = useState('');
  const logRef = useRef<HTMLPreElement>(null);
  const following = phase === 'installing' || phase === 'restarting';

  // Follow the run, through the restart.
  useEffect(() => {
    if (!following) return;
    let stopped = false;
    let timer = 0;
    let downAt: number | null = phase === 'restarting' ? Date.now() : null;
    const schedule = () => {
      if (!stopped) timer = window.setTimeout(() => void tick(), POLL_MS);
    };
    const tick = async () => {
      if (downAt !== null && !(await serverUp())) {
        if (stopped) return;
        setSlow(Date.now() - downAt > SLOW_MS);
        return schedule();
      }
      const r = await api<UpdateRunStatus>('/api/v1/system/update/run');
      if (stopped) return;
      const st = useUpdate.getState();
      if (r.ok) {
        downAt = null;
        setSlow(false);
        const d = r.data;
        if (d.running) {
          st.set({ status: d, percent: Math.max(st.percent, d.percent), target: d.target || st.target, phase: 'installing' });
          return schedule();
        }
        forgetUpdate();
        const res = d.result;
        const ok = res ? res.ok : same(d.version, st.target) || (!!st.from && d.version !== st.from);
        st.set({ status: d, percent: ok ? 100 : st.percent, target: res?.to || st.target, phase: ok ? 'success' : 'failed' });
        return;
      }
      if (r.status === 401 || r.status === 403) {
        // The server is back but this session isn't.
        forgetUpdate();
        st.set({ phase: 'signin' });
        return;
      }
      // Not reachable (or a proxy's 502): NoCapOS is restarting.
      downAt ??= Date.now();
      if (st.phase !== 'restarting') st.set({ phase: 'restarting' });
      schedule();
    };
    void tick();
    return () => {
      stopped = true;
      window.clearTimeout(timer);
    };
  }, [following, phase]);

  // Success: reload into the new version.
  useEffect(() => {
    if (phase !== 'success') return;
    setCountdown(RELOAD_S);
    const t = window.setInterval(() => {
      setCountdown((c) => {
        if (c <= 1) {
          window.clearInterval(t);
          window.location.reload();
          return 0;
        }
        return c - 1;
      });
    }, 1000);
    return () => window.clearInterval(t);
  }, [phase]);

  useEffect(() => {
    logRef.current?.scrollTo(0, logRef.current.scrollHeight);
  }, [status?.log]);

  if (phase === 'idle') return null;

  const stage: UpdateStage = phase === 'success' ? 'done' : phase === 'failed' ? 'failed' : (status?.stage ?? 'download');
  const res = status?.result ?? null;
  const shownPct = phase === 'success' ? 100 : percent;
  const stageIdx = ORDER.indexOf(stage === 'verify' ? 'download' : stage);
  const versions = from && target ? `${vtag(from)} → ${vtag(target)}` : target ? `Installing ${vtag(target)}` : '';

  let title: string;
  let sub: string;
  switch (phase) {
    case 'restarting':
      title = 'Hang tight, restarting — be right back';
      sub = slow
        ? 'This is taking longer than usual. Still trying… if it stays like this, check the server.'
        : 'NoCapOS is restarting into the new version. This page reconnects on its own.';
      break;
    case 'success':
      title = `You're on ${vtag(status?.version || target)}. That's the update, no cap.`;
      sub = `Reloading in ${countdown}…`;
      break;
    case 'signin':
      title = 'NoCapOS is back';
      sub = 'Your session ended while it restarted. Sign back in to keep going — Settings shows how the update went.';
      break;
    case 'failed':
      title = res?.unchanged ? 'Update flopped — nothing changed, your stuff is safe.' : 'Update flopped';
      sub = res?.rolled_back
        ? `The new version didn't start, so ${vtag(res.from || from)} was put back with your data as it was.`
        : res?.unchanged
          ? 'NoCapOS was left exactly as it was.'
          : 'Something went sideways partway through. The details below say what.';
      break;
    default:
      title = 'Leveling up NoCapOS';
      sub = status?.rolling_back ? 'The new version didn’t start — putting the old one back…' : STAGE_TEXT[stage];
  }

  const retry = () => {
    setRetrying(true);
    setRetryError('');
    void api<{ started: boolean; from: string; target: string }>('/api/v1/system/update/run', {
      method: 'POST',
      body: target ? { target } : {},
    }).then((r) => {
      setRetrying(false);
      if (r.ok) openUpdate(r.data.from, r.data.target);
      else setRetryError(r.error ?? 'Could not start the update');
    });
  };

  return (
    <div className={`upd-overlay ${phase}`} role="dialog" aria-modal="true" aria-labelledby="upd-title" aria-describedby="upd-sub">
      <div className="upd-card">
        <div className="upd-mark">
          <LogoMark size={84} />
        </div>
        <h2 id="upd-title">{title}</h2>
        <p id="upd-sub" className="upd-sub" aria-live="polite">
          {sub}
        </p>

        {(following || phase === 'success') && (
          <>
            <div
              className={`upd-bar ${phase === 'restarting' ? 'waiting' : ''}`}
              role="progressbar"
              aria-valuemin={0}
              aria-valuemax={100}
              aria-valuenow={shownPct}
              aria-label="Update progress"
            >
              <div className="upd-fill" style={{ width: `${Math.max(shownPct, 3)}%` }} />
            </div>
            <ol className="upd-steps">
              {STEPS.map((s) => {
                const i = ORDER.indexOf(s.id);
                const cls = stageIdx > i || phase === 'success' ? 'done' : stageIdx === i ? 'now' : '';
                return (
                  <li key={s.id} className={cls}>
                    {cls === 'done' ? <Icon name="check" size={11} /> : <span className="dot" />}
                    {s.label}
                  </li>
                );
              })}
            </ol>
          </>
        )}

        {versions && <span className="upd-version">{versions}</span>}

        {phase === 'failed' && res?.error && <p className="upd-error">{res.error}</p>}
        {retryError && <p className="upd-error">{retryError}</p>}

        {status?.log && phase !== 'success' && (
          <details className="upd-details">
            <summary>Show details</summary>
            <pre ref={logRef}>{status.log}</pre>
          </details>
        )}

        {phase === 'failed' && (
          <div className="upd-actions">
            <button type="button" className="pill ghost small" onClick={closeUpdate}>
              Close
            </button>
            <button type="button" className="pill small" disabled={retrying} onClick={retry}>
              {retrying ? <span className="spinner sm" /> : <Icon name="restart" size={13} />} Try again
            </button>
          </div>
        )}
        {phase === 'signin' && (
          <div className="upd-actions">
            <button type="button" className="pill small" onClick={() => window.location.reload()}>
              Sign in
            </button>
          </div>
        )}
        {phase === 'restarting' && slow && (
          <div className="upd-actions">
            <button type="button" className="pill ghost small" onClick={() => window.location.reload()}>
              Reload page
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
