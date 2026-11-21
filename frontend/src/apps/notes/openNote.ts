import { tr } from '../../i18n';
import { openApp } from '../meta';
import { displayTitle } from './noteDoc';
import { useNotes } from './notesStore';

/**
 * Opens a note in a window of its own, one window per note (opening it
 * again brings that window to the front). fresh: just created, so it's
 * thrown away if it's closed while still empty.
 */
export function openNote(id: string, fresh = false) {
  const n = useNotes.getState().notes.find((x) => x.id === id);
  openApp('note', {
    props: { note: id, ...(fresh ? { fresh: '1' } : {}) },
    key: `note:${id}`,
    newWindow: true,
    title: (n && displayTitle(n, 40)) || tr('notes.untitled'),
  });
}
