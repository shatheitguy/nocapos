// Apps earlier versions had and this one doesn't: saved docks, folders,
// desktop icons and recents drop them when they load. (No imports: the
// stores read this while their modules load.)
export const RETIRED_APPS: ReadonlySet<string> = new Set(['browser']);

/** Drops retired apps from a saved list of app ids. */
export const withoutRetired = (ids: string[]) => ids.filter((id) => !RETIRED_APPS.has(id));
