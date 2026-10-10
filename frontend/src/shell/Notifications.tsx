import { useEffect, useRef, useState, type MouseEvent as RMouseEvent } from 'react';
import { APPS, openApp } from '../apps/meta';
import { AppIcon } from '../components/AppTile';
import { Icon } from '../components/Icon';
import { useClock } from '../lib/hooks';
import { fmtTime, useTimePrefs } from '../lib/time';
import { usePrefs } from '../state/prefs';
import { useStoreApps } from '../state/storeApps';
import { toast } from '../state/toasts';
import {
  BANNER_MS,
  isToday,
  notifyApi,
  openNotice,
  relTime,
  useNotifications,
  useNotifyStyle,
  type Notice,
  type NotifySettings,
} from '../state/notifications';

// System notification UI: banners (macOS: top right; Windows: bottom right,
// above the taskbar), the Notification Center, and the bell with its badge.

type Look = 'mac' | 'windows';

/** The app a notification comes from: its name and icon. */
function useSource(n: Notice) {
  const storeApps = useStoreApps((s) => s.apps);
  const icon = n.icon ?? 'settings';
  if (icon.startsWith('store:')) {
    const a = storeApps.find((x) => x.id === icon.slice(6));
    if (a) return { key: icon, name: a.name, app: a };
    return { key: icon, name: APPS.appcenter.title, app: APPS.appcenter };
  }
  const meta = APPS[icon] ?? APPS.settings;
  const name = n.kind === 'update' || n.kind === 'test' ? 'NoCapOS' : meta.title;
  return { key: icon, name, app: meta };
}

// Which "Notify me about" switch a kind belongs to.
const SETTING: Partial<Record<Notice['kind'], keyof NotifySettings>> = {
  update: 'update',
  app_update: 'app_updates',
  app_error: 'app_errors',
  storage: 'storage',
  backup: 'backups',
};

async function turnOff(n: Notice) {
  const key = SETTING[n.kind];
  if (!key) return;
  const cur = await notifyApi.settings();
  if (!cur.ok) return;
  const r = await notifyApi.saveSettings({ ...cur.data, [key]: false });
  if (r.ok) toast('success', 'Turned off', 'Change it any time in Settings → Notifications.');
}

const openSettings = () => openApp('settings', { props: { section: 'notifications' } });

// ---------------- banners ----------------

function Banner({ n, look }: { n: Notice; look: Look }) {
  const src = useSource(n);
  const dismiss = useNotifications((s) => s.dismissBanner);
  const [leaving, setLeaving] = useState(false);
  const [hold, setHold] = useState(false);
  const [menu, setMenu] = useState(false);
  const left = useRef(BANNER_MS);
  const now = useClock(30_000);

  const close = () => {
    setLeaving(true);
    window.setTimeout(() => dismiss(n.id), 280);
  };

  // Auto-dismiss after ~6 s; hovering (or an open menu) pauses the countdown.
  useEffect(() => {
    if (hold || menu || leaving) return;
    const started = Date.now();
    const t = window.setTimeout(close, left.current);
    return () => {
      window.clearTimeout(t);
      left.current = Math.max(1500, left.current - (Date.now() - started));
    };
  }, [hold, menu, leaving]); // close only uses stable setters

  const stop = (e: RMouseEvent, fn: () => void) => {
    e.stopPropagation();
    fn();
  };
  const activate = () => openNotice(n);
  const cls = `nb ${look} lvl-${n.level} ${leaving ? 'leaving' : ''}`;
  const role = n.level === 'error' ? 'alert' : 'status';

  if (look === 'mac') {
    return (
      <div className={cls} role={role} onPointerEnter={() => setHold(true)} onPointerLeave={() => setHold(false)} onClick={activate}>
        <button type="button" className="nb-x" aria-label="Close" title="Close" onClick={(e) => stop(e, close)}>
          <Icon name="close" size={9} />
        </button>
        <AppIcon app={src.app} size={36} />
        <div className="nb-text">
          <div className="nb-top">
            <b className="nb-title">{n.title}</b>
            <span className="nb-time">{relTime(n.created_at, now.getTime())}</span>
          </div>
          {n.body && <div className="nb-body">{n.body}</div>}
        </div>
      </div>
    );
  }

  return (
    <div className={cls} role={role} onPointerEnter={() => setHold(true)} onPointerLeave={() => setHold(false)} onClick={activate}>
      <div className="nb-head">
        <AppIcon app={src.app} size={16} />
        <span className="nb-app">{src.name}</span>
        <span className="spacer" />
        <button type="button" className="nb-tool" aria-label="More options" title="More options" onClick={(e) => stop(e, () => setMenu((v) => !v))}>
          <Icon name="moreDots" size={14} />
        </button>
        <button type="button" className="nb-tool" aria-label="Close" title="Close" onClick={(e) => stop(e, close)}>
          <Icon name="close" size={12} />
        </button>
        {menu && (
          <div className="nb-menu" role="menu" onClick={(e) => e.stopPropagation()}>
            {SETTING[n.kind] && (
              <button type="button" role="menuitem" onClick={() => { setMenu(false); void turnOff(n); close(); }}>
                Turn off notifications like this
              </button>
            )}
            <button type="button" role="menuitem" onClick={() => { setMenu(false); openSettings(); close(); }}>
              Go to notification settings
            </button>
          </div>
        )}
      </div>
      <div className="nb-main">
        <b className="nb-title">{n.title}</b>
        {n.body && <div className="nb-body">{n.body}</div>}
      </div>
      {n.action && (
        <div className="nb-actions">
          <button type="button" className="nb-btn" onClick={(e) => stop(e, activate)}>
            Open
          </button>
        </div>
      )}
    </div>
  );
}

