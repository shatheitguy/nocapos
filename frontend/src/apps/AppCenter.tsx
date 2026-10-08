import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { appURL, openStoreApp, storeApi, type StoreApp } from '../api/appstore';
import { Bar } from '../components/Charts';
import { Icon } from '../components/Icon';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import type { WinState } from '../state/windows';
import { openApp } from './meta';
import { Empty } from './Monitor';

// App Store: discover, install, open, update and remove self-hosted apps.
// Each app runs as Docker containers with its own data volumes.

type Tab = 'discover' | 'installed' | 'updates';

export function AppCenter({ win }: { win?: WinState }) {
  const [apps, setApps] = useState<StoreApp[] | null>(null);
  const [docker, setDocker] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>('discover');
  const [cat, setCat] = useState('All');
  const [q, setQ] = useState('');
  const [detail, setDetail] = useState<string | null>(win?.props?.app ?? null);
  // Opened again at a specific app (e.g. from search): show that app.
  const wanted = win?.props?.app;
  useEffect(() => {
    if (wanted) setDetail(wanted);
  }, [wanted]);
  const timer = useRef<number | undefined>(undefined);

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

  const categories = useMemo(() => ['All', ...Array.from(new Set((apps ?? []).map((a) => a.category))).sort()], [apps]);
  const installed = (apps ?? []).filter((a) => a.installed);
  const updates = installed.filter((a) => a.installed?.update_available);
  const shown = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return (apps ?? []).filter(
      (a) =>
        (cat === 'All' || a.category === cat) &&
        (!needle || `${a.name} ${a.tagline} ${a.description} ${a.category} ${a.developer}`.toLowerCase().includes(needle)),
    );
  }, [apps, cat, q]);

  if (error) return <Empty icon="store" text={error} />;
  if (!apps) return <Empty icon="store" text="Loading the App Store…" />;

  const current = apps.find((a) => a.id === detail);
  if (current) return <AppPage app={current} docker={docker} onBack={() => setDetail(null)} onChanged={() => void load()} />;

  const featured = apps.filter((a) => a.featured);

  return (
    <div className="store">
      <div className="store-top">
        <div className="segmented store-tabs">
          <button type="button" className={tab === 'discover' ? 'on' : ''} onClick={() => setTab('discover')}>
            Discover
          </button>
          <button type="button" className={tab === 'installed' ? 'on' : ''} onClick={() => setTab('installed')}>
            Installed {installed.length > 0 && <span className="store-count">{installed.length}</span>}
          </button>
          <button type="button" className={tab === 'updates' ? 'on' : ''} onClick={() => setTab('updates')}>
            Updates {updates.length > 0 && <span className="store-count hot">{updates.length}</span>}
          </button>
        </div>
        <label className="search compact store-search">
          <Icon name="search" size={15} />
          <input
            placeholder="Search apps"
            value={q}
            onChange={(e) => {
              setQ(e.target.value);
              setTab('discover');
            }}
          />
        </label>
      </div>

      {!docker && (
        <div className="store-banner">
          <Icon name="alert" size={15} /> Docker isn't running on this machine, so apps can't be installed or started right now. Start Docker and
          NoCapOS reconnects automatically.
        </div>
      )}

      {tab === 'discover' && (
        <>
          {!q && cat === 'All' && featured.length > 0 && (
            <div className="store-featured">
              {featured.map((a) => (
                <button key={a.id} type="button" className="store-hero" style={{ background: `linear-gradient(135deg, ${a.tile[0]}, ${a.tile[1]})` }} onClick={() => setDetail(a.id)}>
                  <span className="store-hero-kicker">Featured · {a.category}</span>
                  <b>{a.name}</b>
                  <span>{a.tagline}</span>
                  <Icon name={a.icon} size={64} className="store-hero-icon" />
                </button>
              ))}
            </div>
          )}
          <div className="chip-row tabs">
            {categories.map((c) => (
              <button key={c} type="button" className={`chip-btn ${cat === c ? 'on' : ''}`} onClick={() => setCat(c)}>
                {c}
              </button>
            ))}
          </div>
          <div className="store-grid">
            {shown.map((a) => (
              <AppCard key={a.id} app={a} onOpen={() => setDetail(a.id)} />
            ))}
            {!shown.length && <p className="muted">No apps match your search.</p>}
          </div>
        </>
      )}

      {tab === 'installed' &&
        (installed.length ? (
          <div className="store-list">
            {installed.map((a) => (
              <InstalledRow key={a.id} app={a} onOpen={() => setDetail(a.id)} onChanged={() => void load()} />
            ))}
          </div>
        ) : (
          <Empty icon="store" text="No apps installed yet. Find one under Discover." />
        ))}

      {tab === 'updates' &&
        (updates.length ? (
          <div className="store-list">
            <div className="store-list-head">
              <span className="muted small">{updates.length} update{updates.length > 1 ? 's' : ''} available</span>
              <button
                type="button"
                className="small"
                onClick={async () => {
                  for (const a of updates) await storeApi.act(a.id, 'update');
                  void load();
                }}
              >
                Update all
              </button>
            </div>
            {updates.map((a) => (
              <InstalledRow key={a.id} app={a} onOpen={() => setDetail(a.id)} onChanged={() => void load()} />
            ))}
          </div>
        ) : (
          <Empty icon="check" text="All your apps are up to date." />
        ))}
    </div>
  );
}

