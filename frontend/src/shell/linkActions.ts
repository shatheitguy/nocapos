import { tr } from '../i18n';
import { openApp } from '../apps/meta';
import { toast } from '../state/toasts';
import { linkApi, useLink, type Carry, type Phone } from '../state/link';

// NoCap Link actions shared by the Notification Center, Files and the browser:
// send a file or a web address to a phone, and open what a phone handed off.

const baseName = (p: string) => p.split('/').filter(Boolean).pop() ?? p;

/** An http(s) address the phone can open, or null. */
export function webUrl(s: string): string | null {
  let v = s.trim();
  if (!v) return null;
  if (!/^[a-z][a-z0-9+.-]*:/i.test(v)) v = 'https://' + v;
  try {
    const u = new URL(v);
    return (u.protocol === 'http:' || u.protocol === 'https:') && u.hostname ? u.toString() : null;
  } catch {
    return null;
  }
}

/** Send files to a phone (Downloads/NoCap there); open: the phone opens it once saved. */
export async function sendFilesToPhone(phone: Phone, root: string, paths: string[], open = false): Promise<void> {
  let ok = 0;
  for (const path of paths) {
    const r = await linkApi.action(phone.id, { kind: 'file', root, path, open });
    if (r.ok) ok++;
    else toast('error', tr('link.fileFailed', { name: baseName(path) }), r.status === 404 ? tr('link.fileNotYours') : r.error);
  }
  if (!ok) return;
  if (open) toast('success', tr('link.fileOpening', { device: phone.name }));
  else if (ok === 1) toast('success', tr('link.fileSent', { name: baseName(paths.find(Boolean) ?? ''), device: phone.name }));
  else toast('success', tr('link.filesSent', { n: ok, device: phone.name }));
}

/** "Open on phone" for a web address. */
export async function openUrlOnPhone(phone: Phone, url: string): Promise<boolean> {
  const u = webUrl(url);
  if (!u) {
    toast('error', tr('link.badUrl'));
    return false;
  }
  const r = await linkApi.action(phone.id, { kind: 'open', url: u });
  if (!r.ok) {
    toast('error', tr('link.openFailed', { device: phone.name }), r.error);
    return false;
  }
  toast('success', tr('link.openSent', { device: phone.name }));
  return true;
}

/** Open what a phone handed off: its file in the viewer, or its web address in a new tab. */
export function openCarry(h: Carry) {
  if (h.root && h.path) {
    openApp('viewer', { title: baseName(h.path), props: { root: h.root, path: h.path }, key: `viewer:${h.root}:${h.path}` });
    return;
  }
  const u = h.url ? webUrl(h.url) : null;
  if (u) window.open(u, '_blank', 'noopener');
}

/** What a carry is called: its title, else its address or file name. */
export const carryLabel = (h: Carry) => h.title || h.url || (h.path ? baseName(h.path) : '');

/** This person's phones, outside React (menus built on demand). */
export const linkPhones = () => useLink.getState().phones;
