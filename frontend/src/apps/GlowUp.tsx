import { useEffect, useRef, useState } from 'react';
import { tr, useT } from '../i18n';
import { api } from '../api/client';
import { Icon } from '../components/Icon';
import { Logo } from '../components/Logo';
import { powerHost } from '../lib/hostActions';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import { missing } from '../lib/settingsKit';
import { openUpdate, resumeRunningUpdate, useUpdate, vtag, type UpdateRunStatus } from '../state/update';
import { useStoreApps } from '../state/storeApps';
import type { StoreApp } from '../api/appstore';
import { AppIcon } from '../components/AppTile';
import { confirmChange, splitKernel, type PkgHealth } from './PackagesRepair';
import { ImageGlowUp, type OSStatus } from './GlowUpImage';

// Settings → Glow Up: one update for everything. One check looks at
// NoCapOS, the system's packages and the App Store apps together; one list
// says what will change (grouped, details folded away); one button installs
// it all as a single run on the server (POST /api/v1/system/update/all): the
// apps, then a package repair when needed (automatic: it only asks when
// something would be removed), the system updates, then NoCapOS itself,
// which restarts it; and one result.

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
  phase: 'apps' | 'packages' | 'nocapos' | 'done';
  packages: number;
  security: number;
  repair?: boolean;
  kernel?: boolean;
  apps?: string[];
  apps_done?: number;
  apps_failed?: string[];
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

/** The count on the folded list: "NoCapOS · 2 apps · 14 packages". */
function changeCount(t: (k: string, v?: Record<string, string | number>) => string, noc: boolean, apps: number, pkgs: number, repair: boolean) {
  return [noc ? 'NoCapOS' : '', apps ? t('gu.partApps', { n: apps }) : '', pkgs ? t('gu.partSys', { n: pkgs }) : '', repair && !pkgs ? t('gu.stepRepair') : '']
    .filter(Boolean)
    .join(' · ');
}

/** What changes for an app: its version, or a newer image of the same one. */
function appChange(t: (k: string, v?: Record<string, string | number>) => string, a: StoreApp) {
  const cur = a.installed?.version ?? '';
  if (a.installed?.image_update && cur === a.version) return t('au.newImage', { version: a.version });
  return `v${cur} → v${a.version}`;
}

/** Asks the App Store apps' registries too (a check is one check for everything). */
async function storeApiCheck() {
  await api('/api/v1/appstore/updates/check', { method: 'POST' });
  await useStoreApps.getState().load();
}

