import { useCallback, useEffect, useRef, useState, type CSSProperties } from 'react';
import { nativeApi, type NativeApp, type NativeDetails, type NativeJob, type NativeStatus } from '../api/nativeApps';
import { Icon } from '../components/Icon';
import { NativeAppIcon } from '../shell/NativeTile';
import { useDesktopIcons } from '../state/desktopIcons';
import { launchNativeApp, nativeKey, uninstallNativeApp, useNativeApps } from '../state/nativeApps';
import { toast } from '../state/toasts';
import { Empty } from './Monitor';

// App Store → Linux apps: desktop apps from Flathub, installed on this server.
// Installed apps show up in Launchpad by themselves; the desktop only gets a
// shortcut when someone adds one.

export function LinuxApps({ initial, q }: { initial?: string; q: string }) {
  const [status, setStatus] = useState<NativeStatus | null>(null);
  const [installed, setInstalled] = useState<NativeApp[]>([]);
  const [popular, setPopular] = useState<NativeApp[] | null>(null);
  const [popularError, setPopularError] = useState<string | null>(null);
  const [jobs, setJobs] = useState<NativeJob[]>([]);
  const [results, setResults] = useState<NativeApp[] | null>(null);
  const [searching, setSearching] = useState(false);
  const [searchError, setSearchError] = useState<string | null>(null);
  const [detail, setDetail] = useState<string | null>(initial ?? null);
  useEffect(() => {
    if (initial) setDetail(initial);
  }, [initial]);

  const loadInstalled = useCallback(async () => {
    const r = await nativeApi.installed();
    if (!r.ok) {
      setStatus({ supported: false, screen: false, reason: r.error });
      return;
    }
    setStatus(r.data.status);
    setInstalled(r.data.apps ?? []);
    if (r.data.jobs) setJobs(r.data.jobs);
  }, []);

  useEffect(() => {
    void loadInstalled();
  }, [loadInstalled]);

  useEffect(() => {
    if (!status?.supported || popular) return;
    void nativeApi.popular().then((r) => {
      if (r.ok) {
        setPopular(r.data.apps);
        setJobs(r.data.jobs ?? []);
      } else setPopularError(r.error ?? 'Flathub is taking a nap. Try again in a sec.');
    });
  }, [status?.supported, popular]);

  // Search as you type (a short pause first); typing leaves an app's page.
  const firstQ = useRef(true);
  useEffect(() => {
    if (firstQ.current) firstQ.current = false;
    else setDetail(null);
    const needle = q.trim();
    if (!needle) {
      setResults(null);
      setSearchError(null);
      return;
    }
    setSearching(true);
    const t = window.setTimeout(async () => {
      const r = await nativeApi.search(needle);
      setSearching(false);
      if (r.ok) {
        setResults(r.data.apps);
        setSearchError(null);
      } else setSearchError(r.error ?? "Couldn't search Flathub right now.");
    }, 350);
    return () => window.clearTimeout(t);
  }, [q]);

  // Follow running jobs; tell people when one ends.
  const watching = useRef(new Set<string>());
  const busy = jobs.some((j) => !j.done);
  useEffect(() => {
    if (!busy) return;
    const t = window.setInterval(async () => {
      const r = await nativeApi.jobs();
      if (r.ok) setJobs(r.data.jobs);
    }, 1200);
    return () => window.clearInterval(t);
  }, [busy]);
  useEffect(() => {
    let changed = false;
    for (const j of jobs) {
      if (!j.done) watching.current.add(j.id);
      else if (watching.current.delete(j.id)) {
        changed = true;
        if (j.error) toast('error', j.action === 'install' ? `Couldn't install ${j.name}` : `Couldn't remove ${j.name}`, j.error);
        else if (j.action === 'install') toast('success', "Installed — it's in your Launchpad", j.name);
        else toast('success', `${j.name} removed`);
      }
    }
    if (changed) {
      void loadInstalled();
      void useNativeApps.getState().load();
    }
  }, [jobs, loadInstalled]);

  const installedIds = new Set(installed.map((a) => a.id));
  const jobFor = (id: string) => jobs.find((j) => j.app === id);
  const mark = (a: NativeApp): NativeApp => ({ ...a, installed: installedIds.has(a.id) });

  const install = async (a: NativeApp) => {
    const r = await nativeApi.install(a.id, a.name);
    if (!r.ok) {
      toast('error', `Couldn't install ${a.name}`, r.error);
      return;
    }
    watching.current.add(r.data.id);
    setJobs((js) => [r.data, ...js.filter((j) => j.app !== a.id)]);
  };
  const remove = async (a: NativeApp) => {
    if (await uninstallNativeApp(a)) {
      const r = await nativeApi.jobs();
      if (r.ok) {
        r.data.jobs.filter((j) => !j.done).forEach((j) => watching.current.add(j.id));
        setJobs(r.data.jobs);
      }
    }
  };

  if (!status) return <Empty icon="linux" text="Checking for Linux apps…" />;
  if (!status.supported) {
    return (
      <Empty icon="linux" text={status.reason || 'Linux apps need NoCapOS installed directly on Linux.'}>
        <p className="muted small" style={{ maxWidth: 440, textAlign: 'center' }}>
          Linux apps come from Flathub and install on the server itself, so they only work when NoCapOS runs straight on a Linux machine
          (not in Docker, not on Windows or macOS).
        </p>
      </Empty>
    );
  }

  const actions = { jobFor, install, remove };

  if (detail) {
    return <LinuxAppPage id={detail} installed={installedIds.has(detail)} onBack={() => setDetail(null)} {...actions} />;
  }

  return (
    <>
      {status.screen && (
        <div className="as-callout">
          <Icon name="desktop" size={15} />
          <span>The server's screen is on, so apps open right there on its monitor.</span>
        </div>
      )}

      {q.trim() ? (
        <section className="as-section">
          <h2 className="as-h2">
            Results for “{q.trim()}” {results && <span className="muted">{results.length}</span>}
          </h2>
          {searchError ? (
            <p className="muted">{searchError}</p>
          ) : !results || searching ? (
            <p className="muted">Searching Flathub…</p>
          ) : results.length ? (
            <LinuxRows apps={results.map(mark)} onOpen={setDetail} {...actions} />
          ) : (
            <p className="muted">Nothing on Flathub matches that. Try another word?</p>
          )}
        </section>
      ) : (
        <>
          {installed.length > 0 && (
            <section className="as-section">
              <h2 className="as-h2">
                On this server <span className="muted">{installed.length}</span>
              </h2>
              <LinuxRows apps={installed} onOpen={setDetail} {...actions} />
            </section>
          )}
          <section className="as-section">
            <h2 className="as-h2">Popular</h2>
            {popular ? (
              <div className="as-cards">
                {popular.map((a) => (
                  <button key={a.id} type="button" className="as-card" onClick={() => setDetail(a.id)}>
                    <NativeAppIcon app={a} size={64} />
                    <b>{a.name}</b>
                    <span>{a.summary}</span>
                  </button>
                ))}
              </div>
            ) : (
              <p className="muted">{popularError ?? 'Loading the good stuff…'}</p>
            )}
          </section>
          <p className="muted small">Apps come from Flathub and install for everyone on this server.</p>
        </>
      )}
    </>
  );
}

