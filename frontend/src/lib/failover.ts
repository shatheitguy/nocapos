// Failover pairs: two linked NoCap servers kept in sync. The shapes the
// server sends (Settings → Your Squad → Failover) and plain functions for
// how they're shown, tested on their own (scripts/failover.test.ts).

export type FailoverState = 'asked' | 'asking' | 'starting' | 'active';

export interface FailoverCategory {
  id: string;
  /** this server's changes the other one hasn't read yet */
  queued: number;
  last_change?: string;
}

export interface FailoverPair {
  nocap_id: string;
  name: string;
  url: string;
  online: boolean;
  state: FailoverState;
  /** whose data the first sync keeps */
  primary: 'self' | 'peer';
  paused: boolean;
  asked_by?: string;
  created_at: string;
  active_at?: string;
  last_pull?: string;
  last_ok?: string;
  last_error?: string;
  failures: number;
  queued: number;
  conflicts: number;
  categories: FailoverCategory[];
  files?: FailoverFiles;
  takeover?: Takeover;
}

export interface Takeover {
  role: 'primary' | 'standby';
  /** this server runs the apps now */
  running: boolean;
  peer_running: boolean;
  peer_up: boolean;
  last_contact?: string;
  auto: boolean;
  after_min: number;
  vip: string;
  vip_iface: string;
  vip_held: boolean;
  can_hand_back: boolean;
  busy: boolean;
}

export type TakeoverPhase = 'normal' | 'took_over' | 'stood_back' | 'primary_down' | 'standby_down';

/** Where the pair stands on running the apps, from this server's side. */
export function takeoverPhase(t: Takeover): TakeoverPhase {
  if (t.role === 'standby') {
    if (t.running) return 'took_over';
    return t.peer_up ? 'normal' : 'primary_down';
  }
  if (!t.running) return 'stood_back';
  return t.peer_up ? 'normal' : 'standby_down';
}

export interface FileLocation {
  id: string;
  name: string;
  kind: 'drive' | 'homes' | 'appdata' | 'config';
  on: boolean;
  /** syncs with the settings, can't be turned off */
  always?: boolean;
  /** bytes; -1 while it's added up */
  size: number;
  files: number;
  queued: number;
  queued_bytes: number;
  scanned?: string;
  synced?: string;
  error?: string;
  received: number;
}

export interface FailoverFiles {
  locations: FileLocation[];
  limit_kbps: number;
  window_from: string;
  window_to: string;
  in_window: boolean;
}

export interface FilesChoice {
  files: string[];
  limit_kbps: number;
  window_from: string;
  window_to: string;
}

/** What an administrator's file choices would send (only switched-on, choosable locations). */
export function filesChoice(f: FailoverFiles, on: Record<string, boolean>, limit: string, from: string, to: string): FilesChoice {
  const kbps = Math.max(0, Math.round(Number(limit) || 0));
  return {
    files: f.locations.filter((l) => !l.always && (on[l.id] ?? l.on)).map((l) => l.id),
    limit_kbps: kbps,
    window_from: from && to ? from : '',
    window_to: from && to ? to : '',
  };
}

export interface FailoverInfo {
  pair: FailoverPair | null;
  categories: string[];
  /** the settings database's size: what the other server needs at most for it */
  db_bytes: number;
}

export interface SyncConflict {
  id: number;
  table: string;
  category: string;
  key: string;
  /** null: deleted on this server */
  mine: Record<string, string> | null;
  /** null: deleted on the other one */
  theirs: Record<string, string> | null;
  winner: 'mine' | 'theirs';
  note?: string;
  created_at: string;
}

export type SyncHealth = 'asked' | 'asking' | 'starting' | 'paused' | 'failing' | 'behind' | 'ok';

/** Failed syncs in a row before the pair counts as failing (as the server says). */
export const FAIL_AFTER = 3;

/** How the pair is doing, worst first. "behind": no good sync for a minute. */
export function syncHealth(p: FailoverPair, now = Date.now()): SyncHealth {
  if (p.state !== 'active') return p.state;
  if (p.paused) return 'paused';
  if (p.failures >= FAIL_AFTER) return 'failing';
  const ok = p.last_ok ? new Date(p.last_ok).getTime() : 0;
  if (!ok || now - ok > 60_000) return 'behind';
  return 'ok';
}

/** The fields two versions of a record differ in, in a stable order. */
export function changedFields(c: SyncConflict): string[] {
  const keys = new Set([...Object.keys(c.mine ?? {}), ...Object.keys(c.theirs ?? {})]);
  return [...keys].filter((k) => (c.mine ?? {})[k] !== (c.theirs ?? {})[k]).sort();
}
