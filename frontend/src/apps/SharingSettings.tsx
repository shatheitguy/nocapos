import { tr } from '../i18n';
import { useEffect, useState, type FormEvent } from 'react';
import { netApi, type GuestMode, type SMBHealth } from '../api/netdrive';
import { sharesApi, type FolderInput, type Perm, type SharedFolder, type SharesOverview } from '../api/shares';
import { Icon } from '../components/Icon';
import { toast } from '../state/toasts';
import { Choice, Row, Section, Toggle } from './Personalize';
import { HowToConnect, MySmbCard, SmbStatusCard } from './SmbStatus';

// Files → File Sharing: shared folders and who may use them, how they're
// offered on the network (SMB and WebDAV), and people's private homes.

/** Say what went wrong, or that it worked; returns whether it worked. */
async function run(p: Promise<{ ok: boolean; error?: string }>, done?: string): Promise<boolean> {
  const r = await p;
  if (!r.ok) {
    toast('error', tr('files.toast.didntWork'), r.error);
    return false;
  }
  if (done) toast('success', done);
  return true;
}

export function SharingSettings() {
  const [data, setData] = useState<SharesOverview | null>(null);
  const [health, setHealth] = useState<SMBHealth | null>(null);
  const [error, setError] = useState<string | null>(null);
  const loadHealth = async () => {
    const r = await netApi.health();
    if (r.ok) setHealth(r.data);
  };
  const load = async () => {
    const r = await sharesApi.overview();
    if (r.ok) setData(r.data);
    else setError(r.error ?? 'Couldn’t load shared folders. Try again in a sec.');
    void loadHealth();
  };
  useEffect(() => {
    void load();
  }, []);
  if (error) return <p className="error">{error}</p>;
  if (!data) return <p className="muted">Loading shared folders…</p>;

  const example = data.folders.find((f) => f.network && !f.holds_homes)?.name ?? '';
  return (
    <div className="stack settings-page">
      {health && <SmbStatusCard health={health} onChanged={setHealth} />}
      {data.smb && <MySmbCard onReady={() => void loadHealth()} />}
      <NetworkStatus data={data} onChanged={load} />
      {(data.smb || data.webdav) && (
        <Section title="How to connect" hint={example ? `Using “${example}” as the example — swap in any shared folder’s name.` : 'Swap “Folder” for a shared folder’s name.'}>
          <HowToConnect health={health} example={example} webdav={data.webdav} />
        </Section>
      )}
      <Section title="Homes" hint="Everyone gets a private home folder. On this machine’s Linux accounts it is their own Linux home.">
        <Toggle
          label="Administrators can open people’s homes"
          hint="Shows a Homes location in Files for administrators. Turn it off to keep homes private even from them."
          checked={data.admins_see_homes}
          onChange={(on) => void run(sharesApi.setAdminsSeeHomes(on), on ? tr('files.share.adminsSeeHomes') : tr('files.share.homesHidden')).then(load)}
        />
      </Section>
      <Section title="Shared folders" hint="Folders people can open in Files and connect to from their computers and phones. Administrators can always reach every shared folder.">
        {data.folders.length === 0 && <Row><span className="muted">{tr('files.share.empty')}</span></Row>}
        {data.folders.map((f) => (
          <FolderRow key={f.id} folder={f} data={data} onChanged={load} />
        ))}
      </Section>
      <AddFolder data={data} onAdded={load} />
    </div>
  );
}

