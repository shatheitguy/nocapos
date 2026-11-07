// Finding NoCapOS again while it restarts (an update, or Restart from the
// power menu). Every probe has a timeout, so a machine that went away
// mid-request can't leave the page waiting forever, and probing backs off
// gently but never gives up.

export type Probe = 'up' | 'down' | 'moved';

const PROBE_TIMEOUT_MS = 6000;

/**
 * Asks the public health check (no session needed). "moved" means the server
 * answered with a redirect, e.g. it now wants HTTPS or another address: only
 * a full page load can follow that.
 */
export async function probeServer(timeoutMs = PROBE_TIMEOUT_MS): Promise<Probe> {
  const ctl = new AbortController();
  const timer = window.setTimeout(() => ctl.abort(), timeoutMs);
  try {
    const res = await fetch(`/healthz?t=${Date.now()}`, { cache: 'no-store', credentials: 'same-origin', redirect: 'manual', signal: ctl.signal });
    if (res.type === 'opaqueredirect' || (res.status >= 300 && res.status < 400)) return 'moved';
    return res.ok ? 'up' : 'down';
  } catch {
    return 'down';
  } finally {
    window.clearTimeout(timer);
  }
}

/** Resolves with fn's result, or with fallback if it takes longer than ms. */
export function withTimeout<T>(p: Promise<T>, ms: number, fallback: T): Promise<T> {
  return new Promise((resolve) => {
    const t = window.setTimeout(() => resolve(fallback), ms);
    void p.then(
      (v) => {
        window.clearTimeout(t);
        resolve(v);
      },
      () => {
        window.clearTimeout(t);
        resolve(fallback);
      },
    );
  });
}

/** The next wait: 1s, 1.5s, 2.25s … up to 8s. */
export const nextDelay = (ms: number) => Math.min(8000, Math.round(ms * 1.5));

export interface WaitOptions {
  /** Only count "up" after the server was seen down (a restart that hasn't begun yet). */
  mustGoDown?: boolean;
  /** Give up waiting to see it go down after this long, and count it as back. */
  downWithinMs?: number;
  /** Called once it's back (or moved). */
  onBack: (how: Probe) => void;
  /** Called with how long it has been away, after each failed probe. */
  onAway?: (ms: number) => void;
  firstDelayMs?: number;
}

/**
 * Polls until NoCapOS answers again. Coming back to the tab or the network
 * coming back probes right away (browsers throttle timers in hidden tabs).
 * Returns a stop function.
 */
export function waitForServer(opts: WaitOptions): () => void {
  const started = Date.now();
  let wentDown = !opts.mustGoDown;
  let downSince: number | null = null;
  let stopped = false;
  let busy = false;
  let delay = 1000;
  let timer = 0;

  const schedule = (ms: number) => {
    window.clearTimeout(timer);
    if (!stopped) timer = window.setTimeout(() => void tick(), ms);
  };
  const tick = async () => {
    if (stopped || busy) return;
    busy = true;
    const p = await probeServer();
    busy = false;
    if (stopped) return;
    if (p === 'down') {
      wentDown = true;
      downSince ??= Date.now();
      opts.onAway?.(Date.now() - downSince);
      delay = nextDelay(delay);
      return schedule(delay);
    }
    const waitedLongEnough = opts.downWithinMs !== undefined && Date.now() - started > opts.downWithinMs;
    if (p === 'moved' || wentDown || waitedLongEnough) {
      stopped = true;
      opts.onBack(p);
      return;
    }
    schedule(1000); // still up: the restart hasn't started yet
  };
  const now = () => {
    if (!document.hidden) {
      delay = 1000;
      schedule(0);
    }
  };
  window.addEventListener('online', now);
  document.addEventListener('visibilitychange', now);
  schedule(opts.firstDelayMs ?? 1500);
  return () => {
    stopped = true;
    window.clearTimeout(timer);
    window.removeEventListener('online', now);
    document.removeEventListener('visibilitychange', now);
  };
}
