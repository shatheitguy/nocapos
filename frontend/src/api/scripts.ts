import { api } from './client';

export interface Script {
  id: string;
  name: string;
  description: string;
  body: string;
  confirm: boolean;
  created_at: string;
  updated_at: string;
  last_run_at?: string;
  last_exit?: number;
}

export interface ScriptHost {
  enabled: boolean;
  shell: string;
  user: string;
  root: boolean;
  os: string;
}

export interface ScriptRun {
  id: string;
  script_id: string;
  name: string;
  started_at: string;
  ended_at?: string;
  running: boolean;
  exit_code?: number;
  output: string;
  truncated: boolean;
  error?: string;
}

export interface ScriptInput {
  name: string;
  description: string;
  body: string;
  confirm: boolean;
}

const path = (id: string) => `/api/v1/scripts/${encodeURIComponent(id)}`;

export const scriptsApi = {
  list: () => api<{ host: ScriptHost; scripts: Script[] }>('/api/v1/scripts'),
  create: (body: ScriptInput) => api<Script>('/api/v1/scripts', { method: 'POST', body }),
  update: (id: string, body: ScriptInput) => api<Script>(path(id), { method: 'PUT', body }),
  remove: (id: string) => api(path(id), { method: 'DELETE' }),
  run: (id: string) => api<{ run_id: string }>(path(id) + '/run', { method: 'POST' }),
  getRun: (run: string) => api<ScriptRun>(`/api/v1/scripts/runs/${encodeURIComponent(run)}`),
  cancel: (run: string) => api(`/api/v1/scripts/runs/${encodeURIComponent(run)}/cancel`, { method: 'POST' }),
};