/** SMB / WebDAV switches, with clear status where Samba can't run. */
function NetworkStatus({ data, onChanged }: { data: SharesOverview; onChanged: () => void }) {
  const [busy, setBusy] = useState(false);
  const sup = data.support;
  const install = async () => {
    setBusy(true);
    await run(netApi.install('samba'), tr('files.share.sambaInstalled'));
    setBusy(false);
    onChanged();
  };
  const smbHint = !sup.native
    ? 'Needs NoCapOS installed on Linux (it isn’t here: Docker, Windows or a development copy). Shared folders still work in Files and over WebDAV.'
    : !sup.samba
      ? 'Samba isn’t installed on this machine yet.'
      : 'For Windows, macOS and Linux file managers. Everyone signs in with their own NoCapOS user name and password.';
  return (
    <Section title="On the network">
      <Toggle
        label="Share over SMB (Windows, Mac, Linux)"
        hint={smbHint}
        checked={data.smb}
        disabled={!sup.native || !sup.samba}
        onChange={(on) => void run(netApi.setProtocols({ smb: on }), on ? tr('files.share.smbOn') : tr('files.share.smbOff')).then(onChanged)}
      />
      {sup.native && !sup.samba && (
        <Row label="Samba" hint={sup.can_install ? 'Installs the samba package with this machine’s package manager.' : 'Install the samba package on this machine, then come back.'}>
          {sup.can_install && (
            <button type="button" className="small" disabled={busy} onClick={() => void install()}>
              {busy ? 'Installing…' : 'Install Samba'}
            </button>
          )}
        </Row>
      )}
      <Toggle
        label="Share over WebDAV (phones, the internet)"
        hint={`At ${window.location.origin}/dav/ — each person sees their home and the folders they may use.`}
        checked={data.webdav}
        onChange={(on) => void run(netApi.setProtocols({ webdav: on }), on ? tr('files.share.webdavOn') : tr('files.share.webdavOff')).then(onChanged)}
      />
      {data.shared_account && (
        <Row label="Shared network account" hint={data.per_user
          ? 'Switched off: people have their own access now, so everyone signs in as themselves.'
          : 'The single “nocapos” account still works until you give the first person access to a shared folder.'}>
          <span className={`badge ${data.per_user ? 'off' : ''}`}>{data.per_user ? 'Off' : 'In use'}</span>
        </Row>
      )}
    </Section>
  );
}

function FolderRow({ folder: f, data, onChanged }: { folder: SharedFolder; data: SharesOverview; onChanged: () => void }) {
  const [open, setOpen] = useState<null | 'edit' | 'delete'>(null);
  const where = `${f.root_name ?? f.root}${f.path === '/' ? '' : f.path}`;
  const toggle = (k: typeof open) => setOpen(open === k ? null : k);
  return (
    <div className="settings-row stacked share-row">
      <div className="share-head">
        <Icon name="network" size={18} />
        <span className="settings-row-text">
          <span className="settings-row-label">{f.name}</span>
          {f.description && <span className="settings-row-hint">{f.description}</span>}
          <span className="user-badges">
            <span className="badge">{where}</span>
            {f.read_only && <span className="badge">Read only for everyone</span>}
            {f.guest && <span className="badge off">{f.guest === 'rw' && !f.read_only ? 'Guests can read & write' : 'Guests can read'}</span>}
            {!f.network && <span className="badge">Hidden on the network</span>}
            {!f.recycle && <span className="badge">No recycle bin</span>}
            {f.missing && <span className="badge off">Folder missing</span>}
            {f.holds_homes && <span className="badge off">Holds people’s homes: not shared</span>}
          </span>
        </span>
        <div className="settings-row-control user-actions">
          <button type="button" className="icon-btn neutral" title="Edit" aria-label={`Edit ${f.name}`} onClick={() => toggle('edit')}>
            <Icon name="edit" size={15} />
          </button>
          <button type="button" className="icon-btn" title="Stop sharing" aria-label={`Stop sharing ${f.name}`} onClick={() => toggle('delete')}>
            <Icon name="trash" size={15} />
          </button>
        </div>
      </div>
      {!f.holds_homes && <WhoCanOpen folder={f} data={data} onChanged={onChanged} />}
      {open === 'edit' && <EditFolder folder={f} onDone={() => { setOpen(null); onChanged(); }} />}
      {open === 'delete' && <DeleteFolder folder={f} onDone={() => { setOpen(null); onChanged(); }} onCancel={() => setOpen(null)} />}
    </div>
  );
}

