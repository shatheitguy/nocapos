import { useCallback, useEffect, useMemo, useState } from 'react';
import { Icon, type IconName } from '../components/Icon';
import {
  KIND_LABEL,
  diskKind,
  fmtEta,
  poolHealth,
  smartHealth,
  storageApi,
  type Dataset,
  type Disk,
  type ImportablePool,
  type Pool,
  type Snapshot,
  type StorageStatus,
  type Vdev,
} from '../api/storage';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import type { WinState } from '../state/windows';
import { openApp } from './meta';
import {
  AddDisks,
  CreatePoolWizard,
  DiskSheet,
  Donut,
  FormatDisk,
  HealthChip,
  Meter,
  ReplaceDisk,
  Sheet,
  SmartBadge,
  TypeConfirm,
  UsageBadge,
  diskIcon,
  diskTitle,
  fmtSize,
} from './StorageParts';

// Storage: every disk and its health, ZFS pools (RAID), datasets and
// snapshots, and single disks used as storage. Admin only. All the safety
// checks are repeated by the server; the UI just explains them early.

type View = { tab: 'overview' | 'disks' | 'pools' } | { tab: 'pool'; pool: string };

type Modal =
  | { kind: 'create' }
  | { kind: 'disk'; name: string }
  | { kind: 'format'; name: string }
  | { kind: 'add'; pool: string }
  | { kind: 'replace'; pool: string; old: Vdev }
  | { kind: 'destroy'; pool: string }
  | null;

