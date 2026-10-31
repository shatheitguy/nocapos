import { useCallback, useEffect, useState } from 'react';
import { netApi, type Discovery, type FoundServer, type NetDrive, type NetSupport, type Sharing } from '../api/netdrive';
import { Icon } from '../components/Icon';
import { toast } from '../state/toasts';
import { Choice, Row, Section, Toggle } from './Personalize';

/** Network drives and sharing (administrators only; off for everyone else). */
export function useNetwork(enabled = true) {
  const [support, setSupport] = useState<NetSupport | null>(null);
  const [drives, setDrives] = useState<NetDrive[]>([]);
  const [sharing, setSharing] = useState<Sharing | null>(null);
  const [error, setError] = useState('');
  const load = useCallback(async () => {
    if (!enabled) return;
    const r = await netApi.overview();
    if (!r.ok) return setError(r.error ?? 'Couldn’t load');
    setError('');
    setSupport(r.data.support);
    setDrives(r.data.drives);
    setSharing(r.data.sharing);
  }, [enabled]);
  useEffect(() => {
    void load();
  }, [load]);
  return { support, drives, sharing, error, load };
}

/** Install a missing host tool (cifs-utils, NFS, Samba). */
function InstallRow({ label, hint, tool, support, cmd, onDone }: { label: string; hint: string; tool: 'smb' | 'nfs' | 'samba' | 'smbclient'; support: NetSupport; cmd: string; onDone: () => void }) {
  const [busy, setBusy] = useState(false);
  const install = async () => {
    setBusy(true);
    const r = await netApi.install(tool);
    setBusy(false);
    if (!r.ok) return toast('error', `Couldn’t install ${label}`, r.error);
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
        (Files → File Sharing) works everywhere.
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
  // SMB shares of the server picked from the network list (null = not asked).
  const [smbShares, setSmbShares] = useState<{ host: string; shares: string[] | null; error?: string } | null>(null);

  const pickServer = async (s: FoundServer) => {
    setHost(s.host);
    setKind('smb');
    setShare('');
    if (s.browsable) return setSmbShares({ host: s.host, shares: s.shares ?? [] });
    setSmbShares({ host: s.host, shares: null });
    const r = await netApi.serverShares(s.host);
    setSmbShares({ host: s.host, shares: r.ok ? r.data.shares : [], error: r.ok ? undefined : r.error });
  };

  const find = async () => {
    setExports(null);
    const r = await netApi.exports(host);
    if (!r.ok) return setError(r.error ?? 'Couldn’t list the shares');
    setError('');
    setExports(r.data.exports);
  };
  const add = async () => {
    setBusy(true);
    setError('');
    const r = await netApi.add({ name: name || share.split('/').filter(Boolean).pop() || host, kind, host, share, username, password, auto });
    setBusy(false);
    if (!r.ok) return setError(r.error ?? 'Couldn’t connect');
    toast('success', `${r.data.name} connected`, 'Find it in Files.');
    setName('');
    setShare('');
    setPassword('');
    void onAdded();
  };

  return (
    <>
    {support.smb && <ServerScan support={support} picked={host} onPick={(s) => void pickServer(s)} />}
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
        <input
          value={host}
          spellCheck={false}
          placeholder="192.168.1.20"
          onChange={(e) => {
            setHost(e.target.value);
            setSmbShares(null);
          }}
        />
      </Row>
      {kind === 'smb' && smbShares && smbShares.host === host && (
        <div className="nd-chips">
          {smbShares.shares === null ? (
            <span className="muted small">
              <span className="spinner sm" /> Asking {host} for its shared folders…
            </span>
          ) : smbShares.shares.length ? (
            smbShares.shares.map((x) => (
              <button key={x} type="button" className={`nd-chip ${share === x ? 'on' : ''}`} onClick={() => setShare(x)}>
                {x}
              </button>
            ))
          ) : (
            <span className="muted small">{smbShares.error ?? `${host} didn’t list any shared folders; type the share’s name.`}</span>
          )}
        </div>
      )}
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
    </>
  );
}

/** File servers found on the local network (SMB), refreshable. */
function ServerScan({ support, picked, onPick }: { support: NetSupport; picked: string; onPick: (s: FoundServer) => void }) {
  const [scan, setScan] = useState<Discovery | null>(null);
  const [scanning, setScanning] = useState(false);
  const [error, setError] = useState('');
  const run = useCallback(async () => {
    setScanning(true);
    const r = await netApi.discover();
    setScanning(false);
    if (!r.ok) return setError(r.error ?? 'Couldn’t scan the network');
    setError('');
    setScan(r.data);
  }, []);
  useEffect(() => {
    void run();
  }, [run]);
  const tools = scan?.tools;
  return (
    <Section title="On your network">
      <div className="nd-scan">
        {scanning && !scan && (
          <p className="muted small nd-pad">
            <span className="spinner sm" /> Looking for file servers…
          </p>
        )}
        {error && <p className="error nd-pad">{error}</p>}
        {scan && scan.servers.length === 0 && !scanning && (
          <p className="muted small nd-pad">{tools && !tools.avahi && !tools.nmblookup ? 'Can’t look for servers yet.' : 'No file servers answered. Type the server’s address below.'}</p>
        )}
        {scan?.servers.map((s) => (
          <button key={s.host} type="button" className={`nd-server ${picked === s.host ? 'on' : ''}`} onClick={() => onPick(s)}>
            <Icon name="network" size={16} />
            <span className="nd-server-text">
              <b>{s.name || s.hostname || s.host}</b>
              <span className="muted small">
                {s.host}
                {s.hostname && s.hostname !== s.name ? ` · ${s.hostname}` : ''}
                {s.browsable ? ` · ${s.shares?.length ? s.shares.join(', ') : 'no shared folders listed'}` : ''}
              </span>
            </span>
          </button>
        ))}
        {scan?.hint && <p className="muted small nd-pad">{scan.hint}</p>}
      </div>
      {support.native && !support.smbclient && (
        <InstallRow label="Samba client tools" hint="Find servers on your network and list their shared folders" tool="smbclient" support={support} cmd="sudo apt install smbclient" onDone={() => void run()} />
      )}
      <div className="nd-footer">
        <button type="button" className="ghost" disabled={scanning} onClick={() => void run()}>
          {scanning ? <span className="spinner sm" /> : <Icon name="restart" size={14} />} {scanning ? 'Scanning…' : 'Scan again'}
        </button>
      </div>
    </Section>
  );
}

