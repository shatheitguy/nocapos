// Mirrors of alfad's JSON responses.

export interface User {
  id: string;
  username: string;
  role: 'admin' | 'user';
  disabled: boolean;
  created_at: string;
}

export interface Session {
  access_token: string;
  token_type: string;
  expires_at: string;
  user: User;
}

export interface HostInfo {
  hostname: string;
  os: string;
  kernel: string;
  arch: string;
  cpu_model: string;
  cpu_cores: number;
  board?: string;
  mem_total: number;
  boot_time: string;
}

export interface GPUMetrics {
  utilization?: number;
  mem_used?: number;
  mem_total?: number;
  temp_c?: number;
  power_w?: number;
  freq_mhz?: number;
}

export interface Accelerator {
  id: string;
  kind: 'gpu' | 'npu' | 'tpu' | 'display';
  vendor: string;
  name: string;
  driver?: string;
  pci_address?: string;
  device_nodes?: string[];
  note?: string;
  metrics: GPUMetrics;
}

export interface DockerInfo {
  server_version: string;
  api_version: string;
  os: string;
  arch: string;
  storage_driver: string;
  cpus: number;
  mem_total: number;
  containers: number;
  running: number;
  images: number;
  runtimes: string[];
}

export interface SystemInfo {
  version: string;
  host: HostInfo;
  docker?: DockerInfo;
  docker_error?: string;
  accelerators: Accelerator[];
}

export interface Snapshot {
  time: string;
  uptime: number;
  cpu: { percent: number; per_core: number[]; load1: number; load5: number; load15: number };
  memory: { total: number; used: number; available: number; percent: number; swap_total: number; swap_used: number };
  disks: { path: string; total: number; used: number; free: number; percent: number }[] | null;
  network: { interface: string; rx_bytes: number; tx_bytes: number; rx_rate: number; tx_rate: number }[] | null;
  temperatures: { sensor: string; celsius: number }[] | null;
  accelerators: Accelerator[] | null;
}

export interface Port {
  ip?: string;
  private: number;
  public?: number;
  protocol: string;
}

export interface Container {
  id: string;
  name: string;
  image: string;
  state: string;
  status: string;
  created: string;
  ports: Port[];
  project?: string;
  system: boolean;
  labels: Record<string, string> | null;
}

export interface ContainerDetail {
  id: string;
  name: string;
  image: string;
  created: string;
  state: string;
  running: boolean;
  exit_code: number;
  oom_killed: boolean;
  started_at: string;
  health?: string;
  restart_count: number;
  restart_policy: string;
  privileged: boolean;
  network_mode: string;
  system: boolean;
  mounts: { type: string; source: string; destination: string; rw: boolean }[];
  networks: { name: string; ip?: string }[];
}

export interface ContainerStats {
  time: string;
  cpu_percent: number;
  mem_usage: number;
  mem_limit: number;
  mem_percent: number;
  net_rx_rate: number;
  net_tx_rate: number;
  block_read: number;
  block_write: number;
  pids: number;
}

export interface LogLine {
  stream: 'stdout' | 'stderr';
  time: string;
  text: string;
}

export interface DockerEvent {
  type: string;
  action: string;
  id: string;
  name?: string;
  image?: string;
  exit_code?: string;
  time: string;
}
