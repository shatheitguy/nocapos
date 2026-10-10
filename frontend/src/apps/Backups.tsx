import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  backupApi,
  downloadFromBackup,
  type BackupJob,
  type BackupKeep,
  type BackupPlan,
  type BackupRepo,
  type BackupSchedule,
  type BackupSource,
  type BackupStatus,
  type NewRepo,
  type RepoKind,
  type SnapNode,
  type Snapshot,
} from '../api/backup';
import { fileApi, type FileRoot } from '../api/files';
import { Icon, type IconName } from '../components/Icon';
import { fmtAgo, fmtBytes } from '../lib/format';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import type { WinState } from '../state/windows';
import { Choice, Row, Section, Toggle } from './Personalize';

type Page =
  | { kind: 'plans' }
  | { kind: 'edit'; plan: BackupPlan | null }
  | { kind: 'repos' }
  | { kind: 'add-repo'; then?: 'edit' }
  | { kind: 'rewind'; planId?: number };

const WEEKDAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
const KIND_ICON: Record<RepoKind, IconName> = { local: 'drive', sftp: 'network', s3: 'globe' };
const KIND_LABEL: Record<RepoKind, string> = { local: 'Drive or folder', sftp: 'Another server', s3: 'Cloud storage' };
const RECOMMENDED: Record<BackupSchedule['every'], BackupKeep> = {
  hour: { hourly: 24, daily: 7, weekly: 4, monthly: 12 },
  day: { daily: 7, weekly: 4, monthly: 12 },
  week: { weekly: 8, monthly: 12 },
  manual: { daily: 7, weekly: 4, monthly: 12 },
};
const dateTime = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' });
const dayFmt = new Intl.DateTimeFormat(undefined, { weekday: 'long', day: 'numeric', month: 'long', year: 'numeric' });
const timeFmt = new Intl.DateTimeFormat(undefined, { timeStyle: 'short' });

const isZero = (t?: string) => !t || t.startsWith('0001-');

function scheduleText(s: BackupSchedule) {
  switch (s.every) {
    case 'hour':
      return 'Every hour';
    case 'day':
      return `Every day at ${s.at}`;
    case 'week':
      return `Every ${WEEKDAYS[s.weekday ?? 0]} at ${s.at}`;
    default:
      return 'Only when you click Back Up Now';
  }
}

function keepText(k: BackupKeep) {
  const parts = [
    k.hourly && `${k.hourly} hourly`,
    k.daily && `${k.daily} daily`,
    k.weekly && `${k.weekly} weekly`,
    k.monthly && `${k.monthly} monthly`,
    k.yearly && `${k.yearly} yearly`,
  ].filter(Boolean);
  return parts.length ? `keeps ${parts.join(', ')}` : 'keeps every backup';
}

/** Backups: automatic, encrypted backups with restic, and Rewind to restore old versions. */
export function Backups(_: { win: WinState }) {
  const [status, setStatus] = useState<BackupStatus | null>(null);
  const [repos, setRepos] = useState<BackupRepo[]>([]);
  const [plans, setPlans] = useState<BackupPlan[]>([]);
  const [roots, setRoots] = useState<FileRoot[]>([]);
  const [page, setPage] = useState<Page>({ kind: 'plans' });
  const [error, setError] = useState('');
  const [installing, setInstalling] = useState(false);

  const load = useCallback(async () => {
    const r = await backupApi.overview();
    if (!r.ok) return setError(r.error ?? 'Could not load backups');
    setError('');
    setStatus(r.data.status);
    setRepos(r.data.repos);
    setPlans(r.data.plans);
  }, []);

  useEffect(() => {
    void load();
    void fileApi.roots().then((r) => r.ok && setRoots(r.data));
  }, [load]);

  // Refresh often while something runs, otherwise now and then.
  const busy = plans.some((p) => p.job && !p.job.done);
  useEffect(() => {
    const t = window.setTimeout(() => void load(), busy ? 1500 : 15000);
    return () => window.clearTimeout(t);
  }, [busy, plans, load]);

  const install = async () => {
    setInstalling(true);
    const r = await backupApi.install();
    setInstalling(false);
    if (!r.ok) return toast('error', 'Could not install restic', r.error);
    toast('success', 'restic installed', 'Backups are ready to set up.');
    void load();
  };

  const nav = (p: Page, icon: IconName, label: string) => (
    <button
      type="button"
      className={page.kind === p.kind || (p.kind === 'plans' && page.kind === 'edit') || (p.kind === 'repos' && page.kind === 'add-repo') ? 'on' : ''}
      onClick={() => setPage(p)}
    >
      <Icon name={icon} size={16} /> {label}
    </button>
  );

  return (
    <div className="app-split backups">
      <nav className="sidebar bk-sidebar">
        {nav({ kind: 'plans' }, 'save', 'Backups')}
        {nav({ kind: 'rewind' }, 'rewind', 'Rewind')}
        {nav({ kind: 'repos' }, 'drive', 'Destinations')}
        {status?.installed && <p className="bk-engine">restic {status.version}</p>}
      </nav>
      <div className="app-content bk-content">
        {error && <p className="error">{error}</p>}
        {!status ? (
          <div className="bk-center">
            <span className="spinner" />
          </div>
        ) : !status.installed ? (
          <NoEngine status={status} installing={installing} onInstall={() => void install()} />
        ) : page.kind === 'plans' ? (
          <PlanList
            plans={plans}
            repos={repos}
            roots={roots}
            onNew={() => setPage(repos.length ? { kind: 'edit', plan: null } : { kind: 'add-repo', then: 'edit' })}
            onEdit={(p) => setPage({ kind: 'edit', plan: p })}
            onRewind={(p) => setPage({ kind: 'rewind', planId: p.id })}
            reload={load}
          />
        ) : page.kind === 'edit' ? (
          <PlanEditor
            plan={page.plan}
            repos={repos}
            roots={roots}
            onAddRepo={() => setPage({ kind: 'add-repo', then: 'edit' })}
            onDone={() => {
              setPage({ kind: 'plans' });
              void load();
            }}
          />
        ) : page.kind === 'repos' ? (
          <RepoList repos={repos} plans={plans} sshKey={status.ssh_public_key} onAdd={() => setPage({ kind: 'add-repo' })} reload={load} />
        ) : page.kind === 'add-repo' ? (
          <AddRepo
            sshKey={status.ssh_public_key}
            onCancel={() => setPage(page.then === 'edit' && repos.length ? { kind: 'edit', plan: null } : page.then ? { kind: 'plans' } : { kind: 'repos' })}
            onDone={() => {
              void load();
              setPage(page.then === 'edit' ? { kind: 'edit', plan: null } : { kind: 'repos' });
            }}
          />
        ) : (
          <Rewind plans={plans} initial={page.planId} />
        )}
      </div>
    </div>
  );
}

