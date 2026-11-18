import { Fragment, useCallback, useEffect, useMemo, useRef, useState, type MouseEvent, type ReactNode } from 'react';
import { api } from '../api/client';
import type { Container, ContainerStats, DockerEvent } from '../api/types';
import { Icon, type IconName } from '../components/Icon';
import { Sparkline } from '../components/Charts';
import { fmtBytes, fmtPct, fmtRate } from '../lib/format';
import { usePoll, useTopic } from '../lib/hooks';
import { groupContainers, sumBy, type Group } from '../lib/containers';
import { resubscribe } from '../api/socket';
import { toast } from '../state/toasts';
import { tr, useT } from '../i18n';
import { Empty } from './Monitor';
import { ContainerEditor } from './ContainerEditor';
import { DockerNetworks } from './DockerNetworks';
import { Stacks } from './Stacks';
import { dockerApi, type ContainerAction, type DockerUsage, type ProjectAction } from '../api/docker';
import { confirmDialog } from '../state/confirm';
import { Meter, TypeConfirm } from './StorageParts';
import { ContainerPage, type History } from './ContainerPage';
import { Badges, LocalLink, PortLinks, StateDot, containerLink, isApp, isRunning, stateTone, uptime } from './ContainerBits';
import { DevicesSection, ImagesSection, VolumesSection } from './ContainerResources';

// Containers: one window with Containers (live cards or a list, grouped by
// Compose project, with usage, ports, local addresses and disk size), Images, Volumes,
// Networks, Devices and Stacks. Opening a container shows its own page
// (usage graphs, details, logs, a command line, its files). Admin only; the
// server enforces it, and NoCapOS's own containers stay protected.

type Tab = 'containers' | 'images' | 'volumes' | 'networks' | 'devices' | 'stacks';
type Filter = 'all' | 'running' | 'stopped' | 'apps' | 'system';
type Sort = 'name' | 'cpu' | 'ram';
type Layout = 'grid' | 'list';

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
function readCollapsed(): Set<string> {
  try {
    return new Set(JSON.parse(localStorage.getItem('nocap.ct.collapsed') ?? '[]') as string[]);
  } catch {
    return new Set();
  }
}

type Editing = { id: string | null; device?: string };