/** "Who can open it": a chip per person with a Read / Write switch, plus adding someone. */
function WhoCanOpen({ folder: f, data, onChanged }: { folder: SharedFolder; data: SharesOverview; onChanged: () => void }) {
  const [busy, setBusy] = useState(false);
  const set = async (name: string, perm: Perm, done?: string) => {
    setBusy(true);
    const ok = await run(sharesApi.setPerms(f.id, { [name]: perm }), done);
    setBusy(false);
    if (ok) onChanged();
  };
  const people = data.people.filter((p) => !p.admin && !p.disabled);
  const inside = people.filter((p) => f.permissions[p.username]);
  const outside = people.filter((p) => !f.permissions[p.username]);
  return (
    <div className="who-can">
      <span className="who-can-label">Who can open it</span>
      <div className="who-can-chips">
        <span className="chip admin" title="Administrators can always reach every shared folder">
          <Icon name="shield" size={12} /> Admins · always
        </span>
        {inside.map((p) => {
          const perm = f.permissions[p.username];
          const canWrite = perm === 'rw' && !f.read_only;
          return (
            <span key={p.username} className="chip person">
              <Icon name="user" size={12} /> {p.username}
              <span className="chip-switch" role="group" aria-label={`${p.username} can`}>
                <button type="button" className={!canWrite ? 'on' : ''} disabled={busy} aria-pressed={!canWrite} onClick={() => void set(p.username, 'ro')}>
                  Read
                </button>
                <button
                  type="button"
                  className={canWrite ? 'on' : ''}
                  disabled={busy || f.read_only}
                  aria-pressed={canWrite}
                  title={f.read_only ? 'This folder is read only for everyone' : undefined}
                  onClick={() => void set(p.username, 'rw')}
                >
                  Write
                </button>
              </span>
              <button type="button" className="chip-x" disabled={busy} aria-label={`Remove ${p.username}`} title={`Remove ${p.username}`} onClick={() => void set(p.username, 'none', `${p.username} is out of ${f.name}`)}>
                <Icon name="close" size={11} />
              </button>
            </span>
          );
        })}
        {f.guest && (
          <span className="chip guest" title="Anyone on your network, no password">
            <Icon name="globe" size={12} /> Guests · {f.guest === 'rw' && !f.read_only ? 'read & write' : 'read'}
          </span>
        )}
        {outside.length > 0 && (
          <select
            className="select chip-add"
            value=""
            disabled={busy}
            aria-label={`Let someone open ${f.name}`}
            onChange={(e) => e.target.value && void set(e.target.value, f.read_only ? 'ro' : 'rw', `${e.target.value} can open ${f.name} now`)}
          >
            <option value="">+ Add someone</option>
            {outside.map((p) => (
              <option key={p.username} value={p.username}>
                {p.username}
                {p.access === 'files' ? ' (file sharing only)' : ''}
              </option>
            ))}
          </select>
        )}
      </div>
    </div>
  );
}

function FolderFields({ v, set }: { v: FolderInput; set: (p: Partial<FolderInput>) => void }) {
  return (
    <>
      <Row label="Description" hint="Optional; shown in network browsers" stacked>
        <input value={v.description ?? ''} maxLength={200} onChange={(e) => set({ description: e.target.value })} />
      </Row>
      <Toggle label="Recycle bin" hint="Deleted files go to the folder’s Trash first, from Files and over the network" checked={v.recycle ?? true} onChange={(on) => set({ recycle: on })} />
      <Toggle label="Visible on the network" hint="Listed when browsing this server; when off it opens only by name" checked={v.network ?? true} onChange={(on) => set({ network: on })} />
      <Toggle label="Read only for everyone" hint="Nobody can change it, whatever their permission" checked={v.read_only ?? false} onChange={(on) => set({ read_only: on })} />
      <GuestFields guest={v.guest ?? ''} readOnly={!!v.read_only} onChange={(guest) => set({ guest })} />
    </>
  );
}

/** "Guest access (no password)" with its mode and a clear warning. */
export function GuestFields({ guest, readOnly, onChange }: { guest: GuestMode; readOnly: boolean; onChange: (g: GuestMode) => void }) {
  return (
    <>
      <Toggle
        label="Guest access (no password)"
        hint="Anyone on your network can open this folder without signing in"
        checked={guest !== ''}
        onChange={(on) => onChange(on ? 'ro' : '')}
      />
      {guest !== '' && (
        <>
          <Row label="Guests can">
            <Choice
              value={readOnly ? 'ro' : guest}
              options={[
                { id: 'ro' as const, label: 'Only read' },
                ...(readOnly ? [] : [{ id: 'rw' as const, label: 'Read & write' }]),
              ]}
              onChange={(g) => onChange(g)}
            />
          </Row>
          <div className="guest-warn">
            <Icon name="alert" size={16} />
            <span>
              Anyone on this network — every phone, TV and visitor’s laptop — can open this folder without a password
              {guest === 'rw' && !readOnly ? ', and change or delete what’s in it' : ''}. Only use it for files you’d happily share with all of them.
            </span>
          </div>
        </>
      )}
    </>
  );
}