function NoEngine({ status, installing, onInstall }: { status: BackupStatus; installing: boolean; onInstall: () => void }) {
  return (
    <div className="bk-hero">
      <span className="bk-hero-icon">
        <Icon name="save" size={34} />
      </span>
      <h2>Set up backups</h2>
      <p>
        NoCapOS backs up with <b>restic</b>: encrypted, only changes are copied, and you can bring back any file from any day. It
        isn't installed on this server yet.
      </p>
      {status.can_install ? (
        <button type="button" disabled={installing} onClick={onInstall}>
          {installing ? <span className="spinner sm" /> : <Icon name="download" size={16} />} {installing ? 'Installing…' : 'Install restic'}
        </button>
      ) : (
        <>
          <p className="muted small">Install it on the server, then come back:</p>
          <code className="bk-cmd">sudo apt install restic</code>
          <p className="muted small">(Fedora: sudo dnf install restic · Arch: sudo pacman -S restic)</p>
        </>
      )}
      {status.error && <p className="muted small">{status.error}</p>}
    </div>
  );
}

// ---------------- backup plans ----------------

function sourceLabel(s: BackupSource, roots: FileRoot[]) {
  if (s.special === 'nocapos') return 'NoCapOS settings & accounts';
  const root = roots.find((r) => r.id === s.root)?.name ?? s.root;
  return s.path && s.path !== '/' ? `${root} › ${s.path.replace(/^\//, '').replace(/\//g, ' › ')}` : root;
}

function PlanList({
  plans,
  repos,
  roots,
  onNew,
  onEdit,
  onRewind,
  reload,
}: {
  plans: BackupPlan[];
  repos: BackupRepo[];
  roots: FileRoot[];
  onNew: () => void;
  onEdit: (p: BackupPlan) => void;
  onRewind: (p: BackupPlan) => void;
  reload: () => Promise<void>;
}) {
  if (!plans.length) {
    return (
      <div className="bk-hero">
        <span className="bk-hero-icon">
          <Icon name="save" size={34} />
        </span>
        <h2>Back up your server automatically</h2>
        <p>Choose what to protect, where to keep the copies (a USB disk, another server or the cloud), and how often. Backups are encrypted and only changes are copied.</p>
        <button type="button" onClick={onNew}>
          <Icon name="plus" size={16} /> New Backup
        </button>
      </div>
    );
  }
  return (
    <>
      <header className="bk-head">
        <h2>Backups</h2>
        <button type="button" onClick={onNew}>
          <Icon name="plus" size={16} /> New Backup
        </button>
      </header>
      <div className="bk-plans">
        {plans.map((p) => (
          <PlanCard key={p.id} plan={p} repo={repos.find((r) => r.id === p.repo_id)} roots={roots} onEdit={() => onEdit(p)} onRewind={() => onRewind(p)} reload={reload} />
        ))}
      </div>
    </>
  );
}

