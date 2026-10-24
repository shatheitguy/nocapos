import { getUser, logout } from '../api/client';
import { layoutKeyFor, useWM } from '../state/windows';

/**
 * Log Out: closes every window and forgets this person's saved window
 * layout, then ends the session, so the next sign-in starts with a clean
 * desktop. (Lock Screen keeps the windows and layout; see App.)
 */
export async function logOutClean(): Promise<void> {
  const user = getUser();
  useWM.getState().reset(); // writes any pending save, then stops saving
  if (user) {
    try {
      localStorage.removeItem(layoutKeyFor(user.id));
    } catch {
      /* storage unavailable */
    }
  }
  await logout();
}
