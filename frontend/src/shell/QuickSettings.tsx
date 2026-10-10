import { useEffect, useRef, useState, type RefObject } from 'react';
import { UserAvatar } from '../components/UserAvatar';
import { logout } from '../api/client';
import { hostApi, type NetworkState } from '../api/hostctl';
import type { User } from '../api/types';
import { openApp } from '../apps/meta';
import { Icon, type IconName } from '../components/Icon';
import { powerHost, setNetwork } from '../lib/hostActions';
import { playCue } from '../lib/uiSound';
import { useDismiss } from '../lib/hooks';
import { IPv4Summary, WifiList } from '../apps/NetworkParts';
import { usePrefs } from '../state/prefs';
import { useSystem } from '../state/system';

/** Is the desktop currently showing the light look? */
function isLight(theme: string): boolean {
  if (theme === 'light') return true;
  if (theme === 'dark') return false;
  return !window.matchMedia?.('(prefers-color-scheme: dark)').matches;
}

function Tile({ icon, title, sub, on, disabled, onClick, onMore }: {
  icon: IconName; title: string; sub: string; on: boolean; disabled?: boolean; onClick: () => void; onMore?: () => void;
}) {
  return (
    <div className={`cc-tile ${on ? 'on' : ''} ${disabled ? 'disabled' : ''}`}>
      <button type="button" className="cc-tile-main" onClick={onClick} disabled={disabled} aria-pressed={on} title={disabled ? sub : undefined}>
        <span className="cc-tile-icon">
          <Icon name={icon} size={17} />
        </span>
        <span className="cc-tile-text">
          <b>{title}</b>
          <span>{sub}</span>
        </span>
      </button>
      {onMore && (
        <button type="button" className="cc-tile-more" onClick={onMore} aria-label={`${title} settings`} title={`${title} settings`}>
          <Icon name="chevronRight" size={14} />
        </button>
      )}
    </div>
  );
}

let lastVolume = 50; // restored when un-muting from the Control Center

function CcSlider({ icon, label, value, min = 0, muted, onChange, onIcon, onCommit }: {
  icon: IconName; label: string; value: number; min?: number; muted?: boolean;
  onChange: (v: number) => void; onIcon?: () => void; onCommit?: () => void;
}) {
  const pct = ((value - min) / (100 - min)) * 100;
  return (
    <div className={`cc-slider ${muted ? 'muted' : ''}`}>
      <span className="cc-slider-label">{label}</span>
      <div className="cc-slider-track">
        <button type="button" className="cc-slider-icon" onClick={onIcon} disabled={!onIcon}
          aria-label={onIcon ? (muted ? `Unmute ${label.toLowerCase()}` : `Mute ${label.toLowerCase()}`) : label} title={onIcon ? (muted ? 'Unmute' : 'Mute') : undefined}>
          <Icon name={icon} size={15} />
        </button>
        <input type="range" min={min} max={100} step={1} value={value} aria-label={label}
          style={{ backgroundImage: `linear-gradient(90deg, var(--accent) ${pct}%, transparent ${pct}%)` }}
          onChange={(e) => onChange(Number(e.target.value))} onPointerUp={onCommit} onKeyUp={onCommit} />
        <span className="cc-slider-value">{muted ? 'Off' : `${value}%`}</span>
      </div>
    </div>
  );
}

