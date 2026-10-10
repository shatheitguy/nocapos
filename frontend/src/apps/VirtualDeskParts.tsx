import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { dockerApi } from '../api/docker';
import { fileApi, type FileRoot } from '../api/files';
import { STATE_LABEL, type IsoFile, type VM, type VmOS, type VmState } from '../api/vms';
import { Icon, type IconName } from '../components/Icon';
import { toast } from '../state/toasts';
import { fmtSize, Sheet } from './StorageParts';

// Pieces of Virtual Desk: OS badges, state chips, and the picker that finds an
// ISO or disk image in a Files location and returns its path on the server.

export const OS_ICON: Record<VmOS, IconName> = {
  windows11: 'windows',
  windows10: 'windows',
  linux: 'linux',
  android: 'android',
  other: 'desktop',
};

export function OsBadge({ os, size = 40 }: { os: VmOS; size?: number }) {
  return (
    <span className={`vd-os ${os}`} style={{ width: size, height: size, borderRadius: size * 0.28 }}>
      <Icon name={OS_ICON[os] ?? 'desktop'} size={Math.round(size * 0.52)} />
    </span>
  );
}

export const stateTone = (s: VmState) => (s === 'running' ? 'good' : s === 'paused' || s === 'stopping' || s === 'suspended' ? 'warn' : s === 'crashed' ? 'bad' : '');

export function StateChip({ vm }: { vm: VM }) {
  if (vm.busy) {
    return (
      <span className="chip warn vd-busy">
        <span className="vd-mini-spin" /> {vm.busy[0].toUpperCase() + vm.busy.slice(1)}…
      </span>
    );
  }
  return (
    <span className={`chip vd-state ${stateTone(vm.state)}`}>
      <span className={`vd-dot ${vm.state}`} />
      {STATE_LABEL[vm.state] ?? vm.state}
    </span>
  );
}

/** A bar with a label and a value on the right (live CPU / memory). */
export function UsageBar({ label, pct, value }: { label: string; pct: number; value: string }) {
  const level = pct >= 90 ? 'bad' : pct >= 75 ? 'warn' : '';
  return (
    <div className="vd-usage">
      <span className="vd-usage-head">
        <span>{label}</span>
        <b>{value}</b>
      </span>
      <div className={`st-meter ${level}`}>
        <span style={{ width: `${Math.min(100, Math.max(0, pct))}%` }} />
      </div>
    </div>
  );
}

