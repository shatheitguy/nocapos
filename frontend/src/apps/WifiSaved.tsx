import QRCode from 'qrcode';
import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { hostApi, type CAMode, type EAPConfig, type EAPInfo, type EAPMethod, type EAPPhase2, type SavedWiFi, type WiFiShare } from '../api/hostctl';
import { Icon } from '../components/Icon';
import { useT, type T } from '../i18n';
import { wifiQrPayload } from '../lib/wifiQr';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';

// Settings → Network → Wi-Fi → Saved networks: every Wi-Fi profile on the
// server, with connect / forget / auto-connect / priority / password (or
// enterprise sign-in) edits, and Share (password + join QR code).

/** A readable name for NetworkManager's key management. */
export function securityLabel(t: T, n: Pick<SavedWiFi, 'security' | 'eap'>): string {
  switch (n.security) {
    case 'open':
      return t('wifi.secOpen');
    case 'wep':
      return 'WEP';
    case 'wpa-psk':
      return 'WPA/WPA2';
    case 'sae':
      return 'WPA3';
    case 'owe':
      return t('wifi.secOwe');
    case 'wpa-eap':
    case 'wpa-eap-suite-b-192':
    case 'ieee8021x':
      return n.eap?.method ? `${t('wifi.secEnterprise')} · ${eapLabel(n.eap.method)}` : t('wifi.secEnterprise');
    default:
      return n.security;
  }
}

const EAP_LABEL: Record<string, string> = { peap: 'PEAP', ttls: 'TTLS', tls: 'TLS', pwd: 'PWD', leap: 'LEAP', fast: 'FAST' };
const eapLabel = (m: string) => EAP_LABEL[m] ?? m.toUpperCase();

function lastSeen(t: T, unix: number): string {
  if (!unix) return t('wifi.neverConnected');
  const s = Math.max(0, Date.now() / 1000 - unix);
  if (s < 120) return t('wifi.lastJustNow');
  if (s < 3600) return t('wifi.lastMinutes', { n: Math.floor(s / 60) });
  if (s < 86400) return t('wifi.lastHours', { n: Math.floor(s / 3600) });
  if (s < 86400 * 30) return t('wifi.lastDays', { n: Math.floor(s / 86400) });
  return t('wifi.lastOn', { date: new Date(unix * 1000).toLocaleDateString() });
}

