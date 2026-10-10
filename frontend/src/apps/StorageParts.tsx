import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { Icon, type IconName } from '../components/Icon';
import {
  KIND_LABEL,
  LAYOUTS,
  diskKind,
  layoutMath,
  smartHealth,
  storageApi,
  waitForJob,
  type Disk,
  type Job,
  type Layout,
  type Pool,
  type SmartReport,
  type Vdev,
} from '../api/storage';
import { usePoll } from '../lib/hooks';
import { toast } from '../state/toasts';

// Pieces of the Storage app: sizes, badges, the sheet (modal) frame, and the
// flows that erase disks (create pool, add disks, replace, use as storage).
// Every erase ends on a review screen where the admin types ERASE; the server
// checks that word and re-checks every disk again before touching it.

/** Disk-style sizes (1 TB = 10^12 bytes), like the label on the drive. */
export function fmtSize(n: number | undefined): string {
  if (n == null || !Number.isFinite(n)) return '–';
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  let i = 0;
  let v = n;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  return `${v.toFixed(v >= 100 || i === 0 ? 0 : 1).replace(/\.0$/, '')} ${units[i]}`;
}

export const diskIcon = (d: Disk): IconName => diskKind(d);
export const diskTitle = (d?: Disk, fallback = '') => d?.model || d?.name || fallback;

export function SmartBadge({ disk }: { disk: Disk }) {
  const h = smartHealth(disk.smart);
  const temp = disk.smart?.temperature_c;
  return (
    <span className={`chip ${h.level === 'good' ? 'good' : h.level === 'warn' ? 'warn' : h.level === 'bad' ? 'bad' : ''}`}>
      {h.label}
      {temp != null && <span className="st-temp">· {temp}°C</span>}
    </span>
  );
}

export function UsageBadge({ disk }: { disk: Disk }) {
  const label: Record<string, string> = { system: 'System', pool: 'In a pool', mounted: 'In use', md: 'RAID (md)', lvm: 'LVM', storage: 'Storage', free: 'Free' };
  return <span className={`chip st-usage ${disk.usage}`}>{disk.available ? 'Available' : label[disk.usage]}</span>;
}

export function HealthChip({ health }: { health: string }) {
  const cls = health === 'ONLINE' || health === 'AVAIL' ? 'good' : health === 'DEGRADED' || health === 'INUSE' ? 'warn' : 'bad';
  const words: Record<string, string> = { ONLINE: 'Healthy', DEGRADED: 'Degraded', FAULTED: 'Faulted', UNAVAIL: 'Unavailable', OFFLINE: 'Offline', REMOVED: 'Removed', AVAIL: 'Spare', INUSE: 'Spare in use', SUSPENDED: 'Suspended' };
  return <span className={`chip ${cls}`}>{words[health] ?? health}</span>;
}

export function Meter({ pct, level }: { pct: number; level?: 'warn' | 'bad' }) {
  return (
    <div className={`st-meter ${level ?? ''}`}>
      <span style={{ width: `${Math.min(100, Math.max(0, pct))}%` }} />
    </div>
  );
}

export function Donut({ pct, size = 92, children }: { pct: number; size?: number; children?: ReactNode }) {
  const r = 40;
  const c = 2 * Math.PI * r;
  const level = pct >= 90 ? 'bad' : pct >= 80 ? 'warn' : '';
  return (
    <div className={`st-donut ${level}`} style={{ width: size, height: size }}>
      <svg viewBox="0 0 100 100" width={size} height={size} aria-hidden="true">
        <circle cx="50" cy="50" r={r} className="st-donut-track" />
        <circle cx="50" cy="50" r={r} className="st-donut-fill" strokeDasharray={`${(c * Math.min(100, pct)) / 100} ${c}`} transform="rotate(-90 50 50)" />
      </svg>
      <div className="st-donut-label">{children}</div>
    </div>
  );
}

/** A modal sheet inside the Storage window. */
export function Sheet({ title, icon, onClose, wide, children, footer }: {
  title: string; icon?: IconName; onClose: () => void; wide?: boolean; children: ReactNode; footer?: ReactNode;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);
  return (
    <div className="st-backdrop" onPointerDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className={`st-sheet ${wide ? 'wide' : ''}`} role="dialog" aria-label={title}>
        <header className="st-sheet-head">
          {icon && <span className="st-sheet-icon"><Icon name={icon} size={18} /></span>}
          <h2>{title}</h2>
          <button type="button" className="icon-btn" aria-label="Close" onClick={onClose}>
            <Icon name="close" size={16} />
          </button>
        </header>
        <div className="st-sheet-body">{children}</div>
        {footer && <footer className="st-sheet-foot">{footer}</footer>}
      </div>
    </div>
  );
}

