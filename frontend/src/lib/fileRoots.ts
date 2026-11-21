// Which storage locations a Files pane may stay in.
//
// The locations list (/api/v1/files/roots) isn't everything a person can
// open: App data locations come and go with the apps, and an administrator's
// own home folder ("homedir-<name>") isn't listed because their Home is the
// main drive. Their Received folder lives in that home, so a pane sent
// there must stay there instead of falling back to the first location.

/** An App data location. */
export const isAppData = (root: string) => root === 'appdata' || root.startsWith('app:');

/** A person's own home folder location. */
export const isHomeDir = (root: string) => root.startsWith('homedir-');

/** Whether a pane may open root even though the locations list doesn't have it. */
export const opensUnlisted = (root: string) => isAppData(root) || isHomeDir(root);

/**
 * The location a pane should show: root itself when it's listed or may be
 * opened unlisted, else the first listed one (or root while the list is
 * still loading or empty).
 */
export function paneRoot(roots: readonly { id: string }[] | null | undefined, root: string): string {
  if (!roots?.length || !root || roots.some((r) => r.id === root) || opensUnlisted(root)) return root;
  return roots[0].id;
}
