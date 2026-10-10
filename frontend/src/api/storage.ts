import { api } from './client';
import { toast } from '../state/toasts';

// Storage manager: disks, SMART health, ZFS pools, datasets and snapshots.

export type Usage = 'system' | 'pool' | 'mounted' | 'md' | 'lvm' | 'storage' | 'free';
export type Layout = 'stripe' | 'mirror' | 'raidz1' | 'raidz2' | 'raidz3';

export interface StorageStatus {
  supported: boolean;
  reason?: string;
  native: boolean;
  demo: boolean;
  zfs: { installed: boolean; module: boolean; version?: string; can_install: boolean };
  smart: { installed: boolean; can_install: boolean };
}

export interface SmartSummary {
  available: boolean;
  passed?: boolean;
  temperature_c?: number;
  power_on_hours?: number;
  reallocated?: number;
  pending?: number;
  percent_used?: number;
  media_errors?: number;
  test_running?: boolean;
  test_remaining?: number;
  asleep?: boolean;
}

export interface Partition {
  name: string;
  path: string;
  size: number;
  fstype?: string;
  label?: string;
  uuid?: string;
  mountpoints: string[];
}

export interface Disk {
  name: string;
  path: string;
  by_id?: string;
  size: number;
  model?: string;
  serial?: string;
  transport?: string;
  rotational: boolean;
  removable: boolean;
  partitions: Partition[];
  usage: Usage;
  available: boolean;
  reason?: string;
  smart?: SmartSummary;
}

export interface SmartReport {
  summary: SmartSummary;
  attributes: { id: number; name: string; value: number; worst: number; thresh: number; raw: string; failing: boolean }[];
  nvme?: {
    critical_warning: number;
    available_spare: number;
    available_spare_threshold: number;
    percentage_used: number;
    data_units_read: number;
    data_units_written: number;
    power_cycles: number;
    unsafe_shutdowns: number;
    media_errors: number;
    num_err_log_entries: number;
  };
  self_tests: { type: string; status: string; hours: number }[];
}

export interface Vdev {
  name: string;
  type: string;
  health: string;
  read_errors: number;
  write_errors: number;
  checksum_errors: number;
  disk?: string;
  path?: string;
  guid?: string;
  was?: string;
  note?: string;
  children: Vdev[];
}

export interface Scan {
  function?: 'scrub' | 'resilver';
  state: 'none' | 'scanning' | 'paused' | 'finished' | 'canceled';
  percent?: number;
  eta_seconds?: number;
  errors?: number;
  finished?: string;
}

export interface Pool {
  name: string;
  health: string;
  size: number;
  allocated: number;
  free: number;
  fragmentation: number;
  capacity: number;
  mountpoint: string;
  layout: string;
  vdevs: Vdev[];
  scan: Scan;
  errors?: string;
  status?: string;
  in_files: boolean;
}

export interface ImportablePool {
  name: string;
  id: string;
  health: string;
  disks: string[];
}

export interface Dataset {
  name: string;
  used: number;
  available: number;
  referenced: number;
  mountpoint: string;
  compression: string;
  compressratio: number;
  quota: number;
}

export interface Snapshot {
  name: string;
  created: string;
  used: number;
  referenced: number;
}

export interface Job {
  id: string;
  kind: string;
  target: string;
  state: 'running' | 'done' | 'failed';
  error?: string;
  started: string;
  finished?: string;
}

export interface Alert {
  key: string;
  level: 'warning' | 'error';
  title: string;
  body: string;
}

const P = '/api/v1/storage';
const q = encodeURIComponent;