function PlanCard({
  plan,
  repo,
  roots,
  onEdit,
  onRewind,
  reload,
}: {
  plan: BackupPlan;
  repo?: BackupRepo;
  roots: FileRoot[];
  onEdit: () => void;
  onRewind: () => void;
  reload: () => Promise<void>;
}) {
  const job = plan.job;
  const running = !!job && !job.done;
  const run = async () => {
    const r = await backupApi.run(plan.id);
    if (!r.ok) toast('error', 'Could not start the backup', r.error);
    void reload();
  };
  const toggle = async (enabled: boolean) => {
    const r = await backupApi.savePlan({ ...plan, enabled });
    if (!r.ok) toast('error', 'Could not change the backup', r.error);
    void reload();
  };
  const remove = async () => {
    const ok = await confirmDialog({
      title: `Delete the backup “${plan.name}”?`,
      message: 'It stops running. Backups already made stay at the destination, and you can still connect to them later with the recovery key.',
      confirmLabel: 'Delete',
      danger: true,
    });
    if (!ok) return;
    const r = await backupApi.deletePlan(plan.id);
    if (!r.ok) toast('error', 'Could not delete the backup', r.error);
    void reload();
  };

  const state = running ? 'running' : plan.last_status === 'failed' ? 'failed' : plan.last_status === 'ok' ? 'ok' : 'new';
  return (
    <article className={`bk-card ${state} ${plan.enabled ? '' : 'paused'}`}>
      <div className="bk-card-top">
        <span className={`bk-dot ${state}`} />
        <div className="bk-card-title">
          <h3>{plan.name}</h3>
          <span className="bk-dest">
            {repo && <Icon name={KIND_ICON[repo.kind]} size={13} />} {repo ? `${repo.name} · ${repo.location}` : 'Destination missing'}
          </span>
        </div>
        <label className="toggle bk-switch" title={plan.enabled ? 'Runs automatically' : 'Paused: runs only when you click Back Up Now'}>
          <input type="checkbox" role="switch" aria-label="Run automatically" checked={plan.enabled} onChange={(e) => void toggle(e.target.checked)} />
        </label>
      </div>
      <p className="bk-what">{plan.sources.map((s) => sourceLabel(s, roots)).join(' · ')}</p>
      <p className="bk-when">
        {scheduleText(plan.schedule)} · {keepText(plan.keep)}
      </p>

      {running ? (
        <div className="bk-progress">
          <div className="bk-bar">
            <span style={{ width: `${Math.max(3, job!.progress.percent)}%` }} />
          </div>
          <span className="bk-progress-text">
            {job!.phase}
            {job!.progress.bytes_total > 0 && ` · ${fmtBytes(job!.progress.bytes_done)} of ${fmtBytes(job!.progress.bytes_total)}`}
          </span>
        </div>
      ) : (
        <p className={`bk-last ${state}`}>
          {state === 'failed' ? (
            <>
              <Icon name="alert" size={14} /> Last backup failed {fmtAgo(plan.last_run)}: {plan.last_error}
            </>
          ) : state === 'ok' ? (
            <>
              <Icon name="check" size={14} /> Backed up {fmtAgo(plan.last_run)}
              {plan.last_bytes > 0 ? ` · ${fmtBytes(plan.last_bytes)} new` : ' · nothing changed'}
            </>
          ) : (
            'Not backed up yet'
          )}
        </p>
      )}
      {!running && (
        <p className="bk-next">
          {!plan.enabled ? 'Paused' : isZero(plan.next_run) ? 'Runs when you click Back Up Now' : `Next backup ${dateTime.format(new Date(plan.next_run))}`}
        </p>
      )}

      <div className="bk-actions">
        {running ? (
          <button type="button" className="ghost" onClick={() => void backupApi.cancel(job!.id).then(() => reload())}>
            <Icon name="stop" size={14} /> Stop
          </button>
        ) : (
          <button type="button" onClick={() => void run()}>
            <Icon name="play" size={14} /> Back Up Now
          </button>
        )}
        <button type="button" className="ghost" disabled={!plan.last_snapshot} onClick={onRewind}>
          <Icon name="rewind" size={14} /> Rewind
        </button>
        <button type="button" className="ghost" onClick={onEdit}>
          <Icon name="pencil" size={14} /> Edit
        </button>
        <button type="button" className="ghost danger" title="Delete" aria-label="Delete backup" onClick={() => void remove()}>
          <Icon name="trash" size={14} />
        </button>
      </div>
    </article>
  );
}

const srcKey = (s: BackupSource) => (s.special ? `special:${s.special}` : `${s.root}:${s.path}`);