/** A host path field with a Browse button that opens the image picker. */
export function PathField({ value, onChange, kind, placeholder, autoFocus }: {
  value: string; onChange: (p: string) => void; kind: 'iso' | 'disk'; placeholder?: string; autoFocus?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  // The picker covers the whole Virtual Desk window, even from inside a sheet.
  const host = open ? (ref.current?.closest('.vd-app') ?? null) : null;
  const picker = <ImagePicker kind={kind} onClose={() => setOpen(false)} onPick={(p) => { onChange(p); setOpen(false); }} />;
  return (
    <div className="vd-path" ref={ref}>
      <input value={value} onChange={(e) => onChange(e.target.value.trim())} placeholder={placeholder ?? (kind === 'iso' ? '/srv/isos/installer.iso' : '/srv/vms/disk.qcow2')}
        spellCheck={false} autoComplete="off" autoFocus={autoFocus} className="mono" />
      <button type="button" className="ghost small" onClick={() => setOpen(true)}>
        <Icon name="folder" size={14} /> Browse
      </button>
      {open && (host ? createPortal(picker, host) : picker)}
    </div>
  );
}

const EXTS: Record<'iso' | 'disk', string[]> = {
  iso: ['.iso'],
  disk: ['.qcow2', '.img', '.raw', '.vmdk', '.vdi', '.vhd', '.vhdx'],
};

/** Choose an ISO or disk image in a storage location; returns its path on the server. */
export function ImagePicker({ kind, onClose, onPick }: { kind: 'iso' | 'disk'; onClose: () => void; onPick: (hostPath: string) => void }) {
  const [roots, setRoots] = useState<FileRoot[]>([]);
  const [root, setRoot] = useState('');
  const [path, setPath] = useState('/');
  const [items, setItems] = useState<{ name: string; dir: boolean; size: number }[] | null>(null);
  useEffect(() => {
    void fileApi.roots().then((r) => {
      if (!r.ok) return;
      setRoots(r.data);
      setRoot(r.data[0]?.id ?? '');
    });
  }, []);
  useEffect(() => {
    if (!root) return;
    setItems(null);
    void fileApi.list(root, path).then((r) => {
      if (!r.ok) return setItems([]);
      const exts = EXTS[kind];
      setItems(
        r.data.entries
          .filter((e) => (e.dir ? !e.name.startsWith('.') : exts.some((x) => e.name.toLowerCase().endsWith(x))))
          .sort((a, b) => Number(b.dir) - Number(a.dir) || a.name.localeCompare(b.name)),
      );
    });
  }, [root, path, kind]);
  const crumbs = path === '/' ? [] : path.slice(1).split('/');
  const choose = async (name: string) => {
    const r = await dockerApi.hostPath(root, (path === '/' ? '' : path) + '/' + name);
    if (!r.ok) return toast('error', 'Couldn’t use that file', r.error);
    onPick(r.data.path);
  };
  return (
    <Sheet title={kind === 'iso' ? 'Choose an ISO' : 'Choose a disk image'} icon={kind === 'iso' ? 'disk' : 'hdd'} onClose={onClose}
      footer={<button type="button" className="ghost" onClick={onClose}>Cancel</button>}>
      <select value={root} onChange={(e) => { setRoot(e.target.value); setPath('/'); }} aria-label="Location">
        {roots.map((r) => (
          <option key={r.id} value={r.id}>{r.name}</option>
        ))}
      </select>
      <div className="vd-crumbs">
        <button type="button" className="ghost small" onClick={() => setPath('/')}>{roots.find((r) => r.id === root)?.name ?? 'Location'}</button>
        {crumbs.map((c, i) => (
          <span key={i}>
            <Icon name="chevronRight" size={12} />
            <button type="button" className="ghost small" onClick={() => setPath('/' + crumbs.slice(0, i + 1).join('/'))}>{c}</button>
          </span>
        ))}
      </div>
      <div className="vd-files">
        {items === null ? (
          <span className="st-spinner" />
        ) : !items.length ? (
          <p className="muted small">No folders or {kind === 'iso' ? 'ISO files' : 'disk images'} here.</p>
        ) : (
          items.map((it) => (
            <button key={it.name} type="button" className="vd-file" onClick={() => (it.dir ? setPath((path === '/' ? '' : path) + '/' + it.name) : void choose(it.name))}>
              <Icon name={it.dir ? 'folder' : kind === 'iso' ? 'disk' : 'hdd'} size={15} />
              <span className="vd-file-name">{it.name}</span>
              {!it.dir && <span className="muted small">{fmtSize(it.size)}</span>}
            </button>
          ))
        )}
      </div>
    </Sheet>
  );
}

/** One-click picks from Virtual Desk's own ISO folder. */
export function IsoShelf({ isos, value, onPick }: { isos: IsoFile[]; value: string; onPick: (p: string) => void }) {
  if (!isos.length) return null;
  return (
    <div className="vd-isos">
      {isos.map((i) => (
        <button key={i.path} type="button" className={`vd-iso ${value === i.path ? 'on' : ''}`} onClick={() => onPick(value === i.path ? '' : i.path)} title={i.path}>
          <Icon name="disk" size={14} />
          <span>{i.name}</span>
        </button>
      ))}
    </div>
  );
}

export const baseName = (p?: string) => (p ? p.split(/[\\/]/).pop() || p : '');
