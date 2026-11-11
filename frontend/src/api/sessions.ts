import { api } from './client';

/** One place an account is signed in: a login session or a linked phone. */
export interface SignedInSession {
  id: string;
  source: 'session' | 'device';
  kind: 'web' | 'phone' | 'desktop' | 'cli';
  device_name?: string;
  browser?: string;
  os?: string;
  ip?: string;
  /** Roughly where the address is: this machine, the local network or the internet. */
  network?: 'local' | 'lan' | 'internet';
  created_at: string;
  last_seen_at: string;
  /** The session (or device) this request came from. */
  current: boolean;
  /** Administrators' lists: whose it is. */
  username?: string;
}

/** A new sign-in to your account. */
export interface SigninAlert {
  id: number;
  /** What "Not you? Sign it out" ends. */
  session_id: string;
  source: SignedInSession['source'];
  kind: SignedInSession['kind'];
  device_name?: string;
  browser?: string;
  os?: string;
  ip?: string;
  network?: SignedInSession['network'];
  at: string;
}

export interface SessionsList {
  sessions: SignedInSession[];
  alerts: SigninAlert[];
}

export const sessionsApi = {
  mine: () => api<SessionsList>('/api/v1/auth/sessions'),
  end: (id: string) => api<void>(`/api/v1/auth/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  endOthers: () => api<{ ended: number }>('/api/v1/auth/sessions/end-others', { method: 'POST' }),
  /** Administrators: everyone's sessions, or one person's. */
  all: (username?: string) => api<SessionsList>(`/api/v1/sessions${username ? `?user=${encodeURIComponent(username)}` : ''}`),
  endAllFor: (username: string) => api<{ ended: number }>(`/api/v1/users/${encodeURIComponent(username)}/sessions/end`, { method: 'POST' }),
};