function PlanEditor({
  plan,
  repos,
  roots,
  onAddRepo,
  onDone,
}: {
  plan: BackupPlan | null;
  repos: BackupRepo[];
  roots: FileRoot[];
  onAddRepo: () => void;
  onDone: () => void;
}) {
  const [name, setName] = useState(plan?.name ?? 'My Backup');
  const [repoId, setRepoId] = useState(plan?.repo_id ?? repos[repos.length - 1]?.id ?? 0);
  const [sources, setSources] = useState<BackupSource[]>(plan?.sources ?? [{ special: 'nocapos' }]);
  const [schedule, setSchedule] = useState<BackupSchedule>(plan?.schedule ?? { every: 'day', at: '02:00' });
  const [keep, setKeep] = useState<BackupKeep>(plan?.keep ?? RECOMMENDED.day);
  const [custom, setCustom] = useState(false);
  const [folders, setFolders] = useState<Record<string, string[]>>({});
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  // Top-level folders of each location, to pick from.
  useEffect(() => {
    roots.forEach((r) =>
      void fileApi.list(r.id, '/').then((l) => {
        if (!l.ok) return;
        const names = l.data.entries.filter((e) => e.dir && !e.name.startsWith('.')).map((e) => e.name);
        setFolders((f) => ({ ...f, [r.id]: names }));
      }),
    );
  }, [roots]);

  const chosen = useMemo(() => new Set(sources.map(srcKey)), [sources]);
  const toggleSource = (s: BackupSource, on: boolean) =>
    setSources((list) => (on ? [...list.filter((x) => srcKey(x) !== srcKey(s)), s] : list.filter((x) => srcKey(x) !== srcKey(s))));

  const setEvery = (every: BackupSchedule['every']) => {
    setSchedule((s) => ({ every, at: s.at ?? '02:00', weekday: s.weekday ?? 0 }));
    if (!custom) setKeep(RECOMMENDED[every]);
  };

  const save = async () => {
    setSaving(true);
    setError('');
    const r = await backupApi.savePlan({ id: plan?.id, name, repo_id: repoId, sources, schedule, keep, enabled: plan?.enabled ?? true });
    setSaving(false);
    if (!r.ok) return setError(r.error ?? 'Could not save');
    toast('success', plan ? 'Backup saved' : 'Backup created', plan ? undefined : 'Click Back Up Now to make the first one right away.');
    onDone();
  };

  return (
    <div className="bk-editor">
      <header className="bk-head">
        <button type="button" className="ghost" onClick={onDone}>
          <Icon name="chevronLeft" size={16} /> Back
        </button>
        <h2>{plan ? `Edit “${plan.name}”` : 'New Backup'}</h2>
      </header>

      <Section title="Name">
        <Row label="Name">
          <input value={name} maxLength={80} onChange={(e) => setName(e.target.value)} />
        </Row>
      </Section>

      <Section title="What to back up" hint="Pick whole drives or the folders that matter. The Recycle Bin is always left out.">
        <Toggle
          label="NoCapOS settings & accounts"
          hint="Users, settings, AI providers, app passwords and keys"
          checked={chosen.has('special:nocapos')}
          onChange={(on) => toggleSource({ special: 'nocapos' }, on)}
        />
        {roots.map((r) => {
          const whole = chosen.has(`${r.id}:/`);
          return (
            <div key={r.id} className="bk-root">
              <Toggle label={`Everything in ${r.name}`} hint={r.id === 'system' ? 'The whole server disk — usually too much; pick folders below' : undefined} checked={whole} onChange={(on) => toggleSource({ root: r.id, path: '/' }, on)} />
              {!whole && (folders[r.id]?.length ?? 0) > 0 && (
                <div className="bk-folders">
                  {folders[r.id].map((f) => {
                    const s = { root: r.id, path: `/${f}` };
                    return (
                      <label key={f} className={`bk-chip ${chosen.has(srcKey(s)) ? 'on' : ''}`}>
                        <input type="checkbox" checked={chosen.has(srcKey(s))} onChange={(e) => toggleSource(s, e.target.checked)} />
                        <Icon name="folder" size={14} /> {f}
                      </label>
                    );
                  })}
                </div>
              )}
            </div>
          );
        })}
      </Section>

      <Section title="Where to keep the backups">
        <Row label="Destination" hint={repos.find((r) => r.id === repoId)?.location}>
          <div className="bk-inline">
            <select value={repoId} onChange={(e) => setRepoId(Number(e.target.value))}>
              {!repos.length && <option value={0}>No destinations yet</option>}
              {repos.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.name} ({KIND_LABEL[r.kind]})
                </option>
              ))}
            </select>
            <button type="button" className="ghost" onClick={onAddRepo}>
              <Icon name="plus" size={14} /> Add
            </button>
          </div>
        </Row>
      </Section>

      <Section title="When">
        <Row label="Back up">
          <Choice
            value={schedule.every}
            options={[
              { id: 'hour', label: 'Hourly' },
              { id: 'day', label: 'Daily' },
              { id: 'week', label: 'Weekly' },
              { id: 'manual', label: 'Manually' },
            ]}
            onChange={setEvery}
          />
        </Row>
        {(schedule.every === 'day' || schedule.every === 'week') && (
          <Row label="At" hint="Server time. Pick a quiet hour.">
            <div className="bk-inline">
              {schedule.every === 'week' && (
                <select value={schedule.weekday ?? 0} onChange={(e) => setSchedule({ ...schedule, weekday: Number(e.target.value) })}>
                  {WEEKDAYS.map((d, i) => (
                    <option key={d} value={i}>
                      {d}
                    </option>
                  ))}
                </select>
              )}
              <input type="time" value={schedule.at ?? '02:00'} onChange={(e) => setSchedule({ ...schedule, at: e.target.value })} />
            </div>
          </Row>
        )}
      </Section>

      <Section title="How long to keep old versions" hint="Older backups are thinned out automatically, so you keep recent days in detail and months further back.">
        <Toggle label="Recommended" hint={keepText(RECOMMENDED[schedule.every])} checked={!custom} onChange={(on) => {
          setCustom(!on);
          if (on) setKeep(RECOMMENDED[schedule.every]);
        }} />
        {custom && (
          <div className="bk-keep">
            {(['hourly', 'daily', 'weekly', 'monthly', 'yearly'] as const).map((k) => (
              <label key={k}>
                <span>{k[0].toUpperCase() + k.slice(1)}</span>
                <input type="number" min={0} max={1000} value={keep[k] ?? 0} onChange={(e) => setKeep({ ...keep, [k]: Math.max(0, Number(e.target.value) || 0) })} />
              </label>
            ))}
          </div>
        )}
      </Section>

      {error && <p className="error">{error}</p>}
      <div className="bk-footer">
        <button type="button" className="ghost" onClick={onDone}>
          Cancel
        </button>
        <button type="button" disabled={saving || !sources.length || !repoId} onClick={() => void save()}>
          {saving ? 'Saving…' : plan ? 'Save' : 'Create Backup'}
        </button>
      </div>
    </div>
  );
}

