import { useCallback, useEffect, useState } from 'react';
import { fileApi, type FileRoot } from '../api/files';
import { netApi, type NetDrive, type NetSupport, type Sharing } from '../api/netdrive';
import { Icon } from '../components/Icon';
import { toast } from '../state/toasts';
import { Choice, Row, Section, Toggle } from './Personalize';

export function useNetwork() {
  const [support, setSupport] = useState<NetSupport | null>(null);
  const [drives, setDrives] = useState<NetDrive[]>([]);
  const [sharing, setSharing] = useState<Sharing | null>(null);
  const [error, setError] = useState('');
  const load = useCallback(async () => {
    const r = await netApi.overview();
    if (!r.ok) return setError(r.error ?? 'Could not load');
    setError('');
    setSupport(r.data.support);
    setDrives(r.data.drives);
    setSharing(r.data.sharing);
  }, []);
  useEffect(() => {
    void load();
  }, [load]);
  return { support, drives, sharing, error, load };
}

/** Install a missing host tool (cifs-utils, NFS, Samba). */
function InstallRow({ label, hint, tool, support, cmd, onDone }: { label: string; hint: string; tool: 'smb' | 'nfs' | 'samba'; support: NetSupport; cmd: string; onDone: () => void }) {
  const [busy, setBusy] = useState(false);
  const install = async () => {
    setBusy(true);
    const r = await netApi.install(tool);
    setBusy(false);
    if (!r.ok) return toast('error', `Could not install ${label}`, r.error);
    toast('success', `${label} installed`);
    onDone();
  };
  return (
    <Row label={label} hint={hint}>
      {support.can_install ? (
        <button type="button" className="ghost" disabled={busy} onClick={() => void install()}>
          {busy ? <span className="spinner sm" /> : <Icon name="download" size={14} />} {busy ? 'Installing…' : 'Install'}
        </button>
      ) : (
        <code className="nd-cmd">{cmd}</code>
      )}
    </Row>
  );
}

function NotNative() {
  return (
    <Section title="Not available here">
      <p className="nd-note">
        Network drives and SMB sharing need NoCapOS installed on Linux, where it runs as root to mount shares and run Samba. WebDAV sharing
        (Settings → File Sharing) works everywhere.
      </p>
    </Section>
  );
}

// ---------------- Network drives ----------------

/** "Connect to Server" in Files: install missing support, then the form. */
export function ConnectServerPanel({ onConnected }: { onConnected: () => void }) {
  const { support, error, load } = useNetwork();
  if (!support) return error ? <p className="error">{error}</p> : <span className="spinner" />;
  return (
    <div className="stack">
      {!support.native && <NotNative />}
      {support.native && (!support.smb || !support.nfs) && (
        <Section title="Support">
          {!support.smb && <InstallRow label="SMB support" hint="For Windows shares and most NAS boxes (cifs-utils)" tool="smb" support={support} cmd="sudo apt install cifs-utils" onDone={load} />}
          {!support.nfs && <InstallRow label="NFS support" hint="For Linux servers and NAS exports" tool="nfs" support={support} cmd="sudo apt install nfs-common" onDone={load} />}
        </Section>
      )}
      {support.native && (support.smb || support.nfs) && (
        <AddDrive
          support={support}
          onAdded={async () => {
            await load();
            onConnected();
          }}
        />
      )}
    </div>
  );
}

