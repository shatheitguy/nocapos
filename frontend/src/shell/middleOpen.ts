import type { MouseEvent } from 'react';
import { canMultiWindow, openApp } from '../apps/meta';
import { MIDDLE, middleAction } from '../lib/middleClick';

/**
 * Handlers for an app icon (dock, roster, desktop, search): middle-click
 * opens a new window of the app, or brings a single-window app forward.
 * The press itself is swallowed, so it neither pastes (middle-click paste)
 * nor starts the browser's autoscroll. `after` runs once the app is opened
 * (e.g. to close the roster).
 */
export function middleOpen(appId: string, after?: () => void) {
  return {
    onMouseDown: (e: MouseEvent) => {
      if (e.button === MIDDLE) e.preventDefault();
    },
    onAuxClick: (e: MouseEvent) => {
      const act = middleAction(e.button, canMultiWindow(appId));
      if (!act) return;
      e.preventDefault();
      e.stopPropagation();
      openApp(appId, { newWindow: act === 'new' });
      after?.();
    },
  };
}
