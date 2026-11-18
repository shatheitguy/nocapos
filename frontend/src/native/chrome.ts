// NoCap Desktop's chrome surface: NoCap OS's menu bar, dock, launcher,
// search, panels and notifications over the windows of the screen. The
// surface covers the whole screen but is see-through; this keeps the host
// told which parts of it take clicks (the input region), how much room the
// menu bar and dock keep free for maximized windows, and when it needs the
// keyboard. It also mirrors the screen's NoCap OS app windows into the window
// manager store, so the dock and menu bar show them exactly as in the
// browser.
import { useWM, type WinState } from '../state/windows';
import { hostCall, hostOn, hostSend } from './platform';

// ---------- the input region ----------

/** Elements that are the page's own background: clicks there go through. */
function isBackground(el: Element | null): boolean {
  if (!el) return true;
  return el === document.documentElement || el === document.body || el.id === 'root' || el.classList.contains('desktop');
}

/**
 * The part of the chrome an element at (x, y) belongs to: its outermost
 * ancestor whose box still holds that point (the menu bar, the dock, a panel
 * that opens out of the menu bar, a dialog).
 */
function partOf(el: Element, x: number, y: number): Element {
  let cur = el;
  for (;;) {
    const up = cur.parentElement;
    if (!up || isBackground(up)) return cur;
    const r = up.getBoundingClientRect();
    if (x < r.left || x >= r.right || y < r.top || y >= r.bottom) return cur;
    cur = up;
  }
}

export type Rect = [number, number, number, number];

/**
 * The parts of the page that take clicks: a grid of hit tests finds them,
 * and each part's exact box goes into the region. `step` is the grid size in
 * CSS pixels; anything smaller than it is found through its part anyway.
 */
export function hitParts(width: number, height: number, hit: (x: number, y: number) => Element | null, step = 24): Element[] {
  const parts = new Set<Element>();
  for (let y = step / 2; y < height; y += step) {
    for (let x = step / 2; x < width; x += step) {
      const el = hit(x, y);
      if (!isBackground(el)) parts.add(partOf(el!, x, y));
    }
  }
  // The very edges too (the menu bar's top row, a dock against the bottom).
  for (let x = step / 2; x < width; x += step) {
    for (const y of [1, height - 2]) {
      const el = hit(x, y);
      if (!isBackground(el)) parts.add(partOf(el!, x, y));
    }
  }
  return [...parts];
}

/** Device-independent integer rectangles, clipped to the screen, merged when identical. */
export function regionOf(boxes: DOMRect[], width: number, height: number): { rects: Rect[]; full: boolean } {
  const out: Rect[] = [];
  const seen = new Set<string>();
  let full = false;
  for (const b of boxes) {
    const x = Math.max(0, Math.floor(b.left));
    const y = Math.max(0, Math.floor(b.top));
    const w = Math.min(width, Math.ceil(b.right)) - x;
    const h = Math.min(height, Math.ceil(b.bottom)) - y;
    if (w <= 0 || h <= 0) continue;
    if (w >= width && h >= height) full = true;
    const k = `${x},${y},${w},${h}`;
    if (!seen.has(k)) {
      seen.add(k);
      out.push([x, y, w, h]);
    }
  }
  return { rects: out.slice(0, 256), full };
}

let lastRegion = '';
let lastKeyboard = '';
let lastZones = '';

function updateRegion() {
  const w = window.innerWidth;
  const h = window.innerHeight;
  const parts = hitParts(w, h, (x, y) => document.elementFromPoint(x, y));
  const { rects, full } = regionOf(
    parts.map((p) => p.getBoundingClientRect()),
    w,
    h,
  );
  const key = full ? 'full' : JSON.stringify(rects);
  if (key !== lastRegion) {
    lastRegion = key;
    hostSend('surface.input', { rects: full ? [] : rects.flat(), full });
  }
  // A full-screen part (the launcher, search, a dialog) has the keyboard to
  // itself; otherwise the chrome takes it when clicked.
  const kb = full ? 'exclusive' : 'on-demand';
  if (kb !== lastKeyboard) {
    lastKeyboard = kb;
    hostSend('surface.keyboard', { mode: kb });
  }
  updateZones(w, h);
}

