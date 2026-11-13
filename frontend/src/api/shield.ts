import { api } from './client';

// Shield: the firewall (Linux's ufw does the work) and the DNS ad blocker.

export type FwAction = 'allow' | 'deny' | 'reject' | 'limit';
export type Policy = 'allow' | 'deny' | 'reject';

export interface FwRule {
  action: FwAction;
  direction: 'in' | 'out';
  interface?: string;
  proto: 'any' | 'tcp' | 'udp';
  port?: string;
  from?: string;
  to?: string;
  app?: string;
  comment?: string;
}

export interface FwEntry extends FwRule {
  number: number;
  v6?: boolean;
  raw: string;
  editable: boolean;
  guard?: boolean;
}

export interface FwPending {
  id: string;
  what: string;
  deadline: string;
}

export interface FwState {
  supported: boolean;
  reason?: string;
  installed: boolean;
  can_install: boolean;
  active: boolean;
  incoming: Policy;
  outgoing: Policy;
  routed?: string;
  logging: 'off' | 'low' | 'medium' | 'high' | 'full';
  ipv6: boolean;
  rules: FwEntry[];
  apps: { name: string; title?: string; description?: string; ports?: string }[];
  pending?: FwPending;
  keep: { client: string; ports: number[]; ssh: number };
  presets: { id: string; name: string; ports: string[] }[];
  local_nets: string[];
}

export interface Exposed {
  port: number;
  proto: 'tcp' | 'udp';
  name: string;
  kind: 'nocap' | 'ssh' | 'container' | 'system';
  process?: string;
  open: boolean;
  locked?: boolean;
  bypass?: boolean;
}

export interface BlockedConn {
  time: string;
  action: string;
  interface?: string;
  out?: boolean;
  src: string;
  dst: string;
  proto: string;
  src_port?: number;
  dst_port?: number;
}

export interface DnsList {
  id: string;
  name: string;
  url: string;
  enabled: boolean;
  domains?: number;
  updated?: string;
  error?: string;
}

export interface DnsRecord {
  name: string;
  type: 'A' | 'AAAA' | 'CNAME';
  value: string;
}

export interface DnsSettings {
  enabled: boolean;
  listen: 'auto' | 'all' | 'lan';
  port: number;
  upstreams: string[];
  bootstrap: string;
  block_mode: 'null' | 'nxdomain';
  cache_size: number;
  check_cnames: boolean;
  lists: DnsList[];
  refresh_hours: number;
  allow: string[];
  block: string[];
  records: DnsRecord[];
  log_enabled: boolean;
  log_hours: number;
  log_clients: boolean;
  use_on_server: boolean;
}

export interface DnsStatus {
  settings: DnsSettings;
  running: boolean;
  listening: string[];
  listen_error?: string;
  listen_note?: string;
  paused_until?: string;
  lists: DnsList[];
  domains: number;
  cache_entries: number;
  refreshing: boolean;
  port53: { stub: boolean; freed: boolean; can_manage: boolean };
  addresses: string[];
}

export interface DnsPoint {
  time: string;
  total: number;
  blocked: number;
}

export interface DnsCount {
  name: string;
  count: number;
  blocked?: number;
}

export interface DnsStats {
  log_enabled: boolean;
  today: number;
  blocked_today: number;
  top_blocked: DnsCount[];
  top_allowed: DnsCount[];
  top_clients: DnsCount[];
  last_hour: DnsPoint[];
  last_day: DnsPoint[];
  clients: { client: string; total: number; blocked: number; last_seen: string }[];
  paused_until?: string;
}

export interface DnsQuery {
  time: string;
  client?: string;
  name: string;
  type: string;
  status: 'blocked' | 'forwarded' | 'cached' | 'local' | 'error';
  reason?: string;
  upstream?: string;
  ms: number;
  rcode?: string;
}

const fw = '/api/v1/shield/firewall';
const dns = '/api/v1/shield/dns';

export const shieldApi = {
  overview: () => api<{ firewall: FwState; firewall_error?: string; dns: DnsStatus; stats: DnsStats }>('/api/v1/shield'),
  firewall: () => api<FwState>(fw),
  install: () => api<FwState>(`${fw}/install`, { method: 'POST' }),
  enable: () => api<FwState>(`${fw}/enable`, { method: 'POST' }),
  disable: () => api<FwState>(`${fw}/disable`, { method: 'POST' }),
  defaults: (body: { incoming?: Policy; outgoing?: Policy }) => api<FwState>(`${fw}/defaults`, { method: 'PUT', body }),
  logging: (level: string) => api<FwState>(`${fw}/logging`, { method: 'PUT', body: { level } }),
  ipv6: (enabled: boolean) => api<FwState>(`${fw}/ipv6`, { method: 'PUT', body: { enabled } }),
  addRule: (rule: FwRule, at?: number) => api<FwState>(`${fw}/rules`, { method: 'POST', body: at === undefined ? { rule } : { rule, at } }),
  updateRule: (raw: string, rule: FwRule) => api<FwState>(`${fw}/rules`, { method: 'PUT', body: { raw, rule } }),
  deleteRule: (raw: string) => api<FwState>(`${fw}/rules/delete`, { method: 'POST', body: { raw } }),
  moveRule: (raw: string, to: number) => api<FwState>(`${fw}/rules/move`, { method: 'POST', body: { raw, to } }),
  preset: (id: string, source: string) => api<FwState>(`${fw}/presets/${encodeURIComponent(id)}`, { method: 'POST', body: { source } }),
  exposed: () => api<{ active: boolean; services: Exposed[] }>(`${fw}/exposed`),
  setExposed: (e: Exposed, open: boolean) => api<FwState>(`${fw}/exposed`, { method: 'PUT', body: { port: e.port, proto: e.proto, name: e.name, open } }),
  blocked: () => api<{ blocked: BlockedConn[] }>(`${fw}/blocked`),
  keep: (id: string) => api<FwState>(`${fw}/keep`, { method: 'POST', body: { id } }),
  undo: (id: string) => api<FwState>(`${fw}/undo`, { method: 'POST', body: { id } }),

  dns: () => api<DnsStatus>(dns),
  saveDns: (s: DnsSettings) => api<DnsStatus>(dns, { method: 'PUT', body: s }),
  stats: () => api<DnsStats>(`${dns}/stats`),
  log: (f: { client?: string; q?: string; status?: string; before?: number }) => {
    const p = new URLSearchParams();
    for (const [k, v] of Object.entries(f)) if (v !== undefined && v !== '') p.set(k, String(v));
    return api<{ queries: DnsQuery[] }>(`${dns}/log?${p.toString()}`);
  },
  clearLog: () => api<null>(`${dns}/log`, { method: 'DELETE' }),
  pause: (minutes: number) => api<DnsStatus>(`${dns}/pause`, { method: 'POST', body: { minutes } }),
  refresh: () => api<DnsStatus>(`${dns}/refresh`, { method: 'POST' }),
  port53: (free: boolean) => api<DnsStatus>(`${dns}/port53`, { method: 'POST', body: { free } }),
};
