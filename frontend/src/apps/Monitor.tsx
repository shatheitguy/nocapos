import { useState, type ReactNode } from 'react';
import { Bar, Sparkline } from '../components/Charts';
import { Icon, type IconName } from '../components/Icon';
import { fmtBytes, fmtPct, fmtRate, fmtUptime } from '../lib/format';
import { useSystem } from '../state/system';

type Tab = 'overview' | 'cpu' | 'memory' | 'network' | 'storage' | 'accelerators';
const TABS: { id: Tab; label: string; icon: IconName }[] = [
  { id: 'overview', label: 'Overview', icon: 'monitor' },
  { id: 'cpu', label: 'CPU', icon: 'cpu' },
  { id: 'memory', label: 'Memory', icon: 'memory' },
  { id: 'network', label: 'Network', icon: 'network' },
  { id: 'storage', label: 'Storage', icon: 'disk' },
  { id: 'accelerators', label: 'Accelerators', icon: 'gpu' },
];

export function Monitor() {
  const [tab, setTab] = useState<Tab>('overview');
  const latest = useSystem((s) => s.latest);
  const history = useSystem((s) => s.history);
  const info = useSystem((s) => s.info);

  if (!latest) {
    return (
      <div className="empty">
        <Icon name="monitor" size={32} />
        <p className="muted">Waiting for the first metrics sample…</p>
      </div>
    );
  }

  const cpu = history.map((h) => h.cpu);
  const mem = history.map((h) => h.mem);
  const rx = history.map((h) => h.rx);
  const tx = history.map((h) => h.tx);
  const last = history[history.length - 1];
  const disks = latest.disks ?? [];
  const nics = latest.network ?? [];
  const accels = (latest.accelerators ?? []).filter((a) => a.kind !== 'display');
  const temps = latest.temperatures ?? [];

  return (
    <div className="app-split">
      <nav className="sidebar">
        {TABS.map((t) => (
          <button key={t.id} type="button" className={tab === t.id ? 'on' : ''} onClick={() => setTab(t.id)}>
            <Icon name={t.icon} size={16} /> {t.label}
          </button>
        ))}
      </nav>

      <div className="app-content">
        {tab === 'overview' && (
          <div className="cards">
            <Card title="CPU" value={fmtPct(latest.cpu.percent, 1)} sub={info?.host.cpu_model}>
              <Sparkline values={cpu} max={100} className="accent" />
            </Card>
            <Card
              title="Memory"
              value={fmtPct(latest.memory.percent, 1)}
              sub={`${fmtBytes(latest.memory.used)} of ${fmtBytes(latest.memory.total)}`}
            >
              <Sparkline values={mem} max={100} className="violet" />
            </Card>
            <Card
              title="Network"
              value={nics.length ? fmtRate((last?.rx ?? 0) + (last?.tx ?? 0)) : '–'}
              sub={nics.length ? `↓ ${fmtRate(last?.rx)} · ↑ ${fmtRate(last?.tx)}` : 'No counters on this platform yet'}
            >
              <Sparkline values={rx.map((v, i) => v + tx[i])} className="teal" />
            </Card>
            <Card title="Uptime" value={fmtUptime(latest.uptime)} sub={info?.host.os}>
              {disks.map((d) => (
                <div key={d.path} className="stack-xs">
                  <div className="row-head small">
                    <span>{d.path}</span>
                    <span className="muted">{fmtPct(d.percent)}</span>
                  </div>
                  <Bar value={d.percent} />
                </div>
              ))}
            </Card>
          </div>
        )}

        {tab === 'cpu' && (
          <>
            <Section title="Utilization" right={fmtPct(latest.cpu.percent, 1)}>
              <Sparkline values={cpu} max={100} height={140} className="accent" />
            </Section>
            <div className="stats-row">
              <Stat label="Cores" value={String(info?.host.cpu_cores ?? latest.cpu.per_core.length)} />
              <Stat label="Load (1/5/15)" value={latest.cpu.load1 || latest.cpu.load5 ? `${latest.cpu.load1.toFixed(2)} / ${latest.cpu.load5.toFixed(2)} / ${latest.cpu.load15.toFixed(2)}` : '–'} />
              <Stat label="Model" value={info?.host.cpu_model ?? '–'} />
            </div>
            {latest.cpu.per_core.length > 0 && (
              <Section title="Per core">
                <div className="core-grid">
                  {latest.cpu.per_core.map((p, i) => (
                    <div key={i} className="core">
                      <span className="muted small">#{i}</span>
                      <Bar value={p} />
                      <span className="small">{fmtPct(p)}</span>
                    </div>
                  ))}
                </div>
              </Section>
            )}
            {temps.length > 0 && (
              <Section title="Temperatures">
                <div className="chip-row">
                  {temps.map((t, i) => (
                    <span key={i} className={`chip ${t.celsius > 85 ? 'bad' : t.celsius > 70 ? 'warn' : ''}`}>
                      {t.sensor} · {t.celsius.toFixed(0)}°C
                    </span>
                  ))}
                </div>
              </Section>
            )}
          </>
        )}

        {tab === 'memory' && (
          <>
            <Section title="Memory in use" right={fmtPct(latest.memory.percent, 1)}>
              <Sparkline values={mem} max={100} height={140} className="violet" />
            </Section>
            <div className="stats-row">
              <Stat label="Used" value={fmtBytes(latest.memory.used)} />
              <Stat label="Available" value={fmtBytes(latest.memory.available)} />
              <Stat label="Total" value={fmtBytes(latest.memory.total)} />
              {latest.memory.swap_total > 0 && (
                <Stat label="Swap" value={`${fmtBytes(latest.memory.swap_used)} / ${fmtBytes(latest.memory.swap_total)}`} />
              )}
            </div>
          </>
        )}

        {tab === 'network' && (
          <>
            {nics.length === 0 ? (
              <Empty icon="network" text="Per-interface counters aren't collected on this platform yet (available on Linux hosts)." />
            ) : (
              <>
                <Section title="Throughput" right={`↓ ${fmtRate(last?.rx)}  ↑ ${fmtRate(last?.tx)}`}>
                  <Sparkline values={rx} height={100} className="teal" />
                  <Sparkline values={tx} height={60} className="accent" />
                </Section>
                <table className="table">
                  <thead>
                    <tr>
                      <th>Interface</th>
                      <th>Download</th>
                      <th>Upload</th>
                      <th>Received</th>
                      <th>Sent</th>
                    </tr>
                  </thead>
                  <tbody>
                    {nics.map((n) => (
                      <tr key={n.interface}>
                        <td>
                          <b>{n.interface}</b>
                        </td>
                        <td>{fmtRate(n.rx_rate)}</td>
                        <td>{fmtRate(n.tx_rate)}</td>
                        <td>{fmtBytes(n.rx_bytes)}</td>
                        <td>{fmtBytes(n.tx_bytes)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </>
            )}
          </>
        )}

        {tab === 'storage' && (
          <div className="stack">
            {disks.length === 0 && <Empty icon="disk" text="No volumes reported." />}
            {disks.map((d) => (
              <div key={d.path} className="panel">
                <div className="row-head">
                  <b>
                    <Icon name="disk" size={16} /> {d.path}
                  </b>
                  <span className="muted">{fmtPct(d.percent, 1)} used</span>
                </div>
                <Bar value={d.percent} />
                <div className="stats-row">
                  <Stat label="Used" value={fmtBytes(d.used)} />
                  <Stat label="Free" value={fmtBytes(d.free)} />
                  <Stat label="Capacity" value={fmtBytes(d.total)} />
                </div>
              </div>
            ))}
          </div>
        )}

        {tab === 'accelerators' && (
          <div className="stack">
            {accels.length === 0 && (
              <Empty icon="gpu" text="No GPUs, NPUs or TPUs detected. NVIDIA, AMD, Intel, Mali, Jetson, Coral and Hailo devices are supported." />
            )}
            {accels.map((g) => (
              <div key={g.id} className="panel">
                <div className="row-head">
                  <b>
                    <Icon name="gpu" size={16} /> {g.name}
                  </b>
                  <span className="chip">{g.kind.toUpperCase()} · {g.vendor}</span>
                </div>
                {g.metrics.utilization != null && <Bar value={g.metrics.utilization} />}
                <div className="stats-row">
                  {g.metrics.utilization != null && <Stat label="Utilization" value={fmtPct(g.metrics.utilization)} />}
                  {g.metrics.mem_total != null && (
                    <Stat label="VRAM" value={`${fmtBytes(g.metrics.mem_used)} / ${fmtBytes(g.metrics.mem_total)}`} />
                  )}
                  {g.metrics.temp_c != null && <Stat label="Temperature" value={`${g.metrics.temp_c.toFixed(0)}°C`} />}
                  {g.metrics.power_w != null && <Stat label="Power" value={`${g.metrics.power_w.toFixed(0)} W`} />}
                  {g.metrics.freq_mhz != null && <Stat label="Clock" value={`${g.metrics.freq_mhz.toFixed(0)} MHz`} />}
                  <Stat label="Driver" value={g.driver || '–'} />
                </div>
                {g.note && <p className="muted small">{g.note}</p>}
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function Card({ title, value, sub, children }: { title: string; value: string; sub?: string; children?: ReactNode }) {
  return (
    <div className="panel">
      <div className="muted small upper">{title}</div>
      <div className="big">{value}</div>
      {sub && <div className="muted small ellipsis">{sub}</div>}
      <div className="card-body">{children}</div>
    </div>
  );
}

function Section({ title, right, children }: { title: string; right?: string; children: ReactNode }) {
  return (
    <div className="panel">
      <div className="row-head">
        <b>{title}</b>
        {right && <span className="muted">{right}</span>}
      </div>
      {children}
    </div>
  );
}

export function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="stat">
      <div className="muted small">{label}</div>
      <div className="stat-value">{value}</div>
    </div>
  );
}

export function Empty({ icon, text, children }: { icon: IconName; text: string; children?: ReactNode }) {
  return (
    <div className="empty">
      <Icon name={icon} size={32} />
      <p className="muted">{text}</p>
      {children}
    </div>
  );
}
