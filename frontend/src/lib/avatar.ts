// Profile photos: the signed-in user's photo (shared by the dock, Control
// Center and Settings) and the "last user" this device remembers for the
// macOS-style login screen.
import { create } from 'zustand';
import { getAccessToken } from '../api/client';

export const useAvatar = create<{ url: string | null; set: (url: string | null) => void }>((set) => ({
  url: null,
  set: (url) => set({ url }),
}));

export interface LastUser {
  username: string;
  avatar: string | null; // data: URL, small
}

const LAST = 'alfa.lastUser';

export function lastUser(): LastUser | null {
  try {
    const v = JSON.parse(localStorage.getItem(LAST) ?? 'null');
    return v && typeof v.username === 'string' ? { username: v.username, avatar: typeof v.avatar === 'string' ? v.avatar : null } : null;
  } catch {
    return null;
  }
}

function saveLast(u: LastUser) {
  try {
    localStorage.setItem(LAST, JSON.stringify(u));
  } catch {
    /* storage full or blocked: the login screen just shows the initial */
  }
}

export function forgetLastUser() {
  try {
    localStorage.removeItem(LAST);
  } catch {
    /* ignore */
  }
}

const toDataURL = (b: Blob) =>
  new Promise<string>((res, rej) => {
    const r = new FileReader();
    r.onload = () => res(String(r.result));
    r.onerror = () => rej(r.error);
    r.readAsDataURL(b);
  });

/** After sign-in: fetch the photo and remember this user for the login screen. */
export async function loadAvatar(username: string) {
  let url: string | null = null;
  try {
    const r = await fetch('/api/v1/auth/avatar', { headers: { Authorization: `Bearer ${getAccessToken()}` }, credentials: 'same-origin' });
    if (r.ok) url = await toDataURL(await r.blob());
  } catch {
    /* offline: keep the initial */
  }
  useAvatar.getState().set(url);
  saveLast({ username, avatar: url });
}

/** Center-crops and shrinks a picked image to a 256 px square WebP/JPEG. */
export async function squareImage(file: File, size = 256): Promise<Blob> {
  const bmp = await createImageBitmap(file);
  const side = Math.min(bmp.width, bmp.height);
  const c = document.createElement('canvas');
  c.width = c.height = size;
  const ctx = c.getContext('2d')!;
  ctx.imageSmoothingQuality = 'high';
  ctx.drawImage(bmp, (bmp.width - side) / 2, (bmp.height - side) / 2, side, side, 0, 0, size, size);
  bmp.close();
  const blob = await new Promise<Blob | null>((res) => c.toBlob(res, 'image/webp', 0.88));
  return blob ?? (await new Promise<Blob>((res) => c.toBlob((b) => res(b!), 'image/jpeg', 0.88)));
}

export async function uploadAvatar(file: File, username: string): Promise<string | null> {
  const blob = await squareImage(file);
  const r = await fetch('/api/v1/auth/avatar', {
    method: 'PUT',
    headers: { Authorization: `Bearer ${getAccessToken()}`, 'Content-Type': blob.type },
    credentials: 'same-origin',
    body: blob,
  });
  if (!r.ok) {
    try {
      return (await r.json())?.error?.message ?? `Upload failed (HTTP ${r.status})`;
    } catch {
      return `Upload failed (HTTP ${r.status})`;
    }
  }
  await loadAvatar(username);
  return null;
}

export async function removeAvatar(username: string) {
  await fetch('/api/v1/auth/avatar', { method: 'DELETE', headers: { Authorization: `Bearer ${getAccessToken()}` }, credentials: 'same-origin' });
  useAvatar.getState().set(null);
  saveLast({ username, avatar: null });
}
