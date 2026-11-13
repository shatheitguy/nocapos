import { useCallback, useEffect, useState } from 'react';
import { useT } from '../i18n';
import { Icon } from '../components/Icon';
import { storageApi, type Disk, type Pool, type Snapshot } from '../api/storage';
import {
  storageExtras,
  type ARCStats,
  type BootDrive,
  type BootMirrorDryRun,
  type CryptInfo,
  type FlexPlan,
  type PlanRequest,
  type PoolPlan,
  type ScrubResult,
  type SnapEntry,
  type SnapOverview,
  type SnapSchedule,
  type SpindownDisk,
  type SupportKind,
} from '../api/storageExtras';
import { fileApi, type ExternalDrive } from '../api/files';
import { netApi, type NetDrive } from '../api/netdrive';
import { api } from '../api/client';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import { openApp } from './meta';
import { DiskPicker, Donut, EraseReview, HealthChip, Meter, Sheet, SmartBadge, TypeConfirm, Working, diskTitle, fmtSize, useJob } from './StorageParts';

// The newer Storage sections: Boot drive, Snapshots, Mounts, and the extra
// pool tools (speed-boost drives, expand, scrub history, RAM cache, spin-down,
// encrypted folders, flexible array). The server checks every rule again.

type Res = { ok: boolean; error?: string };

function useAct(onChanged?: () => void) {
  const t = useT();
  const [busy, setBusy] = useState(false);
  const act = async (fn: () => Promise<Res>, done: string) => {
    setBusy(true);
    const r = await fn();
    setBusy(false);
    if (!r.ok) {
      toast('error', t('storage.toast.didntWork'), r.error);
      return false;
    }
    toast('success', done);
    onChanged?.();
    return true;
  };
  return { busy, act };
}

// ---------------- Dry run: the exact commands, before anything runs ----------------

export function Commands({ commands }: { commands: string[][] }) {
  const t = useT();
  return (
    <details className="stx-cmds">
      <summary>{t('stx.dry.commands', { n: commands.length })}</summary>
      <pre className="mono">{commands.map((c) => c.join(' ')).join('\n')}</pre>
    </details>
  );
}

