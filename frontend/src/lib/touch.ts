// Touch screens: focusing a text field on its own pops the on-screen
// keyboard over half the screen. Search fields there focus only when tapped.

/** True on touch-first devices (phones, tablets): a coarse primary pointer. */
export function isCoarsePointer(): boolean {
  try {
    return window.matchMedia('(pointer: coarse)').matches;
  } catch {
    return false;
  }
}

/** Focuses el on mouse-and-keyboard devices; on touch it waits for a tap. */
export function focusUnlessTouch(el: HTMLElement | null | undefined) {
  if (el && !isCoarsePointer()) el.focus();
}