function AddDrive({ support, onAdded }: { support: NetSupport; onAdded: () => Promise<void> }) {
  const [kind, setKind] = useState<'smb' | 'nfs'>(support.smb ? 'smb' : 'nfs');
  const [name, setName] = useState('');
  const [host, setHost] = useState('');
  const [share, setShare] = useState('');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [auto, setAuto] = useState(true);
  const [exports, setExports] = useState<string[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const find = async () => {
    setExports(null);
    const r = await netApi.exports(host);
    if (!r.ok) return setError(r.error ?? 'Could not list the shares');
    setError('');
    setExports(r.data.exports);
  };
  const add = async () => {
    setBusy(true);
    setError('');
    const r = await netApi.add({ name: name || share.split('/').filter(Boolean).pop() || host, kind, host, share, username, password, auto });
    setBusy(false);
    if (!r.ok) return setError(r.error ?? 'Could not connect');
    toast('success', `${r.data.name} connected`, 'It is now in Files.');
    setName('');
    setShare('');
    setPassword('');
    void onAdded();
  };

  return (
    <Section title="Add a network drive">
      <Row label="Type">
        <Choice
          value={kind}
          options={[
            ...(support.smb ? [{ id: 'smb' as const, label: 'SMB (Windows, NAS)' }] : []),
            ...(support.nfs ? [{ id: 'nfs' as const, label: 'NFS (Linux)' }] : []),
          ]}
          onChange={(k) => {
            setKind(k);
            setExports(null);
          }}
        />
      </Row>
      <Row label="Server" hint="Name or IP address, e.g. 192.168.1.20 or nas.local">
        <input value={host} spellCheck={false} placeholder="192.168.1.20" onChange={(e) => setHost(e.target.value)} />
      </Row>
      <Row label={kind === 'smb' ? 'Share' : 'Export'} hint={kind === 'smb' ? 'The shared folder’s name, e.g. Media' : 'e.g. /volume1/media'}>
        <div className="nd-inline">
          <input value={share} spellCheck={false} placeholder={kind === 'smb' ? 'Media' : '/volume1/media'} onChange={(e) => setShare(e.target.value)} />
          {kind === 'nfs' && (
            <button type="button" className="ghost" disabled={!host.trim()} onClick={() => void find()}>
              Find
            </button>
          )}
        </div>
      </Row>
      {exports && (
        <div className="nd-chips">
          {exports.length ? (
            exports.map((x) => (
              <button key={x} type="button" className={`nd-chip ${share === x ? 'on' : ''}`} onClick={() => setShare(x)}>
                {x}
              </button>
            ))
          ) : (
            <span className="muted small">No exports found on {host}.</span>
          )}
        </div>
      )}
      {kind === 'smb' && (
        <>
          <Row label="User name" hint="Leave empty for guest access. DOMAIN\user works too.">
            <input value={username} spellCheck={false} autoComplete="off" onChange={(e) => setUsername(e.target.value)} />
          </Row>
          <Row label="Password">
            <input type="password" value={password} autoComplete="new-password" onChange={(e) => setPassword(e.target.value)} />
          </Row>
        </>
      )}
      <Row label="Name in Files" hint="Optional">
        <input value={name} placeholder={share.split('/').filter(Boolean).pop() || 'NAS'} onChange={(e) => setName(e.target.value)} />
      </Row>
      <Toggle label="Connect at startup" hint="Reconnects automatically after a restart or when the server comes back" checked={auto} onChange={setAuto} />
      {error && <p className="error nd-pad">{error}</p>}
      <div className="nd-footer">
        <button type="button" disabled={busy || !host.trim() || !share.trim()} onClick={() => void add()}>
          {busy ? <span className="spinner sm" /> : <Icon name="plus" size={14} />} {busy ? 'Connecting…' : 'Connect'}
        </button>
      </div>
    </Section>
  );
}

// ---------------- File sharing ----------------

/** File Sharing in Files: the sharing account, protocols and shared folders. */
export function FileSharingPanel() {
  const { support, sharing, error, load } = useNetwork();
  const [roots, setRoots] = useState<FileRoot[]>([]);
  useEffect(() => {
    void fileApi.roots().then((r) => r.ok && setRoots(r.data.filter((x) => x.id !== 'system')));
  }, []);
  if (!support || !sharing) return error ? <p className="error">{error}</p> : <span className="spinner" />;

  const setProto = async (p: { smb?: boolean; webdav?: boolean }) => {
    const r = await netApi.setProtocols(p);
    if (!r.ok) toast('error', 'Could not change sharing', r.error);
    void load();
  };
  const host = window.location.hostname;
  const davURL = `${window.location.origin}/dav/`;
  const copy = (text: string) => void navigator.clipboard?.writeText(text).then(() => toast('success', 'Copied', text));
  const rootName = (id: string) => roots.find((r) => r.id === id)?.name ?? id;

  return (
    <div className="stack">
      <p className="settings-section-hint">Share folders with your computers and phones on the network. Everyone signs in with the sharing account below.</p>

      <PasswordSection sharing={sharing} onDone={load} />

      <Section title="Share over">
        {support.native && !support.samba ? (
          <InstallRow label="SMB (Windows, Mac, Linux)" hint="Needs Samba on the server" tool="samba" support={support} cmd="sudo apt install samba" onDone={load} />
        ) : (
          <Toggle
            label="SMB (Windows, Mac, Linux)"
            hint={support.native ? 'Shows up like a normal network drive in File Explorer and Finder' : 'Needs NoCapOS installed on Linux'}
            checked={sharing.smb}
            disabled={!support.native || !sharing.password_set}
            onChange={(on) => void setProto({ smb: on })}
          />
        )}
        <Toggle
          label="WebDAV (phones, anywhere)"
          hint="Works from phone file apps and from outside your home through the NoCapOS address"
          checked={sharing.webdav}
          disabled={!sharing.password_set}
          onChange={(on) => void setProto({ webdav: on })}
        />
      </Section>

      <Section title="Shared folders">
        {sharing.shares.map((sh) => (
          <div key={sh.id} className="nd-drive">
            <span className="nd-icon on">
              <Icon name="folder" size={18} />
            </span>
            <div className="nd-main">
              <b>{sh.name}</b>
              <span className="muted small">
                {rootName(sh.root)}
                {sh.path !== '/' ? ` › ${sh.path.replace(/^\//, '').replace(/\//g, ' › ')}` : ''}
              </span>
              {sh.missing && <span className="nd-status bad">The folder is missing</span>}
              <label className="nd-auto toggle">
                <input type="checkbox" role="switch" checked={sh.read_only} onChange={(e) => void netApi.setShareReadOnly(sh.id, e.target.checked).then(() => load())} /> Read only
              </label>
            </div>
            <div className="nd-actions">
              <button
                type="button"
                className="ghost danger"
                aria-label={`Stop sharing ${sh.name}`}
                title="Stop sharing"
                onClick={async () => {
                  const r = await netApi.removeShare(sh.id);
                  if (!r.ok) toast('error', 'Could not stop sharing', r.error);
                  void load();
                }}
              >
                <Icon name="trash" size={14} />
              </button>
            </div>
          </div>
        ))}
        <AddShare roots={roots} onAdded={load} />
      </Section>

      {(sharing.smb || sharing.webdav) && sharing.shares.length > 0 && (
        <Section title="How to connect" hint={`Sign in as “${sharing.user}” with the sharing password.`}>
          {sharing.smb && (
            <>
              <Row label="Windows" hint="File Explorer → This PC → Map network drive">
                <button type="button" className="ghost nd-addr" onClick={() => copy(`\\\\${host}\\${sharing.shares[0].name}`)}>
                  <code>{`\\\\${host}\\${sharing.shares[0].name}`}</code> <Icon name="copy" size={13} />
                </button>
              </Row>
              <Row label="Mac and Linux" hint="Finder → Go → Connect to Server, or your file manager">
                <button type="button" className="ghost nd-addr" onClick={() => copy(`smb://${host}/${sharing.shares[0].name}`)}>
                  <code>{`smb://${host}/${sharing.shares[0].name}`}</code> <Icon name="copy" size={13} />
                </button>
              </Row>
            </>
          )}
          {sharing.webdav && (
            <Row label="WebDAV" hint="Phone file apps, Finder, Linux (davs://), Windows Map network drive">
              <button type="button" className="ghost nd-addr" onClick={() => copy(davURL)}>
                <code>{davURL}</code> <Icon name="copy" size={13} />
              </button>
            </Row>
          )}
        </Section>
      )}
    </div>
  );
}

function PasswordSection({ sharing, onDone }: { sharing: Sharing; onDone: () => Promise<void> }) {
  const [editing, setEditing] = useState(!sharing.password_set);
  const [pw, setPw] = useState('');
  const [pw2, setPw2] = useState('');
  const [busy, setBusy] = useState(false);
  const save = async () => {
    if (pw !== pw2) return toast('error', 'The passwords don’t match');
    setBusy(true);
    const r = await netApi.setPassword(pw);
    setBusy(false);
    if (!r.ok) return toast('error', 'Could not set the password', r.error);
    toast('success', 'Sharing password saved');
    setPw('');
    setPw2('');
    setEditing(false);
    void onDone();
  };
  return (
    <Section title="Sharing account" hint="A separate password just for file sharing, so your own NoCapOS password never goes into other devices.">
      <Row label="User name">
        <code className="nd-user">{sharing.user}</code>
      </Row>
      {editing ? (
        <>
          <Row label="Password" hint="At least 8 characters">
            <input type="password" value={pw} autoComplete="new-password" onChange={(e) => setPw(e.target.value)} />
          </Row>
          <Row label="Confirm">
            <input type="password" value={pw2} autoComplete="new-password" onChange={(e) => setPw2(e.target.value)} />
          </Row>
          <div className="nd-footer">
            {sharing.password_set && (
              <button type="button" className="ghost" onClick={() => setEditing(false)}>
                Cancel
              </button>
            )}
            <button type="button" disabled={busy || pw.length < 8} onClick={() => void save()}>
              {busy ? 'Saving…' : 'Save Password'}
            </button>
          </div>
        </>
      ) : (
        <Row label="Password" hint="Set">
          <button type="button" className="ghost" onClick={() => setEditing(true)}>
            <Icon name="key" size={14} /> Change
          </button>
        </Row>
      )}
    </Section>
  );
}

function AddShare({ roots, onAdded }: { roots: FileRoot[]; onAdded: () => Promise<void> }) {
  const [root, setRoot] = useState('');
  const [folders, setFolders] = useState<string[]>([]);
  const [folder, setFolder] = useState('/');
  const [name, setName] = useState('');
  const [readOnly, setReadOnly] = useState(false);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (!root && roots[0]) setRoot(roots[0].id);
  }, [roots, root]);
  useEffect(() => {
    if (!root) return;
    void fileApi.list(root, '/').then((l) => setFolders(l.ok ? l.data.entries.filter((e) => e.dir && !e.name.startsWith('.')).map((e) => e.name) : []));
  }, [root]);
  const suggested = folder === '/' ? (roots.find((r) => r.id === root)?.name ?? '').replace(/[^A-Za-z0-9 _.-]/g, '').trim() : folder.slice(1);
  const add = async () => {
    setBusy(true);
    const r = await netApi.addShare({ name: name.trim() || suggested, root, path: folder, read_only: readOnly });
    setBusy(false);
    if (!r.ok) return toast('error', 'Could not share it', r.error);
    toast('success', `Sharing “${r.data.name}”`);
    setName('');
    void onAdded();
  };
  return (
    <div className="nd-addshare">
      <select value={root} onChange={(e) => setRoot(e.target.value)} aria-label="Location">
        {roots.map((r) => (
          <option key={r.id} value={r.id}>
            {r.name}
          </option>
        ))}
      </select>
      <select value={folder} onChange={(e) => setFolder(e.target.value)} aria-label="Folder">
        <option value="/">Whole location</option>
        {folders.map((f) => (
          <option key={f} value={`/${f}`}>
            {f}
          </option>
        ))}
      </select>
      <input value={name} placeholder={suggested || 'Share name'} maxLength={40} onChange={(e) => setName(e.target.value)} aria-label="Share name" />
      <label className="toggle nd-ro">
        <input type="checkbox" role="switch" checked={readOnly} onChange={(e) => setReadOnly(e.target.checked)} /> Read only
      </label>
      <button type="button" disabled={busy || !root} onClick={() => void add()}>
        <Icon name="plus" size={14} /> Share
      </button>
    </div>
  );
}
