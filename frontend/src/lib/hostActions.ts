// Host actions shared by the Control Center and Settings: restart / shut down
// and the network switches. Each risky action asks first.
import { create } from 'zustand';
import { hostApi, type IPv4Config, type NetworkState, type WiFiJoin } from '../api/hostctl';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';

/** Full-screen state after a power action (rendered by <PowerOverlay>). */
export const usePowerState = create<{ action: 'reboot' | 'shutdown' | null }>(() => ({ action: null }));

export async function powerHost(action: 'reboot' | 'shutdown'): Promise<void> {
  const reboot = action === 'reboot';
  const ok = await confirmDialog({
    title: reboot ? 'Restart this machine?' : 'Shut down this machine?',
    message: reboot
      ? 'Every app, terminal and remote session will close. NoCapOS is back in a minute or two, and this page reconnects on its own.'
      : "Every app and session will close and the machine powers off. Heads up: you'll need physical access (or your host's console) to turn it back on.",
    confirmLabel: reboot ? 'Restart' : 'Shut down',
    danger: true,
  });
  if (!ok) return;
  const r = await hostApi.powerAction(action);
  if (!r.ok) {
    toast('error', reboot ? "Couldn't restart" : "Couldn't shut down", r.error);
    return;
  }
  usePowerState.setState({ action });
}

/** Wait for the server to go away and come back after a restart, then reload. */
export function waitForReturn(onBack: () => void): () => void {
  let wentDown = false;
  let stopped = false;
  const tick = async () => {
    if (stopped) return;
    try {
      const res = await fetch('/healthz', { cache: 'no-store' });
      if (res.ok && wentDown) return onBack();
      if (!res.ok) wentDown = true;
    } catch {
      wentDown = true;
    }
    window.setTimeout(tick, 3000);
  };
  window.setTimeout(tick, 4000);
  return () => {
    stopped = true;
  };
}

/**
 * Flip Wi-Fi or all networking. Turning one off warns first, because the
 * browser may be reaching NoCapOS over that very connection.
 */
export async function setNetwork(kind: 'wifi' | 'networking', on: boolean): Promise<NetworkState | null> {
  if (!on) {
    const ok = await confirmDialog({
      title: kind === 'wifi' ? 'Turn off Wi-Fi?' : 'Turn off all networking?',
      message:
        kind === 'wifi'
          ? "If you're reaching NoCapOS over this machine's Wi-Fi, this page will lose its connection and you'll need local access to turn it back on."
          : "This disconnects every network on this machine. If you're using NoCapOS remotely, you'll lose access until someone turns networking back on locally.",
      confirmLabel: 'Turn off',
      danger: true,
    });
    if (!ok) return null;
  }
  const r = await hostApi.setNetwork({ [kind]: on });
  if (!r.ok) {
    toast('error', `Couldn't turn ${kind === 'wifi' ? 'Wi-Fi' : 'networking'} ${on ? 'on' : 'off'}`, r.error);
    return null;
  }
  return r.data;
}

/**
 * Change an interface's IPv4 settings, then ask "Keep these settings?". If
 * nobody confirms (e.g. the page lost its connection), the server puts the
 * old settings back by itself.
 */
export async function applyIPv4(iface: string, connection: string, cfg: IPv4Config): Promise<boolean> {
  const desc = cfg.method === 'auto' ? 'get its address automatically (DHCP)' : `use the static address ${cfg.address}`;
  const go = await confirmDialog({
    title: `Change ${iface} settings?`,
    message: `${iface} will ${desc}. If this page reaches NoCapOS through ${iface}, it may disconnect for a moment. You'll be asked to keep the change — if you don't, it's undone automatically.`,
    confirmLabel: 'Apply',
  });
  if (!go) return false;
  const r = await hostApi.setIPv4(connection, cfg);
  if (!r.ok) {
    toast('error', "Couldn't change the network settings", r.error);
    return false;
  }
  await new Promise((res) => setTimeout(res, 4000)); // give NetworkManager time to re-apply
  const keep = await confirmDialog({
    title: 'Keep these network settings?',
    message: `${iface} now uses the new settings. If anything stopped working, choose Revert — or just wait and they'll be undone.`,
    confirmLabel: 'Keep',
    cancelLabel: 'Revert',
    timeoutSec: Math.max(10, r.data.revert_in - 8),
  });
  if (keep) {
    const k = await hostApi.keepIPv4(r.data.token);
    if (k.ok) toast('success', 'Bet — network settings saved');
    else toast('error', 'Too late — the change was already undone', k.error);
    return k.ok;
  }
  await hostApi.revertIPv4(r.data.token);
  toast('info', 'No worries — your previous network settings are back');
  return false;
}

/** Join a network; on failure the error says why (NetworkManager's own words included). */
export async function joinWiFi(j: WiFiJoin): Promise<{ state: NetworkState | null; error?: string }> {
  const r = await hostApi.wifiConnect(j);
  if (!r.ok) {
    const error = r.error ?? 'Couldn’t join the network';
    toast('error', `Wi-Fi's being sus — couldn't join ${j.ssid}`, error);
    return { state: null, error };
  }
  toast('success', `You're on ${j.ssid} — all set`);
  return { state: r.data };
}