/** Runs a background job to completion, showing progress in place. */
export function useJob(onDone: () => void) {
  const [job, setJob] = useState<Job | null>(null);
  const [error, setError] = useState<string | null>(null);
  const start = async (p: Promise<{ ok: boolean; data: { job: Job }; error?: string }>, success: string) => {
    setError(null);
    const r = await p;
    if (!r.ok) {
      setError(r.error ?? 'That didn’t work');
      return;
    }
    setJob(r.data.job);
    const end = await waitForJob(r.data.job.id, setJob);
    if (end?.state === 'done') {
      toast('success', success);
      onDone();
    } else {
      setError(end?.error ?? 'Lost track of the job; check again in a moment');
      setJob(null);
    }
  };
  return { job, running: job?.state === 'running', error, start, setError };
}

export function Working({ text }: { text: string }) {
  return (
    <div className="st-working">
      <span className="st-spinner" />
      <div>
        <b>{text}</b>
        <p className="muted small">This can take a minute. You can close this; it keeps going.</p>
      </div>
    </div>
  );
}

/** The last step of every erase: list what goes, type a word. */
export function EraseReview({ disks, word, value, onChange, what }: {
  disks: Disk[]; word: string; value: string; onChange: (v: string) => void; what: string;
}) {
  return (
    <div className="st-erase">
      <div className="st-erase-warn">
        <Icon name="alert" size={18} />
        <div>
          <b>Everything on {disks.length === 1 ? 'this disk' : `these ${disks.length} disks`} will be erased.</b>
          <p>{what} This can’t be undone. Make sure nothing you need is on {disks.length === 1 ? 'it' : 'them'}.</p>
        </div>
      </div>
      <div className="ucard ulist st-erase-list">
        {disks.map((d) => (
          <div key={d.name} className="urow">
            <span className="urow-icon"><Icon name={diskIcon(d)} size={17} /></span>
            <span className="urow-text">
              <span className="urow-title">{diskTitle(d)}</span>
              <span className="urow-desc">
                {d.serial ? `Serial ${d.serial} · ` : ''}{d.path}
                {d.partitions.length > 0 && ` · ${d.partitions.length} partition${d.partitions.length > 1 ? 's' : ''}${d.partitions.some((p) => p.label) ? ` (${d.partitions.map((p) => p.label).filter(Boolean).join(', ')})` : ''}`}
              </span>
            </span>
            <span className="urow-control"><b>{fmtSize(d.size)}</b></span>
          </div>
        ))}
      </div>
      <label className="st-type">
        <span>Type <b>{word}</b> to confirm</span>
        <input value={value} onChange={(e) => onChange(e.target.value)} autoComplete="off" spellCheck={false} placeholder={word} />
      </label>
    </div>
  );
}

/** Pick disks: available ones have checkboxes, the rest say why not. */
export function DiskPicker({ disks, picked, onChange, single }: {
  disks: Disk[]; picked: string[]; onChange: (v: string[]) => void; single?: boolean;
}) {
  const avail = disks.filter((d) => d.available);
  const other = disks.filter((d) => !d.available);
  const toggle = (n: string) => {
    if (single) onChange([n]);
    else onChange(picked.includes(n) ? picked.filter((x) => x !== n) : [...picked, n]);
  };
  return (
    <div className="st-picker">
      {avail.length === 0 && <p className="muted">No free disks right now. Connect a new disk, or free one up first.</p>}
      {avail.map((d) => (
        <label key={d.name} className={`st-pick ${picked.includes(d.name) ? 'on' : ''}`}>
          <input type={single ? 'radio' : 'checkbox'} name="st-disk" checked={picked.includes(d.name)} onChange={() => toggle(d.name)} />
          <Icon name={diskIcon(d)} size={18} />
          <span className="st-pick-text">
            <b>{diskTitle(d)}</b>
            <span className="muted small">{KIND_LABEL[diskKind(d)]} · {d.name}{d.serial ? ` · ${d.serial}` : ''}</span>
          </span>
          <SmartBadge disk={d} />
          <b className="st-pick-size">{fmtSize(d.size)}</b>
        </label>
      ))}
      {other.length > 0 && (
        <details className="st-unavail">
          <summary>{other.length} disk{other.length > 1 ? 's' : ''} can’t be used</summary>
          {other.map((d) => (
            <div key={d.name} className="st-pick off">
              <Icon name={diskIcon(d)} size={18} />
              <span className="st-pick-text">
                <b>{diskTitle(d)}</b>
                <span className="muted small">{d.reason}</span>
              </span>
              <b className="st-pick-size">{fmtSize(d.size)}</b>
            </div>
          ))}
        </details>
      )}
    </div>
  );
}

