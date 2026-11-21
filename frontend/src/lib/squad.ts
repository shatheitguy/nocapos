// Your Squad: what a found NoCap server is to this one (linked, asked,
// asking), where its blip sits on the radar, and how fingerprints and
// time left are shown. Plain functions so they're tested on their own
// (scripts/squad.test.ts).

export interface FoundServer {
  nocap_id: string;
  name: string;
  hostname: string;
  addresses: string[];
  url: string;
  version: string;
  setup: 'new' | 'done' | '';
}

export interface ServerAsk {
  id: string;
  nocap_id: string;
  name: string;
  url: string;
  address: string;
  version: string;
  fingerprint: string;
  state: 'pending' | 'declined';
  by?: string;
  created_at: string;
  expires_at: string;
}

export interface LinkedServer {
  nocap_id: string;
  name: string;
  url: string;
  fingerprint: string;
  version: string;
  online: boolean;
  last_seen?: string;
  linked_by: string;
  created_at: string;
}

export interface SquadSelf {
  nocap_id: string;
  name: string;
  version: string;
  fingerprint: string;
  can_link: boolean;
}

export interface Squad {
  self: SquadSelf;
  linked: LinkedServer[];
  incoming: ServerAsk[];
  outgoing: ServerAsk[];
}

export interface PhoneAsk {
  id: string;
  device_name: string;
  platform: string;
  model?: string;
  os_version?: string;
  username?: string;
  address: string;
  check: string;
  created_at: string;
  expires_at: string;
  can_accept: boolean;
  note?: 'unknown_user' | 'for_user';
}

export type SquadStatus = 'linked' | 'incoming' | 'sent' | 'declined' | 'new' | 'none';

/** What a found server is to this one, most important first. */
export function statusOf(s: FoundServer, squad: Squad | null): SquadStatus {
  if (squad) {
    if (squad.linked.some((l) => l.nocap_id === s.nocap_id)) return 'linked';
    if (squad.incoming.some((a) => a.nocap_id === s.nocap_id)) return 'incoming';
    const out = squad.outgoing.find((a) => a.nocap_id === s.nocap_id);
    if (out) return out.state === 'declined' ? 'declined' : 'sent';
  }
  return s.setup === 'new' ? 'new' : 'none';
}

/** May this server be sent a link request now. */
export const canAsk = (st: SquadStatus) => st === 'none' || st === 'declined';

/**
 * Keeps the order cards already have (so nothing jumps while a search
 * finishes) and adds new servers at the end. `fresh` are the ones that
 * weren't there before: they get the entrance animation.
 */
export function mergeFound(prev: FoundServer[], next: FoundServer[]): { list: FoundServer[]; fresh: string[] } {
  const byId = new Map(next.map((s) => [s.nocap_id, s]));
  const list: FoundServer[] = [];
  for (const p of prev) {
    const n = byId.get(p.nocap_id);
    if (n) {
      list.push(n);
      byId.delete(p.nocap_id);
    }
  }
  const fresh = [...byId.keys()];
  list.push(...byId.values());
  return { list, fresh };
}

/** A stable number from a string (FNV-1a). */
export function hashOf(s: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h >>> 0;
}

/**
 * Where a server's blip sits on the radar, in percent of its box: always
 * the same spot for the same server, between the inner and outer rings.
 */
export function blipSpot(id: string, index: number): { x: number; y: number } {
  const h = hashOf(id);
  const angle = ((h % 360) * Math.PI) / 180;
  const r = 20 + ((h >>> 9) % 18) + (index % 3) * 4; // 20-46% from the centre
  const round = (v: number) => Math.round(v * 10) / 10;
  return { x: round(50 + r * Math.cos(angle)), y: round(50 + r * Math.sin(angle)) };
}

/** A SHA-256 fingerprint for people to compare: "AB:CD:…". */
export function prettyFingerprint(fp: string): string {
  const hex = fp.replace(/[^0-9a-f]/gi, '').toUpperCase();
  return hex.match(/.{1,2}/g)?.join(':') ?? '';
}

/** The first and last few bytes, for a card that's short on room. */
export function shortFingerprint(fp: string): string {
  const p = prettyFingerprint(fp).split(':');
  if (p.length <= 8) return p.join(':');
  return `${p.slice(0, 4).join(':')}…${p.slice(-4).join(':')}`;
}

/** Whole minutes left until iso (at least 1 while it's still ahead, 0 after). */
export function minutesLeft(iso: string, now = Date.now()): number {
  const ms = new Date(iso).getTime() - now;
  if (!(ms > 0)) return 0;
  return Math.max(1, Math.ceil(ms / 60000));
}

/** Only http(s) links leave the page. */
export const safeURL = (u: string) => (/^https?:\/\//i.test(u) ? u : '');
