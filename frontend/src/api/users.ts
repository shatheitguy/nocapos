import { api } from './client';

export interface Person {
  username: string;
  full_name?: string;
  role: 'admin' | 'user';
  disabled: boolean;
  root?: boolean;
  uid: number; // -1 for NoCapOS accounts
  home?: string;
  shell?: string;
  totp: boolean;
  you: boolean;
  /** desktop: signs in to NoCapOS; files: file sharing only (SMB / WebDAV). */
  access: 'desktop' | 'files';
  /** Has NoCapOS open right now. */
  online?: boolean;
  /** Last active anywhere (ISO time), if ever. */
  last_seen?: string;
  /** Signed-in sessions and linked devices. */
  sessions?: number;
  /** Has a profile photo (GET /users/{name}/avatar). */
  avatar?: boolean;
  /** Names of the groups they're in. */
  groups?: string[];
  created_at?: string;
}

export interface PeopleGroup {
  id: number;
  name: string;
  description: string;
  /** #rrggbb, or '' for the accent color. */
  color: string;
  members: string[];
  created_at: string;
}

export interface GroupBody {
  name: string;
  description?: string;
  color?: string;
  members: string[];
}

export interface PeopleList {
  mode: 'system' | 'app';
  users: Person[];
}

const path = (name: string) => `/api/v1/users/${encodeURIComponent(name)}`;

export const usersApi = {
  list: () => api<PeopleList>('/api/v1/users'),
  create: (body: { username: string; full_name?: string; password: string; role: 'admin' | 'user'; access?: 'desktop' | 'files' }) =>
    api<{ username: string }>('/api/v1/users', { method: 'POST', body }),
  setRole: (name: string, role: 'admin' | 'user') => api(path(name), { method: 'PATCH', body: { role } }),
  setDisabled: (name: string, disabled: boolean) => api(path(name), { method: 'PATCH', body: { disabled } }),
  setPassword: (name: string, password: string) => api(path(name), { method: 'PATCH', body: { password } }),
  remove: (name: string, removeHome: boolean) => api(path(name) + (removeHome ? '?remove_home=1' : ''), { method: 'DELETE' }),
  setAccess: (name: string, access: 'desktop' | 'files') => api(path(name), { method: 'PATCH', body: { access } }),
  avatarPath: (name: string) => path(name) + '/avatar',
};

const groupPath = (id: number) => `/api/v1/groups/${id}`;

export const groupsApi = {
  list: () => api<{ groups: PeopleGroup[] }>('/api/v1/groups'),
  create: (body: GroupBody) => api<PeopleGroup>('/api/v1/groups', { method: 'POST', body }),
  update: (id: number, body: GroupBody) => api<PeopleGroup>(groupPath(id), { method: 'PUT', body }),
  remove: (id: number) => api(groupPath(id), { method: 'DELETE' }),
};
