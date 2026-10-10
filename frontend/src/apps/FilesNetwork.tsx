import { useEffect, useState, type HTMLAttributes, type MouseEvent, type ReactNode } from 'react';
import { netApi, type NetDrive, type NetShare, type NetSupport, type Sharing } from '../api/netdrive';
import type { MenuEntry } from '../components/Dialog';
import { FileGlyph } from './FilesParts';
import { Icon } from '../components/Icon';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import type { FileRoot } from '../api/files';
import { CloudImportsPanel } from './CloudImports';
import { ConnectServerPanel, FileSharingPanel } from './NetworkDrives';
import { Toggle } from './Personalize';

// Network drives and sharing, inside Files: the sidebar's Network section,
// "Connect to Server…", "File Sharing…" and a folder's "Share on Network…".

/** A large dialog inside the Files window. */
export function FilesModal({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation();
        onClose();
      }
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, [onClose]);
  return (
    <div className="dialog-backdrop" onPointerDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="dialog fn-modal" role="dialog" aria-label={title} onKeyDown={(e) => e.stopPropagation()}>
        <header className="fn-modal-head">
          <h3>{title}</h3>
          <button type="button" className="ghost icon-btn neutral" aria-label="Close" onClick={onClose}>
            <Icon name="close" size={16} />
          </button>
        </header>
        <div className="fn-modal-body">{children}</div>
      </div>
    </div>
  );
}

/** A titled sidebar group with an optional button at the right of its title. */
export function SideSection({ title, action, children, className = '', ...drop }: { title: string; action?: ReactNode; children: ReactNode; className?: string } & Pick<HTMLAttributes<HTMLDivElement>, 'onDragOver' | 'onDragLeave' | 'onDrop'>) {
  return (
    <div className={`fs-section ${className}`} {...drop}>
      <div className="fs-head">
        <span>{title}</span>
        {action}
      </div>
      {children}
    </div>
  );
}

/**
 * The sidebar's "Shared folders" and "Network devices" groups: folders shared
 * on the network, connected network drives, and the dialogs to connect to a
 * server, set up file sharing and import from the cloud.
 */
