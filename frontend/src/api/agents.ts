import { api } from './client';

// NoCap MCP: AI agents' profiles (scopes, where they connect from, expiry,
// pause), their tools, activity and approvals. The server enforces all of
// it; this only edits it.

export type AgentScope = 'read' | 'act' | 'custom';
export type AgentKind = 'local' | 'remote' | 'assistant';
export type Decision = 'once' | 'always' | 'deny';
export type Risk = 'none' | 'low' | 'medium' | 'high';

export interface CapabilityLevel {
  id: string;
  risk: Risk;
}

export interface Capability {
  id: string;
  group: 'data' | 'system' | 'apps' | 'danger';
  /** Lowest ("off") first. */
  levels: CapabilityLevel[];
  /** Its higher levels change things, so they ask first unless relaxed. */
  acts: boolean;
  /** Always asks, whatever the profile says. */
  never_auto?: boolean;
}

/** One agent's profile. Capabilities not in levels are off. */
export interface Scopes {
  levels: Record<string, string>;
  /** "location:/path"; empty = every location the owner can open. */
  folders: string[];
  /** Capabilities whose actions run without asking. */
  auto: string[];
}

export interface Preset {
  id: string;
  scopes: Scopes;
}

export interface AgentTool {
  name: string;
  title: string;
  description: string;
  /** Changes something: asks first unless relaxed. */
  action: boolean;
  admin: boolean;
  capability: string;
  /** The least level of capability it needs. */
  level: string;
}

export interface AgentToken {
  id: string;
  name: string;
  kind: AgentKind;
  scope: AgentScope;
  scopes: Scopes;
  risk: Risk;
  /** null = every tool its scopes allow. */
  tools: string[] | null;
  always_allow: string[];
  ip_allow: string[];
  expires_at: string | null;
  expired: boolean;
  paused: boolean;
  /** Waiting for its pairing code to be used. */
  pairing: boolean;
  pair_expires_at: string | null;
  created_at: string;
  last_used_at: string | null;
}

export interface AgentActivity {
  id: number;
  token_id: string;
  agent: string;
  tool: string;
  summary: string;
  outcome: 'ok' | 'error' | 'denied' | 'timeout' | 'stopped';
  detail?: string;
  created_at: string;
  capability?: string;
  effect?: 'read' | 'change' | 'run';
}

export interface Approval {
  id: string;
  token_id: string;
  agent: string;
  tool: string;
  tool_title: string;
  capability?: string;
  risk?: Risk;
  summary: string;
  arguments: Record<string, unknown>;
  created_at: string;
  expires_at: string;
}

export interface AgentsOverview {
  enabled: boolean;
  loopback_only: boolean;
  can_manage: boolean;
  local_url: string;
  network_url: string;
  sse_url: string;
  pair_url: string;
  capabilities: Capability[];
  presets: Preset[];
  assistant: AgentToken;
  tools: AgentTool[];
  tokens: AgentToken[];
  activity: AgentActivity[];
  pending: Approval[];
}

export interface CreateAgent {
  name: string;
  kind: 'local' | 'remote';
  preset?: string;
  scopes?: Scopes;
  ip_allow?: string[];
  /** 0 = never. */
  expires_in_days?: number;
  /** Hand out a one-time pairing code instead of the token. */
  pairing?: boolean;
}

export interface UpdateAgent {
  name?: string;
  preset?: string;
  scopes?: Scopes;
  ip_allow?: string[];
  /** 0 = never, n = n days from now. */
  expires_in_days?: number;
  paused?: boolean;
  tools?: string[];
  all_tools?: boolean;
  always_allow?: string[];
}

export interface Secret {
  token?: string;
  code?: string;
  agent: AgentToken;
}

const tokenPath = (id: string) => `/api/v1/agents/tokens/${encodeURIComponent(id)}`;

export const agentsApi = {
  overview: () => api<AgentsOverview>('/api/v1/agents'),
  settings: (body: { enabled?: boolean; loopback_only?: boolean }) =>
    api<{ enabled: boolean; loopback_only: boolean }>('/api/v1/agents/settings', { method: 'PUT', body }),
  create: (body: CreateAgent) => api<Secret>('/api/v1/agents/tokens', { method: 'POST', body }),
  update: (id: string, body: UpdateAgent) => api<AgentToken>(tokenPath(id), { method: 'PATCH', body }),
  rotate: (id: string, pairing: boolean) => api<Secret>(`${tokenPath(id)}/rotate`, { method: 'POST', body: { pairing } }),
  revoke: (id: string) => api<void>(tokenPath(id), { method: 'DELETE' }),
  activity: (agent?: string, before?: number) => {
    const q = new URLSearchParams();
    if (agent) q.set('agent', agent);
    if (before) q.set('before', String(before));
    return api<AgentActivity[]>(`/api/v1/agents/activity?${q.toString()}`);
  },
  stopAll: () =>
    api<{ paused: number; denied: number; stopped: number; everyone: boolean; mcp_off: boolean }>('/api/v1/agents/stop', {
      method: 'POST',
    }),
  approvals: () => api<Approval[]>('/api/v1/agents/approvals'),
  decide: (id: string, decision: Decision) =>
    api<void>(`/api/v1/agents/approvals/${encodeURIComponent(id)}`, { method: 'POST', body: { decision } }),
};

/** A level's place in its capability (0 = off, -1 = unknown). */
export function levelRank(c: Capability, level: string | undefined): number {
  return c.levels.findIndex((l) => l.id === (level || 'off'));
}

/** Whether a profile reaches a tool's capability level (as the server checks it). */
export function scopesAllow(caps: Capability[], s: Scopes, capability: string, need: string): boolean {
  const c = caps.find((x) => x.id === capability);
  if (!c) return false;
  const want = levelRank(c, need);
  return want > 0 && levelRank(c, s.levels[capability]) >= want;
}
