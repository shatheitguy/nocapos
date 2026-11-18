import { useCallback, useEffect, useRef, useState } from 'react';
import { appDataApi, sizeText, type AppDataCandidate, type AppDataJob, type AppDataOverview, type OldVolume } from '../api/appdata';
import { Icon } from '../components/Icon';
import { useT } from '../i18n';
import { pct } from '../lib/appdataPaths';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import { Section, Toggle } from './Personalize';

// The App data folder in the UI: moving apps that still keep data in Docker
// volumes into it (Files → App data, Containers, Settings), moving the
// folder itself, its SMB share, and cleaning up old volumes. Nothing moves
// without an admin's click.

/** The overview, reloaded while a move runs (and once when it ends). */
export function useAppData() {
  const [data, setData] = useState<AppDataOverview | null>(null);
  const [error, setError] = useState<string | null>(null);
  const timer = useRef(0);
  const load = useCallback(async () => {
    const r = await appDataApi.overview();
    if (!r.ok) {
      setError(r.error ?? 'error');
      return;
    }
    setError(null);
    setData(r.data);
  }, []);
  useEffect(() => void load(), [load]);
  const running = !!data?.job && !data.job.done;
  useEffect(() => {
    if (!running) return;
    const tick = async () => {
      const r = await appDataApi.job();
      if (r.ok && r.data.job) {
        const j = r.data.job;
        setData((d) => (d ? { ...d, job: j } : d));
        if (j.done) {
          void load();
          return;
        }
      }
      timer.current = window.setTimeout(() => void tick(), 1200);
    };
    timer.current = window.setTimeout(() => void tick(), 800);
    return () => window.clearTimeout(timer.current);
  }, [running, load]);
  const started = (j: AppDataJob) => setData((d) => (d ? { ...d, job: j } : d));
  return { data, error, reload: load, started };
}

/** A move's progress, or how it ended. */
export function JobLine({ job }: { job: AppDataJob }) {
  const t = useT();
  if (!job.done) {
    return (
      <div className="ad-job" role="status">
        <span className="small">{job.phase}</span>
        <div className="ad-progress" aria-hidden>
          <span style={{ width: `${pct(job.percent)}%` }} />
        </div>
      </div>
    );
  }
  return (
    <div className="ad-job" role="status">
      {job.error ? <p className="error small">{job.error}</p> : <p className="small">{t('ad.job.done')}</p>}
      {job.results?.some((r) => !r.ok) && (
        <ul className="small">
          {job.results
            .filter((r) => !r.ok)
            .map((r) => (
              <li key={r.app}>
                <b>{r.app}</b>: {r.error}
              </li>
            ))}
        </ul>
      )}
    </div>
  );
}

