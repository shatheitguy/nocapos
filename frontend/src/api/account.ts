import { api, getAccessToken } from './client';

export const accountApi = {
  totpStatus: () => api<{ enabled: boolean }>('/api/v1/auth/totp'),
  totpSetup: () => api<{ secret: string; uri: string }>('/api/v1/auth/totp/setup', { method: 'POST' }),
  totpEnable: (code: string) => api('/api/v1/auth/totp/enable', { method: 'POST', body: { code } }),
  totpDisable: (password: string) => api('/api/v1/auth/totp/disable', { method: 'POST', body: { password } }),
  changePassword: (current: string, next: string) =>
    api('/api/v1/auth/password', { method: 'POST', body: { current, new: next } }),
};

/** Lock-screen "forgot password": reset with a TOTP code. Unauthenticated. */
export async function resetPassword(username: string, code: string, password: string): Promise<string | null> {
  const r = await fetch('/api/v1/auth/reset', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    credentials: 'same-origin',
    body: JSON.stringify({ username, code, password }),
  });
  if (r.status === 204) return null;
  let msg = `Reset failed (HTTP ${r.status})`;
  try {
    const j = await r.json();
    msg = j?.error?.message ?? msg;
  } catch {
    /* non-JSON */
  }
  return msg;
}

/** Download the NoCapOS backup via a bearer-authorized blob fetch. */
export async function downloadBackup(): Promise<string | null> {
  const headers: Record<string, string> = {};
  const token = getAccessToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  const r = await fetch('/api/v1/system/backup', { headers, credentials: 'same-origin' });
  if (!r.ok) {
    try {
      const j = await r.json();
      return j?.error?.message ?? `Backup failed (HTTP ${r.status})`;
    } catch {
      return `Backup failed (HTTP ${r.status})`;
    }
  }
  const blob = await r.blob();
  const cd = r.headers.get('Content-Disposition') ?? '';
  const name = /filename="([^"]+)"/.exec(cd)?.[1] ?? 'nocapos-backup.db';
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  document.body.append(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
  return null;
}
