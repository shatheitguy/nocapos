// Batched browser-storage writes. Dragging a window or a slider changes state
// on every pointermove; writing localStorage each time stalls the main thread,
// so writes are coalesced per key and flushed at most every few hundred ms,
// and always before the page goes away.

const pending = new Map<string, () => string>();
let timer = 0;

/** Queue `localStorage[key] = value()`; the value is computed at flush time. */
export function saveLater(key: string, value: () => string, ms = 500) {
  pending.set(key, value);
  if (!timer) timer = window.setTimeout(flushSaves, ms);
}

/** Write everything queued now. */
export function flushSaves() {
  if (timer) {
    window.clearTimeout(timer);
    timer = 0;
  }
  if (!pending.size) return;
  const items = [...pending];
  pending.clear();
  for (const [key, value] of items) {
    try {
      localStorage.setItem(key, value());
    } catch {
      /* storage may be unavailable or full */
    }
  }
}

window.addEventListener('pagehide', flushSaves);
window.addEventListener('beforeunload', flushSaves);
document.addEventListener('visibilitychange', () => {
  if (document.hidden) flushSaves();
});
