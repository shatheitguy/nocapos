// NoCap Shell sign-in screen and admin prompt: small pure helpers.

// ---------- people ----------

/** Up to two initials for an avatar: "Alex Rivera" → "AR", "sam" → "S". */
export function initials(name: string): string {
  const parts = name.trim().split(/[\s._-]+/).filter(Boolean);
  if (!parts.length) return '?';
  const first = [...parts[0]][0] ?? '';
  const last = parts.length > 1 ? ([...parts[parts.length - 1]][0] ?? '') : '';
  return (first + last).toUpperCase();
}

/** A stable hue (0–359) for a name, for letter tiles and avatars. */
export function hueOf(name: string): number {
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0;
  return h % 360;
}
