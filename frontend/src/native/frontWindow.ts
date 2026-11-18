// NoCap Desktop: is the screen's front window maximized? The chrome's menu
// bar slides away while it is (the same as in the browser, see useMenubarAuto
// in state/prefs.ts), so a maximized window gets the full height.
//
// The front window is the active one. Clicking the menu bar or one of its
// menus can take the activation away from the window (the chrome surface
// gets the keyboard), so while no window is active the last active one still
// counts, as long as it is open and not minimized.

export interface FrontWindow {
  id: string;
  active: boolean;
  minimized: boolean;
  maximized: boolean;
}

export function frontMaximized(list: readonly FrontWindow[], last: string | null): { maxed: boolean; front: string | null } {
  const active = list.find((w) => w.active);
  const front = active ? active.id : last && list.some((w) => w.id === last) ? last : null;
  const w = front === null ? undefined : list.find((x) => x.id === front);
  return { maxed: !!w && w.maximized && !w.minimized, front: w && !w.minimized ? front : null };
}
