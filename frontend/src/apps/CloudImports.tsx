import { useCallback, useEffect, useState } from 'react';
import { cloudApi, type CloudAccount, type CloudImport, type CloudKind, type CloudSchedule, type CloudStatus, type NewCloudAccount } from '../api/cloud';
import type { FileRoot } from '../api/files';
import { Icon, type IconName } from '../components/Icon';
import { fmtAgo, fmtBytes } from '../lib/format';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import { Choice, Row, Section } from './Personalize';

// Cloud Imports, inside Files: copy folders from Google Drive, Dropbox,
// OneDrive, S3/B2, Nextcloud/WebDAV or SFTP into your drives, once or on a
// schedule. Copies only — nothing is deleted on either side.

const KINDS: { kind: CloudKind; label: string; icon: IconName; hint: string }[] = [
  { kind: 'drive', label: 'Google Drive', icon: 'globe', hint: 'Sign in with Google' },
  { kind: 'dropbox', label: 'Dropbox', icon: 'globe', hint: 'Sign in with Dropbox' },
  { kind: 'onedrive', label: 'OneDrive', icon: 'globe', hint: 'Sign in with Microsoft' },
  { kind: 's3', label: 'S3 / B2 / Wasabi', icon: 'drive', hint: 'Access key and secret' },
  { kind: 'webdav', label: 'Nextcloud / WebDAV', icon: 'network', hint: 'Address, user and password' },
  { kind: 'sftp', label: 'Another server (SFTP)', icon: 'terminal', hint: 'Server, user and password' },
];
const label = (k: CloudKind) => KINDS.find((x) => x.kind === k)?.label ?? k;
const isZero = (t?: string) => !t || t.startsWith('0001-');
const WEEKDAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
const when = (s: CloudSchedule) => (s.every === 'day' ? `Every day at ${s.at}` : s.every === 'week' ? `Every ${WEEKDAYS[s.weekday ?? 0]} at ${s.at}` : 'When you click Import');

type View = { kind: 'list' } | { kind: 'add-account' } | { kind: 'edit'; imp: CloudImport | null };

export function CloudImportsPanel({ roots, onOpen }: { roots: FileRoot[]; onOpen: (root: string, path: string) => void }) {
  const [status, setStatus] = useState<CloudStatus | null>(null);
  const [accounts, setAccounts] = useState<CloudAccount[]>([]);
  const [imports, setImports] = useState<CloudImport[]>([]);
  const [view, setView] = useState<View>({ kind: 'list' });
  const [installing, setInstalling] = useState(false);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    const r = await cloudApi.overview();
    if (!r.ok) return setError(r.error ?? 'Could not load');
    setError('');
    setStatus(r.data.status);
    setAccounts(r.data.accounts);
    setImports(r.data.imports);
  }, []);
  useEffect(() => {
    void load();
  }, [load]);
  const busy = imports.some((i) => i.job && !i.job.done);
  useEffect(() => {
    const t = window.setTimeout(() => void load(), busy ? 1500 : 15000);
    return () => window.clearTimeout(t);
  }, [busy, imports, load]);

  if (!status) return error ? <p className="error">{error}</p> : <span className="spinner" />;
  if (!status.installed) {
    return (
      <div className="ci-empty">
        <p>Cloud imports use <b>rclone</b>, which isn't installed on this server yet.</p>
        {status.can_install ? (
          <button
            type="button"
            disabled={installing}
            onClick={async () => {
              setInstalling(true);
              const r = await cloudApi.install();
              setInstalling(false);
              if (!r.ok) return toast('error', 'Could not install rclone', r.error);
              void load();
            }}
          >
            {installing ? <span className="spinner sm" /> : <Icon name="download" size={14} />} {installing ? 'Installing…' : 'Install rclone'}
          </button>
        ) : (
          <code className="nd-cmd">sudo apt install rclone</code>
        )}
      </div>
    );
  }
  if (view.kind === 'add-account') {
    return (
      <AddAccount
        onCancel={() => setView({ kind: 'list' })}
        onAdded={async () => {
          await load();
          setView({ kind: 'edit', imp: null });
        }}
      />
    );
  }
  if (view.kind === 'edit') {
    return (
      <ImportEditor
        imp={view.imp}
        accounts={accounts}
        roots={roots}
        onCancel={() => setView({ kind: 'list' })}
        onSaved={async () => {
          await load();
          setView({ kind: 'list' });
        }}
      />
    );
  }

  const removeAccount = async (a: CloudAccount) => {
    const ok = await confirmDialog({ title: `Remove “${a.name}”?`, message: 'NoCapOS forgets this account. Files already imported stay.', confirmLabel: 'Remove', danger: true });
    if (!ok) return;
    const r = await cloudApi.removeAccount(a.id);
    if (!r.ok) toast('error', 'Could not remove it', r.error);
    void load();
  };

  return (
    <div className="ci">
      <p className="fn-intro">Copy folders from your cloud accounts into your drives, once or on a schedule. Only copies: nothing is ever deleted in the cloud or here.</p>
      <div className="ci-accounts">
        {accounts.map((a) => (
          <span key={a.id} className="ci-account" title={a.detail || label(a.kind)}>
            <Icon name={KINDS.find((k) => k.kind === a.kind)?.icon ?? 'globe'} size={14} /> {a.name}
            <button type="button" className="ci-x" aria-label={`Remove ${a.name}`} onClick={() => void removeAccount(a)}>
              <Icon name="close" size={12} />
            </button>
          </span>
        ))}
        <button type="button" className="ghost" onClick={() => setView({ kind: 'add-account' })}>
          <Icon name="plus" size={14} /> Add Account
        </button>
      </div>

      {imports.map((i) => (
        <ImportCard key={i.id} imp={i} account={accounts.find((a) => a.id === i.account_id)} roots={roots} onEdit={() => setView({ kind: 'edit', imp: i })} onOpen={onOpen} reload={load} />
      ))}
      {!imports.length && accounts.length > 0 && <p className="muted small">No imports yet.</p>}
      <div className="ci-foot">
        <button type="button" onClick={() => setView(accounts.length ? { kind: 'edit', imp: null } : { kind: 'add-account' })}>
          <Icon name="download" size={14} /> New Import
        </button>
      </div>
    </div>
  );
}

