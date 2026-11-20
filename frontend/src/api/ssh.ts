import { api } from './client';
import type { FwPending } from './shield';

// Remote login (SSH): the switch (admins) and each person's keys.

export type SshWho = 'admins' | 'everyone';

export interface SshStatus {
  supported: boolean;
  reason?: string;
  installed: boolean;
  can_install: boolean;
  enabled: boolean;
  running: boolean;
  port: number;
  who: SshWho;
  password: boolean;
  outside: boolean;
  penalties: boolean;
  error?: string;
  overrides?: string[];
  addresses: string[];
  user: string;
  local_only: boolean;
  has_keys: boolean;
  firewall: {
    supported: boolean;
    installed: boolean;
    active: boolean;
    rule: boolean;
    blocked: boolean;
    pending?: FwPending;
  };
}

export interface SshKey {
  type: string;
  bits?: number;
  fingerprint: string;
  name: string;
  added?: string;
  options?: boolean;
}

export interface SshKeys {
  supported: boolean;
  reason?: string;
  error?: string;
  user: string;
  keys: SshKey[];
  others?: { user: string; keys: SshKey[] }[];
}

export interface SshJob {
  id: string;
  running: boolean;
  ok: boolean;
  output: string;
  error?: string;
}

export interface SshChange {
  enabled?: boolean;
  who?: SshWho;
  password?: boolean;
  outside?: boolean;
  confirm?: boolean;
}

export const sshApi = {
  status: () => api<SshStatus>('/api/v1/ssh'),
  /** A status, or (turning on when the server must be installed first) a package job. */
  change: (body: SshChange) => api<SshStatus & { job?: SshJob }>('/api/v1/ssh', { method: 'PUT', body }),
  fixFirewall: () => api<SshStatus>('/api/v1/ssh/firewall/fix', { method: 'POST' }),
  job: (id: string) => api<SshJob>(`/api/v1/packages/jobs/${id}`),
  keys: () => api<SshKeys>('/api/v1/ssh/keys'),
  addKey: (key: string, name: string) => api<SshKeys>('/api/v1/ssh/keys', { method: 'POST', body: { key, name } }),
  removeKey: (user: string, fingerprint: string) => api<SshKeys>('/api/v1/ssh/keys/delete', { method: 'POST', body: { user, fingerprint } }),
};
