import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import QRCode from 'qrcode';
import { getUser } from '../api/client';
import { devicesApi, type LinkedDevice, type PairCode, type PairInfo } from '../api/devices';
import { Icon, type IconName } from '../components/Icon';
import { fmtAgo } from '../lib/format';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import { Section as Group } from './Personalize';

// Settings → Devices: link a phone running the NoCap OS app with a 6-digit
// code (or its QR), see linked devices and unlink them.

const APP_URL = 'https://github.com/shatheitguy/alfa-launcher/releases/latest';

const PLATFORM: Record<LinkedDevice['platform'], { label: string; icon: IconName }> = {
  android: { label: 'Android', icon: 'android' },
  ios: { label: 'Phone', icon: 'phone' },
  other: { label: 'Device', icon: 'phone' },
};

export function DevicesSettings() {
  const isAdmin = getUser()?.role === 'admin';
  const [info, setInfo] = useState<PairInfo | null>(null);
  const [list, setList] = useState<LinkedDevice[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [linking, setLinking] = useState(false);

  const load = () =>
    void devicesApi.list().then((r) => {
      if (r.ok) {
        setList(r.data);
        setError(null);
      } else setError(r.error ?? 'Couldn’t load your devices');
    });
  useEffect(() => {
    void devicesApi.info().then((r) => r.ok && setInfo(r.data));
    load();
    // Keep "last seen" fresh while the page is open.
    const t = window.setInterval(load, 30_000);
    return () => window.clearInterval(t);
  }, []);

  const copyId = () => info && void navigator.clipboard?.writeText(info.nocap_id).then(() => toast('success', 'NoCap ID copied'));

  const unlink = async (d: LinkedDevice) => {
    const ok = await confirmDialog({
      title: `Unlink ${d.name}?`,
      message: 'It loses access to this NoCapOS right away. You can link it again any time with a fresh code.',
      confirmLabel: 'Unlink',
      danger: true,
    });
    if (!ok) return;
    const r = await devicesApi.unlink(d.id);
    if (!r.ok && r.status !== 404) return toast('error', 'Couldn’t unlink that device', r.error);
    toast('success', `${d.name} is unlinked`);
    load();
  };

  return (
    <div className="stack settings-page">
      <section className="ucard dev-hero">
        <span className="dev-hero-icon">
          <Icon name="phone" size={26} />
        </span>
        <div className="dev-hero-text">
          <span className="dev-hero-label">Your NoCap ID</span>
          <span className="dev-hero-id">
            <code>{info?.nocap_id ?? '…'}</code>
            {info && (
              <button type="button" className="icon-btn neutral" title="Copy NoCap ID" aria-label="Copy NoCap ID" onClick={copyId}>
                <Icon name="copy" size={14} />
              </button>
            )}
          </span>
          <span className="dev-hero-hint">This server’s ID in the NoCap ecosystem. It never changes.</span>
        </div>
        <button type="button" className="pill small" onClick={() => setLinking(true)}>
          <Icon name="plus" size={13} /> Link a device
        </button>
      </section>

      <Group title={isAdmin ? 'Linked devices (everyone’s)' : 'Your linked devices'}
        hint="Phones and tablets running the NoCap OS app. Unlink one and it’s cut off instantly.">
        {error ? (
          <div className="settings-row full">
            <p className="error">{error}</p>
          </div>
        ) : !list ? (
          <div className="settings-row full">
            <p className="muted small">Loading…</p>
          </div>
        ) : list.length === 0 ? (
          <div className="settings-row full">
            <p className="muted small dev-empty">No devices yet — hit <b>Link a device</b> and scan the code with your phone.</p>
          </div>
        ) : (
          list.map((d) => <DeviceRow key={d.id} d={d} showOwner={isAdmin} onUnlink={() => void unlink(d)} />)
        )}
      </Group>

      {linking && (
        <LinkDialog
          known={new Set((list ?? []).map((d) => d.id))}
          onClose={() => {
            setLinking(false);
            load();
          }}
        />
      )}
    </div>
  );
}

function DeviceRow({ d, showOwner, onUnlink }: { d: LinkedDevice; showOwner: boolean; onUnlink: () => void }) {
  const p = PLATFORM[d.platform] ?? PLATFORM.other;
  const bits = [
    p.label,
    d.last_seen_at ? `Seen ${fmtAgo(d.last_seen_at)}` : 'Never seen',
    d.battery != null ? `${d.battery}% battery` : null,
    d.app_version ? `App ${d.app_version}` : null,
    showOwner && d.username ? `${d.username}’s` : null,
  ].filter(Boolean);
  return (
    <div className="settings-row dev-row">
      <span className="urow-icon dev-row-icon">
        <Icon name={p.icon} size={18} />
      </span>
      <span className="settings-row-text">
        <span className="settings-row-label">{d.name}</span>
        <span className="settings-row-hint">{bits.join(' · ')}</span>
      </span>
      <div className="settings-row-control">
        <button type="button" className="ghost small danger" onClick={onUnlink}>
          Unlink
        </button>
      </div>
    </div>
  );
}

type Phase = { kind: 'loading' } | { kind: 'error'; message: string } | { kind: 'code'; pc: PairCode; qr: string } | { kind: 'linked'; name: string };

function LinkDialog({ known, onClose }: { known: Set<string>; onClose: () => void }) {
  const [phase, setPhase] = useState<Phase>({ kind: 'loading' });
  const [now, setNow] = useState(Date.now());
  const knownRef = useRef(known);
  const closeRef = useRef(onClose);
  closeRef.current = onClose;

  const fresh = async () => {
    setPhase({ kind: 'loading' });
    const r = await devicesApi.pairCode();
    if (!r.ok) return setPhase({ kind: 'error', message: r.error ?? 'Couldn’t make a code. Try again?' });
    let qr = '';
    try {
      qr = await QRCode.toDataURL(r.data.pair_url, { margin: 1, width: 220 });
    } catch {
      /* the code alone still works */
    }
    setNow(Date.now());
    setPhase({ kind: 'code', pc: r.data, qr });
  };

  useEffect(() => {
    void fresh();
  }, []);

  // Countdown.
  useEffect(() => {
    if (phase.kind !== 'code') return;
    const t = window.setInterval(() => setNow(Date.now()), 500);
    return () => window.clearInterval(t);
  }, [phase.kind]);

  // Watch for the new device while the dialog is open.
  useEffect(() => {
    if (phase.kind === 'linked') return;
    const t = window.setInterval(() => {
      void devicesApi.list().then((r) => {
        if (!r.ok) return;
        const added = r.data.find((d) => !knownRef.current.has(d.id));
        if (added) setPhase({ kind: 'linked', name: added.name });
      });
    }, 3000);
    return () => window.clearInterval(t);
  }, [phase.kind]);

  // Celebrate, then close.
  useEffect(() => {
    if (phase.kind !== 'linked') return;
    toast('success', `Bet — ${phase.name} is linked`);
    const t = window.setTimeout(() => closeRef.current(), 1800);
    return () => window.clearTimeout(t);
  }, [phase]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && closeRef.current();
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  const server = window.location.origin;
  const copyServer = () => void navigator.clipboard?.writeText(server).then(() => toast('success', 'Server address copied'));

  let body;
  if (phase.kind === 'loading') {
    body = (
      <div className="dev-link-wait">
        <span className="spinner" />
      </div>
    );
  } else if (phase.kind === 'error') {
    body = (
      <>
        <p className="error">{phase.message}</p>
        <button type="button" className="small align-start" onClick={() => void fresh()}>
          <Icon name="restart" size={13} /> Try again
        </button>
      </>
    );
  } else if (phase.kind === 'linked') {
    body = (
      <div className="dev-link-done">
        <span className="dev-link-check">
          <Icon name="check" size={30} />
        </span>
        <b>Bet — {phase.name} is linked</b>
        <span className="muted small">It’s all set. Your apps are on your phone now.</span>
      </div>
    );
  } else {
    const ttl = 5 * 60;
    const left = Math.max(0, Math.ceil((new Date(phase.pc.expires_at).getTime() - now) / 1000));
    const expired = left === 0;
    const code = phase.pc.code;
    body = (
      <>
        <p>Open the NoCap OS app on your phone, then scan this or type the code.</p>
        <div className={`dev-link-code ${expired ? 'expired' : ''}`}>
          {phase.qr && <img src={phase.qr} alt="QR code for linking your phone" className="dev-link-qr" />}
          <div className="dev-link-digits" aria-label={`Code ${code.split('').join(' ')}`}>
            <span>{code.slice(0, 3)}</span>
            <span>{code.slice(3)}</span>
          </div>
          {expired ? (
            <div className="dev-link-expired">
              <b>Code expired — grab a fresh one</b>
              <button type="button" className="small" onClick={() => void fresh()}>
                <Icon name="restart" size={13} /> New code
              </button>
            </div>
          ) : (
            <div className="confirm-countdown dev-link-timer">
              <span style={{ width: `${Math.min(100, (left / ttl) * 100)}%` }} />
              <b>
                {Math.floor(left / 60)}:{String(left % 60).padStart(2, '0')}
              </b>
            </div>
          )}
        </div>
        <div className="dev-link-server">
          <span className="muted small">Server address</span>
          <code>{server}</code>
          <button type="button" className="icon-btn neutral" title="Copy server address" aria-label="Copy server address" onClick={copyServer}>
            <Icon name="copy" size={14} />
          </button>
        </div>
        <a className="set-link" href={APP_URL} target="_blank" rel="noreferrer">
          Get the NoCap OS app <Icon name="external" size={12} />
        </a>
      </>
    );
  }

  return createPortal(
    <div className="confirm-backdrop" onPointerDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="confirm-dialog glass dev-link" role="dialog" aria-modal="true" aria-labelledby="dev-link-title">
        <div className="confirm-icon">
          <Icon name="phone" size={22} />
        </div>
        <h3 id="dev-link-title">Link a device</h3>
        {body}
        <div className="confirm-actions">
          <button type="button" className="ghost" onClick={onClose}>
            {phase.kind === 'linked' ? 'Done' : 'Close'}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
