/** Whether a page that started on `mine` should load `theirs` (both NoCapOS versions). */
export function shouldReload(mine: string | null, theirs: string | null | undefined): boolean {
  return !!mine && !!theirs && theirs !== mine;
}
