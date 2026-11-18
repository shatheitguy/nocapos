import { useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent as RKeyboardEvent, type MouseEvent as RMouseEvent } from 'react';
import { Icon } from '../../components/Icon';
import { useT } from '../../i18n';
import { sameBlocks, safeHref, type Block, type BlockType, type Note } from './noteDoc';
import {
  applyShortcut,
  caretAtLineStart,
  caretBlock,
  linkAt,
  pastePlain,
  readBlocks,
  renderBlocks,
  selectedBlocks,
  setLineType,
  tidyLines,
} from './noteDom';
import { flushNote, useNotes } from './notesStore';

const isMac = typeof navigator !== 'undefined' && /mac|iphone|ipad/i.test(navigator.platform || navigator.userAgent);
/** "Ctrl" or "⌘", for tooltips. */
export const MOD = isMac ? '⌘' : 'Ctrl';

interface Props {
  note: Note;
  /** Show the formatting bar (B, I, link, lists). */
  toolbar?: boolean;
  /** Smaller type and padding (desktop stickies). */
  compact?: boolean;
  autoFocus?: boolean;
  placeholder?: string;
  className?: string;
}

/**
 * A note's body, editable in place: bold, italic, links, bullets and
 * checklists. Changes save on their own (useNotes autosave).
 */
