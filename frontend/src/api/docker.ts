import { api } from './client';

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

export const dockerApi = {
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
