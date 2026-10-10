import { api } from './client';
import type { Container } from './types';

export interface StackOp {
  action: string;
  running: boolean;
  output: string;
  error?: string;
  started: string;
  finished?: string;
}

export interface StackSummary {
  name: string;
  /** Its compose file lives in NoCapOS (false = started elsewhere). */
  managed: boolean;
  updated?: string;
  op?: StackOp;
  containers: Container[];
}

export interface ComposeInfo {
  installed: boolean;
  version?: string;
  can_install: boolean;
}

export interface StackDetail {
  name: string;
  compose: string;
  env: string;
  dir: string;
  op?: StackOp | null;
  containers: Container[];
}

export type StackAction = 'up' | 'redeploy' | 'pull' | 'start' | 'stop' | 'restart' | 'down';

const s = (name: string) => `/api/v1/stacks/${encodeURIComponent(name)}`;

export const stacksApi = {
  list: () => api<{ compose: ComposeInfo; stacks: StackSummary[] }>('/api/v1/stacks'),
  get: (name: string) => api<StackDetail>(s(name)),
  create: (name: string, compose: string, env: string, deploy: boolean) =>
    api<{ name: string }>('/api/v1/stacks', { method: 'POST', body: { name, compose, env, deploy } }),
  update: (name: string, compose: string, env: string, deploy: boolean) => api<{ name: string }>(s(name), { method: 'PUT', body: { compose, env, deploy } }),
  run: (name: string, action: StackAction) => api<void>(`${s(name)}/${action}`, { method: 'POST' }),
  logs: (name: string) => api<{ logs: string }>(`${s(name)}/logs?tail=400`),
  remove: (name: string, volumes: boolean) => api<void>(`${s(name)}${volumes ? '?volumes=1' : ''}`, { method: 'DELETE' }),
  installCompose: () => api<ComposeInfo>('/api/v1/docker/compose/install', { method: 'POST' }),
};
