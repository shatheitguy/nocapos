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
}

export interface PeopleList {
  mode: 'system' | 'app';
  users: Person[];
}

const path = (name: string) => `/api/v1/users/${encodeURIComponent(name)}`;

export const usersApi = {
  list: () => api<PeopleList>('/api/v1/users'),
  create: (body: { username: string; full_name?: string; password: string; role: 'admin' | 'user' }) =>
    api<{ username: string }>('/api/v1/users', { method: 'POST', body }),
  setRole: (name: string, role: 'admin' | 'user') => api(path(name), { method: 'PATCH', body: { role } }),
  setDisabled: (name: string, disabled: boolean) => api(path(name), { method: 'PATCH', body: { disabled } }),
  setPassword: (name: string, password: string) => api(path(name), { method: 'PATCH', body: { password } }),
  remove: (name: string, removeHome: boolean) => api(path(name) + (removeHome ? '?remove_home=1' : ''), { method: 'DELETE' }),
};