function Banners({ look }: { look: Look }) {
  const banners = useNotifications((s) => s.banners);
  if (!banners.length) return null;
  return (
    <div className={`nb-stack ${look}`} aria-live="polite">
      {banners.map((n) => (
        <Banner key={n.id} n={n} look={look} />
      ))}
    </div>
  );
}

// ---------------- Notification Center ----------------

function Card({ n, look }: { n: Notice; look: Look }) {
  const src = useSource(n);
  const remove = useNotifications((s) => s.remove);
  const tp = useTimePrefs();
  const now = useClock(30_000);
  const when =
    look === 'mac'
      ? relTime(n.created_at, now.getTime())
      : isToday(n.created_at, now)
        ? fmtTime(new Date(n.created_at), tp, false)
        : new Date(n.created_at).toLocaleDateString(undefined, { day: 'numeric', month: 'short' }) + ' ' + fmtTime(new Date(n.created_at), tp, false);
  return (
    <div
      className={`nc-card ${look} lvl-${n.level}`}
      role="button"
      tabIndex={0}
      onClick={() => openNotice(n)}
      onKeyDown={(e) => e.key === 'Enter' && openNotice(n)}
    >
      <button
        type="button"
        className="nb-x"
        aria-label="Clear"
        title="Clear"
        onClick={(e) => {
          e.stopPropagation();
          remove(n.id);
        }}
      >
        <Icon name="close" size={look === 'mac' ? 9 : 12} />
      </button>
      {look === 'mac' && <AppIcon app={src.app} size={34} />}
      <div className="nb-text">
        <div className="nb-top">
          <b className="nb-title">{n.title}</b>
          <span className="nb-time">{when}</span>
        </div>
        {n.body && <div className="nb-body">{n.body}</div>}
      </div>
    </div>
  );
}

function Empty() {
  return (
    <div className="nc-empty">
      <Icon name="bell" size={26} />
      <span>No new notifications</span>
    </div>
  );
}

function MacCenter({ items }: { items: Notice[] }) {
  const clear = useNotifications((s) => s.clear);
  const now = new Date();
  const today = items.filter((n) => isToday(n.created_at, now));
  const earlier = items.filter((n) => !isToday(n.created_at, now));
  return (
    <>
      <div className="nc-head">
        <h2>Notifications</h2>
        {items.length > 0 && (
          <button type="button" className="nc-clear" onClick={clear}>
            Clear all
          </button>
        )}
      </div>
      <div className="nc-scroll">
        {!items.length && <Empty />}
        {[
          ['Today', today],
          ['Earlier', earlier],
        ].map(([label, list]) =>
          (list as Notice[]).length ? (
            <section key={label as string} className="nc-group">
              <div className="nc-group-title">{label as string}</div>
              {(list as Notice[]).map((n) => (
                <Card key={n.id} n={n} look="mac" />
              ))}
            </section>
          ) : null,
        )}
      </div>
    </>
  );
}

function WinGroup({ list }: { list: Notice[] }) {
  const src = useSource(list[0]);
  const removeMany = useNotifications((s) => s.removeMany);
  return (
    <section className="nc-group">
      <div className="nc-app">
        <AppIcon app={src.app} size={16} />
        <span>{src.name}</span>
        <span className="spacer" />
        <button type="button" className="nb-tool" aria-label={`Clear ${src.name} notifications`} title="Clear" onClick={() => removeMany(list.map((n) => n.id))}>
          <Icon name="close" size={12} />
        </button>
      </div>
      {list.map((n) => (
        <Card key={n.id} n={n} look="windows" />
      ))}
    </section>
  );
}

/** Windows groups by app (newest app first). */
function byApp(items: Notice[]) {
  const groups = new Map<string, Notice[]>();
  for (const n of items) {
    const k = (n.kind === 'update' || n.kind === 'test' ? 'nocapos:' : '') + (n.icon ?? 'settings');
    groups.set(k, [...(groups.get(k) ?? []), n]);
  }
  return [...groups.entries()];
}

