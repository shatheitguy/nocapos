// The page side of the NoCap Shell bridge (shell/PROTOCOL.md).
//
//   page → host: window.webkit.messageHandlers.nocap.postMessage('{"id":1,"method":"…","params":{…}}')
//   host → page: window.__nocap.reply(id, ok, data) and window.__nocap.event(name, payload)
//
// `createBridge` is plain logic (no DOM) so node can test it. `call` / `on`
// use the page's one bridge: the host's WebKit message handler. Without the
// host (a normal browser) every call is refused.

export type Listener = (payload: unknown) => void;

export class BridgeError extends Error {
  readonly method: string;
  constructor(method: string, message: string) {
    super(`${method}: ${message}`);
    this.name = 'BridgeError';
    this.method = method;
  }
}

export interface BridgeOptions {
  /** How long a call waits for its reply (ms). */
  timeoutMs?: number;
  setTimer?: (fn: () => void, ms: number) => unknown;
  clearTimer?: (handle: unknown) => void;
}

export interface Bridge {
  call<T = unknown>(method: string, params?: Record<string, unknown>): Promise<T>;
  /** Subscribe to a host event; returns the unsubscribe function. */
  on(name: string, fn: Listener): () => void;
  /** Called by the host with the result of call `id`. */
  reply(id: number, ok: boolean, data: unknown): void;
  /** Called by the host for an event. */
  event(name: string, payload: unknown): void;
  /** Calls still waiting for a reply. */
  pending(): number;
}

export const CALL_TIMEOUT_MS = 10_000;

interface Waiting {
  method: string;
  resolve: (v: unknown) => void;
  reject: (e: Error) => void;
  timer: unknown;
}

function errorText(data: unknown): string {
  if (data && typeof data === 'object' && 'error' in data) {
    const e = (data as { error: unknown }).error;
    if (typeof e === 'string' && e) return e;
  }
  if (typeof data === 'string' && data) return data;
  return 'refused';
}

export function createBridge(post: (json: string) => void, opts: BridgeOptions = {}): Bridge {
  const timeoutMs = opts.timeoutMs ?? CALL_TIMEOUT_MS;
  const setTimer = opts.setTimer ?? ((fn, ms) => setTimeout(fn, ms));
  const clearTimer = opts.clearTimer ?? ((h) => clearTimeout(h as ReturnType<typeof setTimeout>));
  const waiting = new Map<number, Waiting>();
  const listeners = new Map<string, Set<Listener>>();
  let next = 1;

  return {
    call<T>(method: string, params?: Record<string, unknown>): Promise<T> {
      const id = next++;
      return new Promise<T>((resolve, reject) => {
        const timer = setTimer(() => {
          if (!waiting.delete(id)) return;
          reject(new BridgeError(method, 'timed out'));
        }, timeoutMs);
        waiting.set(id, { method, resolve: resolve as (v: unknown) => void, reject, timer });
        try {
          post(JSON.stringify(params === undefined ? { id, method } : { id, method, params }));
        } catch (e) {
          waiting.delete(id);
          clearTimer(timer);
          reject(new BridgeError(method, e instanceof Error ? e.message : 'could not send'));
        }
      });
    },
    on(name, fn) {
      let set = listeners.get(name);
      if (!set) listeners.set(name, (set = new Set()));
      set.add(fn);
      return () => {
        set.delete(fn);
      };
    },
    reply(id, ok, data) {
      const w = waiting.get(id);
      if (!w) return; // late (timed out) or unknown: ignore
      waiting.delete(id);
      clearTimer(w.timer);
      if (ok) w.resolve(data);
      else w.reject(new BridgeError(w.method, errorText(data)));
    },
    event(name, payload) {
      const set = listeners.get(name);
      if (!set) return;
      for (const fn of [...set]) {
        try {
          fn(payload);
        } catch (e) {
          console.error(`nocap: ${name} listener failed`, e);
        }
      }
    },
    pending: () => waiting.size,
  };
}

// ---------- the page's bridge ----------

interface NocapHandler {
  postMessage(json: string): void;
}
interface ShellWindow {
  webkit?: { messageHandlers?: { nocap?: NocapHandler } };
  __nocap?: { reply: Bridge['reply']; event: Bridge['event'] };
}

let page: Bridge | null = null;

/** Sets up window.__nocap (once). Call it first thing on the page. */
export function startBridge(): Bridge {
  if (page) return page;
  const w = window as unknown as ShellWindow;
  const host = w.webkit?.messageHandlers?.nocap;
  page = createBridge((json) => {
    if (!host) throw new Error('no NoCap Shell host');
    host.postMessage(json);
  });
  const b = page;
  w.__nocap = { reply: (id, ok, data) => b.reply(id, ok, data), event: (name, payload) => b.event(name, payload) };
  return page;
}

export function call<T = unknown>(method: string, params?: Record<string, unknown>): Promise<T> {
  return startBridge().call<T>(method, params);
}

export function on(name: string, fn: Listener): () => void {
  return startBridge().on(name, fn);
}

/** Fire and forget: log a refusal instead of throwing. */
export function send(method: string, params?: Record<string, unknown>): void {
  call(method, params).catch((e) => console.warn('nocap:', e instanceof Error ? e.message : e));
}