export function Storage(_props: { win: WinState }) {
  const [status, setStatus] = useState<StorageStatus | null>(null);
  const [disks, setDisks] = useState<Disk[]>([]);
  const [pools, setPools] = useState<Pool[]>([]);
  const [importable, setImportable] = useState<ImportablePool[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [view, setView] = useState<View>({ tab: 'overview' });
  const [modal, setModal] = useState<Modal>(null);
  const [installing, setInstalling] = useState<string | null>(null);

  const load = useCallback(async () => {
    const st = await storageApi.status();
    if (!st.ok) {
      setError(st.error ?? 'Couldn’t reach NoCapOS');
      setLoaded(true);
      return;
    }
    setStatus(st.data);
    if (!st.data.supported) {
      setLoaded(true);
      return;
    }
    const [d, p] = await Promise.all([
      storageApi.disks(),
      st.data.zfs.installed && st.data.zfs.module ? storageApi.pools() : Promise.resolve(null),
    ]);
    if (d.ok) setDisks(d.data.disks);
    else setError(d.error ?? 'Couldn’t list disks');
    if (p?.ok) {
      setPools(p.data.pools);
      setImportable(p.data.importable);
    } else if (p) setError(p.error ?? 'Couldn’t read pools');
    else {
      setPools([]);
      setImportable([]);
    }
    if (d.ok && (!p || p.ok)) setError(null);
    setLoaded(true);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  // Refresh: quickly while a scrub/resilver runs, slowly otherwise.
  const scanning = pools.some((p) => p.scan.state === 'scanning');
  useEffect(() => {
    if (!status?.supported) return;
    const t = window.setInterval(() => void load(), scanning ? 4000 : 20000);
    return () => window.clearInterval(t);
  }, [load, scanning, status?.supported]);

  const install = async (tool: 'zfs' | 'smart') => {
    setInstalling(tool);
    const r = await storageApi.install(tool);
    setInstalling(null);
    if (!r.ok) return toast('error', tool === 'zfs' ? 'Couldn’t install ZFS' : 'Couldn’t install SMART tools', r.error);
    toast('success', tool === 'zfs' ? 'ZFS is installed' : 'SMART tools are installed');
    void load();
  };

  const byName = useMemo(() => new Map(disks.map((d) => [d.name, d])), [disks]);
  const pool = view.tab === 'pool' ? pools.find((p) => p.name === view.pool) : undefined;
  const reload = () => void load();

  if (!loaded) {
    return (
      <div className="st-app st-center">
        <span className="st-spinner" />
      </div>
    );
  }
  if (status && !status.supported) return <Unsupported reason={status.reason} />;

  const nav: { id: 'overview' | 'disks' | 'pools'; label: string; icon: IconName; count?: number }[] = [
    { id: 'overview', label: 'Overview', icon: 'database' },
    { id: 'disks', label: 'Disks', icon: 'hdd', count: disks.length },
    { id: 'pools', label: 'Pools', icon: 'layers', count: pools.length },
  ];

  return (
    <div className="st-app app-split">
      <nav className="sidebar st-side">
        <div className="st-side-head">
          <h1 className="app-title">Storage</h1>
          {status?.demo && <span className="chip warn st-demo" title="Made-up disks for trying the app; nothing real is changed">Demo data</span>}
        </div>
        {nav.map((n) => (
          <button key={n.id} type="button" className={view.tab === n.id || (n.id === 'pools' && view.tab === 'pool') ? 'on' : ''} onClick={() => setView({ tab: n.id })} title={n.label}>
            <Icon name={n.icon} size={16} />
            <span className="st-nav-label">{n.label}</span>
            {n.count != null && n.count > 0 && <span className="st-count">{n.count}</span>}
          </button>
        ))}
        {pools.length > 0 && <span className="section-label st-side-label">Pools</span>}
        {pools.map((p) => (
          <button key={p.name} type="button" className={`st-side-pool ${view.tab === 'pool' && view.pool === p.name ? 'on' : ''}`} onClick={() => setView({ tab: 'pool', pool: p.name })} title={p.name}>
            <span className={`st-dot ${poolHealth(p.health)}`} />
            <span className="st-nav-label">{p.name}</span>
          </button>
        ))}
      </nav>
      <main className="app-content st-main">
        {error && (
          <div className="st-note bad">
            <Icon name="alert" size={14} /> {error}
          </div>
        )}
        {view.tab === 'overview' && status && (
          <Overview status={status} disks={disks} pools={pools} importable={importable} installing={installing} onInstall={install}
            onOpenPool={(n) => setView({ tab: 'pool', pool: n })} onOpenDisk={(n) => setModal({ kind: 'disk', name: n })}
            onCreate={() => setModal({ kind: 'create' })} onChanged={reload} />
        )}
        {view.tab === 'disks' && <DisksView disks={disks} onOpen={(n) => setModal({ kind: 'disk', name: n })} onCreate={() => setModal({ kind: 'create' })} zfs={!!status?.zfs.installed} />}
        {view.tab === 'pools' && status && (
          <PoolsView status={status} pools={pools} importable={importable} disks={disks} onOpen={(n) => setView({ tab: 'pool', pool: n })}
            onCreate={() => setModal({ kind: 'create' })} onChanged={reload} installing={installing} onInstall={install} />
        )}
        {view.tab === 'pool' &&
          (pool ? (
            <PoolPage key={pool.name} pool={pool} byName={byName} onChanged={reload}
              onAdd={() => setModal({ kind: 'add', pool: pool.name })}
              onReplace={(old) => setModal({ kind: 'replace', pool: pool.name, old })}
              onDestroy={() => setModal({ kind: 'destroy', pool: pool.name })}
              onGone={() => setView({ tab: 'pools' })} />
          ) : (
            <p className="muted">That pool is gone.</p>
          ))}
      </main>

      {modal?.kind === 'create' && <CreatePoolWizard disks={disks} onClose={() => setModal(null)} onDone={reload} />}
      {modal?.kind === 'disk' && byName.get(modal.name) && (
        <DiskSheet disk={byName.get(modal.name)!} smartInstalled={!!status?.smart.installed} onClose={() => setModal(null)}
          onFormat={() => setModal({ kind: 'format', name: modal.name })} onChanged={reload} />
      )}
      {modal?.kind === 'format' && byName.get(modal.name) && <FormatDisk disk={byName.get(modal.name)!} onClose={() => setModal(null)} onDone={reload} />}
      {modal?.kind === 'add' && pools.find((p) => p.name === modal.pool) && (
        <AddDisks pool={pools.find((p) => p.name === modal.pool)!} disks={disks} onClose={() => setModal(null)} onDone={reload} />
      )}
      {modal?.kind === 'replace' && pools.find((p) => p.name === modal.pool) && (
        <ReplaceDisk pool={pools.find((p) => p.name === modal.pool)!} old={modal.old} oldLabel={leafLabel(modal.old, byName)} disks={disks}
          onClose={() => setModal(null)} onDone={reload} />
      )}
      {modal?.kind === 'destroy' && (
        <TypeConfirm
          title={`Destroy pool ${modal.pool}?`}
          word={modal.pool}
          action="Destroy pool"
          message={
            <>
              <b>Every file, dataset and snapshot in {modal.pool} will be deleted for good.</b>
              <p>Its disks become free to reuse. This can’t be undone. To move the pool to another computer instead, use Export.</p>
            </>
          }
          onClose={() => setModal(null)}
          onConfirm={async () => {
            const r = await storageApi.destroyPool(modal.pool, modal.pool);
            if (!r.ok) return r.error ?? 'Couldn’t destroy the pool';
            toast('success', `Pool ${modal.pool} destroyed`);
            setModal(null);
            setView({ tab: 'pools' });
            reload();
            return null;
          }}
        />
      )}
    </div>
  );
}

function leafLabel(v: Vdev, byName: Map<string, Disk>): string {
  const d = v.disk ? byName.get(v.disk) : undefined;
  if (d) return `${diskTitle(d)} (${d.name})`;
  const p = v.was || v.path || v.name;
  return p.replace('/dev/disk/by-id/', '').replace(/-part\d+$/, '');
}

// ---------------- Unsupported ----------------

function Unsupported({ reason }: { reason?: string }) {
  return (
    <div className="st-app st-unsupported">
      <div className="st-unsupported-card">
        <span className="st-hero-icon"><Icon name="hdd" size={34} /></span>
        <h1 className="app-title">Storage</h1>
        <p className="st-lead">{reason}</p>
        <div className="ucard ulist">
          {[
            ['hdd', 'Every disk at a glance', 'Model, size, what it’s used for, and its health from SMART.'] as const,
            ['layers', 'RAID with ZFS', 'Combine disks into a pool that survives failed disks: Mirror, RAIDZ1, RAIDZ2 or RAIDZ3.'] as const,
            ['camera', 'Snapshots', 'Freeze a dataset in time and roll back after a mistake.'] as const,
            ['drive', 'Single-disk storage', 'Format a spare disk and use it in Files.'] as const,
          ].map(([icon, title, desc]) => (
            <div key={title} className="urow">
              <span className="urow-icon"><Icon name={icon} size={17} /></span>
              <span className="urow-text">
                <span className="urow-title">{title}</span>
                <span className="urow-desc">{desc}</span>
              </span>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

// ---------------- Overview ----------------

function ToolsCard({ status, installing, onInstall }: { status: StorageStatus; installing: string | null; onInstall: (t: 'zfs' | 'smart') => void }) {
  const items: { tool: 'zfs' | 'smart'; title: string; desc: string; can: boolean }[] = [];
  if (!status.zfs.installed || !status.zfs.module)
    items.push({
      tool: 'zfs',
      title: status.zfs.installed ? 'ZFS isn’t loaded' : 'Install ZFS for RAID pools',
      desc: status.zfs.can_install ? 'OpenZFS combines disks into pools that survive disk failures.' : 'Install OpenZFS with your distribution’s instructions, then reopen this app.',
      can: status.zfs.can_install,
    });
  if (!status.smart.installed)
    items.push({ tool: 'smart', title: 'Install SMART tools', desc: 'smartmontools reads each disk’s own health report and warns before it fails.', can: status.smart.can_install });
  if (!items.length) return null;
  return (
    <div className="ucard ulist">
      {items.map((i) => (
        <div key={i.tool} className="urow">
          <span className="urow-icon"><Icon name={i.tool === 'zfs' ? 'layers' : 'shield'} size={17} /></span>
          <span className="urow-text">
            <span className="urow-title">{i.title}</span>
            <span className="urow-desc">{i.desc}</span>
          </span>
          {i.can && (
            <span className="urow-control">
              <button type="button" className="small" disabled={!!installing} onClick={() => onInstall(i.tool)}>
                {installing === i.tool ? 'Installing…' : 'Install'}
              </button>
            </span>
          )}
        </div>
      ))}
    </div>
  );
}

function Overview({ status, disks, pools, importable, installing, onInstall, onOpenPool, onOpenDisk, onCreate, onChanged }: {
  status: StorageStatus; disks: Disk[]; pools: Pool[]; importable: ImportablePool[]; installing: string | null;
  onInstall: (t: 'zfs' | 'smart') => void; onOpenPool: (n: string) => void; onOpenDisk: (n: string) => void; onCreate: () => void; onChanged: () => void;
}) {
  const warnings: { key: string; level: 'warn' | 'bad'; title: string; body: string; onClick: () => void }[] = [];
  for (const p of pools) {
    if (p.health !== 'ONLINE')
      warnings.push({ key: `p${p.name}`, level: p.health === 'DEGRADED' ? 'warn' : 'bad', title: `Pool ${p.name} is ${p.health.toLowerCase()}`,
        body: p.health === 'DEGRADED' ? 'A disk failed or is missing. Your data is safe for now; replace the disk soon.' : 'ZFS can’t use this pool right now.', onClick: () => onOpenPool(p.name) });
    else if (p.capacity >= 80)
      warnings.push({ key: `c${p.name}`, level: p.capacity >= 90 ? 'bad' : 'warn', title: `Pool ${p.name} is ${p.capacity}% full`, body: 'ZFS slows down when nearly full. Free up space or add disks.', onClick: () => onOpenPool(p.name) });
  }
  for (const d of disks) {
    const h = smartHealth(d.smart);
    if (h.level === 'bad' || h.level === 'warn')
      warnings.push({ key: `d${d.name}`, level: h.level, title: `${diskTitle(d)} ${h.level === 'bad' ? 'is failing' : 'shows wear'}`,
        body: d.smart?.passed === false ? 'Its SMART health check failed. Back up and replace it.' : `${d.smart?.reallocated ?? 0} reallocated sectors${d.smart?.pending ? `, ${d.smart.pending} pending` : ''}. Keep backups current.`, onClick: () => onOpenDisk(d.name) });
  }
  const free = disks.filter((d) => d.available);
  const groups: { id: string; label: string; size: number }[] = [
    { id: 'pool', label: 'Pools', size: 0 },
    { id: 'storage', label: 'Storage disks', size: 0 },
    { id: 'system', label: 'System', size: 0 },
    { id: 'used', label: 'Other use', size: 0 },
    { id: 'free', label: 'Free', size: 0 },
  ];
  for (const d of disks) {
    const g = d.available ? 'free' : d.usage === 'pool' || d.usage === 'storage' || d.usage === 'system' ? d.usage : 'used';
    groups.find((x) => x.id === g)!.size += d.size;
  }
  const raw = groups.reduce((a, g) => a + g.size, 0);
  const healthy = pools.filter((p) => p.health === 'ONLINE').length;
  const okDisks = disks.filter((d) => smartHealth(d.smart).level === 'good').length;

  return (
    <div className="st-page">
      <header className="st-page-head">
        <div>
          <h2 className="st-h">Overview</h2>
          <p className="app-subtitle">{disks.length} disks · {fmtSize(raw)} in total</p>
        </div>
        {status.zfs.installed && (
          <button type="button" onClick={onCreate} disabled={!free.length} title={free.length ? '' : 'No free disks'}>
            <Icon name="plus" size={14} /> Create pool
          </button>
        )}
      </header>

      <div className="st-chips">
        {pools.length > 0 && <span className={`chip ${healthy === pools.length ? 'good' : 'warn'}`}>{healthy}/{pools.length} pools healthy</span>}
        {status.smart.installed && <span className={`chip ${okDisks === disks.length ? 'good' : 'warn'}`}>{okDisks}/{disks.length} disks healthy</span>}
        <span className="chip">{free.length} free disk{free.length === 1 ? '' : 's'}</span>
        {status.zfs.version && <span className="chip">ZFS {status.zfs.version}</span>}
      </div>

      {warnings.length > 0 && (
        <div className="st-warnings">
          {warnings.map((w) => (
            <button key={w.key} type="button" className={`st-warning ${w.level}`} onClick={w.onClick}>
              <Icon name="alert" size={18} />
              <span>
                <b>{w.title}</b>
                <span>{w.body}</span>
              </span>
              <Icon name="chevronRight" size={16} />
            </button>
          ))}
        </div>
      )}

      <ToolsCard status={status} installing={installing} onInstall={onInstall} />

      {pools.length > 0 && (
        <>
          <h3 className="section-label">Pools</h3>
          <div className="st-pool-cards">
            {pools.map((p) => (
              <button key={p.name} type="button" className="ucard st-pool-card" onClick={() => onOpenPool(p.name)}>
                <Donut pct={p.size ? (p.allocated / p.size) * 100 : 0}>
                  <b>{p.size ? Math.round((p.allocated / p.size) * 100) : 0}%</b>
                </Donut>
                <span className="st-pool-card-text">
                  <span className="st-pool-card-name">{p.name}</span>
                  <span className="muted small">{fmtSize(p.allocated)} of {fmtSize(p.size)}</span>
                  <span className="st-chips">
                    <HealthChip health={p.health} />
                    <span className="chip">{layoutLabel(p.layout)}</span>
                    {p.scan.state === 'scanning' && <span className="chip">{p.scan.function === 'resilver' ? 'Resilvering' : 'Scrubbing'} {Math.floor(p.scan.percent ?? 0)}%</span>}
                  </span>
                </span>
              </button>
            ))}
          </div>
        </>
      )}

      <h3 className="section-label">All disks</h3>
      <div className="ucard pad">
        <div className="st-stack-bar">
          {groups.filter((g) => g.size > 0).map((g) => (
            <span key={g.id} className={`seg ${g.id}`} style={{ flexGrow: g.size }} title={`${g.label}: ${fmtSize(g.size)}`} />
          ))}
        </div>
        <div className="st-legend">
          {groups.filter((g) => g.size > 0).map((g) => (
            <span key={g.id}><i className={`seg ${g.id}`} />{g.label} <b>{fmtSize(g.size)}</b></span>
          ))}
        </div>
      </div>

      {importable.length > 0 && <ImportCard importable={importable} onChanged={onChanged} />}
    </div>
  );
}

const layoutLabel = (l: string) => ({ stripe: 'Stripe', mirror: 'Mirror', raidz1: 'RAIDZ1', raidz2: 'RAIDZ2', raidz3: 'RAIDZ3', mixed: 'Mixed', draid: 'dRAID' })[l] ?? l;

function ImportCard({ importable, onChanged }: { importable: ImportablePool[]; onChanged: () => void }) {
  const [busy, setBusy] = useState<string | null>(null);
  const doImport = async (p: ImportablePool) => {
    setBusy(p.name);
    const r = await storageApi.importPool(p.id || p.name);
    setBusy(null);
    if (!r.ok) return toast('error', `Couldn’t import ${p.name}`, r.error);
    toast('success', `Pool ${p.name} imported`);
    onChanged();
  };
  return (
    <>
      <h3 className="section-label">Found on connected disks</h3>
      <div className="ucard ulist">
        {importable.map((p) => (
          <div key={p.id || p.name} className="urow">
            <span className="urow-icon"><Icon name="layers" size={17} /></span>
            <span className="urow-text">
              <span className="urow-title">{p.name} <HealthChip health={p.health} /></span>
              <span className="urow-desc">A pool from before (or another computer) on {p.disks.join(', ') || 'these disks'}. Import it to use it here.</span>
            </span>
            <span className="urow-control">
              <button type="button" className="small" disabled={!!busy} onClick={() => void doImport(p)}>{busy === p.name ? 'Importing…' : 'Import'}</button>
            </span>
          </div>
        ))}
      </div>
    </>
  );
}

// ---------------- Disks ----------------

function DisksView({ disks, onOpen, onCreate, zfs }: { disks: Disk[]; onOpen: (n: string) => void; onCreate: () => void; zfs: boolean }) {
  return (
    <div className="st-page">
      <header className="st-page-head">
        <div>
          <h2 className="st-h">Disks</h2>
          <p className="app-subtitle">{disks.filter((d) => d.available).length} of {disks.length} free to use</p>
        </div>
        {zfs && (
          <button type="button" onClick={onCreate} disabled={!disks.some((d) => d.available)}>
            <Icon name="plus" size={14} /> Create pool
          </button>
        )}
      </header>
      <div className="st-disk-grid">
        {disks.map((d) => (
          <button key={d.name} type="button" className={`ucard st-disk ${d.available ? 'free' : ''}`} onClick={() => onOpen(d.name)}>
            <span className={`st-disk-icon ${diskKind(d)}`}><Icon name={diskIcon(d)} size={26} /></span>
            <span className="st-disk-text">
              <span className="st-disk-model" title={diskTitle(d)}>{diskTitle(d)}</span>
              <span className="st-disk-size">{fmtSize(d.size)} <span className="muted">{KIND_LABEL[diskKind(d)]}</span></span>
              <span className="st-disk-serial">{d.name}{d.serial ? ` · ${d.serial}` : ''}</span>
              <span className="st-chips">
                <UsageBadge disk={d} />
                {d.smart && <SmartBadge disk={d} />}
              </span>
              {!d.available && d.reason && <span className="st-disk-reason">{d.reason}</span>}
            </span>
          </button>
        ))}
      </div>
    </div>
  );
}

// ---------------- Pools ----------------

function PoolsView({ status, pools, importable, disks, onOpen, onCreate, onChanged, installing, onInstall }: {
  status: StorageStatus; pools: Pool[]; importable: ImportablePool[]; disks: Disk[]; onOpen: (n: string) => void; onCreate: () => void;
  onChanged: () => void; installing: string | null; onInstall: (t: 'zfs' | 'smart') => void;
}) {
  const [auto, setAuto] = useState<boolean | null>(null);
  useEffect(() => {
    void storageApi.settings().then((r) => r.ok && setAuto(r.data.auto_scrub));
  }, []);
  const setAutoScrub = async (v: boolean) => {
    setAuto(v);
    const r = await storageApi.setSettings(v);
    if (!r.ok) {
      setAuto(!v);
      toast('error', 'Couldn’t save', r.error);
    }
  };
  const ready = status.zfs.installed && status.zfs.module;
  return (
    <div className="st-page">
      <header className="st-page-head">
        <div>
          <h2 className="st-h">Pools</h2>
          <p className="app-subtitle">ZFS pools combine disks so your files survive a failed disk</p>
        </div>
        {ready && (
          <button type="button" onClick={onCreate} disabled={!disks.some((d) => d.available)}>
            <Icon name="plus" size={14} /> Create pool
          </button>
        )}
      </header>
      <ToolsCard status={status} installing={installing} onInstall={onInstall} />
      {ready && pools.length === 0 && (
        <div className="ucard pad st-empty">
          <Icon name="layers" size={30} />
          <b>No pools yet</b>
          <p className="muted">Pick two or more free disks and NoCapOS sets up a mirror or RAIDZ for you.</p>
        </div>
      )}
      {pools.length > 0 && (
        <div className="ucard ulist">
          {pools.map((p) => (
            <button key={p.name} type="button" className="urow st-pool-row" onClick={() => onOpen(p.name)}>
              <span className={`urow-icon st-pool-icon ${poolHealth(p.health)}`}><Icon name="layers" size={17} /></span>
              <span className="urow-text">
                <span className="urow-title">{p.name} <HealthChip health={p.health} /></span>
                <span className="urow-desc">{layoutLabel(p.layout)} · {fmtSize(p.allocated)} used of {fmtSize(p.size)}</span>
                <Meter pct={p.size ? (p.allocated / p.size) * 100 : 0} level={p.capacity >= 90 ? 'bad' : p.capacity >= 80 ? 'warn' : undefined} />
              </span>
              <span className="urow-control"><Icon name="chevronRight" size={16} /></span>
            </button>
          ))}
        </div>
      )}
      {importable.length > 0 && <ImportCard importable={importable} onChanged={onChanged} />}
      {ready && auto !== null && (
        <div className="ucard ulist">
          <label className="urow st-switch toggle">
            <span className="urow-icon"><Icon name="clock" size={17} /></span>
            <span className="urow-text">
              <span className="urow-title">Monthly scrub</span>
              <span className="urow-desc">Once a month, ZFS reads every block to find and repair silent errors.</span>
            </span>
            <span className="urow-control">
              <input type="checkbox" role="switch" checked={auto} onChange={(e) => void setAutoScrub(e.target.checked)} />
            </span>
          </label>
        </div>
      )}
    </div>
  );
}

function PoolPage({ pool, byName, onChanged, onAdd, onReplace, onDestroy, onGone }: {
  pool: Pool; byName: Map<string, Disk>; onChanged: () => void; onAdd: () => void; onReplace: (v: Vdev) => void; onDestroy: () => void; onGone: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const pct = pool.size ? (pool.allocated / pool.size) * 100 : 0;
  const scan = pool.scan;

  const act = async (fn: () => Promise<{ ok: boolean; error?: string }>, done: string) => {
    setBusy(true);
    const r = await fn();
    setBusy(false);
    if (!r.ok) {
      toast('error', 'That didn’t work', r.error);
      return false;
    }
    toast('success', done);
    onChanged();
    return true;
  };
  const exportPool = async () => {
    const ok = await confirmDialog({
      title: `Export ${pool.name}?`,
      message: 'NoCapOS stops using the pool so you can move its disks to another computer. Nothing is deleted; you can import it again later.',
      confirmLabel: 'Export',
    });
    if (ok && (await act(() => storageApi.exportPool(pool.name), `Pool ${pool.name} exported`))) onGone();
  };

  return (
    <div className="st-page">
      <header className="st-page-head">
        <div>
          <h2 className="st-h">{pool.name} <HealthChip health={pool.health} /></h2>
          <p className="app-subtitle">{layoutLabel(pool.layout)} · <span className="mono">{pool.mountpoint}</span></p>
        </div>
        <div className="btn-group st-wrap-row">
          <button type="button" className="ghost small" onClick={onAdd}><Icon name="plus" size={13} /> Add disks</button>
          <button type="button" className="ghost small" disabled={busy} onClick={() => void exportPool()}><Icon name="eject" size={13} /> Export</button>
          <button type="button" className="ghost small danger" onClick={onDestroy}><Icon name="trash" size={13} /> Destroy</button>
        </div>
      </header>

      {pool.health !== 'ONLINE' && pool.status && (
        <div className={`st-note ${pool.health === 'DEGRADED' ? 'warn' : 'bad'}`}><Icon name="alert" size={14} /> <span>{pool.status}</span></div>
      )}
      {pool.errors && <div className="st-note bad"><Icon name="alert" size={14} /> <span>{pool.errors}</span></div>}

      <div className="st-pool-top">
        <div className="ucard pad st-cap">
          <Donut pct={pct} size={104}>
            <b>{Math.round(pct)}%</b>
            <small>used</small>
          </Donut>
          <div className="st-cap-text">
            <span className="st-big">{fmtSize(pool.free)} <small>free</small></span>
            <span className="muted small">{fmtSize(pool.allocated)} used of {fmtSize(pool.size)}</span>
            <span className="muted small">Fragmentation {pool.fragmentation}%</span>
            <label className="st-switch-row toggle compact">
              <span><b>Show in Files</b></span>
              <input type="checkbox" role="switch" checked={pool.in_files} disabled={busy}
                onChange={(e) => void act(() => storageApi.poolFiles(pool.name, e.target.checked), e.target.checked ? 'Shown in Files' : 'Hidden from Files')} />
            </label>
            {pool.in_files && (
              <button type="button" className="ghost small" onClick={() => openApp('files')}><Icon name="folder" size={13} /> Open Files</button>
            )}
          </div>
        </div>
        <div className="ucard pad st-scrub">
          <div className="row-head">
            <b>{scan.state === 'scanning' || scan.state === 'paused' ? (scan.function === 'resilver' ? 'Resilvering' : 'Scrubbing') : 'Scrub'}</b>
            {scan.function !== 'resilver' && (
              scan.state === 'scanning' || scan.state === 'paused' ? (
                <button type="button" className="ghost small" disabled={busy} onClick={() => void act(() => storageApi.scrub(pool.name, 'stop'), 'Scrub stopped')}>
                  <Icon name="stop" size={12} /> Stop
                </button>
              ) : (
                <button type="button" className="ghost small" disabled={busy || pool.health === 'FAULTED'} onClick={() => void act(() => storageApi.scrub(pool.name, 'start'), 'Scrub started')}>
                  <Icon name="play" size={12} /> Start scrub
                </button>
              )
            )}
          </div>
          {scan.state === 'scanning' || scan.state === 'paused' ? (
            <>
              <Meter pct={scan.percent ?? 0} />
              <span className="muted small">
                {(scan.percent ?? 0).toFixed(1)}% {scan.state === 'paused' ? '· paused' : scan.eta_seconds != null ? `· ${fmtEta(scan.eta_seconds)}` : ''}
              </span>
              {scan.function === 'resilver' && <span className="muted small">ZFS is copying data onto a new disk. The pool stays usable.</span>}
            </>
          ) : (
            <span className="muted small">
              {scan.state === 'finished' && scan.finished
                ? `Last ${scan.function === 'resilver' ? 'resilver' : 'scrub'} finished ${new Date(scan.finished).toLocaleDateString()}${scan.errors ? ` with ${scan.errors} errors` : ' with no errors'}.`
                : scan.state === 'canceled'
                  ? 'The last scrub was stopped before it finished.'
                  : 'Never scrubbed. A scrub reads every block to find and repair silent errors.'}
            </span>
          )}
        </div>
      </div>

      <h3 className="section-label">Disks</h3>
      <div className="ucard st-tree">
        {pool.vdevs.map((v, i) => (
          <VdevRow key={`${v.name}${i}`} v={v} depth={0} byName={byName} onReplace={onReplace} />
        ))}
      </div>

      <Datasets pool={pool} onChanged={onChanged} />
    </div>
  );
}

const groupTitle: Record<string, string> = { logs: 'Log devices', cache: 'Cache devices', spares: 'Spares', special: 'Special devices', dedup: 'Dedup devices' };

function VdevRow({ v, depth, byName, onReplace }: { v: Vdev; depth: number; byName: Map<string, Disk>; onReplace: (v: Vdev) => void }) {
  const leaf = v.children.length === 0 && (v.type === 'disk' || v.type === 'file');
  const d = v.disk ? byName.get(v.disk) : undefined;
  const group = groupTitle[v.type];
  const errs = v.read_errors + v.write_errors + v.checksum_errors;
  return (
    <>
      <div className={`st-vdev ${leaf ? 'leaf' : 'group'}`} style={{ paddingLeft: 14 + depth * 22 }}>
        <span className="st-vdev-icon">
          <Icon name={leaf ? (d ? diskIcon(d) : 'hdd') : 'layers'} size={15} />
        </span>
        <span className="st-vdev-text">
          <b>{group ?? (leaf ? (d ? diskTitle(d) : leafLabelShort(v)) : layoutLabel(v.type === 'replacing' ? 'Replacing' : v.type) + ` ${v.name.split('-').pop()}`)}</b>
          {leaf && <span className="muted small mono st-wrap">{d ? `${d.name} · ${fmtSize(d.size)}${d.serial ? ` · ${d.serial}` : ''}` : v.was ? `was ${v.was}` : v.path}</span>}
          {v.note && <span className="chip tiny">{v.note}</span>}
        </span>
        {!group && (
          <span className="st-vdev-errs" title="Read / write / checksum errors">
            {errs > 0 ? <span className="chip warn">{v.read_errors}/{v.write_errors}/{v.checksum_errors} errors</span> : <span className="muted small">No errors</span>}
          </span>
        )}
        {!group && v.health && <HealthChip health={v.health} />}
        {leaf && v.health !== 'AVAIL' && v.health !== 'INUSE' && (
          <button type="button" className={`ghost small ${v.health === 'ONLINE' ? '' : 'st-urgent'}`} onClick={() => onReplace(v)}>
            Replace
          </button>
        )}
      </div>
      {v.children.map((c, i) => (
        <VdevRow key={`${c.name}${i}`} v={c} depth={depth + 1} byName={byName} onReplace={onReplace} />
      ))}
    </>
  );
}

function leafLabelShort(v: Vdev) {
  const p = v.was || v.path || v.name;
  return v.guid && !v.path ? 'Missing disk' : p.replace('/dev/disk/by-id/', '').replace('/dev/', '').replace(/-part\d+$/, '');
}

// ---------------- Datasets & snapshots ----------------

function Datasets({ pool, onChanged }: { pool: Pool; onChanged: () => void }) {
  const [list, setList] = useState<Dataset[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const [edit, setEdit] = useState<Dataset | 'new' | null>(null);
  const [del, setDel] = useState<Dataset | null>(null);
  const load = useCallback(() => {
    void storageApi.datasets(pool.name).then((r) => {
      if (r.ok) {
        setList(r.data);
        setError(null);
      } else setError(r.error ?? 'Couldn’t list datasets');
    });
  }, [pool.name]);
  useEffect(load, [load]);

  return (
    <>
      <div className="row-head st-gap">
        <h3 className="section-label">Datasets & snapshots</h3>
        <button type="button" className="ghost small" onClick={() => setEdit('new')}><Icon name="folderPlus" size={13} /> New dataset</button>
      </div>
      <p className="muted small st-tight">Datasets are folders with their own quota, compression and snapshots.</p>
      {error && <p className="error">{error}</p>}
      <div className="ucard ulist">
        {(list ?? []).map((d) => {
          const depth = d.name.split('/').length - 1;
          return (
            <div key={d.name} className="st-ds">
              <div className="urow st-ds-row" style={{ paddingLeft: 18 + depth * 18 }}>
                <span className="urow-icon"><Icon name={depth ? 'folder' : 'database'} size={16} /></span>
                <span className="urow-text">
                  <span className="urow-title mono">{depth ? d.name.slice(pool.name.length + 1) : d.name}</span>
                  <span className="urow-desc">
                    {fmtSize(d.used)} used · {fmtSize(d.available)} free{d.quota ? ` · quota ${fmtSize(d.quota)}` : ''} · {d.compression}
                    {d.compressratio > 1.005 ? ` (${d.compressratio.toFixed(2)}×)` : ''}
                  </span>
                </span>
                <span className="urow-control btn-group">
                  <button type="button" className={`ghost small ${open === d.name ? 'on' : ''}`} onClick={() => setOpen(open === d.name ? null : d.name)} title="Snapshots">
                    <Icon name="camera" size={13} /> <span className="st-hide-sm">Snapshots</span>
                  </button>
                  <button type="button" className="icon-btn" title="Edit" onClick={() => setEdit(d)}><Icon name="pencil" size={14} /></button>
                  {depth > 0 && <button type="button" className="icon-btn" title="Delete" onClick={() => setDel(d)}><Icon name="trash" size={14} /></button>}
                </span>
              </div>
              {open === d.name && <Snapshots dataset={d.name} />}
            </div>
          );
        })}
        {list && list.length === 0 && <p className="muted small st-pad">No datasets.</p>}
        {!list && !error && <p className="muted small st-pad">Loading…</p>}
      </div>
      {edit && (
        <DatasetEditor pool={pool.name} ds={edit === 'new' ? null : edit} onClose={() => setEdit(null)} onDone={() => { setEdit(null); load(); onChanged(); }} />
      )}
      {del && (
        <TypeConfirm
          title={`Delete ${del.name}?`}
          word={del.name}
          action="Delete dataset"
          message={<><b>All files in {del.name}, its child datasets and their snapshots will be deleted.</b><p>This can’t be undone.</p></>}
          onClose={() => setDel(null)}
          onConfirm={async () => {
            const r = await storageApi.deleteDataset(del.name, del.name);
            if (!r.ok) return r.error ?? 'Couldn’t delete';
            toast('success', `${del.name} deleted`);
            setDel(null);
            load();
            onChanged();
            return null;
          }}
        />
      )}
    </>
  );
}

const GiB = 1024 ** 3;

function DatasetEditor({ pool, ds, onClose, onDone }: { pool: string; ds: Dataset | null; onClose: () => void; onDone: () => void }) {
  const [name, setName] = useState('');
  const [quota, setQuota] = useState(ds?.quota ? String(Math.round(ds.quota / GiB)) : '');
  const [comp, setComp] = useState(ds ? ds.compression : 'inherit');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const isRoot = ds && !ds.name.includes('/');
  const nameOk = ds || /^[a-zA-Z0-9_.:-]+(\/[a-zA-Z0-9_.:-]+){0,4}$/.test(name);
  const save = async () => {
    setBusy(true);
    const q = quota.trim() === '' ? 0 : Math.round(parseFloat(quota) * GiB);
    const r = ds
      ? await storageApi.updateDataset({ name: ds.name, quota: q, compression: comp === ds.compression ? undefined : comp })
      : await storageApi.createDataset({ name: `${pool}/${name}`, quota: q || undefined, compression: comp === 'inherit' ? undefined : comp });
    setBusy(false);
    if (!r.ok) return setError(r.error ?? 'Couldn’t save');
    toast('success', ds ? 'Saved' : `Dataset ${pool}/${name} created`);
    onDone();
  };
  const options = [...(isRoot ? [] : [{ id: 'inherit', label: 'Inherit' }]), { id: 'lz4', label: 'LZ4' }, { id: 'zstd', label: 'ZSTD' }, { id: 'off', label: 'Off' }];
  return (
    <Sheet
      title={ds ? `Edit ${ds.name}` : 'New dataset'}
      icon="folderPlus"
      onClose={onClose}
      footer={
        <>
          {error && <span className="error st-foot-error">{error}</span>}
          <button type="button" className="ghost" onClick={onClose}>Cancel</button>
          <button type="button" disabled={busy || !nameOk || (quota !== '' && !(parseFloat(quota) >= 0))} onClick={() => void save()}>{ds ? 'Save' : 'Create'}</button>
        </>
      }
    >
      <div className="st-form">
        {!ds && (
          <label>
            <span>Name</span>
            <div className="st-prefix"><span className="mono">{pool}/</span><input value={name} onChange={(e) => setName(e.target.value.trim())} placeholder="media" autoFocus /></div>
            <small className={name && !nameOk ? 'error' : 'muted'}>{name && !nameOk ? 'Use letters, numbers and _ . : - (use / for a dataset inside another).' : 'Appears as a folder inside the pool.'}</small>
          </label>
        )}
        <label>
          <span>Quota (GB)</span>
          <input type="number" min={0} step="any" value={quota} onChange={(e) => setQuota(e.target.value)} placeholder="No limit" />
          <small className="muted">Leave empty for no limit.</small>
        </label>
        <div className="st-field">
          <span>Compression</span>
          <div className="segmented">
            {options.map((o) => (
              <button key={o.id} type="button" className={comp === o.id ? 'on' : ''} onClick={() => setComp(o.id)}>{o.label}</button>
            ))}
          </div>
        </div>
      </div>
    </Sheet>
  );
}

function Snapshots({ dataset }: { dataset: string }) {
  const [list, setList] = useState<Snapshot[] | null>(null);
  const [name, setName] = useState('');
  const [rollback, setRollback] = useState<Snapshot | null>(null);
  const load = useCallback(() => void storageApi.snapshots(dataset).then((r) => r.ok && setList(r.data)), [dataset]);
  useEffect(load, [load]);
  const create = async () => {
    const n = name.trim() || `manual-${new Date().toISOString().slice(0, 16).replace(/[T:]/g, '-')}`;
    const r = await storageApi.createSnapshot(dataset, n);
    if (!r.ok) return toast('error', 'Couldn’t take a snapshot', r.error);
    toast('success', 'Snapshot taken');
    setName('');
    load();
  };
  const remove = async (s: Snapshot) => {
    const ok = await confirmDialog({ title: 'Delete snapshot?', message: `${s.name} will be deleted. The current files stay as they are.`, confirmLabel: 'Delete', danger: true });
    if (!ok) return;
    const r = await storageApi.deleteSnapshot(s.name);
    if (!r.ok) return toast('error', 'Couldn’t delete', r.error);
    load();
  };
  return (
    <div className="st-snaps">
      <div className="st-snap-new">
        <input value={name} onChange={(e) => setName(e.target.value)} placeholder="Snapshot name (optional)" onKeyDown={(e) => e.key === 'Enter' && void create()} />
        <button type="button" className="small" onClick={() => void create()}><Icon name="camera" size={13} /> Take snapshot</button>
      </div>
      {list?.length === 0 && <p className="muted small">No snapshots of {dataset} yet.</p>}
      {list?.slice().reverse().map((s) => (
        <div key={s.name} className="st-snap">
          <Icon name="camera" size={14} />
          <span className="st-snap-text">
            <b className="mono">{s.name.split('@')[1]}</b>
            <span className="muted small">{new Date(s.created).toLocaleString()} · {fmtSize(s.used)} unique</span>
          </span>
          <button type="button" className="ghost small" onClick={() => setRollback(s)}>Roll back</button>
          <button type="button" className="icon-btn" title="Delete" onClick={() => void remove(s)}><Icon name="trash" size={14} /></button>
        </div>
      ))}
      {rollback && (
        <TypeConfirm
          title="Roll back?"
          word={rollback.name}
          action="Roll back"
          message={
            <>
              <b>{dataset} goes back to how it was on {new Date(rollback.created).toLocaleString()}.</b>
              <p>Changes since then are lost, and any newer snapshots are deleted.</p>
            </>
          }
          onClose={() => setRollback(null)}
          onConfirm={async () => {
            const r = await storageApi.rollback(rollback.name, rollback.name);
            if (!r.ok) return r.error ?? 'Couldn’t roll back';
            toast('success', `${dataset} rolled back`);
            setRollback(null);
            load();
            return null;
          }}
        />
      )}
    </div>
  );
}
