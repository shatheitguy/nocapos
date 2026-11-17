import { api } from './client';
import { wsBase } from '../native/platform';

export interface RdpEngineStatus {
  phase: 'idle' | 'preparing' | 'ready' | 'error';
  message: string;
  ready: boolean;
}

export interface RdpTicketRequest {
  hostname: string;
  port?: number;
  username?: string;
  password?: string;
  domain?: string;
  security?: 'any' | 'nla' | 'tls' | 'rdp';
  ignore_cert?: boolean;
  width: number;
  height: number;
  dpi?: number;
}

/** Reports the RDP engine (guacd) state, and starts preparing it if needed. */
export const rdpStatus = () => api<RdpEngineStatus>('/api/v1/rdp/status');

/** Exchanges connection details (incl. password) for a single-use ticket. */
export const rdpTicket = (req: RdpTicketRequest) =>
  api<{ ticket: string }>('/api/v1/rdp/ticket', { method: 'POST', body: req });

export function rdpSocketURL(): string {
  return `${wsBase()}/ws/rdp`;
}
