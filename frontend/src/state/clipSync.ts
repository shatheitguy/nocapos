import { getAccessToken } from '../api/client';
import { linkApi, onClipArrived, type Clip } from './link';

// Seamless clipboard, NoCap OS's side: copy on the phone, then just paste
// here, and the other way round, with no "send" button.
//  - A clip from another device goes onto this computer's clipboard as soon
//    as the browser lets us write it (now, or the next time this tab gets
//    focus).
//  - Until then, Ctrl/Cmd+V in NoCap OS pastes it (it's newer than anything
//    copied here, which would have cleared it).
//  - Text copied (Ctrl/Cmd+C, Cut) inside NoCap OS goes to the person's
//    other devices.
// Each browser can turn this off (Settings → Linked Devices → Clipboard).

const KEY = 'nocap.clipSync';
/** Text up to 1 MB, as the server takes. */
export const MAX_CLIP_TEXT = 1 << 20;

export function clipSyncOn(): boolean {
  try {
    return localStorage.getItem(KEY) !== '0';
  } catch {
    return true;
  }
}

export function setClipSyncOn(on: boolean) {
  try {
    localStorage.setItem(KEY, on ? '1' : '0');
  } catch {
    /* private window: stays on for this visit */
  }
}

interface Pending {
  clip: Clip;
  /** This tab has had focus since it arrived. */
  seen: boolean;
}

/** The newest clip from another device that isn't on this computer's clipboard yet. */
let pending: Pending | null = null;
/** What we last sent or received, so nothing bounces back. */
let lastText = '';
let pushTimer: number | undefined;

const utf8Len = (s: string) => new TextEncoder().encode(s).length;

/** A clip's picture, read with this session. */
export async function clipPicture(c: Clip): Promise<Blob | null> {
  try {
    const token = getAccessToken();
    const res = await fetch(`/api/v1/clipboard/${c.id}/data`, {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
      credentials: 'same-origin',
    });
    if (!res.ok) return null;
    return await res.blob();
  } catch {
    return null;
  }
}

/** Puts a picture on this computer's clipboard (browsers take PNG only); false when it can't. */
export async function copyPicture(c: Clip): Promise<boolean> {
  if (typeof ClipboardItem === 'undefined' || !navigator.clipboard?.write || c.mime !== 'image/png') return false;
  const blob = await clipPicture(c);
  if (!blob) return false;
  try {
    await navigator.clipboard.write([new ClipboardItem({ 'image/png': blob.type === 'image/png' ? blob : new Blob([blob], { type: 'image/png' }) })]);
    return true;
  } catch {
    return false;
  }
}

/** Tries to put the pending clip on this computer's clipboard (browsers allow it only with focus). */
async function apply() {
  const p = pending;
  if (!p || !document.hasFocus()) return;
  p.seen = true;
  const c = p.clip;
  let ok = false;
  if (c.kind === 'image') ok = await copyPicture(c);
  else {
    try {
      await navigator.clipboard.writeText(c.text);
      ok = true;
    } catch {
      ok = false;
    }
  }
  if (ok && pending === p) {
    lastText = c.text;
    pending = null;
  }
}

function arrived(c: Clip) {
  if (!clipSyncOn()) return;
  if (c.kind !== 'image' && (!c.text || c.text === lastText)) return;
  pending = { clip: c, seen: false };
  lastText = c.text;
  void apply();
}

const editable = (el: Element | null): el is HTMLElement => {
  if (!(el instanceof HTMLElement)) return false;
  if (el.isContentEditable) return true;
  if (el instanceof HTMLTextAreaElement) return !el.readOnly && !el.disabled;
  if (el instanceof HTMLInputElement) return !el.readOnly && !el.disabled && /^(text|search|url|email|tel|)$/.test(el.type);
  return false;
};

/** Pastes text where the cursor is, the way typing would (so undo and the app's own handlers work). */
function insertText(el: HTMLElement, text: string) {
  // execCommand is deprecated but still the only way that keeps undo and input events.
  if (document.execCommand('insertText', false, text)) return;
  if (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement) {
    const start = el.selectionStart ?? el.value.length;
    const end = el.selectionEnd ?? start;
    el.setRangeText(text, start, end, 'end');
    el.dispatchEvent(new Event('input', { bubbles: true }));
  }
}

function onPaste(e: ClipboardEvent) {
  const p = pending;
  if (!p || p.clip.kind === 'image' || !clipSyncOn()) return;
  const here = e.clipboardData?.getData('text/plain') ?? '';
  if (here === p.clip.text) {
    pending = null; // it got here after all
    return;
  }
  const target = document.activeElement;
  if (!editable(target)) return;
  e.preventDefault();
  e.stopPropagation();
  insertText(target, p.clip.text);
}

/** What was copied: the app's own data when it set it, else the selection. */
function copiedText(e: ClipboardEvent): string {
  if (e.defaultPrevented) return e.clipboardData?.getData('text/plain') ?? '';
  const el = document.activeElement;
  if (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement) {
    if (el instanceof HTMLInputElement && el.type === 'password') return '';
    const s = el.selectionStart ?? 0;
    const end = el.selectionEnd ?? 0;
    return end > s ? el.value.slice(s, end) : '';
  }
  return window.getSelection()?.toString() ?? '';
}

function onCopy(e: ClipboardEvent) {
  // Something was copied here: that's now the newest thing on the clipboard.
  pending = null;
  if (!clipSyncOn()) return;
  const text = copiedText(e);
  if (!text.trim() || text === lastText || utf8Len(text) > MAX_CLIP_TEXT) return;
  // Marked sensitive by the page (tokens, passwords): never shared.
  const at = e.target instanceof Element ? e.target : null;
  if (at?.closest('[data-sensitive]')) return;
  lastText = text;
  window.clearTimeout(pushTimer);
  pushTimer = window.setTimeout(() => void linkApi.send(text), 250);
}

function onFocus() {
  void apply();
}

function onBlur() {
  // Left this tab after seeing the clip: something newer may get copied elsewhere.
  if (pending?.seen) pending = null;
}

/** Starts the seamless clipboard for this session; returns a stop function. */
export function watchClipSync(): () => void {
  pending = null;
  lastText = '';
  onClipArrived(arrived);
  window.addEventListener('focus', onFocus);
  window.addEventListener('blur', onBlur);
  document.addEventListener('paste', onPaste, true);
  // Bubble phase: an app's own copy handler has run (and set its data) by then.
  document.addEventListener('copy', onCopy);
  document.addEventListener('cut', onCopy);
  return () => {
    onClipArrived(null);
    window.removeEventListener('focus', onFocus);
    window.removeEventListener('blur', onBlur);
    document.removeEventListener('paste', onPaste, true);
    document.removeEventListener('copy', onCopy);
    document.removeEventListener('cut', onCopy);
    window.clearTimeout(pushTimer);
    pending = null;
  };
}