// ---------------- destinations ----------------

function SSHKeyBox({ sshKey }: { sshKey?: string }) {
  if (!sshKey) return null;
  return (
    <div className="bk-sshkey">
      <p className="small">
        On the other server, add this line to <code>~/.ssh/authorized_keys</code> of the user NoCapOS signs in as:
      </p>
      <div className="bk-keyline">
        <code>{sshKey}</code>
        <button type="button" className="ghost" onClick={() => void navigator.clipboard?.writeText(sshKey).then(() => toast('success', 'Key copied'))}>
          <Icon name="copy" size={14} /> Copy
        </button>
      </div>
    </div>
  );
}

function RepoList({ repos, plans, sshKey, onAdd, reload }: { repos: BackupRepo[]; plans: BackupPlan[]; sshKey?: string; onAdd: () => void; reload: () => Promise<void> }) {
  const [shown, setShown] = useState<Record<number, string>>({});
  const reveal = async (r: BackupRepo) => {
    const ok = await confirmDialog({
      title: `Show the recovery key for “${r.name}”?`,
      message: 'Anyone with this key and access to the destination can read the backups. Keep it somewhere safe, like a password manager.',
      confirmLabel: 'Show Key',
    });
    if (!ok) return;
    const k = await backupApi.recoveryKey(r.id);
    if (!k.ok) return toast('error', 'Could not show the key', k.error);
    setShown((s) => ({ ...s, [r.id]: k.data.recovery_key }));
  };
  const remove = async (r: BackupRepo) => {
    const ok = await confirmDialog({
      title: `Remove “${r.name}”?`,
      message: 'NoCapOS stops using it. The backups stay there; reconnect later with the recovery key.',
      confirmLabel: 'Remove',
      danger: true,
    });
    if (!ok) return;
    const d = await backupApi.deleteRepo(r.id);
    if (!d.ok) return toast('error', 'Could not remove it', d.error);
    void reload();
  };
  return (
    <>
      <header className="bk-head">
        <h2>Destinations</h2>
        <button type="button" onClick={onAdd}>
          <Icon name="plus" size={16} /> Add Destination
        </button>
      </header>
      {!repos.length && <p className="muted">No destinations yet. Add a USB disk, another server or cloud storage to back up to.</p>}
      <div className="bk-repos">
        {repos.map((r) => (
          <article key={r.id} className="bk-repo">
            <span className="bk-repo-icon">
              <Icon name={KIND_ICON[r.kind]} size={20} />
            </span>
            <div className="bk-repo-main">
              <h3>{r.name}</h3>
              <span className="muted small">
                {KIND_LABEL[r.kind]} · {r.location}
              </span>
              <span className="muted small">{plans.filter((p) => p.repo_id === r.id).map((p) => p.name).join(', ') || 'Not used by any backup'}</span>
              {shown[r.id] && (
                <div className="bk-keyline">
                  <code className="bk-recovery">{shown[r.id]}</code>
                  <button type="button" className="ghost" onClick={() => void navigator.clipboard?.writeText(shown[r.id]).then(() => toast('success', 'Recovery key copied'))}>
                    <Icon name="copy" size={14} /> Copy
                  </button>
                </div>
              )}
            </div>
            <div className="bk-repo-actions">
              <button type="button" className="ghost" onClick={() => void reveal(r)}>
                <Icon name="key" size={14} /> Recovery Key
              </button>
              <button type="button" className="ghost danger" aria-label="Remove destination" title="Remove" onClick={() => void remove(r)}>
                <Icon name="trash" size={14} />
              </button>
            </div>
          </article>
        ))}
      </div>
      <Section title="This server's backup key" hint="Used to sign in to other servers you back up to over SFTP.">
        <div className="bk-pad">
          <SSHKeyBox sshKey={sshKey} />
        </div>
      </Section>
    </>
  );
}

