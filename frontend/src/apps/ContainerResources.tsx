import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import type { Container } from '../api/types';
import { dockerApi, type DockerImage, type DockerUsage, type DockerVolume, type HostDevice, type PullJob } from '../api/docker';
import { Icon } from '../components/Icon';
import { VirtualList } from '../components/VirtualList';
import { fmtAgo, fmtBytes } from '../lib/format';
import { useTopic } from '../lib/hooks';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import { tr, useT } from '../i18n';
import { openApp } from './meta';
import { TypeConfirm } from './StorageParts';
import type { DockerEvent } from '../api/types';

// Images, Volumes and Devices sections of the Containers app.

function useReloadOnEvents(type: string, load: () => void) {
  const timer = useRef<number | undefined>(undefined);
  useTopic<DockerEvent>('docker.events', (ev) => {
    if (ev.type !== type && !(type === 'image' && ev.type === 'container' && (ev.action === 'destroy' || ev.action === 'create'))) return;
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(load, 500);
  });
}

function Searchbar({ value, onChange, placeholder, children }: { value: string; onChange: (v: string) => void; placeholder: string; children?: ReactNode }) {
  return (
    <div className="ct-toolbar">
      <label className="search compact ct-search">
        <Icon name="search" size={15} />
        <input placeholder={placeholder} aria-label={placeholder} value={value} onChange={(e) => onChange(e.target.value)} />
      </label>
      {children}
    </div>
  );
}

// ---------------- images ----------------