export function NoteEditor({ note, toolbar = true, compact, autoFocus, placeholder, className }: Props) {
  const t = useT();
  const ref = useRef<HTMLDivElement>(null);
  const shown = useRef<{ id: string; body: Block[] } | null>(null);
  const [focused, setFocused] = useState(false);
  const [link, setLink] = useState<{ url: string; range: Range | null; editing: HTMLAnchorElement | null } | null>(null);
  const [empty, setEmpty] = useState(false);

  // Draw the note when it opens, and again when it changes elsewhere while
  // this box isn't being typed in.
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const prev = shown.current;
    if (prev && prev.id === note.id && (focused || sameBlocks(prev.body, note.body))) return;
    renderBlocks(el, note.body);
    shown.current = { id: note.id, body: note.body };
    setEmpty(isBlank(note.body));
  }, [note.id, note.body, focused]);

  useEffect(() => {
    if (autoFocus && ref.current) {
      ref.current.focus();
      const line = ref.current.lastElementChild as HTMLElement | null;
      if (line) placeAtEnd(line);
    }
  }, [autoFocus, note.id]);

  const emit = () => {
    const el = ref.current;
    if (!el) return;
    const body = readBlocks(el);
    shown.current = { id: note.id, body };
    setEmpty(isBlank(body));
    useNotes.getState().update(note.id, { body });
  };

  const onInput = () => {
    const el = ref.current;
    if (!el) return;
    tidyLines(el);
    const line = caretBlock(el);
    if (line) applyShortcut(line);
    emit();
  };

  const setType = (type: BlockType) => {
    const el = ref.current;
    if (!el) return;
    el.focus();
    const lines = selectedBlocks(el);
    if (!lines.length) return;
    // Pressing the same kind again turns the lines back into plain text.
    const all = lines.every((l) => l.dataset.type === type);
    for (const l of lines) setLineType(l, all ? 'p' : type);
    emit();
  };

  const toggleDone = (lines: HTMLElement[]) => {
    let changed = false;
    for (const l of lines) {
      if (l.dataset.type !== 'todo') continue;
      l.dataset.done = l.dataset.done === '1' ? '0' : '1';
      changed = true;
    }
    if (changed) emit();
  };

  const format = (cmd: 'bold' | 'italic') => {
    ref.current?.focus();
    document.execCommand('styleWithCSS', false, 'false');
    document.execCommand(cmd);
    emit();
  };

  const openLink = () => {
    const el = ref.current;
    if (!el) return;
    const sel = window.getSelection();
    const range = sel && sel.rangeCount && el.contains(sel.anchorNode) ? sel.getRangeAt(0).cloneRange() : null;
    const a = linkAt(el);
    setLink({ url: a?.getAttribute('href') ?? '', range, editing: a });
  };

  const applyLink = (raw: string | null) => {
    const el = ref.current;
    const cur = link;
    setLink(null);
    if (!el || !cur) return;
    el.focus();
    const sel = window.getSelection();
    if (cur.range && sel) {
      sel.removeAllRanges();
      sel.addRange(cur.range);
    }
    if (raw === null) {
      // Remove the link, keep the words.
      if (cur.editing) {
        cur.editing.replaceWith(...Array.from(cur.editing.childNodes));
      } else document.execCommand('unlink');
      emit();
      return;
    }
    const href = safeHref(raw);
    if (!href) return;
    if (cur.editing) {
      cur.editing.setAttribute('href', href);
    } else if (sel && !sel.isCollapsed) {
      document.execCommand('createLink', false, href);
    } else {
      // Nothing selected: put the address itself in as a link.
      document.execCommand('insertHTML', false, `<a href="${escapeAttr(href)}">${escapeText(raw.trim())}</a>`);
    }
    emit();
  };

  const onKeyDown = (e: RKeyboardEvent<HTMLDivElement>) => {
    const el = ref.current;
    if (!el) return;
    const mod = e.ctrlKey || e.metaKey;
    const line = caretBlock(el);
    if (mod && !e.shiftKey && !e.altKey && (e.key === 'b' || e.key === 'i')) {
      e.preventDefault();
      format(e.key === 'b' ? 'bold' : 'italic');
      return;
    }
    if (mod && e.key.toLowerCase() === 'k') {
      e.preventDefault();
      openLink();
      return;
    }
    if (mod && e.shiftKey && (e.code === 'Digit8' || e.code === 'Digit7')) {
      e.preventDefault();
      setType('li');
      return;
    }
    if (mod && e.shiftKey && e.code === 'Digit9') {
      e.preventDefault();
      setType('todo');
      return;
    }
    if (mod && e.key === 'Enter') {
      e.preventDefault();
      toggleDone(selectedBlocks(el));
      return;
    }
    if (!line) return;
    const type = line.dataset.type;
    // Enter on an empty list line, or Backspace at a list line's start: back to plain text.
    if ((e.key === 'Enter' && !e.shiftKey && !(line.textContent ?? '').trim() && type !== 'p') || (e.key === 'Backspace' && type !== 'p' && caretAtLineStart(line))) {
      if (type && type !== 'p') {
        e.preventDefault();
        setLineType(line, 'p');
        emit();
      }
      return;
    }
    if (e.key === 'Enter' && type === 'todo') {
      // A new checklist item starts unchecked.
      requestAnimationFrame(() => {
        const next = caretBlock(el);
        if (next && next !== line && next.dataset.type === 'todo' && next.dataset.done === '1') {
          next.dataset.done = '0';
          emit();
        }
      });
    }
  };

  const onMouseDown = (e: RMouseEvent<HTMLDivElement>) => {
    const el = ref.current;
    if (!el) return;
    const target = e.target as HTMLElement;
    const line = target.closest('.nb') as HTMLElement | null;
    // The checkbox is drawn in the line's left gutter.
    if (line && line.parentElement === el && line.dataset.type === 'todo') {
      const box = line.getBoundingClientRect();
      if (e.clientX - box.left < 26) {
        e.preventDefault();
        toggleDone([line]);
        return;
      }
    }
    const a = target.closest('a');
    if (a && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      const href = safeHref(a.getAttribute('href') ?? '');
      if (href) window.open(href, '_blank', 'noopener,noreferrer');
    }
  };

  return (
    <div className={`ne ${compact ? 'compact' : ''} ${className ?? ''}`}>
      {toolbar && (
        <div className="ne-bar" onMouseDown={(e) => e.preventDefault()}>
          <button type="button" className="ne-btn" title={`${t('notes.fmt.bold')} (${MOD}+B)`} aria-label={t('notes.fmt.bold')} onClick={() => format('bold')}>
            <Icon name="bold" size={16} />
          </button>
          <button type="button" className="ne-btn" title={`${t('notes.fmt.italic')} (${MOD}+I)`} aria-label={t('notes.fmt.italic')} onClick={() => format('italic')}>
            <Icon name="italic" size={16} />
          </button>
          <button type="button" className="ne-btn" title={`${t('notes.fmt.link')} (${MOD}+K)`} aria-label={t('notes.fmt.link')} onClick={openLink}>
            <Icon name="link" size={16} />
          </button>
          <span className="ne-sep" />
          <button type="button" className="ne-btn" title={`${t('notes.fmt.bullets')} (${MOD}+Shift+8)`} aria-label={t('notes.fmt.bullets')} onClick={() => setType('li')}>
            <Icon name="bulletList" size={16} />
          </button>
          <button type="button" className="ne-btn" title={`${t('notes.fmt.checklist')} (${MOD}+Shift+9)`} aria-label={t('notes.fmt.checklist')} onClick={() => setType('todo')}>
            <Icon name="checklist" size={16} />
          </button>
        </div>
      )}
      {link && (
        <LinkBox
          url={link.url}
          editing={!!link.editing}
          onApply={(u) => applyLink(u)}
          onRemove={() => applyLink(null)}
          onCancel={() => {
            setLink(null);
            ref.current?.focus();
          }}
        />
      )}
      <div
        ref={ref}
        className={`ne-body ${empty ? 'empty' : ''}`}
        contentEditable
        suppressContentEditableWarning
        role="textbox"
        aria-multiline="true"
        aria-label={t('notes.bodyLabel')}
        data-placeholder={placeholder ?? t('notes.bodyPlaceholder')}
        spellCheck
        onInput={onInput}
        onKeyDown={onKeyDown}
        onMouseDown={onMouseDown}
        onPaste={(e) => {
          pastePlain(e);
          onInput();
        }}
        onDrop={(e) => e.preventDefault()}
        onFocus={() => setFocused(true)}
        onBlur={() => {
          setFocused(false);
          // Whatever is typed is saved soon; leaving the box saves now.
          flushNote(note.id);
        }}
      />
    </div>
  );
}

