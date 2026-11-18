// The shell's words in NoCapOS's two voices. Only the shell's own files are
// bundled (the web OS's i18n module reads its prefs store, which talks to the
// web API, so it can't come along). The voice comes from shell.info and
// settings.changed.
import { useCallback, useSyncExternalStore } from 'react';
import type { Voice } from './protocol';

type Dict = Record<string, string>;
const FILES = import.meta.glob<Dict>(['../i18n/*/shell.json', '../i18n/*/deskshell.json'], { eager: true, import: 'default' });
const dictFor = (v: Voice): Dict =>
  Object.assign({}, ...Object.entries(FILES).filter(([path]) => path.includes(`/${v}/`)).map(([, d]) => d)) as Dict;
const DICTS: Record<Voice, Dict> = { genz: dictFor('genz'), standard: dictFor('standard') };

let voice: Voice = 'genz';
const subs = new Set<() => void>();

export function setVoice(v: unknown) {
  const next: Voice = v === 'standard' ? 'standard' : 'genz';
  if (next === voice) return;
  voice = next;
  subs.forEach((fn) => fn());
}

export type Vars = Record<string, string | number | undefined | null>;

export function translate(v: Voice, key: string, vars?: Vars): string {
  const raw = DICTS[v][key] ?? DICTS.standard[key] ?? key;
  if (!vars) return raw;
  return raw.replace(/\{(\w+)\}/g, (m, name: string) => (vars[name] == null ? m : String(vars[name])));
}

/** Outside React. */
export const tr = (key: string, vars?: Vars) => translate(voice, key, vars);

export type T = (key: string, vars?: Vars) => string;

export function useT(): T {
  const v = useSyncExternalStore(
    (fn) => {
      subs.add(fn);
      return () => subs.delete(fn);
    },
    () => voice,
  );
  return useCallback((key: string, vars?: Vars) => translate(v, key, vars), [v]);
}
