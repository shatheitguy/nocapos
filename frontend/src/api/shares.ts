import { api } from './client';
import type { NetSupport } from './netdrive';

// Shared folders with per-person permissions, people's access type, and
// which App Store apps each person may use (administrators).

export type Perm = 'rw' | 'ro' | 'none';
export type Access = 'desktop' | 'files';

export interface SharedFolder {
  id: number;
  name: string;
  root: string;
  root_name?: string;
  path: string;
  full_path?: string;
  description: string;
  recycle: boolean;
  network: boolean;
  read_only: boolean;
  /** Anyone on the network, no password: '' off, 'ro' read only, 'rw' read & write. */
  guest: '' | 'ro' | 'rw';
  missing?: boolean;
  holds_homes?: boolean;
  /** user name → rw | ro (people not listed have no access). */
  permissions: Record<string, 'rw' | 'ro'>;
}

export interface SharePerson {
  username: string;
  admin: boolean;
  disabled: boolean;
  access: Access;
}

export interface SharesOverview {
  folders: SharedFolder[];
  people: SharePerson[];
  admins_see_homes: boolean;
  storage?: { id: string; name: string };
  locations: { id: string; name: string }[];
  /** Someone has their own access: the old shared network account is off. */
  per_user: boolean;
  support: NetSupport;
  smb: boolean;
  webdav: boolean;
  shared_account: boolean;
}

export interface FolderInput {
  name?: string;
  root?: string;
  path?: string;
  description?: string;
  recycle?: boolean;
  network?: boolean;
  read_only?: boolean;
  guest?: '' | 'ro' | 'rw';
}

export interface PersonSpaces {
  access: Access;
  admin: boolean;
  home: boolean;
  folders: { id: number; name: string; perm: Perm; read_only: boolean }[];
  apps: { id: string; name: string; allowed: boolean; everyone: boolean }[];
}

export interface AppAccess {
  everyone: boolean;
  users: string[];
}

const user = (name: string) => `/api/v1/users/${encodeURIComponent(name)}`;

export const sharesApi = {
  overview: () => api<SharesOverview>('/api/v1/shares'),
  create: (f: FolderInput) => api<SharedFolder>('/api/v1/shares', { method: 'POST', body: f }),
  update: (id: number, f: FolderInput) => api<SharedFolder>(`/api/v1/shares/${id}`, { method: 'PATCH', body: f }),
  remove: (id: number, deleteData: boolean, confirm = '') =>
    api<void>(`/api/v1/shares/${id}`, { method: 'DELETE', body: deleteData ? { delete_data: true, confirm } : {} }),
  setPerms: (id: number, permissions: Record<string, Perm>) =>
    api<SharedFolder>(`/api/v1/shares/${id}/permissions`, { method: 'PUT', body: { permissions } }),
  setAdminsSeeHomes: (on: boolean) => api<void>('/api/v1/shares/settings', { method: 'PUT', body: { admins_see_homes: on } }),

  person: (name: string) => api<PersonSpaces>(`${user(name)}/spaces`),
  setAccess: (name: string, access: Access) => api<void>(user(name), { method: 'PATCH', body: { access } }),
  setFolderPerm: (name: string, id: number, perm: Perm) => api<void>(`${user(name)}/folders/${id}`, { method: 'PUT', body: { perm } }),
  setAppAllowed: (name: string, app: string, allowed: boolean) =>
    api<void>(`${user(name)}/apps/${encodeURIComponent(app)}`, { method: 'PUT', body: { allowed } }),

  appRules: () => api<{ rules: Record<string, AppAccess> }>('/api/v1/appstore/access'),
  setAppAccess: (app: string, a: AppAccess) => api<void>(`/api/v1/appstore/access/${encodeURIComponent(app)}`, { method: 'PUT', body: a }),
};

export const PERM_LABEL: Record<Perm, string> = { rw: 'Read & write', ro: 'Read only', none: 'No access' };
