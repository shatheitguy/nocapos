import { useEffect, useRef, useState } from 'react';
import { call, on } from './bridge';
import { setVoice } from './i18n';
import type { ShellInfo, Theme } from './protocol';

/** NoCap red: the shell's accent in every theme. */
export const ACCENT = '#e8232b';

/** Dark is the Glass look; light is the classic NoCap look in light mode. */
export function applyTheme(theme: unknown) {
  const root = document.documentElement;
  const light = theme === 'light';
  root.dataset.ui = light ? 'classic' : 'glass';
  root.dataset.theme = light ? 'light' : 'dark';
  root.style.setProperty('--accent', ACCENT);
}

/** shell.info once (the host's smoke test expects every surface to ask), then settings.changed. */
export function useShellInfo(): ShellInfo | null {
  const [info, setInfo] = useState<ShellInfo | null>(null);
  useEffect(() => {
    let live = true;
    call<ShellInfo>('shell.info')
      .then((i) => {
        if (!live) return;
        setVoice(i.voice);
        applyTheme(i.theme);
        setInfo(i);
      })
      .catch((e) => console.warn('nocap: shell.info', e));
    const off = on('settings.changed', (p) => {
      const s = (p ?? {}) as { voice?: string; theme?: Theme };
      if (s.voice) setVoice(s.voice);
      if (s.theme) applyTheme(s.theme);
      setInfo((i) => (i ? { ...i, ...(s.voice ? { voice: s.voice as ShellInfo['voice'] } : {}), ...(s.theme ? { theme: s.theme } : {}) } : i));
    });
    return () => {
      live = false;
      off();
    };
  }, []);
  return info;
}

/** Subscribe to a host event for the component's lifetime (the latest handler is used). */
export function useEvent(name: string, fn: (payload: unknown) => void) {
  const ref = useRef(fn);
  ref.current = fn;
  useEffect(() => on(name, (p) => ref.current(p)), [name]);
}

/** A value read with `method` and kept fresh by `event` (whose payload is the new value). */
export function useHostValue<T>(method: string, event: string | null, initial: T): [T, (v: T) => void, () => void] {
  const [value, setValue] = useState<T>(initial);
  const reload = useRef(() => {});
  reload.current = () => {
    call<T>(method)
      .then(setValue)
      .catch((e) => console.warn('nocap:', e instanceof Error ? e.message : e));
  };
  useEffect(() => {
    reload.current();
    if (!event) return;
    return on(event, (p) => setValue(p as T));
  }, [method, event]);
  return [value, setValue, () => reload.current()];
}

/** The current time, updated on the minute (one timer a minute, aligned to the clock). */
export function useMinuteClock(): Date {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    let timer = 0;
    const arm = () => {
      const d = new Date();
      timer = window.setTimeout(() => {
        setNow(new Date());
        arm();
      }, 60_000 - (d.getSeconds() * 1000 + d.getMilliseconds()) + 50);
    };
    arm();
    // A suspend can skip minutes: catch up when the page is shown again.
    const vis = () => document.visibilityState === 'visible' && setNow(new Date());
    document.addEventListener('visibilitychange', vis);
    return () => {
      window.clearTimeout(timer);
      document.removeEventListener('visibilitychange', vis);
    };
  }, []);
  return now;
}

/** Whether this overlay surface is currently shown (surface.visible), plus a counter that bumps on every show. */
export function useSurfaceShown(name: string): { visible: boolean; shown: number } {
  const [state, setState] = useState({ visible: true, shown: 0 });
  useEvent('surface.visible', (p) => {
    const e = (p ?? {}) as { name?: string; visible?: boolean };
    if (e.name !== name) return;
    setState((s) => ({ visible: !!e.visible, shown: e.visible ? s.shown + 1 : s.shown }));
  });
  return state;
}

export const fmtTime = (d: Date) => d.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
export const fmtDate = (d: Date) => d.toLocaleDateString([], { weekday: 'short', month: 'short', day: 'numeric' });
export const fmtLongDate = (d: Date) => d.toLocaleDateString([], { weekday: 'long', month: 'long', day: 'numeric' });