export function LayoutCards({ sizes, value, onChange, only }: { sizes: number[]; value: Layout | null; onChange: (l: Layout) => void; only?: Layout[] }) {
  return (
    <div className="st-layouts">
      {LAYOUTS.filter((l) => !only || only.includes(l.id)).map((l) => {
        const ok = sizes.length >= l.min;
        const m = layoutMath(l.id, sizes);
        return (
          <button key={l.id} type="button" className={`st-layout ${value === l.id ? 'on' : ''}`} disabled={!ok} onClick={() => onChange(l.id)}>
            <span className="st-layout-head">
              <b>{l.title}</b>
              {!ok && <span className="chip">{l.min}+ disks</span>}
            </span>
            <span className="st-layout-cap">{ok ? fmtSize(m.usable) : '–'}<small> usable</small></span>
            <span className={`st-layout-fail ${ok && m.canFail === 0 ? 'risk' : ''}`}>
              <Icon name={ok && m.canFail === 0 ? 'alert' : 'shield'} size={13} />
              {!ok ? 'Not enough disks' : m.canFail === 0 ? 'No disk can fail' : `${m.canFail} disk${m.canFail > 1 ? 's' : ''} can fail`}
            </span>
            <span className="st-layout-blurb">{l.blurb}</span>
          </button>
        );
      })}
    </div>
  );
}

function Steps({ step, names }: { step: number; names: string[] }) {
  return (
    <ol className="st-steps">
      {names.map((n, i) => (
        <li key={n} className={i === step ? 'on' : i < step ? 'done' : ''}>
          <span>{i < step ? <Icon name="check" size={12} /> : i + 1}</span>
          {n}
        </li>
      ))}
    </ol>
  );
}

// ---------------- Create pool wizard ----------------

