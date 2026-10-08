// HTTP client for alfad. The access token lives only in memory; the refresh
// token is an httpOnly cookie scoped to /api/v1/auth.
import type { Session, User } from './types';

export interface ApiResult<T> {
  ok: boolean;
  status: number;
  data: T;
  error?: string;
}

type Listener = (user: User | null) => void;

let accessToken: string | null = null;
let currentUser: User | null = null;
let refreshTimer: number | undefined;
let refreshing: Promise<boolean> | null = null;
const listeners = new Set<Listener>();

export function onSessionChange(fn: Listener): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

function emit() {
  for (const fn of listeners) fn(currentUser);
}

function setSession(s: Session) {
  accessToken = s.access_token;
  currentUser = s.user;
  window.clearTimeout(refreshTimer);
  const ms = new Date(s.expires_at).getTime() - Date.now() - 60_000;
  refreshTimer = window.setTimeout(() => void refresh(), Math.max(ms, 5_000));
  emit();
}

function clearSession() {
  accessToken = null;
  currentUser = null;
  window.clearTimeout(refreshTimer);
  emit();
}

interface RequestOptions {
  method?: string;
  body?: unknown;
  headers?: Record<string, string>;
  auth?: boolean;
}

async function raw<T>(path: string, opts: RequestOptions = {}): Promise<ApiResult<T>> {
  const headers: Record<string, string> = { ...opts.headers };
  if (opts.body !== undefined) headers['Content-Type'] = 'application/json';
  if (opts.auth !== false && accessToken) headers.Authorization = `Bearer ${accessToken}`;
  let res: Response;
  try {
    res = await fetch(path, {
      method: opts.method ?? 'GET',
      headers,
      credentials: 'same-origin',
      body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
    });
  } catch {
    return { ok: false, status: 0, data: null as T, error: 'NoCapOS is not reachable' };
  }
  let data: unknown = null;
  if (res.status !== 204) {
    try {
      data = await res.json();
    } catch {
      /* empty body */
    }
  }
  const error = res.ok ? undefined : ((data as { error?: { message?: string } })?.error?.message ?? `HTTP ${res.status}`);
  return { ok: res.ok, status: res.status, data: data as T, error };
}

// Single-flight: the server revokes the whole session if a rotated refresh
// token is replayed, so concurrent callers must share one refresh.
export function refresh(): Promise<boolean> {
  refreshing ??= raw<Session>('/api/v1/auth/refresh', {
    method: 'POST',
    auth: false,
    headers: { 'X-Alfa-Request': '1' },
  })
    .then((r) => {
      if (r.ok) {
        setSession(r.data);
        return true;
      }
      clearSession();
      return false;
    })
    .finally(() => {
      refreshing = null;
    });
  return refreshing;
}

export async function api<T>(path: string, opts: RequestOptions = {}): Promise<ApiResult<T>> {
  let r = await raw<T>(path, opts);
  if (r.status === 401 && (await refresh())) r = await raw<T>(path, opts);
  return r;
}

export const getUser = () => currentUser;

/** For requests that can't go through fetch (XHR uploads with progress). */
export const getAccessToken = () => accessToken;

/** "system" = sign in with this machine's Linux users; "app" = NoCapOS accounts. */
export async function accountsMode(): Promise<'system' | 'app'> {
  const r = await raw<{ accounts?: 'system' | 'app' }>('/api/v1/auth/status', { auth: false });
  return r.ok && r.data.accounts === 'system' ? 'system' : 'app';
}

export async function setupRequired(): Promise<boolean> {
  const r = await raw<{ setup_required: boolean }>('/api/v1/auth/status', { auth: false });
  return r.ok && r.data.setup_required;
}

export async function login(username: string, password: string): Promise<string | null> {
  const r = await raw<Session>('/api/v1/auth/login', { method: 'POST', auth: false, body: { username, password } });
  if (!r.ok) return r.error ?? 'Sign-in failed';
  setSession(r.data);
  return null;
}

export async function setup(setupToken: string, username: string, password: string): Promise<string | null> {
  const r = await raw<Session>('/api/v1/auth/setup', {
    method: 'POST',
    auth: false,
    body: { setup_token: setupToken, username, password },
  });
  if (!r.ok) return r.error ?? 'Setup failed';
  setSession(r.data);
  return null;
}

export async function logout(): Promise<void> {
  await raw('/api/v1/auth/logout', { method: 'POST', headers: { 'X-Alfa-Request': '1' } });
  clearSession();
}