function AddRepo({ sshKey, onCancel, onDone }: { sshKey?: string; onCancel: () => void; onDone: () => void }) {
  const [kind, setKind] = useState<RepoKind>('local');
  const [name, setName] = useState('USB Disk');
  const [cfg, setCfg] = useState<NewRepo['config']>({ path: '/mnt/usb/nocapos-backups', port: 22, user: 'root' });
  const [secret, setSecret] = useState('');
  const [existing, setExisting] = useState(false);
  const [key, setKey] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [created, setCreated] = useState<string | null>(null);
  const set = (k: keyof NewRepo['config'], v: string | number) => setCfg((c) => ({ ...c, [k]: v }));

  const pickKind = (k: RepoKind) => {
    setKind(k);
    setName(k === 'local' ? 'USB Disk' : k === 'sftp' ? 'Backup Server' : 'Cloud Backup');
    setCfg(k === 'local' ? { path: '/mnt/usb/nocapos-backups' } : k === 'sftp' ? { host: '', port: 22, user: 'root', path: '/srv/backups/nocapos' } : { endpoint: 'https://', bucket: '', prefix: 'nocapos', key_id: '' });
  };

  const submit = async () => {
    setBusy(true);
    setError('');
    const r = await backupApi.addRepo({ name, kind, config: cfg, secret: secret || undefined, recovery_key: existing ? key : undefined });
    setBusy(false);
    if (!r.ok) return setError(r.error ?? 'Could not add the destination');
    if (existing) {
      toast('success', 'Connected', `${name} is ready.`);
      return onDone();
    }
    setCreated(r.data.recovery_key);
  };

  if (created) {
    return (
      <div className="bk-hero bk-saved">
        <span className="bk-hero-icon key">
          <Icon name="key" size={32} />
        </span>
        <h2>Save your recovery key</h2>
        <p>
          Your backups at <b>{name}</b> are encrypted with this key. If this server is ever lost, you need it to get your files back.
          Store it in a password manager or print it.
        </p>
        <code className="bk-recovery big">{created}</code>
        <div className="bk-inline">
          <button type="button" className="ghost" onClick={() => void navigator.clipboard?.writeText(created).then(() => toast('success', 'Recovery key copied'))}>
            <Icon name="copy" size={14} /> Copy
          </button>
          <button type="button" onClick={onDone}>
            I've Saved It
          </button>
        </div>
        <p className="muted small">You can see it again later under Destinations.</p>
      </div>
    );
  }

  return (
    <div className="bk-editor">
      <header className="bk-head">
        <button type="button" className="ghost" onClick={onCancel}>
          <Icon name="chevronLeft" size={16} /> Back
        </button>
        <h2>Add Destination</h2>
      </header>
      <div className="bk-kinds">
        {(['local', 'sftp', 's3'] as RepoKind[]).map((k) => (
          <button key={k} type="button" className={`bk-kind ${kind === k ? 'on' : ''}`} onClick={() => pickKind(k)}>
            <Icon name={KIND_ICON[k]} size={22} />
            <b>{KIND_LABEL[k]}</b>
            <span>{k === 'local' ? 'USB disk, second drive or a mounted NAS share' : k === 'sftp' ? 'Another NoCapOS or any Linux server, over SSH' : 'Backblaze B2, Wasabi, AWS S3 or any S3-compatible storage'}</span>
          </button>
        ))}
      </div>

      <Section title="Details">
        <Row label="Name">
          <input value={name} maxLength={80} onChange={(e) => setName(e.target.value)} />
        </Row>
        {kind === 'local' && (
          <Row label="Folder" hint="Full path on this server. Plug in and mount the disk first.">
            <input value={cfg.path ?? ''} spellCheck={false} onChange={(e) => set('path', e.target.value)} />
          </Row>
        )}
        {kind === 'sftp' && (
          <>
            <Row label="Server">
              <div className="bk-inline">
                <input placeholder="192.168.1.20 or backup.example.com" value={cfg.host ?? ''} spellCheck={false} onChange={(e) => set('host', e.target.value)} />
                <input className="bk-port" type="number" min={1} max={65535} value={cfg.port ?? 22} onChange={(e) => set('port', Number(e.target.value))} aria-label="Port" />
              </div>
            </Row>
            <Row label="User">
              <input value={cfg.user ?? ''} spellCheck={false} onChange={(e) => set('user', e.target.value)} />
            </Row>
            <Row label="Folder" hint="On the other server">
              <input value={cfg.path ?? ''} spellCheck={false} onChange={(e) => set('path', e.target.value)} />
            </Row>
            <div className="bk-pad">
              <SSHKeyBox sshKey={sshKey} />
            </div>
          </>
        )}
        {kind === 's3' && (
          <>
            <Row label="Endpoint" hint="e.g. https://s3.eu-central-003.backblazeb2.com">
              <input value={cfg.endpoint ?? ''} spellCheck={false} onChange={(e) => set('endpoint', e.target.value)} />
            </Row>
            <Row label="Bucket">
              <input value={cfg.bucket ?? ''} spellCheck={false} onChange={(e) => set('bucket', e.target.value)} />
            </Row>
            <Row label="Folder in bucket">
              <input value={cfg.prefix ?? ''} spellCheck={false} onChange={(e) => set('prefix', e.target.value)} />
            </Row>
            <Row label="Access key ID">
              <input value={cfg.key_id ?? ''} spellCheck={false} autoComplete="off" onChange={(e) => set('key_id', e.target.value)} />
            </Row>
            <Row label="Secret key">
              <input type="password" value={secret} autoComplete="new-password" onChange={(e) => setSecret(e.target.value)} />
            </Row>
          </>
        )}
      </Section>

      <Section title="Existing backups">
        <Toggle label="There are already NoCapOS backups here" hint="After reinstalling, connect to your old backups with their recovery key" checked={existing} onChange={setExisting} />
        {existing && (
          <Row label="Recovery key">
            <input value={key} spellCheck={false} autoComplete="off" onChange={(e) => setKey(e.target.value)} />
          </Row>
        )}
      </Section>

      {error && <p className="error">{error}</p>}
      <div className="bk-footer">
        <button type="button" className="ghost" onClick={onCancel}>
          Cancel
        </button>
        <button type="button" disabled={busy || (existing && !key.trim())} onClick={() => void submit()}>
          {busy ? <span className="spinner sm" /> : null} {busy ? 'Checking…' : existing ? 'Connect' : 'Add Destination'}
        </button>
      </div>
    </div>
  );
}

