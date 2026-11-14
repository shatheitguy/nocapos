import { api } from './client';

// Phone mirroring: your own linked phones' live screens (never anyone
// else's, administrators included).

export type MirrorLink = 'nearby' | 'remote' | '';

export interface MirrorStatus {
  /** The phone is streaming right now. */
  online: boolean;
  viewers: number;
  /** "", asking, denied, unsupported, streaming, paused, stopping */
  state: string;
  since?: string;
  /** The phone can take taps and keys (its accessibility helper is on). */
  control: boolean;
  link: MirrorLink;
}

export interface MirrorPhone {
  id: string;
  name: string;
  platform: string;
  last_seen_at: string | null;
  battery: number | null;
  app_version: string;
  /** e.g. "Google Pixel 8" ("" until the device says). */
  model?: string;
  os_version?: string;
  /** phone, tablet or computer ("" until the device says). */
  form?: '' | 'phone' | 'tablet' | 'computer';
  /** NoCap Link features it has on; "screen" = it can share its screen. */
  capabilities?: string[];
  /** Called this server in the last few minutes. */
  online?: boolean;
  mirror: MirrorStatus;
}

export const mirrorApi = {
  phones: () => api<MirrorPhone[]>('/api/v1/mirror/phones'),
  ticket: (id: string, jpeg: boolean) =>
    api<{ ticket: string; expires_in: number }>(`/api/v1/mirror/phones/${encodeURIComponent(id)}/ticket`, { method: 'POST', body: { jpeg } }),
  ask: (id: string) => api<{ ok: boolean }>(`/api/v1/mirror/phones/${encodeURIComponent(id)}/ask`, { method: 'POST' }),
};

export const mirrorSocketURL = (ticket: string) =>
  `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/ws/mirror?ticket=${encodeURIComponent(ticket)}`;
