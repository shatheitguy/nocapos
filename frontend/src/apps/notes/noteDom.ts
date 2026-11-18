// The note editor's DOM side: drawing a note into an editable box and reading
// it back. What's read is only ever text, bold, italic, links (web and mail)
// and line kinds, whatever the browser or a paste put in the box; it is drawn
// again from that model with createElement and text nodes, never HTML.
import type { ClipboardEvent as RClipboardEvent } from 'react';
import { lineShortcut, normalizeBlocks, safeHref, type Block, type BlockType, type Span } from './noteDoc';

const BLOCK_TAGS = new Set(['DIV', 'P', 'LI', 'UL', 'OL', 'H1', 'H2', 'H3', 'H4', 'H5', 'H6', 'BLOCKQUOTE', 'PRE', 'SECTION', 'ARTICLE']);

type Marks = Omit<Span, 'text'>;

/** Draws blocks into root (an editable box). */
export function renderBlocks(root: HTMLElement, blocks: Block[]) {
  root.replaceChildren(...(blocks.length ? blocks : [{ type: 'p' as const, spans: [] }]).map(blockEl));
}

export function blockEl(b: Block): HTMLElement {
  const div = document.createElement('div');
  div.className = 'nb';
  div.dataset.type = b.type;
  if (b.type === 'todo') div.dataset.done = b.done ? '1' : '0';
  const spans = b.spans.filter((s) => s.text);
  if (!spans.length) div.appendChild(document.createElement('br'));
  for (const s of spans) {
    let node: Node = document.createTextNode(s.text);
    if (s.italic) node = wrap('i', node);
    if (s.bold) node = wrap('b', node);
    if (s.href) {
      const a = document.createElement('a');
      a.href = s.href;
      a.target = '_blank';
      a.rel = 'noopener noreferrer';
      a.appendChild(node);
      node = a;
    }
    div.appendChild(node);
  }
  return div;
}

function wrap(tag: string, child: Node): HTMLElement {
  const el = document.createElement(tag);
  el.appendChild(child);
  return el;
}

/** Reads the box back into blocks. */
export function readBlocks(root: HTMLElement): Block[] {
  const blocks: Block[] = [];
  let cur: Block | null = null;
  const start = (type: BlockType, done = false) => {
    cur = { type, spans: [] };
    if (type === 'todo' && done) cur.done = true;
    blocks.push(cur);
    return cur;
  };
  const walk = (node: Node, marks: Marks, listType: BlockType | undefined) => {
    if (node.nodeType === Node.TEXT_NODE) {
      const t = (node.textContent ?? '').replace(/​/g, '').replace(/[\r\n]+/g, ' ');
      if (t) (cur ?? start(listType ?? 'p')).spans.push({ text: t, ...marks });
      return;
    }
    if (node.nodeType !== Node.ELEMENT_NODE) return;
    const el = node as HTMLElement;
    const tag = el.tagName;
    if (tag === 'BR') {
      if (!trailingBr(el, root)) start(cur?.type === 'todo' ? 'todo' : (cur?.type ?? listType ?? 'p'));
      return;
    }
    if (tag === 'SCRIPT' || tag === 'STYLE' || tag === 'TEMPLATE') return;
    let m = marks;
    const st = el.style;
    if (tag === 'B' || tag === 'STRONG' || st.fontWeight === 'bold' || Number(st.fontWeight) >= 600) m = { ...m, bold: true };
    if (tag === 'I' || tag === 'EM' || st.fontStyle === 'italic') m = { ...m, italic: true };
    if (tag === 'A') {
      const h = safeHref(el.getAttribute('href') ?? '');
      if (h) m = { ...m, href: h };
    }
    if (!BLOCK_TAGS.has(tag)) {
      for (const c of Array.from(el.childNodes)) walk(c, m, listType);
      return;
    }
    if (tag === 'UL' || tag === 'OL') {
      cur = null;
      for (const c of Array.from(el.childNodes)) walk(c, m, 'li');
      cur = null;
      return;
    }
    const dt = el.dataset.type;
    let type: BlockType = 'p';
    if (dt === 'li' || dt === 'todo' || dt === 'p') type = dt;
    else if (tag === 'LI') type = listType ?? 'li';
    else if (listType) type = listType;
    start(type, type === 'todo' && el.dataset.done === '1');
    for (const c of Array.from(el.childNodes)) walk(c, m, type === 'p' ? undefined : type);
    cur = null;
  };
  for (const c of Array.from(root.childNodes)) walk(c, {}, undefined);
  const out = normalizeBlocks(blocks);
  return out.length ? out : [{ type: 'p', spans: [] }];
}

