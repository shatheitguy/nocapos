// Checks for the setup wizard's machine steps. They mirror the server's
// rules so mistakes show while typing; the server checks everything again.
// No imports: `npm test` runs these straight from Node.

const LABEL = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;

/** An RFC 1123 host name: '' when fine, else the translation key of the problem. */
export function hostnameProblem(name: string): string {
  const n = name.trim().toLowerCase();
  if (!n || n.length > 253) return 'wiz.hostLen';
  if (!n.split('.').every((l) => LABEL.test(l))) return 'wiz.hostChars';
  if (/^[0-9.]+$/.test(n)) return 'wiz.hostDigits';
  return '';
}

const V4 = /^(25[0-5]|2[0-4]\d|1?\d?\d)(\.(25[0-5]|2[0-4]\d|1?\d?\d)){3}$/;
export const isV4 = (s: string) => V4.test(s.trim());

export function isV6(s: string): boolean {
  const v = s.trim();
  if (!v.includes(':') || !/^[0-9a-f:]+$/i.test(v)) return false;
  const halves = v.split('::');
  if (halves.length > 2) return false;
  const groups = (part: string) => (part ? part.split(':') : []);
  const all = halves.flatMap(groups);
  if (!all.every((g) => /^[0-9a-f]{1,4}$/i.test(g))) return false;
  return halves.length === 2 ? all.length < 8 : all.length === 8;
}

const cidr = (s: string, ok: (a: string) => boolean, max: number) => {
  const [a, b, extra] = s.trim().split('/');
  return extra === undefined && ok(a ?? '') && /^\d{1,3}$/.test(b ?? '') && +b >= 1 && +b <= max;
};
export const isV4Cidr = (s: string) => cidr(s, isV4, 32);
export const isV6Cidr = (s: string) => cidr(s, isV6, 128);

export const splitDNS = (s: string) => s.split(/[\s,]+/).map((x) => x.trim()).filter(Boolean);

export interface NetCheck {
  v4: { method: 'auto' | 'manual'; address?: string; gateway?: string; dns: string[] };
  v6mode: 'keep' | 'auto' | 'manual' | 'disabled';
  v6: { address?: string; gateway?: string; dns: string[] };
}

/** '' when the network settings are fine, else the translation key of the problem. */
export function networkProblem(n: NetCheck): string {
  if (n.v4.method === 'manual') {
    if (!isV4Cidr(n.v4.address ?? '')) return 'wiz.netBadV4';
    if (n.v4.gateway && !isV4(n.v4.gateway)) return 'wiz.netBadGw4';
  }
  if (!n.v4.dns.every(isV4)) return 'wiz.netBadDns4';
  if (n.v6mode === 'manual') {
    if (!isV6Cidr(n.v6.address ?? '')) return 'wiz.netBadV6';
    if (n.v6.gateway && !isV6(n.v6.gateway)) return 'wiz.netBadGw6';
  }
  if (n.v6mode !== 'keep' && !n.v6.dns.every(isV6)) return 'wiz.netBadDns6';
  return '';
}

/**
 * Where the browser finds the server after a name or address change, or null
 * when the current address keeps working: <name>.local follows a rename, an
 * address typed in follows a static address.
 */
export function nextOrigin(
  change: { hostname: string; net: NetCheck | null },
  oldHost: string,
  here: { hostname: string; port: string; protocol: string },
): string | null {
  const host = here.hostname.toLowerCase();
  // On the server's own screen the loopback address never changes.
  if (host === 'localhost' || host === '[::1]' || /^127\./.test(host)) return null;
  const port = here.port ? `:${here.port}` : '';
  const newHost = change.hostname.trim().toLowerCase();
  if (newHost && oldHost && newHost !== oldHost.toLowerCase() && host === `${oldHost.toLowerCase()}.local`) {
    return `${here.protocol}//${newHost.split('.')[0]}.local${port}`;
  }
  const n = change.net;
  if (n && n.v4.method === 'manual' && isV4(host)) {
    const ip = (n.v4.address ?? '').split('/')[0];
    if (ip && ip !== host) return `${here.protocol}//${ip}${port}`;
  }
  if (n && n.v6mode === 'manual' && host.startsWith('[')) {
    const ip = (n.v6.address ?? '').split('/')[0].toLowerCase();
    if (ip && `[${ip}]` !== host) return `${here.protocol}//[${ip}]${port}`;
  }
  return null;
}
