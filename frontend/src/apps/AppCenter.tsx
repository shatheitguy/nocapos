import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { appURL, openStoreApp, storeApi, type StoreApp } from '../api/appstore';
import { Icon, type IconName } from '../components/Icon';
import { choiceDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import type { WinState } from '../state/windows';
import { ContainerEditor } from './ContainerEditor';
import { openApp } from './meta';
import { Empty } from './Monitor';

// App Store: discover, install, open, update and remove self-hosted apps.
// Each app runs as Docker containers with its own data volumes.

type Tab = 'discover' | 'installed' | 'updates';
/** A list page: one category, every app ('*'), or nothing (the Discover home). */
type ListPage = string | null;

const DOCKER_DOWN = "Docker isn't running, so apps can't be installed or changed right now.";

const CATEGORY: Record<string, { label: string; icon: IconName }> = {
  AI: { label: 'AI', icon: 'sparkles' },
  Cloud: { label: 'Files & sync', icon: 'files' },
  Developer: { label: 'Developer tools', icon: 'fileCode' },
  Media: { label: 'Media', icon: 'play' },
  Network: { label: 'Networking', icon: 'network' },
  Productivity: { label: 'Productivity', icon: 'edit' },
  Security: { label: 'Security', icon: 'shield' },
};
const catLabel = (c: string) => CATEGORY[c]?.label ?? c;
const catIcon = (c: string): IconName => CATEGORY[c]?.icon ?? 'grid';

const tileBg = (a: StoreApp, angle = 145) => `linear-gradient(${angle}deg, ${a.tile[0]}, ${a.tile[1]})`;

/** Date the app first appeared in the store (its oldest release). */
const addedAt = (a: StoreApp) => Date.parse(a.releases[a.releases.length - 1]?.date ?? '') || 0;

type Act = 'install' | 'update' | 'start' | 'stop' | 'restart';

async function runAction(app: StoreApp, action: Act, after: () => void) {
  const r = await storeApi.act(app.id, action);
  if (!r.ok) toast('error', `Could not ${action} ${app.name}`, r.error);
  after();
}

async function uninstallApp(app: StoreApp, after: () => void) {
  const pick = await choiceDialog({
    title: `Uninstall ${app.name}?`,
    message:
      'Its containers are removed. Keep the data and a reinstall picks up where you left off, or delete it for good (settings, accounts and files stored inside the app).',
    choices: [
      { id: 'delete', label: 'Delete data too', danger: true },
      { id: 'keep', label: 'Uninstall, keep data' },
    ],
  });
  if (!pick) return;
  const r = await storeApi.uninstall(app.id, pick === 'delete');
  if (!r.ok) toast('error', `Could not uninstall ${app.name}`, r.error);
  after();
}

export function AppCenter({ win }: { win?: WinState }) {
  const [apps, setApps] = useState<StoreApp[] | null>(null);
  const [docker, setDocker] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>('discover');
  const [list, setList] = useState<ListPage>(null);
  const [q, setQ] = useState('');
  const [detail, setDetail] = useState<string | null>(win?.props?.app ?? null);
  // Opened again at a specific app (e.g. from search): show that app.
  const wanted = win?.props?.app;
  useEffect(() => {
    if (wanted) setDetail(wanted);
  }, [wanted]);
  const timer = useRef<number | undefined>(undefined);
  const scroller = useRef<HTMLDivElement>(null);

  const load = useCallback(async () => {
    const r = await storeApi.list();
    if (!r.ok) {
      setError(r.status === 403 ? 'Only administrators can manage apps.' : (r.error ?? 'Could not load the App Store'));
      return;
    }
    setError(null);
    setApps(r.data.apps);
    setDocker(r.data.docker);
  }, []);
  const reload = useCallback(() => void load(), [load]);

  // Poll faster while something is installing / updating / uninstalling.
  const busy = !!apps?.some((a) => a.job && !a.job.done);
  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => {
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => void load(), busy ? 1200 : 10_000);
    return () => window.clearTimeout(timer.current);
  }, [apps, busy, load]);

  // Tell the user when a job they watched finishes, wherever they are in the store.
  const watching = useRef(new Set<string>());
  useEffect(() => {
    for (const a of apps ?? []) {
      const j = a.job;
      if (!j) continue;
      if (!j.done) watching.current.add(j.id);
      else if (watching.current.delete(j.id)) {
        if (j.error) toast('error', `${a.name}: ${j.action} failed`, j.error);
        else toast('success', `${a.name} ${j.action === 'install' ? 'installed' : j.action === 'update' ? 'updated' : 'removed'}`);
      }
    }
  }, [apps]);

  // New page: start at the top.
  useLayoutEffect(() => {
    scroller.current?.scrollTo(0, 0);
  }, [tab, list, detail]);

  const categories = useMemo(() => Array.from(new Set((apps ?? []).map((a) => a.category))).sort((a, b) => catLabel(a).localeCompare(catLabel(b))), [apps]);
  const installed = (apps ?? []).filter((a) => a.installed);
  const updates = installed.filter((a) => a.installed?.update_available);
  const results = useMemo(() => {
    const needle = q.trim().toLowerCase();
    if (!needle) return [];
    return (apps ?? []).filter((a) => `${a.name} ${a.tagline} ${a.description} ${a.category} ${catLabel(a.category)} ${a.developer}`.toLowerCase().includes(needle));
  }, [apps, q]);

  if (error) return <Empty icon="store" text={error} />;
  if (!apps) return <Empty icon="store" text="Loading the App Store…" />;

  const current = apps.find((a) => a.id === detail);
  const open = (id: string) => setDetail(id);
  const goTab = (t: Tab) => {
    setTab(t);
    setDetail(null);
    setList(null);
    setQ('');
  };

  let body: ReactNode;
  if (current) {
    body = <AppPage app={current} apps={apps} docker={docker} onBack={() => setDetail(null)} onOpen={open} onChanged={reload} />;
  } else if (q.trim()) {
    body = (
      <section className="as-section">
        <h2 className="as-h2">
          Results for “{q.trim()}” <span className="muted">{results.length}</span>
        </h2>
        {results.length ? <RowGrid apps={results} docker={docker} onOpen={open} onChanged={reload} /> : <p className="muted">No apps match your search.</p>}
      </section>
    );
  } else if (tab === 'discover' && list) {
    const shown = list === '*' ? [...apps].sort((a, b) => a.name.localeCompare(b.name)) : apps.filter((a) => a.category === list);
    body = (
      <>
        <section className="as-section">
          <button type="button" className="ghost small pill as-back" onClick={() => setList(null)}>
            <Icon name="chevronLeft" size={14} /> Discover
          </button>
          <h2 className="as-h1">{list === '*' ? 'All apps' : catLabel(list)}</h2>
          <CategoryBar categories={categories} active={list} onPick={setList} />
          <RowGrid apps={shown} docker={docker} onOpen={open} onChanged={reload} />
        </section>
      </>
    );
  } else if (tab === 'discover') {
    body = <Discover apps={apps} categories={categories} docker={docker} onOpen={open} onList={setList} onChanged={reload} />;
  } else if (tab === 'installed') {
    body = installed.length ? (
      <section className="as-section">
        <h2 className="as-h2">
          Installed <span className="muted">{installed.length}</span>
        </h2>
        <div className="as-installed">
          {installed.map((a) => (
            <InstalledCard key={a.id} app={a} docker={docker} onOpen={() => open(a.id)} onChanged={reload} />
          ))}
        </div>
      </section>
    ) : (
      <Empty icon="store" text="No apps installed yet. Find one under Discover.">
        <button type="button" className="pill" onClick={() => goTab('discover')}>
          Browse apps
        </button>
      </Empty>
    );
  } else {
    body = updates.length ? (
      <Updates updates={updates} docker={docker} onOpen={open} onChanged={reload} />
    ) : (
      <Empty icon="check" text="All your apps are up to date." />
    );
  }

  return (
    <div className="as" ref={scroller}>
      <header className="as-head">
        <h1 className="app-title">App Store</h1>
        <nav className="segmented as-tabs" aria-label="App Store sections">
          <button type="button" className={tab === 'discover' && !q ? 'on' : ''} onClick={() => goTab('discover')}>
            Discover
          </button>
          <button type="button" className={tab === 'installed' && !q ? 'on' : ''} onClick={() => goTab('installed')}>
            Installed
          </button>
          <button type="button" className={tab === 'updates' && !q ? 'on' : ''} onClick={() => goTab('updates')}>
            Updates {updates.length > 0 && <span className="as-badge">{updates.length}</span>}
          </button>
        </nav>
        <label className="as-search">
          <Icon name="search" size={15} />
          <input
            placeholder="Search apps"
            value={q}
            onChange={(e) => {
              setQ(e.target.value);
              setDetail(null);
            }}
            onKeyDown={(e) => e.key === 'Escape' && setQ('')}
          />
          {q && (
            <button type="button" className="as-search-clear" title="Clear search" onClick={() => setQ('')}>
              <Icon name="close" size={12} />
            </button>
          )}
        </label>
      </header>

      {!docker && (
        <div className="as-banner" role="status">
          <Icon name="alert" size={14} />
          <span>Docker isn't running, so apps can't be installed or started right now. NoCapOS reconnects as soon as it's back.</span>
        </div>
      )}

      {body}
    </div>
  );
}

