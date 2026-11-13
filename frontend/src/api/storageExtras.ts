import { api } from './client';
import type { Disk, Job, Pool } from './storage';

// Storage extras: boot drive, dry-run plans, speed-boost drives, spin-down,
// snapshot schedules, encrypted folders and the flexible array.

export type BootWhy = 'not-zfs' | 'pi' | 'sd' | 'arch' | 'already' | 'unknown';

export interface BootPoolMember {
  pool: string;
  part: number;
  old: string;
}

export interface BootDrive {
  source: string;
  fstype: string;
  size: number;
  used: number;
  avail: number;
  disks: Disk[];
  pool?: Pool;
  mirrored: boolean;
  degraded: boolean;
  efi: boolean;
  platform: 'pc' | 'pi';
  can_mirror: boolean;
  mode?: 'attach' | 'replace';
  why?: BootWhy;
  why_text?: string;
  min_size?: number;
  candidates: string[];
  members?: BootPoolMember[];
}

export interface BootMirrorDryRun {
  plan: { mode: string; source: string; target: string; members: BootPoolMember[]; efi: boolean };
  disk: Disk;
  commands: string[][];
  confirm: string;
}

export interface PlanRequest {
  action: 'create' | 'add' | 'support' | 'replace';
  pool?: string;
  name?: string;
  layout?: string;
  kind?: SupportKind;
  disks: string[];
  old?: string;
  compression?: string;
}

export interface PoolPlan {
  action: string;
  layout?: string;
  disks: Disk[];
  raw: number;
  usable: number;
  tolerance: number;
  commands: string[][];
  warnings: string[];
  confirm: string;
}

export type SupportKind = 'cache' | 'log' | 'special';

export interface ARCStats {
  size: number;
  target: number;
  max: number;
  hits: number;
  misses: number;
  hit_rate: number;
  l2_size: number;
  l2_hits: number;
  l2_misses: number;
}

export interface ScrubResult {
  finished: string;
  errors: number;
  function: string;
}

export interface SpindownDisk {
  name: string;
  by_id: string;
  model?: string;
  minutes: number;
}

export interface SnapSchedule {
  dataset: string;
  hourly: number;
  daily: number;
  weekly: number;
  monthly: number;
}

export interface SnapOverview {
  dataset: string;
  mountpoint: string;
  count: number;
  used: number;
  latest?: string;
  schedule?: SnapSchedule;
}

export interface SnapEntry {
  name: string;
  dir: boolean;
  size: number;
  modified: string;
}

export interface CryptInfo {
  name: string;
  encrypted: boolean;
  root?: string;
  unlocked: boolean;
  mounted: boolean;
  auto_lock_minutes?: number;
  unlocked_at?: string;
}

export interface FlexPlan {
  data: string[];
  parity: string[];
  usable: number;
  tolerance: number;
  warnings: string[];
  enabled: boolean;
}

const P = '/api/v1/storage';
const q = encodeURIComponent;

export const storageExtras = {
  boot: () => api<BootDrive>(`${P}/boot`),
  bootPlan: (disk: string) => api<BootMirrorDryRun>(`${P}/boot/mirror/plan`, { method: 'POST', body: { disk } }),
  bootMirror: (disk: string, confirm: string) => api<{ job: Job }>(`${P}/boot/mirror`, { method: 'POST', body: { disk, confirm } }),
  plan: (body: PlanRequest) => api<PoolPlan>(`${P}/plan`, { method: 'POST', body }),
  addSupport: (pool: string, body: { kind: SupportKind; disks: string[]; confirm: string }) =>
    api<{ job: Job }>(`${P}/pools/${q(pool)}/support`, { method: 'POST', body }),
  removeSupport: (pool: string, dev: string) => api<void>(`${P}/pools/${q(pool)}/support?dev=${q(dev)}`, { method: 'DELETE' }),
  expand: (pool: string) => api<void>(`${P}/pools/${q(pool)}/expand`, { method: 'POST' }),
  scrubs: (pool: string) => api<ScrubResult[]>(`${P}/pools/${q(pool)}/scrubs`),
  encryption: (pool: string) => api<CryptInfo[]>(`${P}/pools/${q(pool)}/encryption`),
  arc: () => api<ARCStats>(`${P}/arc`),
  spindown: () => api<{ disks: SpindownDisk[]; choices: number[] }>(`${P}/spindown`),
  setSpindown: (name: string, minutes: number) => api<void>(`${P}/spindown/${q(name)}`, { method: 'PUT', body: { minutes } }),
  snapOverview: () => api<SnapOverview[]>(`${P}/snapshots/overview`),
  setSchedule: (s: SnapSchedule) => api<void>(`${P}/snapshots/schedules`, { method: 'PUT', body: s }),
  clone: (name: string, target: string) => api<void>(`${P}/snapshots/clone`, { method: 'POST', body: { name, target } }),
  browse: (name: string, path: string) => api<SnapEntry[]>(`${P}/snapshots/browse?name=${q(name)}&path=${q(path)}`),
  createEncrypted: (body: { name: string; passphrase: string; repeat: string; quota?: number; auto_lock_minutes?: number }) =>
    api<void>(`${P}/encrypted`, { method: 'POST', body }),
  unlock: (name: string, passphrase: string) => api<void>(`${P}/encrypted/unlock`, { method: 'POST', body: { name, passphrase } }),
  lock: (name: string) => api<void>(`${P}/encrypted/lock`, { method: 'POST', body: { name } }),
  autoLock: (name: string, minutes: number) => api<void>(`${P}/encrypted/auto-lock`, { method: 'PUT', body: { name, minutes } }),
  scrubSettings: () => api<{ auto_scrub: boolean; scrub_every?: string }>(`${P}/settings`),
  setScrubSettings: (auto_scrub: boolean, scrub_every: string) =>
    api<{ auto_scrub: boolean }>(`${P}/settings`, { method: 'PUT', body: { auto_scrub, scrub_every } }),
  flexPlan: (data: string[], parity: string[]) => api<FlexPlan>(`${P}/flex/plan`, { method: 'POST', body: { data, parity } }),
  flexCreate: (data: string[], parity: string[], confirm: string) => api<{ job: Job }>(`${P}/flex`, { method: 'POST', body: { data, parity, confirm } }),
  install: (tool: 'hdparm' | 'flex') => api<unknown>(`${P}/tools/${tool}/install`, { method: 'POST' }),
};