/** The saved-network list. `rev` reloads it after a join elsewhere on the page. */
export function SavedNetworks({ rev, onChanged }: { rev?: number; onChanged?: () => void }) {
  const t = useT();
  const [list, setList] = useState<SavedWiFi[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState<string | null>(null); // uuid with its editor open
  const [busy, setBusy] = useState<string | null>(null);
  const [share, setShare] = useState<SavedWiFi | null>(null);

  const load = async () => {
    const r = await hostApi.wifiSaved();
    if (r.ok) {
      setList(r.data);
      setError(null);
    } else setError(r.error ?? t('wifi.loadFail'));
  };
  useEffect(() => {
    void load();
  }, [rev]);

  // The page reloads (bumping rev) when told; otherwise reload here.
  const changed = async () => (onChanged ? onChanged() : load());

  const connect = async (n: SavedWiFi) => {
    setBusy(n.uuid);
    const r = await hostApi.wifiSavedConnect(n.uuid);
    setBusy(null);
    if (r.ok) toast('success', t('wifi.connected', { ssid: n.ssid }));
    else toast('error', t('wifi.connectFail', { ssid: n.ssid }), r.error);
    await changed();
  };

  const forget = async (n: SavedWiFi) => {
    const ok = await confirmDialog({
      title: t('wifi.forgetTitle', { ssid: n.ssid }),
      message: n.active ? t('wifi.forgetMsgActive') : t('wifi.forgetMsg'),
      confirmLabel: t('wifi.forget'),
      danger: true,
    });
    if (!ok) return;
    setBusy(n.uuid);
    const r = await hostApi.wifiSavedForget(n.uuid);
    setBusy(null);
    if (r.ok) {
      toast('success', t('wifi.forgotten', { ssid: n.ssid }));
      setOpen(null);
    } else toast('error', t('wifi.forgetFail'), r.error);
    await changed();
  };

  if (error) return <p className="net-note">{error}</p>;
  if (!list) return <p className="net-note"><span className="spinner sm" /> {t('wifi.loading')}</p>;
  if (list.length === 0) return <p className="net-note">{t('wifi.noneSaved')}</p>;

  return (
    <div className="wifi-saved">
      {list.map((n) => (
        <div key={n.uuid} className={`wifi-saved-row ${n.active ? 'current' : ''}`}>
          <div className="wifi-saved-head">
            <span className="wifi-saved-icon"><Icon name={n.enterprise ? 'shield' : n.security === 'open' ? 'wifi' : 'lock'} size={15} /></span>
            <span className="wifi-saved-text">
              <span className="wifi-saved-name">
                {n.ssid}
                {n.active && <span className="chip good tiny">{t('wifi.connectedNow')}</span>}
              </span>
              <span className="wifi-saved-meta">
                {securityLabel(t, n)}
                {' · '}
                {n.autoconnect ? t('wifi.autoOn') : t('wifi.autoOff')}
                {' · '}
                {t('wifi.priorityShort', { n: n.priority })}
                {' · '}
                {n.active ? t('wifi.inUse') : lastSeen(t, n.last_connected)}
              </span>
            </span>
            {busy === n.uuid && <span className="spinner sm" />}
          </div>
          <div className="wifi-saved-actions">
            {!n.active && (
              <button type="button" className="ghost small" disabled={!!busy} onClick={() => void connect(n)}>
                {t('wifi.connect')}
              </button>
            )}
            <button type="button" className="ghost small" disabled={!!busy} aria-expanded={open === n.uuid} onClick={() => setOpen(open === n.uuid ? null : n.uuid)}>
              <Icon name="edit" size={12} /> {t('wifi.edit')}
            </button>
            <button
              type="button"
              className="ghost small"
              disabled={!!busy}
              title={n.shareable ? undefined : t('wifi.shareNoEnterprise')}
              onClick={() => (n.shareable ? setShare(n) : toast('info', t('wifi.shareNoEnterpriseTitle'), t('wifi.shareNoEnterprise')))}
            >
              <Icon name="share" size={12} /> {t('wifi.share')}
            </button>
            <button type="button" className="ghost small danger" disabled={!!busy} onClick={() => void forget(n)}>
              <Icon name="trash" size={12} /> {t('wifi.forget')}
            </button>
          </div>
          {open === n.uuid && <SavedEditor n={n} onDone={() => void changed()} onClose={() => setOpen(null)} />}
        </div>
      ))}
      {share && <ShareDialog n={share} onClose={() => setShare(null)} />}
    </div>
  );
}

/** Auto-connect, priority, and the password or enterprise sign-in. */
function SavedEditor({ n, onDone, onClose }: { n: SavedWiFi; onDone: () => void; onClose: () => void }) {
  const t = useT();
  const [priority, setPriority] = useState(String(n.priority));
  const [pw, setPw] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const setAuto = async (on: boolean) => {
    setBusy(true);
    const r = await hostApi.wifiSavedSet(n.uuid, { autoconnect: on });
    setBusy(false);
    if (!r.ok) toast('error', t('wifi.saveFail'), r.error);
    onDone();
  };
  const savePriority = async () => {
    const p = Number(priority);
    if (!Number.isInteger(p) || p < -999 || p > 999) {
      setErr(t('wifi.priorityRange'));
      return;
    }
    setBusy(true);
    const r = await hostApi.wifiSavedSet(n.uuid, { priority: p });
    setBusy(false);
    if (r.ok) {
      setErr(null);
      toast('success', t('wifi.saved'));
      onDone();
    } else setErr(r.error ?? t('wifi.saveFail'));
  };
  const savePassword = async () => {
    setBusy(true);
    const r = await hostApi.wifiSavedPassword(n.uuid, pw);
    setBusy(false);
    if (r.ok) {
      setPw('');
      setErr(null);
      toast('success', t('wifi.passwordSaved', { ssid: n.ssid }));
      onDone();
    } else setErr(r.error ?? t('wifi.saveFail'));
  };
  const saveEAP = async (cfg: EAPConfig) => {
    setBusy(true);
    const r = await hostApi.wifiSavedEAP(n.uuid, cfg);
    setBusy(false);
    if (r.ok) {
      setErr(null);
      toast('success', t('wifi.signinSaved', { ssid: n.ssid }));
      onDone();
      onClose();
    } else setErr(r.error ?? t('wifi.saveFail'));
  };
  const personal = n.security === 'wpa-psk' || n.security === 'sae';

  return (
    <div className="wifi-saved-editor">
      <label className="wifi-saved-line toggle">
        <span>
          <b>{t('wifi.autoConnect')}</b>
          <small>{t('wifi.autoConnectHint')}</small>
        </span>
        <input type="checkbox" role="switch" aria-checked={n.autoconnect} checked={n.autoconnect} disabled={busy} onChange={(e) => void setAuto(e.target.checked)} />
      </label>
      <form
        className="wifi-saved-line"
        onSubmit={(e) => {
          e.preventDefault();
          void savePriority();
        }}
      >
        <span>
          <b>{t('wifi.priority')}</b>
          <small>{t('wifi.priorityHint')}</small>
        </span>
        <span className="wifi-saved-inline">
          <input type="number" inputMode="numeric" min={-999} max={999} step={1} value={priority} aria-label={t('wifi.priority')} onChange={(e) => setPriority(e.target.value)} />
          <button type="submit" className="small" disabled={busy || priority === String(n.priority)}>
            {t('wifi.save')}
          </button>
        </span>
      </form>
      {personal && (
        <form
          className="wifi-saved-line"
          onSubmit={(e) => {
            e.preventDefault();
            if (pw) void savePassword();
          }}
        >
          <span>
            <b>{t('wifi.password')}</b>
            <small>{n.editable ? t('wifi.passwordHint') : t('wifi.notEditable')}</small>
          </span>
          <span className="wifi-saved-inline">
            <input type="password" autoComplete="new-password" placeholder={t('wifi.newPassword')} value={pw} disabled={!n.editable} onChange={(e) => setPw(e.target.value)} />
            <button type="submit" className="small" disabled={busy || !pw || !n.editable}>
              {t('wifi.save')}
            </button>
          </span>
        </form>
      )}
      {n.enterprise &&
        (n.editable ? (
          <EnterpriseForm ssid={n.ssid} initial={n.eap} busy={busy} submitLabel={t('wifi.saveSignin')} onSubmit={(c) => void saveEAP(c)} />
        ) : (
          <p className="net-note">{t('wifi.notEditable')}</p>
        ))}
      {err && <p className="wifi-error">{err}</p>}
    </div>
  );
}

// ---------- Share ----------

function ShareDialog({ n, onClose }: { n: SavedWiFi; onClose: () => void }) {
  const t = useT();
  const [account, setAccount] = useState('');
  const [share, setShare] = useState<WiFiShare | null>(null);
  const [qr, setQr] = useState('');
  const [show, setShow] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const closeRef = useRef(onClose);
  closeRef.current = onClose;

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && closeRef.current();
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  const reveal = async () => {
    setBusy(true);
    const r = await hostApi.wifiSavedShare(n.uuid, account);
    setBusy(false);
    setAccount('');
    if (!r.ok) {
      setErr(r.error ?? t('wifi.shareFail'));
      return;
    }
    setErr(null);
    setShare(r.data);
    try {
      // Drawn right here in the browser: the password never leaves for a QR service.
      setQr(await QRCode.toDataURL(wifiQrPayload(r.data), { margin: 1, width: 240, errorCorrectionLevel: 'M' }));
    } catch {
      setQr('');
    }
  };

  const copy = () =>
    void navigator.clipboard
      ?.writeText(share?.password ?? '')
      .then(() => toast('success', t('wifi.copied')))
      .catch(() => toast('error', t('wifi.copyFail')));

  return createPortal(
    <div className="confirm-backdrop" onPointerDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="confirm-dialog glass wifi-share" role="dialog" aria-modal="true" aria-labelledby="wifi-share-title">
        <div className="confirm-icon">
          <Icon name="wifi" size={22} />
        </div>
        <h3 id="wifi-share-title">{t('wifi.shareTitle', { ssid: n.ssid })}</h3>
        {!share ? (
          <form
            className="wifi-share-auth"
            onSubmit={(e) => {
              e.preventDefault();
              if (account) void reveal();
            }}
          >
            <p>{t('wifi.shareAuth')}</p>
            <input type="password" autoFocus autoComplete="current-password" placeholder={t('wifi.accountPassword')} value={account} onChange={(e) => setAccount(e.target.value)} />
            {err && <p className="wifi-error">{err}</p>}
            <div className="confirm-actions">
              <button type="button" className="ghost" onClick={onClose}>
                {t('wifi.cancel')}
              </button>
              <button type="submit" disabled={!account || busy}>
                {busy ? t('wifi.checking') : t('wifi.showPassword')}
              </button>
            </div>
          </form>
        ) : (
          <>
            <p>{t('wifi.shareScan')}</p>
            {qr && <img className="wifi-share-qr" src={qr} alt={t('wifi.qrAlt', { ssid: share.ssid })} />}
            <div className="wifi-share-fields">
              <span className="muted small">{t('wifi.network')}</span>
              <code>{share.ssid}</code>
              {share.type === 'WPA' ? (
                <>
                  <span className="muted small">{t('wifi.password')}</span>
                  <span className="wifi-share-pw">
                    <code>{show ? share.password : '•'.repeat(Math.min(16, share.password.length))}</code>
                    <button type="button" className="icon-btn neutral" title={show ? t('wifi.hide') : t('wifi.reveal')} aria-label={show ? t('wifi.hide') : t('wifi.reveal')} onClick={() => setShow(!show)}>
                      <Icon name="eye" size={14} />
                    </button>
                  </span>
                </>
              ) : (
                <span className="muted small wifi-share-open">{t('wifi.openNetwork')}</span>
              )}
            </div>
            <div className="confirm-actions">
              {share.type === 'WPA' && (
                <button type="button" className="ghost" onClick={copy}>
                  <Icon name="copy" size={13} /> {t('wifi.copyPassword')}
                </button>
              )}
              <button type="button" onClick={onClose}>
                {t('wifi.done')}
              </button>
            </div>
          </>
        )}
      </div>
    </div>,
    document.body,
  );
}

// ---------- Enterprise (802.1X) sign-in ----------

const PHASE2: Record<string, EAPPhase2[]> = { peap: ['mschapv2', 'gtc', 'md5'], ttls: ['mschapv2', 'pap', 'gtc', 'md5'] };
const PHASE2_LABEL: Record<EAPPhase2, string> = { mschapv2: 'MSCHAPv2', pap: 'PAP', gtc: 'GTC', md5: 'MD5' };
const METHODS: EAPMethod[] = ['peap', 'ttls', 'tls'];

function readBase64(file: File): Promise<string> {
  return file.arrayBuffer().then((buf) => {
    const bytes = new Uint8Array(buf);
    let s = '';
    for (let i = 0; i < bytes.length; i += 0x8000) s += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
    return btoa(s);
  });
}

function CertPicker({ label, have, onPick, accept }: { label: string; have: boolean; onPick: (b64: string) => void; accept: string }) {
  const t = useT();
  const input = useRef<HTMLInputElement>(null);
  const [name, setName] = useState('');
  const [err, setErr] = useState('');
  return (
    <div className="wifi-eap-field">
      <span>{label}</span>
      <span className="wifi-eap-file">
        <input
          ref={input}
          type="file"
          hidden
          accept={accept}
          onChange={async (e) => {
            const f = e.target.files?.[0];
            e.target.value = '';
            if (!f) return;
            if (f.size > 64 * 1024) {
              setErr(t('wifi.certTooBig'));
              return;
            }
            setErr('');
            setName(f.name);
            onPick(await readBase64(f));
          }}
        />
        <button type="button" className="ghost small" aria-label={`${label}: ${t('wifi.chooseFile')}`} onClick={() => input.current?.click()}>
          <Icon name="upload" size={12} /> {t('wifi.chooseFile')}
        </button>
        <small className="muted">{name || (have ? t('wifi.certKept') : t('wifi.certNone'))}</small>
      </span>
      {err && <small className="wifi-error">{err}</small>}
    </div>
  );
}

/** Join or edit a WPA/WPA2/WPA3-Enterprise network. */
export function EnterpriseForm({
  ssid,
  initial,
  busy,
  error,
  submitLabel,
  onSubmit,
  onCancel,
}: {
  ssid: string;
  initial?: EAPInfo;
  busy?: boolean;
  error?: string;
  submitLabel: string;
  onSubmit: (c: EAPConfig) => void;
  onCancel?: () => void;
}) {
  const t = useT();
  const editing = !!initial;
  const [method, setMethod] = useState<EAPMethod>((METHODS as string[]).includes(initial?.method ?? '') ? (initial!.method as EAPMethod) : 'peap');
  const [phase2, setPhase2] = useState<EAPPhase2>((initial?.phase2 as EAPPhase2) || 'mschapv2');
  const [identity, setIdentity] = useState(initial?.identity ?? '');
  const [anon, setAnon] = useState(initial?.anonymous_identity ?? '');
  const [password, setPassword] = useState('');
  const [ca, setCa] = useState<CAMode>(initial?.ca ?? 'system');
  const [domain, setDomain] = useState(initial?.domain_suffix_match ?? '');
  const [caCert, setCaCert] = useState<string>();
  const [clientCert, setClientCert] = useState<string>();
  const [privKey, setPrivKey] = useState<string>();
  const [keyPw, setKeyPw] = useState('');

  const phase2s = PHASE2[method];
  const usesPw = method !== 'tls';
  const keepPw = editing && initial?.method === method;
  const sameMethod = editing && initial?.method === method;
  const p2 = phase2s && !phase2s.includes(phase2) ? 'mschapv2' : phase2;

  const ready =
    identity.trim() !== '' &&
    (!usesPw || password !== '' || keepPw) &&
    (ca !== 'file' || !!caCert || (editing && initial?.ca === 'file')) &&
    (method !== 'tls' || ((!!clientCert || (sameMethod && !!initial?.client_cert)) && (!!privKey || !!clientCert || (sameMethod && !!initial?.private_key))));

  return (
    <form
      className="wifi-other wifi-eap"
      onSubmit={(e) => {
        e.preventDefault();
        if (!ready) return;
        onSubmit({
          method,
          phase2: phase2s ? p2 : undefined,
          identity: identity.trim(),
          anonymous_identity: phase2s ? anon.trim() || undefined : undefined,
          password: usesPw ? password || undefined : undefined,
          ca,
          ca_cert: ca === 'file' ? caCert : undefined,
          domain_suffix_match: ca !== 'none' ? domain.trim() || undefined : undefined,
          client_cert: method === 'tls' ? clientCert : undefined,
          private_key: method === 'tls' ? privKey : undefined,
          private_key_password: method === 'tls' ? keyPw || undefined : undefined,
        });
      }}
    >
      <p className="wifi-eap-title">
        <Icon name="shield" size={13} /> {editing ? t('wifi.eapEditTitle', { ssid }) : t('wifi.eapJoinTitle', { ssid })}
      </p>
      <label className="wifi-eap-field">
        <span>{t('wifi.eapMethod')}</span>
        <select className="select" value={method} onChange={(e) => setMethod(e.target.value as EAPMethod)}>
          {METHODS.map((m) => (
            <option key={m} value={m}>
              {eapLabel(m)}
            </option>
          ))}
        </select>
      </label>
      {phase2s && (
        <label className="wifi-eap-field">
          <span>{t('wifi.eapPhase2')}</span>
          <select className="select" value={p2} onChange={(e) => setPhase2(e.target.value as EAPPhase2)}>
            {phase2s.map((m) => (
              <option key={m} value={m}>
                {PHASE2_LABEL[m]}
              </option>
            ))}
          </select>
        </label>
      )}
      <label className="wifi-eap-field">
        <span>{t('wifi.eapIdentity')}</span>
        <input type="text" autoComplete="username" spellCheck={false} maxLength={255} value={identity} onChange={(e) => setIdentity(e.target.value)} />
      </label>
      {phase2s && (
        <label className="wifi-eap-field">
          <span>{t('wifi.eapAnon')}</span>
          <input type="text" spellCheck={false} maxLength={255} placeholder={t('wifi.optional')} value={anon} onChange={(e) => setAnon(e.target.value)} />
        </label>
      )}
      {usesPw && (
        <label className="wifi-eap-field">
          <span>{t('wifi.password')}</span>
          <input type="password" autoComplete="new-password" placeholder={keepPw ? t('wifi.keepSaved') : ''} value={password} onChange={(e) => setPassword(e.target.value)} />
        </label>
      )}
      <label className="wifi-eap-field">
        <span>{t('wifi.eapCA')}</span>
        <select className="select" value={ca} onChange={(e) => setCa(e.target.value as CAMode)}>
          <option value="system">{t('wifi.caSystem')}</option>
          <option value="file">{t('wifi.caFile')}</option>
          <option value="none">{t('wifi.caNone')}</option>
        </select>
      </label>
      {ca === 'file' && (
        <CertPicker label={t('wifi.caCert')} have={!!editing && initial?.ca === 'file'} accept=".pem,.crt,.cer,.der" onPick={(b) => setCaCert(b)} />
      )}
      {ca === 'none' ? (
        <p className="wifi-warn">
          <Icon name="alert" size={13} /> {t('wifi.caNoneWarn')}
        </p>
      ) : (
        <label className="wifi-eap-field">
          <span>{t('wifi.eapDomain')}</span>
          <input type="text" spellCheck={false} maxLength={255} placeholder="radius.example.com" value={domain} onChange={(e) => setDomain(e.target.value)} />
        </label>
      )}
      {method === 'tls' && (
        <>
          <CertPicker label={t('wifi.clientCert')} have={sameMethod && !!initial?.client_cert} accept=".pem,.crt,.cer,.der,.p12,.pfx" onPick={(b) => setClientCert(b)} />
          <CertPicker label={t('wifi.privateKey')} have={sameMethod && !!initial?.private_key} accept=".pem,.key,.der,.p12,.pfx" onPick={(b) => setPrivKey(b)} />
          <small className="muted">{t('wifi.p12Hint')}</small>
          <label className="wifi-eap-field">
            <span>{t('wifi.keyPassword')}</span>
            <input type="password" autoComplete="new-password" placeholder={t('wifi.optional')} value={keyPw} onChange={(e) => setKeyPw(e.target.value)} />
          </label>
        </>
      )}
      {error && <p className="wifi-error">{error}</p>}
      <div className="btn-group">
        <button type="submit" className="small" disabled={!ready || busy}>
          {busy ? t('wifi.working') : submitLabel}
        </button>
        {onCancel && (
          <button type="button" className="ghost small" onClick={onCancel}>
            {t('wifi.cancel')}
          </button>
        )}
      </div>
    </form>
  );
}