/** Asks the server for a dry-run plan and shows it. */
export function DryRun({ req }: { req: PlanRequest }) {
  const t = useT();
  const [plan, setPlan] = useState<PoolPlan | null>(null);
  const [error, setError] = useState<string | null>(null);
  const key = JSON.stringify(req);
  useEffect(() => {
    let live = true;
    void storageExtras.plan(req).then((r) => {
      if (!live) return;
      if (r.ok) {
        setPlan(r.data);
        setError(null);
      } else setError(r.error ?? t('storage.toast.didntWork'));
    });
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);
  if (error) return <p className="st-note bad"><Icon name="alert" size={14} /> {error}</p>;
  if (!plan) return <p className="muted small">{t('stx.dry.loading')}</p>;
  return (
    <div className="stx-dry">
      <b className="small">{t('stx.dry.title')}</b>
      {plan.warnings.map((w) => (
        <p key={w} className="st-note warn"><Icon name="alert" size={14} /> {t(`stx.warn.${w}`)}</p>
      ))}
      <Commands commands={plan.commands} />
    </div>
  );
}

// ---------------- Boot drive ----------------

export function BootPage({ onOpenDisk, onChanged }: { onOpenDisk: (n: string) => void; onChanged: () => void }) {
  const t = useT();
  const [boot, setBoot] = useState<BootDrive | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [mirror, setMirror] = useState(false);
  const load = useCallback(() => {
    void storageExtras.boot().then((r) => (r.ok ? (setBoot(r.data), setError(null)) : setError(r.error ?? t('storage.toast.didntWork'))));
  }, [t]);
  useEffect(load, [load]);
  const resilvering = boot?.pool?.scan.state === 'scanning' && boot.pool.scan.function === 'resilver';
  useEffect(() => {
    if (!resilvering) return;
    const id = window.setInterval(load, 4000);
    return () => window.clearInterval(id);
  }, [resilvering, load]);

  if (error) return <div className="st-page"><p className="st-note bad"><Icon name="alert" size={14} /> {error}</p></div>;
  if (!boot) return <div className="st-page"><span className="st-spinner" /></div>;
  const pct = boot.size ? (boot.used / boot.size) * 100 : 0;
  const state = boot.degraded ? 'bad' : boot.mirrored ? 'good' : 'single';
  return (
    <div className="st-page">
      <header className="st-page-head">
        <div>
          <h2 className="st-h">{t('stx.boot.title')}</h2>
          <p className="app-subtitle">{t('stx.boot.subtitle')}</p>
        </div>
      </header>

      <div className="st-pool-top">
        <div className="ucard pad st-cap">
          <Donut pct={pct} size={104}>
            <b>{Math.round(pct)}%</b>
            <small>{t('stx.used')}</small>
          </Donut>
          <div className="st-cap-text">
            <span className="st-big">{fmtSize(boot.avail)} <small>{t('stx.free')}</small></span>
            <span className="muted small">{t('stx.usedOf', { used: fmtSize(boot.used), size: fmtSize(boot.size) })}</span>
            <span className="muted small mono">{boot.source} · {boot.fstype}</span>
            <span className="muted small">{boot.efi ? t('stx.boot.uefi') : boot.platform === 'pi' ? t('stx.boot.piFirmware') : t('stx.boot.bios')}</span>
          </div>
        </div>
        <div className={`ucard pad stx-mirror ${state}`}>
          <div className="row-head">
            <b><Icon name={state === 'good' ? 'shield' : 'alert'} size={15} /> {t(`stx.boot.state.${state}`)}</b>
            {boot.pool && <HealthChip health={boot.pool.health} />}
          </div>
          <p className="muted small">{t(`stx.boot.stateText.${state}`)}</p>
          {resilvering && (
            <>
              <Meter pct={boot.pool?.scan.percent ?? 0} />
              <span className="muted small">{t('stx.boot.resilver', { pct: (boot.pool?.scan.percent ?? 0).toFixed(1) })}</span>
            </>
          )}
          {boot.can_mirror ? (
            <button type="button" className={boot.mode === 'replace' ? 'danger solid' : ''} onClick={() => setMirror(true)} disabled={!boot.candidates.length}>
              <Icon name="layers" size={14} /> {boot.mode === 'replace' ? t('stx.boot.replace') : t('stx.boot.mirror')}
            </button>
          ) : (
            boot.why && boot.why !== 'already' && <p className="st-note"><Icon name="info" size={14} /> <span>{t(`stx.boot.why.${boot.why}`)}</span></p>
          )}
          {boot.can_mirror && !boot.candidates.length && <p className="muted small">{t('stx.boot.noCandidates', { size: fmtSize(boot.min_size) })}</p>}
        </div>
      </div>

      <h3 className="section-label">{t('stx.boot.drives')}</h3>
      <div className="ucard ulist">
        {boot.disks.map((d) => (
          <button key={d.name} type="button" className="urow" onClick={() => onOpenDisk(d.name)}>
            <span className="urow-icon"><Icon name="hdd" size={17} /></span>
            <span className="urow-text">
              <span className="urow-title">{diskTitle(d)}</span>
              <span className="urow-desc">{d.name} · {fmtSize(d.size)}{d.serial ? ` · ${d.serial}` : ''}</span>
            </span>
            <span className="urow-control"><SmartBadge disk={d} /></span>
          </button>
        ))}
      </div>
      <p className="muted small">{t('stx.boot.safe')}</p>

      {mirror && <MirrorBoot boot={boot} onClose={() => setMirror(false)} onDone={() => { load(); onChanged(); }} />}
    </div>
  );
}

function MirrorBoot({ boot, onClose, onDone }: { boot: BootDrive; onClose: () => void; onDone: () => void }) {
  const t = useT();
  const [disks, setDisks] = useState<Disk[]>([]);
  const [picked, setPicked] = useState<string[]>([]);
  const [plan, setPlan] = useState<BootMirrorDryRun | null>(null);
  const [confirm, setConfirm] = useState('');
  const { job, running, error, start, setError } = useJob(() => {
    onDone();
    onClose();
  });
  useEffect(() => {
    void storageApi.disks().then((r) => r.ok && setDisks(r.data.disks.filter((d) => boot.candidates.includes(d.name))));
  }, [boot.candidates]);
  useEffect(() => {
    setPlan(null);
    if (!picked[0]) return;
    void storageExtras.bootPlan(picked[0]).then((r) => (r.ok ? setPlan(r.data) : setError(r.error ?? null)));
  }, [picked, setError]);
  return (
    <Sheet
      title={boot.mode === 'replace' ? t('stx.boot.replace') : t('stx.boot.mirror')}
      icon="layers"
      wide
      onClose={onClose}
      footer={
        job ? null : (
          <>
            {error && <span className="error st-foot-error">{error}</span>}
            <button type="button" className="ghost" onClick={onClose}>{t('stx.cancel')}</button>
            <button type="button" className="danger solid" disabled={!plan || confirm !== plan.confirm || running}
              onClick={() => void start(storageExtras.bootMirror(picked[0], confirm), t('stx.boot.started'))}>
              {t('stx.boot.go')}
            </button>
          </>
        )
      }
    >
      {job ? (
        <Working text={t('stx.boot.working')} />
      ) : (
        <>
          <p className="st-lead">{t('stx.boot.lead', { size: fmtSize(boot.min_size) })}</p>
          <DiskPicker disks={disks} picked={picked} onChange={setPicked} single />
          {plan && (
            <>
              <ul className="stx-list small">
                <li>{t('stx.boot.step.copy')}</li>
                <li>{t('stx.boot.step.join', { pools: plan.plan.members.map((m) => m.pool).join(', ') })}</li>
                <li>{plan.plan.efi ? t('stx.boot.step.efi') : t('stx.boot.step.bios')}</li>
                <li>{t('stx.boot.step.resilver')}</li>
              </ul>
              <Commands commands={plan.commands} />
              <EraseReview disks={[plan.disk]} word={plan.confirm} value={confirm} onChange={setConfirm} what={t('stx.boot.erase')} />
            </>
          )}
        </>
      )}
    </Sheet>
  );
}

// ---------------- Pool extras: speed boost, expand, scrubs, RAM cache ----------------

export function PoolExtras({ pool, disks, onChanged }: { pool: Pool; disks: Disk[]; onChanged: () => void }) {
  const t = useT();
  const [hist, setHist] = useState<ScrubResult[]>([]);
  const [arc, setArc] = useState<ARCStats | null>(null);
  const [boost, setBoost] = useState(false);
  const { busy, act } = useAct(onChanged);
  useEffect(() => {
    void storageExtras.scrubs(pool.name).then((r) => r.ok && setHist(r.data));
    void storageExtras.arc().then((r) => r.ok && setArc(r.data));
  }, [pool.name, pool.scan.finished]);
  const support = pool.vdevs.filter((v) => v.type === 'cache' || v.type === 'logs' || v.type === 'special');

  const removeDrive = async (dev: string) => {
    const ok = await confirmDialog({ title: t('stx.boost.removeTitle'), message: t('stx.boost.removeBody'), confirmLabel: t('stx.remove') });
    if (ok) await act(() => storageExtras.removeSupport(pool.name, dev), t('stx.boost.removed'));
  };
  return (
    <>
      <div className="row-head st-gap">
        <h3 className="section-label">{t('stx.boost.title')}</h3>
        <button type="button" className="ghost small" onClick={() => setBoost(true)}><Icon name="sparkles" size={13} /> {t('stx.boost.add')}</button>
      </div>
      <p className="muted small st-tight">{t('stx.boost.lead')}</p>
      {support.length > 0 && (
        <div className="ucard ulist">
          {support.flatMap((g) =>
            g.children.map((c) => (
              <div key={c.path || c.name} className="urow">
                <span className="urow-icon"><Icon name="ssd" size={16} /></span>
                <span className="urow-text">
                  <span className="urow-title">{t(`stx.boost.kind.${g.type}`)}</span>
                  <span className="urow-desc mono">{(c.path || c.name).replace('/dev/disk/by-id/', '')}</span>
                </span>
                {g.type !== 'special' && (
                  <span className="urow-control">
                    <button type="button" className="ghost small" disabled={busy} onClick={() => void removeDrive(c.path || c.name)}>{t('stx.remove')}</button>
                  </span>
                )}
              </div>
            )),
          )}
        </div>
      )}

      <div className="st-pool-top">
        <div className="ucard pad">
          <div className="row-head">
            <b>{t('stx.scrub.history')}</b>
          </div>
          <p className="muted small">{t('stx.scrub.explain')}</p>
          {hist.length === 0 ? (
            <p className="muted small">{t('stx.scrub.none')}</p>
          ) : (
            <ul className="stx-list small">
              {hist.slice().reverse().map((h) => (
                <li key={h.finished}>
                  {new Date(h.finished).toLocaleDateString()} · {h.function === 'resilver' ? t('stx.scrub.resilver') : t('stx.scrub.scrub')} ·{' '}
                  <span className={h.errors ? 'error' : ''}>{h.errors ? t('stx.scrub.fixed', { n: h.errors }) : t('stx.scrub.clean')}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
        {arc && (
          <div className="ucard pad">
            <div className="row-head"><b>{t('stx.arc.title')}</b></div>
            <p className="muted small">{t('stx.arc.lead')}</p>
            <Meter pct={arc.max ? (arc.size / arc.max) * 100 : 0} />
            <span className="muted small">{t('stx.arc.size', { size: fmtSize(arc.size), max: fmtSize(arc.max) })}</span>
            <span className="muted small">{t('stx.arc.hits', { pct: arc.hit_rate.toFixed(1) })}</span>
            {arc.l2_size > 0 && <span className="muted small">{t('stx.arc.l2', { size: fmtSize(arc.l2_size) })}</span>}
          </div>
        )}
      </div>
      <div className="btn-group st-wrap-row">
        <button type="button" className="ghost small" disabled={busy} title={t('stx.expand.hint')}
          onClick={() => void act(() => storageExtras.expand(pool.name), t('stx.expand.done'))}>
          <Icon name="arrowUp" size={13} /> {t('stx.expand.button')}
        </button>
      </div>
      {boost && <AddBoost pool={pool} disks={disks} onClose={() => setBoost(false)} onDone={onChanged} />}
    </>
  );
}

function AddBoost({ pool, disks, onClose, onDone }: { pool: Pool; disks: Disk[]; onClose: () => void; onDone: () => void }) {
  const t = useT();
  const [kind, setKind] = useState<SupportKind>('cache');
  const [picked, setPicked] = useState<string[]>([]);
  const [confirm, setConfirm] = useState('');
  const { job, running, error, start } = useJob(() => {
    onDone();
    onClose();
  });
  const chosen = disks.filter((d) => picked.includes(d.name));
  return (
    <Sheet
      title={t('stx.boost.add')}
      icon="sparkles"
      wide
      onClose={onClose}
      footer={
        job ? null : (
          <>
            {error && <span className="error st-foot-error">{error}</span>}
            <button type="button" className="ghost" onClick={onClose}>{t('stx.cancel')}</button>
            <button type="button" className="danger solid" disabled={!picked.length || confirm !== 'ERASE' || running}
              onClick={() => void start(storageExtras.addSupport(pool.name, { kind, disks: picked, confirm }), t('stx.boost.added'))}>
              {t('stx.boost.go')}
            </button>
          </>
        )
      }
    >
      {job ? (
        <Working text={t('stx.boost.working')} />
      ) : (
        <>
          <div className="st-layouts">
            {(['cache', 'log', 'special'] as SupportKind[]).map((k) => (
              <button key={k} type="button" className={`st-layout ${kind === k ? 'on' : ''}`} onClick={() => setKind(k)}>
                <span className="st-layout-head"><b>{t(`stx.boost.pick.${k}`)}</b></span>
                <span className="st-layout-blurb">{t(`stx.boost.blurb.${k}`)}</span>
              </button>
            ))}
          </div>
          <DiskPicker disks={disks} picked={picked} onChange={setPicked} />
          {picked.length > 0 && (
            <>
              <DryRun req={{ action: 'support', pool: pool.name, kind, disks: picked }} />
              <EraseReview disks={chosen} word="ERASE" value={confirm} onChange={setConfirm} what={t('stx.boost.erase', { pool: pool.name })} />
            </>
          )}
        </>
      )}
    </Sheet>
  );
}

/** Scrub frequency, shown under the automatic scrub switch. */
export function ScrubEvery() {
  const t = useT();
  const [s, setS] = useState<{ auto_scrub: boolean; scrub_every?: string } | null>(null);
  useEffect(() => {
    void storageExtras.scrubSettings().then((r) => r.ok && setS(r.data));
  }, []);
  if (!s) return null;
  const set = async (every: string) => {
    const r = await storageExtras.setScrubSettings(s.auto_scrub, every);
    if (!r.ok) return toast('error', t('storage.toast.saveFail'), r.error);
    setS({ ...s, scrub_every: every });
  };
  return (
    <div className="st-field stx-every">
      <span className="small muted">{t('stx.scrub.every')}</span>
      <div className="segmented">
        {['weekly', 'monthly', 'quarterly'].map((e) => (
          <button key={e} type="button" className={(s.scrub_every ?? 'monthly') === e ? 'on' : ''} onClick={() => void set(e)}>{t(`stx.scrub.${e}`)}</button>
        ))}
      </div>
    </div>
  );
}

// ---------------- Spin-down ----------------

export function SpindownCard() {
  const t = useT();
  const [list, setList] = useState<SpindownDisk[] | null>(null);
  const [choices, setChoices] = useState<number[]>([]);
  const [needTool, setNeedTool] = useState(false);
  const load = () => void storageExtras.spindown().then((r) => r.ok && (setList(r.data.disks), setChoices(r.data.choices)));
  useEffect(load, []);
  if (!list || list.length === 0) return null;
  const set = async (d: SpindownDisk, minutes: number) => {
    const r = await storageExtras.setSpindown(d.name, minutes);
    if (!r.ok) {
      if (r.error?.includes('hdparm')) setNeedTool(true);
      return toast('error', t('storage.toast.saveFail'), r.error);
    }
    toast('success', t('stx.spin.saved'));
    load();
  };
  const install = async () => {
    const r = await storageExtras.install('hdparm');
    if (!r.ok) return toast('error', t('storage.toast.didntWork'), r.error);
    setNeedTool(false);
    toast('success', t('stx.spin.installed'));
  };
  return (
    <>
      <h3 className="section-label">{t('stx.spin.title')}</h3>
      <p className="muted small st-tight">{t('stx.spin.lead')}</p>
      {needTool && (
        <p className="st-note warn"><Icon name="alert" size={14} /> {t('stx.spin.needTool')} <button type="button" className="small" onClick={() => void install()}>{t('stx.install')}</button></p>
      )}
      <div className="ucard ulist">
        {list.map((d) => (
          <div key={d.name} className="urow">
            <span className="urow-icon"><Icon name="hdd" size={17} /></span>
            <span className="urow-text">
              <span className="urow-title">{d.model || d.name}</span>
              <span className="urow-desc">{d.name}</span>
            </span>
            <span className="urow-control">
              <select value={d.minutes} onChange={(e) => void set(d, Number(e.target.value))} aria-label={t('stx.spin.title')}>
                {choices.map((c) => (
                  <option key={c} value={c}>{c === 0 ? t('stx.spin.never') : c < 60 ? t('stx.spin.min', { n: c }) : t('stx.spin.hours', { n: c / 60 })}</option>
                ))}
              </select>
            </span>
          </div>
        ))}
      </div>
    </>
  );
}

// ---------------- Encrypted folders ----------------

export function EncryptedFolders({ pool, onChanged }: { pool: string; onChanged: () => void }) {
  const t = useT();
  const [list, setList] = useState<CryptInfo[]>([]);
  const [create, setCreate] = useState(false);
  const [unlock, setUnlock] = useState<string | null>(null);
  const load = useCallback(() => void storageExtras.encryption(pool).then((r) => r.ok && setList(r.data)), [pool]);
  useEffect(load, [load]);
  const { busy, act } = useAct(() => {
    load();
    onChanged();
  });
  const roots = list.filter((c) => c.encrypted && (!c.root || c.root === c.name));
  const autoLock = async (c: CryptInfo, minutes: number) => {
    const r = await storageExtras.autoLock(c.name, minutes);
    if (!r.ok) return toast('error', t('storage.toast.saveFail'), r.error);
    load();
  };
  return (
    <>
      <div className="row-head st-gap">
        <h3 className="section-label">{t('stx.crypt.title')}</h3>
        <button type="button" className="ghost small" onClick={() => setCreate(true)}><Icon name="lock" size={13} /> {t('stx.crypt.new')}</button>
      </div>
      <p className="muted small st-tight">{t('stx.crypt.lead')}</p>
      {roots.length > 0 && (
        <div className="ucard ulist">
          {roots.map((c) => (
            <div key={c.name} className="urow">
              <span className="urow-icon"><Icon name={c.unlocked ? 'key' : 'lock'} size={16} /></span>
              <span className="urow-text">
                <span className="urow-title mono">{c.name.slice(pool.length + 1)}</span>
                <span className="urow-desc">{c.unlocked ? t('stx.crypt.open') : t('stx.crypt.locked')}</span>
              </span>
              <span className="urow-control btn-group">
                <select value={c.auto_lock_minutes ?? 0} onChange={(e) => void autoLock(c, Number(e.target.value))} aria-label={t('stx.crypt.autoLock')} title={t('stx.crypt.autoLock')}>
                  {[0, 15, 60, 240, 1440].map((m) => (
                    <option key={m} value={m}>{m === 0 ? t('stx.crypt.noAuto') : t('stx.crypt.autoAfter', { n: m >= 60 ? `${m / 60} h` : `${m} min` })}</option>
                  ))}
                </select>
                {c.unlocked ? (
                  <button type="button" className="ghost small" disabled={busy} onClick={() => void act(() => storageExtras.lock(c.name), t('stx.crypt.lockedToast'))}>{t('stx.crypt.lock')}</button>
                ) : (
                  <button type="button" className="small" onClick={() => setUnlock(c.name)}>{t('stx.crypt.unlock')}</button>
                )}
              </span>
            </div>
          ))}
        </div>
      )}
      {create && <NewEncrypted pool={pool} onClose={() => setCreate(false)} onDone={() => { setCreate(false); load(); onChanged(); }} />}
      {unlock && <UnlockSheet name={unlock} onClose={() => setUnlock(null)} onDone={() => { setUnlock(null); load(); onChanged(); }} />}
    </>
  );
}

function NewEncrypted({ pool, onClose, onDone }: { pool: string; onClose: () => void; onDone: () => void }) {
  const t = useT();
  const [name, setName] = useState('');
  const [pass, setPass] = useState('');
  const [repeat, setRepeat] = useState('');
  const [auto, setAuto] = useState(0);
  const [understood, setUnderstood] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const nameOk = /^[a-zA-Z0-9_.:-]+$/.test(name);
  const ok = nameOk && pass.length >= 8 && pass === repeat && understood;
  const save = async () => {
    setBusy(true);
    const r = await storageExtras.createEncrypted({ name: `${pool}/${name}`, passphrase: pass, repeat, auto_lock_minutes: auto || undefined });
    setBusy(false);
    setPass('');
    setRepeat('');
    if (!r.ok) return setError(r.error ?? t('storage.toast.didntWork'));
    toast('success', t('stx.crypt.created'));
    onDone();
  };
  return (
    <Sheet title={t('stx.crypt.new')} icon="lock" onClose={onClose}
      footer={
        <>
          {error && <span className="error st-foot-error">{error}</span>}
          <button type="button" className="ghost" onClick={onClose}>{t('stx.cancel')}</button>
          <button type="button" disabled={!ok || busy} onClick={() => void save()}>{t('stx.crypt.create')}</button>
        </>
      }>
      <div className="st-form">
        <label>
          <span>{t('stx.crypt.name')}</span>
          <div className="st-prefix"><span className="mono">{pool}/</span><input value={name} onChange={(e) => setName(e.target.value.trim())} placeholder="vault" autoFocus /></div>
        </label>
        <label>
          <span>{t('stx.crypt.pass')}</span>
          <input type="password" value={pass} onChange={(e) => setPass(e.target.value)} autoComplete="new-password" />
          <small className="muted">{t('stx.crypt.passHint')}</small>
        </label>
        <label>
          <span>{t('stx.crypt.repeat')}</span>
          <input type="password" value={repeat} onChange={(e) => setRepeat(e.target.value)} autoComplete="new-password" />
          {repeat && repeat !== pass && <small className="error">{t('stx.crypt.mismatch')}</small>}
        </label>
        <label>
          <span>{t('stx.crypt.autoLock')}</span>
          <select value={auto} onChange={(e) => setAuto(Number(e.target.value))}>
            {[0, 15, 60, 240, 1440].map((m) => (
              <option key={m} value={m}>{m === 0 ? t('stx.crypt.noAuto') : t('stx.crypt.autoAfter', { n: m >= 60 ? `${m / 60} h` : `${m} min` })}</option>
            ))}
          </select>
        </label>
        <p className="st-note warn"><Icon name="alert" size={14} /> {t('stx.crypt.forget')}</p>
        <label className="st-switch-row toggle">
          <span><b>{t('stx.crypt.understood')}</b></span>
          <input type="checkbox" role="switch" checked={understood} onChange={(e) => setUnderstood(e.target.checked)} />
        </label>
      </div>
    </Sheet>
  );
}

function UnlockSheet({ name, onClose, onDone }: { name: string; onClose: () => void; onDone: () => void }) {
  const t = useT();
  const [pass, setPass] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const go = async () => {
    setBusy(true);
    const r = await storageExtras.unlock(name, pass);
    setBusy(false);
    setPass('');
    if (!r.ok) return setError(r.error ?? t('storage.toast.didntWork'));
    toast('success', t('stx.crypt.unlockedToast'));
    onDone();
  };
  return (
    <Sheet title={t('stx.crypt.unlockTitle', { name })} icon="key" onClose={onClose}
      footer={
        <>
          {error && <span className="error st-foot-error">{error}</span>}
          <button type="button" className="ghost" onClick={onClose}>{t('stx.cancel')}</button>
          <button type="button" disabled={pass.length < 8 || busy} onClick={() => void go()}>{t('stx.crypt.unlock')}</button>
        </>
      }>
      <div className="st-form">
        <label>
          <span>{t('stx.crypt.pass')}</span>
          <input type="password" value={pass} onChange={(e) => setPass(e.target.value)} autoFocus autoComplete="current-password"
            onKeyDown={(e) => e.key === 'Enter' && pass.length >= 8 && void go()} />
        </label>
      </div>
    </Sheet>
  );
}

// ---------------- Snapshots page ----------------

export function SnapshotsPage() {
  const t = useT();
  const [list, setList] = useState<SnapOverview[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const load = useCallback(() => {
    void storageExtras.snapOverview().then((r) => (r.ok ? (setList(r.data), setError(null)) : setError(r.error ?? t('storage.toast.didntWork'))));
  }, [t]);
  useEffect(load, [load]);
  const total = (list ?? []).reduce((a, o) => a + o.used, 0);
  return (
    <div className="st-page">
      <header className="st-page-head">
        <div>
          <h2 className="st-h">{t('stx.snap.title')}</h2>
          <p className="app-subtitle">{t('stx.snap.subtitle', { size: fmtSize(total) })}</p>
        </div>
      </header>
      {error && <p className="st-note bad"><Icon name="alert" size={14} /> {error}</p>}
      <p className="muted small">{t('stx.snap.lead')}</p>
      <div className="ucard ulist">
        {(list ?? []).map((o) => (
          <div key={o.dataset} className="st-ds">
            <button type="button" className="urow" onClick={() => setOpen(open === o.dataset ? null : o.dataset)}>
              <span className="urow-icon"><Icon name="camera" size={16} /></span>
              <span className="urow-text">
                <span className="urow-title mono">{o.dataset}</span>
                <span className="urow-desc">
                  {t('stx.snap.count', { n: o.count, size: fmtSize(o.used) })}
                  {o.latest ? ` · ${t('stx.snap.latest', { when: new Date(o.latest).toLocaleString() })}` : ''}
                  {o.schedule ? ` · ${t('stx.snap.auto')}` : ''}
                </span>
              </span>
              <span className="urow-control"><Icon name={open === o.dataset ? 'chevronUp' : 'chevronDown'} size={16} /></span>
            </button>
            {open === o.dataset && <DatasetSnapshots ov={o} onChanged={load} />}
          </div>
        ))}
        {list && list.length === 0 && <p className="muted small st-pad">{t('stx.snap.empty')}</p>}
      </div>
    </div>
  );
}

function DatasetSnapshots({ ov, onChanged }: { ov: SnapOverview; onChanged: () => void }) {
  const t = useT();
  const [snaps, setSnaps] = useState<Snapshot[] | null>(null);
  const [rollback, setRollback] = useState<Snapshot | null>(null);
  const [browse, setBrowse] = useState<Snapshot | null>(null);
  const [clone, setClone] = useState<Snapshot | null>(null);
  const [sched, setSched] = useState<SnapSchedule>(ov.schedule ?? { dataset: ov.dataset, hourly: 0, daily: 0, weekly: 0, monthly: 0 });
  const load = useCallback(() => void storageApi.snapshots(ov.dataset).then((r) => r.ok && setSnaps(r.data)), [ov.dataset]);
  useEffect(load, [load]);
  const refresh = () => {
    load();
    onChanged();
  };
  const take = async () => {
    const n = `manual-${new Date().toISOString().slice(0, 16).replace(/[T:]/g, '-')}`;
    const r = await storageApi.createSnapshot(ov.dataset, n);
    if (!r.ok) return toast('error', t('storage.toast.snapFail'), r.error);
    toast('success', t('storage.toast.snapTaken'));
    refresh();
  };
  const remove = async (s: Snapshot) => {
    const ok = await confirmDialog({ title: t('storage.confirm.deleteSnap'), message: t('storage.confirm.deleteSnapBody', { name: s.name }), confirmLabel: t('storage.delete'), danger: true });
    if (!ok) return;
    const r = await storageApi.deleteSnapshot(s.name);
    if (!r.ok) return toast('error', t('storage.toast.deleteFail'), r.error);
    refresh();
  };
  const saveSched = async () => {
    const r = await storageExtras.setSchedule(sched);
    if (!r.ok) return toast('error', t('storage.toast.saveFail'), r.error);
    toast('success', t('stx.snap.schedSaved'));
    onChanged();
  };
  return (
    <div className="st-snaps">
      <div className="stx-sched">
        <b className="small">{t('stx.snap.schedule')}</b>
        {(['hourly', 'daily', 'weekly', 'monthly'] as const).map((p) => (
          <label key={p} className="small">
            {t(`stx.snap.keep.${p}`)}
            <input type="number" min={0} max={500} value={sched[p]} onChange={(e) => setSched({ ...sched, [p]: Math.max(0, Math.min(500, Number(e.target.value) || 0)) })} />
          </label>
        ))}
        <button type="button" className="small" onClick={() => void saveSched()}>{t('stx.save')}</button>
      </div>
      <p className="muted small">{t('stx.snap.schedHint')}</p>
      <div className="st-snap-new">
        <button type="button" className="small" onClick={() => void take()}><Icon name="camera" size={13} /> {t('stx.snap.takeNow')}</button>
      </div>
      {snaps?.length === 0 && <p className="muted small">{t('storage.empty.snapshots', { name: ov.dataset })}</p>}
      {snaps?.slice().reverse().map((s) => (
        <div key={s.name} className="st-snap">
          <Icon name="camera" size={14} />
          <span className="st-snap-text">
            <b className="mono">{s.name.split('@')[1]}</b>
            <span className="muted small">{new Date(s.created).toLocaleString()} · {t('stx.snap.unique', { size: fmtSize(s.used) })}</span>
          </span>
          <button type="button" className="ghost small" onClick={() => setBrowse(s)}>{t('stx.snap.browse')}</button>
          <button type="button" className="ghost small" onClick={() => setClone(s)}>{t('stx.snap.clone')}</button>
          <button type="button" className="ghost small" onClick={() => setRollback(s)}>{t('stx.snap.rollback')}</button>
          <button type="button" className="icon-btn" title={t('storage.delete')} onClick={() => void remove(s)}><Icon name="trash" size={14} /></button>
        </div>
      ))}
      {rollback && (
        <TypeConfirm
          title={t('stx.snap.rollbackTitle')}
          word={rollback.name}
          action={t('stx.snap.rollback')}
          message={<><b>{t('stx.snap.rollbackWarn', { name: ov.dataset, when: new Date(rollback.created).toLocaleString() })}</b><p>{t('stx.snap.rollbackLoss')}</p></>}
          onClose={() => setRollback(null)}
          onConfirm={async () => {
            const r = await storageApi.rollback(rollback.name, rollback.name);
            if (!r.ok) return r.error ?? t('storage.toast.rollbackFail');
            toast('success', t('storage.toast.rolledBack', { name: ov.dataset }));
            setRollback(null);
            refresh();
            return null;
          }}
        />
      )}
      {browse && <BrowseSnapshot snap={browse} onClose={() => setBrowse(null)} />}
      {clone && <CloneSnapshot snap={clone} onClose={() => setClone(null)} onDone={() => { setClone(null); refresh(); }} />}
    </div>
  );
}

function BrowseSnapshot({ snap, onClose }: { snap: Snapshot; onClose: () => void }) {
  const t = useT();
  const [path, setPath] = useState('');
  const [list, setList] = useState<SnapEntry[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    setList(null);
    void storageExtras.browse(snap.name, path).then((r) => (r.ok ? (setList(r.data), setError(null)) : setError(r.error ?? null)));
  }, [snap.name, path]);
  const up = path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : '';
  return (
    <Sheet title={t('stx.snap.browseTitle', { name: snap.name.split('@')[1] })} icon="camera" wide onClose={onClose}>
      <p className="muted small mono">/{path}</p>
      <p className="muted small">{t('stx.snap.readOnly')}</p>
      {error && <p className="error">{error}</p>}
      <div className="ucard ulist">
        {path && (
          <button type="button" className="urow" onClick={() => setPath(up)}>
            <span className="urow-icon"><Icon name="chevronLeft" size={16} /></span>
            <span className="urow-text"><span className="urow-title">..</span></span>
          </button>
        )}
        {(list ?? []).map((e) =>
          e.dir ? (
            <button key={e.name} type="button" className="urow" onClick={() => setPath(path ? `${path}/${e.name}` : e.name)}>
              <span className="urow-icon"><Icon name="folder" size={16} /></span>
              <span className="urow-text"><span className="urow-title">{e.name}</span></span>
            </button>
          ) : (
            <div key={e.name} className="urow">
              <span className="urow-icon"><Icon name="file" size={16} /></span>
              <span className="urow-text">
                <span className="urow-title">{e.name}</span>
                <span className="urow-desc">{fmtSize(e.size)} · {new Date(e.modified).toLocaleString()}</span>
              </span>
            </div>
          ),
        )}
        {list?.length === 0 && <p className="muted small st-pad">{t('stx.snap.emptyFolder')}</p>}
      </div>
    </Sheet>
  );
}

function CloneSnapshot({ snap, onClose, onDone }: { snap: Snapshot; onClose: () => void; onDone: () => void }) {
  const t = useT();
  const ds = snap.name.split('@')[0];
  const pool = ds.split('/')[0];
  const [name, setName] = useState(`${ds.slice(pool.length + 1) || 'copy'}-${snap.name.split('@')[1]}`.replace(/[^a-zA-Z0-9_.:-]/g, '-'));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const ok = /^[a-zA-Z0-9_.:-]+$/.test(name);
  const go = async () => {
    setBusy(true);
    const r = await storageExtras.clone(snap.name, `${pool}/${name}`);
    setBusy(false);
    if (!r.ok) return setError(r.error ?? t('storage.toast.didntWork'));
    toast('success', t('stx.snap.cloned', { name: `${pool}/${name}` }));
    onDone();
  };
  return (
    <Sheet title={t('stx.snap.cloneTitle')} icon="copy" onClose={onClose}
      footer={
        <>
          {error && <span className="error st-foot-error">{error}</span>}
          <button type="button" className="ghost" onClick={onClose}>{t('stx.cancel')}</button>
          <button type="button" disabled={!ok || busy} onClick={() => void go()}>{t('stx.snap.clone')}</button>
        </>
      }>
      <p className="muted small">{t('stx.snap.cloneLead')}</p>
      <div className="st-form">
        <label>
          <span>{t('stx.crypt.name')}</span>
          <div className="st-prefix"><span className="mono">{pool}/</span><input value={name} onChange={(e) => setName(e.target.value.trim())} autoFocus /></div>
        </label>
      </div>
    </Sheet>
  );
}

// ---------------- Mounts: USB, network, cloud, shares ----------------

interface CloudMount {
  account_id: number;
  name: string;
  kind: string;
  path: string;
  mounted: boolean;
}

export function MountsPage() {
  const t = useT();
  const [usb, setUsb] = useState<ExternalDrive[]>([]);
  const [net, setNet] = useState<NetDrive[]>([]);
  const [cloud, setCloud] = useState<CloudMount[]>([]);
  const [busy, setBusy] = useState<string | null>(null);
  const load = useCallback(() => {
    void fileApi.external().then((r) => r.ok && setUsb(r.data));
    void netApi.overview().then((r) => r.ok && setNet(r.data.drives));
    void api<CloudMount[]>('/api/v1/cloud/mounts').then((r) => r.ok && setCloud(r.data));
  }, []);
  useEffect(load, [load]);
  useEffect(() => {
    const id = window.setInterval(load, 8000);
    return () => window.clearInterval(id);
  }, [load]);
  const run = async (key: string, fn: () => Promise<Res>, done: string) => {
    setBusy(key);
    const r = await fn();
    setBusy(null);
    if (!r.ok) return toast('error', t('storage.toast.didntWork'), r.error);
    toast('success', done);
    load();
  };
  return (
    <div className="st-page">
      <header className="st-page-head">
        <div>
          <h2 className="st-h">{t('stx.mounts.title')}</h2>
          <p className="app-subtitle">{t('stx.mounts.subtitle')}</p>
        </div>
        <button type="button" className="ghost small" onClick={() => openApp('files')}><Icon name="folder" size={13} /> {t('stx.mounts.openFiles')}</button>
      </header>

      <h3 className="section-label">{t('stx.mounts.usb')}</h3>
      <div className="ucard ulist">
        {usb.length === 0 && <p className="muted small st-pad">{t('stx.mounts.noUsb')}</p>}
        {usb.map((d) => (
          <div key={d.root} className="urow">
            <span className="urow-icon"><Icon name="usb" size={17} /></span>
            <span className="urow-text">
              <span className="urow-title">{d.name}</span>
              <span className="urow-desc">{d.device}{d.fs_type ? ` · ${d.fs_type}` : ''} · {t('stx.mounts.freeOf', { free: fmtSize(d.free), total: fmtSize(d.total) })}</span>
            </span>
            {d.ejectable && (
              <span className="urow-control">
                <button type="button" className="ghost small" disabled={busy === d.root} onClick={() => void run(d.root, () => fileApi.eject(d.path), t('stx.mounts.ejected', { name: d.name }))}>
                  <Icon name="eject" size={13} /> {t('stx.mounts.eject')}
                </button>
              </span>
            )}
          </div>
        ))}
      </div>

      <h3 className="section-label">{t('stx.mounts.network')}</h3>
      <div className="ucard ulist">
        {net.length === 0 && <p className="muted small st-pad">{t('stx.mounts.noNet')}</p>}
        {net.map((d) => (
          <div key={d.id} className="urow">
            <span className="urow-icon"><Icon name="network" size={17} /></span>
            <span className="urow-text">
              <span className="urow-title">{d.name}</span>
              <span className="urow-desc mono">{d.address}{d.error ? ` · ${d.error}` : ''}</span>
            </span>
            <span className="urow-control">
              <button type="button" className="ghost small" disabled={busy === `n${d.id}`}
                onClick={() => void run(`n${d.id}`, () => netApi.connect(d.id, !d.mounted), d.mounted ? t('stx.mounts.disconnected') : t('stx.mounts.connected'))}>
                {d.mounted ? t('stx.mounts.disconnect') : t('stx.mounts.connect')}
              </button>
            </span>
          </div>
        ))}
      </div>

      <h3 className="section-label">{t('stx.mounts.cloud')}</h3>
      <p className="muted small st-tight">{t('stx.mounts.cloudLead')}</p>
      <div className="ucard ulist">
        {cloud.length === 0 && <p className="muted small st-pad">{t('stx.mounts.noCloud')}</p>}
        {cloud.map((c) => (
          <div key={c.account_id} className="urow">
            <span className="urow-icon"><Icon name="globe" size={17} /></span>
            <span className="urow-text">
              <span className="urow-title">{c.name}</span>
              <span className="urow-desc">{c.mounted ? t('stx.mounts.openAt', { path: c.path }) : t('stx.mounts.closed')}</span>
            </span>
            <span className="urow-control">
              <button type="button" className="ghost small" disabled={busy === `c${c.account_id}`}
                onClick={() => void run(`c${c.account_id}`, () => (c.mounted
                  ? api<void>(`/api/v1/cloud/accounts/${c.account_id}/mount`, { method: 'DELETE' })
                  : api<void>(`/api/v1/cloud/accounts/${c.account_id}/mount`, { method: 'POST', body: { read_only: false } })),
                c.mounted ? t('stx.mounts.disconnected') : t('stx.mounts.connected'))}>
                {c.mounted ? t('stx.mounts.disconnect') : t('stx.mounts.openFolder')}
              </button>
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}

// ---------------- Flexible array (preview) ----------------

export function FlexArrayCard({ disks, onDone }: { disks: Disk[]; onDone: () => void }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  return (
    <>
      <div className="ucard ulist">
        <div className="urow">
          <span className="urow-icon"><Icon name="grid" size={17} /></span>
          <span className="urow-text">
            <span className="urow-title">{t('stx.flex.title')} <span className="chip tiny">{t('stx.flex.preview')}</span></span>
            <span className="urow-desc">{t('stx.flex.lead')}</span>
          </span>
          <span className="urow-control"><button type="button" className="ghost small" onClick={() => setOpen(true)}>{t('stx.flex.plan')}</button></span>
        </div>
      </div>
      {open && <FlexWizard disks={disks} onClose={() => setOpen(false)} onDone={onDone} />}
    </>
  );
}

function FlexWizard({ disks, onClose, onDone }: { disks: Disk[]; onClose: () => void; onDone: () => void }) {
  const t = useT();
  const [data, setData] = useState<string[]>([]);
  const [parity, setParity] = useState<string[]>([]);
  const [plan, setPlan] = useState<FlexPlan | null>(null);
  const [confirm, setConfirm] = useState('');
  const { job, running, error, start, setError } = useJob(() => {
    onDone();
    onClose();
  });
  useEffect(() => {
    setPlan(null);
    if (!data.length || !parity.length) return;
    void storageExtras.flexPlan(data, parity).then((r) => (r.ok ? (setPlan(r.data), setError(null)) : setError(r.error ?? null)));
  }, [data, parity, setError]);
  const free = disks.filter((d) => d.available);
  return (
    <Sheet title={t('stx.flex.title')} icon="grid" wide onClose={onClose}
      footer={
        job ? null : (
          <>
            {error && <span className="error st-foot-error">{error}</span>}
            <button type="button" className="ghost" onClick={onClose}>{t('stx.cancel')}</button>
            <button type="button" className="danger solid" disabled={!plan?.enabled || confirm !== 'ERASE' || running}
              onClick={() => void start(storageExtras.flexCreate(data, parity, confirm), t('stx.flex.done'))}>
              {t('stx.flex.go')}
            </button>
          </>
        )
      }>
      {job ? (
        <Working text={t('stx.flex.working')} />
      ) : (
        <>
          <p className="st-lead">{t('stx.flex.how')}</p>
          <b className="small">{t('stx.flex.data')}</b>
          <DiskPicker disks={free.filter((d) => !parity.includes(d.name))} picked={data} onChange={setData} />
          <b className="small">{t('stx.flex.parity')}</b>
          <DiskPicker disks={free.filter((d) => !data.includes(d.name))} picked={parity} onChange={(p) => setParity(p.slice(0, 2))} />
          {plan && (
            <>
              <div className="st-summary">
                <div><span className="muted small">{t('stx.flex.usable')}</span><b>{fmtSize(plan.usable)}</b></div>
                <div><span className="muted small">{t('stx.flex.survives')}</span><b>{t('stx.flex.failures', { n: plan.tolerance })}</b></div>
              </div>
              {plan.warnings.map((w) => <p key={w} className="st-note warn"><Icon name="alert" size={14} /> {w}</p>)}
              {!plan.enabled && <p className="st-note"><Icon name="info" size={14} /> {t('stx.flex.off')}</p>}
              {plan.enabled && (
                <EraseReview disks={disks.filter((d) => data.includes(d.name) || parity.includes(d.name))} word="ERASE" value={confirm} onChange={setConfirm} what={t('stx.flex.erase')} />
              )}
            </>
          )}
        </>
      )}
    </Sheet>
  );
}
