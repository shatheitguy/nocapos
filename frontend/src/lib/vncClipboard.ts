// Keeps the browser's clipboard and a streamed app's in sync.
//
// The stream's noVNC page is same-origin (alfad proxies it), and its
// clipboard panel is a textarea: x11vnc puts the app's clipboard there, and
// noVNC sends the textarea to the app when it changes. So: whatever the app
// copies is written to the browser's clipboard, and when the stream gets
// focus the browser's clipboard is handed to the app. Browsers may ask
// once for clipboard access; if it's refused, noVNC's own clipboard panel
// still works.

const AREA = 'noVNC_clipboard_text';

export function bridgeClipboard(frame: HTMLIFrameElement): () => void {
  let last = '';
  let timer = 0;
  let win: Window | null = null;

  const area = (): HTMLTextAreaElement | null => {
    try {
      return (frame.contentDocument?.getElementById(AREA) as HTMLTextAreaElement | null) ?? null;
    } catch {
      return null; // not same-origin (yet)
    }
  };

  // App → browser.
  const poll = () => {
    const a = area();
    if (!a || a.value === last) return;
    last = a.value;
    void frame.contentWindow?.navigator.clipboard?.writeText(last).catch(() => {});
  };

  // Browser → app, when the stream gets focus.
  const onFocus = async () => {
    try {
      const text = await frame.contentWindow?.navigator.clipboard?.readText();
      const a = area();
      if (!a || text == null || text === last) return;
      last = text;
      a.value = text;
      a.dispatchEvent(new Event('change'));
    } catch {
      /* no clipboard access: noVNC's clipboard panel still works */
    }
  };

  const attach = () => {
    win?.removeEventListener('focus', onFocus);
    win = frame.contentWindow;
    win?.addEventListener('focus', onFocus);
  };

  frame.addEventListener('load', attach);
  attach();
  timer = window.setInterval(poll, 800);
  return () => {
    window.clearInterval(timer);
    frame.removeEventListener('load', attach);
    win?.removeEventListener('focus', onFocus);
  };
}