export function Containers() {
  const t = useT();
  const [list, setList] = useState<Container[] | null>(null);
  const [error, setError] = useState<{ status: number; message: string } | null>(null);
  const [tab, setTab] = useState<Tab>('containers');
  const [openId, setOpenId] = useState<string | null>(null);
  const [search, setSearch] = useState('');
  const [filter, setFilter] = useState<Filter>('all');
  const [sort, setSort] = useState<Sort>(() => readPref('nocap.ct.sort', ['name', 'cpu', 'ram'] as const, 'name'));
  const [layout, setLayout] = useState<Layout>(() => readPref('nocap.ct.layout', ['grid', 'list'] as const, 'grid'));
  const [busy, setBusy] = useState<string | null>(null);
  const [editing, setEditing] = useState<Editing | null>(null);
  const [stats, setStats] = useState<Record<string, ContainerStats>>({});
  const [usage, setUsage] = useState<DockerUsage | null>(null);
  const [collapsed, setCollapsed] = useState<Set<string>>(readCollapsed);
  const [removingProject, setRemovingProject] = useState<string | null>(null);
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
  const loadUsage = useCallback(async () => {
    const r = await dockerApi.usage();
    if (r.ok) setUsage(r.data);
  }, []);

  useEffect(() => {
    void load();
    void loadUsage();
  }, [load, loadUsage]);
  // Changes arrive as Docker events; this is only a safety net.
  usePoll(load, 60_000);
  usePoll(loadUsage, 120_000);

  useTopic<DockerEvent>(
    'docker.events',
    (ev) => {
      if (ev.type !== 'container') return;
      window.clearTimeout(reloadTimer.current);
      reloadTimer.current = window.setTimeout(() => void load(), 300);
      // 143 is a normal stop; containers being edited stop on purpose too.
      const quiet = ev.name !== undefined && (quietUntil.current.get(ev.name) ?? 0) > Date.now();
      if (ev.action === 'die' && ev.exit_code && ev.exit_code !== '0' && ev.exit_code !== '143' && ev.exit_code !== '137' && !quiet) {
        toast('error', tr('ctr.exited', { name: ev.name ?? 'Container' }), tr('ctr.exitCode', { code: ev.exit_code }));
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
      return !q || c.name.toLowerCase().includes(q) || c.image.toLowerCase().includes(q) || !!c.project?.toLowerCase().includes(q) ||
        c.ports.some((p) => String(p.public ?? '').includes(q)) || !!c.local_addresses?.some((a) => a.includes(q));
    });
    const byName = (a: Container, b: Container) => a.name.localeCompare(b.name);
    if (sort === 'cpu') out.sort((a, b) => (stats[b.id]?.cpu_percent ?? -1) - (stats[a.id]?.cpu_percent ?? -1) || byName(a, b));
    else if (sort === 'ram') out.sort((a, b) => (stats[b.id]?.mem_usage ?? -1) - (stats[a.id]?.mem_usage ?? -1) || byName(a, b));
    return out;
  }, [list, search, filter, sort, stats]);

  const groups = useMemo(() => {
    const gs = groupContainers(shown);
    if (sort !== 'name') {
      const key = (c: Container) => (sort === 'cpu' ? stats[c.id]?.cpu_percent : stats[c.id]?.mem_usage) ?? -1;
      for (const g of gs) g.items.sort((a, b) => key(b) - key(a));
    }
    return gs;
  }, [shown, sort, stats]);
  // Project headers only appear once there is a project to tell apart.
  const showHeads = groups.some((g) => g.project !== '');
  const isOpen = (g: Group<Container>) => !showHeads || !collapsed.has(g.project || '\u0000');

  const toggleGroup = (p: string) => {
    setCollapsed((cur) => {
      const next = new Set(cur);
      const k = p || '\u0000';
      if (next.has(k)) next.delete(k);
      else next.add(k);
      writePref('nocap.ct.collapsed', JSON.stringify([...next]));
      return next;
    });
  };

  const act = async (c: Container, action: ContainerAction) => {
    if (action === 'kill') {
      const ok = await confirmDialog({ title: tr('ctr.killTitle', { name: c.name }), message: tr('ctr.killMsg'), confirmLabel: tr('ctr.act.kill'), danger: true });
      if (!ok) return;
    }
    setBusy(c.id);
    const r = await dockerApi.action(c.id, action);
    setBusy(null);
    if (r.ok) toast('success', tr('ctr.actDone', { name: c.name, action: tr(`ctr.act.${action}`) }));
    else toast('error', tr('ctr.actFail', { name: c.name, action: tr(`ctr.act.${action}`) }), r.error);
    void load();
  };

  const projectAct = async (project: string, action: ProjectAction) => {
    if (action === 'stop') {
      const ok = await confirmDialog({ title: tr('ctr.proj.stopTitle', { name: project }), message: tr('ctr.proj.stopMsg'), confirmLabel: tr('ctr.act.stop'), danger: true });
      if (!ok) return null;
    }
    setBusy(`p:${project}`);
    for (const c of list ?? []) if (c.project === project) quietUntil.current.set(c.name, Date.now() + 120_000);
    const r = await dockerApi.project(project, action);
    setBusy(null);
    if (r.ok) toast('success', r.data?.stack ? tr('ctr.proj.stackStarted', { name: project }) : tr('ctr.proj.done', { name: project, action: tr(`ctr.act.${action}`) }));
    else toast('error', tr('ctr.proj.fail', { name: project }), r.error);
    void load();
    return r.ok ? null : (r.error ?? '');
  };

  const groupProps = (g: Group<Container>) => ({
    g,
    open: isOpen(g),
    stats,
    usage,
    busy: busy === `p:${g.project}`,
    onToggle: () => toggleGroup(g.project),
    onAct: (a: ProjectAction) => (a === 'remove' ? setRemovingProject(g.project) : void projectAct(g.project, a)),
  });

  if (error) {
    const offline = error.status === 503 || error.status === 0;
    return (
      <Empty icon="containers" text={offline ? t('ctr.offline') : error.message}>
        <button type="button" onClick={() => void load()}>
          <Icon name="restart" size={15} /> {t('ctr.retry')}
        </button>
      </Empty>
    );
  }
  if (!list) return <Empty icon="containers" text={t('ctr.loading')} />;

  if (editing) {
    return (
      <div className="containers">
        <ContainerEditor
          containerId={editing.id}
          addDevice={editing.device}
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

  const nav: { id: Tab; icon: IconName; count?: number }[] = [
    { id: 'containers', icon: 'containers', count: list.length },
    { id: 'images', icon: 'archiveBox' },
    { id: 'volumes', icon: 'database' },
    { id: 'networks', icon: 'network' },
    { id: 'devices', icon: 'usb' },
    { id: 'stacks', icon: 'layers' },
  ];
  const current = openId ? list.find((c) => c.id === openId) ?? null : null;
  const names = list.map((c) => c.name).sort();

  return (
    <div className="ct-app app-split">
      <nav className="sidebar ct-side" aria-label={t('ctr.title')}>
        <div className="ct-side-head">
          <h1 className="app-title">{t('ctr.title')}</h1>
          <span className="app-subtitle">{t('ctr.summary', { running: counts.running, total: counts.all })}</span>
        </div>
        {nav.map((n) => (
          <button key={n.id} type="button" className={tab === n.id ? 'on' : ''} title={t(`ctr.nav.${n.id}`)} onClick={() => { setTab(n.id); setOpenId(null); }}>
            <Icon name={n.icon} size={16} />
            <span className="ct-nav-label">{t(`ctr.nav.${n.id}`)}</span>
            {n.count != null && n.count > 0 && <span className="ct-count">{n.count}</span>}
          </button>
        ))}
      </nav>
      <main className="app-content ct-main">
        {tab === 'stacks' && <Stacks />}
        {tab === 'networks' && <DockerNetworks containers={names} />}
        {tab === 'images' && <ImagesSection usage={usage} />}
        {tab === 'volumes' && <VolumesSection />}
        {tab === 'devices' && <DevicesSection containers={list} onAttach={(id, device) => setEditing({ id, device })} />}
        {tab === 'containers' && current && (
          <ContainerPage
            key={current.id}
            c={current}
            stats={stats[current.id]}
            hist={history.current.get(current.id)}
            size={usage?.containers[current.id]}
            busy={busy === current.id}
            onBack={() => setOpenId(null)}
            onAct={act}
            onEdit={() => setEditing({ id: current.id })}
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
                <input placeholder={t('ctr.search')} aria-label={t('ctr.search')} value={search} onChange={(e) => setSearch(e.target.value)} />
              </label>
              <select
                className="ct-select"
                value={sort}
                aria-label={t('ctr.sortBy')}
                onChange={(e) => {
                  const v = e.target.value as Sort;
                  setSort(v);
                  writePref('nocap.ct.sort', v);
                }}
              >
                <option value="name">{t('ctr.sort.name')}</option>
                <option value="cpu">{t('ctr.sort.cpu')}</option>
                <option value="ram">{t('ctr.sort.ram')}</option>
              </select>
              <div className="ct-seg" role="group" aria-label={t('ctr.layout')}>
                {(['grid', 'list'] as const).map((l) => (
                  <button
                    key={l}
                    type="button"
                    className={layout === l ? 'on' : ''}
                    title={t(`ctr.layout.${l}`)}
                    aria-label={t(`ctr.layout.${l}`)}
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
              <button type="button" className="ghost icon-btn" aria-label={t('ctr.refresh')} title={t('ctr.refresh')} onClick={() => { void load(); void loadUsage(); }}>
                <Icon name="restart" size={15} />
              </button>
              <button type="button" onClick={() => setEditing({ id: null })}>
                <Icon name="plus" size={14} /> <span className="ct-hide-narrow">{t('ctr.add')}</span>
              </button>
            </div>
            <div className="ct-filters" role="tablist">
              {(['all', 'running', 'stopped', 'apps', 'system'] as const).map((id) => (
                <button key={id} type="button" role="tab" aria-selected={filter === id} className={`ct-filter ${filter === id ? 'on' : ''}`} onClick={() => setFilter(id)}>
                  {t(`ctr.filter.${id}`)} <span>{counts[id]}</span>
                </button>
              ))}
            </div>
            {shown.length === 0 ? (
              <Empty icon="containers" text={list.length ? t('ctr.noMatch') : t('ctr.none')} />
            ) : layout === 'grid' ? (
              <div className="ct-groups">
                {groups.map((g) => {
                  const open = isOpen(g);
                  return (
                    <section key={`g:${g.project}`} className="ct-groupblock" aria-label={g.project || t('ctr.standalone')}>
                      {showHeads && <GroupHead {...groupProps(g)} />}
                      {open && (
                        <div className="ct-grid">
                          {g.items.map((c) => (
                            <ContainerCard
                              key={c.id}
                              c={c}
                              s={stats[c.id]}
                              hist={history.current.get(c.id)}
                              size={usage?.containers[c.id]?.rw}
                              busy={busy === c.id}
                              onOpen={() => setOpenId(c.id)}
                              onAct={act}
                            />
                          ))}
                        </div>
                      )}
                    </section>
                  );
                })}
              </div>
            ) : (
              <ContainerTable
                groups={groups}
                showHeads={showHeads}
                isOpen={isOpen}
                head={(g) => <GroupHead {...groupProps(g)} />}
                stats={stats}
                usage={usage}
                busy={busy}
                onOpen={setOpenId}
                onAct={act}
              />
            )}
          </>
        )}
        {removingProject && (
          <TypeConfirm
            title={t('ctr.proj.removeTitle', { name: removingProject })}
            word={removingProject}
            action={t('ctr.act.remove')}
            message={<p>{t('ctr.proj.removeMsg')}</p>}
            onClose={() => setRemovingProject(null)}
            onConfirm={async () => {
              const err = await projectAct(removingProject, 'remove');
              if (err === null) setRemovingProject(null);
              return err;
            }}
          />
        )}
      </main>
    </div>
  );
}

// ---------------- rows ----------------

function QuickActions({ c, busy, onAct }: { c: Container; busy: boolean; onAct: (c: Container, a: ContainerAction) => void }) {
  const t = useT();
  const link = containerLink(c);
  const stop = (e: MouseEvent) => e.stopPropagation();
  return (
    <div className="btn-group ct-quick" onClick={stop}>
      {link && isRunning(c) && (
        <a className="button ghost icon-btn ct-hide-narrow" href={link} target="_blank" rel="noopener noreferrer" title={t('ctr.openUrl', { url: link })}>
          <Icon name="external" size={15} />
        </a>
      )}
      {c.state === 'paused' ? (
        <button type="button" className="ghost icon-btn" title={t('ctr.act.unpause')} aria-label={t('ctr.act.unpause')} disabled={busy} onClick={() => onAct(c, 'unpause')}>
          <Icon name="play" size={15} />
        </button>
      ) : isRunning(c) ? (
        <>
          <button type="button" className="ghost icon-btn ct-hide-narrow" title={t('ctr.act.restart')} aria-label={t('ctr.act.restart')} disabled={busy || c.system} onClick={() => onAct(c, 'restart')}>
            <Icon name="restart" size={15} />
          </button>
          <button type="button" className="ghost icon-btn danger" title={c.system ? t('ctr.protected') : t('ctr.act.stop')} aria-label={t('ctr.act.stop')} disabled={busy || c.system} onClick={() => onAct(c, 'stop')}>
            <Icon name="stop" size={15} />
          </button>
        </>
      ) : (
        <button type="button" className="ghost icon-btn" title={t('ctr.act.start')} aria-label={t('ctr.act.start')} disabled={busy} onClick={() => onAct(c, 'start')}>
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

const memLevel = (pct: number) => (pct >= 90 ? 'bad' : pct >= 75 ? 'warn' : undefined);

/** One container as a live card: state, image, usage, then ports, local address, size and uptime. */
function ContainerCard({ c, s, hist, size, busy, onOpen, onAct }: {
  c: Container; s?: ContainerStats; hist?: History; size?: number; busy: boolean; onOpen: () => void; onAct: (c: Container, a: ContainerAction) => void;
}) {
  const t = useT();
  const live = isRunning(c) && s;
  const up = uptime(c);
  const local = c.local_addresses?.[0];
  return (
    <div
      className={`ct-card ucard ${isRunning(c) ? '' : 'off'}`}
      role="button"
      tabIndex={0}
      aria-label={t('ctr.openContainer', { name: c.name })}
      onClick={onOpen}
      onKeyDown={(e) => e.key === 'Enter' && e.target === e.currentTarget && onOpen()}
    >
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
              <span>{t('ctr.cpu')}</span>
              <b>{fmtPct(s.cpu_percent, 1)}</b>
            </span>
            <Sparkline values={hist?.cpu ?? []} height={26} className="accent" />
          </div>
          <div className="ct-metric">
            <span className="ct-metric-head">
              <span>{t('ctr.memory')}</span>
              <b>{fmtPct(s.mem_percent, 0)}</b>
            </span>
            <Meter pct={s.mem_percent} level={memLevel(s.mem_percent)} />
            <span className="ct-sub">{memText(s)}</span>
          </div>
          <div className="ct-io">
            <span title={t('ctr.netIO')}>
              <Icon name="network" size={12} /> ↓ {fmtRate(s.net_rx_rate)} · ↑ {fmtRate(s.net_tx_rate)}
            </span>
            <span title={t('ctr.diskIO')}>
              <Icon name="disk" size={12} /> R {fmtRate(s.block_read_rate ?? 0)} · W {fmtRate(s.block_write_rate ?? 0)}
            </span>
          </div>
        </div>
      ) : (
        <div className="ct-metrics idle muted small">{isRunning(c) ? t('ctr.readingUsage') : c.status}</div>
      )}
      <div className="ct-card-foot">
        <PortLinks c={c} max={3} />
        {local && <LocalLink url={local} />}
        <span className="spacer" />
        {size != null && (
          <span className="muted small" title={t('ctr.sizeHint')}>
            <Icon name="database" size={12} /> {fmtBytes(size)}
          </span>
        )}
        {up && (
          <span className="muted small" title={t('ctr.uptime')}>
            <Icon name="clock" size={12} /> {up}
          </span>
        )}
        {(c.restart_count ?? 0) > 0 && <span className="chip tiny warn" title={t('ctr.restarts')}>↻ {c.restart_count}</span>}
      </div>
    </div>
  );
}

/** The list layout: one table, a project header row above each project's containers. */
function ContainerTable({ groups, showHeads, isOpen, head, stats, usage, busy, onOpen, onAct }: {
  groups: Group<Container>[];
  showHeads: boolean;
  isOpen: (g: Group<Container>) => boolean;
  head: (g: Group<Container>) => ReactNode;
  stats: Record<string, ContainerStats>;
  usage: DockerUsage | null;
  busy: string | null;
  onOpen: (id: string) => void;
  onAct: (c: Container, a: ContainerAction) => void;
}) {
  const t = useT();
  return (
    <div className="ucard ct-table-wrap">
      <table className="table hover ct-table">
        <thead>
          <tr>
            <th>{t('ctr.name')}</th>
            <th>{t('ctr.cpu')}</th>
            <th>{t('ctr.memory')}</th>
            <th>{t('ctr.col.net')}</th>
            <th>{t('ctr.col.disk')}</th>
            <th>{t('ctr.uptime')}</th>
            <th>{t('ctr.ports')}</th>
            <th title={t('ctr.sizeHint')}>{t('ctr.size')}</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {groups.map((g) => (
            <Fragment key={`g:${g.project}`}>
              {showHeads && (
                <tr className="ct-group-tr">
                  <td colSpan={9}>{head(g)}</td>
                </tr>
              )}
              {isOpen(g) &&
                g.items.map((c) => {
                  const s = isRunning(c) ? stats[c.id] : undefined;
                  const size = usage?.containers[c.id]?.rw;
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
                            <Meter pct={s.mem_percent} level={memLevel(s.mem_percent)} />
                          </>
                        ) : (
                          '–'
                        )}
                      </td>
                      <td className="num small">{s ? `${fmtRate(s.net_rx_rate)} / ${fmtRate(s.net_tx_rate)}` : '–'}</td>
                      <td className="num small">{s ? `${fmtRate(s.block_read_rate ?? 0)} / ${fmtRate(s.block_write_rate ?? 0)}` : '–'}</td>
                      <td className="small">
                        {uptime(c) ?? <span className="muted">{c.state}</span>}
                        {(c.restart_count ?? 0) > 0 && <span className="chip tiny warn">↻ {c.restart_count}</span>}
                      </td>
                      <td className="small">
                        <span className="ct-port-cell">
                          <PortLinks c={c} max={2} />
                          {c.local_addresses?.[0] && <LocalLink url={c.local_addresses[0]} />}
                        </span>
                      </td>
                      <td className="num small muted">{size != null ? fmtBytes(size) : '–'}</td>
                      <td className="row-actions">
                        <QuickActions c={c} busy={busy === c.id} onAct={onAct} />
                      </td>
                    </tr>
                  );
                })}
            </Fragment>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** A project's header row: open/close, how many run, its total usage and size, and actions for all of it. */
function GroupHead({ g, open, stats, usage, busy, onToggle, onAct }: {
  g: Group<Container>; open: boolean; stats: Record<string, ContainerStats>; usage: DockerUsage | null; busy: boolean; onToggle: () => void; onAct: (a: ProjectAction) => void;
}) {
  const t = useT();
  const standalone = g.project === '';
  const cpu = sumBy(g.items, (c) => (isRunning(c) ? stats[c.id]?.cpu_percent : undefined));
  const mem = sumBy(g.items, (c) => (isRunning(c) ? stats[c.id]?.mem_usage : undefined));
  const size = usage ? sumBy(g.items, (c) => usage.containers[c.id]?.rw) : null;
  const locked = g.items.some((c) => c.system);
  return (
    <div className="ct-group-head">
      <button type="button" className="ct-group-toggle" onClick={onToggle} aria-expanded={open}>
        <Icon name={open ? 'chevronDown' : 'chevronRight'} size={14} />
        <span className={`ct-group-ico ${stateTone(g.state)}`}>
          <Icon name={standalone ? 'containers' : 'layers'} size={15} />
        </span>
        <b className="ellipsis">{standalone ? t('ctr.standalone') : g.project}</b>
        <span className="muted small ct-group-count">{t('ctr.groupCount', { running: g.running, total: g.items.length })}</span>
      </button>
      <span className="ct-group-usage muted small">
        {g.running > 0 && (
          <span title={t('ctr.cpuMem')}>
            {fmtPct(cpu, 1)} · {fmtBytes(mem)}
          </span>
        )}
        {size != null && (
          <span title={t('ctr.sizeHint')}>
            <Icon name="database" size={12} /> {fmtBytes(size)}
          </span>
        )}
      </span>
      {!standalone && (
        <div className="btn-group ct-quick">
          {g.state !== 'running' && (
            <button type="button" className="ghost icon-btn" title={t('ctr.proj.start')} aria-label={t('ctr.proj.start')} disabled={busy} onClick={() => onAct('start')}>
              <Icon name="play" size={15} />
            </button>
          )}
          {g.running > 0 && (
            <button type="button" className="ghost icon-btn ct-hide-narrow" title={t('ctr.proj.restart')} aria-label={t('ctr.proj.restart')} disabled={busy || locked} onClick={() => onAct('restart')}>
              <Icon name="restart" size={15} />
            </button>
          )}
          {g.running > 0 && (
            <button type="button" className="ghost icon-btn danger" title={t('ctr.proj.stop')} aria-label={t('ctr.proj.stop')} disabled={busy || locked} onClick={() => onAct('stop')}>
              <Icon name="stop" size={15} />
            </button>
          )}
          <button type="button" className="ghost icon-btn danger" title={t('ctr.proj.remove')} aria-label={t('ctr.proj.remove')} disabled={busy || locked} onClick={() => onAct('remove')}>
            <Icon name="trash" size={15} />
          </button>
        </div>
      )}
    </div>
  );
}