/** Room kept free for maximized windows: the menu bar and a dock that stays. */
function updateZones(w: number, h: number) {
  const bar = document.querySelector('.menubar');
  const dock = document.querySelector('.dock');
  const zones = { top: 0, bottom: 0, left: 0, right: 0 };
  if (bar) zones.top = Math.ceil(bar.getBoundingClientRect().bottom);
  if (dock && !dock.closest('.autohide')) {
    const r = dock.getBoundingClientRect();
    if (r.height > 0 && r.width > 0) {
      if (r.bottom >= h - 40 && r.width > r.height) zones.bottom = Math.ceil(h - r.top);
      else if (r.left <= 40 && r.height > r.width) zones.left = Math.ceil(r.right);
      else if (r.right >= w - 40 && r.height > r.width) zones.right = Math.ceil(w - r.left);
    }
  }
  for (const k of Object.keys(zones) as (keyof typeof zones)[]) zones[k] = Math.min(600, Math.max(0, zones[k]));
  const key = JSON.stringify(zones);
  if (key !== lastZones) {
    lastZones = key;
    hostSend('surface.zones', zones);
  }
}

/** Keeps the input region, keyboard and zones up to date until stopped. */
export function watchChromeRegion(): () => void {
  let timer = 0;
  const soon = () => {
    if (timer) return;
    timer = window.setTimeout(() => {
      timer = 0;
      updateRegion();
    }, 60);
  };
  const mo = new MutationObserver(soon);
  mo.observe(document.body, { subtree: true, childList: true, attributes: true, attributeFilter: ['class', 'style', 'hidden'] });
  window.addEventListener('resize', soon);
  document.addEventListener('transitionend', soon, true);
  document.addEventListener('animationend', soon, true);
  // Safety net for changes nothing announces.
  const tick = window.setInterval(soon, 2000);
  soon();
  return () => {
    mo.disconnect();
    window.removeEventListener('resize', soon);
    document.removeEventListener('transitionend', soon, true);
    document.removeEventListener('animationend', soon, true);
    window.clearInterval(tick);
    window.clearTimeout(timer);
  };
}

// ---------- the screen's NoCap OS windows ----------

interface HostWindow {
  id: string;
  appId: string;
  title: string;
  active: boolean;
  minimized: boolean;
  maximized: boolean;
}

const APP_PREFIX = 'io.nocapos.App.';

/** The host's window list as window manager entries: NoCap OS's own apps only. */
export function windowsFromHost(list: readonly HostWindow[]): { windows: WinState[]; focused: string | null } {
  let focused: string | null = null;
  const windows: WinState[] = [];
  list.forEach((w, i) => {
    if (!w.appId.startsWith(APP_PREFIX)) return;
    const id = `native:${w.id}`;
    if (w.active) focused = id;
    windows.push({
      id,
      appId: w.appId.slice(APP_PREFIX.length),
      title: w.title,
      x: 0,
      y: 0,
      w: 0,
      h: 0,
      z: i + 1,
      minimized: w.minimized,
      maximized: w.maximized,
      snap: null,
    });
  });
  return { windows, focused };
}

const hostId = (id: string) => (id.startsWith('native:') ? id.slice(7) : null);

/** The window manager store follows the screen's real windows; its actions act on them. */
export function mirrorWindows(): () => void {
  const apply = (list: unknown) => {
    if (!Array.isArray(list)) return;
    useWM.setState(windowsFromHost(list as HostWindow[]));
  };
  const act = (method: string) => (id: string) => {
    const h = hostId(id);
    if (h) hostSend(method, { id: h });
  };
  useWM.setState({
    focus: act('windows.focus'),
    minimize: act('windows.minimize'),
    close: act('windows.close'),
    toggleMaximize: act('windows.maximize'),
    // Windows aren't laid out here: the compositor does that.
    restoreLayout: () => {},
  });
  const off = hostOn('windows.changed', apply);
  void hostCall('windows.list').then(apply, () => undefined);
  return off;
}
