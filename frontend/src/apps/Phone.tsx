import { useCallback, useEffect, useRef, useState, type MouseEvent } from 'react';
import { mirrorApi, type MirrorPhone } from '../api/mirror';
import { devicesApi, type DeviceLimits, type LinkedDevice } from '../api/devices';
import { upload } from '../api/files';
import { receivedApi } from '../api/received';
import { ContextMenu, type MenuItem } from '../components/ContextMenu';
import { Dialog, type DialogSpec } from '../components/Dialog';
import { Icon, type IconName } from '../components/Icon';
import { tr, useT, type T } from '../i18n';
import { confirmDialog } from '../state/confirm';
import { linkApi } from '../state/link';
import { toast } from '../state/toasts';
import { openUrlOnPhone, webUrl } from '../shell/linkActions';
import { openApp } from './meta';
import { Mirror, type DeviceForm } from './PhoneMirror';
import { LinkSheet } from './SettingsDevices';
import { findApi, type FoundServer } from './SettingsFindServers';

// Device Screens ("Screen Squad"): every device linked to your account, with
// its type, whether it's online and its battery; pick one to see its live
// screen in a frame of its shape. Each device has a menu (rename, ring, send
// a file or a link, open in Linked Devices, unlink), and other NoCap servers
// on this network can be opened in a new tab. Only your own devices are
// listed (the server enforces it). The app id stays "phone" so older links
// and pins keep working.

const PICK_KEY = 'nocap.phone.last';

function loadPick(): string | null {
  try {
    return localStorage.getItem(PICK_KEY);
  } catch {
    return null;
  }
}
function savePick(id: string) {
  try {
    localStorage.setItem(PICK_KEY, id);
  } catch {
    /* private window */
  }
}

/** The shape to frame a device in (Android phones until they say). */
export function deviceForm(d: MirrorPhone): DeviceForm {
  if (d.form === 'tablet' || d.form === 'computer' || d.form === 'phone') return d.form;
  return d.platform === 'android' || d.platform === 'ios' ? 'phone' : 'computer';
}

const FORM_ICON: Record<DeviceForm, IconName> = {
  phone: 'phone',
  tablet: 'tablet',
  computer: 'desktop',
};

/** Why a device can't show its screen here, or "" when it can. Android
 * apps too old to share it say so themselves once asked. */
function cantStream(d: MirrorPhone): '' | 'platform' {
  return d.platform === 'android' ? '' : 'platform';
}

function ago(t: T, iso: string): string {
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 60) return t('phone.ago.now');
  if (s < 3600) return t('phone.ago.min', { n: Math.floor(s / 60) });
  if (s < 86400) return t('phone.ago.hour', { n: Math.floor(s / 3600) });
  return t('phone.ago.day', { n: Math.floor(s / 86400) });
}

const isOnline = (d: MirrorPhone) => !!(d.online || d.mirror?.online);

/** "Online" / "Last seen 3 min ago", plus the battery. */
function statusLine(t: T, d: MirrorPhone): string {
  const seen = isOnline(d) ? t('ds.online') : d.last_seen_at ? t('phone.lastSeen', { ago: ago(t, d.last_seen_at) }) : t('phone.neverSeen');
  return d.battery != null ? `${seen} · ${t('phone.battery', { n: d.battery })}` : seen;
}

interface Sending {
  device: string;
  name: string;
  loaded: number;
  total: number;
}