/* ---------------- Discover ---------------- */

function Discover({
  apps,
  categories,
  docker,
  onOpen,
  onList,
  onChanged,
}: {
  apps: StoreApp[];
  categories: string[];
  docker: boolean;
  onOpen: (id: string) => void;
  onList: (p: ListPage) => void;
  onChanged: () => void;
}) {
  const featured = apps.filter((a) => a.featured);
  const month = Date.now() - 31 * 86_400_000;
  const fresh = apps.filter((a) => addedAt(a) >= month).sort((a, b) => addedAt(b) - addedAt(a) || a.name.localeCompare(b.name));
  return (
    <>
      {featured.length > 0 && <Gallery apps={featured} onOpen={onOpen} />}
      <CategoryBar categories={categories} active={null} onPick={onList} />
      {fresh.length > 0 && (
        <section className="as-section">
          <SectionHead title="New this month" onSeeAll={() => onList('*')} />
          <div className="as-cards">
            {fresh.slice(0, 8).map((a) => (
              <button key={a.id} type="button" className="as-card" onClick={() => onOpen(a.id)}>
                <AppTileIcon app={a} size={64} />
                <b>{a.name}</b>
                <span>{a.tagline}</span>
              </button>
            ))}
          </div>
        </section>
      )}
      {categories.map((c) => {
        const inCat = apps.filter((a) => a.category === c);
        return (
          <section key={c} className="as-section">
            <SectionHead title={catLabel(c)} onSeeAll={() => onList(c)} />
            <RowGrid apps={inCat.slice(0, 6)} docker={docker} onOpen={onOpen} onChanged={onChanged} />
          </section>
        );
      })}
    </>
  );
}

