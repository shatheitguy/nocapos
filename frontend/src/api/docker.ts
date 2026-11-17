import { api, getAccessToken } from './client';
import type { ContainerStats } from './types';

export interface EnvVar {
  key: string;
  value: string;
}

export interface PortMap {
  host_ip?: string;
  host: number;
  container: number;
  protocol: 'tcp' | 'udp';
}

export interface SpecMount {
  type: 'bind' | 'volume';
  source: string;
  target: string;
  read_only: boolean;
}

export interface NetLink {
  name: string;
  ipv4?: string;
  aliases?: string[];
}

export interface ContainerSpec {
  name: string;
  image: string;
  cmd?: string[];
  env: EnvVar[];
  ports: PortMap[];
  mounts: SpecMount[];
  network_mode: string;
  networks: NetLink[];
  restart: 'no' | 'always' | 'unless-stopped' | 'on-failure';
  memory_mb: number;
  cpus: number;
  /** Most processes the container may run (0 = no limit). */
  pids_limit?: number;
  /** Read-only root filesystem. */
  read_only?: boolean;
  privileged: boolean;
  devices: string[];
  /** Extra Linux capabilities, e.g. NET_ADMIN. */
  cap_add?: string[];
  hostname?: string;
}

export interface SpecInfo {
  spec: ContainerSpec;
  app?: string;
  service?: string;
  stack?: string;
  system: boolean;
}

export interface DockerNetwork {
  id: string;
  name: string;
  driver: string;
  subnet?: string;
  gateway?: string;
  ip_range?: string;
  parent?: string;
  mode?: string;
  internal: boolean;
  builtin: boolean;
  containers: string[];
}

export interface HostInterface {
  name: string;
  address?: string;
  subnet?: string;
  gateway?: string;
}

export interface NewNetwork {
  name: string;
  driver: 'bridge' | 'macvlan' | 'ipvlan';
  subnet?: string;
  gateway?: string;
  ip_range?: string;
  parent?: string;
  mode?: 'l2' | 'l3';
  internal?: boolean;
}

const c = (id: string) => `/api/v1/containers/${encodeURIComponent(id)}`;

export type ContainerAction = 'start' | 'stop' | 'restart' | 'pause' | 'unpause' | 'kill';

export const dockerApi = {
  /** One-shot usage of the given (or every running) container. */
  stats: (ids?: string[]) => api<{ stats: Record<string, ContainerStats> }>(`/api/v1/containers/stats${ids?.length ? `?ids=${ids.map(encodeURIComponent).join(',')}` : ''}`),
  action: (id: string, action: ContainerAction) => api<void>(`${c(id)}/${action}`, { method: 'POST' }),
  /** Download the image again and recreate the container if it changed. */
  update: (id: string) => api<{ id: string; updated: boolean; warning?: string }>(`${c(id)}/update`, { method: 'POST' }),
  removeWithVolumes: (id: string) => api<{ volumes: string[] }>(`${c(id)}?volumes=1`, { method: 'DELETE' }),
  setWebAddress: (id: string, url: string) => api<{ url: string }>(`${c(id)}/web-address`, { method: 'PUT', body: { url } }),
  spec: (id: string) => api<SpecInfo>(`${c(id)}/spec`),
  edit: (id: string, spec: ContainerSpec) => api<{ id: string; warning?: string }>(`${c(id)}/spec`, { method: 'PUT', body: spec }),
  create: (spec: ContainerSpec) => api<{ id: string }>('/api/v1/containers', { method: 'POST', body: spec }),
  remove: (id: string) => api<void>(c(id), { method: 'DELETE' }),
  networks: () => api<{ networks: DockerNetwork[]; interfaces: HostInterface[] }>('/api/v1/docker/networks'),
  createNetwork: (n: NewNetwork) => api<void>('/api/v1/docker/networks', { method: 'POST', body: n }),
  removeNetwork: (name: string) => api<void>(`/api/v1/docker/networks/${encodeURIComponent(name)}`, { method: 'DELETE' }),
  volumes: () => api<{ volumes: { name: string; driver: string }[] }>('/api/v1/docker/volumes'),
  hostPath: (root: string, path: string) => api<{ path: string }>(`/api/v1/docker/hostpath?root=${encodeURIComponent(root)}&path=${encodeURIComponent(path)}`),

  // ---- the rest of the Containers app ----
  usage: (fresh = false) => api<DockerUsage>(`/api/v1/docker/usage${fresh ? '?fresh=1' : ''}`),
  project: (name: string, action: ProjectAction) => api<{ containers?: number; stack?: boolean }>(`/api/v1/docker/projects/${encodeURIComponent(name)}/${action}`, { method: 'POST' }),
  exec: (id: string, command: string) => api<ExecResult>(`${c(id)}/exec`, { method: 'POST', body: { command } }),
  files: (id: string, path: string) => api<FileListing>(`${c(id)}/files?path=${encodeURIComponent(path)}`),
  fileContent: (id: string, path: string) => api<FilePreview>(`${c(id)}/files/content?path=${encodeURIComponent(path)}`),
  fileDownloadUrl: (id: string, path: string) => `${c(id)}/files/download?path=${encodeURIComponent(path)}`,
  uploadUrl: (id: string, dir: string, name: string) => `${c(id)}/files/upload?path=${encodeURIComponent(dir)}&name=${encodeURIComponent(name)}`,
  images: () => api<{ images: DockerImage[] }>('/api/v1/docker/images'),
  pull: (image: string) => api<{ job: string }>('/api/v1/docker/images/pull', { method: 'POST', body: { image } }),
  pullJob: (job: string) => api<PullJob>(`/api/v1/docker/images/pull/${encodeURIComponent(job)}`),
  tag: (source: string, target: string) => api<void>('/api/v1/docker/images/tag', { method: 'POST', body: { source, target } }),
  removeImage: (ref: string) => api<{ removed: string[] }>('/api/v1/docker/images/remove', { method: 'POST', body: { ref } }),
  pruneImages: (all: boolean) => api<PruneResult>('/api/v1/docker/images/prune', { method: 'POST', body: { all } }),
  volumeDetails: (usage: boolean) => api<{ volumes: DockerVolume[] }>(`/api/v1/docker/volumes/details${usage ? '?usage=1' : ''}`),
  removeVolume: (name: string) => api<void>(`/api/v1/docker/volumes/${encodeURIComponent(name)}`, { method: 'DELETE' }),
  pruneVolumes: (all: boolean) => api<PruneResult>('/api/v1/docker/volumes/prune', { method: 'POST', body: { all } }),
  connect: (network: string, container: string) => api<void>(`/api/v1/docker/networks/${encodeURIComponent(network)}/connect`, { method: 'POST', body: { container } }),
  disconnect: (network: string, container: string) => api<void>(`/api/v1/docker/networks/${encodeURIComponent(network)}/disconnect`, { method: 'POST', body: { container } }),
  devices: () => api<{ devices: HostDevice[] }>('/api/v1/docker/devices'),
  localAddresses: () => api<LocalAddresses>('/api/v1/docker/local-addresses'),
  setLocalAddresses: (enabled: boolean) => api<LocalAddresses>('/api/v1/docker/local-addresses', { method: 'PUT', body: { enabled } }),
};