function AppTileIcon({ app, size = 44 }: { app: StoreApp; size?: number }) {
  return (
    <span className="store-icon" style={{ width: size, height: size, background: `linear-gradient(145deg, ${app.tile[0]}, ${app.tile[1]})` }}>
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

function AppCard({ app, onOpen }: { app: StoreApp; onOpen: () => void }) {
  const st = stateLabel(app);
  return (
    <button type="button" className="store-card" onClick={onOpen}>
      <AppTileIcon app={app} />
      <span className="store-card-text">
        <b>{app.name}</b>
        <span className="muted small">{app.tagline}</span>
      </span>
      {st ? <span className={`chip tiny ${st.tone}`}>{st.text}</span> : <span className="store-get">Get</span>}
    </button>
  );
}

function InstalledRow({ app, onOpen, onChanged }: { app: StoreApp; onOpen: () => void; onChanged: () => void }) {
  const st = stateLabel(app);
  const i = app.installed!;
  const working = !!app.job && !app.job.done;
  return (
    <div className="store-row">
      <button type="button" className="store-row-main" onClick={onOpen}>
        <AppTileIcon app={app} size={38} />
        <span className="store-card-text">
          <b>{app.name}</b>
          <span className="muted small">
            v{i.version}
            {i.update_available ? ` → v${app.version}` : ''}
          </span>
        </span>
      </button>
      {st && <span className={`chip tiny ${st.tone}`}>{st.text}</span>}
      {working && <span className="store-row-bar"><Bar value={app.job!.percent} /></span>}
      {i.update_available && !working && (
        <button type="button" className="small" onClick={async () => { const r = await storeApi.act(app.id, 'update'); if (!r.ok) toast('error', `Could not update ${app.name}`, r.error); onChanged(); }}>
          Update
        </button>
      )}
      {i.status === 'running' && appURL(app) && (
        <button type="button" className="ghost small" onClick={() => openStoreApp(app)}>
          Open
        </button>
      )}
    </div>
  );
}

function AppPage({ app, docker, onBack, onChanged }: { app: StoreApp; docker: boolean; onBack: () => void; onChanged: () => void }) {
  const i = app.installed;
  const job = app.job;
  const working = !!job && !job.done;
  const [reveal, setReveal] = useState(false);
  const lastJob = useRef<string | undefined>(undefined);

  // Tell the user when a job finishes.
  useEffect(() => {
    if (job?.done && lastJob.current === job.id) {
      if (job.error) toast('error', `${app.name}: ${job.action} failed`, job.error);
      else toast('success', `${app.name} ${job.action === 'install' ? 'installed' : job.action === 'update' ? 'updated' : 'removed'}`);
      lastJob.current = undefined;
    } else if (job && !job.done) lastJob.current = job.id;
  }, [job, app.name]);

  const act = async (action: 'install' | 'update' | 'start' | 'stop' | 'restart') => {
    const r = await storeApi.act(app.id, action);
    if (!r.ok) toast('error', `Could not ${action} ${app.name}`, r.error);
    onChanged();
  };

  const uninstall = async () => {
    let deleteData = false;
    const ok = await confirmDialog({
      title: `Uninstall ${app.name}?`,
      message: 'Its containers are removed. Your data is kept unless you choose to delete it in the next step, so a reinstall picks up where you left off.',
      confirmLabel: 'Uninstall',
      danger: true,
    });
    if (!ok) return;
    deleteData = await confirmDialog({
      title: `Also delete ${app.name}'s data?`,
      message: 'Settings, accounts and files stored inside the app are deleted for good. Choose "Keep data" to keep them for a later reinstall.',
      confirmLabel: 'Delete data',
      cancelLabel: 'Keep data',
      danger: true,
    });
    const r = await storeApi.uninstall(app.id, deleteData);
    if (!r.ok) toast('error', `Could not uninstall ${app.name}`, r.error);
    onChanged();
  };

  const url = appURL(app);
  const ports = app.services.flatMap((s) => (s.ports ?? []).filter((p) => p.host).map((p) => `${p.host}/${p.protocol ?? 'tcp'}`));

  return (
    <div className="store store-page">
      <button type="button" className="ghost small store-back" onClick={onBack}>
        <Icon name="chevronLeft" size={14} /> App Store
      </button>
      <div className="store-page-head">
        <AppTileIcon app={app} size={84} />
        <div className="store-page-title">
          <h2 className="no-margin">{app.name}</h2>
          <span className="muted">{app.tagline}</span>
          <span className="muted small">
            {app.developer} · {app.category}
          </span>
        </div>
        <div className="store-page-actions">
          {!i && (
            <button type="button" disabled={!docker} onClick={() => void act('install')}>
              <Icon name="download" size={14} /> Install
            </button>
          )}
          {i && !working && (
            <>
              {i.status === 'running' && url && (
                <button type="button" onClick={() => openStoreApp(app)}>
                  <Icon name="external" size={14} /> Open
                </button>
              )}
              {i.update_available && (
                <button type="button" className="ghost" disabled={!docker} onClick={() => void act('update')}>
                  Update to v{app.version}
                </button>
              )}
              {i.status === 'running' ? (
                <>
                  <button type="button" className="ghost icon-btn" title="Restart" disabled={!docker} onClick={() => void act('restart')}>
                    <Icon name="restart" size={15} />
                  </button>
                  <button type="button" className="ghost icon-btn" title="Stop" disabled={!docker} onClick={() => void act('stop')}>
                    <Icon name="stop" size={15} />
                  </button>
                </>
              ) : (
                <button type="button" className="ghost" disabled={!docker || i.status === 'missing'} onClick={() => void act('start')}>
                  <Icon name="play" size={14} /> Start
                </button>
              )}
            </>
          )}
        </div>
      </div>

      {job && (working || job.error) && (
        <div className={`store-progress ${job.error ? 'failed' : ''}`}>
          <div className="store-progress-text">
            <b>{job.error ? `${job.action[0].toUpperCase()}${job.action.slice(1)} failed` : job.phase}</b>
            {!job.error && <span>{job.percent}%</span>}
          </div>
          {job.error ? <p className="small">{job.error}</p> : <Bar value={job.percent} />}
        </div>
      )}

      <div className="store-page-body">
        <section className="store-about">
          <p>{app.description}</p>
          {i && app.first_run && (
            <div className="store-callout">
              <Icon name="info" size={15} />
              <span>{app.first_run}</span>
            </div>
          )}
          {i?.credentials && (i.credentials.username || i.credentials.password) && (
            <div className="store-creds">
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
          <h4>What's new</h4>
          <ul className="store-releases">
            {app.releases.map((r) => (
              <li key={r.version}>
                <b>v{r.version}</b> <span className="muted small">{r.date}</span>
                <p className="small">{r.notes}</p>
              </li>
            ))}
          </ul>
        </section>

        <aside className="store-info">
          <dl>
            <dt>Version</dt>
            <dd>{i ? `v${i.version}${i.update_available ? ` (v${app.version} available)` : ''}` : `v${app.version}`}</dd>
            <dt>Category</dt>
            <dd>{app.category}</dd>
            <dt>Developer</dt>
            <dd>{app.developer}</dd>
            {url && (
              <>
                <dt>Address</dt>
                <dd className="mono small">{url}</dd>
              </>
            )}
            {ports.length > 0 && (
              <>
                <dt>Also uses ports</dt>
                <dd className="mono small">{ports.join(', ')}</dd>
              </>
            )}
            <dt>Website</dt>
            <dd>
              <a href={app.website} target="_blank" rel="noopener noreferrer">
                {app.website.replace(/^https?:\/\//, '')}
              </a>
            </dd>
            <dt>Images</dt>
            <dd className="mono small">{app.services.map((s) => s.image).join(', ')}</dd>
          </dl>
          {i?.containers && i.containers.length > 0 && (
            <div className="store-containers">
              <b className="small">Containers</b>
              {i.containers.map((c) => (
                <div key={c.id} className="store-container">
                  <span className={`dot ${c.state === 'running' ? 'on' : ''}`} />
                  <span className="mono small">{c.service}</span>
                  <span className="muted small">{c.state}</span>
                  <button
                    type="button"
                    className="ghost icon-btn"
                    title="Logs"
                    onClick={() => openApp('logs', { title: `Logs — ${app.name} (${c.service})`, props: { id: c.id, name: `${app.name} ${c.service}` }, key: `logs:${c.id}` })}
                  >
                    <Icon name="logs" size={13} />
                  </button>
                </div>
              ))}
            </div>
          )}
          {i && !working && (
            <button type="button" className="ghost danger small store-uninstall" disabled={!docker} onClick={() => void uninstall()}>
              <Icon name="trash" size={13} /> Uninstall…
            </button>
          )}
        </aside>
      </div>
    </div>
  );
}