function SectionHead({ title, onSeeAll }: { title: string; onSeeAll?: () => void }) {
  return (
    <div className="as-section-head">
      <h2 className="as-h2">{title}</h2>
      {onSeeAll && (
        <button type="button" className="as-link" onClick={onSeeAll}>
          See all <Icon name="chevronRight" size={13} />
        </button>
      )}
    </div>
  );
}

function CategoryBar({ categories, active, onPick }: { categories: string[]; active: ListPage; onPick: (p: ListPage) => void }) {
  return (
    <div className="as-cats" role="tablist" aria-label="Categories">
      <button type="button" className={`as-cat ${active === '*' ? 'on' : ''}`} onClick={() => onPick('*')}>
        <Icon name="grid" size={14} /> All apps
      </button>
      {categories.map((c) => (
        <button key={c} type="button" className={`as-cat ${active === c ? 'on' : ''}`} onClick={() => onPick(c)}>
          <Icon name={catIcon(c)} size={14} /> {catLabel(c)}
        </button>
      ))}
    </div>
  );
}

/** Big featured banners: auto-advances, arrows, dots and swipe. */
function Gallery({ apps, onOpen }: { apps: StoreApp[]; onOpen: (id: string) => void }) {
  const [i, setI] = useState(0);
  const [paused, setPaused] = useState(false);
  const n = apps.length;
  const go = (d: number) => setI((v) => (v + d + n) % n);
  useEffect(() => {
    if (paused || n < 2) return;
    const t = window.setTimeout(() => setI((v) => (v + 1) % n), 6000);
    return () => window.clearTimeout(t);
  }, [i, paused, n]);
  const start = useRef<{ x: number; moved: boolean } | null>(null);

  return (
    <div className="as-gallery" onMouseEnter={() => setPaused(true)} onMouseLeave={() => setPaused(false)}>
      <div
        className="as-gallery-track"
        style={{ transform: `translateX(-${i * 100}%)` }}
        onPointerDown={(e) => (start.current = { x: e.clientX, moved: false })}
        onPointerUp={(e) => {
          const s = start.current;
          if (!s) return;
          const dx = e.clientX - s.x;
          if (Math.abs(dx) > 40) {
            s.moved = true;
            go(dx < 0 ? 1 : -1);
          }
        }}
        onClickCapture={(e) => {
          // A swipe isn't a click.
          if (start.current?.moved) e.stopPropagation();
          start.current = null;
        }}
      >
        {apps.map((a, k) => (
          <button
            key={a.id}
            type="button"
            className="as-banner-card"
            style={{ background: tileBg(a, 120) }}
            onClick={() => onOpen(a.id)}
            tabIndex={k === i ? 0 : -1}
            aria-hidden={k !== i}
          >
            <span className="as-banner-text">
              <span className="as-kicker">Featured · {catLabel(a.category)}</span>
              <b>{a.name}</b>
              <span className="as-banner-tag">{a.tagline}</span>
              <span className="as-banner-cta">View app</span>
            </span>
            <span className="as-banner-art" aria-hidden="true">
              <span className="as-banner-orb">
                <Icon name={a.icon} size={72} />
              </span>
            </span>
          </button>
        ))}
      </div>
      {n > 1 && (
        <>
          <button type="button" className="as-gallery-nav prev" title="Previous" onClick={() => go(-1)}>
            <Icon name="chevronLeft" size={16} />
          </button>
          <button type="button" className="as-gallery-nav next" title="Next" onClick={() => go(1)}>
            <Icon name="chevronRight" size={16} />
          </button>
          <div className="as-dots">
            {apps.map((a, k) => (
              <button key={a.id} type="button" className={k === i ? 'on' : ''} aria-label={`Show ${a.name}`} onClick={() => setI(k)} />
            ))}
          </div>
        </>
      )}
    </div>
  );
}

