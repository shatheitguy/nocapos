// NoCap Desktop: the web session of the person signed in on the screen. The
// NoCap Shell host asks alfad for it over a local socket that only its own
// program can use (alfad checks the account and the program); the page gets
// the access token, never the refresh token, and asks the host again before
// it ends.
import { adoptSession, useSessionSource } from '../api/client';
import type { Session, User } from '../api/types';
import { hostCall, setApiOrigin } from './platform';

interface HostSession {
  access_token: string;
  token_type?: string;
  expires_at: string;
  apiOrigin: string;
  account: User;
}

function toSession(h: HostSession): Session {
  setApiOrigin(h.apiOrigin);
  return { access_token: h.access_token, token_type: h.token_type ?? 'Bearer', expires_at: h.expires_at, user: h.account } as Session;
}

async function fromHost(method: 'session.get' | 'session.refresh'): Promise<Session | null> {
  try {
    const h = await hostCall<HostSession>(method);
    return h?.access_token && h.account ? toSession(h) : null;
  } catch {
    return null;
  }
}

/**
 * Signs this surface in as the person on the screen. Retries while alfad is
 * still starting (the desktop can come up before it). Resolves false only
 * after a long wait.
 */
export async function nativeSignIn(maxWaitMs = 120_000): Promise<boolean> {
  useSessionSource(() => fromHost('session.refresh'));
  const start = Date.now();
  let delay = 500;
  for (;;) {
    const s = await fromHost('session.get');
    if (s) {
      adoptSession(s);
      return true;
    }
    if (Date.now() - start > maxWaitMs) return false;
    await new Promise((r) => setTimeout(r, delay));
    delay = Math.min(delay * 2, 5000);
  }
}
