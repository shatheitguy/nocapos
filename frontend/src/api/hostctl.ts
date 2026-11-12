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
  /** 802.1X (WPA/WPA2/WPA3-Enterprise): sign in with an EAP method. */
  enterprise?: boolean;
}

export type EAPMethod = 'peap' | 'ttls' | 'tls' | 'pwd' | 'leap';
export type EAPPhase2 = 'mschapv2' | 'pap' | 'gtc' | 'md5';
export type CAMode = 'system' | 'file' | 'none';

/** The non-secret part of a saved enterprise sign-in. */
export interface EAPInfo {
  method: string;
  phase2?: string;
  identity?: string;
  anonymous_identity?: string;
  domain_suffix_match?: string;
  ca: CAMode;
  client_cert: boolean;
  private_key: boolean;
}

/** An enterprise sign-in to save. Certificates are base64 file contents; on an edit, blanks keep what's saved. */
export interface EAPConfig {
  method: EAPMethod;
  phase2?: EAPPhase2;
  identity: string;
  anonymous_identity?: string;
  password?: string;
  domain_suffix_match?: string;
  ca: CAMode;
  ca_cert?: string;
  client_cert?: string;
  private_key?: string;
  private_key_password?: string;
}

/** A Wi-Fi profile saved on the server. */
export interface SavedWiFi {
  uuid: string;
  name: string;
  ssid: string;
  security: string;
  enterprise: boolean;
  eap?: EAPInfo;
  autoconnect: boolean;
  priority: number;
  last_connected: number;
  active: boolean;
  device?: string;
  hidden: boolean;
  shareable: boolean;
  editable: boolean;
}

export interface WiFiShare {
  ssid: string;
  password: string;
  type: 'WPA' | 'nopass';
  hidden: boolean;
}

/** Joining a network: hidden ones are joined by name. */
export interface WiFiJoin {
  ssid: string;
  password?: string;
  hidden?: boolean;
  device?: string;
  eap?: EAPConfig;
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
  wifiConnect: (j: WiFiJoin) => api<NetworkState>('/api/v1/system/wifi/connect', { method: 'POST', body: j }),
  setHostname: (hostname: string) => api<{ hostname: string }>('/api/v1/system/hostname', { method: 'PUT', body: { hostname } }),
  wifiDisconnect: (device: string) => api<NetworkState>('/api/v1/system/wifi/disconnect', { method: 'POST', body: { device } }),
  setIPv4: (connection: string, cfg: IPv4Config) =>
    api<{ token: string; revert_in: number }>('/api/v1/system/network/ipv4', { method: 'POST', body: { connection, ...cfg } }),
  keepIPv4: (token: string) => api('/api/v1/system/network/ipv4/keep', { method: 'POST', body: { token } }),
  revertIPv4: (token: string) => api('/api/v1/system/network/ipv4/revert', { method: 'POST', body: { token } }),
  wifiSaved: () => api<SavedWiFi[]>('/api/v1/system/wifi/saved'),
  wifiSavedConnect: (uuid: string) => api<NetworkState>(`/api/v1/system/wifi/saved/${encodeURIComponent(uuid)}/connect`, { method: 'POST' }),
  wifiSavedForget: (uuid: string) => api(`/api/v1/system/wifi/saved/${encodeURIComponent(uuid)}`, { method: 'DELETE' }),
  wifiSavedSet: (uuid: string, body: { autoconnect?: boolean; priority?: number }) =>
    api(`/api/v1/system/wifi/saved/${encodeURIComponent(uuid)}`, { method: 'PATCH', body }),
  wifiSavedPassword: (uuid: string, password: string) =>
    api(`/api/v1/system/wifi/saved/${encodeURIComponent(uuid)}/password`, { method: 'PUT', body: { password } }),
  wifiSavedEAP: (uuid: string, eap: EAPConfig) => api(`/api/v1/system/wifi/saved/${encodeURIComponent(uuid)}/eap`, { method: 'PUT', body: eap }),
  wifiSavedShare: (uuid: string, password: string) =>
    api<WiFiShare>(`/api/v1/system/wifi/saved/${encodeURIComponent(uuid)}/share`, { method: 'POST', body: { password } }),
  powerAction: (action: 'reboot' | 'shutdown') => api<{ status: string }>('/api/v1/system/power', { method: 'POST', body: { action } }),
};