/* ---------------- shared pieces ---------------- */

function AppTileIcon({ app, size = 44 }: { app: StoreApp; size?: number }) {
  return (
    <span className="as-icon" style={{ width: size, height: size, background: tileBg(app) }}>
      <Icon name={app.icon} size={Math.round(size * 0.5)} />
    </span>
  );
}

function stateLabel(a: StoreApp): { text: string; tone: string } | null {
  const i = a.installed;
  const j = a.job;
  if (j && !j.done) return { text: j.action === 'uninstall' ? 'Removing…' : j.action === 'update' ? 'Updating…' : 'Installing…', tone: 'warn' };
  if (!i) return j?.error && j.action === 'install' ? { text: 'Install failed', tone: 'bad' } : null;
  if (i.status === 'running') return { text: 'Running', tone: 'good' };
  if (i.status === 'stopped') return { text: 'Stopped', tone: '' };
  if (i.status === 'partial') return { text: 'Partly running', tone: 'warn' };
  if (i.status === 'missing') return { text: 'Containers missing', tone: 'bad' };
  return null;
}

/** Wraps a disabled button so its tooltip still shows. */
function Tip({ text, children }: { text?: string; children: ReactNode }) {
  return text ? (
    <span className="as-tip" title={text}>
      {children}
    </span>
  ) : (
    <>{children}</>
  );
}

/**
 * The app's main pill: Get/Install, live progress while a job runs, Open when
 * it's running, otherwise a way into its page.
 */
function MainButton({ app, docker, big, onOpen, onChanged }: { app: StoreApp; docker: boolean; big?: boolean; onOpen: () => void; onChanged: () => void }) {
  const j = app.job;
  const i = app.installed;
  const cls = `pill as-pill ${big ? 'big' : ''}`;
  if (j && !j.done) {
    const label = j.action === 'uninstall' ? 'Removing' : j.action === 'update' ? 'Updating' : 'Installing';
    return (
      <button type="button" className={`${cls} busy`} style={{ '--p': `${j.percent}%` } as CSSProperties} onClick={onOpen} title={j.phase}>
        <span>{big ? `${label}… ${j.percent}%` : `${j.percent}%`}</span>
      </button>
    );
  }
  if (!i) {
    return (
      <Tip text={docker ? undefined : DOCKER_DOWN}>
        <button type="button" className={cls} disabled={!docker} onClick={() => void runAction(app, 'install', onChanged)}>
          {big ? 'Install' : 'Get'}
        </button>
      </Tip>
    );
  }
  if (i.status === 'running' && appURL(app)) {
    return (
      <button type="button" className={`${cls} solid`} onClick={() => openStoreApp(app)}>
        Open
      </button>
    );
  }
  if (big || i.status === 'stopped') {
    return (
      <Tip text={docker ? undefined : DOCKER_DOWN}>
        <button type="button" className={`${cls} ${big ? 'solid' : ''}`} disabled={!docker || i.status === 'missing'} onClick={() => void runAction(app, 'start', onChanged)}>
          Start
        </button>
      </Tip>
    );
  }
  return (
    <button type="button" className={cls} onClick={onOpen}>
      View
    </button>
  );
}