export function NetworkSections({
  drives,
  sharing,
  current,
  roots,
  onGo,
  onChanged,
  onUnshare,
  openMenu,
}: {
  drives: NetDrive[];
  sharing: Sharing | null;
  current?: { root: string; path: string };
  roots: FileRoot[];
  onGo: (root: string, path?: string) => void;
  onChanged: () => Promise<void>;
  onUnshare: (share: NetShare) => void;
  openMenu: (e: MouseEvent, items: MenuEntry[]) => void;
}) {
  const [dialog, setDialog] = useState<'connect' | 'sharing' | 'cloud' | null>(null);
  const [busy, setBusy] = useState<number | null>(null);

  const connect = async (d: NetDrive) => {
    setBusy(d.id);
    const r = await netApi.connect(d.id, true);
    setBusy(null);
    if (!r.ok) return toast('error', `Couldn't connect to ${d.name}`, r.error);
    await onChanged();
    onGo(r.data.root_id);
  };
  const eject = async (d: NetDrive) => {
    setBusy(d.id);
    const r = await netApi.connect(d.id, false);
    setBusy(null);
    if (!r.ok) return toast('error', `Couldn't disconnect ${d.name}`, r.error);
    await onChanged();
  };
  const forget = async (d: NetDrive) => {
    const ok = await confirmDialog({
      title: `Remove “${d.name}”?`,
      message: 'NoCapOS disconnects it and forgets it. The files stay on the other device.',
      confirmLabel: 'Remove',
      danger: true,
    });
    if (!ok) return;
    const r = await netApi.remove(d.id);
    if (!r.ok) toast('error', 'Could not remove it', r.error);
    await onChanged();
  };
  const shares = sharing?.shares ?? [];
  const sharingOn = !!sharing && (sharing.smb || sharing.webdav);
  const addMenu: MenuEntry[] = [
    { label: 'Connect to Server…', icon: 'network', onClick: () => setDialog('connect') },
    { label: sharingOn ? 'File Sharing (on)…' : 'File Sharing…', icon: 'users', onClick: () => setDialog('sharing') },
    { label: 'Cloud Imports…', icon: 'download', onClick: () => setDialog('cloud') },
  ];

  return (
    <>
      {shares.length > 0 && (
        <SideSection
          title="Shared folders"
          action={
            <button type="button" className="fs-add" title="File Sharing settings" aria-label="File Sharing settings" onClick={() => setDialog('sharing')}>
              <Icon name="sliders" size={13} />
            </button>
          }
        >
          {shares.map((sh) => {
            const on = current?.root === sh.root && current.path === sh.path;
            return (
              <button
                key={sh.id}
                type="button"
                className={`fs-item ${on ? 'on' : ''}`}
                title={`${sh.name}${sh.read_only ? ' (read only)' : ''}`}
                onClick={() => onGo(sh.root, sh.path)}
                onContextMenu={(e) =>
                  openMenu(e, [
                    { label: 'Open', icon: 'folder', onClick: () => onGo(sh.root, sh.path) },
                    { label: `Stop Sharing “${sh.name}”`, icon: 'network', danger: true, onClick: () => onUnshare(sh) },
                  ])
                }
              >
                <FileGlyph name={sh.name} dir size={18} shared />
                <span className="fs-name">{sh.name}</span>
              </button>
            );
          })}
        </SideSection>
      )}

      <SideSection
        title="Network devices"
        action={
          <button type="button" className="fs-add" title="Connect to a server, share files or import from the cloud" aria-label="Add network device" onClick={(e) => openMenu(e, addMenu)}>
            <Icon name="plus" size={14} />
          </button>
        }
      >
        {drives.map((d) => (
          <div key={d.id} className={`fn-drive ${d.mounted ? '' : 'off'} ${d.mounted && current?.root === d.root_id ? 'on' : ''}`}>
            <button
              type="button"
              className="fn-drive-main"
              title={d.mounted ? d.address : d.error ? `${d.address} — ${d.error}` : `${d.address} — click to connect`}
              disabled={busy === d.id}
              onClick={() => (d.mounted ? onGo(d.root_id) : void connect(d))}
              onContextMenu={(e) =>
                openMenu(e, [
                  d.mounted
                    ? { label: 'Disconnect', icon: 'eject', onClick: () => void eject(d) }
                    : { label: 'Connect', icon: 'network', onClick: () => void connect(d) },
                  { label: `Remove “${d.name}”…`, icon: 'trash', danger: true, onClick: () => void forget(d) },
                ])
              }
            >
              {busy === d.id ? <span className="spinner sm" /> : <Icon name="network" size={16} />}
              <span className="fn-drive-name">{d.name}</span>
              {!d.mounted && d.error && <Icon name="alert" size={13} />}
            </button>
            {d.mounted && (
              <button type="button" className="fn-eject" aria-label={`Disconnect ${d.name}`} title="Disconnect" disabled={busy === d.id} onClick={() => void eject(d)}>
                <Icon name="eject" size={14} />
              </button>
            )}
          </div>
        ))}
        {!drives.length && (
          <button type="button" className="fs-item fs-hint" onClick={() => setDialog('connect')}>
            <Icon name="plus" size={15} /> <span className="fs-name">Connect to Server…</span>
          </button>
        )}
      </SideSection>

      {dialog === 'connect' && (
        <FilesModal title="Connect to Server" onClose={() => setDialog(null)}>
          <p className="fn-intro">Connect a shared folder from a NAS, a Windows PC or another server. It shows up here, and in Photos and Backups.</p>
          <ConnectServerPanel
            onConnected={() => {
              setDialog(null);
              void onChanged();
            }}
          />
        </FilesModal>
      )}
      {dialog === 'cloud' && (
        <FilesModal title="Cloud Imports" onClose={() => setDialog(null)}>
          <CloudImportsPanel
            roots={roots}
            onOpen={(root, path) => {
              setDialog(null);
              onGo(root, path);
            }}
          />
        </FilesModal>
      )}
      {dialog === 'sharing' && (
        <FilesModal
          title="File Sharing"
          onClose={() => {
            setDialog(null);
            void onChanged();
          }}
        >
          <FileSharingPanel />
        </FilesModal>
      )}
    </>
  );
}

const shareSafe = (n: string) => n.replace(/[^A-Za-z0-9 _.-]/g, '').trim().slice(0, 40) || 'Shared';

