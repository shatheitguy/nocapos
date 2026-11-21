// Middle-clicking an app icon (pressing the scroll wheel) opens another
// window of the app, as on other desktops. Apps that keep one window just
// come to the front.

/** The middle mouse button. */
export const MIDDLE = 1;

/**
 * What a click with `button` on an app icon does: open another window
 * ('new'), bring the app's window forward ('focus'), or nothing extra
 * (null: not the middle button, so the icon's own handlers decide).
 */
export function middleAction(button: number, multiWindow: boolean): 'new' | 'focus' | null {
  if (button !== MIDDLE) return null;
  return multiWindow ? 'new' : 'focus';
}
