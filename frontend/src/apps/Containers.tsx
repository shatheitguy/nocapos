import { Fragment, useCallback, useEffect, useMemo, useRef, useState, type MouseEvent, type ReactNode } from 'react';
import { api } from '../api/client';
import type { Container, ContainerDetail, ContainerStats, DockerEvent } from '../api/types';
import { Sparkline } from '../components/Charts';
import { Icon, type IconName } from '../components/Icon';
import { fmtAgo, fmtBytes, fmtPct, fmtRate, fmtUptime } from '../lib/format';
import { usePoll, useTopic } from '../lib/hooks';
import { resubscribe } from '../api/socket';
import { toast } from '../state/toasts';
import { openApp } from './meta';
import { Empty } from './Monitor';
import { ContainerEditor } from './ContainerEditor';
import { DockerNetworks } from './DockerNetworks';
import { Stacks } from './Stacks';
import { dockerApi, type ContainerAction } from '../api/docker';
import { confirmDialog } from '../state/confirm';
import { LogView } from './Logs';
import { Meter, TypeConfirm } from './StorageParts';
import { WebAddressField } from './WebAddress';

// Containers: every container as a live card (CPU, memory, network, disk,
// uptime, ports), a page per container (overview, logs, inspect, actions),
// plus Stacks and Networks. Admin only; NoCapOS's own containers can't be
// stopped, edited or removed (the server enforces it too).

type Tab = 'containers' | 'stacks' | 'networks';
type Filter = 'all' | 'running' | 'stopped' | 'apps' | 'system';
type Sort = 'name' | 'cpu' | 'ram';
type Layout = 'grid' | 'list';

/** Recent samples per container, for sparklines. */
interface History {
  cpu: number[];
  mem: number[];
  net: number[];
  disk: number[];
}
const HISTORY = 60;
const STATS_EVERY = 2500;

function readPref<T extends string>(key: string, allowed: readonly T[], fallback: T): T {
  try {
    const v = localStorage.getItem(key) as T | null;
    return v && allowed.includes(v) ? v : fallback;
  } catch {
    return fallback;
  }
}
function writePref(key: string, v: string) {
  try {
    localStorage.setItem(key, v);
  } catch {
    /* private mode */
  }
}

const isApp = (c: Container) => !!c.labels?.['nocapos.app'];
const isRunning = (c: Container) => c.state === 'running';

function stateTone(state: string) {
  return state === 'running' ? 'good' : state === 'paused' || state === 'restarting' ? 'warn' : state === 'exited' || state === 'dead' ? 'bad' : '';
}

/** Where "Open" goes: the server's pick (own address / macvlan IP), else this
 *  server on the first published TCP port. */
function containerLink(c: Pick<Container, 'web_url' | 'ports'>): string | null {
  if (c.web_url) return c.web_url;
  const p = c.ports.find((x) => x.public && (x.protocol === 'tcp' || !x.protocol));
  return p ? `http://${window.location.hostname}:${p.public}/` : null;
}

function uptime(c: Container): string | null {
  if (!c.started_at || !isRunning(c)) return null;
  return fmtUptime(Math.max(0, (Date.now() - new Date(c.started_at).getTime()) / 1000));
}

function openTerminal(c: Container) {
  openApp('terminal', { title: `Terminal — ${c.name}`, props: { target: `container:${c.id}`, label: c.name }, key: `terminal:${c.id}` });
}

