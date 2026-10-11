import { api } from './client';

// Virtual Desk: virtual machines on this server (QEMU/KVM through libvirt).

export type VmState = 'running' | 'paused' | 'off' | 'crashed' | 'stopping' | 'suspended';
export type VmOS = 'windows11' | 'windows10' | 'linux' | 'android' | 'other';
export type NetMode = 'nat' | 'bridge' | 'direct' | 'none';
export type PowerAction = 'start' | 'shutdown' | 'poweroff' | 'reboot' | 'pause' | 'resume';

export interface VmDisk {
  target: string;
  bus: string;
  path: string;
  format?: string;
  size: number;
  used: number;
}

export interface VmMedia {
  target: string;
  bus: string;
  path?: string;
}

export interface VmNetwork {
  mode: NetMode;
  source?: string;
  model?: string;
  mac?: string;
}

export interface VM {
  name: string;
  uuid: string;
  state: VmState;
  state_reason?: string;
  os: VmOS;
  cpus: number;
  memory_mib: number;
  firmware: 'bios' | 'uefi';
  tpm: boolean;
  disks: VmDisk[];
  media: VmMedia[];
  network: VmNetwork;
  autostart: boolean;
  vnc_port?: number;
  created?: string;
  managed: boolean;
  busy?: string;
}

export interface VmUsage {
  cpu: number;
  mem_used: number;
  mem_total: number;
}

export interface VmSnapshot {
  name: string;
  created: string;
  state: VmState;
  current: boolean;
}

export interface VmPreset {
  id: VmOS;
  title: string;
  cpus: number;
  memory_mib: number;
  disk_gib: number;
  uefi: boolean;
  tpm: boolean;
  virtio: boolean;
  video: string;
}

export interface HostIface {
  name: string;
  kind: 'bridge' | 'ethernet' | 'wireless';
}

export interface IsoFile {
  name: string;
  path: string;
  size: number;
}

export interface VmStatus {
  available: boolean;
  demo: boolean;
  native: boolean;
  reason?: string;
  missing?: string[];
  hint?: string;
  can_install: boolean;
  tools: { virsh: boolean; qemu_img: boolean; kvm: boolean; libvirtd: boolean; swtpm: boolean; ovmf: boolean; version?: string };
  host: { cpus: number; memory_mib: number; interfaces: HostIface[]; iso_dir: string; isos: IsoFile[] };
  presets: VmPreset[];
}

export interface CreateVm {
  name: string;
  os: VmOS;
  cpus: number;
  memory_mib: number;
  disk_gib: number;
  disk_image?: string;
  iso?: string;
  iso2?: string;
  virtio?: boolean;
  network: VmNetwork;
  autostart: boolean;
  start: boolean;
}

export interface UpdateVm {
  cpus?: number;
  memory_mib?: number;
  add_disk_gib?: number;
  resize?: { target: string; size_gib: number }[];
  media?: { target: string; path: string }[];
  network?: VmNetwork;
  autostart?: boolean;
}

const P = '/api/v1/vms';
const q = encodeURIComponent;

export const vmApi = {
  status: () => api<VmStatus>(`${P}/status`),
  install: () => api<VmStatus>(`${P}/install`, { method: 'POST' }),
  list: () => api<{ vms: VM[] }>(P),
  stats: () => api<{ stats: Record<string, VmUsage> }>(`${P}/stats`),
  create: (body: CreateVm) => api<{ vm: VM; warning?: string }>(P, { method: 'POST', body }),
  update: (name: string, body: UpdateVm) => api<void>(`${P}/${q(name)}`, { method: 'PATCH', body }),
  remove: (name: string, confirm: string, disks: boolean) =>
    api<void>(`${P}/${q(name)}?confirm=${q(confirm)}${disks ? '&disks=1' : ''}`, { method: 'DELETE' }),
  power: (name: string, action: PowerAction) => api<void>(`${P}/${q(name)}/power`, { method: 'POST', body: { action } }),
  clone: (name: string, newName: string) => api<void>(`${P}/${q(name)}/clone`, { method: 'POST', body: { name: newName } }),
  rename: (name: string, newName: string) => api<void>(`${P}/${q(name)}/rename`, { method: 'POST', body: { name: newName } }),
  snapshots: (name: string) => api<{ snapshots: VmSnapshot[] }>(`${P}/${q(name)}/snapshots`),
  snapshot: (name: string, snap: string) => api<void>(`${P}/${q(name)}/snapshots`, { method: 'POST', body: { name: snap } }),
  revert: (name: string, snap: string) => api<void>(`${P}/${q(name)}/snapshots/${q(snap)}/revert`, { method: 'POST' }),
  deleteSnapshot: (name: string, snap: string) => api<void>(`${P}/${q(name)}/snapshots/${q(snap)}`, { method: 'DELETE' }),
  /** A single-use ticket for the machine's screen (or, in demo mode, a message). */
  console: (name: string, width: number, height: number) =>
    api<{ ticket?: string; demo?: boolean; message?: string }>(`${P}/${q(name)}/console`, { method: 'POST', body: { width, height } }),
};

// ---- helpers shared by Virtual Desk ----

export const STATE_LABEL: Record<VmState, string> = {
  running: 'Running',
  paused: 'Paused',
  off: 'Off',
  crashed: 'Crashed',
  stopping: 'Shutting down',
  suspended: 'Suspended',
};

export const OS_LABEL: Record<VmOS, string> = {
  windows11: 'Windows 11',
  windows10: 'Windows 10',
  linux: 'Linux',
  android: 'Android',
  other: 'Other',
};

export const isLive = (v: VM) => v.state === 'running' || v.state === 'paused' || v.state === 'stopping';

/** Sizes in binary units, the way the machine sees its memory and disks. */
export function fmtGiB(bytes: number): string {
  const g = bytes / 2 ** 30;
  if (g >= 1024) return `${(g / 1024).toFixed(g >= 10240 ? 0 : 1).replace(/\.0$/, '')} TB`;
  if (g >= 1) return `${g.toFixed(g >= 100 ? 0 : 1).replace(/\.0$/, '')} GB`;
  return `${Math.max(1, Math.round(bytes / 2 ** 20))} MB`;
}

export const fmtMiB = (mib: number) => (mib >= 1024 ? `${(mib / 1024).toFixed(mib % 1024 ? 1 : 0)} GB` : `${mib} MB`);
