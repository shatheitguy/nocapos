import { tr } from '../i18n';
import { useEffect, useState, type FormEvent } from 'react';
import { netApi, type HealthItem, type MySMB, type SMBHealth } from '../api/netdrive';
import { Icon } from '../components/Icon';
import { toast } from '../state/toasts';

// Files → File Sharing: is SMB actually reachable? A status card with the
// addresses to use (or exactly what's wrong, with a Fix button), turning SMB
// on for your own account, and how to connect from each kind of device.

const copy = (text: string) => void navigator.clipboard?.writeText(text).then(() => toast('success', tr('files.toast.copiedText'), text));

function CopyAddr({ text }: { text: string }) {
  return (
    <button type="button" className="ghost fn-addr" title="Copy" onClick={() => copy(text)}>
      <code>{text}</code> <Icon name="copy" size={13} />
    </button>
  );
}

/** The server's name to type: its host name, else the address the browser used. */
export function smbHost(h: SMBHealth | null): string {
  return h?.host || window.location.hostname;
}

/** SMB health: live (with addresses) or what's wrong, with fixes. */
export function SmbStatusCard({ health, onChanged }: { health: SMBHealth; onChanged: (h: SMBHealth) => void }) {
  const [busy, setBusy] = useState<string | null>(null);
  const fix = async (it: HealthItem) => {
    setBusy(it.id);
    const r = await netApi.fix(it.id);
    setBusy(null);
    if (!r.ok) return toast('error', tr('files.toast.fixFail', { what: it.label.toLowerCase() }), r.error);
    toast('success', tr('files.toast.fixed'), it.label);
    onChanged(r.data);
  };
  if (!health.smb) return null;
  const host = smbHost(health);
  const ip = health.addresses[0];
  const issues = health.items.filter((i) => i.state !== 'ok');
  return (
    <div className={`smb-card ${health.live ? 'live' : 'down'}`}>
      <div className="smb-card-head">
        <span className="smb-dot" aria-hidden />
        {health.live ? (
          <strong>SMB is live</strong>
        ) : (
          <strong>SMB isn’t reachable yet — {issues.filter((i) => i.state === 'bad').length} thing(s) to sort</strong>
        )}
      </div>
      {health.live && (
        <div className="smb-addrs">
          <CopyAddr text={`\\\\${host}`} />
          {ip && <CopyAddr text={`smb://${ip}`} />}
          {ip && host !== ip && <CopyAddr text={`\\\\${ip}`} />}
        </div>
      )}
      {issues.length > 0 && (
        <ul className="smb-issues">
          {issues.map((it) => (
            <li key={it.id} className={it.state}>
              <Icon name={it.state === 'bad' ? 'alert' : 'info'} size={15} />
              <span>
                <b>{it.label}:</b> {it.detail}
              </span>
              {it.fix && (
                <button type="button" className="small" disabled={busy !== null} onClick={() => void fix(it)}>
                  <Icon name="wrench" size={13} /> {busy === it.id ? 'Fixing…' : it.fix}
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
      {health.accounts.length > 0 && (
        <div className="smb-people" aria-label="Who can sign in over SMB">
          {health.accounts.map((a) => (
            <span key={a.name} className={`chip ${a.ready ? 'good' : 'warn'}`} title={a.ready ? 'Can sign in over SMB' : 'Hasn’t turned SMB on yet'}>
              <Icon name={a.ready ? 'check' : 'clock'} size={12} /> {a.name}
              {!a.ready && <span className="muted"> · not yet</span>}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

/** "Turn on SMB for your account": your sign-in password, once, handed to Samba. */
export function MySmbCard({ onReady }: { onReady?: () => void }) {
  const [me, setMe] = useState<MySMB | null>(null);
  const [pw, setPw] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => {
    void netApi.mySmb().then((r) => r.ok && setMe(r.data));
  }, []);
  if (!me || !me.smb || me.root) return null;
  if (me.ready) {
    return (
      <div className="smb-me ready">
        <Icon name="check" size={15} /> Your account is SMB-ready. Sign in from other devices with your usual user name and password.
      </div>
    );
  }
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError('');
    const r = await netApi.enableMySmb(pw);
    setBusy(false);
    if (!r.ok) return setError(r.error ?? 'That didn’t go through');
    setPw('');
    setMe(r.data);
    toast('success', tr('files.toast.smbMine'), tr('files.toast.smbMineBody'));
    onReady?.();
  };
  return (
    <form className="smb-me" onSubmit={(e) => void submit(e)}>
      <div>
        <strong>Your account isn’t SMB-ready yet</strong>
        <p className="muted small">Other computers can’t check your password on their own. Type it once and we’ll hand it to SMB — it’s never stored in plain text.</p>
      </div>
      <div className="smb-me-row">
        <input type="password" autoComplete="current-password" placeholder="Your password" aria-label="Your password" value={pw} onChange={(e) => setPw(e.target.value)} />
        <button type="submit" disabled={busy || !pw}>
          <Icon name="key" size={13} /> {busy ? 'Turning on…' : 'Turn on SMB for my account'}
        </button>
      </div>
      {error && <p className="error small">{error}</p>}
    </form>
  );
}

/** How to open a shared folder from each kind of device. */
export function HowToConnect({ health, example, webdav }: { health: SMBHealth | null; example: string; webdav: boolean }) {
  const host = smbHost(health);
  const smb = !!health?.smb;
  const share = example || 'Folder';
  const rows: { icon: 'windows' | 'desktop' | 'linux' | 'phone'; who: string; how: string; addr: string }[] = [];
  if (smb) {
    rows.push({ icon: 'windows', who: 'Windows', how: 'File Explorer → address bar, or Map network drive', addr: `\\\\${host}\\${share}` });
    rows.push({ icon: 'desktop', who: 'Mac', how: 'Finder → Go → Connect to Server (⌘K)', addr: `smb://${host}/${share}` });
    rows.push({ icon: 'linux', who: 'Linux', how: 'Files → Other Locations → Connect to Server', addr: `smb://${host}/${share}` });
  }
  if (webdav) rows.push({ icon: 'phone', who: 'Phones & tablets', how: 'Any WebDAV app, or your file manager’s “Connect to server”', addr: `${window.location.origin}/dav/${encodeURIComponent(share)}/` });
  if (rows.length === 0) return null;
  return (
    <div className="smb-howto">
      {rows.map((r) => (
        <div key={r.who} className="smb-howto-row">
          <Icon name={r.icon} size={18} />
          <span className="smb-howto-text">
            <strong>{r.who}</strong>
            <span className="muted small">{r.how}</span>
          </span>
          <CopyAddr text={r.addr} />
        </div>
      ))}
      <p className="muted small">Sign in with your NoCapOS user name and password. Guest folders open without one.</p>
    </div>
  );
}
