import { tr } from '../../i18n';
import { openApp } from '../meta';
import { displayTitle } from './noteDoc';
import { useNotes } from './notesStore';

/**
 * Opens a note in Sticky Notes, in its editor sheet (Sticky Notes comes to
 * the front, or opens). fresh: just created, so it's thrown away if it's
 * closed while still empty.
 */
export function openNote(id: string, fresh = false) {
  openApp('notes', { props: { note: id, at: String(Date.now()), ...(fresh ? { fresh: '1' } : {}) } });
}

/**
 * Pops a note out into a window of its own, one window per note (opening it
 * again brings that window to the front), to keep it open next to the app.
 */
export function popOutNote(id: string, fresh = false) {
  const n = useNotes.getState().notes.find((x) => x.id === id);
  openApp('note', {
    props: { note: id, ...(fresh ? { fresh: '1' } : {}) },
    key: `note:${id}`,
    newWindow: true,
    title: (n && displayTitle(n, 40)) || tr('notes.untitled'),
  });
}
