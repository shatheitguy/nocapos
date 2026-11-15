import { useCallback, useEffect, useMemo, useRef, useState, type MouseEvent } from 'react';
import { api } from '../api/client';
import type { Container, ContainerStats, DockerEvent } from '../api/types';
import { Icon, type IconName } from '../components/Icon';
import { VirtualList } from '../components/VirtualList';
import { fmtBytes, fmtPct } from '../lib/format';
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
import { TypeConfirm } from './StorageParts';
import { ContainerPage, type History } from './ContainerPage';
import { Badges, LocalLink, PortLinks, StateDot, containerLink, isApp, isRunning, stateTone } from './ContainerBits';
import { DevicesSection, ImagesSection, VolumesSection } from './ContainerResources';

// Containers: one window with Containers (grouped by Compose project, with
// live usage, ports, local addresses and disk size), Images, Volumes,
// Networks, Devices and Stacks. Opening a container shows its own page
// (usage graphs, details, logs, a command line, its files). Admin only; the
// server enforces it, and NoCapOS's own containers stay protected.

type Tab = 'containers' | 'images' | 'volumes' | 'networks' | 'devices' | 'stacks';
type Filter = 'all' | 'running' | 'stopped' | 'apps' | 'system';
type Sort = 'name' | 'cpu' | 'ram';

const HISTORY = 60;
const STATS_EVERY = 2500;
const ROW = 62;

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

type Row = { kind: 'group'; g: Group<Container> } | { kind: 'item'; c: Container };
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
  const rows = useMemo(() => {
    const out: Row[] = [];
    const showHeads = groups.some((g) => g.project !== '');
    for (const g of groups) {
      if (showHeads) out.push({ kind: 'group', g });
      if (!showHeads || !collapsed.has(g.project || '\u0000')) for (const c of g.items) out.push({ kind: 'item', c });
    }
    return out;
  }, [groups, collapsed]);

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
      <main className={`app-content ct-main ${tab === 'containers' && !current ? 'ct-fill' : ''}`}>
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
            {rows.length === 0 ? (
              <Empty icon="containers" text={list.length ? t('ctr.noMatch') : t('ctr.none')} />
            ) : (
              <VirtualList
                className="ct-list ucard"
                label={t('ctr.nav.containers')}
                count={rows.length}
                rowHeight={ROW}
                render={(i) => {
                  const row = rows[i];
                  return row.kind === 'group' ? (
                    <GroupRow
                      key={`g:${row.g.project}`}
                      g={row.g}
                      open={!collapsed.has(row.g.project || '\u0000')}
                      stats={stats}
                      usage={usage}
                      busy={busy === `p:${row.g.project}`}
                      onToggle={() => toggleGroup(row.g.project)}
                      onAct={(a) => (a === 'remove' ? setRemovingProject(row.g.project) : void projectAct(row.g.project, a))}
                    />
                  ) : (
                    <ContainerRow
                      key={row.c.id}
                      c={row.c}
                      s={stats[row.c.id]}
                      size={usage?.containers[row.c.id]?.rw}
                      busy={busy === row.c.id}
                      nested={!!row.c.project}
                      onOpen={() => setOpenId(row.c.id)}
                      onAct={act}
                    />
                  );
                }}
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

function ContainerRow({ c, s, size, busy, nested, onOpen, onAct }: {
  c: Container; s?: ContainerStats; size?: number; busy: boolean; nested: boolean; onOpen: () => void; onAct: (c: Container, a: ContainerAction) => void;
}) {
  const t = useT();
  const live = isRunning(c) && s;
  const local = c.local_addresses?.[0];
  return (
    <div className={`ct-row ${nested ? 'nested' : ''} ${isRunning(c) ? '' : 'off'}`} role="listitem" style={{ height: ROW }}>
      <button type="button" className="ct-row-main" onClick={onOpen} aria-label={t('ctr.openContainer', { name: c.name })}>
        <StateDot state={c.state} />
        <span className="ct-row-text">
          <span className="ct-row-line">
            <b className="ellipsis">{c.name}</b>
            <Badges c={c} />
          </span>
          <span className="ct-row-line ct-row-sub">
            <span className="mono ellipsis ct-row-image" title={c.image}>{c.image}</span>
          </span>
        </span>
      </button>
      <span className="ct-row-meta">
        <PortLinks c={c} max={2} />
        {local && <LocalLink url={local} />}
      </span>
      <span className="ct-row-num" title={t('ctr.cpuMem')}>
        {live ? (
          <>
            <b>{fmtPct(s.cpu_percent, 1)}</b>
            <span>{fmtBytes(s.mem_usage)}</span>
          </>
        ) : (
          <span className="muted">{c.state}</span>
        )}
      </span>
      <span className="ct-row-size muted small" title={t('ctr.sizeHint')}>{size != null ? fmtBytes(size) : '–'}</span>
      <QuickActions c={c} busy={busy} onAct={onAct} />
    </div>
  );
}

function GroupRow({ g, open, stats, usage, busy, onToggle, onAct }: {
  g: Group<Container>; open: boolean; stats: Record<string, ContainerStats>; usage: DockerUsage | null; busy: boolean; onToggle: () => void; onAct: (a: ProjectAction) => void;
}) {
  const t = useT();
  const standalone = g.project === '';
  const cpu = sumBy(g.items, (c) => (isRunning(c) ? stats[c.id]?.cpu_percent : undefined));
  const mem = sumBy(g.items, (c) => (isRunning(c) ? stats[c.id]?.mem_usage : undefined));
  const size = usage ? sumBy(g.items, (c) => usage.containers[c.id]?.rw) : null;
  const locked = g.items.some((c) => c.system);
  return (
    <div className="ct-group" role="listitem" style={{ height: ROW }}>
      <button type="button" className="ct-group-main" onClick={onToggle} aria-expanded={open}>
        <Icon name={open ? 'chevronDown' : 'chevronRight'} size={14} />
        <span className={`ct-group-ico ${stateTone(g.state)}`}>
          <Icon name={standalone ? 'containers' : 'layers'} size={15} />
        </span>
        <span className="ct-row-text">
          <b className="ellipsis">{standalone ? t('ctr.standalone') : g.project}</b>
          <span className="muted small">{t('ctr.groupCount', { running: g.running, total: g.items.length })}</span>
        </span>
      </button>
      <span className="ct-row-num">
        {g.running > 0 && (
          <>
            <b>{fmtPct(cpu, 1)}</b>
            <span>{fmtBytes(mem)}</span>
          </>
        )}
      </span>
      <span className="ct-row-size muted small">{size != null ? fmtBytes(size) : ''}</span>
      {!standalone ? (
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
      ) : (
        <span />
      )}
    </div>
  );
}
