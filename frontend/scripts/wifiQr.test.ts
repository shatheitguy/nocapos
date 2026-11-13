// Wi-Fi share QR payload: run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { escapeWifiQr, wifiQrPayload } from '../src/lib/wifiQr.ts';

test('a plain WPA network', () => {
  assert.equal(wifiQrPayload({ ssid: 'Home', password: 'hunter22', type: 'WPA' }), 'WIFI:T:WPA;S:Home;P:hunter22;H:false;;');
});

test('special characters are escaped', () => {
  assert.equal(escapeWifiQr('a\\b;c,d:e"f'), 'a\\\\b\\;c\\,d\\:e\\"f');
  assert.equal(
    wifiQrPayload({ ssid: 'Joe\'s "Fast"; Wi:Fi', password: 'p,a;s\\s', type: 'WPA', hidden: true }),
    'WIFI:T:WPA;S:Joe\'s \\"Fast\\"\\; Wi\\:Fi;P:p\\,a\\;s\\\\s;H:true;;',
  );
});

test('an open network has no password field', () => {
  assert.equal(wifiQrPayload({ ssid: 'Cafe', type: 'nopass' }), 'WIFI:T:nopass;S:Cafe;H:false;;');
});