export function Phone() {
  const t = useT();
  const [devices, setDevices] = useState<MirrorPhone[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [picked, setPicked] = useState<string | null>(null);
  const [menu, setMenu] = useState<{
    x: number;
    y: number;
    d: MirrorPhone;
  } | null>(null);
  const [dialog, setDialog] = useState<DialogSpec | null>(null);
  const [sending, setSending] = useState<Sending | null>(null);
  const [linking, setLinking] = useState<{
    list: LinkedDevice[];
    limits: DeviceLimits | null;
  } | null>(null);
  const [servers, setServers] = useState<{
    available: boolean;
    list: FoundServer[];
  } | null>(null);
  const [looking, setLooking] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const sendTo = useRef<MirrorPhone | null>(null);

  const load = useCallback(() => {
    setError(null);
    void mirrorApi.phones().then((r) => {
      if (!r.ok) return setError(r.error ?? tr('phone.loadError'));
      const list = r.data ?? [];
      setDevices(list);
      setPicked((cur) => {
        if (cur && list.some((p) => p.id === cur)) return cur;
        const last = loadPick();
        if (last && list.some((p) => p.id === last)) return last;
        return list.find(isOnline)?.id ?? list[0]?.id ?? null;
      });
    });
  }, []);
  useEffect(() => {
    load();
    // Keep online and battery fresh while it's open.
    const id = window.setInterval(load, 30_000);
    return () => window.clearInterval(id);
  }, [load]);

  const choose = (id: string) => {
    savePick(id);
    setPicked(id);
  };

  // ---------- per-device actions ----------

  const rename = (d: MirrorPhone) =>
    setDialog({
      kind: 'prompt',
      title: tr('ds.renameTitle', { name: d.name }),
      label: tr('ds.renameLabel'),
      initial: d.name,
      confirm: tr('ld.save'),
      onSubmit: (name) => {
        void devicesApi.rename(d.id, name).then((r) => {
          if (!r.ok) return toast('error', tr('ld.renameFail'), r.error);
          toast('success', tr('ld.renamed', { name: r.data.name }));
          load();
        });
      },
    });

  const ring = async (d: MirrorPhone) => {
    const r = await linkApi.action(d.id, { kind: 'ring' });
    if (!r.ok) return toast('error', tr('link.ringFailed', { device: d.name }), r.error);
    toast('success', tr('link.ringing', { device: d.name }));
  };

  const sendLink = (d: MirrorPhone) =>
    setDialog({
      kind: 'prompt',
      title: tr('ds.sendLinkTitle', { name: d.name }),
      label: tr('link.urlPlaceholder'),
      confirm: tr('ds.send'),
      onSubmit: (url) => {
        if (!webUrl(url)) return toast('error', tr('link.badUrl'));
        void openUrlOnPhone(
          {
            id: d.id,
            name: d.name,
            platform: d.platform,
            online: isOnline(d),
            route: '',
          },
          url,
        );
      },
    });

  const pickFile = (d: MirrorPhone) => {
    sendTo.current = d;
    fileRef.current?.click();
  };

  // Upload to <Received>/<device>/Sent here, then ask the device to fetch it.
  const sendFiles = async (d: MirrorPhone, list: File[]) => {
    if (!list.length || sending) return;
    const box = await receivedApi.outbox(d.id);
    if (!box.ok) return toast('error', tr('ds.sendFailed', { name: d.name }), box.error);
    const total = list.reduce((n, f) => n + f.size, 0);
    setSending({
      device: d.name,
      name: list.length === 1 ? list[0].name : tr('ds.nFiles', { n: list.length }),
      loaded: 0,
      total,
    });
    const up = await upload(box.data.root, box.data.path, list, (p) => setSending((s) => s && { ...s, loaded: p.loaded, total: p.total }));
    setSending(null);
    if (!up.ok) return toast('error', tr('ds.sendFailed', { name: d.name }), up.error);
    let ok = 0;
    for (const path of up.data.paths ?? []) {
      const r = await linkApi.action(d.id, {
        kind: 'file',
        root: box.data.root,
        path,
        open: false,
      });
      if (r.ok) ok++;
    }
    if (ok) toast('success', tr('ds.sent', { n: ok, name: d.name }));
    else toast('error', tr('ds.sendFailed', { name: d.name }));
  };

  const unlink = async (d: MirrorPhone) => {
    const ok = await confirmDialog({
      title: tr('ld.unlinkTitle', { name: d.name }),
      message: tr('ld.unlinkMsg'),
      confirmLabel: tr('ld.unlink'),
      danger: true,
    });
    if (!ok) return;
    const r = await devicesApi.unlink(d.id);
    if (!r.ok && r.status !== 404) return toast('error', tr('ld.unlinkFail'), r.error);
    toast('success', tr('ld.unlinked', { name: d.name }));
    load();
  };

  const openSettings = () => openApp('settings', { props: { section: 'devices' } });

  const startLinking = async () => {
    const [l, lim] = await Promise.all([devicesApi.list(), devicesApi.limits()]);
    if (!l.ok) return toast('error', tr('ld.loadFail'), l.error);
    setLinking({ list: l.data ?? [], limits: lim.ok ? lim.data : null });
  };

  const lookForServers = async () => {
    setLooking(true);
    const r = await findApi.discover();
    setLooking(false);
    if (!r.ok) return toast('error', tr('find.failed'), r.error);
    setServers({ available: r.data.available, list: r.data.servers ?? [] });
  };

  const menuItems = (d: MirrorPhone): MenuItem[] => [
    { label: tr('ds.menu.rename'), icon: 'edit', onClick: () => rename(d) },
    {
      label: tr('ds.menu.ring'),
      icon: 'bell',
      disabled: d.platform !== 'android',
      onClick: () => void ring(d),
    },
    {
      label: tr('ds.menu.sendFile'),
      icon: 'upload',
      disabled: !!sending,
      onClick: () => pickFile(d),
    },
    {
      label: tr('ds.menu.sendLink'),
      icon: 'globe',
      onClick: () => sendLink(d),
    },
    {
      label: tr('ds.menu.settings'),
      icon: 'settings',
      divider: true,
      onClick: openSettings,
    },
    {
      label: tr('ds.menu.unlink'),
      icon: 'trash',
      danger: true,
      divider: true,
      onClick: () => void unlink(d),
    },
  ];

  const openMenu = (e: MouseEvent, d: MirrorPhone) => {
    e.preventDefault();
    e.stopPropagation();
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    setMenu({
      x: e.type === 'contextmenu' ? e.clientX : r.left,
      y: e.type === 'contextmenu' ? e.clientY : r.bottom + 4,
      d,
    });
  };

  const menuButton = (d: MirrorPhone) => (
    <button type="button" className="phone-tool ds-more" title={t('ds.menu.title')} aria-label={t('ds.menu.title')} aria-haspopup="menu" onClick={(e) => openMenu(e, d)}>
      <Icon name="more" size={17} />
    </button>
  );

  // ---------- views ----------

  const overlays = (
    <>
      <input
        ref={fileRef}
        type="file"
        multiple
        hidden
        onChange={(e) => {
          const list = Array.from(e.target.files ?? []);
          e.target.value = '';
          if (sendTo.current) void sendFiles(sendTo.current, list);
        }}
      />
      {menu && <ContextMenu x={menu.x} y={menu.y} items={menuItems(menu.d)} onClose={() => setMenu(null)} />}
      {dialog && <Dialog spec={dialog} onClose={() => setDialog(null)} />}
      {linking && (
        <LinkSheet
          known={new Set(linking.list.map((d) => d.id))}
          mine={linking.list}
          used={linking.limits?.used ?? linking.list.length}
          limit={linking.limits?.limit ?? 0}
          onUnlink={async (d) => {
            const ok = await confirmDialog({
              title: tr('ld.unlinkTitle', { name: d.name }),
              message: tr('ld.unlinkMsg'),
              confirmLabel: tr('ld.unlink'),
              danger: true,
            });
            if (!ok) return false;
            const r = await devicesApi.unlink(d.id);
            if (!r.ok && r.status !== 404) {
              toast('error', tr('ld.unlinkFail'), r.error);
              return false;
            }
            load();
            return true;
          }}
          onChanged={load}
          onClose={() => {
            setLinking(null);
            load();
          }}
        />
      )}
    </>
  );

  if (error && !devices) {
    return (
      <div className="phone-app ds-app">
        <div className="phone-empty">
          <Icon name="alert" size={30} />
          <b>{t('phone.loadError')}</b>
          <p className="muted small">{error}</p>
          <button type="button" onClick={load}>
            {t('phone.retry')}
          </button>
        </div>
      </div>
    );
  }
  if (!devices) {
    return (
      <div className="phone-app ds-app">
        <div className="phone-empty">
          <span className="phone-spin" />
          <span className="muted">{t('ds.loading')}</span>
        </div>
      </div>
    );
  }
  if (devices.length === 0) {
    return (
      <div className="phone-app ds-app">
        <div className="phone-empty">
          <span className="phone-empty-ico">
            <Icon name="devices" size={30} />
          </span>
          <b>{t('ds.none.title')}</b>
          <p className="muted small">{t('ds.none.body', { page: t('set.devices') })}</p>
          <div className="ds-actions">
            <button type="button" onClick={() => void startLinking()}>
              <Icon name="plus" size={14} /> {t('ds.linkNew')}
            </button>
            <button type="button" className="neutral" onClick={openSettings}>
              {t('ds.openPage', { page: t('set.devices') })}
            </button>
            <button type="button" className="neutral" onClick={load}>
              {t('phone.retry')}
            </button>
          </div>
        </div>
        {overlays}
      </div>
    );
  }

  const device = devices.find((p) => p.id === picked) ?? null;
  const why = device ? cantStream(device) : '';

  return (
    <div className="ds-wrap">
      <div className="ds-app">
        <aside className="ds-side" aria-label={t('ds.listLabel')}>
          <div className="ds-side-head">
            <b>{t('ds.yours')}</b>
            <button type="button" className="icon-btn neutral" title={t('phone.retry')} aria-label={t('phone.retry')} onClick={load}>
              <Icon name="restart" size={14} />
            </button>
          </div>
          <div className="ds-list" role="listbox" aria-label={t('ds.listLabel')}>
            {devices.map((d) => {
              const form = deviceForm(d);
              return (
                <div
                  key={d.id}
                  role="option"
                  aria-selected={d.id === picked}
                  tabIndex={0}
                  className={`ds-item${d.id === picked ? ' active' : ''}`}
                  onClick={() => choose(d.id)}
                  onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), choose(d.id))}
                  onContextMenu={(e) => openMenu(e, d)}
                >
                  <span className="ds-item-ico">
                    <Icon name={FORM_ICON[form]} size={18} />
                    <span className={`phone-dot${isOnline(d) ? ' live' : ''}`} />
                  </span>
                  <span className="ds-item-text">
                    <b title={d.name}>{d.name}</b>
                    <span className="muted small">
                      {t(`ds.form.${form}`)}
                      {d.model ? ` · ${d.model}` : ''}
                    </span>
                    <span className="muted small">{statusLine(t, d)}</span>
                  </span>
                  <button type="button" className="icon-btn neutral ds-item-more" title={t('ds.menu.title')} aria-label={t('ds.menu.title')} aria-haspopup="menu" onClick={(e) => openMenu(e, d)}>
                    <Icon name="more" size={14} />
                  </button>
                </div>
              );
            })}
          </div>
          <button type="button" className="small ds-link-new" onClick={() => void startLinking()}>
            <Icon name="plus" size={14} /> {t('ds.linkNew')}
          </button>

          <div className="ds-side-head ds-servers-head">
            <b>{t('ds.servers')}</b>
            <button type="button" className="small neutral" onClick={() => void lookForServers()} disabled={looking}>
              {looking ? t('ds.looking') : t('ds.look')}
            </button>
          </div>
          {servers &&
            (!servers.available ? (
              <p className="muted small ds-servers-note">{t('ds.serversOff')}</p>
            ) : servers.list.length === 0 ? (
              <p className="muted small ds-servers-note">{t('ds.serversNone')}</p>
            ) : (
              <div className="ds-list">
                {servers.list.map((s) => (
                  <ServerRow key={s.nocap_id || s.url} s={s} />
                ))}
                <p className="muted small ds-servers-note">{t('ds.serversHint')}</p>
              </div>
            ))}
        </aside>

        <section className="ds-main">
          {sending && (
            <div className="ds-progress" role="progressbar" aria-valuemin={0} aria-valuemax={sending.total || 1} aria-valuenow={sending.loaded}>
              <Icon name="upload" size={14} />
              <span className="ds-progress-text">
                {t('ds.sending', {
                  name: sending.name,
                  device: sending.device,
                })}
              </span>
              <span className="ds-progress-bar">
                <span
                  style={{
                    width: `${sending.total ? Math.round((sending.loaded / sending.total) * 100) : 0}%`,
                  }}
                />
              </span>
              <span className="muted small">{sending.total ? Math.round((sending.loaded / sending.total) * 100) : 0}%</span>
            </div>
          )}
          {!device ? (
            <div className="phone-empty">
              <span className="phone-empty-ico">
                <Icon name="devices" size={30} />
              </span>
              <b>{t('ds.pick')}</b>
            </div>
          ) : why ? (
            <Needs d={device} why={why} menu={menuButton(device)} onSettings={openSettings} />
          ) : (
            <Mirror key={device.id} phone={device} form={deviceForm(device)} menu={menuButton(device)} onGone={load} />
          )}
        </section>
        {overlays}
      </div>
    </div>
  );
}