interface Actions {
  jobFor: (id: string) => NativeJob | undefined;
  install: (a: NativeApp) => Promise<void>;
  remove: (a: NativeApp) => Promise<void>;
}

function LinuxButton({ app, big, jobFor, install }: Actions & { app: NativeApp; big?: boolean }) {
  const j = jobFor(app.id);
  const cls = `pill as-pill ${big ? 'big' : ''}`;
  if (j && !j.done) {
    const label = j.action === 'uninstall' ? 'Removing' : 'Installing';
    return (
      <button type="button" className={`${cls} busy`} style={{ '--p': `${j.percent}%` } as CSSProperties} title={j.phase}>
        <span>{big ? `${label}… ${j.percent}%` : `${j.percent}%`}</span>
      </button>
    );
  }
  if (app.installed) {
    return (
      <button type="button" className={`${cls} solid`} onClick={() => void launchNativeApp(app)}>
        Open
      </button>
    );
  }
  return (
    <button type="button" className={cls} onClick={() => void install(app)}>
      {big ? 'Install' : 'Get'}
    </button>
  );
}

function LinuxRows({ apps, onOpen, ...actions }: Actions & { apps: NativeApp[]; onOpen: (id: string) => void }) {
  return (
    <div className="as-rows">
      {apps.map((a) => (
        <div key={a.id} className="as-row">
          <button type="button" className="as-row-main" onClick={() => onOpen(a.id)}>
            <NativeAppIcon app={a} size={50} />
            <span className="as-row-text">
              <b>{a.name}</b>
              <span>{a.summary || a.id}</span>
            </span>
          </button>
          <LinuxButton app={a} {...actions} />
        </div>
      ))}
    </div>
  );
}