function WinCenter({ items }: { items: Notice[] }) {
  const clear = useNotifications((s) => s.clear);
  const focus = usePrefs((s) => s.focusMode);
  return (
    <>
      <div className="nc-head">
        <h2>Notifications</h2>
        <span className="spacer" />
        <button
          type="button"
          className={`nb-tool nc-focus ${focus ? 'on' : ''}`}
          aria-pressed={focus}
          title={focus ? 'Focus is on: only errors pop up' : 'Turn on Focus (do not disturb)'}
          onClick={() => usePrefs.getState().set({ focusMode: !focus })}
        >
          <Icon name="moon" size={14} />
        </button>
        {items.length > 0 && (
          <button type="button" className="nc-clear" onClick={clear}>
            Clear all
          </button>
        )}
      </div>
      <div className="nc-scroll">
        {!items.length && <Empty />}
        {byApp(items).map(([k, list]) => (
          <WinGroup key={k} list={list} />
        ))}
      </div>
    </>
  );
}

function Center({ look }: { look: Look }) {
  const open = useNotifications((s) => s.centerOpen);
  const items = useNotifications((s) => s.items);
  const [shown, setShown] = useState(open);
  const ref = useRef<HTMLElement>(null);

  // Keep it mounted briefly after closing so it can slide away.
  useEffect(() => {
    if (open) {
      setShown(true);
      return;
    }
    const t = window.setTimeout(() => setShown(false), 220);
    return () => window.clearTimeout(t);
  }, [open]);

  // Close on a press outside (the bell buttons toggle it themselves) or Escape.
  useEffect(() => {
    if (!open) return;
    const onDown = (e: PointerEvent) => {
      const t = e.target as Element | null;
      if (ref.current?.contains(t) || t?.closest?.('[data-notify-bell], .context-menu, .confirm-backdrop')) return;
      useNotifications.getState().setCenter(false);
    };
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && useNotifications.getState().setCenter(false);
    window.addEventListener('pointerdown', onDown);
    window.addEventListener('keydown', onKey);
    return () => {
      window.removeEventListener('pointerdown', onDown);
      window.removeEventListener('keydown', onKey);
    };
  }, [open]);

  if (!shown) return null;
  return (
    <aside ref={ref} className={`nc ${look} ${open ? '' : 'leaving'}`} aria-label="Notification Center">
      {look === 'mac' ? <MacCenter items={items} /> : <WinCenter items={items} />}
    </aside>
  );
}

/** Banners and the Notification Center (admins). */
export function NotificationLayer() {
  const look = useNotifyStyle();
  return (
    <>
      <Banners look={look} />
      <Center look={look} />
    </>
  );
}

/** The bell in the tray / menu bar, with the unread count. */
export function NotifyBell({ className, size = 16 }: { className: string; size?: number }) {
  const unread = useNotifications((s) => s.unread);
  const open = useNotifications((s) => s.centerOpen);
  const label = unread ? `Notifications — ${unread} unread` : 'Notifications';
  return (
    <button
      type="button"
      data-notify-bell
      className={`${className} nb-bell ${open ? 'open' : ''}`}
      aria-label={label}
      aria-expanded={open}
      title={label}
      onClick={() => useNotifications.getState().setCenter(!open)}
    >
      <Icon name="bell" size={size} />
      {unread > 0 && <span className="nb-badge">{unread > 99 ? '99+' : unread}</span>}
    </button>
  );
}

/** A still banner for Settings, showing what the chosen style looks like. */
export function BannerPreview({ look }: { look: Look }) {
  const title = 'NoCapOS 0.4.0 is available';
  const body = 'Open Software Update to see what’s new and install it.';
  if (look === 'mac') {
    return (
      <div className="nb mac still" aria-hidden>
        <AppIcon app={APPS.settings} size={36} />
        <div className="nb-text">
          <div className="nb-top">
            <b className="nb-title">{title}</b>
            <span className="nb-time">now</span>
          </div>
          <div className="nb-body">{body}</div>
        </div>
      </div>
    );
  }
  return (
    <div className="nb windows still" aria-hidden>
      <div className="nb-head">
        <AppIcon app={APPS.settings} size={16} />
        <span className="nb-app">NoCapOS</span>
        <span className="spacer" />
        <span className="nb-tool">
          <Icon name="moreDots" size={14} />
        </span>
        <span className="nb-tool">
          <Icon name="close" size={12} />
        </span>
      </div>
      <div className="nb-main">
        <b className="nb-title">{title}</b>
        <div className="nb-body">{body}</div>
      </div>
      <div className="nb-actions">
        <span className="nb-btn">Open</span>
      </div>
    </div>
  );
}