export type ProjectAction = 'start' | 'stop' | 'restart' | 'remove';

export interface DockerUsage {
  containers: Record<string, { rw: number; rootfs: number }>;
  images: Record<string, number>;
  volumes: Record<string, { size: number; ref_count: number }>;
}

export interface ExecResult {
  stdout: string;
  stderr: string;
  exit_code: number;
  truncated: boolean;
}

export interface FileEntry {
  name: string;
  path: string;
  dir: boolean;
  link: boolean;
  link_target?: string;
  size: number;
  mode: string;
  mtime: string;
}

export interface FileListing {
  path: string;
  entries: FileEntry[];
  truncated: boolean;
}

export interface FilePreview {
  path: string;
  size: number;
  binary: boolean;
  truncated: boolean;
  content?: string;
}

export interface DockerImage {
  id: string;
  tags: string[];
  created: string;
  size: number;
  containers: string[];
  dangling: boolean;
}

export interface PullJob {
  id: string;
  image: string;
  status: string;
  done: boolean;
  error?: string;
  percent: number;
  layers: { id: string; status: string; current: number; total: number }[];
}

export interface PruneResult {
  removed: string[];
  reclaimed: number;
}

export interface DockerVolume {
  name: string;
  driver: string;
  mountpoint?: string;
  created?: string;
  project?: string;
  size: number;
  containers: string[];
  files_id?: string;
  system: boolean;
}

export interface HostDevice {
  path: string;
  kind: 'serial' | 'usb';
  by_id?: string;
  vendor_id?: string;
  product_id?: string;
  vendor?: string;
  product?: string;
  serial?: string;
  used_by: string[];
  device_flag: string;
}

export interface LocalAddresses {
  enabled: boolean;
  https: boolean;
  suffix: string;
  lan_ip: string;
  mdns: boolean;
  dns: boolean;
  error?: string;
  addresses: { name: string; container: string; id: string; source: string; http?: string; https?: string }[];
}

/** Fetches with the session and saves the response as a file. Returns an error message or null. */
export async function downloadWithAuth(url: string, fallbackName: string): Promise<string | null> {
  const headers: Record<string, string> = {};
  const token = getAccessToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  let r: Response;
  try {
    r = await fetch(url, { headers, credentials: 'same-origin' });
  } catch {
    return 'NoCapOS is not reachable';
  }
  if (!r.ok) {
    try {
      const j = await r.json();
      return j?.error?.message ?? `HTTP ${r.status}`;
    } catch {
      return `HTTP ${r.status}`;
    }
  }
  const cd = r.headers.get('Content-Disposition') ?? '';
  const name = /filename\*?="?([^";]+)"?/.exec(cd)?.[1] ?? fallbackName;
  const href = URL.createObjectURL(await r.blob());
  const a = document.createElement('a');
  a.href = href;
  a.download = decodeURIComponent(name);
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(href), 1000);
  return null;
}

/** Sends a file as the raw request body (uploads into a container). */
export async function uploadWithAuth(url: string, file: Blob): Promise<string | null> {
  const headers: Record<string, string> = { 'Content-Type': 'application/octet-stream' };
  const token = getAccessToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  try {
    const r = await fetch(url, { method: 'POST', headers, body: file, credentials: 'same-origin' });
    if (r.ok) return null;
    const j = await r.json().catch(() => null);
    return j?.error?.message ?? `HTTP ${r.status}`;
  } catch {
    return 'NoCapOS is not reachable';
  }
}

export const emptySpec = (): ContainerSpec => ({
  name: '',
  image: '',
  env: [],
  ports: [],
  mounts: [],
  network_mode: 'bridge',
  networks: [],
  restart: 'unless-stopped',
  memory_mb: 0,
  cpus: 0,
  privileged: false,
  devices: [],
  cap_add: [],
});
