import { useCallback, useEffect, useState } from 'react';
import { dockerApi, type DockerNetwork, type HostInterface, type NewNetwork } from '../api/docker';
import { Icon } from '../components/Icon';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';

// Networks for containers: the default bridge, private bridges, and macvlan /
// ipvlan networks that give containers their own address on your LAN.

const DRIVERS: { id: NewNetwork['driver']; label: string; hint: string }[] = [
  { id: 'bridge', label: 'Bridge', hint: 'A private network for containers to talk to each other; reach them through published ports.' },
  { id: 'macvlan', label: 'Macvlan', hint: 'Each container gets its own address (and MAC) on your LAN, like a separate device.' },
  { id: 'ipvlan', label: 'Ipvlan', hint: 'Containers get their own LAN address but share the server’s MAC (for Wi-Fi or switches that limit MACs).' },
];

/** A /27 at the top of the subnet, away from most routers' DHCP ranges. */
function suggestRange(subnet: string): string {
  const m = /^(\d+)\.(\d+)\.(\d+)\.(\d+)\/(\d+)$/.exec(subnet);
  if (!m || Number(m[5]) !== 24) return '';
  return `${m[1]}.${m[2]}.${m[3]}.224/27`;
}

export function DockerNetworks() {
  const [nets, setNets] = useState<DockerNetwork[] | null>(null);
  const [ifaces, setIfaces] = useState<HostInterface[]>([]);
  const [error, setError] = useState('');
  const [adding, setAdding] = useState(false);

  const load = useCallback(async () => {
    const r = await dockerApi.networks();
    if (!r.ok) return setError(r.error ?? 'Could not load networks');
    setError('');
    setNets(r.data.networks);
    setIfaces(r.data.interfaces);
  }, []);
  useEffect(() => {
    void load();
  }, [load]);

  const remove = async (n: DockerNetwork) => {
    const ok = await confirmDialog({ title: `Delete the network “${n.name}”?`, message: 'Containers can’t use it after this.', confirmLabel: 'Delete', danger: true });
    if (!ok) return;
    const r = await dockerApi.removeNetwork(n.name);
    if (!r.ok) return toast('error', 'Could not delete it', r.error);
    void load();
  };

  if (error) return <p className="error dn-pad">{error}</p>;
  if (!nets) return <span className="spinner" />;
  return (
    <div className="dn">
      <div className="dn-head">
        <p className="muted small">Networks decide how containers reach each other and your LAN. Pick one for a container in its settings.</p>
        <button type="button" onClick={() => setAdding(true)}>
          <Icon name="plus" size={14} /> Create Network
        </button>
      </div>
      {adding && (
        <NewNetworkForm
          ifaces={ifaces}
          onCancel={() => setAdding(false)}
          onCreated={() => {
            setAdding(false);
            void load();
          }}
        />
      )}
      <table className="table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Addresses</th>
            <th>Containers</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {nets.map((n) => (
            <tr key={n.id}>
              <td>
                <b>{n.name}</b>
                {n.builtin && <span className="chip tiny">built-in</span>}
                {n.internal && <span className="chip tiny">no internet</span>}
              </td>
              <td>
                <span className={`chip ${n.driver === 'macvlan' || n.driver === 'ipvlan' ? 'good' : ''}`}>{n.driver}</span>
                {n.parent && <div className="muted small">on {n.parent}{n.mode ? ` (${n.mode})` : ''}</div>}
              </td>
              <td className="small mono">
                {n.subnet || '–'}
                {n.gateway && <div className="muted">gw {n.gateway}</div>}
                {n.ip_range && <div className="muted">range {n.ip_range}</div>}
              </td>
              <td className="small">{n.containers.length ? n.containers.join(', ') : <span className="muted">none</span>}</td>
              <td className="row-actions">
                {!n.builtin && (
                  <button type="button" className="ghost icon-btn danger" title={n.containers.length ? 'In use' : 'Delete'} disabled={n.containers.length > 0} onClick={() => void remove(n)}>
                    <Icon name="trash" size={14} />
                  </button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function NewNetworkForm({ ifaces, onCancel, onCreated }: { ifaces: HostInterface[]; onCancel: () => void; onCreated: () => void }) {
  const first = ifaces[0];
  const [n, setN] = useState<NewNetwork>({ name: 'lan', driver: 'macvlan', parent: first?.name ?? '', subnet: first?.subnet ?? '', gateway: first?.gateway ?? '', ip_range: suggestRange(first?.subnet ?? ''), mode: 'l2' });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const set = (patch: Partial<NewNetwork>) => setN((x) => ({ ...x, ...patch }));
  const lan = n.driver !== 'bridge';

  const pickParent = (name: string) => {
    const i = ifaces.find((x) => x.name === name);
    set({ parent: name, subnet: i?.subnet ?? n.subnet, gateway: i?.gateway ?? n.gateway, ip_range: suggestRange(i?.subnet ?? n.subnet ?? '') });
  };
  const create = async () => {
    setBusy(true);
    setError('');
    const body: NewNetwork = lan ? n : { name: n.name, driver: 'bridge', subnet: n.subnet || undefined, gateway: n.gateway || undefined, internal: n.internal };
    const r = await dockerApi.createNetwork(body);
    setBusy(false);
    if (!r.ok) return setError(r.error ?? 'Could not create the network');
    toast('success', `Network ${n.name} created`);
    onCreated();
  };

  return (
    <div className="dn-form">
      <div className="bk-kinds dn-kinds">
        {DRIVERS.map((d) => (
          <button
            key={d.id}
            type="button"
            className={`bk-kind ${n.driver === d.id ? 'on' : ''}`}
            onClick={() => (d.id === 'bridge' ? set({ driver: 'bridge', name: n.name === 'lan' ? 'apps' : n.name, subnet: '', gateway: '', ip_range: '' }) : (set({ driver: d.id }), pickParent(n.parent || first?.name || '')))}
          >
            <Icon name={d.id === 'bridge' ? 'containers' : 'network'} size={20} />
            <b>{d.label}</b>
            <span>{d.hint}</span>
          </button>
        ))}
      </div>
      <div className="ce-grid">
        <label>
          Name
          <input value={n.name} spellCheck={false} onChange={(e) => set({ name: e.target.value })} />
        </label>
        {lan && (
          <label>
            Network card
            <select value={n.parent} onChange={(e) => pickParent(e.target.value)}>
              {ifaces.map((i) => (
                <option key={i.name} value={i.name}>
                  {i.name}
                  {i.address ? ` (${i.address})` : ''}
                </option>
              ))}
              {!ifaces.length && <option value="">No network cards found</option>}
            </select>
          </label>
        )}
        <label>
          Subnet {lan ? '' : <span className="muted">(optional)</span>}
          <input value={n.subnet ?? ''} spellCheck={false} placeholder={lan ? '192.168.1.0/24' : 'automatic'} onChange={(e) => set({ subnet: e.target.value.trim(), ip_range: lan ? suggestRange(e.target.value.trim()) : '' })} />
        </label>
        <label>
          Gateway {lan ? <span className="muted">(your router)</span> : <span className="muted">(optional)</span>}
          <input value={n.gateway ?? ''} spellCheck={false} placeholder={lan ? '192.168.1.1' : 'automatic'} onChange={(e) => set({ gateway: e.target.value.trim() })} />
        </label>
        {lan && (
          <label>
            Addresses for containers <span className="muted">(keep outside your router's DHCP)</span>
            <input value={n.ip_range ?? ''} spellCheck={false} placeholder="192.168.1.224/27" onChange={(e) => set({ ip_range: e.target.value.trim() })} />
          </label>
        )}
        {n.driver === 'ipvlan' && (
          <label>
            Mode
            <select value={n.mode} onChange={(e) => set({ mode: e.target.value as 'l2' | 'l3' })}>
              <option value="l2">L2 (same LAN, most common)</option>
              <option value="l3">L3 (routed)</option>
            </select>
          </label>
        )}
        {!lan && (
          <label className="ce-check">
            <input type="checkbox" checked={!!n.internal} onChange={(e) => set({ internal: e.target.checked })} /> No internet access (internal only)
          </label>
        )}
      </div>
      {lan && <p className="muted small">Tip: containers on this network get their own LAN address; give each a fixed IP inside the range above. The server itself can't reach them directly (other devices can).</p>}
      {error && <p className="error">{error}</p>}
      <div className="dialog-actions">
        <button type="button" className="ghost" onClick={onCancel}>
          Cancel
        </button>
        <button type="button" disabled={busy || !n.name.trim() || (lan && (!n.parent || !n.subnet))} onClick={() => void create()}>
          {busy ? 'Creating…' : 'Create Network'}
        </button>
      </div>
    </div>
  );
}