function LinkBox({ url, editing, onApply, onRemove, onCancel }: { url: string; editing: boolean; onApply: (u: string) => void; onRemove: () => void; onCancel: () => void }) {
  const t = useT();
  const [v, setV] = useState(url);
  const ok = !!safeHref(v);
  return (
    <form
      className="ne-link glass"
      onSubmit={(e) => {
        e.preventDefault();
        if (ok) onApply(v);
      }}
    >
      <Icon name="link" size={15} />
      <input
        autoFocus
        type="text"
        value={v}
        placeholder={t('notes.linkPlaceholder')}
        aria-label={t('notes.fmt.link')}
        onChange={(e) => setV(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            e.preventDefault();
            e.stopPropagation();
            onCancel();
          }
        }}
      />
      <button type="submit" className="small" disabled={!ok}>
        {t('notes.linkApply')}
      </button>
      {editing && (
        <button type="button" className="ghost small" onClick={onRemove}>
          {t('notes.linkRemove')}
        </button>
      )}
    </form>
  );
}

const isBlank = (b: Block[]) => b.every((x) => x.type === 'p' && !x.spans.some((s) => s.text.trim()));

function placeAtEnd(line: HTMLElement) {
  const sel = window.getSelection();
  if (!sel) return;
  const r = document.createRange();
  r.selectNodeContents(line);
  r.collapse(false);
  sel.removeAllRanges();
  sel.addRange(r);
}

const escapeText = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
const escapeAttr = (s: string) => escapeText(s).replace(/"/g, '&quot;');