/** Control Center: quick switches, brightness & sound, and session / power actions. */
export function QuickSettings({
  user,
  anchor,
  onClose,
  onLock,
  showActions = true,
}: {
  user: User;
  anchor: RefObject<HTMLButtonElement | null>;
  onClose: () => void;
  onLock: () => void;
  /** Windows style shows session & power buttons here; macOS style keeps them in the N menu. */
  showActions?: boolean;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useDismiss(true, onClose, ref, anchor);
  const prefs = usePrefs();
  const host = useSystem((s) => s.info?.host.hostname);
  const isAdmin = user.role === 'admin';
  const [net, setNet] = useState<NetworkState | null>(null);
  const [busy, setBusy] = useState<'wifi' | 'networking' | null>(null);
  // Detail views open inside the panel (Wi-Fi networks, network info).
  const [view, setView] = useState<'main' | 'wifi' | 'network'>('main');

  const loadNet = () => void hostApi.network().then((r) => r.ok && setNet(r.data));
  useEffect(() => {
    if (isAdmin) loadNet();
  }, [isAdmin]);

  const openSettings = (section?: string) => {
    openApp('settings', section ? { props: { section } } : {});
    onClose();
  };

  const flip = async (kind: 'wifi' | 'networking') => {
    if (!net || busy) return;
    const t = kind === 'wifi' ? net.wifi : net.networking;
    if (!t.supported) return setView(kind === 'wifi' ? 'wifi' : 'network');
    setBusy(kind);
    const next = await setNetwork(kind, !t.enabled);
    if (next) setNet(next);
    setBusy(null);
  };

  const classic = prefs.uiTheme === 'classic';
  const light = classic && isLight(prefs.theme);
  const wired = net?.interfaces.find((i) => i.kind === 'ethernet' && i.addresses.length);
  const wireless = net?.interfaces.find((i) => i.kind === 'wifi');

  if (view !== 'main' && net) {
    const wifi = view === 'wifi';
    return (
      <div ref={ref} className="popover quick-settings control-center" role="dialog" aria-label={wifi ? 'Wi-Fi' : 'Network'}>
        <div className="cc-detail-head">
          <button type="button" className="icon-btn neutral" aria-label="Back" onClick={() => setView('main')}>
            <Icon name="chevronLeft" size={16} />
          </button>
          <b>{wifi ? 'Wi-Fi' : 'Network'}</b>
          <span className="spacer" />
          {wifi && net.wifi.supported && (
            <label className="toggle row cc-detail-switch">
              <input type="checkbox" role="switch" aria-label="Wi-Fi" checked={net.wifi.enabled} disabled={!!busy} onChange={() => void flip('wifi')} />
            </label>
          )}
        </div>
        <div className="cc-detail-body">
          {wifi ? (
            !net.wifi.supported ? (
              <p className="net-note">{net.wifi.reason}</p>
            ) : !net.wifi.enabled ? (
              <p className="net-note">Wi-Fi is off.</p>
            ) : (
              <WifiList device={wireless?.name} onChanged={loadNet} />
            )
          ) : (
            <div className="cc-ifaces">
              {net.interfaces.length === 0 && <p className="net-note">No network adapters found.</p>}
              {net.interfaces.map((i) => (
                <div key={i.name} className="cc-iface">
                  <div className="cc-iface-head">
                    <Icon name={i.kind === 'wifi' ? 'wifi' : 'ethernet'} size={15} />
                    <b>{i.name}</b>
                    <span className={`status-dot ${i.up && (i.ipv4?.addresses.length || i.addresses.length) ? 'on' : 'off'}`} />
                    <span className="muted small">{i.connection || i.state || (i.up ? 'up' : 'down')}</span>
                  </div>
                  <IPv4Summary iface={i} />
                </div>
              ))}
            </div>
          )}
        </div>
        <button type="button" className="ghost small cc-detail-foot" onClick={() => openSettings('network')}>
          Network Settings…
        </button>
      </div>
    );
  }

  return (
    <div ref={ref} className="popover quick-settings control-center" role="dialog" aria-label="Control Center">
      <div className="qs-user">
        <UserAvatar name={user.username} size="lg" />
        <div>
          <b>{user.username}</b>
          <div className="muted small">
            {user.role === 'admin' ? 'Administrator' : 'User'}
            {host ? ` · ${host}` : ''}
          </div>
        </div>
      </div>

      <div className="cc-grid">
        {isAdmin && (
          <>
            <Tile
              icon="wifi"
              title="Wi-Fi"
              on={!!net?.wifi.enabled}
              disabled={!net}
              sub={busy === 'wifi' ? 'Switching…' : !net ? 'Checking…' : net.wifi.enabled ? wireless?.connection || 'On' : net.wifi.supported ? 'Off' : 'Not available'}
              onClick={() => void flip('wifi')}
              onMore={() => setView('wifi')}
            />
            <Tile
              icon="ethernet"
              title="Network"
              on={!!net?.networking.enabled}
              disabled={!net}
              sub={busy === 'networking' ? 'Switching…' : !net ? 'Checking…' : net.networking.enabled ? wired?.name || 'Connected' : 'Off'}
              onClick={() => void flip('networking')}
              onMore={() => setView('network')}
            />
          </>
        )}
        <Tile
          icon={light ? 'sun' : 'moon'}
          title="Light mode"
          on={light}
          disabled={!classic}
          sub={!classic ? `${prefs.uiTheme === 'glass' ? 'Glass' : 'Cyber-Deck'} is always dark` : light ? 'On' : 'Off'}
          onClick={() => prefs.set({ theme: light ? 'dark' : 'light' })}
        />
        <Tile
          icon="moon"
          title="Focus"
          on={prefs.focusMode}
          sub={prefs.focusMode ? 'Notifications silenced' : 'Off'}
          onClick={() => prefs.set({ focusMode: !prefs.focusMode })}
          onMore={() => openSettings('focus')}
        />
      </div>

      <div className="cc-sliders">
        <CcSlider
          icon={prefs.brightness < 60 ? 'moon' : 'sun'}
          label="Brightness"
          value={prefs.brightness}
          min={30}
          onChange={(brightness) => prefs.set({ brightness })}
        />
        <CcSlider
          icon="sound"
          label="Sound"
          muted={!prefs.uiSounds || prefs.soundVolume === 0}
          value={prefs.uiSounds ? prefs.soundVolume : 0}
          onIcon={() => {
            // Speaker icon = mute / unmute, restoring the previous level.
            if (prefs.uiSounds && prefs.soundVolume > 0) {
              lastVolume = prefs.soundVolume;
              prefs.set({ uiSounds: false });
            } else prefs.set({ uiSounds: true, soundVolume: prefs.soundVolume || lastVolume || 50 });
          }}
          onChange={(v) => prefs.set({ soundVolume: v, uiSounds: v > 0 })}
          onCommit={() => playCue('click', true)}
        />
      </div>

      {showActions && (
      <div className={`cc-actions ${isAdmin ? 'admin' : ''}`}>
        <button type="button" className="ghost" onClick={() => openSettings()}>
          <Icon name="settings" size={16} /> Settings
        </button>
        <button type="button" className="ghost" onClick={onLock}>
          <Icon name="lock" size={16} /> Lock
        </button>
        <button type="button" className="ghost" onClick={() => void logout()}>
          <Icon name="logout" size={16} /> Sign out
        </button>
        {isAdmin && (
          <>
            <button type="button" className="ghost" onClick={() => void powerHost('reboot')}>
              <Icon name="restart" size={16} /> Restart
            </button>
            <button type="button" className="ghost danger" onClick={() => void powerHost('shutdown')}>
              <Icon name="power" size={16} /> Shut down
            </button>
          </>
        )}
      </div>
      )}
    </div>
  );
}