function RowGrid({ apps, docker, onOpen, onChanged }: { apps: StoreApp[]; docker: boolean; onOpen: (id: string) => void; onChanged: () => void }) {
  return (
    <div className="as-rows">
      {apps.map((a) => (
        <div key={a.id} className="as-row">
          <button type="button" className="as-row-main" onClick={() => onOpen(a.id)}>
            <AppTileIcon app={a} size={50} />
            <span className="as-row-text">
              <b>{a.name}</b>
              <span>{a.tagline}</span>
            </span>
          </button>
          <MainButton app={a} docker={docker} onOpen={() => onOpen(a.id)} onChanged={onChanged} />
        </div>
      ))}
    </div>
  );
}

interface MenuItem {
  label: string;
  icon: IconName;
  onClick: () => void;
  danger?: boolean;
  disabled?: boolean;
}

/** "…" button with a small menu. */
function MoreMenu({ items, title = 'More' }: { items: MenuItem[]; title?: string }) {
  const [at, setAt] = useState<{ x: number; y: number } | null>(null);
  const btn = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!at) return;
    const close = () => setAt(null);
    const key = (e: KeyboardEvent) => e.key === 'Escape' && close();
    window.addEventListener('pointerdown', close);
    window.addEventListener('keydown', key);
    window.addEventListener('resize', close);
    return () => {
      window.removeEventListener('pointerdown', close);
      window.removeEventListener('keydown', key);
      window.removeEventListener('resize', close);
    };
  }, [at]);
  if (!items.length) return null;
  return (
    <>
      <button
        ref={btn}
        type="button"
        className="ghost icon-btn as-more"
        title={title}
        aria-haspopup="menu"
        aria-expanded={!!at}
        onClick={(e) => {
          e.stopPropagation();
          if (at) return setAt(null);
          const r = btn.current!.getBoundingClientRect();
          setAt({ x: Math.max(8, Math.min(r.right - 220, window.innerWidth - 228)), y: r.bottom + 6 });
        }}
      >
        <Icon name="more" size={18} />
      </button>
      {at &&
        createPortal(
          <div className="context-menu as-menu" role="menu" style={{ left: at.x, top: at.y }} onPointerDown={(e) => e.stopPropagation()}>
            {items.map((m) => (
              <button
                key={m.label}
                type="button"
                role="menuitem"
                className={m.danger ? 'danger' : ''}
                disabled={m.disabled}
                onClick={() => {
                  setAt(null);
                  m.onClick();
                }}
              >
                <Icon name={m.icon} size={15} />
                <span className="menu-label">{m.label}</span>
              </button>
            ))}
          </div>,
          document.body,
        )}
    </>
  );
}

/** Restart / stop / start / edit / logs / uninstall for an installed app. */
function manageItems(app: StoreApp, docker: boolean, onChanged: () => void, onEdit?: (containerId: string) => void): MenuItem[] {
  const i = app.installed;
  if (!i || (app.job && !app.job.done)) return [];
  const items: MenuItem[] = [];
  const off = !docker;
  if (i.status === 'running' || i.status === 'partial') {
    items.push({ label: 'Restart', icon: 'restart', disabled: off, onClick: () => void runAction(app, 'restart', onChanged) });
    items.push({ label: 'Stop', icon: 'stop', disabled: off, onClick: () => void runAction(app, 'stop', onChanged) });
  }
  if (i.status !== 'running') items.push({ label: 'Start', icon: 'play', disabled: off || i.status === 'missing', onClick: () => void runAction(app, 'start', onChanged) });
  if (i.update_available) items.push({ label: `Update to v${app.version}`, icon: 'download', disabled: off, onClick: () => void runAction(app, 'update', onChanged) });
  const cs = i.containers ?? [];
  if (onEdit) {
    for (const c of cs) {
      items.push({ label: cs.length > 1 ? `Edit settings: ${c.service}` : 'Edit container settings', icon: 'sliders', disabled: off, onClick: () => onEdit(c.id) });
    }
  }
  for (const c of cs) {
    items.push({
      label: cs.length > 1 ? `Logs: ${c.service}` : 'Show logs',
      icon: 'logs',
      onClick: () => openApp('logs', { title: `Logs — ${app.name} (${c.service})`, props: { id: c.id, name: `${app.name} ${c.service}` }, key: `logs:${c.id}` }),
    });
  }
  items.push({ label: 'Uninstall…', icon: 'trash', danger: true, disabled: off, onClick: () => void uninstallApp(app, onChanged) });
  return items;
}