function ScriptGlowUp() {
  const t = useT();
  const [u, setU] = useState<UpdateInfo | null>(null);
  const [sys, setSys] = useState<SysStatus | null>(null);
  const [run, setRun] = useState<UpdateRunStatus | null>(null);
  const [all, setAll] = useState<AllStatus | null>(null);
  const [checking, setChecking] = useState(false);
  const [starting, setStarting] = useState(false);
  const [showAll, setShowAll] = useState(false);
  const [relErr, setRelErr] = useState<string | null>(null);
  const updating = useUpdate((s) => s.phase !== 'idle');
  const logRef = useRef<HTMLPreElement>(null);
  // App Store apps with an update (a newer version or a newer image).
  const storeApps = useStoreApps((s) => s.apps);
  const appUps = storeApps.filter((a) => a.installed?.update_available && !(a.job && !a.job.done));

  const loadRelease = async (refresh: boolean) => {
    const r = await api<UpdateInfo>(`/api/v1/system/update${refresh ? '?refresh=1' : ''}`);
    const ok = r.ok && !missing(r);
    if (ok) {
      setU(r.data);
      setRelErr(null);
    } else {
      setRelErr(missing(r) ? tr('set.notAvailable') : (r.error ?? tr('sp.updCantCheck')));
      if (refresh) toast('error', tr('sp.updCheckFail'), r.error);
    }
    return ok ? r.data : null;
  };
  const loadSys = async () => {
    const r = await api<SysStatus>('/api/v1/system/update/packages');
    const ok = r.ok && !missing(r);
    // A server that can't list the system's packages just leaves them out.
    setSys(ok ? r.data : { supported: false, updates: [], security: 0, kernel: false, restart_needed: false, error: missing(r) ? undefined : r.error });
    return ok ? r.data : null;
  };
  const loadAll = async () => {
    const r = await api<AllStatus>('/api/v1/system/update/all');
    if (r.ok && !missing(r)) setAll(r.data);
    return r.ok ? r.data : null;
  };

  useEffect(() => {
    void loadRelease(false);
    void loadSys();
    void loadAll();
    void useStoreApps.getState().load();
  }, []);
  useEffect(() => {
    if (updating) return;
    void api<UpdateRunStatus>('/api/v1/system/update/run').then((r) => {
      if (!r.ok || missing(r)) return;
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
          void useStoreApps.getState().load();
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
    if (checking) return;
    setChecking(true);
    const refresh = sys?.supported
      ? api<{ job: PkgJob }>('/api/v1/system/update/packages/refresh', { method: 'POST' }).then(async (r) => {
          if (r.ok) await waitJob(r.data.job.id);
        })
      : Promise.resolve();
    await Promise.all([loadRelease(true), refresh, storeApiCheck()]);
    await loadSys();
    setChecking(false);
  };

  const nocPending = !!u && (u.available || u.dev) && !u.error && !!u.latest && !!run?.supported;
  const pending = sys?.supported ? sys.updates.filter((x) => !x.held) : [];
  const security = pending.filter((x) => x.security).length;
  const broken = !!sys?.health?.broken;
  const kernel = pending.some((x) => x.kernel);
  const anything = nocPending || pending.length > 0 || broken || appUps.length > 0;
  const busy = checking || starting || allRunning || updating || !!sys?.job?.running || !!run?.running;

  // One button: everything at once.
  const updateAll = async () => {
    const target = nocPending ? u!.latest! : '';
    const what = [
      ...(appUps.length ? [t('gu.askApps', { n: appUps.length })] : []),
      ...(pending.length ? [t('gu.askSys', { n: pending.length })] : []),
      ...(target ? [t('gu.askNoc', { version: vtag(target) })] : []),
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
      body: { target: target || undefined, allow_remove: allow, apps: appUps.map((a) => a.id) },
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
    detail =
      ar.phase === 'apps'
        ? t('gu.runningApps', { done: Math.min((ar.apps_done ?? 0) + 1, ar.apps?.length ?? 0), n: ar.apps?.length ?? 0 })
        : ar.phase === 'packages'
          ? t('gu.runningBody')
          : '';
  } else if (checking) {
    headline = t('gu.checking');
  } else if (!u && relErr) {
    headline = t('sp.updCantCheck');
    detail = relErr;
  } else if (!u || !sys) {
    headline = t('gu.checking');
  } else if (nocPending && u.dev && !pending.length && !appUps.length) {
    headline = t('sp.updDev');
    detail = t('sp.updDevBody', { current: u.current, latest: vtag(u.latest) });
  } else if (nocPending || pending.length || appUps.length) {
    // One line for everything waiting.
    const parts = [
      ...(nocPending ? [t('gu.partNoc', { version: vtag(u.latest) })] : []),
      ...(pending.length ? [t('gu.partSys', { n: pending.length }) + (security ? t('gu.sec', { n: security }) : '')] : []),
      ...(appUps.length ? [t('gu.partApps', { n: appUps.length })] : []),
    ];
    headline = t('gu.sumParts', { parts: parts.join(' · ') });
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
  type StepState = 'done' | 'now' | 'todo' | 'bad';
  const steps: { id: string; label: string; state: StepState }[] = [];
  if (ar && (allRunning || ar.phase === 'done')) {
    const order = { apps: 0, packages: 1, nocapos: 2, done: 3 }[ar.phase];
    const hasSys = ar.packages > 0 || !!ar.repair;
    const failed = ar.phase === 'done' && !ar.ok;
    const appsBad = !!ar.apps_failed?.length;
    const sysBad = failed && hasSys && !!all?.job && !all.job.ok;
    const st = (i: number, bad: boolean): StepState => (order === i ? 'now' : order < i ? 'todo' : bad ? 'bad' : 'done');
    if (ar.apps?.length) steps.push({ id: 'apps', label: t('gu.stepApps', { n: ar.apps.length }), state: st(0, appsBad) });
    if (ar.repair) steps.push({ id: 'repair', label: t('gu.stepRepair'), state: st(1, sysBad) });
    if (ar.packages > 0) steps.push({ id: 'sys', label: t('gu.stepSys', { n: ar.packages }), state: st(1, sysBad) });
    if (ar.target) steps.push({ id: 'noc', label: t('gu.stepNoc', { version: vtag(ar.target) }), state: st(2, failed && !sysBad && !appsBad) });
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


        {(all?.job?.output || (run?.log && !ar)) && (
          <details className="set-update-log" open={allRunning}>
            <summary>{allRunning ? t('gu.liveOutput') : t('sp.showDetails')}</summary>
            <pre ref={logRef} className="set-logs">
              {all?.job?.output || run?.log}
            </pre>
          </details>
        )}
      </div>

      {anything && !allRunning && (
        <details className="settings-section glowup-details">
          <summary className="settings-section-title">
            {t('gu.details')} <span className="muted">{changeCount(t, nocPending, appUps.length, pending.length, broken)}</span>
          </summary>
          <div className="settings-card">
            {nocPending && u && (
              <div className="settings-row full glowup-group">
                <span className="glowup-group-title">NoCapOS</span>
                <ul className="sysupd-list">
                  <li>
                    <span className="sysupd-name">NoCapOS</span>
                    <span className="sysupd-ver">
                      {vtag(u.current)} → {vtag(u.latest)}
                    </span>
                  </li>
                </ul>
                {u.notes && !u.error && (
                  <details className="glowup-notes">
                    <summary>{t('sp.whatsNew', { version: vtag(u.latest) })}</summary>
                    <pre className="set-notes">{plainNotes(u.notes)}</pre>
                    {u.url && (
                      <a className="set-link" href={u.url} target="_blank" rel="noreferrer">
                        {t('sp.viewGithub')} <Icon name="external" size={12} />
                      </a>
                    )}
                  </details>
                )}
              </div>
            )}
            {appUps.length > 0 && (
              <div className="settings-row full glowup-group">
                <span className="glowup-group-title">
                  {t('gu.groupApps')} <span className="glowup-badge">{appUps.length}</span>
                </span>
                <ul className="sysupd-list">
                  {appUps.map((a) => (
                    <li key={a.id}>
                      <span className="sysupd-name glowup-app">
                        <AppIcon app={a} size={20} /> {a.name}
                      </span>
                      <span className="sysupd-ver">{appChange(t, a)}</span>
                    </li>
                  ))}
                </ul>
              </div>
            )}
            {(broken || (sys?.supported && sys.updates.length > 0)) && (
              <div className="settings-row full sysupd glowup-group">
                <span className="glowup-group-title">
                  {t('gu.groupSys')}
                  {sys?.manager && <span className="muted"> · {sys.manager}</span>}
                </span>
                <ul className="sysupd-list">
                  {broken && (
                    <li className="sysupd-kernel">
                      <span className="sysupd-name">{t('gu.repairRow')}</span>
                      <span className="sysupd-tag">{t('gu.tagAuto')}</span>
                    </li>
                  )}
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
            )}
          </div>
        </details>
      )}
      {sys && !sys.supported && sys.reason && <p className="muted small glowup-note">{sys.reason}</p>}
      {sys?.error && <p className="muted small glowup-note">{sys.error}</p>}
    </div>
  );
}

/** Glow Up: whole-system updates on an image install, else the script
 *  install's NoCapOS + system packages view. */
export function SoftwareUpdate() {
  const [os, setOs] = useState<OSStatus | null | undefined>(undefined);
  useEffect(() => {
    void api<OSStatus>('/api/v1/system/os').then((r) => setOs(r.ok && !missing(r) ? r.data : null));
  }, []);
  if (os === undefined) return <div className="stack settings-page"><span className="spinner" /></div>;
  if (os?.image) return <ImageGlowUp initial={os} />;
  return <ScriptGlowUp />;
}
