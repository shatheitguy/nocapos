import { useEffect, useRef, useState } from 'react';
import { tr } from '../i18n';
import { api, type ApiResult } from '../api/client';
import { BrandBar, BrandMark } from '../components/BrandLoader';
import { Icon } from '../components/Icon';
import { nextDelay, probeServer, withTimeout } from '../lib/reconnect';
import { closeUpdate, forgetUpdate, openUpdate, useUpdate, vtag, type UpdateRunStatus, type UpdateStage } from '../state/update';

// Full-screen while NoCapOS updates itself. It keeps going through the
// restart: when the API stops answering it keeps probing the public health
// check (with a timeout and a gentle backoff, never giving up), then asks
// how the update ended. A server that answers with a redirect (now on HTTPS,
// or another address) gets a full page load, which follows it.

const POLL_MS = 2000;
const STATUS_TIMEOUT_MS = 10_000;
const SLOW_MS = 3 * 60_000; // down this long: offer a reload, keep trying
const STUCK_MS = 12 * 60_000; // ... and this long: say to check the machine
const RELOAD_S = 3;

const STAGE_TEXT: Record<UpdateStage, () => string> = {
  download: () => tr('upd.stage.download'),
  verify: () => tr('upd.stage.verify'),
  packages: () => tr('upd.stage.packages'),
  install: () => tr('upd.stage.install'),
  restart: () => tr('upd.stage.restart'),
  done: () => tr('upd.stage.done'),
  failed: () => tr('upd.stage.failed'),
};

const STEPS: { id: UpdateStage; label: () => string }[] = [
  { id: 'download', label: () => tr('upd.step.download') },
  { id: 'packages', label: () => tr('upd.step.packages') },
  { id: 'install', label: () => tr('upd.step.install') },
  { id: 'restart', label: () => tr('upd.step.restart') },
];
const ORDER: UpdateStage[] = ['download', 'verify', 'packages', 'install', 'restart', 'done'];

const same = (a?: string, b?: string) => !!a && !!b && a.replace(/^v/, '') === b.replace(/^v/, '');

const unreachable = { ok: false, status: 0, data: null, error: 'NoCapOS is not reachable' } as unknown as ApiResult<UpdateRunStatus>;

