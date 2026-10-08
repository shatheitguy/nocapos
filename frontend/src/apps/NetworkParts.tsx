import { useEffect, useState } from 'react';
import { hostApi, type IPv4Config, type NetInterface, type WiFiNetwork } from '../api/hostctl';
import { Icon } from '../components/Icon';
import { applyIPv4, joinWiFi } from '../lib/hostActions';

// Network UI shared by the Control Center and Settings → Network.

/** Four-bar signal strength. */
export function SignalBars({ level }: { level: number }) {
  const bars = level >= 75 ? 4 : level >= 50 ? 3 : level >= 25 ? 2 : 1;
  return (
    <svg className="signal-bars" viewBox="0 0 16 14" width="16" height="14" aria-label={`Signal ${level}%`}>
      {[0, 1, 2, 3].map((i) => (
        <rect key={i} x={i * 4} y={10 - i * 3} width="3" height={4 + i * 3} rx="1" className={i < bars ? 'on' : ''} />
      ))}
    </svg>
  );
}

/** Available Wi-Fi networks with join / disconnect. */
export function WifiList({ device, onChanged }: { device?: string; onChanged?: () => void }) {
  const [list, setList] = useState<WiFiNetwork[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [scanning, setScanning] = useState(false);
  const [askFor, setAskFor] = useState<string | null>(null);
  const [password, setPassword] = useState('');
  const [joining, setJoining] = useState<string | null>(null);

  const scan = async (rescan: boolean) => {
    setScanning(true);
    const r = await hostApi.wifiList(rescan);
    setScanning(false);
    if (r.ok) {
      setList(r.data);
      setError(null);
    } else setError(r.error ?? 'Could not scan for networks');
  };
  useEffect(() => {
    void scan(true);
  }, []);

  const join = async (n: WiFiNetwork, pw?: string) => {
    setJoining(n.ssid);
    const ok = await joinWiFi(n.ssid, pw);
    setJoining(null);
    if (ok) {
      setAskFor(null);
      setPassword('');
      await scan(false);
      onChanged?.();
    }
  };

  const pick = (n: WiFiNetwork) => {
    if (n.in_use || joining) return;
    if (n.secure && !n.saved) {
      setAskFor(askFor === n.ssid ? null : n.ssid);
      setPassword('');
    } else void join(n);
  };

  if (error) return <p className="net-note">{error}</p>;
  if (!list) return <p className="net-note"><span className="spinner sm" /> Scanning for networks…</p>;

  return (
    <div className="wifi-list">
      {list.length === 0 && <p className="net-note">No networks in range.</p>}
      {list.map((n) => (
        <div key={n.ssid} className={`wifi-row ${n.in_use ? 'current' : ''}`}>
          <button type="button" className="wifi-main" onClick={() => pick(n)} disabled={!!joining}>
            <SignalBars level={n.signal} />
            <span className="wifi-name">{n.ssid}</span>
            {joining === n.ssid && <span className="spinner sm" />}
            {n.secure && <Icon name="lock" size={12} />}
            {n.in_use && <Icon name="check" size={14} />}
          </button>
          {n.in_use && device && (
            <button
              type="button"
              className="ghost small"
              onClick={async () => {
                const r = await hostApi.wifiDisconnect(device);
                if (r.ok) {
                  await scan(false);
                  onChanged?.();
                }
              }}
            >
              Disconnect
            </button>
          )}
          {askFor === n.ssid && (
            <form
              className="wifi-join"
              onSubmit={(e) => {
                e.preventDefault();
                if (password) void join(n, password);
              }}
            >
              <input type="password" autoFocus autoComplete="off" placeholder={`Password for ${n.ssid}`} value={password} onChange={(e) => setPassword(e.target.value)} />
              <button type="submit" className="small" disabled={!password || !!joining}>
                Join
              </button>
            </form>
          )}
        </div>
      ))}
      <button type="button" className="ghost small wifi-rescan" onClick={() => void scan(true)} disabled={scanning}>
        <Icon name="restart" size={12} /> {scanning ? 'Scanning…' : 'Scan again'}
      </button>
    </div>
  );
}

const METHOD_LABEL: Record<string, string> = { auto: 'DHCP', manual: 'Static', disabled: 'Disabled', unknown: '—' };

/** One-line IP summary for an interface. */
export function IPv4Summary({ iface }: { iface: NetInterface }) {
  const ip = iface.ipv4;
  const addrs = ip?.addresses.length ? ip.addresses : iface.addresses;
  return (
    <div className="ipv4-summary">
      <div>
        <span>IP address</span>
        <b>{addrs.length ? addrs.join(', ') : 'none'}</b>
      </div>
      <div>
        <span>Configure</span>
        <b>{METHOD_LABEL[ip?.method ?? 'unknown']}</b>
      </div>
      {ip?.gateway && (
        <div>
          <span>Router</span>
          <b>{ip.gateway}</b>
        </div>
      )}
      {!!ip?.dns.length && (
        <div>
          <span>DNS</span>
          <b>{ip.dns.join(', ')}</b>
        </div>
      )}
    </div>
  );
}

/** DHCP / static editor for one interface (Settings → Network). */
export function IPv4Editor({ iface, onApplied }: { iface: NetInterface; onApplied: () => void }) {
  const ip = iface.ipv4;
  const [method, setMethod] = useState<'auto' | 'manual'>(ip?.method === 'manual' ? 'manual' : 'auto');
  const [address, setAddress] = useState(ip?.addresses[0] ?? '');
  const [gateway, setGateway] = useState(ip?.gateway ?? '');
  const [dns, setDns] = useState((ip?.dns ?? []).join(', '));
  const [busy, setBusy] = useState(false);
  const editable = !!ip?.editable && !!iface.connection;

  const apply = async () => {
    const cfg: IPv4Config = {
      method,
      address: method === 'manual' ? address.trim() : undefined,
      gateway: method === 'manual' ? gateway.trim() : undefined,
      dns: dns.split(/[\s,]+/).filter(Boolean),
    };
    setBusy(true);
    const ok = await applyIPv4(iface.name, iface.connection!, cfg);
    setBusy(false);
    if (ok) onApplied();
  };

  return (
    <div className={`ipv4-editor ${editable ? '' : 'readonly'}`}>
      <div className="settings-row">
        <span className="settings-row-text">
          <span className="settings-row-label">Configure IPv4</span>
          <span className="settings-row-hint">
            {editable ? 'DHCP gets an address from your router; Static uses the one you set' : 'Read-only: changing it needs NetworkManager on a Linux host'}
          </span>
        </span>
        <div className="settings-row-control">
          <div className="segmented">
            <button type="button" className={method === 'auto' ? 'on' : ''} disabled={!editable} onClick={() => setMethod('auto')}>
              DHCP
            </button>
            <button type="button" className={method === 'manual' ? 'on' : ''} disabled={!editable} onClick={() => setMethod('manual')}>
              Static
            </button>
          </div>
        </div>
      </div>
      {method === 'manual' && (
        <>
          <div className="settings-row">
            <span className="settings-row-text">
              <span className="settings-row-label">IP address</span>
              <span className="settings-row-hint">With prefix, e.g. 192.168.1.50/24</span>
            </span>
            <div className="settings-row-control">
              <input value={address} disabled={!editable} placeholder="192.168.1.50/24" spellCheck={false} onChange={(e) => setAddress(e.target.value)} />
            </div>
          </div>
          <div className="settings-row">
            <span className="settings-row-text">
              <span className="settings-row-label">Router (gateway)</span>
            </span>
            <div className="settings-row-control">
              <input value={gateway} disabled={!editable} placeholder="192.168.1.1" spellCheck={false} onChange={(e) => setGateway(e.target.value)} />
            </div>
          </div>
        </>
      )}
      <div className="settings-row">
        <span className="settings-row-text">
          <span className="settings-row-label">DNS servers</span>
          <span className="settings-row-hint">{method === 'auto' ? 'Leave empty to use the router’s DNS' : 'Separate with commas'}</span>
        </span>
        <div className="settings-row-control">
          <input value={dns} disabled={!editable} placeholder="1.1.1.1, 8.8.8.8" spellCheck={false} onChange={(e) => setDns(e.target.value)} />
        </div>
      </div>
      {editable && (
        <div className="ipv4-apply">
          <button type="button" disabled={busy} onClick={() => void apply()}>
            {busy ? 'Applying…' : 'Apply'}
          </button>
        </div>
      )}
    </div>
  );
}
