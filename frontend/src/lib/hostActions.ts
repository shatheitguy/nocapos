// Host actions shared by the Control Center and Settings: restart / shut down
// and the network switches. Each risky action asks first.
import { create } from 'zustand';
import { hostApi, type IPv4Config, type NetworkState, type WiFiJoin } from '../api/hostctl';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import { tr } from '../i18n';

/** Full-screen state after a power action (rendered by <PowerOverlay>). */
export const usePowerState = create<{ action: 'reboot' | 'shutdown' | null }>(() => ({ action: null }));

export async function powerHost(action: 'reboot' | 'shutdown'): Promise<void> {
  const reboot = action === 'reboot';
  const ok = await confirmDialog({
    title: reboot ? tr('host.rebootTitle') : tr('host.shutdownTitle'),
    message: reboot ? tr('host.rebootMsg') : tr('host.shutdownMsg'),
    confirmLabel: reboot ? tr('host.reboot') : tr('host.shutdown'),
    danger: true,
  });
  if (!ok) return;
  const r = await hostApi.powerAction(action);
  if (!r.ok) {
    toast('error', reboot ? tr('host.rebootFail') : tr('host.shutdownFail'), r.error);
    return;
  }
  usePowerState.setState({ action });
}


/**
 * Flip Wi-Fi or all networking. Turning one off warns first, because the
 * browser may be reaching NoCapOS over that very connection.
 */
export async function setNetwork(kind: 'wifi' | 'networking', on: boolean): Promise<NetworkState | null> {
  if (!on) {
    const ok = await confirmDialog({
      title: kind === 'wifi' ? tr('host.wifiOffTitle') : tr('host.netOffTitle'),
      message: kind === 'wifi' ? tr('host.wifiOffMsg') : tr('host.netOffMsg'),
      confirmLabel: tr('host.turnOff'),
      danger: true,
    });
    if (!ok) return null;
  }
  const r = await hostApi.setNetwork({ [kind]: on });
  if (!r.ok) {
    const title = kind === 'wifi' ? (on ? tr('host.wifiOnFail') : tr('host.wifiOffFail')) : on ? tr('host.netOnFail') : tr('host.netOffFail');
    toast('error', title, r.error);
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
  const desc = cfg.method === 'auto' ? tr('host.ipDhcp') : tr('host.ipStatic', { address: cfg.address });
  const go = await confirmDialog({
    title: tr('host.ipTitle', { iface }),
    message: tr('host.ipMsg', { iface, desc }),
    confirmLabel: tr('host.apply'),
  });
  if (!go) return false;
  const r = await hostApi.setIPv4(connection, cfg);
  if (!r.ok) {
    toast('error', tr('host.ipFail'), r.error);
    return false;
  }
  await new Promise((res) => setTimeout(res, 4000)); // give NetworkManager time to re-apply
  const keep = await confirmDialog({
    title: tr('host.keepTitle'),
    message: tr('host.keepMsg', { iface }),
    confirmLabel: tr('host.keep'),
    cancelLabel: tr('host.revert'),
    timeoutSec: Math.max(10, r.data.revert_in - 8),
  });
  if (keep) {
    const k = await hostApi.keepIPv4(r.data.token);
    if (k.ok) toast('success', tr('host.kept'));
    else toast('error', tr('host.tooLate'), k.error);
    return k.ok;
  }
  await hostApi.revertIPv4(r.data.token);
  toast('info', tr('host.reverted'));
  return false;
}

/** Join a network; on failure the error says why (NetworkManager's own words included). */
export async function joinWiFi(j: WiFiJoin): Promise<{ state: NetworkState | null; error?: string }> {
  const r = await hostApi.wifiConnect(j);
  if (!r.ok) {
    const error = r.error ?? tr('host.joinErr');
    toast('error', tr('host.joinFail', { ssid: j.ssid }), error);
    return { state: null, error };
  }
  toast('success', tr('host.joined', { ssid: j.ssid }));
  return { state: r.data };
}