export function CreatePoolWizard({ disks, onClose, onDone }: { disks: Disk[]; onClose: () => void; onDone: () => void }) {
  const [step, setStep] = useState(0);
  const [picked, setPicked] = useState<string[]>([]);
  const [layout, setLayout] = useState<Layout | null>(null);
  const [touched, setTouched] = useState(false);
  const [name, setName] = useState('');
  const [compression, setCompression] = useState('lz4');
  const [inFiles, setInFiles] = useState(true);
  const [confirm, setConfirm] = useState('');
  const { job, running, error, start, setError } = useJob(() => {
    onDone();
    onClose();
  });
  const chosen = disks.filter((d) => picked.includes(d.name));
  const sizes = chosen.map((d) => d.size);
  const def = LAYOUTS.find((l) => l.id === layout);
  const math = layout ? layoutMath(layout, sizes) : null;
  const nameOk = /^[a-zA-Z][a-zA-Z0-9_.:-]{0,49}$/.test(name) && !/^(mirror|raidz\d?|draid\d?|spare|log|cache|special|c\d.*)$/i.test(name);
  const mixed = sizes.length > 1 && Math.min(...sizes) < Math.max(...sizes) * 0.95;

  useEffect(() => {
    // Suggest the safest layout that fits until the admin picks one themselves.
    if (touched && layout && sizes.length >= (def?.min ?? 1)) return;
    const n = sizes.length;
    setLayout(n >= 4 ? 'raidz2' : n === 3 ? 'raidz1' : n === 2 ? 'mirror' : n === 1 ? 'stripe' : null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [picked.length]);

  const next = [picked.length > 0, !!layout && sizes.length >= (def?.min ?? 99), nameOk, confirm === 'ERASE'][step];
  const create = () =>
    void start(storageApi.createPool({ name, layout: layout!, disks: picked, compression, add_to_files: inFiles, confirm }), `Pool ${name} is ready`);

  return (
    <Sheet
      title="Create a storage pool"
      icon="layers"
      wide
      onClose={onClose}
      footer={
        job ? null : (
          <>
            {error && <span className="error st-foot-error">{error}</span>}
            <button type="button" className="ghost" onClick={() => (step ? (setStep(step - 1), setError(null)) : onClose())}>
              {step ? 'Back' : 'Cancel'}
            </button>
            {step < 3 ? (
              <button type="button" disabled={!next} onClick={() => setStep(step + 1)}>Continue</button>
            ) : (
              <button type="button" className="danger solid" disabled={!next || running} onClick={create}>Erase and create pool</button>
            )}
          </>
        )
      }
    >
      <Steps step={step} names={['Disks', 'Layout', 'Name', 'Review']} />
      {job ? (
        <Working text={`Creating pool ${name}…`} />
      ) : step === 0 ? (
        <>
          <p className="st-lead">Pick the disks for the new pool. Only disks that aren’t in use are shown; they’ll be erased.</p>
          <DiskPicker disks={disks} picked={picked} onChange={setPicked} />
        </>
      ) : step === 1 ? (
        <>
          <p className="st-lead">
            How should {picked.length} disk{picked.length > 1 ? 's' : ''} work together? Usable space is based on the smallest disk.
          </p>
          {mixed && <p className="st-note"><Icon name="info" size={14} /> These disks are different sizes, so the larger ones will only use as much as the smallest.</p>}
          <LayoutCards sizes={sizes} value={layout} onChange={(l) => { setLayout(l); setTouched(true); }} />
        </>
      ) : step === 2 ? (
        <div className="st-form">
          <label>
            <span>Pool name</span>
            <input value={name} onChange={(e) => setName(e.target.value.trim())} placeholder="e.g. tank" autoFocus maxLength={50} />
            <small className={name && !nameOk ? 'error' : 'muted'}>
              {name && !nameOk ? 'Start with a letter; use letters, numbers and _ . : - only.' : `It will appear at /srv/nocapos/pools/${name || 'name'}`}
            </small>
          </label>
          <div className="st-field">
            <span>Compression</span>
            <div className="segmented">
              {[{ id: 'lz4', label: 'LZ4 (recommended)' }, { id: 'zstd', label: 'ZSTD (smaller)' }, { id: 'off', label: 'Off' }].map((o) => (
                <button key={o.id} type="button" className={compression === o.id ? 'on' : ''} onClick={() => setCompression(o.id)}>{o.label}</button>
              ))}
            </div>
          </div>
          <label className="st-switch-row toggle">
            <span>
              <b>Show in Files</b>
              <small className="muted">Add the pool as a location in the Files app</small>
            </span>
            <input type="checkbox" role="switch" checked={inFiles} onChange={(e) => setInFiles(e.target.checked)} />
          </label>
        </div>
      ) : (
        <>
          <div className="st-summary">
            <div><span className="muted small">Pool</span><b>{name}</b></div>
            <div><span className="muted small">Layout</span><b>{def?.title}</b></div>
            <div><span className="muted small">Usable space</span><b>{fmtSize(math?.usable)}</b></div>
            <div><span className="muted small">Can survive</span><b>{math?.canFail ? `${math.canFail} failed disk${math.canFail > 1 ? 's' : ''}` : 'no failures'}</b></div>
          </div>
          {layout === 'stripe' && picked.length > 0 && (
            <p className="st-note warn"><Icon name="alert" size={14} /> A stripe has no redundancy: if any disk fails, the whole pool is lost.</p>
          )}
          <EraseReview disks={chosen} word="ERASE" value={confirm} onChange={setConfirm} what={`They become the new ${def?.title} pool “${name}”.`} />
        </>
      )}
    </Sheet>
  );
}

// ---------------- Use a single disk as storage (ext4) ----------------

export function FormatDisk({ disk, onClose, onDone }: { disk: Disk; onClose: () => void; onDone: () => void }) {
  const [step, setStep] = useState(0);
  const [label, setLabel] = useState('');
  const [inFiles, setInFiles] = useState(true);
  const [confirm, setConfirm] = useState('');
  const { job, error, start } = useJob(() => {
    onDone();
    onClose();
  });
  const ok = /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,15}$/.test(label);
  return (
    <Sheet
      title="Use as storage"
      icon={diskIcon(disk)}
      onClose={onClose}
      footer={
        job ? null : (
          <>
            {error && <span className="error st-foot-error">{error}</span>}
            <button type="button" className="ghost" onClick={() => (step ? setStep(0) : onClose())}>{step ? 'Back' : 'Cancel'}</button>
            {step === 0 ? (
              <button type="button" disabled={!ok} onClick={() => setStep(1)}>Continue</button>
            ) : (
              <button type="button" className="danger solid" disabled={confirm !== 'ERASE'}
                onClick={() => void start(storageApi.format(disk.name, label, inFiles, confirm), `${label} is ready`)}>
                Erase and set up
              </button>
            )}
          </>
        )
      }
    >
      {job ? (
        <Working text={`Setting up ${diskTitle(disk)}…`} />
      ) : step === 0 ? (
        <div className="st-form">
          <p className="st-lead">Format this one disk as simple storage (ext4). It’s mounted automatically at every start. For redundancy, create a pool with two or more disks instead.</p>
          <label>
            <span>Name</span>
            <input value={label} onChange={(e) => setLabel(e.target.value.trim())} placeholder="e.g. media" autoFocus maxLength={16} />
            <small className={label && !ok ? 'error' : 'muted'}>
              {label && !ok ? 'Up to 16 letters, numbers, _ or -.' : `It will appear at /srv/nocapos/disks/${label || 'name'}`}
            </small>
          </label>
          <label className="st-switch-row toggle">
            <span>
              <b>Show in Files</b>
              <small className="muted">Add the disk as a location in the Files app</small>
            </span>
            <input type="checkbox" role="switch" checked={inFiles} onChange={(e) => setInFiles(e.target.checked)} />
          </label>
        </div>
      ) : (
        <EraseReview disks={[disk]} word="ERASE" value={confirm} onChange={setConfirm} what={`It gets one new partition named “${label}”.`} />
      )}
    </Sheet>
  );
}

// ---------------- Add disks to a pool ----------------

export function AddDisks({ pool, disks, onClose, onDone }: { pool: Pool; disks: Disk[]; onClose: () => void; onDone: () => void }) {
  const [step, setStep] = useState(0);
  const [picked, setPicked] = useState<string[]>([]);
  const same = (LAYOUTS.find((l) => l.id === pool.layout)?.id ?? 'mirror') as Layout;
  const [layout, setLayout] = useState<Layout>(same);
  const [allow, setAllow] = useState(false);
  const [confirm, setConfirm] = useState('');
  const { job, error, start } = useJob(() => {
    onDone();
    onClose();
  });
  const chosen = disks.filter((d) => picked.includes(d.name));
  const sizes = chosen.map((d) => d.size);
  const min = LAYOUTS.find((l) => l.id === layout)!.min;
  const mismatch = layout !== pool.layout;
  const can = [picked.length >= min && (!mismatch || allow), confirm === 'ERASE'][step];
  return (
    <Sheet
      title={`Add disks to ${pool.name}`}
      icon="plus"
      wide
      onClose={onClose}
      footer={
        job ? null : (
          <>
            {error && <span className="error st-foot-error">{error}</span>}
            <button type="button" className="ghost" onClick={() => (step ? setStep(0) : onClose())}>{step ? 'Back' : 'Cancel'}</button>
            {step === 0 ? (
              <button type="button" disabled={!can} onClick={() => setStep(1)}>Continue</button>
            ) : (
              <button type="button" className="danger solid" disabled={!can}
                onClick={() => void start(storageApi.addVdev(pool.name, { layout, disks: picked, confirm, allow_mismatch: mismatch || undefined }), `Added ${picked.length} disks to ${pool.name}`)}>
                Erase and add
              </button>
            )}
          </>
        )
      }
    >
      {job ? (
        <Working text={`Adding disks to ${pool.name}…`} />
      ) : step === 0 ? (
        <>
          <p className="st-lead">
            Grow {pool.name} by adding a new group of disks. To keep the same protection, add a {LAYOUTS.find((l) => l.id === same)?.title ?? same} group of {LAYOUTS.find((l) => l.id === same)?.min ?? 2} or more disks. Disks can’t be removed from a pool later.
          </p>
          <DiskPicker disks={disks} picked={picked} onChange={setPicked} />
          <h4 className="section-label st-gap">New group layout</h4>
          <LayoutCards sizes={sizes} value={layout} onChange={setLayout} />
          {mismatch && (
            <label className="st-note warn st-check">
              <input type="checkbox" checked={allow} onChange={(e) => setAllow(e.target.checked)} />
              <span>
                {pool.name} is a {pool.layout} pool. Adding a {LAYOUTS.find((l) => l.id === layout)?.title} group changes how many disks can fail
                {layout === 'stripe' ? ': if this disk fails, the whole pool is lost' : ''}. I understand.
              </span>
            </label>
          )}
        </>
      ) : (
        <EraseReview disks={chosen} word="ERASE" value={confirm} onChange={setConfirm} what={`They join pool “${pool.name}” for good.`} />
      )}
    </Sheet>
  );
}

// ---------------- Replace a disk ----------------

export function ReplaceDisk({ pool, old, oldLabel, disks, onClose, onDone }: {
  pool: Pool; old: Vdev; oldLabel: string; disks: Disk[]; onClose: () => void; onDone: () => void;
}) {
  const [step, setStep] = useState(0);
  const [picked, setPicked] = useState<string[]>([]);
  const [confirm, setConfirm] = useState('');
  const { job, error, start } = useJob(() => {
    onDone();
    onClose();
  });
  const chosen = disks.filter((d) => picked.includes(d.name));
  const oldRef = old.path || old.guid || old.name;
  return (
    <Sheet
      title="Replace disk"
      icon="swap"
      wide
      onClose={onClose}
      footer={
        job ? null : (
          <>
            {error && <span className="error st-foot-error">{error}</span>}
            <button type="button" className="ghost" onClick={() => (step ? setStep(0) : onClose())}>{step ? 'Back' : 'Cancel'}</button>
            {step === 0 ? (
              <button type="button" disabled={!picked.length} onClick={() => setStep(1)}>Continue</button>
            ) : (
              <button type="button" className="danger solid" disabled={confirm !== 'ERASE'}
                onClick={() => void start(storageApi.replace(pool.name, { old: oldRef, disk: picked[0], confirm }), 'Replacement started; ZFS is copying data to the new disk')}>
                Erase and replace
              </button>
            )}
          </>
        )
      }
    >
      {job ? (
        <Working text="Starting the replacement…" />
      ) : step === 0 ? (
        <>
          <div className="st-note">
            <Icon name="info" size={14} />
            <span>
              Replacing <b>{oldLabel}</b> ({old.health.toLowerCase()}) in {pool.name}. ZFS copies its data onto the new disk (a “resilver”); the pool stays usable meanwhile. The new disk should be at least as big.
            </span>
          </div>
          <DiskPicker disks={disks} picked={picked} onChange={setPicked} single />
        </>
      ) : (
        <EraseReview disks={chosen} word="ERASE" value={confirm} onChange={setConfirm} what={`It takes the place of ${oldLabel} in “${pool.name}”.`} />
      )}
    </Sheet>
  );
}

/** Ask the admin to type a name before something destructive. */
export function TypeConfirm({ title, message, word, action, onClose, onConfirm }: {
  title: string; message: ReactNode; word: string; action: string; onClose: () => void; onConfirm: () => Promise<string | null>;
}) {
  const [value, setValue] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const go = async () => {
    setBusy(true);
    setError(await onConfirm());
    setBusy(false);
  };
  return (
    <Sheet
      title={title}
      icon="alert"
      onClose={onClose}
      footer={
        <>
          {error && <span className="error st-foot-error">{error}</span>}
          <button type="button" className="ghost" onClick={onClose}>Cancel</button>
          <button type="button" className="danger solid" disabled={value !== word || busy} onClick={() => void go()}>{busy ? 'Working…' : action}</button>
        </>
      }
    >
      <div className="st-erase-warn"><Icon name="alert" size={18} /><div>{message}</div></div>
      <label className="st-type">
        <span>Type <b>{word}</b> to confirm</span>
        <input value={value} onChange={(e) => setValue(e.target.value)} autoFocus autoComplete="off" spellCheck={false} placeholder={word} />
      </label>
    </Sheet>
  );
}

// ---------------- Disk details (partitions + SMART) ----------------

export function DiskSheet({ disk, onClose, onFormat, onChanged, smartInstalled }: {
  disk: Disk; onClose: () => void; onFormat: () => void; onChanged: () => void; smartInstalled: boolean;
}) {
  const [smart, setSmart] = useState<SmartReport | null>(null);
  const [smartErr, setSmartErr] = useState<string | null>(null);
  const [testing, setTesting] = useState(false);
  const load = () =>
    storageApi.smart(disk.name).then((r) => {
      if (r.ok) setSmart(r.data);
      else setSmartErr(r.error ?? 'No SMART data');
    });
  useEffect(() => {
    if (smartInstalled) void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [disk.name, smartInstalled]);
  usePoll(load, 5000, !!smart?.summary.test_running);
  const runTest = async (type: 'short' | 'long') => {
    setTesting(true);
    const r = await storageApi.smartTest(disk.name, type);
    setTesting(false);
    if (!r.ok) return toast('error', 'Couldn’t start the test', r.error);
    toast('info', type === 'short' ? 'Short test started' : 'Long test started', type === 'short' ? 'It takes about 2 minutes.' : 'It can take several hours; the disk stays usable.');
    window.setTimeout(() => void load(), 1500);
  };
  const s = smart?.summary ?? disk.smart;
  const h = smartHealth(s);
  const storageMount = disk.usage === 'storage' ? disk.partitions.flatMap((p) => p.mountpoints).find((m) => m.startsWith('/srv/nocapos/disks/')) : undefined;
  const kind = diskKind(disk);
  const rows = useMemo(() => smart?.attributes ?? [], [smart]);

  return (
    <Sheet title={diskTitle(disk)} icon={diskIcon(disk)} wide onClose={onClose}>
      <div className="st-disk-hero">
        <div className="st-chips">
          <UsageBadge disk={disk} />
          <SmartBadge disk={{ ...disk, smart: s }} />
          <span className="chip">{KIND_LABEL[kind]}</span>
        </div>
        <dl className="kv st-kv">
          <dt>Size</dt><dd>{fmtSize(disk.size)}</dd>
          <dt>Device</dt><dd className="mono">{disk.path}</dd>
          {disk.serial && (<><dt>Serial</dt><dd className="mono">{disk.serial}</dd></>)}
          {disk.by_id && (<><dt>ID</dt><dd className="mono st-wrap">{disk.by_id}</dd></>)}
          {disk.transport && (<><dt>Connection</dt><dd>{disk.transport.toUpperCase()}</dd></>)}
          {!disk.available && disk.reason && (<><dt>In use</dt><dd>{disk.reason}</dd></>)}
        </dl>
        <div className="st-actions">
          {disk.available && (
            <button type="button" onClick={onFormat}>
              <Icon name="drive" size={14} /> Use as storage
            </button>
          )}
          {storageMount && <FilesToggleDisk disk={disk} onChanged={onChanged} />}
        </div>
      </div>

      <h4 className="section-label">Partitions</h4>
      {disk.partitions.length === 0 ? (
        <p className="muted small">No partitions — this disk is blank.</p>
      ) : (
        <div className="ucard st-table-wrap">
          <table className="table st-table">
            <thead><tr><th>Name</th><th>Size</th><th>Type</th><th>Label</th><th>Mounted at</th></tr></thead>
            <tbody>
              {disk.partitions.map((p) => (
                <tr key={p.name}>
                  <td className="mono">{p.name}</td>
                  <td>{fmtSize(p.size)}</td>
                  <td>{p.fstype ?? '–'}</td>
                  <td>{p.label ?? ''}</td>
                  <td className="mono">{p.mountpoints.join(', ')}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <div className="row-head st-gap">
        <h4 className="section-label">Health (SMART)</h4>
        {smartInstalled && smart?.summary.available && (
          <div className="btn-group">
            <button type="button" className="ghost small" disabled={testing || s?.test_running} onClick={() => void runTest('short')}>Run short test</button>
            <button type="button" className="ghost small" disabled={testing || s?.test_running} onClick={() => void runTest('long')}>Long test</button>
          </div>
        )}
      </div>
      {!smartInstalled ? (
        <p className="muted small">Install SMART tools from the Overview page to see this disk’s health.</p>
      ) : !smart ? (
        <p className="muted small">{smartErr ?? 'Reading SMART data…'}</p>
      ) : !smart.summary.available ? (
        <p className="muted small">This disk doesn’t report SMART health (common for some USB enclosures).</p>
      ) : (
        <>
          <div className={`st-health ${h.level}`}>
            <Icon name={h.level === 'good' ? 'check' : 'alert'} size={18} />
            <div>
              <b>{h.level === 'good' ? 'This disk looks healthy' : h.level === 'bad' ? 'This disk reports it is failing' : h.level === 'warn' ? 'This disk shows early signs of wear' : 'Health unknown'}</b>
              <p className="muted small">
                {h.level === 'bad' ? 'Copy your data off it and replace it as soon as you can.' : h.level === 'warn' ? 'Keep backups current and plan to replace it.' : s?.asleep ? 'The disk was asleep; showing its last reading.' : 'No warnings from the disk.'}
              </p>
            </div>
          </div>
          {s?.test_running && (
            <div className="st-scan">
              <span>Self-test running{s.test_remaining != null ? ` · ${s.test_remaining}% left` : ''}</span>
              <Meter pct={100 - (s.test_remaining ?? 50)} />
            </div>
          )}
          <div className="st-stats">
            {s?.temperature_c != null && <div><span>Temperature</span><b>{s.temperature_c}°C</b></div>}
            {s?.power_on_hours != null && <div><span>Powered on</span><b>{Math.round(s.power_on_hours / 24).toLocaleString()} days</b></div>}
            {s?.reallocated != null && <div><span>Reallocated sectors</span><b className={s.reallocated ? 'warn' : ''}>{s.reallocated}</b></div>}
            {s?.pending != null && <div><span>Pending sectors</span><b className={s.pending ? 'warn' : ''}>{s.pending}</b></div>}
            {s?.percent_used != null && <div><span>Life used</span><b>{s.percent_used}%</b></div>}
            {s?.media_errors != null && <div><span>Media errors</span><b className={s.media_errors ? 'warn' : ''}>{s.media_errors}</b></div>}
          </div>
          {smart.nvme && (
            <dl className="kv st-kv">
              <dt>Spare capacity</dt><dd>{smart.nvme.available_spare}% (warns below {smart.nvme.available_spare_threshold}%)</dd>
              <dt>Data written</dt><dd>{fmtSize(smart.nvme.data_units_written * 512000)}</dd>
              <dt>Data read</dt><dd>{fmtSize(smart.nvme.data_units_read * 512000)}</dd>
              <dt>Power cycles</dt><dd>{smart.nvme.power_cycles.toLocaleString()}</dd>
              <dt>Unsafe shutdowns</dt><dd>{smart.nvme.unsafe_shutdowns}</dd>
              <dt>Critical warning</dt><dd>{smart.nvme.critical_warning ? 'Yes' : 'None'}</dd>
            </dl>
          )}
          {rows.length > 0 && (
            <div className="ucard st-table-wrap">
              <table className="table st-table">
                <thead><tr><th>ID</th><th>Attribute</th><th>Value</th><th>Worst</th><th>Threshold</th><th>Raw</th></tr></thead>
                <tbody>
                  {rows.map((a) => (
                    <tr key={a.id} className={a.failing ? 'st-failing' : ''}>
                      <td>{a.id}</td>
                      <td>{a.name}</td>
                      <td>{a.value}</td>
                      <td>{a.worst}</td>
                      <td>{a.thresh}</td>
                      <td className="mono">{a.raw}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {smart.self_tests.length > 0 && (
            <>
              <h4 className="section-label st-gap">Self-tests</h4>
              <div className="ucard ulist">
                {smart.self_tests.slice(0, 6).map((t, i) => (
                  <div key={i} className="urow">
                    <span className="urow-text">
                      <span className="urow-title">{t.type}</span>
                      <span className="urow-desc">{t.status}</span>
                    </span>
                    <span className="urow-control muted small">at {t.hours.toLocaleString()} h</span>
                  </div>
                ))}
              </div>
            </>
          )}
        </>
      )}
    </Sheet>
  );
}

function FilesToggleDisk({ disk, onChanged }: { disk: Disk; onChanged: () => void }) {
  const [busy, setBusy] = useState(false);
  const set = async (on: boolean) => {
    setBusy(true);
    const r = await storageApi.diskFiles(disk.name, on);
    setBusy(false);
    if (!r.ok) return toast('error', 'Couldn’t change Files', r.error);
    toast('success', on ? 'Shown in Files' : 'Hidden from Files');
    onChanged();
  };
  return (
    <span className="btn-group">
      <button type="button" className="ghost small" disabled={busy} onClick={() => void set(true)}><Icon name="folder" size={13} /> Show in Files</button>
      <button type="button" className="ghost small" disabled={busy} onClick={() => void set(false)}>Hide</button>
    </span>
  );
}
