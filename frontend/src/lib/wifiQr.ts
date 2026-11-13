// The standard "join this Wi-Fi" QR payload phones understand:
//   WIFI:T:WPA;S:<ssid>;P:<password>;H:<true|false>;;
// Backslash, semicolon, comma, colon and double quote inside a value are
// escaped with a backslash.

export interface WifiQrInput {
  ssid: string;
  password?: string;
  /** WPA (WPA/WPA2/WPA3 personal) or nopass (open network). */
  type: 'WPA' | 'nopass';
  hidden?: boolean;
}

export function escapeWifiQr(v: string): string {
  return v.replace(/[\\;,:"]/g, (c) => `\\${c}`);
}

export function wifiQrPayload({ ssid, password, type, hidden }: WifiQrInput): string {
  const pw = type === 'nopass' ? '' : `P:${escapeWifiQr(password ?? '')};`;
  return `WIFI:T:${type};S:${escapeWifiQr(ssid)};${pw}H:${hidden ? 'true' : 'false'};;`;
}