export const storageApi = {
  status: () => api<StorageStatus>(`${P}/status`),
  install: (tool: 'zfs' | 'smart') => api<StorageStatus>(`${P}/tools/${tool}/install`, { method: 'POST' }),
  disks: () => api<{ disks: Disk[] }>(`${P}/disks`),
  smart: (name: string) => api<SmartReport>(`${P}/disks/${q(name)}/smart`),
  smartTest: (name: string, type: 'short' | 'long') => api<{ job: Job }>(`${P}/disks/${q(name)}/smart-test`, { method: 'POST', body: { type } }),
  format: (name: string, label: string, add_to_files: boolean, confirm: string) =>
    api<{ job: Job }>(`${P}/disks/${q(name)}/format`, { method: 'POST', body: { label, add_to_files, confirm } }),
  diskFiles: (name: string, enabled: boolean) => api<void>(`${P}/disks/${q(name)}/files`, { method: 'POST', body: { enabled } }),
  pools: () => api<{ pools: Pool[]; importable: ImportablePool[] }>(`${P}/pools`),
  createPool: (body: { name: string; layout: Layout; disks: string[]; compression: string; add_to_files: boolean; confirm: string }) =>
    api<{ job: Job }>(`${P}/pools`, { method: 'POST', body }),
  importPool: (name: string) => api<void>(`${P}/pools/import`, { method: 'POST', body: { name } }),
  exportPool: (name: string) => api<void>(`${P}/pools/${q(name)}/export`, { method: 'POST' }),
  destroyPool: (name: string, confirm: string) => api<void>(`${P}/pools/${q(name)}?confirm=${q(confirm)}`, { method: 'DELETE' }),
  scrub: (name: string, action: 'start' | 'stop') => api<void>(`${P}/pools/${q(name)}/scrub`, { method: 'POST', body: { action } }),
  addVdev: (name: string, body: { layout: Layout; disks: string[]; confirm: string; allow_mismatch?: boolean }) =>
    api<{ job: Job }>(`${P}/pools/${q(name)}/add`, { method: 'POST', body }),
  replace: (name: string, body: { old: string; disk: string; confirm: string }) =>
    api<{ job: Job }>(`${P}/pools/${q(name)}/replace`, { method: 'POST', body }),
  poolFiles: (name: string, enabled: boolean) => api<void>(`${P}/pools/${q(name)}/files`, { method: 'POST', body: { enabled } }),
  datasets: (pool: string) => api<Dataset[]>(`${P}/pools/${q(pool)}/datasets`),
  createDataset: (body: { name: string; quota?: number; compression?: string }) => api<void>(`${P}/datasets`, { method: 'POST', body }),
  updateDataset: (body: { name: string; quota?: number; compression?: string }) => api<void>(`${P}/datasets`, { method: 'PUT', body }),
  deleteDataset: (name: string, confirm: string) => api<void>(`${P}/datasets?name=${q(name)}&confirm=${q(confirm)}`, { method: 'DELETE' }),
  snapshots: (dataset: string) => api<Snapshot[]>(`${P}/snapshots?dataset=${q(dataset)}`),
  createSnapshot: (dataset: string, name: string) => api<void>(`${P}/snapshots`, { method: 'POST', body: { dataset, name } }),
  rollback: (name: string, confirm: string) => api<void>(`${P}/snapshots/rollback`, { method: 'POST', body: { name, confirm } }),
  deleteSnapshot: (name: string) => api<void>(`${P}/snapshots?name=${q(name)}`, { method: 'DELETE' }),
  settings: () => api<{ auto_scrub: boolean }>(`${P}/settings`),
  setSettings: (auto_scrub: boolean) => api<{ auto_scrub: boolean }>(`${P}/settings`, { method: 'PUT', body: { auto_scrub } }),
  job: (id: string) => api<Job>(`${P}/jobs/${q(id)}`),
};

/** Polls a job until it finishes; resolves with the final job (or null if polling failed). */
export async function waitForJob(id: string, onTick?: (j: Job) => void): Promise<Job | null> {
  for (;;) {
    const r = await storageApi.job(id);
    if (!r.ok) return null;
    onTick?.(r.data);
    if (r.data.state !== 'running') return r.data;
    await new Promise((res) => window.setTimeout(res, 1200));
  }
}