export function ImagesSection({ usage }: { usage: DockerUsage | null }) {
  const t = useT();
  const [images, setImages] = useState<DockerImage[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState('');
  const [filter, setFilter] = useState<'all' | 'used' | 'unused'>('all');
  const [pullRef, setPullRef] = useState('');
  const [job, setJob] = useState<PullJob | null>(null);
  const [tagging, setTagging] = useState<DockerImage | null>(null);
  const [tagTo, setTagTo] = useState('');
  const [pruning, setPruning] = useState(false);

  const load = useCallback(async () => {
    const r = await dockerApi.images();
    if (!r.ok) return setError(r.error ?? tr('ctr.img.failed'));
    setError(null);
    setImages(r.data.images);
  }, []);
  useEffect(() => {
    void load();
  }, [load]);
  useReloadOnEvents('image', () => void load());

  // Follow a download until it's done.
  useEffect(() => {
    if (!job || job.done) return;
    const id = window.setTimeout(async () => {
      const r = await dockerApi.pullJob(job.id);
      if (!r.ok) return setJob({ ...job, done: true, error: r.error });
      setJob(r.data);
      if (r.data.done) {
        if (r.data.error) toast('error', tr('ctr.img.pullFail', { image: r.data.image }), r.data.error);
        else toast('success', tr('ctr.img.pulled', { image: r.data.image }));
        void load();
      }
    }, 900);
    return () => window.clearTimeout(id);
  }, [job, load]);

  const pull = async () => {
    const ref = pullRef.trim();
    if (!ref) return;
    const r = await dockerApi.pull(ref);
    if (!r.ok) return toast('error', tr('ctr.img.pullFail', { image: ref }), r.error);
    setJob({ id: r.data.job, image: ref, status: tr('ctr.img.starting'), done: false, percent: 0, layers: [] });
    setPullRef('');
  };

  const remove = async (im: DockerImage, ref: string) => {
    const ok = await confirmDialog({ title: tr('ctr.img.removeTitle', { name: ref }), message: im.tags.length > 1 && ref !== im.id ? tr('ctr.img.untagMsg') : tr('ctr.img.removeMsg'), confirmLabel: tr('ctr.act.remove'), danger: true });
    if (!ok) return;
    const r = await dockerApi.removeImage(ref);
    if (!r.ok) return toast('error', tr('ctr.img.removeFail'), r.error);
    toast('success', tr('ctr.img.removed', { name: ref }));
    void load();
  };

  const tag = async () => {
    if (!tagging) return;
    const r = await dockerApi.tag(tagging.id, tagTo.trim());
    if (!r.ok) return toast('error', tr('ctr.img.tagFail'), r.error);
    toast('success', tr('ctr.img.tagged', { name: tagTo.trim() }));
    setTagging(null);
    setTagTo('');
    void load();
  };

  const q = search.trim().toLowerCase();
  const shown = useMemo(
    () =>
      (images ?? []).filter((im) => {
        if (filter === 'used' && !im.containers.length) return false;
        if (filter === 'unused' && im.containers.length) return false;
        return !q || im.tags.some((x) => x.toLowerCase().includes(q)) || im.id.includes(q) || im.containers.some((c) => c.toLowerCase().includes(q));
      }),
    [images, filter, q],
  );
  const unused = (images ?? []).filter((im) => !im.containers.length);
  const unusedSize = unused.reduce((n, im) => n + im.size, 0);
  const total = (images ?? []).reduce((n, im) => n + (usage?.images[im.id] ?? im.size), 0);

  return (
    <div className="ct-section">
      <div className="ct-section-head">
        <h2>{t('ctr.nav.images')}</h2>
        <span className="muted small">{images ? t('ctr.img.summary', { n: images.length, size: fmtBytes(total) }) : ''}</span>
      </div>
      <form
        className="ucard pad ct-pull"
        onSubmit={(e) => {
          e.preventDefault();
          void pull();
        }}
      >
        <label className="ct-pull-row">
          <span className="small">{t('ctr.img.pull')}</span>
          <input className="mono" value={pullRef} placeholder="nginx:latest" spellCheck={false} autoCapitalize="off" onChange={(e) => setPullRef(e.target.value)} disabled={!!job && !job.done} />
        </label>
        <button type="submit" disabled={!pullRef.trim() || (!!job && !job.done)}>
          <Icon name="download" size={14} /> {t('ctr.img.pullBtn')}
        </button>
        {job && (
          <div className="ct-pull-job">
            <div className="ct-pull-line">
              <b className="mono ellipsis">{job.image}</b>
              <span className="muted small">{job.error ? job.error : job.done ? t('ctr.img.done') : `${job.status} · ${Math.round(job.percent)}%`}</span>
              {job.done && (
                <button type="button" className="ghost icon-btn" aria-label={t('ctr.close')} onClick={() => setJob(null)}>
                  <Icon name="close" size={13} />
                </button>
              )}
            </div>
            <div className={`ct-progress ${job.error ? 'bad' : ''}`} role="progressbar" aria-valuenow={Math.round(job.percent)} aria-valuemin={0} aria-valuemax={100}>
              <span style={{ width: `${Math.min(100, job.percent)}%` }} />
            </div>
          </div>
        )}
      </form>
      <Searchbar value={search} onChange={setSearch} placeholder={t('ctr.img.search')}>
        <select className="ct-select" value={filter} aria-label={t('ctr.filterBy')} onChange={(e) => setFilter(e.target.value as typeof filter)}>
          <option value="all">{t('ctr.img.all')}</option>
          <option value="used">{t('ctr.img.used')}</option>
          <option value="unused">{t('ctr.img.unused', { n: unused.length })}</option>
        </select>
        <button type="button" className="ghost small danger" disabled={!unused.length} onClick={() => setPruning(true)}>
          <Icon name="trash" size={14} /> {t('ctr.img.prune')}
        </button>
      </Searchbar>
      {error && <p className="error">{error}</p>}
      {!images ? (
        <p className="muted">{t('ctr.loading')}</p>
      ) : !shown.length ? (
        <p className="muted">{t('ctr.noMatch')}</p>
      ) : (
        <VirtualList
          className="ct-list ucard"
          label={t('ctr.nav.images')}
          count={shown.length}
          rowHeight={62}
          render={(i) => {
            const im = shown[i];
            const name = im.tags[0] ?? im.id.replace('sha256:', '').slice(0, 12);
            return (
              <div key={im.id} className="ct-row" role="listitem" style={{ height: 62 }}>
                <span className="ct-row-main static">
                  <Icon name="archiveBox" size={16} />
                  <span className="ct-row-text">
                    <span className="ct-row-line">
                      <b className="mono ellipsis" title={im.tags.join(', ')}>{name}</b>
                      {im.tags.length > 1 && <span className="chip tiny">+{im.tags.length - 1}</span>}
                      {im.dangling && <span className="chip tiny warn">{t('ctr.img.untagged')}</span>}
                    </span>
                    <span className="ct-row-line ct-row-sub muted small ellipsis">
                      {im.containers.length ? t('ctr.img.usedBy', { list: im.containers.join(', ') }) : t('ctr.img.notUsed')} · {fmtAgo(im.created)}
                    </span>
                  </span>
                </span>
                <span className="ct-row-size muted small">{fmtBytes(im.size)}</span>
                <div className="btn-group ct-quick">
                  <button type="button" className="ghost icon-btn" title={t('ctr.img.tag')} aria-label={t('ctr.img.tag')} onClick={() => { setTagging(im); setTagTo(name.includes(':') ? name : ''); }}>
                    <Icon name="pencil" size={14} />
                  </button>
                  <button type="button" className="ghost icon-btn danger" title={t('ctr.act.remove')} aria-label={t('ctr.act.remove')} disabled={im.containers.length > 0} onClick={() => void remove(im, im.tags.length > 1 ? name : im.id)}>
                    <Icon name="trash" size={14} />
                  </button>
                </div>
              </div>
            );
          }}
        />
      )}
      {tagging && (
        <div className="ucard pad ct-inline-form">
          <b>{t('ctr.img.tagTitle', { name: tagging.tags[0] ?? tagging.id.slice(7, 19) })}</b>
          <input className="mono" value={tagTo} placeholder="myapp:stable" spellCheck={false} onChange={(e) => setTagTo(e.target.value)} aria-label={t('ctr.img.tagNew')} />
          <div className="ct-inline-actions">
            <button type="button" className="ghost" onClick={() => setTagging(null)}>{t('ctr.cancel')}</button>
            <button type="button" disabled={!tagTo.trim()} onClick={() => void tag()}>{t('ctr.img.tag')}</button>
          </div>
        </div>
      )}
      {pruning && (
        <TypeConfirm
          title={t('ctr.img.pruneTitle')}
          word="prune"
          action={t('ctr.img.prune')}
          message={<p>{t('ctr.img.pruneMsg', { n: unused.length, size: fmtBytes(unusedSize) })}</p>}
          onClose={() => setPruning(false)}
          onConfirm={async () => {
            const r = await dockerApi.pruneImages(true);
            if (!r.ok) return r.error ?? tr('ctr.img.removeFail');
            toast('success', tr('ctr.img.pruned', { n: r.data.removed.length, size: fmtBytes(r.data.reclaimed) }));
            setPruning(false);
            void load();
            return null;
          }}
        />
      )}
    </div>
  );
}

// ---------------- volumes ----------------

export function VolumesSection() {
  const t = useT();
  const [vols, setVols] = useState<DockerVolume[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState('');
  const [filter, setFilter] = useState<'all' | 'used' | 'unused'>('all');
  const [pruning, setPruning] = useState(false);
  const [pruneAll, setPruneAll] = useState(false);

  const load = useCallback(async (usage = true) => {
    const r = await dockerApi.volumeDetails(usage);
    if (!r.ok) return setError(r.error ?? tr('ctr.vol.failed'));
    setError(null);
    setVols(r.data.volumes);
  }, []);
  useEffect(() => {
    // Names first (fast), then sizes (Docker may take a moment to count).
    void load(false).then(() => load(true));
  }, [load]);
  useReloadOnEvents('volume', () => void load(true));

  const remove = async (v: DockerVolume) => {
    const ok = await confirmDialog({ title: tr('ctr.vol.removeTitle', { name: v.name }), message: tr('ctr.vol.removeMsg'), confirmLabel: tr('ctr.act.remove'), danger: true });
    if (!ok) return;
    const r = await dockerApi.removeVolume(v.name);
    if (!r.ok) return toast('error', tr('ctr.vol.removeFail'), r.error);
    toast('success', tr('ctr.vol.removed', { name: v.name }));
    void load(true);
  };

  const q = search.trim().toLowerCase();
  const shown = (vols ?? []).filter((v) => {
    if (filter === 'used' && !v.containers.length) return false;
    if (filter === 'unused' && v.containers.length) return false;
    return !q || v.name.toLowerCase().includes(q) || v.containers.some((c) => c.toLowerCase().includes(q)) || !!v.project?.toLowerCase().includes(q);
  });
  const unused = (vols ?? []).filter((v) => !v.containers.length);
  const total = (vols ?? []).reduce((n, v) => n + Math.max(0, v.size), 0);

  return (
    <div className="ct-section">
      <div className="ct-section-head">
        <h2>{t('ctr.nav.volumes')}</h2>
        <span className="muted small">{vols ? t('ctr.vol.summary', { n: vols.length, size: fmtBytes(total) }) : ''}</span>
      </div>
      <Searchbar value={search} onChange={setSearch} placeholder={t('ctr.vol.search')}>
        <select className="ct-select" value={filter} aria-label={t('ctr.filterBy')} onChange={(e) => setFilter(e.target.value as typeof filter)}>
          <option value="all">{t('ctr.img.all')}</option>
          <option value="used">{t('ctr.img.used')}</option>
          <option value="unused">{t('ctr.img.unused', { n: unused.length })}</option>
        </select>
        <button type="button" className="ghost small danger" disabled={!unused.length} onClick={() => setPruning(true)}>
          <Icon name="trash" size={14} /> {t('ctr.vol.prune')}
        </button>
      </Searchbar>
      {error && <p className="error">{error}</p>}
      {!vols ? (
        <p className="muted">{t('ctr.loading')}</p>
      ) : !shown.length ? (
        <p className="muted">{vols.length ? t('ctr.noMatch') : t('ctr.vol.none')}</p>
      ) : (
        <VirtualList
          className="ct-list ucard"
          label={t('ctr.nav.volumes')}
          count={shown.length}
          rowHeight={62}
          render={(i) => {
            const v = shown[i];
            return (
              <div key={v.name} className="ct-row" role="listitem" style={{ height: 62 }}>
                <span className="ct-row-main static">
                  <Icon name="database" size={16} />
                  <span className="ct-row-text">
                    <span className="ct-row-line">
                      <b className="mono ellipsis" title={v.name}>{v.name}</b>
                      {v.project && <span className="chip tiny"><Icon name="layers" size={10} /> {v.project}</span>}
                      {v.driver !== 'local' && <span className="chip tiny">{v.driver}</span>}
                    </span>
                    <span className="ct-row-line ct-row-sub muted small ellipsis">
                      {v.containers.length ? t('ctr.img.usedBy', { list: v.containers.join(', ') }) : t('ctr.vol.notUsed')}
                    </span>
                  </span>
                </span>
                <span className="ct-row-size muted small">{v.size >= 0 ? fmtBytes(v.size) : '–'}</span>
                <div className="btn-group ct-quick">
                  {v.files_id && (
                    <button type="button" className="ghost icon-btn" title={t('ctr.vol.browse')} aria-label={t('ctr.vol.browse')} onClick={() => openApp('files', { props: { root: v.files_id ?? '', path: '/' } })}>
                      <Icon name="folder" size={14} />
                    </button>
                  )}
                  <button type="button" className="ghost icon-btn danger" title={v.containers.length ? t('ctr.vol.inUse') : t('ctr.act.remove')} aria-label={t('ctr.act.remove')} disabled={v.containers.length > 0 || v.system} onClick={() => void remove(v)}>
                    <Icon name="trash" size={14} />
                  </button>
                </div>
              </div>
            );
          }}
        />
      )}
      {pruning && (
        <TypeConfirm
          title={t('ctr.vol.pruneTitle')}
          word="prune"
          action={t('ctr.vol.prune')}
          message={
            <>
              <p>{t('ctr.vol.pruneMsg')}</p>
              <label className="ct-check">
                <input type="checkbox" checked={pruneAll} onChange={(e) => setPruneAll(e.target.checked)} />
                <span>{t('ctr.vol.pruneNamed', { n: unused.length })}</span>
              </label>
            </>
          }
          onClose={() => setPruning(false)}
          onConfirm={async () => {
            const r = await dockerApi.pruneVolumes(pruneAll);
            if (!r.ok) return r.error ?? tr('ctr.vol.removeFail');
            toast('success', tr('ctr.vol.pruned', { n: r.data.removed.length, size: fmtBytes(r.data.reclaimed) }));
            setPruning(false);
            void load(true);
            return null;
          }}
        />
      )}
    </div>
  );
}

// ---------------- devices ----------------

export function DevicesSection({ containers, onAttach }: { containers: Container[]; onAttach: (containerId: string, device: string) => void }) {
  const t = useT();
  const [devs, setDevs] = useState<HostDevice[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState('');
  const [attaching, setAttaching] = useState<HostDevice | null>(null);
  const [target, setTarget] = useState('');

  const load = useCallback(async () => {
    const r = await dockerApi.devices();
    if (!r.ok) return setError(r.error ?? tr('ctr.dev.failed'));
    setError(null);
    setDevs(r.data.devices);
  }, []);
  useEffect(() => {
    void load();
  }, [load]);

  // Containers that can be recreated from the editor (not NoCapOS's own, not part of a stack).
  const editable = containers.filter((c) => !c.system && !c.project).sort((a, b) => a.name.localeCompare(b.name));
  const q = search.trim().toLowerCase();
  const shown = (devs ?? []).filter((d) => !q || [d.path, d.by_id, d.vendor, d.product, d.vendor_id, d.product_id, ...d.used_by].some((x) => x?.toLowerCase().includes(q)));

  return (
    <div className="ct-section">
      <div className="ct-section-head">
        <h2>{t('ctr.nav.devices')}</h2>
        <button type="button" className="ghost icon-btn" title={t('ctr.refresh')} aria-label={t('ctr.refresh')} onClick={() => void load()}>
          <Icon name="restart" size={15} />
        </button>
      </div>
      <p className="muted small">{t('ctr.dev.hint')}</p>
      <Searchbar value={search} onChange={setSearch} placeholder={t('ctr.dev.search')} />
      {error && <p className="error">{error}</p>}
      {!devs ? (
        <p className="muted">{t('ctr.loading')}</p>
      ) : !shown.length ? (
        <p className="muted">{devs.length ? t('ctr.noMatch') : t('ctr.dev.none')}</p>
      ) : (
        <div className="ct-devs">
          {shown.map((d) => (
            <div key={d.path} className="ucard pad ct-dev">
              <div className="ct-dev-head">
                <Icon name={d.kind === 'usb' ? 'usb' : 'ethernet'} size={18} />
                <span className="ct-row-text">
                  <b className="ellipsis">{d.product || d.vendor || d.path}</b>
                  <span className="muted small ellipsis">
                    {[d.vendor, d.vendor_id && d.product_id ? `${d.vendor_id}:${d.product_id}` : '', d.kind === 'serial' ? t('ctr.dev.serial') : t('ctr.dev.usb')].filter(Boolean).join(' · ')}
                  </span>
                </span>
                <button type="button" className="ghost small" onClick={() => { setAttaching(d); setTarget(editable[0]?.id ?? ''); }} disabled={!editable.length}>
                  <Icon name="plus" size={13} /> {t('ctr.dev.attach')}
                </button>
              </div>
              <code className="ct-dev-flag" title={t('ctr.dev.flagHint')}>--device {d.device_flag}</code>
              <span className="muted small">
                {d.used_by.length ? t('ctr.dev.usedBy', { list: d.used_by.join(', ') }) : t('ctr.dev.free')}
              </span>
              {attaching?.path === d.path && (
                <div className="ct-inline-form">
                  <label>
                    <span className="small">{t('ctr.dev.pick')}</span>
                    <select className="ct-select" value={target} onChange={(e) => setTarget(e.target.value)}>
                      {editable.map((c) => (
                        <option key={c.id} value={c.id}>{c.name}</option>
                      ))}
                    </select>
                  </label>
                  <p className="muted small">{t('ctr.dev.attachHint')}</p>
                  <div className="ct-inline-actions">
                    <button type="button" className="ghost" onClick={() => setAttaching(null)}>{t('ctr.cancel')}</button>
                    <button type="button" disabled={!target} onClick={() => onAttach(target, d.device_flag)}>{t('ctr.dev.review')}</button>
                  </div>
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