/** Apps still on Docker volumes, each with "Move to App data folder", and "Organize all". */
export function AppDataMoves({
  candidates,
  job,
  root,
  onStarted,
}: {
  candidates: AppDataCandidate[];
  job?: AppDataJob | null;
  root: string;
  onStarted: (j: AppDataJob) => void;
}) {
  const t = useT();
  const busy = !!job && !job.done;
  const movable = candidates.filter((c) => !c.blocked);
  if (!candidates.length && !job) return null;

  const move = async (c: AppDataCandidate) => {
    const ok = await confirmDialog({
      title: t('ad.move.confirmTitle', { app: c.app }),
      message: t('ad.move.confirmText', { app: c.app, dirs: c.volumes.map((v) => v.dir).join(', ') }),
      confirmLabel: t('ad.move.button'),
    });
    if (!ok) return;
    const r = await appDataApi.move(c.app);
    if (!r.ok) return toast('error', t('ad.move.fail', { app: c.app }), r.error);
    onStarted(r.data);
  };
  const all = async () => {
    const ok = await confirmDialog({
      title: t('ad.all.confirmTitle', { n: movable.length }),
      message: t('ad.all.confirmText', { root }),
      confirmLabel: t('ad.all.button'),
    });
    if (!ok) return;
    const r = await appDataApi.organizeAll();
    if (!r.ok) return toast('error', t('ad.all.fail'), r.error);
    onStarted(r.data);
  };

  return (
    <div className="ad-banner">
      <div className="ad-banner-head">
        <span>
          <b>{t('ad.cands.title', { n: candidates.length })}</b>
          <br />
          <span className="muted small">{t('ad.cands.hint', { root })}</span>
        </span>
        {movable.length > 1 && (
          <button type="button" disabled={busy} onClick={() => void all()}>
            <Icon name="folder" size={14} /> {t('ad.all.button')}
          </button>
        )}
      </div>
      {job && <JobLine job={job} />}
      <div className="ad-cands">
        {candidates.map((c) => (
          <div key={c.app} className="ad-cand">
            <span className="ad-cand-text">
              <span>
                <b>{c.app}</b> <span className="muted small">{t(`ad.kind.${c.kind}`)}</span>
              </span>
              <span className="muted small" title={c.volumes.map((v) => `${v.volume} → ${v.dir}`).join('\n')}>
                {c.blocked ? c.blocked : c.volumes.map((v) => `${v.volume} → ${v.dir}`).join(' · ')}
              </span>
            </span>
            {!c.blocked && (
              <button type="button" className="ghost" disabled={busy} onClick={() => void move(c)}>
                {t('ad.move.button')}
              </button>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

/** Settings → Containers → App data. */
export function AppDataSettings() {
  const t = useT();
  const { data, error, reload, started } = useAppData();
  const [path, setPath] = useState('');
  const [olds, setOlds] = useState<OldVolume[] | null>(null);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);

  const loadOld = useCallback(async () => {
    const r = await appDataApi.oldVolumes();
    setOlds(r.ok ? r.data.volumes : []);
  }, []);
  useEffect(() => void loadOld(), [loadOld]);
  useEffect(() => {
    if (data?.job?.done) void loadOld();
  }, [data?.job?.done, loadOld]);

  if (error) return <p className="error">{error}</p>;
  if (!data) return <span className="spinner" />;
  if (!data.available || !data.state) return <p className="muted">{t('ad.off')}</p>;
  const st = data.state;
  const running = !!data.job && !data.job.done;

  const moveRoot = async () => {
    const to = path.trim();
    if (!to) return;
    const ok = await confirmDialog({
      title: t('ad.root.confirmTitle'),
      message: t('ad.root.confirmText', { from: st.root, to }),
      confirmLabel: t('ad.root.move'),
    });
    if (!ok) return;
    const r = await appDataApi.setRoot(to);
    if (!r.ok) return toast('error', t('ad.root.fail'), r.error);
    setPath('');
    started(r.data);
  };
  const oldRoot = async (action: 'remove' | 'keep') => {
    if (action === 'remove') {
      const ok = await confirmDialog({ title: t('ad.oldRoot.confirmTitle'), message: t('ad.oldRoot.confirmText', { path: st.old_root ?? '' }), danger: true, confirmLabel: t('ad.oldRoot.remove'), typed: 'delete' });
      if (!ok) return;
    }
    setBusy(true);
    const r = await appDataApi.oldRoot(action);
    setBusy(false);
    if (!r.ok) return toast('error', t('ad.oldRoot.fail'), r.error);
    void reload();
  };
  const share = async (on: boolean) => {
    setBusy(true);
    const r = await appDataApi.share(on);
    setBusy(false);
    if (!r.ok) return toast('error', t('ad.share.fail'), r.error);
    if (r.data.warning) toast('info',t('ad.share.warn'), r.data.warning);
    void reload();
  };
  const cleanup = async () => {
    const list = [...picked];
    const total = (olds ?? []).filter((o) => picked.has(o.volume)).reduce((a, o) => a + o.size, 0);
    const ok = await confirmDialog({
      title: t('ad.old.confirmTitle', { n: list.length }),
      message: t('ad.old.confirmText', { size: sizeText(total) }),
      danger: true,
      confirmLabel: t('ad.old.remove'),
    });
    if (!ok) return;
    setBusy(true);
    const r = await appDataApi.removeOldVolumes(list);
    setBusy(false);
    if (!r.ok) toast('error', t('ad.old.fail'), r.error);
    else if (r.data.error) toast('error', t('ad.old.fail'), r.data.error);
    else toast('success', t('ad.old.done', { n: r.data.removed.length }));
    setPicked(new Set());
    void loadOld();
  };

  return (
    <>
      <Section title={t('ad.title')} hint={t('ad.hint')}>
        <div className="settings-row">
          <span className="settings-row-text">
            <span className="settings-row-label">{t('ad.root.label')}</span>
            <span className="settings-row-hint mono">{st.root}</span>
          </span>
        </div>
        <div className="settings-row ad-root-row">
          <input value={path} spellCheck={false} placeholder={t('ad.root.placeholder')} aria-label={t('ad.root.newLabel')} disabled={running} onChange={(e) => setPath(e.target.value)} />
          <button type="button" disabled={running || !path.trim()} onClick={() => void moveRoot()}>
            {t('ad.root.move')}
          </button>
        </div>
        <p className="muted small">{t('ad.root.hint')}</p>
        {data.job && <JobLine job={data.job} />}
        {st.old_root && (
          <div className="settings-row">
            <span className="settings-row-text">
              <span className="settings-row-label">{t('ad.oldRoot.label')}</span>
              <span className="settings-row-hint">{t('ad.oldRoot.hint', { path: st.old_root })}</span>
            </span>
            <span className="ad-root-row">
              <button type="button" className="ghost" disabled={busy || running} onClick={() => void oldRoot('keep')}>
                {t('ad.oldRoot.keep')}
              </button>
              <button type="button" className="danger-solid" disabled={busy || running} onClick={() => void oldRoot('remove')}>
                {t('ad.oldRoot.remove')}
              </button>
            </span>
          </div>
        )}
      </Section>

      <Section title={t('ad.share.title')} hint={t('ad.share.hint')}>
        <Toggle label={t('ad.share.toggle')} hint={t('ad.share.toggleHint')} checked={!!data.share} disabled={busy} onChange={(v) => void share(v)} />
      </Section>

      {!!data.candidates?.length && (
        <Section title={t('ad.organize.title')} hint={t('ad.organize.hint')}>
          <AppDataMoves candidates={data.candidates} job={data.job} root={st.root} onStarted={started} />
        </Section>
      )}

      <Section title={t('ad.old.title')} hint={t('ad.old.hint')}>
        {olds === null ? (
          <span className="spinner" />
        ) : olds.length === 0 ? (
          <p className="muted small">{t('ad.old.none')}</p>
        ) : (
          <div className="ad-old">
            {olds.map((o) => (
              <label key={o.volume} className="ad-old-row">
                <input
                  type="checkbox"
                  disabled={o.in_use}
                  checked={picked.has(o.volume)}
                  onChange={(e) => setPicked((p) => {
                    const n = new Set(p);
                    if (e.target.checked) n.add(o.volume);
                    else n.delete(o.volume);
                    return n;
                  })}
                />
                <span className="grow">
                  <span className="mono">{o.volume}</span> <span className="muted small">→ {o.dir}</span>
                </span>
                <span className="muted small">{o.in_use ? t('ad.old.inUse') : sizeText(o.size)}</span>
              </label>
            ))}
            <div className="ad-root-row">
              <button type="button" className="danger-solid" disabled={!picked.size || busy} onClick={() => void cleanup()}>
                {t('ad.old.remove')}
              </button>
            </div>
          </div>
        )}
      </Section>
    </>
  );
}