// Health alerts from the storage.alerts topic, toasted once per session.
const seenAlerts = new Set<string>();
export function showStorageAlerts(data: unknown) {
  if (!Array.isArray(data)) return;
  for (const a of data as Alert[]) {
    if (seenAlerts.has(a.key)) continue;
    seenAlerts.add(a.key);
    toast(a.level === 'error' ? 'error' : 'info', a.title, a.body);
  }
}

// ---- helpers shared by the Storage app ----

export const LAYOUTS: { id: Layout; title: string; min: number; parity: number; blurb: string }[] = [
  { id: 'stripe', title: 'Stripe', min: 1, parity: 0, blurb: 'All the space and speed, but if any disk fails, everything is lost.' },
  { id: 'mirror', title: 'Mirror', min: 2, parity: -1, blurb: 'Every disk holds the same copy. Safest, but you get one disk of space.' },
  { id: 'raidz1', title: 'RAIDZ1', min: 3, parity: 1, blurb: 'Like RAID 5: one disk’s worth of space protects the rest.' },
  { id: 'raidz2', title: 'RAIDZ2', min: 4, parity: 2, blurb: 'Like RAID 6: survives two failed disks. Good for big disks.' },
  { id: 'raidz3', title: 'RAIDZ3', min: 5, parity: 3, blurb: 'Survives three failed disks, for large arrays.' },
];

/** Usable bytes and how many disks can fail, computed from the smallest disk. */
export function layoutMath(layout: Layout, sizes: number[]): { usable: number; canFail: number } {
  const n = sizes.length;
  if (!n) return { usable: 0, canFail: 0 };
  const min = Math.min(...sizes);
  switch (layout) {
    case 'stripe':
      return { usable: sizes.reduce((a, b) => a + b, 0), canFail: 0 };
    case 'mirror':
      return { usable: min, canFail: n - 1 };
    case 'raidz1':
      return { usable: (n - 1) * min, canFail: 1 };
    case 'raidz2':
      return { usable: (n - 2) * min, canFail: 2 };
    case 'raidz3':
      return { usable: (n - 3) * min, canFail: 3 };
  }
}

export type DiskKind = 'nvme' | 'ssd' | 'hdd' | 'usb';
export function diskKind(d: Disk): DiskKind {
  if (d.transport === 'usb') return 'usb';
  if (d.transport === 'nvme' || d.name.startsWith('nvme')) return 'nvme';
  return d.rotational ? 'hdd' : 'ssd';
}

export const KIND_LABEL: Record<DiskKind, string> = { nvme: 'NVMe SSD', ssd: 'SSD', hdd: 'Hard drive', usb: 'USB drive' };

export type Health = 'good' | 'warn' | 'bad' | 'unknown';
export function smartHealth(s?: SmartSummary): { level: Health; label: string } {
  if (!s || !s.available) return { level: 'unknown', label: 'No SMART' };
  if (s.passed === false) return { level: 'bad', label: 'Failing' };
  if ((s.reallocated ?? 0) > 0 || (s.pending ?? 0) > 0 || (s.media_errors ?? 0) > 0 || (s.percent_used ?? 0) >= 90) return { level: 'warn', label: 'Warning' };
  if (s.asleep && s.passed === undefined) return { level: 'unknown', label: 'Asleep' };
  return { level: 'good', label: 'Healthy' };
}

export function poolHealth(h: string): Health {
  if (h === 'ONLINE') return 'good';
  if (h === 'DEGRADED') return 'warn';
  return 'bad';
}

export const USAGE_LABEL: Record<Usage, string> = {
  system: 'System',
  pool: 'In a pool',
  mounted: 'In use',
  md: 'RAID (md)',
  lvm: 'LVM',
  storage: 'Storage',
  free: 'Free',
};

export function fmtEta(sec?: number): string {
  if (sec == null) return '';
  const h = Math.floor(sec / 3600);
  const m = Math.round((sec % 3600) / 60);
  if (h >= 24) return `${Math.floor(h / 24)} d ${h % 24} h left`;
  if (h) return `${h} h ${m} min left`;
  return `${Math.max(1, m)} min left`;
}