export function Containers() {
  const [list, setList] = useState<Container[] | null>(null);
  const [error, setError] = useState<{ status: number; message: string } | null>(null);
  const [tab, setTab] = useState<Tab>('containers');
  const [openId, setOpenId] = useState<string | null>(null);
  const [search, setSearch] = useState('');
  const [filter, setFilter] = useState<Filter>('all');
  const [sort, setSort] = useState<Sort>(() => readPref('nocap.ct.sort', ['name', 'cpu', 'ram'] as const, 'name'));
  const [layout, setLayout] = useState<Layout>(() => readPref('nocap.ct.layout', ['grid', 'list'] as const, 'grid'));
  const [busy, setBusy] = useState<string | null>(null);
  /** The container being edited ('new' = adding one). */
  const [editing, setEditing] = useState<string | null>(null);
  const [stats, setStats] = useState<Record<string, ContainerStats>>({});
  const history = useRef(new Map<string, History>());
  const reloadTimer = useRef<number | undefined>(undefined);
  const quietUntil = useRef(new Map<string, number>());

  const load = useCallback(async () => {
    const r = await api<Container[]>('/api/v1/containers');
    if (!r.ok) {
      setError({ status: r.status, message: r.error ?? 'Request failed' });
      setList(null);
      return;
    }
    setError(null);
    setList(r.data);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);
  usePoll(load, 15_000);

  useTopic<DockerEvent>(
    'docker.events',
    (ev) => {
      if (ev.type !== 'container') return;
      window.clearTimeout(reloadTimer.current);
      reloadTimer.current = window.setTimeout(() => void load(), 300);
      // 143 is a normal stop; containers being edited stop on purpose too.
      const quiet = ev.name !== undefined && (quietUntil.current.get(ev.name) ?? 0) > Date.now();
      if (ev.action === 'die' && ev.exit_code && ev.exit_code !== '0' && ev.exit_code !== '143' && ev.exit_code !== '137' && !quiet) {
        toast('error', `${ev.name ?? 'Container'} exited`, `Exit code ${ev.exit_code}`);
      }
    },
    // The feed ends while Docker is down; retry so events resume on reconnect.
    () => window.setTimeout(() => resubscribe('docker.events'), 10_000),
  );

  // Live usage: one batched request for the containers on screen, only while
  // this window is visible (usePoll pauses in hidden tabs and minimized windows).
  const running = useMemo(() => (list ?? []).filter(isRunning), [list]);
  const statIds = useMemo(() => (openId ? running.filter((c) => c.id === openId) : running).map((c) => c.id), [running, openId]);
  const statKey = statIds.join(',');
  const fetchStats = useCallback(async () => {
    if (!statKey) return;
    const r = await dockerApi.stats(statKey.split(','));
    if (!r.ok) return;
    const fresh = r.data.stats;
    for (const [id, s] of Object.entries(fresh)) {
      const h = history.current.get(id) ?? { cpu: [], mem: [], net: [], disk: [] };
      const push = (a: number[], v: number) => {
        a.push(v);
        if (a.length > HISTORY) a.shift();
      };
      push(h.cpu, s.cpu_percent);
      push(h.mem, s.mem_usage);
      push(h.net, s.net_rx_rate + s.net_tx_rate);
      push(h.disk, (s.block_read_rate ?? 0) + (s.block_write_rate ?? 0));
      history.current.set(id, h);
    }
    setStats((cur) => ({ ...cur, ...fresh }));
  }, [statKey]);
  useEffect(() => {
    if (tab === 'containers') void fetchStats();
  }, [fetchStats, tab]);
  usePoll(fetchStats, STATS_EVERY, tab === 'containers' && !!statKey);

  const counts = useMemo(() => {
    const l = list ?? [];
    return {
      all: l.length,
      running: l.filter(isRunning).length,
      stopped: l.filter((c) => !isRunning(c)).length,
      apps: l.filter(isApp).length,
      system: l.filter((c) => c.system).length,
    };
  }, [list]);

  const shown = useMemo(() => {
    const q = search.trim().toLowerCase();
    const out = (list ?? []).filter((c) => {
      if (filter === 'running' && !isRunning(c)) return false;
      if (filter === 'stopped' && isRunning(c)) return false;
      if (filter === 'apps' && !isApp(c)) return false;
      if (filter === 'system' && !c.system) return false;
      return !q || c.name.toLowerCase().includes(q) || c.image.toLowerCase().includes(q) || !!c.project?.toLowerCase().includes(q);
    });
    const byName = (a: Container, b: Container) => a.name.localeCompare(b.name);
    if (sort === 'cpu') out.sort((a, b) => (stats[b.id]?.cpu_percent ?? -1) - (stats[a.id]?.cpu_percent ?? -1) || byName(a, b));
    else if (sort === 'ram') out.sort((a, b) => (stats[b.id]?.mem_usage ?? -1) - (stats[a.id]?.mem_usage ?? -1) || byName(a, b));
    else out.sort(byName);
    return out;
  }, [list, search, filter, sort, stats]);

  const act = async (c: Container, action: ContainerAction) => {
    if (action === 'kill') {
      const ok = await confirmDialog({ title: `Force stop ${c.name}?`, message: 'Kill stops it at once, without letting it save or shut down cleanly.', confirmLabel: 'Kill', danger: true });
      if (!ok) return;
    }
    setBusy(c.id);
    const r = await dockerApi.action(c.id, action);
    setBusy(null);
    if (r.ok) toast('success', `${c.name}: ${action} done`);
    else toast('error', `Couldn’t ${action} ${c.name}`, r.error);
    void load();
  };

  if (error) {
    const offline = error.status === 503 || error.status === 0;
    return (
      <Empty icon="containers" text={offline ? 'Docker is not running on this machine. Start Docker — NoCapOS connects automatically.' : error.message}>
        <button type="button" onClick={() => void load()}>
          <Icon name="restart" size={15} /> Retry
        </button>
      </Empty>
    );
  }
  if (!list) return <Empty icon="containers" text="Loading containers…" />;

  if (editing) {
    return (
      <div className="containers">
        <ContainerEditor
          containerId={editing === 'new' ? null : editing}
          onClose={() => setEditing(null)}
          onApplying={(name) => quietUntil.current.set(name, Date.now() + 120_000)}
          onSaved={() => {
            setEditing(null);
            setOpenId(null);
            void load();
          }}
        />
      </div>
    );
  }

  const nav: { id: Tab; label: string; icon: IconName; count?: number }[] = [
    { id: 'containers', label: 'Containers', icon: 'containers', count: list.length },
    { id: 'stacks', label: 'Stacks', icon: 'layers' },
    { id: 'networks', label: 'Networks', icon: 'network' },
  ];
  const current = openId ? list.find((c) => c.id === openId) ?? null : null;

  return (
    <div className="ct-app app-split">
      <nav className="sidebar ct-side">
        <div className="ct-side-head">
          <h1 className="app-title">Containers</h1>
          <span className="app-subtitle">
            {counts.running} running · {counts.all} total
          </span>
        </div>
        {nav.map((n) => (
          <button key={n.id} type="button" className={tab === n.id ? 'on' : ''} onClick={() => { setTab(n.id); setOpenId(null); }}>
            <Icon name={n.icon} size={16} />
            <span className="ct-nav-label">{n.label}</span>
            {n.count != null && n.count > 0 && <span className="ct-count">{n.count}</span>}
          </button>
        ))}
      </nav>
      <main className="app-content ct-main">
        {tab === 'stacks' && <Stacks />}
        {tab === 'networks' && <DockerNetworks />}
        {tab === 'containers' && current && (
          <ContainerPage
            key={current.id}
            c={current}
            stats={stats[current.id]}
            hist={history.current.get(current.id)}
            busy={busy === current.id}
            onBack={() => setOpenId(null)}
            onAct={act}
            onEdit={() => setEditing(current.id)}
            onChanged={load}
            onGone={() => {
              setOpenId(null);
              void load();
            }}
            onQuiet={(name) => quietUntil.current.set(name, Date.now() + 120_000)}
          />
        )}
        {tab === 'containers' && !current && (
          <>
            <div className="ct-toolbar">
              <label className="search compact ct-search">
                <Icon name="search" size={15} />
                <input placeholder="Search by name, image or stack" value={search} onChange={(e) => setSearch(e.target.value)} />
              </label>
              <select
                className="ct-select"
                value={sort}
                aria-label="Sort by"
                onChange={(e) => {
                  const v = e.target.value as Sort;
                  setSort(v);
                  writePref('nocap.ct.sort', v);
                }}
              >
                <option value="name">Sort: Name</option>
                <option value="cpu">Sort: CPU</option>
                <option value="ram">Sort: Memory</option>
              </select>
              <div className="ct-seg" role="group" aria-label="Layout">
                {(['grid', 'list'] as const).map((l) => (
                  <button
                    key={l}
                    type="button"
                    className={layout === l ? 'on' : ''}
                    title={l === 'grid' ? 'Cards' : 'List'}
                    aria-pressed={layout === l}
                    onClick={() => {
                      setLayout(l);
                      writePref('nocap.ct.layout', l);
                    }}
                  >
                    <Icon name={l} size={15} />
                  </button>
                ))}
              </div>
              <button type="button" className="ghost icon-btn" aria-label="Refresh" title="Refresh" onClick={() => void load()}>
                <Icon name="restart" size={15} />
              </button>
              <button type="button" onClick={() => setEditing('new')}>
                <Icon name="plus" size={14} /> Add container
              </button>
            </div>
            <div className="ct-filters" role="tablist">
              {(
                [
                  ['all', 'All'],
                  ['running', 'Running'],
                  ['stopped', 'Stopped'],
                  ['apps', 'App Store'],
                  ['system', 'System'],
                ] as const
              ).map(([id, label]) => (
                <button key={id} type="button" role="tab" aria-selected={filter === id} className={`ct-filter ${filter === id ? 'on' : ''}`} onClick={() => setFilter(id)}>
                  {label} <span>{counts[id]}</span>
                </button>
              ))}
            </div>
            {shown.length === 0 ? (
              <Empty icon="containers" text={list.length ? 'No containers match.' : 'No containers yet. Apps from the App Store appear here.'} />
            ) : layout === 'grid' ? (
              <div className="ct-grid">
                {shown.map((c) => (
                  <ContainerCard key={c.id} c={c} s={stats[c.id]} hist={history.current.get(c.id)} busy={busy === c.id} onOpen={() => setOpenId(c.id)} onAct={act} />
                ))}
              </div>
            ) : (
              <ContainerTable list={shown} stats={stats} busy={busy} onOpen={setOpenId} onAct={act} />
            )}
          </>
        )}
      </main>
    </div>
  );
}

// ---------------- pieces ----------------

function StateDot({ state }: { state: string }) {
  return <span className={`ct-dot ${stateTone(state)}`} title={state} />;
}

function Badges({ c }: { c: Container }) {
  return (
    <>
      {c.system && <span className="chip tiny">system</span>}
      {isApp(c) && <span className="chip tiny accent">app</span>}
      {c.project && <span className="chip tiny" title={`Stack ${c.project}`}>{c.project}</span>}
      {c.health && <span className={`chip tiny ${c.health === 'healthy' ? 'good' : c.health === 'unhealthy' ? 'bad' : 'warn'}`}>{c.health}</span>}
    </>
  );
}

function PortLinks({ c, max = 4 }: { c: Container; max?: number }) {
  const seen = new Set<string>();
  const ports = c.ports.filter((p) => {
    const k = `${p.public ?? ''}:${p.private}/${p.protocol}`;
    if (!p.public || seen.has(k)) return false;
    seen.add(k);
    return true;
  });
  if (!ports.length) return <span className="muted small">No published ports</span>;
  return (
    <span className="ct-ports">
      {ports.slice(0, max).map((p) =>
        p.protocol === 'tcp' ? (
          <a key={`${p.public}/${p.protocol}`} className="ct-port" href={`http://${window.location.hostname}:${p.public}/`} target="_blank" rel="noopener noreferrer" onClick={(e) => e.stopPropagation()} title={`Open port ${p.public}`}>
            {p.public}→{p.private}
          </a>
        ) : (
          <span key={`${p.public}/${p.protocol}`} className="ct-port udp">
            {p.public}→{p.private}/{p.protocol}
          </span>
        ),
      )}
      {ports.length > max && <span className="muted small">+{ports.length - max}</span>}
    </span>
  );
}

function QuickActions({ c, busy, onAct }: { c: Container; busy: boolean; onAct: (c: Container, a: ContainerAction) => void }) {
  const link = containerLink(c);
  const stop = (e: MouseEvent) => e.stopPropagation();
  return (
    <div className="btn-group ct-quick" onClick={stop}>
      {link && isRunning(c) && (
        <a className="button ghost icon-btn" href={link} target="_blank" rel="noopener noreferrer" title={`Open ${link}`}>
          <Icon name="external" size={15} />
        </a>
      )}
      {c.state === 'paused' ? (
        <button type="button" className="ghost icon-btn" title="Resume" disabled={busy} onClick={() => onAct(c, 'unpause')}>
          <Icon name="play" size={15} />
        </button>
      ) : isRunning(c) ? (
        <>
          <button type="button" className="ghost icon-btn" title="Restart" disabled={busy || c.system} onClick={() => onAct(c, 'restart')}>
            <Icon name="restart" size={15} />
          </button>
          <button type="button" className="ghost icon-btn danger" title={c.system ? 'NoCapOS’s own containers are protected' : 'Stop'} disabled={busy || c.system} onClick={() => onAct(c, 'stop')}>
            <Icon name="stop" size={15} />
          </button>
        </>
      ) : (
        <button type="button" className="ghost icon-btn" title="Start" disabled={busy} onClick={() => onAct(c, 'start')}>
          <Icon name="play" size={15} />
        </button>
      )}
    </div>
  );
}

function memText(s?: ContainerStats) {
  if (!s) return '–';
  return `${fmtBytes(s.mem_usage)} / ${fmtBytes(s.mem_limit)}`;
}

function ContainerCard({ c, s, hist, busy, onOpen, onAct }: {
  c: Container; s?: ContainerStats; hist?: History; busy: boolean; onOpen: () => void; onAct: (c: Container, a: ContainerAction) => void;
}) {
  const live = isRunning(c) && s;
  const up = uptime(c);
  return (
    <div className={`ct-card ucard ${isRunning(c) ? '' : 'off'}`} role="button" tabIndex={0} onClick={onOpen} onKeyDown={(e) => e.key === 'Enter' && onOpen()}>
      <div className="ct-card-head">
        <StateDot state={c.state} />
        <b className="ct-card-name ellipsis" title={c.name}>{c.name}</b>
        <QuickActions c={c} busy={busy} onAct={onAct} />
      </div>
      <div className="ct-card-image mono ellipsis" title={c.image}>{c.image}</div>
      <div className="ct-badges">
        <span className={`chip tiny ${stateTone(c.state)}`}>{c.state}</span>
        <Badges c={c} />
      </div>
      {live ? (
        <div className="ct-metrics">
          <div className="ct-metric">
            <span className="ct-metric-head">
              <span>CPU</span>
              <b>{fmtPct(s.cpu_percent, 1)}</b>
            </span>
            <Sparkline values={hist?.cpu ?? []} height={26} className="accent" />
          </div>
          <div className="ct-metric">
            <span className="ct-metric-head">
              <span>Memory</span>
              <b>{fmtPct(s.mem_percent, 0)}</b>
            </span>
            <Meter pct={s.mem_percent} level={s.mem_percent >= 90 ? 'bad' : s.mem_percent >= 75 ? 'warn' : undefined} />
            <span className="ct-sub">{memText(s)}</span>
          </div>
          <div className="ct-io">
            <span title="Network in / out">
              <Icon name="network" size={12} /> ↓ {fmtRate(s.net_rx_rate)} · ↑ {fmtRate(s.net_tx_rate)}
            </span>
            <span title="Disk read / write">
              <Icon name="disk" size={12} /> R {fmtRate(s.block_read_rate ?? 0)} · W {fmtRate(s.block_write_rate ?? 0)}
            </span>
          </div>
        </div>
      ) : (
        <div className="ct-metrics idle muted small">{isRunning(c) ? 'Reading usage…' : c.status}</div>
      )}
      <div className="ct-card-foot">
        <PortLinks c={c} max={3} />
        <span className="spacer" />
        {up && <span className="muted small" title="Uptime"><Icon name="clock" size={12} /> {up}</span>}
        {(c.restart_count ?? 0) > 0 && <span className="chip tiny warn" title="Restarts">↻ {c.restart_count}</span>}
      </div>
    </div>
  );
}

function ContainerTable({ list, stats, busy, onOpen, onAct }: {
  list: Container[]; stats: Record<string, ContainerStats>; busy: string | null; onOpen: (id: string) => void; onAct: (c: Container, a: ContainerAction) => void;
}) {
  return (
    <div className="ucard ct-table-wrap">
      <table className="table hover ct-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>CPU</th>
            <th>Memory</th>
            <th>Network ↓/↑</th>
            <th>Disk R/W</th>
            <th>Uptime</th>
            <th>Ports</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {list.map((c) => {
            const s = isRunning(c) ? stats[c.id] : undefined;
            return (
              <tr key={c.id} onClick={() => onOpen(c.id)}>
                <td>
                  <span className="ct-name-cell">
                    <StateDot state={c.state} />
                    <b className="ellipsis">{c.name}</b>
                    <Badges c={c} />
                  </span>
                  <div className="muted small mono ellipsis ct-img-cell">{c.image}</div>
                </td>
                <td className="num">{s ? fmtPct(s.cpu_percent, 1) : '–'}</td>
                <td className="num">
                  {s ? (
                    <>
                      {fmtBytes(s.mem_usage)}
                      <Meter pct={s.mem_percent} level={s.mem_percent >= 90 ? 'bad' : s.mem_percent >= 75 ? 'warn' : undefined} />
                    </>
                  ) : (
                    '–'
                  )}
                </td>
                <td className="num small">{s ? `${fmtRate(s.net_rx_rate)} / ${fmtRate(s.net_tx_rate)}` : '–'}</td>
                <td className="num small">{s ? `${fmtRate(s.block_read_rate ?? 0)} / ${fmtRate(s.block_write_rate ?? 0)}` : '–'}</td>
                <td className="small">{uptime(c) ?? <span className="muted">{c.state}</span>}{(c.restart_count ?? 0) > 0 && <span className="chip tiny warn">↻ {c.restart_count}</span>}</td>
                <td className="small"><PortLinks c={c} max={2} /></td>
                <td className="row-actions">
                  <QuickActions c={c} busy={busy === c.id} onAct={onAct} />
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

// ---------------- one container ----------------

type PageTab = 'overview' | 'logs' | 'inspect';

function ContainerPage({ c, stats, hist, busy, onBack, onAct, onEdit, onChanged, onGone, onQuiet }: {
  c: Container;
  stats?: ContainerStats;
  hist?: History;
  busy: boolean;
  onBack: () => void;
  onAct: (c: Container, a: ContainerAction) => void;
  onEdit: () => void;
  onChanged: () => Promise<void>;
  onGone: () => void;
  onQuiet: (name: string) => void;
}) {
  const [tab, setTab] = useState<PageTab>('overview');
  const [detail, setDetail] = useState<ContainerDetail | null>(null);
  const [updating, setUpdating] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [withVolumes, setWithVolumes] = useState(false);

  const loadDetail = useCallback(async () => {
    const r = await api<ContainerDetail>(`/api/v1/containers/${encodeURIComponent(c.id)}`);
    if (r.ok) setDetail(r.data);
  }, [c.id]);
  useEffect(() => {
    void loadDetail();
  }, [loadDetail, c.state]);

  const app = c.labels?.['nocapos.app'];
  const link = containerLink({ web_url: detail?.web_url ?? c.web_url, ports: c.ports });
  const locked = c.system;
  const running = isRunning(c);
  const removable = !locked && !app && !c.project;

  const update = async () => {
    const ok = await confirmDialog({
      title: `Update ${c.name}?`,
      message: `NoCapOS downloads ${c.image} again. If there's a newer image, the container is recreated with it, keeping its settings and data.`,
      confirmLabel: 'Update',
    });
    if (!ok) return;
    setUpdating(true);
    onQuiet(c.name);
    const r = await dockerApi.update(c.id);
    setUpdating(false);
    if (!r.ok) return toast('error', `Couldn’t update ${c.name}`, r.error);
    if (!r.data.updated) toast('success', `${c.name} is up to date`);
    else toast(r.data.warning ? 'error' : 'success', `${c.name} updated`, r.data.warning);
    if (r.data.updated && r.data.id !== c.id) onGone();
    else void onChanged();
  };

  const actions: { label: string; icon: IconName; onClick: () => void; disabled?: boolean; danger?: boolean; title?: string; hidden?: boolean }[] = [
    { label: 'Start', icon: 'play', onClick: () => onAct(c, 'start'), hidden: running || c.state === 'paused' },
    { label: 'Resume', icon: 'play', onClick: () => onAct(c, 'unpause'), hidden: c.state !== 'paused' },
    { label: 'Restart', icon: 'restart', onClick: () => onAct(c, 'restart'), hidden: !running, disabled: locked },
    { label: 'Pause', icon: 'pause', onClick: () => onAct(c, 'pause'), hidden: !running, disabled: locked },
    { label: 'Stop', icon: 'stop', onClick: () => onAct(c, 'stop'), hidden: !running && c.state !== 'paused', disabled: locked },
    { label: 'Kill', icon: 'alert', onClick: () => onAct(c, 'kill'), hidden: !running && c.state !== 'paused', disabled: locked, danger: true },
  ];

  return (
    <div className="ct-page">
      <button type="button" className="ghost small pill ct-back" onClick={onBack}>
        <Icon name="chevronLeft" size={14} /> All containers
      </button>
      <header className="ct-page-head">
        <span className={`ct-hero ${stateTone(c.state)}`}>
          <Icon name="containers" size={26} />
        </span>
        <div className="ct-page-title">
          <h2 className="ellipsis">{c.name}</h2>
          <span className="mono small muted ellipsis">{c.image}</span>
          <span className="ct-badges">
            <span className={`chip tiny ${stateTone(c.state)}`}>{c.state}</span>
            <Badges c={c} />
            {detail?.privileged && <span className="chip tiny warn">privileged</span>}
            {locked && <span className="chip tiny" title="NoCapOS’s own container: it can’t be stopped, edited or removed here"><Icon name="shield" size={11} /> protected</span>}
          </span>
        </div>
        <div className="ct-page-actions">
          {link && running && (
            <a className="button solid" href={link} target="_blank" rel="noopener noreferrer" title={link}>
              <Icon name="external" size={14} /> Open
            </a>
          )}
          {running && (
            <button type="button" className="ghost" onClick={() => openTerminal(c)}>
              <Icon name="terminal" size={14} /> Terminal
            </button>
          )}
        </div>
      </header>

      <div className="ct-actionbar">
        {actions.filter((a) => !a.hidden).map((a) => (
          <button key={a.label} type="button" className={`ghost small ${a.danger ? 'danger' : ''}`} disabled={busy || a.disabled} title={a.disabled ? 'NoCapOS’s own containers are protected' : undefined} onClick={a.onClick}>
            <Icon name={a.icon} size={14} /> {a.label}
          </button>
        ))}
        <span className="spacer" />
        {!locked && (
          <button type="button" className="ghost small" disabled={!!c.project} title={c.project ? `Part of the stack ${c.project}: edit its compose file` : undefined} onClick={onEdit}>
            <Icon name="pencil" size={14} /> Edit
          </button>
        )}
        {!locked && (
          <button type="button" className="ghost small" disabled={updating || !!c.project} title={c.project ? 'Update the stack instead' : 'Download the image again and recreate if it changed'} onClick={() => void update()}>
            <Icon name="download" size={14} /> {updating ? 'Updating…' : 'Update image'}
          </button>
        )}
        {removable && (
          <button type="button" className="ghost small danger" onClick={() => setRemoving(true)}>
            <Icon name="trash" size={14} /> Remove
          </button>
        )}
      </div>

      <div className="ct-tabs" role="tablist">
        {(['overview', 'logs', 'inspect'] as const).map((t) => (
          <button key={t} type="button" role="tab" aria-selected={tab === t} className={tab === t ? 'on' : ''} onClick={() => setTab(t)}>
            {t === 'overview' ? 'Overview' : t === 'logs' ? 'Logs' : 'Inspect'}
          </button>
        ))}
      </div>

      {tab === 'overview' && <Overview c={c} s={stats} hist={hist} detail={detail} onChanged={() => { void loadDetail(); void onChanged(); }} />}
      {tab === 'logs' && (
        <div className="ucard ct-logs">
          <LogView id={c.id} />
        </div>
      )}
      {tab === 'inspect' && <Inspect c={c} detail={detail} />}

      {removing && (
        <TypeConfirm
          title={`Remove ${c.name}?`}
          word={c.name}
          action={withVolumes ? 'Remove with volumes' : 'Remove'}
          message={
            <>
              <b>The container is deleted.</b>
              <p>Folders from the server that it used are never touched.</p>
              <label className="ct-check">
                <input type="checkbox" checked={withVolumes} onChange={(e) => setWithVolumes(e.target.checked)} />
                <span>Also delete its volumes (data stored only in Docker volumes is lost for good)</span>
              </label>
            </>
          }
          onClose={() => setRemoving(false)}
          onConfirm={async () => {
            onQuiet(c.name);
            const r = withVolumes ? await dockerApi.removeWithVolumes(c.id) : await dockerApi.remove(c.id);
            if (!r.ok) return r.error ?? 'Couldn’t remove the container';
            const vols = withVolumes && r.data && typeof r.data === 'object' && 'volumes' in r.data ? r.data.volumes : [];
            toast('success', `${c.name} removed`, vols.length ? `Deleted volumes: ${vols.join(', ')}` : undefined);
            setRemoving(false);
            onGone();
            return null;
          }}
        />
      )}
    </div>
  );
}

function Tile({ icon, label, value, sub, children }: { icon: IconName; label: string; value: string; sub?: string; children?: ReactNode }) {
  return (
    <div className="ucard ct-tile">
      <span className="ct-tile-head">
        <Icon name={icon} size={14} /> {label}
      </span>
      <b className="ct-tile-value">{value}</b>
      {sub && <span className="ct-sub">{sub}</span>}
      {children}
    </div>
  );
}

function Overview({ c, s, hist, detail, onChanged }: { c: Container; s?: ContainerStats; hist?: History; detail: ContainerDetail | null; onChanged: () => void }) {
  const running = isRunning(c);
  const up = uptime(c);
  return (
    <div className="ct-overview">
      {running && (
        <div className="ct-tiles">
          <Tile icon="cpu" label="CPU" value={s ? fmtPct(s.cpu_percent, 1) : '–'} sub={s ? `${s.pids} processes` : undefined}>
            <Sparkline values={hist?.cpu ?? []} height={40} className="accent" />
          </Tile>
          <Tile icon="memory" label="Memory" value={s ? fmtBytes(s.mem_usage) : '–'} sub={s ? `of ${fmtBytes(s.mem_limit)} (${fmtPct(s.mem_percent)})` : undefined}>
            <Sparkline values={hist?.mem ?? []} height={40} className="accent" />
          </Tile>
          <Tile icon="network" label="Network" value={s ? `↓ ${fmtRate(s.net_rx_rate)}` : '–'} sub={s ? `↑ ${fmtRate(s.net_tx_rate)}` : undefined}>
            <Sparkline values={hist?.net ?? []} height={40} className="accent" />
          </Tile>
          <Tile icon="disk" label="Disk" value={s ? `R ${fmtRate(s.block_read_rate ?? 0)}` : '–'} sub={s ? `W ${fmtRate(s.block_write_rate ?? 0)}` : undefined}>
            <Sparkline values={hist?.disk ?? []} height={40} className="accent" />
          </Tile>
        </div>
      )}
      <div className="ct-two">
        <div className="ucard pad">
          <span className="section-label">Details</span>
          <dl className="kv small">
            <dt>Status</dt>
            <dd>{c.status}</dd>
            {up && (
              <>
                <dt>Uptime</dt>
                <dd>{up}</dd>
              </>
            )}
            <dt>Created</dt>
            <dd>{fmtAgo(c.created)}</dd>
            {detail && (
              <>
                <dt>Restart policy</dt>
                <dd>{detail.restart_policy || 'no'}</dd>
                <dt>Restarts</dt>
                <dd>{detail.restart_count}</dd>
                {!running && detail.finished_at && !detail.finished_at.startsWith('0001') && (
                  <>
                    <dt>Exited</dt>
                    <dd>
                      {fmtAgo(detail.finished_at)} with code {detail.exit_code}
                      {detail.oom_killed && <span className="chip tiny bad">out of memory</span>}
                    </dd>
                  </>
                )}
                {detail.health && (
                  <>
                    <dt>Health</dt>
                    <dd>{detail.health}</dd>
                  </>
                )}
              </>
            )}
            <dt>Ports</dt>
            <dd>
              <PortLinks c={c} max={12} />
            </dd>
            <dt>Networks</dt>
            <dd>
              {(detail?.networks ?? c.networks ?? []).map((n) => (
                <div key={n.name}>
                  {n.name}
                  {n.driver && n.driver !== 'bridge' && <span className="chip tiny">{n.driver}</span>}
                  {n.ip && <span className="mono muted"> {n.ip}</span>}
                </div>
              ))}
              {!(detail?.networks ?? c.networks ?? []).length && (detail?.network_mode || '–')}
            </dd>
          </dl>
        </div>
        <div className="ucard pad">
          <WebAddressField
            value={detail?.web_address ?? c.web_address ?? ''}
            automatic={(detail?.web_address ? null : containerLink({ web_url: detail?.web_url ?? c.web_url, ports: c.ports })) ?? null}
            onSave={(u) => dockerApi.setWebAddress(c.id, u)}
            onSaved={onChanged}
          />
          <p className="muted small ct-hint">
            Containers on a macvlan or ipvlan network open at their own address automatically. Set an address here when the app sits behind a reverse
            proxy or a name like https://media.home.lan.
          </p>
        </div>
      </div>
    </div>
  );
}

function Inspect({ c, detail }: { c: Container; detail: ContainerDetail | null }) {
  if (!detail) return <p className="muted">Loading…</p>;
  const labels = Object.entries(detail.labels ?? c.labels ?? {}).sort(([a], [b]) => a.localeCompare(b));
  return (
    <div className="ct-inspect">
      <section className="ucard pad">
        <span className="section-label">Container</span>
        <dl className="kv small">
          <dt>ID</dt>
          <dd className="mono">{detail.id.slice(0, 24)}</dd>
          <dt>Image</dt>
          <dd className="mono">{detail.image}</dd>
          {detail.image_id && (
            <>
              <dt>Image ID</dt>
              <dd className="mono">{detail.image_id.replace('sha256:', '').slice(0, 24)}</dd>
            </>
          )}
          <dt>Network mode</dt>
          <dd>{detail.network_mode || '–'}</dd>
          {detail.runtime && (
            <>
              <dt>Runtime</dt>
              <dd>{detail.runtime}</dd>
            </>
          )}
          <dt>Exposed ports</dt>
          <dd className="mono">{detail.exposed_ports?.join(', ') || '–'}</dd>
        </dl>
      </section>

      <section className="ucard pad">
        <span className="section-label">Networks</span>
        {detail.networks.length ? (
          <table className="table ct-mini">
            <thead>
              <tr>
                <th>Network</th>
                <th>Driver</th>
                <th>IP</th>
                <th>Gateway</th>
                <th>MAC</th>
              </tr>
            </thead>
            <tbody>
              {detail.networks.map((n) => (
                <tr key={n.name}>
                  <td>{n.name}</td>
                  <td>{n.driver || '–'}</td>
                  <td className="mono">{n.ip || '–'}</td>
                  <td className="mono">{n.gateway || '–'}</td>
                  <td className="mono">{n.mac || '–'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <p className="muted small">{detail.network_mode === 'host' ? 'Uses the server’s own network (host mode).' : 'No networks.'}</p>
        )}
      </section>

      <section className="ucard pad">
        <span className="section-label">Storage</span>
        {detail.mounts.length ? (
          <table className="table ct-mini">
            <thead>
              <tr>
                <th>Type</th>
                <th>Source</th>
                <th>In the container</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {detail.mounts.map((m, i) => (
                <tr key={i}>
                  <td>{m.type}</td>
                  <td className="mono ellipsis" title={m.source}>{m.type === 'volume' ? m.name || m.source : m.source}</td>
                  <td className="mono">{m.destination}</td>
                  <td>{!m.rw && <span className="chip tiny">read-only</span>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <p className="muted small">No volumes or folders.</p>
        )}
      </section>

      <section className="ucard pad">
        <span className="section-label">Environment</span>
        <p className="muted small">Only the names are shown: values often hold passwords. Edit the container to change them.</p>
        <div className="ct-env">
          {(detail.env_names ?? []).map((n) => (
            <code key={n}>{n}</code>
          ))}
          {!detail.env_names?.length && <span className="muted small">None</span>}
        </div>
      </section>

      {labels.length > 0 && (
        <details className="ucard pad ct-labels">
          <summary className="section-label">Labels ({labels.length})</summary>
          <dl className="kv small">
            {labels.map(([k, v]) => (
              <Fragment key={k}>
                <dt className="mono">{k}</dt>
                <dd className="mono">{v}</dd>
              </Fragment>
            ))}
          </dl>
        </details>
      )}
    </div>
  );
}