/** Browsers keep a <br> at the end of a line so it has height; that one isn't a new line. */
function trailingBr(br: HTMLElement, root: HTMLElement): boolean {
  let n: Node | null = br;
  while (n && n !== root) {
    let sib = n.nextSibling;
    while (sib && sib.nodeType === Node.TEXT_NODE && !(sib.textContent ?? '').replace(/​/g, '')) sib = sib.nextSibling;
    if (sib) return false;
    const parent: Node | null = n.parentNode;
    if (!parent || parent === root || (parent.nodeType === Node.ELEMENT_NODE && BLOCK_TAGS.has((parent as HTMLElement).tagName))) return true;
    n = parent;
  }
  return true;
}

// ---------- caret and lines ----------

/** The line the caret is on. */
export function caretBlock(root: HTMLElement): HTMLElement | null {
  const sel = window.getSelection();
  if (!sel || !sel.rangeCount) return null;
  return lineOf(root, sel.getRangeAt(0).startContainer);
}

export function lineOf(root: HTMLElement, node: Node | null): HTMLElement | null {
  let n: Node | null = node;
  while (n && n !== root) {
    if (n.parentNode === root && n.nodeType === Node.ELEMENT_NODE) return n as HTMLElement;
    n = n.parentNode;
  }
  return null;
}

/** The lines the selection touches (just the caret's line when it's collapsed). */
export function selectedBlocks(root: HTMLElement): HTMLElement[] {
  const sel = window.getSelection();
  if (!sel || !sel.rangeCount || !root.contains(sel.anchorNode)) return [];
  const r = sel.getRangeAt(0);
  const out = Array.from(root.children).filter((el) => r.intersectsNode(el)) as HTMLElement[];
  if (out.length) return out;
  const one = caretBlock(root);
  return one ? [one] : [];
}

/** Makes sure every top-level line is a .nb line (typing into an empty box makes bare text). */
export function tidyLines(root: HTMLElement) {
  if (!root.firstChild) {
    const line = blockEl({ type: 'p', spans: [] });
    root.appendChild(line);
    placeCaret(line, false);
    return;
  }
  for (const n of Array.from(root.childNodes)) {
    if (n.nodeType === Node.ELEMENT_NODE && (n as HTMLElement).classList.contains('nb')) continue;
    if (n.nodeType === Node.ELEMENT_NODE && BLOCK_TAGS.has((n as HTMLElement).tagName)) {
      const el = n as HTMLElement;
      el.classList.add('nb');
      if (!el.dataset.type) el.dataset.type = 'p';
      continue;
    }
    // Bare text or inline bits: into a line of their own (keeping the caret).
    const sel = window.getSelection();
    const hadCaret = !!sel?.rangeCount && n.contains(sel.anchorNode);
    const offset = hadCaret ? sel!.anchorOffset : 0;
    const anchor = hadCaret ? sel!.anchorNode : null;
    const div = blockEl({ type: 'p', spans: [] });
    div.replaceChildren();
    root.insertBefore(div, n);
    div.appendChild(n);
    if (hadCaret && anchor) {
      const r = document.createRange();
      r.setStart(anchor, offset);
      r.collapse(true);
      sel!.removeAllRanges();
      sel!.addRange(r);
    }
  }
}

export function setLineType(el: HTMLElement, type: BlockType) {
  el.classList.add('nb');
  el.dataset.type = type;
  if (type === 'todo') el.dataset.done = el.dataset.done === '1' ? '1' : '0';
  else delete el.dataset.done;
}

