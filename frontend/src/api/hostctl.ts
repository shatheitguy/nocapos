import { api } from './client';

export interface Toggle {
  supported: boolean;
  enabled: boolean;
  reason?: string;
}

export interface IPv4Info {
  method: 'auto' | 'manual' | 'disabled' | 'unknown';
  addresses: string[];
  gateway?: string;
  dns: string[];
  editable: boolean;
}

export interface IPv4Config {
  method: 'auto' | 'manual';
  address?: string;
  gateway?: string;
  dns: string[];
}

export interface WiFiNetwork {
  ssid: string;
  signal: number;
  security: string;
  secure: boolean;
  in_use: boolean;
  saved: boolean;
}

export interface NetInterface {
  name: string;
  kind: 'wifi' | 'ethernet' | 'other';
  up: boolean;
  state?: string;
  connection?: string;
  mac?: string;
  addresses: string[];
  ipv4?: IPv4Info;
}

export interface NetworkState {
  manager: 'networkmanager' | 'windows' | 'none';
  wifi: Toggle;
  networking: Toggle;
  interfaces: NetInterface[];
}

export interface PowerInfo {
  supported: boolean;
  reason?: string;
}

export const hostApi = {
  network: () => api<NetworkState>('/api/v1/system/network'),
  setNetwork: (body: { wifi?: boolean; networking?: boolean }) =>
    api<NetworkState>('/api/v1/system/network', { method: 'POST', body }),
  power: () => api<PowerInfo>('/api/v1/system/power'),
  wifiList: (rescan: boolean) => api<WiFiNetwork[]>(`/api/v1/system/wifi${rescan ? '?rescan=1' : ''}`),
  wifiConnect: (ssid: string, password?: string) => api<NetworkState>('/api/v1/system/wifi/connect', { method: 'POST', body: { ssid, password } }),
  wifiDisconnect: (device: string) => api<NetworkState>('/api/v1/system/wifi/disconnect', { method: 'POST', body: { device } }),
  setIPv4: (connection: string, cfg: IPv4Config) =>
    api<{ token: string; revert_in: number }>('/api/v1/system/network/ipv4', { method: 'POST', body: { connection, ...cfg } }),
  keepIPv4: (token: string) => api('/api/v1/system/network/ipv4/keep', { method: 'POST', body: { token } }),
  revertIPv4: (token: string) => api('/api/v1/system/network/ipv4/revert', { method: 'POST', body: { token } }),
  powerAction: (action: 'reboot' | 'shutdown') => api<{ status: string }>('/api/v1/system/power', { method: 'POST', body: { action } }),
};
