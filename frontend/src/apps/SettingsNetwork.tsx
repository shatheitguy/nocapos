import { tr } from '../i18n';
import { useCallback, useEffect, useState } from 'react';
import { hostApi, type NetInterface, type NetworkState } from '../api/hostctl';
import { Icon, type IconName } from '../components/Icon';
import { setNetwork } from '../lib/hostActions';
import { missing } from '../lib/settingsKit';
import { IPv4Editor, IPv4Summary, WifiList } from './NetworkParts';
import { Section, Toggle } from './Personalize';
import { FindServersSection } from './SettingsFindServers';
import { SavedNetworks } from './WifiSaved';

// Settings → Network: an overview of connections, then Wi-Fi and one page
// per adapter with its IP settings.

function useNetwork() {
  const [net, setNet] = useState<NetworkState | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [rev, setRev] = useState(0); // remount editors after a change
  const load = useCallback(
    () =>
      void hostApi.network().then((r) => {
        if (r.ok && !missing(r)) {
          setError(null);
          setNet(r.data);
          setRev((v) => v + 1);
        } else setError(missing(r) ? tr('set.notAvailable') : (r.error ?? tr('sn.loadFail')));
      }),
    [],
  );
  useEffect(load, [load]);
  return { net, setNet, error, rev, load };
}

const ipOf = (i: NetInterface) => (i.ipv4?.addresses[0] ?? i.addresses.find((a) => !a.includes(':')))?.replace(/\/\d+$/, '');
const connected = (i: NetInterface) => i.up && !!ipOf(i);
const ifaceIcon = (i: NetInterface): IconName => (i.kind === 'wifi' ? 'wifi' : i.kind === 'ethernet' ? 'ethernet' : 'network');
const ifaceKind = (i: NetInterface) => tr(i.kind === 'wifi' ? 'sn.kindWifi' : i.kind === 'ethernet' ? 'sn.kindEthernet' : 'sn.kindOther');

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
      <Icon name="info" size={15} /> {tr('sn.readOnly')}
    </p>
  );
}

export function NetworkHome({ open }: { open: (sub: string) => void }) {
  const { net, setNet, error } = useNetwork();
  const [busy, setBusy] = useState(false);
  if (error) return <p className="error">{error}</p>;
  if (!net) return <p className="muted">{tr('sp.netChecking')}</p>;
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
          <b>{primary ? (primary.connection ? tr('sn.connectedTo', { name: primary.connection }) : tr('sn.connected')) : tr('sn.notConnected')}</b>
          <span>{primary ? `${ifaceKind(primary)} · ${primary.name} · ${ipOf(primary)}` : tr('sn.noAddressAll')}</span>
        </div>
      </div>
      <ViewOnly net={net} />

      <div className="ucard ulist">
        <div className="urow">
          <span className="urow-icon set-ico" style={{ ['--c' as string]: '#0a84ff' }}><Icon name="wifi" size={16} /></span>
          <button type="button" className="urow-text set-row-link" disabled={!net.wifi.supported} onClick={() => open('wifi')}>
            <span className="urow-title">{tr('set.wifi')}</span>
            <span className="urow-desc">
              {!net.wifi.supported ? tr('set.notAvailable') : !net.wifi.enabled ? tr('sn.off') : wireless?.connection || tr('sn.notConnected')}
            </span>
          </button>
          <span className="urow-control">
            <Switch label={tr('sn.kindWifi')} checked={net.wifi.enabled} disabled={!net.wifi.supported || busy} onChange={(on) => void flip('wifi', on)} />
            {net.wifi.supported && <Icon name="chevronRight" size={16} />}
          </span>
        </div>
        {wired.map((i) => (
          <button key={i.name} type="button" className="urow" onClick={() => open(`iface:${i.name}`)}>
            <span className="urow-icon set-ico" style={{ ['--c' as string]: connected(i) ? '#30d158' : '#8e8e93' }}><Icon name={ifaceIcon(i)} size={16} /></span>
            <span className="urow-text">
              <span className="urow-title">{i.kind === 'ethernet' ? tr('sn.ethernetName', { name: i.name }) : i.name}</span>
              <span className="urow-desc">{connected(i) ? tr('sn.connectedIp', { ip: ipOf(i) }) : i.up ? tr('sn.noAddress') : tr('sn.notConnected')}</span>
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
          <span className="urow-icon set-ico" style={{ ['--c' as string]: '#636366' }}><Icon name="globe" size={16} /></span>
          <span className="urow-text">
            <span className="urow-title">{tr('sn.networking')}</span>
            <span className="urow-desc">{tr('sn.networkingHint')}</span>
          </span>
          <span className="urow-control">
            <Switch label={tr('sn.networking')} checked={net.networking.enabled} disabled={!net.networking.supported || busy} onChange={(on) => void flip('networking', on)} />
          </span>
        </div>
      </div>

      <FindServersSection />
    </div>
  );
}

export function WifiPage() {
  const { net, setNet, error, rev, load } = useNetwork();
  const [busy, setBusy] = useState(false);
  if (error) return <p className="error">{error}</p>;
  if (!net) return <p className="muted">{tr('sn.checkingWifi')}</p>;
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
      <Section title={tr('sn.kindWifi')}>
        <Toggle label={tr('sn.kindWifi')} hint={net.wifi.supported ? (net.wifi.enabled ? wireless?.connection || tr('sn.notConnected') : tr('sn.off')) : tr('set.notAvailable')}
          checked={net.wifi.enabled} disabled={!net.wifi.supported || busy} onChange={(on) => void flip(on)} />
        {net.wifi.supported && net.wifi.enabled && (
          <div className="settings-row full">
            <WifiList device={wireless?.name} onChanged={load} />
          </div>
        )}
      </Section>
      {net.manager === 'networkmanager' && net.wifi.supported && (
        <Section title={tr('wifi.savedTitle')} hint={tr('wifi.savedHint')}>
          <div className="settings-row full">
            <SavedNetworks rev={rev} onChanged={load} />
          </div>
        </Section>
      )}
      {wireless && <AddressSection key={`${wireless.name}-${rev}`} iface={wireless} onApplied={load} />}
    </div>
  );
}

export function InterfacePage({ name }: { name: string }) {
  const { net, error, rev, load } = useNetwork();
  if (error) return <p className="error">{error}</p>;
  if (!net) return <p className="muted">{tr('sp.netChecking')}</p>;
  const iface = net.interfaces.find((i) => i.name === name);
  if (!iface) return <p className="muted">{tr('sp.netNoAdapter', { name })}</p>;
  return (
    <div className="stack settings-page">
      <ViewOnly net={net} />
      <AddressSection key={`${iface.name}-${rev}`} iface={iface} onApplied={load} />
      <button type="button" className="ghost small align-start" onClick={load}>
        <Icon name="restart" size={13} /> {tr('sp.refresh')}
      </button>
    </div>
  );
}

function AddressSection({ iface, onApplied }: { iface: NetInterface; onApplied: () => void }) {
  return (
    <Section title={tr('sn.tcpip')} hint={[ifaceKind(iface), iface.connection, iface.mac].filter(Boolean).join(' · ')}>
      <div className="settings-row full">
        <IPv4Summary iface={iface} />
      </div>
      <IPv4Editor iface={iface} onApplied={onApplied} />
    </Section>
  );
}