export function UpdateOverlay() {
  const phase = useUpdate((s) => s.phase);
  const from = useUpdate((s) => s.from);
  const target = useUpdate((s) => s.target);
  const status = useUpdate((s) => s.status);
  const percent = useUpdate((s) => s.percent);
  const [away, setAway] = useState(0); // how long the server has been away (ms)
  const [countdown, setCountdown] = useState(RELOAD_S);
  const [retrying, setRetrying] = useState(false);
  const [retryError, setRetryError] = useState('');
  const logRef = useRef<HTMLPreElement>(null);
  const following = phase === 'installing' || phase === 'restarting';

  // Follow the run, through the restart.
  useEffect(() => {
    if (!following) return;
    let stopped = false;
    let busy = false;
    let timer = 0;
    let delay = POLL_MS;
    let downAt: number | null = phase === 'restarting' ? Date.now() : null;
    const schedule = (ms: number) => {
      window.clearTimeout(timer);
      if (!stopped) timer = window.setTimeout(() => void tick(), ms);
    };
    const wentAway = () => {
      downAt ??= Date.now();
      setAway(Date.now() - downAt);
      delay = nextDelay(Math.max(delay, 1000));
      const st = useUpdate.getState();
      if (st.phase !== 'restarting') st.set({ phase: 'restarting' });
      schedule(delay);
    };
    const tick = async () => {
      if (stopped || busy) return;
      busy = true;
      try {
        if (downAt !== null) {
          const p = await probeServer();
          if (stopped) return;
          if (p === 'moved') {
            // Back, but somewhere else (HTTPS, another port): a page load
            // follows the redirect, and the overlay picks up from there.
            window.location.reload();
            return;
          }
          if (p === 'down') return wentAway();
        }
        const r = await withTimeout(api<UpdateRunStatus>('/api/v1/system/update/run'), STATUS_TIMEOUT_MS, unreachable);
        if (stopped) return;
        const st = useUpdate.getState();
        if (r.ok && r.data) {
          downAt = null;
          delay = POLL_MS;
          setAway(0);
          const d = r.data;
          if (d.running) {
            st.set({ status: d, percent: Math.max(st.percent, d.percent), target: d.target || st.target, phase: 'installing' });
            return schedule(POLL_MS);
          }
          forgetUpdate();
          const res = d.result;
          const ok = res ? res.ok : same(d.version, st.target) || (!!st.from && !same(d.version, st.from));
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
        wentAway();
      } finally {
        busy = false;
      }
    };
    // Back on the tab or the network: check now (hidden tabs' timers crawl).
    const now = () => {
      if (!document.hidden) {
        delay = POLL_MS;
        schedule(0);
      }
    };
    window.addEventListener('online', now);
    document.addEventListener('visibilitychange', now);
    void tick();
    return () => {
      stopped = true;
      window.clearTimeout(timer);
      window.removeEventListener('online', now);
      document.removeEventListener('visibilitychange', now);
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
  const versions = from && target ? `${vtag(from)} → ${vtag(target)}` : target ? tr('upd.installing', { version: vtag(target) }) : '';
  const slow = phase === 'restarting' && away > SLOW_MS;
  const stuck = phase === 'restarting' && away > STUCK_MS;

  let title: string;
  let sub: string;
  switch (phase) {
    case 'restarting':
      title = tr('upd.restartingTitle');
      sub = stuck
        ? tr('upd.stuck')
        : slow
          ? tr('upd.slow')
          : tr('upd.restartingSub');
      break;
    case 'success':
      title = tr('upd.successTitle', { version: vtag(status?.version || target) });
      sub = res?.restart_needed ? tr('upd.reloadingRestart', { n: countdown }) : tr('upd.reloading', { n: countdown });
      break;
    case 'signin':
      title = tr('upd.backTitle');
      sub = tr('upd.backSub');
      break;
    case 'failed':
      title = res?.unchanged ? tr('upd.failedUnchanged') : tr('upd.failed');
      sub = res?.rolled_back
        ? tr('upd.rolledBack', { version: vtag(res.from || from) })
        : res?.unchanged
          ? tr('upd.leftAsIs')
          : tr('upd.partway');
      break;
    default:
      title = tr('upd.workingTitle');
      sub = status?.rolling_back ? tr('upd.rollingBack') : STAGE_TEXT[stage]();
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
      else setRetryError(r.error ?? tr('upd.startFail'));
    });
  };

  return (
    <div className={`upd-overlay ${phase}`} role="dialog" aria-modal="true" aria-labelledby="upd-title" aria-describedby="upd-sub">
      <div className="upd-card">
        <BrandMark size={96} busy={following} />
        <h2 id="upd-title">{title}</h2>
        <p id="upd-sub" className="upd-sub" aria-live="polite">
          {sub}
        </p>

        {(following || phase === 'success') && (
          <>
            <BrandBar percent={phase === 'restarting' && shownPct < 1 ? undefined : shownPct} done={phase === 'success'} label={tr('upd.progress')} />
            <ol className="upd-steps">
              {STEPS.map((s) => {
                const i = ORDER.indexOf(s.id);
                const cls = stageIdx > i || phase === 'success' ? 'done' : stageIdx === i ? 'now' : '';
                return (
                  <li key={s.id} className={cls}>
                    {cls === 'done' ? <Icon name="check" size={11} /> : <span className="dot" />}
                    {s.label()}
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
            <summary>{tr('upd.details')}</summary>
            <pre ref={logRef}>{status.log}</pre>
          </details>
        )}

        {phase === 'failed' && (
          <div className="upd-actions">
            <button type="button" className="pill ghost small" onClick={closeUpdate}>
              {tr('upd.close')}
            </button>
            <button type="button" className="pill small" disabled={retrying} onClick={retry}>
              {retrying ? <span className="spinner sm" /> : <Icon name="restart" size={13} />} {tr('upd.retry')}
            </button>
          </div>
        )}
        {phase === 'signin' && (
          <div className="upd-actions">
            <button type="button" className="pill small" onClick={() => window.location.reload()}>
              {tr('upd.signIn')}
            </button>
          </div>
        )}
        {slow && (
          <div className="upd-actions">
            <button type="button" className="pill ghost small" onClick={() => window.location.reload()}>
              {tr('upd.reload')}
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
