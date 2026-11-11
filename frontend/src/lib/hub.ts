// NoCap Hub: NoCapOS in its own app window on the full desktop
// (/usr/local/lib/nocapos/nocap-hub opens /?hub=1, optionally &open=<app>).
// In the hub, NoCapOS's notifications also show as the desktop's own
// notifications (the browser hands Web Notifications to the desktop; the
// installer's browser policy allows them for the local address).

const params = () => new URLSearchParams(window.location.search);

/** This page is the NoCap Hub window on the server's own desktop. */
export const isHub = (): boolean => params().has('hub');

/** The app the hub was asked to open (nocap-hub appcenter, settings, files...), once. */
export function takeHubApp(): string | null {
  const p = params();
  const app = p.get('open');
  if (!app || !/^[a-z0-9-]{1,32}$/.test(app)) return null;
  p.delete('open');
  const q = p.toString();
  window.history.replaceState(null, '', window.location.pathname + (q ? `?${q}` : '') + window.location.hash);
  return app;
}

/** Shows a notification on the desktop when the hub window isn't in front. */
export function desktopNotify(title: string, body: string | undefined, tag: string, onClick: () => void) {
  if (!isHub() || typeof Notification === 'undefined') return;
  if (document.visibilityState === 'visible' && document.hasFocus()) return; // the banner shows already
  const show = () => {
    try {
      const n = new Notification(title, { body, tag, icon: '/brand/mark-128.png' });
      n.onclick = () => {
        window.focus();
        onClick();
        n.close();
      };
    } catch {
      /* not allowed here */
    }
  };
  if (Notification.permission === 'granted') show();
  else if (Notification.permission === 'default') void Notification.requestPermission().then((p) => p === 'granted' && show());
}
