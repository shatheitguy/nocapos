// NoCapOS's words, in two voices: "genz" (the default) and "standard"
// (plain, professional copy). Each voice is a folder of JSON files, one per
// area of the system; both folders hold the same files with the same keys
// (scripts/check-i18n.mjs fails the build otherwise).
//
//   const t = useT();            // in components: re-renders on a switch
//   t('dock.launchpad')          // → "The Roster" / "Launchpad"
//   tr('toast.saved', { name })  // outside React (toasts, dialogs)
//
// Placeholders are written {name} and filled from the vars object.
import { useCallback } from 'react';
import { usePrefs, type Lingo } from '../state/prefs';

type Dict = Record<string, string>;

function merge(files: Record<string, Dict>): Dict {
  return Object.assign({}, ...Object.values(files)) as Dict;
}

export const DICTS: Record<Lingo, Dict> = {
  genz: merge(import.meta.glob<Dict>('./genz/*.json', { eager: true, import: 'default' })),
  standard: merge(import.meta.glob<Dict>('./standard/*.json', { eager: true, import: 'default' })),
};

export type Vars = Record<string, string | number | undefined | null>;

/** The text for key in a voice, with {placeholders} filled in. Unknown keys fall back to Standard, then the key. */
export function translate(lingo: Lingo, key: string, vars?: Vars): string {
  const raw = DICTS[lingo]?.[key] ?? DICTS.standard[key] ?? key;
  if (!vars) return raw;
  return raw.replace(/\{(\w+)\}/g, (m, name: string) => {
    const v = vars[name];
    return v == null ? m : String(v);
  });
}

/** The current voice, outside React. */
export const lingo = (): Lingo => usePrefs.getState().lingo;

/** Translate outside React (toasts, confirm dialogs, menus built on demand). */
export const tr = (key: string, vars?: Vars) => translate(lingo(), key, vars);

export type T = (key: string, vars?: Vars) => string;

/** Translate inside a component; it re-renders when the voice changes. */
export function useT(): T {
  const l = usePrefs((s) => s.lingo);
  return useCallback((key: string, vars?: Vars) => translate(l, key, vars), [l]);
}

/** Whether the Gen Z voice is on (for the extra description lines under fun names). */
export const useGenZ = () => usePrefs((s) => s.lingo === 'genz');
