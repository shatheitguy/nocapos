import { useCallback, useEffect, useRef, useState, type CSSProperties } from 'react';
import { ALL_NATIVE, nativeApi, type NativeJob, type NativeUpdate } from '../api/nativeApps';
import { Icon } from '../components/Icon';
import { NativeAppIcon } from '../shell/NativeTile';
import { useNativeApps } from '../state/nativeApps';
import { toast } from '../state/toasts';

// App Store → Updates → System app updates: Flatpak apps with a newer
// version on Flathub. Updates run as background jobs, one at a time.

export function useSystemUpdates() {
  const [updates, setUpdates] = useState<NativeUpdate[] | null>(null);
  const [unsupported, setUnsupported] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [checking, setChecking] = useState(false);
  const load = useCallback(async (fresh = false) => {
    setChecking(true);
    const r = await nativeApi.updates(fresh);
    setChecking(false);
    if (r.ok) {
      setUpdates(r.data.updates);
      setError(null);
    } else if (r.status === 501) setUnsupported(r.error ?? 'System apps need NoCapOS installed directly on Linux.');
    else setError(r.error ?? 'Couldn’t check Flathub for updates.');
  }, []);
  useEffect(() => {
    void load();
  }, [load]);
  return { updates, unsupported, error, checking, load };
}

export function SystemUpdates({ state }: { state: ReturnType<typeof useSystemUpdates> }) {
  const { updates, unsupported, error, checking, load } = state;
  const [jobs, setJobs] = useState<NativeJob[]>([]);
  const watching = useRef(new Set<string>());
  const running = jobs.find((j) => !j.done);

  useEffect(() => {
    void nativeApi.jobs().then((r) => r.ok && setJobs(r.data.jobs));
  }, []);
  useEffect(() => {
    if (!running) return;
    const t = window.setInterval(async () => {
      const r = await nativeApi.jobs();
      if (r.ok) setJobs(r.data.jobs);
    }, 1200);
    return () => window.clearInterval(t);
  }, [running]);
  useEffect(() => {
    let changed = false;
    for (const j of jobs) {
      if (j.action !== 'update' || !j.done || !watching.current.delete(j.id)) continue;
      changed = true;
      if (j.error) toast('error', `Couldn't update ${j.name}`, j.error);
      else toast('success', j.app === ALL_NATIVE ? 'All system apps updated — fresh' : `Big moves: ${j.name} updated`);
    }
    if (changed) {
      void load(true);
      void useNativeApps.getState().load();
    }
  }, [jobs, load]);

  const start = async (u: NativeUpdate | null) => {
    const r = u ? await nativeApi.update(u.id, u.name) : await nativeApi.updateAll();
    if (!r.ok) return toast('error', u ? `Couldn't update ${u.name}` : 'Couldn’t start the updates', r.error);
    watching.current.add(r.data.id);
    setJobs((js) => [r.data, ...js.filter((j) => j.app !== r.data.app)]);
  };
  const jobFor = (id: string) => jobs.find((j) => !j.done && (j.app === id || j.app === ALL_NATIVE));

  let body;
  if (unsupported) body = <p className="muted small">{unsupported}</p>;
  else if (error) body = <p className="muted small">{error}</p>;
  else if (!updates) body = <p className="muted small">Checking Flathub…</p>;
  else if (!updates.length) body = <p className="muted small">All system apps are up to date. No cap.</p>;
  else
    body = (
      <div className="ucard as-updates">
        {updates.map((u) => {
          const j = jobFor(u.id);
          return (
            <div key={u.id} className="as-update">
              <div className="as-row-main as-static">
                <NativeAppIcon app={{ id: u.id }} size={46} />
                <span className="as-row-text">
                  <b>{u.name}</b>
                  <span>{u.from || u.to ? `${u.from ? `v${u.from}` : 'installed'} → ${u.to ? `v${u.to}` : 'newer'}` : 'Newer version on Flathub'}</span>
                </span>
              </div>
              {j ? (
                <button type="button" className="pill as-pill busy" style={{ '--p': `${j.percent}%` } as CSSProperties} title={j.phase}>
                  <span>{j.percent}%</span>
                </button>
              ) : (
                <button type="button" className="pill as-pill" disabled={!!running} onClick={() => void start(u)}>
                  Update
                </button>
              )}
            </div>
          );
        })}
      </div>
    );

  return (
    <section className="as-section">
      <div className="as-section-head">
        <h2 className="as-h2">
          <Icon name="linux" size={17} /> System app updates {updates && updates.length > 0 && <span className="muted">{updates.length}</span>}
        </h2>
        {!unsupported && (
          <div className="as-head-actions">
            <button type="button" className="ghost pill small" disabled={checking || !!running} onClick={() => void load(true)}>
              {checking ? 'Checking…' : 'Check again'}
            </button>
            {updates && updates.length > 0 && (
              <button type="button" className="pill" disabled={!!running} onClick={() => void start(null)}>
                Update all
              </button>
            )}
          </div>
        )}
      </div>
      {body}
    </section>
  );
}
