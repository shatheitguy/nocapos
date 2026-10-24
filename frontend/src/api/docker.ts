import { api } from './client';
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
  privileged: boolean;
  devices: string[];
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
};

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
});
