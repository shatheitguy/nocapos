// Which wallpaper to show, kept free of imports so it can be unit-tested on
// its own (scripts/wallpapers.test.ts).

/** Wallpapers people add themselves: the original single slot, and newer ones. */
export const isCustomWallpaper = (id: string) => id === 'custom' || id.startsWith('custom-');

/**
 * The wallpaper to actually use: the chosen one when it still exists (a
 * built-in, or a picture of your own that's still stored), otherwise the
 * first built-in. Old built-ins that were retired land here too.
 */
export function pickWallpaper(chosen: string | undefined, builtIns: readonly string[], customs: readonly string[]): string {
  if (chosen && builtIns.includes(chosen)) return chosen;
  if (chosen && isCustomWallpaper(chosen) && customs.includes(chosen)) return chosen;
  return builtIns[0];
}