/* ---------------- Installed & Updates ---------------- */

function InstalledCard({ app, docker, onOpen, onChanged }: { app: StoreApp; docker: boolean; onOpen: () => void; onChanged: () => void }) {
  const st = stateLabel(app);
  const j = app.job;
  return (
    <div className="as-inst">
      <button type="button" className="as-inst-main" onClick={onOpen}>
        <AppTileIcon app={app} size={56} />
        <b>{app.name}</b>
        <span className={`as-status ${st?.tone ?? ''}`}>
          <i />
          {st?.text ?? 'Installed'}
        </span>
        {app.installed?.update_available && <span className="as-upd">Update to v{app.version}</span>}
      </button>
      {j && !j.done && (
        <span className="as-progress small">
          <i style={{ width: `${j.percent}%` }} />
        </span>
      )}
      <div className="as-inst-actions">
        <MainButton app={app} docker={docker} onOpen={onOpen} onChanged={onChanged} />
        <MoreMenu items={manageItems(app, docker, onChanged)} />
      </div>
    </div>
  );
}

/** Releases newer than the installed version (or just the latest). */
function newReleases(a: StoreApp) {
  const cur = a.installed?.version;
  const k = a.releases.findIndex((r) => r.version === cur);
  return k > 0 ? a.releases.slice(0, k) : a.releases.slice(0, 1);
}

function Updates({ updates, docker, onOpen, onChanged }: { updates: StoreApp[]; docker: boolean; onOpen: (id: string) => void; onChanged: () => void }) {
  const [running, setRunning] = useState(false);
  const all = async () => {
    setRunning(true);
    for (const a of updates) {
      const r = await storeApi.act(a.id, 'update');
      if (!r.ok) toast('error', `Could not update ${a.name}`, r.error);
    }
    setRunning(false);
    onChanged();
  };
  return (
    <section className="as-section">
      <div className="as-section-head">
        <h2 className="as-h2">
          {updates.length} update{updates.length > 1 ? 's' : ''} available
        </h2>
        <Tip text={docker ? undefined : DOCKER_DOWN}>
          <button type="button" className="pill" disabled={!docker || running} onClick={() => void all()}>
            Update all
          </button>
        </Tip>
      </div>
      <div className="ucard as-updates">
        {updates.map((a) => {
          const working = !!a.job && !a.job.done;
          return (
            <div key={a.id} className="as-update">
              <button type="button" className="as-row-main" onClick={() => onOpen(a.id)}>
                <AppTileIcon app={a} size={46} />
                <span className="as-row-text">
                  <b>{a.name}</b>
                  <span>
                    v{a.installed?.version} → v{a.version}
                  </span>
                </span>
              </button>
              {working ? (
                <MainButton app={a} docker={docker} onOpen={() => onOpen(a.id)} onChanged={onChanged} />
              ) : (
                <Tip text={docker ? undefined : DOCKER_DOWN}>
                  <button type="button" className="pill as-pill" disabled={!docker} onClick={() => void runAction(a, 'update', onChanged)}>
                    Update
                  </button>
                </Tip>
              )}
              <div className="as-notes">
                {newReleases(a).map((r) => (
                  <p key={r.version}>
                    <b>v{r.version}</b> <span className="muted">{r.date}</span> — {r.notes}
                  </p>
                ))}
              </div>
            </div>
          );
        })}
      </div>
    </section>
  );
}

/* ---------------- App page ---------------- */

