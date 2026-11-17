import { api } from './client';
import { wsBase } from '../native/platform';
import type { RemoteOptions, RemoteProtocol } from '../apps/remote/rdModel';

export interface RdpEngineStatus {
  phase: 'idle' | 'preparing' | 'ready' | 'error';
  message: string;
  ready: boolean;
}

export interface RdpTicketRequest extends RemoteOptions {
  protocol?: RemoteProtocol;
  hostname: string;
  port?: number;
  username?: string;
  password?: string;
  domain?: string;
  /** A saved computer: its remembered passwords fill in empty ones. */
  connection_id?: string;
  gateway_password?: string;
  width: number;
  height: number;
}

/** A saved computer (the server never sends its passwords back). */
export interface SavedComputer {
  id: string;
  name: string;
  protocol: RemoteProtocol;
  hostname: string;
  port: number;
  username: string;
  domain: string;
  options: RemoteOptions;
  has_password: boolean;
  has_gateway_password: boolean;
  last_used?: number;
  created_at: number;
  updated_at: number;
}

export interface SavedComputerInput {
  name: string;
  protocol: RemoteProtocol;
  hostname: string;
  port: number;
  username: string;
  domain: string;
  options: RemoteOptions;
  /** Keep password (and gateway_password); empty ones keep what's remembered. */
  remember_password: boolean;
  password?: string;
  gateway_password?: string;
}

/** Reports the remote desktop engine (guacd) state, and starts preparing it if needed. */
export const rdpStatus = () => api<RdpEngineStatus>('/api/v1/rdp/status');

/** Exchanges connection details (incl. password) for a single-use ticket. */
export const rdpTicket = (req: RdpTicketRequest) => api<{ ticket: string }>('/api/v1/rdp/ticket', { method: 'POST', body: req });

export const savedComputers = {
  list: () => api<{ connections: SavedComputer[]; can_remember: boolean }>('/api/v1/rdp/connections'),
  create: (c: SavedComputerInput) => api<SavedComputer>('/api/v1/rdp/connections', { method: 'POST', body: c }),
  update: (id: string, c: SavedComputerInput) => api<SavedComputer>(`/api/v1/rdp/connections/${encodeURIComponent(id)}`, { method: 'PUT', body: c }),
  remove: (id: string) => api<null>(`/api/v1/rdp/connections/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  duplicate: (id: string) => api<SavedComputer>(`/api/v1/rdp/connections/${encodeURIComponent(id)}/duplicate`, { method: 'POST' }),
};

export function rdpSocketURL(): string {
  return `${wsBase()}/ws/rdp`;
}