/** A device that can't show its screen here: what it would take. */
function Needs({ d, why, menu, onSettings }: { d: MirrorPhone; why: 'platform'; menu: React.ReactNode; onSettings: () => void }) {
  const t = useT();
  const form = deviceForm(d);
  return (
    <div className="phone-app">
      <div className="phone-top">
        <span className={`phone-dot${isOnline(d) ? ' live' : ''}`} />
        <span className="phone-top-text">
          <b title={d.name}>{d.name}</b>
          <span className="muted small">{statusLine(t, d)}</span>
        </span>
        <span className="spacer" />
        {menu}
      </div>
      <div className="phone-empty">
        <span className="phone-empty-ico">
          <Icon name={FORM_ICON[form]} size={30} />
        </span>
        <b>{why === 'platform' && t('ds.needs.platformTitle')}</b>
        <p className="muted small">{t('ds.needs.platformBody')}</p>
        <div className="ds-actions">
          <button type="button" className="neutral" onClick={onSettings}>
            {t('ds.openPage', { page: t('set.devices') })}
          </button>
        </div>
      </div>
    </div>
  );
}

/** Another NoCap server on this network: opens its own page in a new tab. */
function ServerRow({ s }: { s: FoundServer }) {
  const t = useT();
  const url = /^https?:\/\//i.test(s.url) ? s.url : '';
  return (
    <div className="ds-item ds-server">
      <span className="ds-item-ico">
        <Icon name="monitor" size={18} />
      </span>
      <span className="ds-item-text">
        <b title={s.name || s.hostname}>{s.name || s.hostname}</b>
        <span className="muted small">{s.setup === 'new' ? t('ds.serverNew') : (s.addresses[0] ?? s.hostname)}</span>
      </span>
      {url && (
        <button type="button" className="small neutral" title={t('ds.serverOpenHint')} onClick={() => window.open(url, '_blank', 'noopener')}>
          {t('ds.serverOpen')}
        </button>
      )}
    </div>
  );
}
