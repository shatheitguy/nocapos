import { useEffect, useRef, useState } from 'react';
import { tr, useT } from '../i18n';
import { api } from '../api/client';
import { Icon } from '../components/Icon';
import { Logo } from '../components/Logo';
import { powerHost } from '../lib/hostActions';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import { openUpdate, resumeRunningUpdate, useUpdate, vtag, type UpdateRunStatus } from '../state/update';
import { confirmChange, RepairCard, splitKernel, type PkgHealth } from './PackagesRepair';

// Settings → Glow Up: one place for every update. One check looks at
// NoCapOS and the system's packages together, one summary says what's
// waiting, and one button installs it all as a single run on the server
// (POST /api/v1/system/update/all): repair the packages if needed, the
// system updates, then NoCapOS itself, which restarts it.

interface UpdateInfo {
  current: string;
  latest?: string;
  available: boolean;
  dev: boolean;
  name?: string;
  notes?: string;
  url?: string;
  published?: string;
  checked: string;
  command: string;
  error?: string;
}

interface SysUpdate {
  name: string;
  current?: string;
  available: string;
  held: boolean;
  security: boolean;
  kernel: boolean;
  group?: string;
}

interface PkgJob {
  id: string;
  action: string;
  running: boolean;
  ok: boolean;
  output?: string;
  error?: string;
}

interface SysStatus {
  supported: boolean;
  reason?: string;
  manager?: string;
  checked?: string;
  updates: SysUpdate[];
  security: number;
  kernel: boolean;
  restart_needed: boolean;
  job?: PkgJob;
  health?: PkgHealth;
  error?: string;
}

interface AllRun {
  started: string;
  ended?: string;
  phase: 'packages' | 'nocapos' | 'done';
  packages: number;
  security: number;
  repair?: boolean;
  kernel?: boolean;
  from?: string;
  target?: string;
  ok: boolean;
  error?: string;
}

interface AllStatus {
  run: AllRun | null;
  running: boolean;
  job?: PkgJob;
  nocapos?: UpdateRunStatus;
  restart_needed: boolean;
}

const SHOWN = 12; // packages listed before "Show all"
const POLL_MS = 1500;

/** Release notes are Markdown; show them as plain, readable text. */
function plainNotes(md: string) {
  return md
    .replace(/\r/g, '')
    .split('\n')
    .filter((l) => !/^\s*```/.test(l))
    .map((l) => l.replace(/^#{1,6}\s*/, '').replace(/^\s*[-*]\s+/, '• ').replace(/\*\*(.+?)\*\*/g, '$1').replace(/`([^`]+)`/g, '$1').replace(/\[([^\]]+)\]\([^)]+\)/g, '$1'))
    .join('\n')
    .replace(/\n{3,}/g, '\n\n')
    .trim();
}

const sleep = (ms: number) => new Promise((r) => window.setTimeout(r, ms));

/** Waits for a package job (the list refresh) to end. */
async function waitJob(id: string) {
  for (let i = 0; i < 400; i++) {
    const r = await api<PkgJob>(`/api/v1/packages/jobs/${id}`);
    if (!r.ok || !r.data.running) return;
    await sleep(POLL_MS);
  }
}

