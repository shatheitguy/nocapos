import { useCallback, useEffect, useRef, useState } from 'react';
import { pkgApi, type Pkg, type PkgAction, type PkgFeature, type PkgJob, type PkgStatus, type PkgUpdate } from '../api/packages';
import { Icon, type IconName } from '../components/Icon';
import { fmtBytes } from '../lib/format';
import { toast } from '../state/toasts';
import { confirmDialog } from '../state/confirm';
import { TypeConfirm } from './StorageParts';

// Packages: this server's own system packages (apt, dnf, pacman or zypper).
// Admin only. Every change runs as a background task on the server, one at a
// time, with its output shown here and an entry in the audit log.

type View = 'overview' | 'installed' | 'updates';
const PAGE = 100;

const ACTION_LABEL: Record<string, string> = {
  refresh: 'Checking for updates',
  'upgrade-all': 'Installing all updates',
  upgrade: 'Updating',
  install: 'Installing',
  remove: 'Removing',
  hold: 'Holding',
  unhold: 'Releasing',
  autoremove: 'Removing unused packages',
  clean: 'Clearing the download cache',
  'install-file': 'Installing',
  feature: 'Installing',
};

export function Packages() {
  const [status, setStatus] = useState<PkgStatus | null>(null);
  const [features, setFeatures] = useState<PkgFeature[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [view, setView] = useState<View>('overview');
  const [job, setJob] = useState<PkgJob | null>(null);
  const [reloadKey, setReloadKey] = useState(0);
  const timer = useRef<number | undefined>(undefined);

  const loadStatus = useCallback(async () => {
    const r = await pkgApi.status();
    if (!r.ok) return setError(r.error ?? 'Couldn’t reach NoCapOS');
    setError(null);
    setStatus(r.data.status);
    setFeatures(r.data.features);
    const running = r.data.jobs.find((j) => j.running);
    if (running) follow(running.id);
  }, []);

  useEffect(() => {
    void loadStatus();
    return () => window.clearTimeout(timer.current);
  }, [loadStatus]);

  const follow = (id: string) => {
    window.clearTimeout(timer.current);
    const poll = async () => {
      const r = await pkgApi.job(id);
      if (!r.ok) return;
      setJob(r.data);
      if (r.data.running) {
        timer.current = window.setTimeout(() => void poll(), 1000);
      } else {
        if (r.data.ok) toast('success', `${ACTION_LABEL[r.data.action] ?? r.data.action} finished`, r.data.target);
        else toast('error', `${ACTION_LABEL[r.data.action] ?? r.data.action} failed`, r.data.error);
        setReloadKey((k) => k + 1);
        void loadStatus();
      }
    };
    void poll();
  };

  const start = async (p: Promise<{ ok: boolean; data: { job: PkgJob }; error?: string }>) => {
    const r = await p;
    if (!r.ok) {
      toast('error', 'Couldn’t start that', r.error);
      return false;
    }
    setJob(r.data.job);
    follow(r.data.job.id);
    return true;
  };
  const busy = !!job?.running;

  if (error && !status) {
    return (
      <div className="pk-app st-center">
        <p className="muted">{error}</p>
      </div>
    );
  }
  if (!status) {
    return (
      <div className="pk-app st-center">
        <span className="st-spinner" />
      </div>
    );
  }
  if (!status.supported) {
    return (
      <div className="pk-app st-unsupported">
        <div className="st-unsupported-card">
          <span className="st-hero-icon">
            <Icon name="archiveBox" size={34} />
          </span>
          <h1 className="app-title">Packages</h1>
          <p className="st-lead">{status.reason}</p>
        </div>
      </div>
    );
  }

  const nav: { id: View; label: string; icon: IconName }[] = [
    { id: 'overview', label: 'Overview', icon: 'home' },
    { id: 'installed', label: 'Installed', icon: 'archiveBox' },
    { id: 'updates', label: 'Updates', icon: 'download' },
  ];

  return (
    <div className="pk-app app-split">
      <nav className="sidebar pk-side">
        <div className="ct-side-head">
          <h1 className="app-title">Packages</h1>
          <span className="app-subtitle">{status.manager}</span>
        </div>
        {nav.map((n) => (
          <button key={n.id} type="button" className={view === n.id ? 'on' : ''} onClick={() => setView(n.id)}>
            <Icon name={n.icon} size={16} />
            <span className="ct-nav-label">{n.label}</span>
          </button>
        ))}
      </nav>
      <main className="app-content pk-main">
        {job && <JobPanel job={job} onClose={() => !job.running && setJob(null)} />}
        {view === 'overview' && <Overview status={status} features={features} busy={busy} start={start} />}
        {view === 'installed' && <Installed key={reloadKey} status={status} busy={busy} start={start} />}
        {view === 'updates' && <Updates key={reloadKey} busy={busy} start={start} />}
      </main>
    </div>
  );
}

type Start = (p: Promise<{ ok: boolean; data: { job: PkgJob }; error?: string }>) => Promise<boolean>;

function JobPanel({ job, onClose }: { job: PkgJob; onClose: () => void }) {
  const out = useRef<HTMLPreElement>(null);
  useEffect(() => {
    if (out.current) out.current.scrollTop = out.current.scrollHeight;
  }, [job.output]);
  return (
    <div className="ucard pk-job">
      <div className="pk-job-head">
        {job.running ? <span className="pk-spin" /> : <Icon name={job.ok ? 'check' : 'alert'} size={16} />}
        <b>
          {ACTION_LABEL[job.action] ?? job.action}
          {job.target ? ` ${job.target}` : ''}
          {job.running ? '…' : job.ok ? ' — done' : ' — failed'}
        </b>
        {!job.running && (
          <button type="button" className="ghost icon-btn" aria-label="Close" onClick={onClose}>
            <Icon name="close" size={14} />
          </button>
        )}
      </div>
      {job.error && <span className="error small">{job.error}</span>}
      <pre ref={out} className="pk-output">{job.output || 'Starting…'}</pre>
    </div>
  );
}

function Overview({ status, features, busy, start }: { status: PkgStatus; features: PkgFeature[]; busy: boolean; start: Start }) {
  const [name, setName] = useState('');
  const [progress, setProgress] = useState<number | null>(null);
  const file = useRef<HTMLInputElement>(null);
  const validName = /^[a-zA-Z0-9][a-zA-Z0-9+._@:~-]{0,127}$/.test(name.trim());

  const upload = async (f: File) => {
    if (!status.file_types || !f.name.endsWith(status.file_types)) return toast('error', `Choose a ${status.file_types} file`);
    const ok = await confirmDialog({
      title: `Install ${f.name}?`,
      message: 'NoCapOS checks the file with the package tool, then installs it as root with any packages it needs. Only install files you trust.',
      confirmLabel: 'Install',
    });
    if (!ok) return;
    setProgress(0);
    await start(pkgApi.upload(f, setProgress));
    setProgress(null);
  };

  const maint: { action: PkgAction; label: string; desc: string; icon: IconName }[] = [
    { action: 'refresh', label: 'Check for updates', desc: 'Download the latest package lists.', icon: 'restart' },
    { action: 'upgrade-all', label: 'Install all updates', desc: 'Update every package that has a newer version.', icon: 'download' },
  ];
  if (status.manager !== 'zypper') maint.push({ action: 'autoremove', label: 'Remove unused packages', desc: 'Packages that were installed as dependencies and are no longer needed.', icon: 'trash' });
  maint.push({ action: 'clean', label: 'Clear download cache', desc: 'Delete downloaded package files to free space.', icon: 'archiveBox' });

  return (
    <div className="st-page">
      <span className="section-label">NoCapOS features</span>
      <div className="ucard ulist pk-features">
        {features.map((f) => (
          <div key={f.id} className="urow">
            <span className="urow-icon">
              <Icon name={f.installed ? 'check' : 'wrench'} size={16} />
            </span>
            <span className="urow-text">
              <span className="urow-title">{f.title}</span>
              <span className="urow-desc">{f.installed ? 'Installed' : f.can_install ? 'Not installed' : 'Not available from this system’s packages'}</span>
            </span>
            {f.can_install && (
              <span className="urow-control">
                <button type="button" className={`small ${f.installed ? 'ghost' : ''}`} disabled={busy} onClick={() => void start(pkgApi.installFeature(f.id))}>
                  {f.installed ? 'Repair' : 'Install'}
                </button>
              </span>
            )}
          </div>
        ))}
      </div>

      <span className="section-label">Maintenance</span>
      <div className="ucard ulist">
        {maint.map((m) => (
          <div key={m.action} className="urow">
            <span className="urow-icon">
              <Icon name={m.icon} size={16} />
            </span>
            <span className="urow-text">
              <span className="urow-title">{m.label}</span>
              <span className="urow-desc">{m.desc}</span>
            </span>
            <span className="urow-control">
              <button
                type="button"
                className="small ghost"
                disabled={busy}
                onClick={async () => {
                  if (m.action === 'upgrade-all' || m.action === 'autoremove') {
                    const ok = await confirmDialog({ title: `${m.label}?`, message: m.desc, confirmLabel: m.label });
                    if (!ok) return;
                  }
                  void start(pkgApi.act(m.action));
                }}
              >
                Run
              </button>
            </span>
          </div>
        ))}
      </div>

      <span className="section-label">Install</span>
      <div className="ucard pad">
        <form
          className="pk-drop"
          onSubmit={async (e) => {
            e.preventDefault();
            if (!validName) return;
            const ok = await confirmDialog({ title: `Install ${name.trim()}?`, message: `NoCapOS installs it with ${status.manager}, with the packages it needs.`, confirmLabel: 'Install' });
            if (ok && (await start(pkgApi.act('install', name.trim())))) setName('');
          }}
        >
          <label className="search compact">
            <Icon name="archiveBox" size={15} />
            <input placeholder="Package name, e.g. htop" value={name} onChange={(e) => setName(e.target.value)} spellCheck={false} autoComplete="off" />
          </label>
          <button type="submit" className="small" disabled={busy || !validName}>
            Install
          </button>
          {status.file_types && (
            <>
              <span className="muted small">or</span>
              <button type="button" className="small ghost" disabled={busy || progress != null} onClick={() => file.current?.click()}>
                <Icon name="upload" size={14} /> {progress != null ? `Uploading ${progress}%` : `Install a ${status.file_types} file…`}
              </button>
              <input
                ref={file}
                type="file"
                accept={status.file_types}
                hidden
                onChange={(e) => {
                  const f = e.target.files?.[0];
                  e.target.value = '';
                  if (f) void upload(f);
                }}
              />
            </>
          )}
        </form>
      </div>
    </div>
  );
}

function Installed({ status, busy, start }: { status: PkgStatus; busy: boolean; start: Start }) {
  const [q, setQ] = useState('');
  const [held, setHeld] = useState(false);
  const [offset, setOffset] = useState(0);
  const [data, setData] = useState<{ packages: Pkg[]; total: number; installed: number; size: number } | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [removing, setRemoving] = useState<Pkg | null>(null);

  useEffect(() => {
    const t = window.setTimeout(async () => {
      const r = await pkgApi.list(q.trim(), offset, PAGE, held);
      if (!r.ok) return setErr(r.error ?? 'Couldn’t list packages');
      setErr(null);
      setData(r.data);
    }, 250);
    return () => window.clearTimeout(t);
  }, [q, offset, held]);

  return (
    <div className="st-page">
      <div className="pk-head">
        <h2>Installed</h2>
        {data && (
          <span className="muted small">
            {data.installed} packages · {fmtBytes(data.size)}
          </span>
        )}
      </div>
      <div className="ct-toolbar">
        <label className="search compact ct-search">
          <Icon name="search" size={15} />
          <input
            placeholder="Search name or description"
            value={q}
            onChange={(e) => {
              setQ(e.target.value);
              setOffset(0);
            }}
          />
        </label>
        {status.can_hold && (
          <label className="toggle">
            <input
              type="checkbox"
              checked={held}
              onChange={(e) => {
                setHeld(e.target.checked);
                setOffset(0);
              }}
            />{' '}
            Held only
          </label>
        )}
      </div>
      {err && <div className="st-note bad">{err}</div>}
      {!data ? (
        <p className="muted">Loading…</p>
      ) : (
        <>
          <div className="ucard ct-table-wrap">
            <table className="table pk-table">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Version</th>
                  <th>Size</th>
                  <th>Description</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {data.packages.map((p) => (
                  <tr key={p.name}>
                    <td className="name">
                      {p.name}
                      {p.held && <span className="chip tiny warn">held</span>}
                    </td>
                    <td className="mono small">{p.version}</td>
                    <td className="num small">{fmtBytes(p.size)}</td>
                    <td className="desc small" title={p.description}>{p.description}</td>
                    <td>
                      <div className="pk-row-actions">
                        {status.can_hold && (
                          <button type="button" className="ghost small" disabled={busy} onClick={() => void start(pkgApi.act(p.held ? 'unhold' : 'hold', p.name))}>
                            {p.held ? 'Release' : 'Hold'}
                          </button>
                        )}
                        <button type="button" className="ghost small danger" disabled={busy} onClick={() => setRemoving(p)}>
                          Remove
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            {!data.packages.length && <p className="muted st-pad">No packages match.</p>}
          </div>
          <div className="pk-pager">
            <span>
              {data.total ? `${offset + 1}–${Math.min(offset + PAGE, data.total)} of ${data.total}` : '0'}
            </span>
            <button type="button" className="ghost small" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - PAGE))}>
              <Icon name="chevronLeft" size={13} />
            </button>
            <button type="button" className="ghost small" disabled={offset + PAGE >= data.total} onClick={() => setOffset(offset + PAGE)}>
              <Icon name="chevronRight" size={13} />
            </button>
          </div>
        </>
      )}
      {removing && (
        <TypeConfirm
          title={`Remove ${removing.name}?`}
          word={removing.name}
          action="Remove package"
          message={
            <>
              <b>{removing.name} is uninstalled from this server.</b>
              <p>Programs that depend on it may be removed with it or stop working. Packages the system or NoCapOS needs are refused.</p>
            </>
          }
          onClose={() => setRemoving(null)}
          onConfirm={async () => {
            const r = await pkgApi.act('remove', removing.name, removing.name);
            if (!r.ok) return r.error ?? 'Couldn’t remove it';
            setRemoving(null);
            void start(Promise.resolve(r));
            return null;
          }}
        />
      )}
    </div>
  );
}

function Updates({ busy, start }: { busy: boolean; start: Start }) {
  const [ups, setUps] = useState<PkgUpdate[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  useEffect(() => {
    void pkgApi.updates().then((r) => {
      if (r.ok) setUps(r.data.updates);
      else setErr(r.error ?? 'Couldn’t read updates');
    });
  }, []);
  return (
    <div className="st-page">
      <div className="pk-head">
        <h2>Updates</h2>
        <button type="button" className="ghost small" disabled={busy} onClick={() => void start(pkgApi.act('refresh'))}>
          <Icon name="restart" size={14} /> Check now
        </button>
        <button
          type="button"
          className="small"
          disabled={busy || !ups?.length}
          onClick={async () => {
            if (await confirmDialog({ title: 'Install all updates?', message: `${ups?.length ?? 0} packages will be updated.`, confirmLabel: 'Update all' })) void start(pkgApi.act('upgrade-all'));
          }}
        >
          <Icon name="download" size={14} /> Update all
        </button>
      </div>
      {err && <div className="st-note bad">{err}</div>}
      {!ups ? (
        !err && <p className="muted">Loading…</p>
      ) : ups.length === 0 ? (
        <p className="muted">Everything is up to date (as of the last check).</p>
      ) : (
        <div className="ucard ct-table-wrap">
          <table className="table pk-table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Installed</th>
                <th>Available</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {ups.map((u) => (
                <tr key={u.name}>
                  <td className="name">
                    {u.name}
                    {u.held && <span className="chip tiny warn">held</span>}
                  </td>
                  <td className="mono small">{u.current || '–'}</td>
                  <td className="mono small">{u.available}</td>
                  <td>
                    <div className="pk-row-actions">
                      <button type="button" className="ghost small" disabled={busy || u.held} onClick={() => void start(pkgApi.act('upgrade', u.name))}>
                        Update
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