function AppPage({
  app,
  apps,
  docker,
  onBack,
  onOpen,
  onChanged,
}: {
  app: StoreApp;
  apps: StoreApp[];
  docker: boolean;
  onBack: () => void;
  onOpen: (id: string) => void;
  onChanged: () => void;
}) {
  const i = app.installed;
  const job = app.job;
  const working = !!job && !job.done;
  const [reveal, setReveal] = useState(false);
  const [editing, setEditing] = useState<string | null>(null);

  if (editing) {
    return (
      <div className="containers as-editor">
        <button type="button" className="ghost small pill as-back" onClick={() => setEditing(null)}>
          <Icon name="chevronLeft" size={14} /> {app.name}
        </button>
        <ContainerEditor
          containerId={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            onChanged();
          }}
        />
      </div>
    );
  }

  const url = appURL(app);
  const ports = app.services.flatMap((s) => (s.ports ?? []).filter((p) => p.host).map((p) => `${p.host}/${p.protocol ?? 'tcp'}`));
  const similar = apps.filter((a) => a.id !== app.id && a.category === app.category);
  const alsoLike = (similar.length ? similar : apps.filter((a) => a.id !== app.id && a.featured)).slice(0, 6);
  const menu = manageItems(app, docker, onChanged, setEditing);

  return (
    <div className="as-page">
      <button type="button" className="ghost small pill as-back" onClick={onBack}>
        <Icon name="chevronLeft" size={14} /> Back
      </button>

      <div className="as-page-head">
        <AppTileIcon app={app} size={96} />
        <div className="as-page-title">
          <h2>{app.name}</h2>
          <span className="as-page-tag">{app.tagline}</span>
          <span className="muted small">{app.developer}</span>
        </div>
        <div className="as-page-actions">
          <MainButton app={app} docker={docker} big onOpen={() => undefined} onChanged={onChanged} />
          {i && i.update_available && !working && (
            <Tip text={docker ? undefined : DOCKER_DOWN}>
              <button type="button" className="ghost pill" disabled={!docker} onClick={() => void runAction(app, 'update', onChanged)}>
                Update to v{app.version}
              </button>
            </Tip>
          )}
          <MoreMenu items={menu} title="Manage" />
        </div>
      </div>

      {job && (working || job.error) && (
        <div className={`as-job ${job.error ? 'failed' : ''}`}>
          <div className="as-job-text">
            <b>{job.error ? `${job.action[0].toUpperCase()}${job.action.slice(1)} failed` : job.phase}</b>
            {!job.error && <span>{job.percent}%</span>}
          </div>
          {job.error ? (
            <p className="small">{job.error}</p>
          ) : (
            <span className="as-progress">
              <i style={{ width: `${job.percent}%` }} />
            </span>
          )}
        </div>
      )}

      <Shots app={app} />

      <div className="as-page-body">
        <div className="as-page-main">
          <section>
            <h3 className="as-h3">About</h3>
            <p className="as-about">{app.description}</p>
            {i && app.first_run && (
              <div className="as-callout">
                <Icon name="info" size={15} />
                <span>{app.first_run}</span>
              </div>
            )}
            {i?.credentials && (i.credentials.username || i.credentials.password) && (
              <div className="ucard as-creds">
                <b>Sign-in details</b>
                {i.credentials.username && (
                  <div>
                    <span>Username</span>
                    <code>{i.credentials.username}</code>
                  </div>
                )}
                {i.credentials.password && (
                  <div>
                    <span>Password</span>
                    <code>{reveal ? i.credentials.password : '•'.repeat(12)}</code>
                    <button type="button" className="ghost icon-btn" title={reveal ? 'Hide' : 'Show'} onClick={() => setReveal(!reveal)}>
                      <Icon name="eye" size={13} />
                    </button>
                    <button
                      type="button"
                      className="ghost icon-btn"
                      title="Copy"
                      onClick={() => void navigator.clipboard?.writeText(i.credentials!.password!).then(() => toast('success', 'Password copied'))}
                    >
                      <Icon name="copy" size={13} />
                    </button>
                  </div>
                )}
              </div>
            )}
          </section>

          {app.services.length > 1 && (
            <section>
              <h3 className="as-h3">Includes</h3>
              <div className="ucard ulist">
                {app.services.map((s) => (
                  <div key={s.name} className="urow">
                    <span className="urow-icon">
                      <Icon name="containers" size={16} />
                    </span>
                    <span className="urow-text">
                      <span className="urow-title">{s.name}</span>
                      <span className="urow-desc mono">{s.image}</span>
                    </span>
                  </div>
                ))}
              </div>
            </section>
          )}

          <section>
            <h3 className="as-h3">What's new</h3>
            <div className="as-releases">
              {app.releases.map((r) => (
                <div key={r.version} className="as-release">
                  <div>
                    <b>Version {r.version}</b> <span className="muted small">{r.date}</span>
                  </div>
                  <p>{r.notes}</p>
                </div>
              ))}
            </div>
          </section>
        </div>

        <aside className="ucard as-info">
          <h3 className="as-h3">Info</h3>
          <dl>
            <dt>Version</dt>
            <dd>{i ? `${i.version}${i.update_available ? ` (${app.version} available)` : ''}` : app.version}</dd>
            <dt>Category</dt>
            <dd>{catLabel(app.category)}</dd>
            <dt>Developer</dt>
            <dd>{app.developer}</dd>
            {app.website && (
              <>
                <dt>Website</dt>
                <dd>
                  <a href={app.website} target="_blank" rel="noopener noreferrer">
                    {app.website.replace(/^https?:\/\//, '').replace(/\/$/, '')}
                  </a>
                </dd>
              </>
            )}
            {app.source && (
              <>
                <dt>Source code</dt>
                <dd>
                  <a href={app.source} target="_blank" rel="noopener noreferrer">
                    {app.source.replace(/^https?:\/\/(www\.)?/, '')}
                  </a>
                </dd>
              </>
            )}
            {url ? (
              <>
                <dt>Address</dt>
                <dd className="mono small">{url}</dd>
              </>
            ) : (
              app.web && (
                <>
                  <dt>Web UI</dt>
                  <dd>Gets its own port on install</dd>
                </>
              )
            )}
            {ports.length > 0 && (
              <>
                <dt>Also uses ports</dt>
                <dd className="mono small">{ports.join(', ')}</dd>
              </>
            )}
            <dt>Requires</dt>
            <dd>
              Docker{' '}
              <span className={`as-status inline ${docker ? 'good' : 'bad'}`}>
                <i />
                {docker ? 'running' : 'not running'}
              </span>
            </dd>
            <dt>Images</dt>
            <dd className="mono small">{app.services.map((s) => s.image).join(', ')}</dd>
          </dl>
          {i?.containers && i.containers.length > 0 && (
            <div className="as-containers">
              <span className="section-label">Containers</span>
              {i.containers.map((c) => (
                <div key={c.id} className="as-container">
                  <span className={`as-dot ${c.state === 'running' ? 'on' : ''}`} />
                  <span className="mono small">{c.service}</span>
                  <span className="muted small">{c.state}</span>
                </div>
              ))}
            </div>
          )}
        </aside>
      </div>

      {alsoLike.length > 0 && (
        <section className="as-section">
          <h3 className="as-h3">You might also like</h3>
          <RowGrid apps={alsoLike} docker={docker} onOpen={onOpen} onChanged={onChanged} />
        </section>
      )}
    </div>
  );
}

/** Screenshot strip; without screenshots, banners drawn from the app's colours. */
function Shots({ app }: { app: StoreApp }) {
  if (app.screenshots?.length) {
    return (
      <div className="as-shots">
        {app.screenshots.map((s) => (
          <img key={s} className="as-shot" src={s} alt={`${app.name} screenshot`} loading="lazy" referrerPolicy="no-referrer" />
        ))}
      </div>
    );
  }
  const web = app.web ? `Opens in your browser on its own port` : 'Runs in the background';
  return (
    <div className="as-shots" aria-hidden="true">
      <div className="as-shot gen one" style={{ background: tileBg(app, 125) }}>
        <span className="as-banner-orb">
          <Icon name={app.icon} size={64} />
        </span>
        <b>{app.name}</b>
        <span>{app.tagline}</span>
      </div>
      <div className="as-shot gen two" style={{ background: tileBg(app, 200) }}>
        <Icon name={app.icon} size={200} className="as-shot-watermark" />
        <span className="as-kicker">{catLabel(app.category)}</span>
        <b>Self-hosted. Yours.</b>
        <span>Runs on this server, with its data in its own volumes.</span>
      </div>
      <div className="as-shot gen three" style={{ background: tileBg(app, 300) }}>
        <span className="as-shot-chips">
          <span>
            <Icon name="containers" size={14} /> {app.services.length} container{app.services.length > 1 ? 's' : ''}
          </span>
          <span>
            <Icon name="globe" size={14} /> {web}
          </span>
          <span>
            <Icon name="download" size={14} /> One-click updates
          </span>
        </span>
      </div>
    </div>
  );
}
