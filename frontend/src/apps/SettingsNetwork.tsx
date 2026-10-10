import { useCallback, useEffect, useState } from 'react';
import { hostApi, type NetInterface, type NetworkState } from '../api/hostctl';
import { Icon, type IconName } from '../components/Icon';
import { setNetwork } from '../lib/hostActions';
import { IPv4Editor, IPv4Summary, WifiList } from './NetworkParts';
import { Section, Toggle } from './Personalize';

// Settings → Network: an overview of connections, then Wi-Fi and one page
// per adapter with its IP settings.

function useNetwork() {
  const [net, setNet] = useState<NetworkState | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [rev, setRev] = useState(0); // remount editors after a change
  const load = useCallback(
    () =>
      void hostApi.network().then((r) => {
        if (r.ok) {
          setNet(r.data);
          setRev((v) => v + 1);
        } else setError(r.error ?? 'Could not read network status');
      }),
    [],
  );
  useEffect(load, [load]);
  return { net, setNet, error, rev, load };
}

const ipOf = (i: NetInterface) => (i.ipv4?.addresses[0] ?? i.addresses.find((a) => !a.includes(':')))?.replace(/\/\d+$/, '');
const connected = (i: NetInterface) => i.up && !!ipOf(i);
const ifaceIcon = (i: NetInterface): IconName => (i.kind === 'wifi' ? 'wifi' : i.kind === 'ethernet' ? 'ethernet' : 'network');
const ifaceKind = (i: NetInterface) => (i.kind === 'wifi' ? 'Wi-Fi' : i.kind === 'ethernet' ? 'Ethernet' : 'Network adapter');

function Switch({ label, checked, disabled, onChange }: { label: string; checked: boolean; disabled?: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="toggle set-switch">
      <input type="checkbox" role="switch" aria-label={label} aria-checked={checked} checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
    </label>
  );
}

function ViewOnly({ net }: { net: NetworkState }) {
  if (net.manager === 'networkmanager') return null;
  return (
    <p className="set-note">
      <Icon name="info" size={15} /> {(net.wifi.reason ?? 'Changing network settings needs NetworkManager on a Linux host').replace(/\.?\s*$/, '.')} Settings are shown read-only.
    </p>
  );
}

export function NetworkHome({ open }: { open: (sub: string) => void }) {
  const { net, setNet, error } = useNetwork();
  const [busy, setBusy] = useState(false);
  if (error) return <p className="error">{error}</p>;
  if (!net) return <p className="muted">Checking network…</p>;
  const flip = async (kind: 'wifi' | 'networking', on: boolean) => {
    setBusy(true);
    const next = await setNetwork(kind, on);
    if (next) setNet(next);
    setBusy(false);
  };
  const primary = net.interfaces.find(connected);
  const wireless = net.interfaces.find((i) => i.kind === 'wifi');
  const wired = net.interfaces.filter((i) => i.kind !== 'wifi');
  return (
    <div className="stack settings-page">
      <div className="ucard set-status">
        <span className={`set-status-icon ${primary ? 'on' : ''}`}>
          <Icon name={primary ? ifaceIcon(primary) : 'network'} size={22} />
        </span>
        <div className="set-status-text">
          <b>{primary ? `Connected${primary.connection ? ` to ${primary.connection}` : ''}` : 'Not connected'}</b>
          <span>{primary ? `${ifaceKind(primary)} · ${primary.name} · ${ipOf(primary)}` : 'No adapter has an address'}</span>
        </div>
      </div>
      <ViewOnly net={net} />

      <div className="ucard ulist">
        <div className="urow">
          <span className="urow-icon set-ico" style={{ background: '#0a84ff' }}><Icon name="wifi" size={16} /></span>
          <button type="button" className="urow-text set-row-link" disabled={!net.wifi.supported} onClick={() => open('wifi')}>
            <span className="urow-title">Wi-Fi</span>
            <span className="urow-desc">
              {!net.wifi.supported ? 'Not available on this host' : !net.wifi.enabled ? 'Off' : wireless?.connection || 'Not connected'}
            </span>
          </button>
          <span className="urow-control">
            <Switch label="Wi-Fi" checked={net.wifi.enabled} disabled={!net.wifi.supported || busy} onChange={(on) => void flip('wifi', on)} />
            {net.wifi.supported && <Icon name="chevronRight" size={16} />}
          </span>
        </div>
        {wired.map((i) => (
          <button key={i.name} type="button" className="urow" onClick={() => open(`iface:${i.name}`)}>
            <span className="urow-icon set-ico" style={{ background: connected(i) ? '#30d158' : '#8e8e93' }}><Icon name={ifaceIcon(i)} size={16} /></span>
            <span className="urow-text">
              <span className="urow-title">{i.kind === 'ethernet' ? `Ethernet · ${i.name}` : i.name}</span>
              <span className="urow-desc">{connected(i) ? `Connected · ${ipOf(i)}` : i.up ? 'No address' : 'Not connected'}</span>
            </span>
            <span className="urow-control">
              <span className={`set-dot ${connected(i) ? 'on' : ''}`} />
              <Icon name="chevronRight" size={16} />
            </span>
          </button>
        ))}
      </div>

      <div className="ucard ulist">
        <div className="urow">
          <span className="urow-icon set-ico" style={{ background: '#636366' }}><Icon name="globe" size={16} /></span>
          <span className="urow-text">
            <span className="urow-title">Networking</span>
            <span className="urow-desc">Every network connection on this machine</span>
          </span>
          <span className="urow-control">
            <Switch label="Networking" checked={net.networking.enabled} disabled={!net.networking.supported || busy} onChange={(on) => void flip('networking', on)} />
          </span>
        </div>
      </div>
    </div>
  );
}