function LinuxAppPage({ id, installed, onBack, ...actions }: Actions & { id: string; installed: boolean; onBack: () => void }) {
  const [d, setD] = useState<NativeDetails | null>(null);
  const [error, setError] = useState<string | null>(null);
  const onDesk = useDesktopIcons((s) => s.icons.some((i) => i.appId === nativeKey(id)));

  useEffect(() => {
    setD(null);
    setError(null);
    void nativeApi.details(id).then((r) => (r.ok ? setD(r.data) : setError(r.error ?? "Couldn't load that app.")));
  }, [id]);

  const back = (
    <button type="button" className="ghost small pill as-back" onClick={onBack}>
      <Icon name="chevronLeft" size={14} /> Back
    </button>
  );
  if (error) {
    return (
      <div className="as-page">
        {back}
        <Empty icon="linux" text={error} />
      </div>
    );
  }
  if (!d) {
    return (
      <div className="as-page">
        {back}
        <Empty icon="linux" text="Loading…" />
      </div>
    );
  }
  const app: NativeApp = { ...d, installed };
  const job = actions.jobFor(id);
  const working = !!job && !job.done;

  return (
    <div className="as-page">
      {back}
      <div className="as-page-head">
        <NativeAppIcon app={app} size={96} />
        <div className="as-page-title">
          <h2>{app.name}</h2>
          <span className="as-page-tag">{app.summary}</span>
          <span className="muted small">{app.developer || app.id}</span>
        </div>
        <div className="as-page-actions">
          <LinuxButton app={app} big {...actions} />
          {installed && !working && (
            <>
              <button
                type="button"
                className="ghost pill"
                onClick={() => (onDesk ? useDesktopIcons.getState().remove(nativeKey(id)) : useDesktopIcons.getState().place(nativeKey(id)))}
              >
                {onDesk ? 'Remove from Desktop' : 'Add to Desktop'}
              </button>
              <button type="button" className="ghost pill danger" onClick={() => void actions.remove(app)}>
                Remove
              </button>
            </>
          )}
        </div>
      </div>

      {job && (working || job.error) && (
        <div className={`as-job ${job.error ? 'failed' : ''}`}>
          <div className="as-job-text">
            <b>{job.error ? (job.action === 'install' ? 'Install failed' : 'Remove failed') : job.phase}</b>
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

      <div className="as-page-body">
        <div className="as-page-main">
          <section>
            <h3 className="as-h3">About</h3>
            <p className="as-about" style={{ whiteSpace: 'pre-line' }}>
              {d.description || app.summary}
            </p>
            {installed && (
              <div className="as-callout">
                <Icon name="info" size={15} />
                <span>It's in everyone's Launchpad. Want it on the desktop too? Hit “Add to Desktop”.</span>
              </div>
            )}
          </section>
        </div>
        <aside className="ucard as-info">
          <h3 className="as-h3">Info</h3>
          <dl>
              <dt>Source</dt>
              <dd>Flathub</dd>
              <dt>App ID</dt>
              <dd>
                <code>{app.id}</code>
              </dd>
              {d.version && (
                <>
                  <dt>Version</dt>
                  <dd>{d.version}</dd>
                </>
              )}
              {d.license && (
                <>
                  <dt>License</dt>
                  <dd>{d.license}</dd>
                </>
              )}
              {d.homepage && (
                <>
                  <dt>Website</dt>
                  <dd>
                    <a href={d.homepage} target="_blank" rel="noopener noreferrer">
                      {d.homepage.replace(/^https?:\/\//, '').replace(/\/$/, '')}
                    </a>
                  </dd>
                </>
              )}
          </dl>
        </aside>
      </div>
    </div>
  );
}