/** "Share on Network…" for one folder: sets sharing up on first use. */
export function ShareFolderDialog({ root, path, name, onClose, onShared }: { root: string; path: string; name: string; onClose: () => void; onShared: () => void }) {
  const [state, setState] = useState<{ support: NetSupport; sharing: Sharing } | null>(null);
  const [shareName, setShareName] = useState(shareSafe(name));
  const [readOnly, setReadOnly] = useState(false);
  const [pw, setPw] = useState('');
  const [pw2, setPw2] = useState('');
  const [smb, setSmb] = useState(false);
  const [dav, setDav] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [done, setDone] = useState<string | null>(null);

  useEffect(() => {
    void netApi.overview().then((r) => {
      if (!r.ok) return setError(r.error ?? 'Could not load sharing');
      setState({ support: r.data.support, sharing: r.data.sharing });
      const canSmb = r.data.support.native && r.data.support.samba;
      setSmb(r.data.sharing.smb || (canSmb && !r.data.sharing.webdav));
      setDav(r.data.sharing.webdav || !canSmb);
    });
  }, []);

  if (!state) {
    return (
      <FilesModal title={`Share “${name}”`} onClose={onClose}>
        {error ? <p className="error">{error}</p> : <span className="spinner" />}
      </FilesModal>
    );
  }
  const { support, sharing } = state;
  const needPw = !sharing.password_set;
  const canSmb = support.native && support.samba;

  const share = async () => {
    setError('');
    if (needPw && pw !== pw2) return setError('The passwords don’t match');
    if (!smb && !dav) return setError('Choose at least one way to share');
    setBusy(true);
    try {
      if (needPw) {
        const r = await netApi.setPassword(pw);
        if (!r.ok) return setError(r.error ?? 'Could not set the password');
      }
      if (smb !== sharing.smb || dav !== sharing.webdav) {
        const r = await netApi.setProtocols({ smb: smb || sharing.smb, webdav: dav || sharing.webdav });
        if (!r.ok) return setError(r.error ?? 'Could not turn sharing on');
      }
      const r = await netApi.addShare({ name: shareName.trim(), root, path, read_only: readOnly });
      if (!r.ok) return setError(r.error ?? 'Could not share the folder');
      setDone(r.data.name);
      onShared();
    } finally {
      setBusy(false);
    }
  };

  const host = window.location.hostname;
  const copy = (text: string) => void navigator.clipboard?.writeText(text).then(() => toast('success', 'Copied', text));
  if (done) {
    const addrs = [
      ...(smb || sharing.smb ? [`\\\\${host}\\${done}`, `smb://${host}/${done}`] : []),
      ...(dav || sharing.webdav ? [`${window.location.origin}/dav/${encodeURIComponent(done)}/`] : []),
    ];
    return (
      <FilesModal title={`Sharing “${done}”`} onClose={onClose}>
        <div className="fn-done">
          <span className="fn-done-icon">
            <Icon name="check" size={22} />
          </span>
          <p>
            Other devices can open it now. Sign in as <b>{sharing.user}</b> with the sharing password.
          </p>
          {addrs.map((a) => (
            <button key={a} type="button" className="ghost fn-addr" onClick={() => copy(a)}>
              <code>{a}</code> <Icon name="copy" size={13} />
            </button>
          ))}
          <button type="button" onClick={onClose}>
            Done
          </button>
        </div>
      </FilesModal>
    );
  }

  return (
    <FilesModal title={`Share “${name}”`} onClose={onClose}>
      <div className="fn-form">
        <label>
          Share name
          <input value={shareName} maxLength={40} onChange={(e) => setShareName(e.target.value)} />
        </label>
        <Toggle label="Read only" hint="Others can open and copy files, but not change or delete them" checked={readOnly} onChange={setReadOnly} />
        {needPw && (
          <>
            <p className="fn-intro">
              Everyone signs in as <b>{sharing.user}</b> with a sharing password, so your own password never goes into other devices.
            </p>
            <label>
              Sharing password
              <input type="password" value={pw} autoComplete="new-password" placeholder="At least 8 characters" onChange={(e) => setPw(e.target.value)} />
            </label>
            <label>
              Confirm
              <input type="password" value={pw2} autoComplete="new-password" onChange={(e) => setPw2(e.target.value)} />
            </label>
          </>
        )}
        {(!sharing.smb || !sharing.webdav) && (
          <>
            {canSmb && !sharing.smb && <Toggle label="Share with Windows, Mac and Linux (SMB)" checked={smb} onChange={setSmb} />}
            {!sharing.webdav && <Toggle label="Share with phones and over the internet (WebDAV)" checked={dav} onChange={setDav} />}
            {support.native && !support.samba && <p className="muted small">For Windows and Mac sharing, install Samba under File Sharing…</p>}
          </>
        )}
        {error && <p className="error">{error}</p>}
        <div className="dialog-actions">
          <button type="button" className="ghost" onClick={onClose}>
            Cancel
          </button>
          <button type="button" disabled={busy || !shareName.trim() || (needPw && pw.length < 8)} onClick={() => void share()}>
            {busy ? 'Sharing…' : 'Share'}
          </button>
        </div>
      </div>
    </FilesModal>
  );
}