export function SoftwareUpdate() {
  const t = useT();
  const [u, setU] = useState<UpdateInfo | null>(null);
  const [sys, setSys] = useState<SysStatus | null>(null);
  const [run, setRun] = useState<UpdateRunStatus | null>(null);
  const [all, setAll] = useState<AllStatus | null>(null);
  const [checking, setChecking] = useState(false);
  const [starting, setStarting] = useState(false);
  const [showAll, setShowAll] = useState(false);
  const updating = useUpdate((s) => s.phase !== 'idle');
  const logRef = useRef<HTMLPreElement>(null);

  const loadRelease = async (refresh: boolean) => {
    const r = await api<UpdateInfo>(`/api/v1/system/update${refresh ? '?refresh=1' : ''}`);
    if (r.ok) setU(r.data);
    else if (refresh) toast('error', tr('sp.updCheckFail'), r.error);
    return r.ok ? r.data : null;
  };
  const loadSys = async () => {
    const r = await api<SysStatus>('/api/v1/system/update/packages');
    if (r.ok) setSys(r.data);
    return r.ok ? r.data : null;
  };
  const loadAll = async () => {
    const r = await api<AllStatus>('/api/v1/system/update/all');
    if (r.ok) setAll(r.data);
    return r.ok ? r.data : null;
  };

  useEffect(() => {
    void loadRelease(false);
    void loadSys();
    void loadAll();
  }, []);
  useEffect(() => {
    if (updating) return;
    void api<UpdateRunStatus>('/api/v1/system/update/run').then((r) => {
      if (!r.ok) return;
      setRun(r.data);
      // Opened mid-update (another tab started it): follow along full-screen.
      if (r.data.running && useUpdate.getState().phase === 'idle') void resumeRunningUpdate();
    });
  }, [updating]);

  // Follow a running "Update everything".
  const allRunning = !!all?.running;
  useEffect(() => {
    if (!allRunning) return;
    let stopped = false;
    let timer = 0;
    const tick = async () => {
      const r = await api<AllStatus>('/api/v1/system/update/all');
      if (stopped) return;
      if (r.ok) {
        setAll(r.data);
        const st = r.data.run;
        if (st?.phase === 'nocapos' && useUpdate.getState().phase === 'idle') {
          // NoCapOS restarts now: the full-screen view takes it from here.
          openUpdate(st.from ?? '', st.target ?? '');
          return;
        }
        if (!r.data.running) {
          if (st?.ok) toast('success', tr('gu.doneOk'));
          else toast('error', tr('gu.doneFail'), st?.error);
          void loadSys();
          void loadRelease(false);
          return;
        }
      }
      timer = window.setTimeout(() => void tick(), POLL_MS);
    };
    timer = window.setTimeout(() => void tick(), POLL_MS);
    return () => {
      stopped = true;
      window.clearTimeout(timer);
    };
  }, [allRunning]);

  useEffect(() => {
    logRef.current?.scrollTo(0, logRef.current.scrollHeight);
  }, [all?.job?.output]);

  // One check: the NoCapOS release and the system's package lists together.
  const check = async () => {
    setChecking(true);
    const refresh = sys?.supported
      ? api<{ job: PkgJob }>('/api/v1/system/update/packages/refresh', { method: 'POST' }).then(async (r) => {
          if (r.ok) await waitJob(r.data.job.id);
        })
      : Promise.resolve();
    await Promise.all([loadRelease(true), refresh]);
    await loadSys();
    setChecking(false);
  };

  const nocPending = !!u && (u.available || u.dev) && !u.error && !!u.latest && !!run?.supported;
  const pending = sys?.supported ? sys.updates.filter((x) => !x.held) : [];
  const security = pending.filter((x) => x.security).length;
  const broken = !!sys?.health?.broken;
  const kernel = pending.some((x) => x.kernel);
  const anything = nocPending || pending.length > 0 || broken;
  const busy = checking || starting || allRunning || updating || !!sys?.job?.running || !!run?.running;

  // One button: everything at once.
  const updateAll = async () => {
    const target = nocPending ? u!.latest! : '';
    const what = [
      ...(target ? [t('gu.askNoc', { version: vtag(target) })] : []),
      ...(pending.length ? [t('gu.askSys', { n: pending.length })] : []),
    ].join(' + ');
    let message = t('gu.askMsg', { what: what || t('gu.stepRepair') });
    if (broken) message += ' ' + t('gu.bodyRepair');
    if (kernel) message += ' ' + t('gu.bodyKernel');
    if (target) message += ' ' + t('gu.askRestartNoc');
    const ask = { title: t('gu.askTitle'), message, confirmLabel: t('gu.confirm') };
    setStarting(true);
    let allow: string[] | null = [];
    if (pending.length || broken) allow = await confirmChange('upgrade-all', [], ask);
    else if (!(await confirmDialog(ask))) allow = null;
    if (allow === null) return setStarting(false);
    const r = await api<{ run: AllRun }>('/api/v1/system/update/all', {
      method: 'POST',
      body: { target: target || undefined, allow_remove: allow },
    });
    setStarting(false);
    if (r.ok) {
      if (r.data.run.phase === 'nocapos') {
        openUpdate(r.data.run.from ?? '', r.data.run.target ?? '');
        return;
      }
      setAll({ run: r.data.run, running: true, restart_needed: false });
      return;
    }
    const code = (r.data as unknown as { error?: { code?: string } } | null)?.error?.code;
    if (code === 'already_running') {
      void loadAll();
      void resumeRunningUpdate();
      return;
    }
    if (code === 'release_changed') void loadRelease(true);
    toast('error', tr('sp.updStartFail'), r.error);
  };

  // ---- the summary
  let headline: string;
  let detail = '';
  const ar = all?.run;
  if (allRunning && ar) {
    headline = t('gu.running');
    detail = ar.phase === 'packages' ? t('gu.runningBody') : '';
  } else if (checking) {
    headline = t('gu.checking');
  } else if (!u || !sys) {
    headline = t('gu.checking');
  } else if (nocPending && pending.length) {
    headline = t('gu.sumBoth', { version: vtag(u.latest), n: pending.length }) + (security ? t('gu.sec', { n: security }) : '');
  } else if (nocPending) {
    headline = u.dev ? t('sp.updDev') : t('gu.sumNoc', { version: vtag(u.latest) });
    if (u.dev) detail = t('sp.updDevBody', { current: u.current, latest: vtag(u.latest) });
  } else if (pending.length) {
    headline = t('gu.sumSys', { n: pending.length }) + (security ? t('gu.sec', { n: security }) : '');
  } else if (broken) {
    headline = t('gu.sumRepair');
  } else if (u.error) {
    headline = t('sp.updCantCheck');
    detail = u.error;
  } else {
    headline = t('gu.upToDate');
    detail = t('gu.upToDateBody', { version: vtag(u.current) });
  }
  if (!allRunning && anything && !checking) {
    const bits = [security ? t('gu.bodySec') : '', broken ? t('gu.bodyRepair') : '', kernel ? t('gu.bodyKernel') : '', t('sp.updKeep')];
    detail = [detail, ...bits].filter(Boolean).join(' ');
  }
  const checked = [u?.checked, sys?.checked].filter(Boolean).map((x) => new Date(x!).getTime());
  const lastChecked = checked.length ? new Date(Math.min(...checked)) : null;

  // ---- the progress view
  const steps: { id: string; label: string; state: 'done' | 'now' | 'todo' | 'bad' }[] = [];
  if (ar && (allRunning || ar.phase === 'done')) {
    const order = { packages: 0, nocapos: 1, done: 2 }[ar.phase];
    const hasSys = ar.packages > 0 || !!ar.repair;
    const failedAt = ar.phase === 'done' && !ar.ok ? (hasSys && !all?.job?.ok ? 0 : 1) : -1;
    const st = (i: number) => (failedAt === i ? 'bad' : order > i ? 'done' : order === i ? 'now' : 'todo');
    if (hasSys) {
      steps.push({ id: 'sys', label: ar.packages > 0 ? t('gu.stepSys', { n: ar.packages }) : t('gu.stepRepair'), state: st(0) });
    }
    if (ar.target) steps.push({ id: 'noc', label: t('gu.stepNoc', { version: vtag(ar.target) }), state: st(1) });
  }
  const restartNeeded = !!(all?.restart_needed || sys?.restart_needed);

  const { kernel: kernelUps, rest } = splitKernel(sys?.updates ?? []);
  const list = showAll ? rest : rest.slice(0, SHOWN - (kernelUps.length ? 1 : 0));
  const rows = rest.length + (kernelUps.length ? 1 : 0);
  const lastNoc = run && !run.running ? run.result : null;

  return (
    <div className="stack settings-page">
      <div className="ucard set-update glowup">
        <Logo size={40} />
        <div className="set-update-text">
          <b>{headline}</b>
          {detail && <span>{detail}</span>}
          {lastChecked && !allRunning && <small>{t('sp.updChecked', { time: lastChecked.toLocaleString() })}</small>}
        </div>
        <div className="btn-group">
          {allRunning ? null : anything ? (
            <button type="button" className="pill small" disabled={busy} onClick={() => void updateAll()}>
              {starting ? <span className="spinner sm" /> : <Icon name="download" size={13} />} {t('gu.updateAll')}
            </button>
          ) : (
            <button type="button" className="pill ghost small" disabled={busy} onClick={() => void check()}>
              {checking ? <span className="spinner sm" /> : <Icon name="restart" size={13} />} {t('gu.check')}
            </button>
          )}
        </div>

        {steps.length > 0 && (
          <ol className="glowup-steps">
            {steps.map((s) => (
              <li key={s.id} className={s.state}>
                <span className="glowup-dot">
                  {s.state === 'done' ? <Icon name="check" size={12} /> : s.state === 'bad' ? <Icon name="alert" size={12} /> : s.state === 'now' ? <span className="spinner sm" /> : null}
                </span>
                {s.label}
              </li>
            ))}
          </ol>
        )}
        {allRunning && <div className="sysupd-bar" role="progressbar" aria-label={headline} />}

        {ar?.phase === 'done' && !allRunning && (
          <div className={`set-update-result ${ar.ok ? 'ok' : 'bad'}`}>
            <Icon name={ar.ok ? 'check' : 'alert'} size={15} />
            <span>
              {ar.ok ? t('gu.doneOk') : t('gu.doneFail')}
              {ar.ended && ` · ${new Date(ar.ended).toLocaleString()}`}
              {!ar.ok && ar.error && <small className="set-update-err">{ar.error}</small>}
            </span>
          </div>
        )}
        {!ar && lastNoc && !lastNoc.ok && (
          <div className="set-update-result bad">
            <Icon name="alert" size={15} />
            <span>
              {lastNoc.to ? t('sp.updFailedTo', { to: vtag(lastNoc.to) }) : t('sp.updFailed')}
              {lastNoc.rolled_back && lastNoc.from ? t('sp.updRolledBack', { from: vtag(lastNoc.from) }) : lastNoc.unchanged ? t('sp.updUnchanged') : ''}
              {lastNoc.error && <small className="set-update-err">{lastNoc.error}</small>}
            </span>
          </div>
        )}

        {restartNeeded && !allRunning && (
          <div className="sysupd-restart">
            <Icon name="power" size={15} />
            <span>{t('gu.restartNeeded')}</span>
            <button type="button" className="pill small" onClick={() => void powerHost('reboot')}>
              {t('gu.restart')}
            </button>
          </div>
        )}

        {sys?.health && !allRunning && <RepairCard health={sys.health} />}

        {(all?.job?.output || (run?.log && !ar)) && (
          <details className="set-update-log" open={allRunning}>
            <summary>{allRunning ? t('gu.liveOutput') : t('sp.showDetails')}</summary>
            <pre ref={logRef} className="set-logs">
              {all?.job?.output || run?.log}
            </pre>
          </details>
        )}
      </div>

      {sys?.supported && sys.updates.length > 0 && !allRunning && (
        <details className="settings-section glowup-details">
          <summary className="settings-section-title">
            {t('gu.details')} <span className="muted">{t('gu.sysHeader', { n: pending.length }) + (sys.manager ? ` · ${sys.manager}` : '')}</span>
          </summary>
          <div className="settings-card">
            <div className="settings-row full sysupd">
              <ul className="sysupd-list">
                {kernelUps.length > 0 && (
                  <li className="sysupd-kernel">
                    <span className="sysupd-name">{t('pkg.kernelRow')}</span>
                    {kernelUps.some((x) => x.security) && <span className="sysupd-tag sec">{t('gu.tagSecurity')}</span>}
                    <span className="sysupd-tag">{t('gu.tagRestart')}</span>
                    <span className="sysupd-ver" title={kernelUps.map((x) => `${x.name} ${x.available}`).join('\n')}>
                      {t('pkg.kernelPkgs', { n: kernelUps.length, names: kernelUps.map((x) => x.name).join(', ') })}
                    </span>
                  </li>
                )}
                {list.map((x) => (
                  <li key={x.name}>
                    <span className="sysupd-name">{x.name}</span>
                    {x.security && <span className="sysupd-tag sec">{t('gu.tagSecurity')}</span>}
                    {x.held && <span className="sysupd-tag">{t('gu.tagHeld')}</span>}
                    <span className="sysupd-ver">
                      {x.current ? `${x.current} → ` : ''}
                      {x.available}
                    </span>
                  </li>
                ))}
              </ul>
              {rows > SHOWN && (
                <button type="button" className="ghost small sysupd-more" onClick={() => setShowAll((a) => !a)}>
                  {showAll ? t('gu.showFewer') : t('gu.showAll', { n: rows })}
                </button>
              )}
            </div>
          </div>
        </details>
      )}
      {sys && !sys.supported && sys.reason && <p className="muted small glowup-note">{sys.reason}</p>}
      {sys?.error && <p className="muted small glowup-note">{sys.error}</p>}

      {u?.notes && !u.error && nocPending && (
        <details className="settings-section glowup-details">
          <summary className="settings-section-title">{t('sp.whatsNew', { version: vtag(u.latest) })}</summary>
          <div className="settings-card">
            <div className="settings-row full">
              <pre className="set-notes">{plainNotes(u.notes)}</pre>
              {u.url && (
                <a className="set-link" href={u.url} target="_blank" rel="noreferrer">
                  {t('sp.viewGithub')} <Icon name="external" size={12} />
                </a>
              )}
            </div>
          </div>
        </details>
      )}
    </div>
  );
}