export function WifiPage() {
  const { net, setNet, error, rev, load } = useNetwork();
  const [busy, setBusy] = useState(false);
  if (error) return <p className="error">{error}</p>;
  if (!net) return <p className="muted">Checking Wi-Fi…</p>;
  const wireless = net.interfaces.find((i) => i.kind === 'wifi');
  const flip = async (on: boolean) => {
    setBusy(true);
    const next = await setNetwork('wifi', on);
    if (next) setNet(next);
    setBusy(false);
  };
  return (
    <div className="stack settings-page">
      <ViewOnly net={net} />
      <Section title="Wi-Fi">
        <Toggle label="Wi-Fi" hint={net.wifi.supported ? (net.wifi.enabled ? wireless?.connection || 'Not connected' : 'Off') : 'Not available on this host'}
          checked={net.wifi.enabled} disabled={!net.wifi.supported || busy} onChange={(on) => void flip(on)} />
        {net.wifi.supported && net.wifi.enabled && (
          <div className="settings-row full">
            <WifiList device={wireless?.name} onChanged={load} />
          </div>
        )}
      </Section>
      {wireless && <AddressSection key={`${wireless.name}-${rev}`} iface={wireless} onApplied={load} />}
    </div>
  );
}

export function InterfacePage({ name }: { name: string }) {
  const { net, error, rev, load } = useNetwork();
  if (error) return <p className="error">{error}</p>;
  if (!net) return <p className="muted">Checking network…</p>;
  const iface = net.interfaces.find((i) => i.name === name);
  if (!iface) return <p className="muted">The adapter “{name}” isn't there any more.</p>;
  return (
    <div className="stack settings-page">
      <ViewOnly net={net} />
      <AddressSection key={`${iface.name}-${rev}`} iface={iface} onApplied={load} />
      <button type="button" className="ghost small align-start" onClick={load}>
        <Icon name="restart" size={13} /> Refresh
      </button>
    </div>
  );
}

function AddressSection({ iface, onApplied }: { iface: NetInterface; onApplied: () => void }) {
  return (
    <Section title="TCP/IP" hint={[ifaceKind(iface), iface.connection, iface.mac].filter(Boolean).join(' · ')}>
      <div className="settings-row full">
        <IPv4Summary iface={iface} />
      </div>
      <IPv4Editor iface={iface} onApplied={onApplied} />
    </Section>
  );
}
