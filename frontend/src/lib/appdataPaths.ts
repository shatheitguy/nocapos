// App data folder paths (pure; tested in scripts/appdata.test.ts).
// Every app keeps its files in <root>/<app>/<folder>, with names made the
// same way the server makes them.

/** A folder name: lowercase letters, numbers, . _ and - (never empty, "." or ".."). */
export function safeName(s: string): string {
  const out = s
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9._]+/g, '-')
    .replace(/^[-.]+|[-.]+$/g, '')
    .slice(0, 64)
    .replace(/[-.]+$/g, '');
  return out || 'app';
}

/** The folder offered for new storage: <root>/<container or project>/<last part of the target>. */
export function suggestPath(root: string, owner: string, target: string): string {
  const base = target.split('/').filter(Boolean).pop() ?? '';
  return `${root.replace(/\/+$/, '')}/${safeName(owner)}/${safeName(base || 'data')}`;
}

/** Whether a path is one suggestPath would have made for this owner (so it can follow renames). */
export function isSuggested(path: string, root: string, owner: string): boolean {
  const prefix = `${root.replace(/\/+$/, '')}/${safeName(owner)}/`;
  return path === '' || (path.startsWith(prefix) && !path.slice(prefix.length).includes('/'));
}

/** Human size: 1.2 GB ("…" while unknown). */
export function sizeText(n: number): string {
  if (n < 0) return '…';
  const u = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 10 || i === 0 ? Math.round(v) : v.toFixed(1)} ${u[i]}`;
}

/** Job progress as a bar width (0–100). */
export const pct = (n: number) => Math.max(0, Math.min(100, Math.round(n)));