function ImportCard({
  imp: i,
  account,
  roots,
  onEdit,
  onOpen,
  reload,
}: {
  imp: CloudImport;
  account?: CloudAccount;
  roots: FileRoot[];
  onEdit: () => void;
  onOpen: (root: string, path: string) => void;
  reload: () => Promise<void>;
}) {
  const running = !!i.job && !i.job.done;
  const p = i.job?.progress;
  const rootName = roots.find((r) => r.id === i.dest_root)?.name ?? i.dest_root;
  const remove = async () => {
    const ok = await confirmDialog({ title: `Delete the import “${i.name}”?`, message: 'Files it already copied stay where they are.', confirmLabel: 'Delete', danger: true });
    if (!ok) return;
    const r = await cloudApi.removeImport(i.id);
    if (!r.ok) toast('error', 'Could not delete it', r.error);
    void reload();
  };
  return (
    <article className="ci-card">
      <div className="ci-card-top">
        <b>{i.name}</b>
        <span className="muted small">{when(i.schedule)}</span>
      </div>
      <p className="ci-route">
        <span>{account ? `${account.name}${i.source ? ` › ${i.source.replace(/\//g, ' › ')}` : ''}` : 'Account missing'}</span>
        <Icon name="chevronRight" size={13} />
        <span>
          {rootName} › {i.dest_path.replace(/^\//, '').replace(/\//g, ' › ')}
        </span>
      </p>
      {running && p ? (
        <div className="bk-progress">
          <div className="bk-bar">
            <span style={{ width: `${p.total_bytes ? Math.max(3, (p.bytes / p.total_bytes) * 100) : 3}%` }} />
          </div>
          <span className="bk-progress-text">
            {p.files} of {p.total_files || '…'} files · {fmtBytes(p.bytes)}
            {p.total_bytes ? ` of ${fmtBytes(p.total_bytes)}` : ''}
          </span>
        </div>
      ) : (
        <p className={`ci-last ${i.last_status}`}>
          {i.last_status === 'ok'
            ? `Imported ${fmtAgo(i.last_run)} · ${i.last_files ? `${i.last_files} new files, ${fmtBytes(i.last_bytes)}` : 'nothing new'}`
            : i.last_status === 'failed'
              ? `Failed ${fmtAgo(i.last_run)}: ${i.last_error}`
              : i.last_status === 'canceled'
                ? `Stopped ${fmtAgo(i.last_run)}`
                : 'Not imported yet'}
          {!isZero(i.next_run) && ` · next ${new Date(i.next_run).toLocaleString()}`}
        </p>
      )}
      <div className="ci-actions">
        {running ? (
          <button type="button" className="ghost" onClick={() => void cloudApi.cancel(i.job!.id).then(() => reload())}>
            <Icon name="stop" size={13} /> Stop
          </button>
        ) : (
          <button type="button" onClick={() => void cloudApi.run(i.id).then((r) => (r.ok ? reload() : toast('error', 'Could not start', r.error)))}>
            <Icon name="download" size={13} /> Import Now
          </button>
        )}
        <button type="button" className="ghost" onClick={() => onOpen(i.dest_root, i.dest_path)}>
          <Icon name="folder" size={13} /> Open Folder
        </button>
        <button type="button" className="ghost" onClick={onEdit}>
          <Icon name="pencil" size={13} /> Edit
        </button>
        <button type="button" className="ghost danger" aria-label="Delete import" onClick={() => void remove()}>
          <Icon name="trash" size={13} />
        </button>
      </div>
    </article>
  );
}

function ImportEditor({ imp, accounts, roots, onCancel, onSaved }: { imp: CloudImport | null; accounts: CloudAccount[]; roots: FileRoot[]; onCancel: () => void; onSaved: () => void }) {
  const drives = roots.filter((r) => r.id !== 'system');
  const [accountId, setAccountId] = useState(imp?.account_id ?? accounts[accounts.length - 1]?.id ?? 0);
  const [source, setSource] = useState(imp?.source ?? '');
  const [browse, setBrowse] = useState(imp?.source ?? '');
  const [folders, setFolders] = useState<{ name: string; path: string }[] | null>(null);
  const [folderErr, setFolderErr] = useState('');
  const account = accounts.find((a) => a.id === accountId);
  const [destRoot, setDestRoot] = useState(imp?.dest_root ?? drives[0]?.id ?? '');
  const [destPath, setDestPath] = useState(imp?.dest_path ?? `/Imports/${account?.name ?? 'Cloud'}`);
  const [schedule, setSchedule] = useState<CloudSchedule>(imp?.schedule ?? { every: 'manual', at: '03:00' });
  const [name, setName] = useState(imp?.name ?? '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!accountId) return;
    setFolders(null);
    setFolderErr('');
    void cloudApi.folders(accountId, browse).then((r) => {
      if (!r.ok) {
        setFolders([]);
        return setFolderErr(r.error ?? 'Could not list folders');
      }
      setFolders(r.data.folders);
    });
  }, [accountId, browse]);

  const crumbs = browse ? browse.split('/') : [];
  const save = async () => {
    setBusy(true);
    setError('');
    const r = await cloudApi.saveImport({
      id: imp?.id,
      account_id: accountId,
      name: name.trim() || (source ? source.split('/').pop() : account?.name) || 'Import',
      source,
      dest_root: destRoot,
      dest_path: destPath,
      schedule: schedule.every === 'manual' ? { every: 'manual' } : { every: schedule.every, at: schedule.at ?? '03:00', weekday: schedule.weekday ?? 0 },
    });
    setBusy(false);
    if (!r.ok) return setError(r.error ?? 'Could not save');
    if (!imp) {
      const run = await cloudApi.run(r.data.id);
      toast(run.ok ? 'success' : 'error', run.ok ? 'Import started' : 'Saved, but could not start', run.ok ? 'Progress shows in Cloud Imports.' : run.error);
    }
    onSaved();
  };

  return (
    <div className="ci-editor">
      <Section title="From">
        <Row label="Account">
          <select
            value={accountId}
            onChange={(e) => {
              const id = Number(e.target.value);
              setAccountId(id);
              setBrowse('');
              setSource('');
              if (!imp) setDestPath(`/Imports/${accounts.find((a) => a.id === id)?.name ?? 'Cloud'}`);
            }}
          >
            {accounts.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name} ({label(a.kind)})
              </option>
            ))}
          </select>
        </Row>
        <div className="ci-browser">
          <div className="ci-crumbs">
            <button type="button" className="ghost" onClick={() => setBrowse('')}>
              {account?.name ?? 'Account'}
            </button>
            {crumbs.map((c, n) => (
              <span key={n}>
                <Icon name="chevronRight" size={12} />
                <button type="button" className="ghost" onClick={() => setBrowse(crumbs.slice(0, n + 1).join('/'))}>
                  {c}
                </button>
              </span>
            ))}
          </div>
          <div className="ci-folders">
            {folders === null ? (
              <span className="spinner sm" />
            ) : folderErr ? (
              <p className="error">{folderErr}</p>
            ) : !folders.length ? (
              <p className="muted small">No folders here.</p>
            ) : (
              folders.map((f) => (
                <button key={f.path} type="button" className="ci-folder" onClick={() => setBrowse(f.path)}>
                  <Icon name="folder" size={14} /> {f.name}
                </button>
              ))
            )}
          </div>
          <div className="ci-pick">
            <span className="small">
              Importing: <b>{source ? source.replace(/\//g, ' › ') : 'everything in the account'}</b>
            </span>
            {browse !== source && (
              <button type="button" className="ghost" onClick={() => setSource(browse)}>
                <Icon name="check" size={13} /> Import “{browse ? browse.split('/').pop() : 'everything'}”
              </button>
            )}
          </div>
        </div>
      </Section>

      <Section title="To">
        <Row label="Drive">
          <select value={destRoot} onChange={(e) => setDestRoot(e.target.value)}>
            {drives.map((r) => (
              <option key={r.id} value={r.id}>
                {r.name}
              </option>
            ))}
          </select>
        </Row>
        <Row label="Folder" hint="Created if it doesn't exist">
          <input value={destPath} spellCheck={false} onChange={(e) => setDestPath(e.target.value)} />
        </Row>
      </Section>

      <Section title="When">
        <Row label="Import">
          <Choice
            value={schedule.every}
            options={[
              { id: 'manual', label: 'Once' },
              { id: 'day', label: 'Daily' },
              { id: 'week', label: 'Weekly' },
            ]}
            onChange={(every) => setSchedule({ ...schedule, every })}
          />
        </Row>
        {schedule.every !== 'manual' && (
          <Row label="At" hint="Picks up new and changed files each time">
            <div className="nd-inline">
              {schedule.every === 'week' && (
                <select value={schedule.weekday ?? 0} onChange={(e) => setSchedule({ ...schedule, weekday: Number(e.target.value) })}>
                  {WEEKDAYS.map((d, n) => (
                    <option key={d} value={n}>
                      {d}
                    </option>
                  ))}
                </select>
              )}
              <input type="time" value={schedule.at ?? '03:00'} onChange={(e) => setSchedule({ ...schedule, at: e.target.value })} />
            </div>
          </Row>
        )}
        <Row label="Name" hint="Optional">
          <input value={name} placeholder={source ? source.split('/').pop() : account?.name} onChange={(e) => setName(e.target.value)} />
        </Row>
      </Section>
      {error && <p className="error">{error}</p>}
      <div className="dialog-actions">
        <button type="button" className="ghost" onClick={onCancel}>
          Cancel
        </button>
        <button type="button" disabled={busy || !accountId || !destRoot} onClick={() => void save()}>
          {busy ? 'Saving…' : imp ? 'Save' : 'Start Import'}
        </button>
      </div>
    </div>
  );
}

function AddAccount({ onCancel, onAdded }: { onCancel: () => void; onAdded: () => void }) {
  const [kind, setKind] = useState<CloudKind | null>(null);
  const [name, setName] = useState('');
  const [form, setForm] = useState<NewCloudAccount>({ name: '', kind: 's3' });
  const [session, setSession] = useState<{ id: string; url: string } | null>(null);
  const [address, setAddress] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const set = (k: keyof NewCloudAccount, v: string | number) => setForm((f) => ({ ...f, [k]: v }));
  const oauth = kind === 'drive' || kind === 'dropbox' || kind === 'onedrive';

  const pick = (k: CloudKind) => {
    setKind(k);
    setName(label(k));
    setSession(null);
    setError('');
    setForm({ name: '', kind: k === 's3' || k === 'webdav' || k === 'sftp' ? k : 's3', vendor: 'nextcloud', port: 22 });
  };

  const startSignIn = async () => {
    if (!kind) return;
    setBusy(true);
    setError('');
    const r = await cloudApi.signInStart(kind);
    setBusy(false);
    if (!r.ok) return setError(r.error ?? 'Could not start the sign-in');
    setSession({ id: r.data.session, url: r.data.url });
    window.open(r.data.url, '_blank', 'noopener');
  };
  const finish = async () => {
    if (!session) return;
    setBusy(true);
    setError('');
    const r = await cloudApi.signInFinish(session.id, address, name);
    setBusy(false);
    if (!r.ok) return setError(r.error ?? 'Could not finish signing in');
    toast('success', `${r.data.name} connected`);
    onAdded();
  };
  const addForm = async () => {
    setBusy(true);
    setError('');
    const r = await cloudApi.addAccount({ ...form, name });
    setBusy(false);
    if (!r.ok) return setError(r.error ?? 'Could not add the account');
    toast('success', `${r.data.name} connected`);
    onAdded();
  };

  return (
    <div className="ci-editor">
      <div className="bk-kinds ci-kinds">
        {KINDS.map((k) => (
          <button key={k.kind} type="button" className={`bk-kind ${kind === k.kind ? 'on' : ''}`} onClick={() => pick(k.kind)}>
            <Icon name={k.icon} size={20} />
            <b>{k.label}</b>
            <span>{k.hint}</span>
          </button>
        ))}
      </div>
      {kind && (
        <Section title={label(kind)}>
          <Row label="Name">
            <input value={name} maxLength={60} onChange={(e) => setName(e.target.value)} />
          </Row>
          {oauth && !session && (
            <div className="ci-steps">
              <p>NoCapOS opens the {label(kind)} sign-in in a new tab. Allow access there.</p>
              <button type="button" disabled={busy} onClick={() => void startSignIn()}>
                {busy ? <span className="spinner sm" /> : <Icon name="external" size={14} />} Sign In with {label(kind)}
              </button>
            </div>
          )}
          {oauth && session && (
            <div className="ci-steps">
              <p>
                After you allow access, that tab shows a page that <b>can't be reached</b> at <code>127.0.0.1:53682</code>. That's expected. Copy the whole address from that tab's
                address bar and paste it here.
              </p>
              <input value={address} placeholder="http://127.0.0.1:53682/?state=…&code=…" spellCheck={false} onChange={(e) => setAddress(e.target.value)} />
              <div className="nd-inline">
                <a className="ghost btn-like ci-reopen" href={session.url} target="_blank" rel="noopener noreferrer">
                  Open the sign-in again
                </a>
                <button type="button" disabled={busy || !address.trim()} onClick={() => void finish()}>
                  {busy ? 'Connecting…' : 'Connect'}
                </button>
              </div>
            </div>
          )}
          {kind === 's3' && (
            <>
              <Row label="Endpoint" hint="e.g. https://s3.eu-central-003.backblazeb2.com">
                <input value={form.endpoint ?? ''} spellCheck={false} onChange={(e) => set('endpoint', e.target.value)} />
              </Row>
              <Row label="Access key ID">
                <input value={form.key_id ?? ''} spellCheck={false} autoComplete="off" onChange={(e) => set('key_id', e.target.value)} />
              </Row>
              <Row label="Secret key">
                <input type="password" value={form.secret ?? ''} autoComplete="new-password" onChange={(e) => set('secret', e.target.value)} />
              </Row>
            </>
          )}
          {kind === 'webdav' && (
            <>
              <Row label="Address" hint="Nextcloud: https://cloud.example.com/remote.php/dav/files/USER">
                <input value={form.url ?? ''} spellCheck={false} onChange={(e) => set('url', e.target.value)} />
              </Row>
              <Row label="Type">
                <select value={form.vendor ?? 'nextcloud'} onChange={(e) => set('vendor', e.target.value)}>
                  <option value="nextcloud">Nextcloud</option>
                  <option value="owncloud">ownCloud</option>
                  <option value="other">Other WebDAV</option>
                </select>
              </Row>
            </>
          )}
          {kind === 'sftp' && (
            <Row label="Server">
              <div className="nd-inline">
                <input value={form.host ?? ''} placeholder="192.168.1.20" spellCheck={false} onChange={(e) => set('host', e.target.value)} />
                <input className="bk-port" type="number" value={form.port ?? 22} onChange={(e) => set('port', Number(e.target.value))} aria-label="Port" />
              </div>
            </Row>
          )}
          {(kind === 'webdav' || kind === 'sftp') && (
            <>
              <Row label="User name">
                <input value={form.user ?? ''} spellCheck={false} autoComplete="off" onChange={(e) => set('user', e.target.value)} />
              </Row>
              <Row label="Password" hint={kind === 'webdav' ? 'Nextcloud: create an app password under Security' : undefined}>
                <input type="password" value={form.password ?? ''} autoComplete="new-password" onChange={(e) => set('password', e.target.value)} />
              </Row>
            </>
          )}
        </Section>
      )}
      {error && <p className="error">{error}</p>}
      <div className="dialog-actions">
        <button type="button" className="ghost" onClick={onCancel}>
          Cancel
        </button>
        {kind && !oauth && (
          <button type="button" disabled={busy} onClick={() => void addForm()}>
            {busy ? 'Checking…' : 'Add Account'}
          </button>
        )}
      </div>
    </div>
  );
}
