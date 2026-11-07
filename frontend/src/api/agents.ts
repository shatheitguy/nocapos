import { api } from './client';

// NoCap MCP: AI agents' access tokens, their tools, activity and approvals.

export type AgentScope = 'read' | 'act';
export type Decision = 'once' | 'always' | 'deny';

export interface AgentTool {
  name: string;
  title: string;
  description: string;
  /** Changes something: the owner approves each call. */
  action: boolean;
  admin: boolean;
}

export interface AgentToken {
  id: string;
  name: string;
  scope: AgentScope;
  /** null = every tool its scope allows. */
  tools: string[] | null;
  always_allow: string[];
  created_at: string;
  last_used_at: string | null;
}

export interface AgentActivity {
  id: number;
  token_id: string;
  agent: string;
  tool: string;
  summary: string;
  outcome: 'ok' | 'error' | 'denied' | 'timeout';
  detail?: string;
  created_at: string;
}

export interface Approval {
  id: string;
  token_id: string;
  agent: string;
  tool: string;
  tool_title: string;
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
  tools: AgentTool[];
  tokens: AgentToken[];
  activity: AgentActivity[];
  pending: Approval[];
}

export const agentsApi = {
  overview: () => api<AgentsOverview>('/api/v1/agents'),
  settings: (body: { enabled?: boolean; loopback_only?: boolean }) =>
    api<{ enabled: boolean; loopback_only: boolean }>('/api/v1/agents/settings', { method: 'PUT', body }),
  create: (name: string, scope: AgentScope) =>
    api<{ token: string; agent: AgentToken }>('/api/v1/agents/tokens', { method: 'POST', body: { name, scope } }),
  setTools: (id: string, body: { tools?: string[]; all_tools?: boolean; always_allow?: string[] }) =>
    api<AgentToken>(`/api/v1/agents/tokens/${encodeURIComponent(id)}`, { method: 'PATCH', body }),
  revoke: (id: string) => api<void>(`/api/v1/agents/tokens/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  approvals: () => api<Approval[]>('/api/v1/agents/approvals'),
  decide: (id: string, decision: Decision) =>
    api<void>(`/api/v1/agents/approvals/${encodeURIComponent(id)}`, { method: 'POST', body: { decision } }),
};