// ---------------- rewind ----------------

function Rewind({ plans, initial }: { plans: BackupPlan[]; initial?: number }) {
  const usable = plans.filter((p) => p.last_snapshot);
  const [planId, setPlanId] = useState<number | undefined>(initial ?? usable[0]?.id);
  const [snaps, setSnaps] = useState<Snapshot[] | null>(null);
  const [snap, setSnap] = useState<Snapshot | null>(null);
  const [path, setPath] = useState('');
  const [entries, setEntries] = useState<SnapNode[] | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [error, setError] = useState('');
  const [job, setJob] = useState<BackupJob | null>(null);
  const [labels, setLabels] = useState<Record<string, string>>({});

  useEffect(() => {
    if (!planId) return;
    setSnaps(null);
    setSnap(null);
    setError('');
    void backupApi.snapshots(planId).then((r) => {
      if (!r.ok) {
        setSnaps([]);
        return setError(r.error ?? 'Could not read the backups');
      }
      setSnaps(r.data.snapshots);
      setSnap(r.data.snapshots[0] ?? null);
    });
  }, [planId]);

  useEffect(() => {
    setPath('');
  }, [snap]);

  useEffect(() => {
    if (!planId || !snap) return;
    setEntries(null);
    setSelected(new Set());
    void backupApi.browse(planId, snap.id, path).then((r) => {
      if (!r.ok) {
        setEntries([]);
        return setError(r.error ?? 'Could not open the backup');
      }
      setError('');
      if (!path) setLabels(Object.fromEntries(r.data.entries.map((n) => [n.path, n.name])));
      setEntries(
        [...r.data.entries].sort((a, b) => (a.type === 'dir') !== (b.type === 'dir') ? (a.type === 'dir' ? -1 : 1) : a.name.localeCompare(b.name)),
      );
    });
  }, [planId, snap, path]);

  // Follow a running restore.
  useEffect(() => {
    if (!job || job.done) return;
    const t = window.setTimeout(async () => {
      const r = await backupApi.job(job.id);
      if (!r.ok) return;
      setJob(r.data);
      if (r.data.done) {
        if (r.data.error) toast('error', 'Restore failed', r.data.error);
        else toast('success', 'Restored', r.data.result?.split('\n').map((l) => l.replace(/^[^:]+:/, '')).join(', '));
      }
    }, 800);
    return () => window.clearTimeout(t);
  }, [job]);

  const restore = async (mode: 'beside' | 'replace') => {
    if (!planId || !snap) return;
    const paths = [...selected];
    const ok = await confirmDialog({
      title: mode === 'beside' ? 'Restore next to the current files?' : 'Replace the current files?',
      message:
        mode === 'beside'
          ? `${paths.length} item${paths.length === 1 ? '' : 's'} from ${dateTime.format(new Date(snap.time))} come back with “(restored …)” in the name. Nothing is overwritten.`
          : `${paths.length} item${paths.length === 1 ? '' : 's'} go back to how they were on ${dateTime.format(new Date(snap.time))}. The current versions move to the Recycle Bin.`,
      confirmLabel: mode === 'beside' ? 'Restore' : 'Replace',
      danger: mode === 'replace',
    });
    if (!ok) return;
    const r = await backupApi.restore(planId, snap.id, paths, mode);
    if (!r.ok) return toast('error', 'Could not restore', r.error);
    setJob(r.data);
    setSelected(new Set());
  };

  const download = async (n: SnapNode) => {
    if (!planId || !snap) return;
    toast('info', 'Preparing download', n.name);
    const err = await downloadFromBackup(planId, snap.id, n);
    if (err) toast('error', 'Download failed', err);
  };

  if (!usable.length) {
    return (
      <div className="bk-hero">
        <span className="bk-hero-icon">
          <Icon name="rewind" size={34} />
        </span>
        <h2>Rewind</h2>
        <p>Once a backup has run, you can go back to any point in time here, look around, and bring back files or whole folders.</p>
      </div>
    );
  }

  // Group backups by day for the timeline.
  const days: { day: string; items: Snapshot[] }[] = [];
  (snaps ?? []).forEach((s) => {
    const d = dayFmt.format(new Date(s.time));
    const last = days[days.length - 1];
    if (last?.day === d) last.items.push(s);
    else days.push({ day: d, items: [s] });
  });

  const crumbs = path ? path.split('/').filter(Boolean) : [];
  const top = snap?.paths.find((p) => path === p || path.startsWith(p + '/'));
  const restoring = job && !job.done;

  return (
    <div className="bk-rewind">
      <aside className="bk-timeline">
        {usable.length > 1 && (
          <select value={planId} onChange={(e) => setPlanId(Number(e.target.value))}>
            {usable.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        )}
        {snaps === null ? (
          <span className="spinner" />
        ) : (
          days.map((d) => (
            <div key={d.day} className="bk-day">
              <h4>{d.day}</h4>
              {d.items.map((s) => (
                <button key={s.id} type="button" className={snap?.id === s.id ? 'on' : ''} onClick={() => setSnap(s)}>
                  <Icon name="clock" size={13} /> {timeFmt.format(new Date(s.time))}
                </button>
              ))}
            </div>
          ))
        )}
      </aside>

      <section className="bk-browser">
        <header className="bk-browser-head">
          <div className="bk-crumbs">
            <button type="button" className="ghost" onClick={() => setPath('')}>
              <Icon name="rewind" size={14} /> {snap ? dateTime.format(new Date(snap.time)) : 'Backup'}
            </button>
            {top &&
              crumbs.map((c, i) => {
                const p = '/' + crumbs.slice(0, i + 1).join('/');
                if (p.length < top.length && top.startsWith(p)) return null; // skip the parents above the backed-up folder
                return (
                  <span key={p} className="bk-crumb">
                    <Icon name="chevronRight" size={12} />
                    <button type="button" className="ghost" onClick={() => setPath(p)}>
                      {p === top ? (labels[top] ?? top) : c}
                    </button>
                  </span>
                );
              })}
          </div>
          <div className="bk-inline">
            {selected.size > 0 && <span className="small muted">{selected.size} selected</span>}
            <button type="button" className="ghost" disabled={!selected.size || !!restoring || !path} onClick={() => void restore('beside')}>
              <Icon name="restart" size={14} /> Restore
            </button>
            <button type="button" className="ghost danger" disabled={!selected.size || !!restoring || !path} onClick={() => void restore('replace')}>
              Replace Current
            </button>
          </div>
        </header>
        {restoring && (
          <div className="bk-progress">
            <div className="bk-bar">
              <span style={{ width: `${Math.max(4, job!.progress.percent)}%` }} />
            </div>
            <span className="bk-progress-text">{job!.phase}</span>
          </div>
        )}
        {error && <p className="error">{error}</p>}
        <div className="bk-entries">
          {entries === null ? (
            <span className="spinner" />
          ) : !entries.length ? (
            <p className="muted">This folder was empty.</p>
          ) : (
            entries.map((n) => {
              const sel = selected.has(n.path);
              const atTop = !path;
              return (
                <div key={n.path} className={`bk-entry ${sel ? 'sel' : ''}`}>
                  {!atTop && (
                    <input
                      type="checkbox"
                      aria-label={`Select ${n.name}`}
                      checked={sel}
                      onChange={(e) => {
                        const next = new Set(selected);
                        if (e.target.checked) next.add(n.path);
                        else next.delete(n.path);
                        setSelected(next);
                      }}
                    />
                  )}
                  <button type="button" className="bk-entry-name" onClick={() => (n.type === 'dir' ? setPath(n.path) : void download(n))} title={n.type === 'dir' ? 'Open' : 'Download'}>
                    <Icon name={n.type === 'dir' ? 'folder' : 'file'} size={16} />
                    <span>{n.name}</span>
                  </button>
                  <span className="bk-entry-size">{n.type === 'dir' ? '' : fmtBytes(n.size)}</span>
                  <span className="bk-entry-date">{n.mtime && !isZero(n.mtime) ? dateTime.format(new Date(n.mtime)) : ''}</span>
                  {!atTop && (
                    <button type="button" className="ghost icon-btn neutral" title={n.type === 'dir' ? 'Download as .tar' : 'Download'} aria-label="Download" onClick={() => void download(n)}>
                      <Icon name="download" size={15} />
                    </button>
                  )}
                </div>
              );
            })
          )}
        </div>
      </section>
    </div>
  );
}
