import { openApp } from '../apps/meta';
import { Bar, Ring, Sparkline } from '../components/Charts';
import { Icon } from '../components/Icon';
import { fmtBytes, fmtRate, fmtUptime } from '../lib/format';
import { useClock } from '../lib/hooks';
import { fmtDate, fmtTime, useTimePrefs, zoned } from '../lib/time';
import { useSystem } from '../state/system';

export function ClockWidget({ username }: { username: string }) {
  const now = useClock(1000);
  const tp = useTimePrefs();
  const hour = zoned(now, tp.timeZone).hour;
  const greeting = hour < 5 ? 'Good night' : hour < 12 ? 'Good morning' : hour < 18 ? 'Good afternoon' : 'Good evening';
  return (
    <div className="clock-widget">
      <div className="clock-time">{fmtTime(now, tp)}</div>
      <div className="clock-date">
        {fmtDate(now, tp)}
      </div>
      <div className="clock-greet">
        {greeting}, {username}
      </div>
    </div>
  );
}

export function SystemWidget() {
  const latest = useSystem((s) => s.latest);
  const history = useSystem((s) => s.history);
  const info = useSystem((s) => s.info);
  if (!latest) return <div className="widget glass skeleton" />;
  const disks = latest.disks ?? [];
  return (
    <button type="button" className="widget glass" onClick={() => openApp('monitor')}>
      <div className="widget-head">
        <span>
          <Icon name="monitor" size={15} /> {info?.host.hostname ?? 'System'}
        </span>
        <span className="muted small">up {fmtUptime(latest.uptime)}</span>
      </div>
      <div className="rings">
        <Ring value={latest.cpu.percent} label="CPU" sub={info ? `${info.host.cpu_cores} cores` : undefined} />
        <Ring
          value={latest.memory.percent}
          label="Memory"
          sub={`${fmtBytes(latest.memory.used, 1)} / ${fmtBytes(latest.memory.total, 0)}`}
        />
      </div>
      <Sparkline values={history.map((h) => h.cpu)} max={100} height={36} className="accent" />
      {disks.slice(0, 2).map((d) => (
        <div key={d.path} className="widget-row">
          <div className="row-head small">
            <span>
              <Icon name="disk" size={13} /> {d.path}
            </span>
            <span className="muted">
              {fmtBytes(d.free, 0)} free of {fmtBytes(d.total, 0)}
            </span>
          </div>
          <Bar value={d.percent} />
        </div>
      ))}
    </button>
  );
}

export function NetworkWidget() {
  const history = useSystem((s) => s.history);
  const latest = useSystem((s) => s.latest);
  const nics = latest?.network ?? [];
  if (!latest || !nics.length) return null;
  const last = history[history.length - 1];
  return (
    <div className="widget glass">
      <div className="widget-head">
        <span>
          <Icon name="network" size={15} /> Network
        </span>
        <span className="muted small">{nics.map((n) => n.interface).join(', ')}</span>
      </div>
      <div className="net-rates">
        <span>
          <Icon name="arrowDown" size={14} /> {fmtRate(last?.rx)}
        </span>
        <span>
          <Icon name="arrowUp" size={14} /> {fmtRate(last?.tx)}
        </span>
      </div>
      <Sparkline values={history.map((h) => h.rx + h.tx)} height={36} className="teal" />
    </div>
  );
}

export function AcceleratorWidget() {
  const latest = useSystem((s) => s.latest);
  const gpus = (latest?.accelerators ?? []).filter((a) => a.kind !== 'display');
  if (!gpus.length) return null;
  return (
    <div className="widget glass">
      <div className="widget-head">
        <span>
          <Icon name="gpu" size={15} /> AI accelerators
        </span>
        <span className="muted small">{gpus.length}</span>
      </div>
      {gpus.slice(0, 3).map((g) => (
        <div key={g.id} className="widget-row">
          <div className="row-head small">
            <span>{g.name}</span>
            <span className="muted">
              {g.metrics.utilization != null ? `${g.metrics.utilization.toFixed(0)}%` : g.kind.toUpperCase()}
            </span>
          </div>
          {g.metrics.utilization != null && <Bar value={g.metrics.utilization} />}
        </div>
      ))}
    </div>
  );
}