function EditFolder({ folder: f, onDone }: { folder: SharedFolder; onDone: () => void }) {
  const [v, setV] = useState<FolderInput>({ name: f.name, description: f.description, recycle: f.recycle, network: f.network, read_only: f.read_only, guest: f.guest ?? '' });
  const [busy, setBusy] = useState(false);
  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    const ok = await run(sharesApi.update(f.id, v), tr('files.share.saved', { name: v.name }));
    setBusy(false);
    if (ok) onDone();
  };
  return (
    <form className="share-form" onSubmit={(e) => void save(e)}>
      <Row label="Name" hint="Also its name on the network" stacked>
        <input value={v.name ?? ''} maxLength={40} onChange={(e) => setV({ ...v, name: e.target.value })} />
      </Row>
      <FolderFields v={v} set={(p) => setV({ ...v, ...p })} />
      <div className="user-add-foot">
        <button type="submit" disabled={busy || !v.name?.trim()}>
          {busy ? 'Saving…' : 'Save'}
        </button>
      </div>
    </form>
  );
}

/** Stop sharing, keeping the files — or erase them after typing "delete data". */
function DeleteFolder({ folder: f, onDone, onCancel }: { folder: SharedFolder; onDone: () => void; onCancel: () => void }) {
  const [typed, setTyped] = useState('');
  const [busy, setBusy] = useState(false);
  const go = async (deleteData: boolean) => {
    setBusy(true);
    const ok = await run(sharesApi.remove(f.id, deleteData, typed), deleteData ? tr('files.share.deletedAll', { name: f.name }) : tr('files.share.stopped', { name: f.name }));
    setBusy(false);
    if (ok) onDone();
  };
  const canErase = f.path !== '/' && !f.missing && !f.holds_homes;
  return (
    <div className="share-form share-delete">
      <p className="settings-row-hint">
        Stopping sharing keeps every file in {f.path}; people just can’t reach it any more.
        {canErase && ' To erase the folder and everything in it, type “delete data” first.'}
      </p>
      <div className="user-reset">
        <button type="button" className="small" disabled={busy} onClick={() => void go(false)}>
          Stop sharing, keep files
        </button>
        {canErase && (
          <>
            <input value={typed} placeholder="delete data" aria-label="Type delete data to erase the files" onChange={(e) => setTyped(e.target.value)} />
            <button type="button" className="small danger" disabled={busy || typed.trim().toLowerCase() !== 'delete data'} onClick={() => void go(true)}>
              Delete files too
            </button>
          </>
        )}
        <button type="button" className="ghost small" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </div>
  );
}

function AddFolder({ data, onAdded }: { data: SharesOverview; onAdded: () => void }) {
  const blank: FolderInput = { name: '', root: data.storage?.id ?? '', path: '', description: '', recycle: true, network: true, read_only: false, guest: '' };
  const [v, setV] = useState<FolderInput>(blank);
  const [busy, setBusy] = useState(false);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    const ok = await run(sharesApi.create(v), tr('files.share.created', { name: v.name }));
    setBusy(false);
    if (ok) {
      setV(blank);
      onAdded();
    }
  };
  return (
    <Section title="Add a shared folder" hint="Then pick who can open it. Until you do, only administrators can.">
      <form className="user-add" onSubmit={(e) => void submit(e)}>
        <Row label="Name" hint="Letters, numbers, spaces, - _ or . (up to 40)">
          <input value={v.name} maxLength={40} placeholder="Media" onChange={(e) => setV({ ...v, name: e.target.value })} />
        </Row>
        <Row label="Location">
          <select className="select" value={v.root} onChange={(e) => setV({ ...v, root: e.target.value })}>
            {data.locations.map((l) => (
              <option key={l.id} value={l.id}>
                {l.name}
              </option>
            ))}
          </select>
        </Row>
        <Row label="Folder" hint="An existing folder, e.g. /Media — leave empty to make a new folder named after the share">
          <input value={v.path} spellCheck={false} placeholder={`/${v.name || 'Media'}`} onChange={(e) => setV({ ...v, path: e.target.value })} />
        </Row>
        <FolderFields v={v} set={(p) => setV({ ...v, ...p })} />
        <div className="user-add-foot">
          <button type="submit" disabled={busy || !v.name?.trim()}>
            <Icon name="plus" size={14} /> {busy ? 'Sharing…' : 'Share folder'}
          </button>
        </div>
      </form>
    </Section>
  );
}
