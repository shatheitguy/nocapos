import { api } from './client';
import type { IconName } from '../components/Icon';

export interface StoreJob {
  id: string;
  app: string;
  action: 'install' | 'update' | 'uninstall';
  phase: string;
  percent: number;
  done: boolean;
  error?: string;
}

export interface StoreInstalled {
  version: string;
  web_port: number;
  status: 'running' | 'stopped' | 'partial' | 'missing';
  update_available: boolean;
  containers: { service: string; id: string; state: string }[] | null;
  credentials?: { username?: string; password?: string };
  installed_at: string;
  /** Where "Open" goes when it isn't this server + web_port: the admin's own
   *  address, or the app's own IP on a macvlan/ipvlan network. */
  web_url?: string;
  /** The admin's own web address (empty = automatic). */
  web_address?: string;
}

export interface StoreApp {
  id: string;
  name: string;
  tagline: string;
  description: string;
  category: string;
  developer: string;
  website: string;
  /** Source code repository. */
  source?: string;
  /** Image URLs for the app page gallery (none = generated banners). */
  screenshots?: string[];
  icon: IconName;
  /** The app's own logo, bundled with the UI (falls back to icon + tile). */
  logo?: string;
  /** A single-colour black logo, shown white on dark backgrounds. */
  logo_mono?: boolean;
  tile: [string, string];
  featured?: boolean;
  version: string;
  web?: { service: string; port: number; path?: string };
  first_run?: string;
  services: { name: string; image: string; ports?: { container: number; host?: number; protocol?: string }[]; volumes?: { name: string; path: string }[] }[];
  releases: { version: string; date: string; notes: string }[];
  installed?: StoreInstalled;
  /** Running job, or the last one if it ended in the last few minutes. */
  job?: StoreJob;
  /** Administrators: who can use it (absent = everyone). */
  access?: { everyone: boolean; users: string[] };
}

const path = (id: string) => `/api/v1/appstore/${encodeURIComponent(id)}`;

export const storeApi = {
  list: () => api<{ apps: StoreApp[]; docker: boolean }>('/api/v1/appstore'),
  act: (id: string, action: 'install' | 'update' | 'start' | 'stop' | 'restart') => api<{ job?: string }>(`${path(id)}/${action}`, { method: 'POST' }),
  uninstall: (id: string, deleteData: boolean) => api<{ job: string }>(`${path(id)}/uninstall`, { method: 'POST', body: { delete_data: deleteData } }),
  job: (id: string) => api<StoreJob>(`/api/v1/appstore/jobs/${encodeURIComponent(id)}`),
  /** Save the address Open uses ('' = automatic). */
  setWebAddress: (id: string, url: string) => api<{ url: string }>(`${path(id)}/web-address`, { method: 'PUT', body: { url } }),
};

/** The app's web address: the admin's own or its macvlan/ipvlan IP when
 *  there is one, else this server on the app's own port (plain HTTP). */
export function appURL(a: StoreApp): string | null {
  if (a.installed?.web_url) return a.installed.web_url;
  if (!a.installed?.web_port) return null;
  return `http://${window.location.hostname}:${a.installed.web_port}${a.web?.path ?? '/'}`;
}

export function openStoreApp(a: StoreApp) {
  const url = appURL(a);
  if (url) window.open(url, '_blank', 'noopener,noreferrer');
}