/** Turns "- ", "[ ] " and the like at the start of a plain line into a list line. Reports whether it did. */
export function applyShortcut(line: HTMLElement): boolean {
  if (line.dataset.type && line.dataset.type !== 'p') return false;
  const text = line.textContent ?? '';
  const sc = lineShortcut(text);
  if (!sc) return false;
  const at = caretOffset(line);
  setLineType(line, sc.type);
  if (sc.type === 'todo') line.dataset.done = sc.done ? '1' : '0';
  // Drop the typed marker, keeping the caret where it was.
  let left = sc.strip;
  for (const t of textNodes(line)) {
    if (left <= 0) break;
    const n = Math.min(left, t.data.length);
    t.deleteData(0, n);
    left -= n;
  }
  if (!(line.textContent ?? '')) {
    line.replaceChildren(document.createElement('br'));
    placeCaret(line, false);
  } else setCaretOffset(line, Math.max(0, (at ?? sc.strip) - sc.strip));
  return true;
}

function textNodes(el: HTMLElement): Text[] {
  const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
  const out: Text[] = [];
  while (walker.nextNode()) out.push(walker.currentNode as Text);
  return out;
}

/** How many characters into the line the caret is (null when it isn't in it). */
export function caretOffset(line: HTMLElement): number | null {
  const sel = window.getSelection();
  if (!sel || !sel.rangeCount || !line.contains(sel.anchorNode)) return null;
  const r = document.createRange();
  r.selectNodeContents(line);
  r.setEnd(sel.anchorNode!, sel.anchorOffset);
  return r.toString().length;
}

export function setCaretOffset(line: HTMLElement, offset: number) {
  const sel = window.getSelection();
  if (!sel) return;
  let left = offset;
  for (const t of textNodes(line)) {
    if (left <= t.data.length) {
      const r = document.createRange();
      r.setStart(t, left);
      r.collapse(true);
      sel.removeAllRanges();
      sel.addRange(r);
      return;
    }
    left -= t.data.length;
  }
  placeCaret(line, true);
}

/** Puts the caret at the start (or end) of a line. */
export function placeCaret(line: HTMLElement, atEnd: boolean) {
  const sel = window.getSelection();
  if (!sel) return;
  const r = document.createRange();
  const walker = document.createTreeWalker(line, NodeFilter.SHOW_TEXT);
  let first: Text | null = null;
  let last: Text | null = null;
  while (walker.nextNode()) {
    const t = walker.currentNode as Text;
    if (!first) first = t;
    last = t;
  }
  if (atEnd && last) r.setStart(last, last.data.length);
  else if (!atEnd && first) r.setStart(first, 0);
  else r.setStart(line, 0);
  r.collapse(true);
  sel.removeAllRanges();
  sel.addRange(r);
}

/** Whether the caret sits at the very start of its line. */
export function caretAtLineStart(line: HTMLElement): boolean {
  const sel = window.getSelection();
  if (!sel || !sel.rangeCount || !sel.isCollapsed) return false;
  const r = document.createRange();
  r.selectNodeContents(line);
  r.setEnd(sel.anchorNode!, sel.anchorOffset);
  return r.toString().replace(/​/g, '') === '';
}

/** The link the caret or selection is in, if any. */
export function linkAt(root: HTMLElement): HTMLAnchorElement | null {
  const sel = window.getSelection();
  if (!sel || !sel.rangeCount) return null;
  let n: Node | null = sel.anchorNode;
  while (n && n !== root) {
    if (n.nodeType === Node.ELEMENT_NODE && (n as HTMLElement).tagName === 'A') return n as HTMLAnchorElement;
    n = n.parentNode;
  }
  return null;
}

/** Paste as plain text, one line per line. */
export function pastePlain(e: ClipboardEvent | RClipboardEvent) {
  e.preventDefault();
  const text = (e.clipboardData?.getData('text/plain') ?? '').replace(/\r\n?/g, '\n');
  // insertText keeps the browser's undo history.
  document.execCommand('insertText', false, text);
}
