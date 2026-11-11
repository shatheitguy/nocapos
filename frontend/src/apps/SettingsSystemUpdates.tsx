import { tr } from '../i18n';
import { useEffect, useRef, useState } from 'react';
import { api } from '../api/client';
import { Icon } from '../components/Icon';
import { powerHost } from '../lib/hostActions';
import { toast } from '../state/toasts';
import { Section as Group } from './Personalize';
import { confirmChange, confirmRepair, RepairCard, splitKernel, type PkgHealth } from './PackagesRepair';

// Settings → Software Update → System packages: the host's own updates
// (Debian security fixes and the rest), checked and installed as background
// package tasks.

interface SysUpdate {
  name: string;
  current?: string;
  available: string;
  origin?: string;
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
  started_at: string;
  ended_at?: string;
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

const SHOWN = 12; // packages listed before "Show all"

export function SystemPackagesUpdate() {
  const [st, setSt] = useState<SysStatus | null>(null);
  const [job, setJob] = useState<PkgJob | null>(null);
  const [all, setAll] = useState(false);
  const [starting, setStarting] = useState(false);
  const logRef = useRef<HTMLPreElement>(null);

  const load = () =>
    void api<SysStatus>('/api/v1/system/update/packages').then((r) => {
      if (!r.ok) return;
      setSt(r.data);
      if (r.data.job?.running) setJob((j) => j ?? r.data.job ?? null);
    });
  useEffect(load, []);

  // Follow a running check / install.
  const jobId = job?.id;
  const jobRunning = !!job?.running;
  useEffect(() => {
    if (!jobId || !jobRunning) return;
    let stopped = false;
    let timer = 0;
    const tick = async () => {
      const r = await api<PkgJob>(`/api/v1/packages/jobs/${jobId}`);
      if (stopped) return;
      if (r.ok) {
        setJob(r.data);
        if (!r.data.running) {
          const install = r.data.action === 'upgrade-all';
          if (r.data.action === 'repair') {
            if (r.data.ok) toast('success', tr('pkg.repaired'));
            else toast('error', tr('pkg.repairFailed'), r.data.error);
          } else if (r.data.ok) toast('success', install ? tr('sp.sysPkgUpdated') : tr('sp.sysPkgListsRefreshed'));
          else toast('error', install ? 'Some updates didn’t install' : 'Couldn’t check for system updates', r.data.error);
          load();
          return;
        }
      }
      timer = window.setTimeout(() => void tick(), 1500);
    };
    void tick();
    return () => {
      stopped = true;
      window.clearTimeout(timer);
    };
  }, [jobId, jobRunning]);

  useEffect(() => {
    logRef.current?.scrollTo(0, logRef.current.scrollHeight);
  }, [job?.output]);

  const start = async (what: 'refresh' | 'install' | 'repair') => {
    let allow: string[] | null = [];
    setStarting(true);
    if (what === 'install' && st) {
      const n = st.updates.filter((u) => !u.held).length;
      allow = await confirmChange('upgrade-all', [], {
        title: `Install ${n} system update${n === 1 ? '' : 's'}?`,
        message:
          'Runs in the background — NoCapOS and your apps keep going.' +
          (st.health?.broken ? ' The broken packages are repaired first.' : '') +
          (st.kernel ? ' A new kernel is in there, so restart the machine afterwards to finish.' : ''),
        confirmLabel: 'Install',
      });
    } else if (what === 'repair') {
      allow = await confirmRepair();
    }
    if (allow === null) return setStarting(false);
    const r = await api<{ job: PkgJob }>(`/api/v1/system/update/packages/${what}`, {
      method: 'POST',
      body: what === 'refresh' ? undefined : { allow_remove: allow },
    });
    setStarting(false);
    if (r.ok) setJob(r.data.job);
    else toast('error', what === 'install' ? 'The install didn’t start' : what === 'repair' ? tr('pkg.repairStartFailed') : 'The check didn’t start', r.error);
  };

  if (!st) {
    return (
      <Group title="System packages">
        <div className="settings-row full">
          <span className="muted">Checking the system’s packages…</span>
        </div>
      </Group>
    );
  }
  if (!st.supported) {
    return (
      <Group title="System packages">
        <div className="settings-row full">
          <span className="muted">{st.reason || 'System updates can’t be managed from here on this machine.'}</span>
        </div>
      </Group>
    );
  }

  const busy = starting || !!job?.running;
  const installing = !!job?.running && (job.action === 'upgrade-all' || job.action === 'repair');
  const pending = st.updates.filter((u) => !u.held);
  const security = pending.filter((u) => u.security).length;
  const { kernel: kernelUps, rest } = splitKernel(st.updates);
  const list = all ? rest : rest.slice(0, SHOWN - (kernelUps.length ? 1 : 0));
  const checked = st.checked ? new Date(st.checked) : null;

  let headline: string;
  let detail: string;
  if (job?.running) {
    headline = installing ? 'Installing system updates…' : 'Checking for system updates…';
    detail = installing ? tr('sp.sysPkgInstalling') : tr('sp.sysPkgChecking');
  } else if (st.error) {
    headline = 'Couldn’t read the system updates';
    detail = st.error;
  } else if (pending.length === 0) {
    headline = 'System packages are up to date';
    detail = tr('sp.sysPkgNone');
  } else {
    headline = `${pending.length} update${pending.length === 1 ? '' : 's'} available`;
    detail =
      security > 0
        ? `${security} of them ${security === 1 ? 'is a security fix' : 'are security fixes'} — worth installing soon.`
        : 'Bug fixes and improvements from your distribution.';
  }

  return (
    <Group title="System packages" hint={`Updates for the system under NoCapOS${st.manager ? ` (${st.manager})` : ''}. Your files and settings stay put.`}>
      <div className="settings-row full sysupd">
        <div className="sysupd-head">
          <span className={`sysupd-icon ${security > 0 ? 'warn' : pending.length === 0 ? 'ok' : ''}`}>
            <Icon name={security > 0 ? 'shield' : pending.length === 0 && !job?.running ? 'check' : 'download'} size={18} />
          </span>
          <div className="set-update-text">
            <b>{headline}</b>
            <span>{detail}</span>
            {checked && <small>Lists refreshed {checked.toLocaleString()}</small>}
          </div>
          <div className="btn-group">
            {pending.length > 0 && !job?.running && (
              <button type="button" className="pill small" disabled={busy} onClick={() => void start('install')}>
                <Icon name="download" size={13} /> Install updates
              </button>
            )}
            <button type="button" className="pill ghost small" disabled={busy} onClick={() => void start('refresh')}>
              {busy && !installing ? <span className="spinner sm" /> : <Icon name="restart" size={13} />} Check now
            </button>
          </div>
        </div>

        {job?.running && <div className="sysupd-bar" role="progressbar" aria-label={headline} />}

        {st.health && !job?.running && <RepairCard health={st.health} busy={busy} onRepair={() => void start('repair')} />}

        {st.restart_needed && !job?.running && (
          <div className="sysupd-restart">
            <Icon name="power" size={15} />
            <span>A new kernel is installed. Restart the machine to switch to it.</span>
            <button type="button" className="pill small" onClick={() => void powerHost('reboot')}>
              Restart
            </button>
          </div>
        )}
        {!st.restart_needed && st.kernel && pending.length > 0 && !job?.running && (
          <p className="sysupd-note">
            <Icon name="info" size={13} /> Includes a kernel update — restart the machine after installing.
          </p>
        )}

        {st.updates.length > 0 && (
          <ul className="sysupd-list">
            {kernelUps.length > 0 && (
              <li className="sysupd-kernel">
                <span className="sysupd-name">{tr('pkg.kernelRow')}</span>
                {kernelUps.some((u) => u.security) && <span className="sysupd-tag sec">Security</span>}
                <span className="sysupd-tag">Restart needed</span>
                <span className="sysupd-ver" title={kernelUps.map((u) => `${u.name} ${u.available}`).join('\n')}>
                  {tr('pkg.kernelPkgs', { n: kernelUps.length, names: kernelUps.map((u) => u.name).join(', ') })}
                </span>
              </li>
            )}
            {list.map((u) => (
              <li key={u.name}>
                <span className="sysupd-name">{u.name}</span>
                {u.security && <span className="sysupd-tag sec">Security</span>}
                {u.kernel && <span className="sysupd-tag">Kernel</span>}
                {u.held && <span className="sysupd-tag">Held</span>}
                <span className="sysupd-ver">
                  {u.current ? `${u.current} → ` : ''}
                  {u.available}
                </span>
              </li>
            ))}
          </ul>
        )}
        {rest.length + (kernelUps.length ? 1 : 0) > SHOWN && (
          <button type="button" className="ghost small sysupd-more" onClick={() => setAll((a) => !a)}>
            {all ? 'Show fewer' : `Show all ${rest.length + (kernelUps.length ? 1 : 0)}`}
          </button>
        )}

        {job?.output && (
          <details className="set-update-log" open={!!job.running}>
            <summary>{job.running ? 'Live output' : 'Output of the last task'}</summary>
            <pre ref={logRef} className="set-logs">
              {job.output}
            </pre>
          </details>
        )}
      </div>
    </Group>
  );
}
