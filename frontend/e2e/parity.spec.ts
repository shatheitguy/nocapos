// NoCap Desktop parity: the desktop on the machine's own screen is the same
// NoCap OS as in the browser. This renders both, with the same mocked
// server and the same plain backdrop (the screen's wallpaper is another
// layer there), and compares the menu bar, the dock, the launcher, the
// Control Center and the notification center pixel by pixel.
//
//   npm run build && npm run build:shell && npx playwright test
import { mkdirSync, writeFileSync } from 'node:fs';
import { expect, test, type Page } from '@playwright/test';

const USER = { id: 'u1', username: 'sam', role: 'admin', disabled: false, totp_enabled: false, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' };
const SESSION = { access_token: 'parity-token-0123456789', token_type: 'Bearer', expires_at: '2099-01-01T00:00:00Z', user: USER };
const FIXED_NOW = new Date('2026-11-18T10:24:00Z').getTime();

// The same server for both: a session, the system, and empty lists.
async function mockServer(page: Page) {
  page.on('console', (m) => {
    if (m.type() === 'error' && /crashed/.test(m.text())) console.log(`[${page.url().includes('4174') ? 'NoCap Desktop' : 'browser'}] ${m.text().slice(0, 300)}`);
  });
  page.on('pageerror', (e) => console.log(`[${page.url().includes('4174') ? 'NoCap Desktop' : 'browser'}] page error: ${e.message}`));
  await page.route('**/api/**', async (route) => {
    const url = new URL(route.request().url());
    const p = url.pathname;
    const json = (body: unknown, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
    if (p === '/api/v1/auth/status') return json({ setup_required: false, accounts: 'system' });
    if (p === '/api/v1/auth/refresh' || p === '/api/v1/auth/me') return json(p.endsWith('me') ? USER : SESSION);
    // The person's look comes from the server, for both (welcome already done).
    if (p === '/api/v1/me/prefs') return json({ prefs: { welcomeDone: true } });
    if (p === '/api/v1/system/metrics')
      return json({ time: '2026-11-18T10:24:00Z', uptime: 3600, cpu: { percent: 12, per_core: [10, 14], load1: 0.2, load5: 0.2, load15: 0.1 }, memory: { total: 8e9, used: 2e9, available: 6e9, percent: 25, swap_total: 0, swap_used: 0 }, disks: [], network: [], temperatures: [], accelerators: [] });
    if (p === '/api/v1/system/info') return json({ version: '0.14.0', host: { hostname: 'nocap', os: 'linux', platform: 'NoCapOS', kernel: '6.12', arch: 'amd64', cpus: 4, memory_total: 8e9 }, accelerators: [] });
    if (p === '/api/v1/system/network')
      return json({ manager: 'networkmanager', wifi: { supported: true, enabled: true }, networking: { supported: true, enabled: true }, interfaces: [{ name: 'eth0', kind: 'ethernet', addresses: ['192.168.1.20/24'], up: true }] });
    if (p === '/api/v1/system/power') return json({ supported: true });
    if (p === '/api/v1/ws/ticket') return json({ error: { message: 'offline in the test' } }, 503);
    if (/avatar|icons?\//.test(p)) return route.fulfill({ status: 404, body: '' });
    // Everything else: nothing yet (empty lists under the usual names).
    const empty = { apps: [], items: [], jobs: [], updates: [], phones: [], sessions: [], notes: [], folders: [], devices: [], interfaces: [], networks: [], status: {}, cursor: 0, unread: 0 };
    return json(p.endsWith('s') || p.includes('list') ? [] : empty);
  });
  await page.routeWebSocket?.(/\/ws/, (ws) => ws.close());
}

// Same clock, same look, same backdrop on both pages.
async function common(page: Page) {
  await page.addInitScript((now) => {
    const RealDate = Date;
    class FixedDate extends RealDate {
      constructor(...a: ConstructorParameters<typeof Date>) {
        if (a.length === 0) super(now);
        else super(...a);
      }
      static now() {
        return now;
      }
    }
    (window as unknown as { Date: typeof Date }).Date = FixedDate as unknown as typeof Date;
  }, FIXED_NOW);
  await page.addStyleTag({ content: '' }).catch(() => undefined);
}

const BACKDROP = `
  :root, :root body, html[data-native-surface] body, html[data-native-surface] { background: #1d2733 !important; background-image: none !important; }
  .desktop-surface, .desktop-icons, .widget-layer, .stuck-notes, .window-layer, .welcome { display: none !important; }
  *, *::before, *::after { transition: none !important; animation: none !important; caret-color: transparent !important; }
`;

async function openWeb(page: Page) {
  await mockServer(page);
  await common(page);
  await page.goto('http://localhost:4173/');
  await page.addStyleTag({ content: BACKDROP });
  await page.waitForSelector('.dock', { timeout: 30_000 });
}

async function openNative(page: Page) {
  await mockServer(page);
  await common(page);
  // The NoCap Shell host, stood in for: the person's session and no windows.
  await page.addInitScript((session) => {
    const w = window as unknown as Record<string, unknown>;
    w.__NOCAP_PARITY_TEST__ = true;
    w.webkit = {
      messageHandlers: {
        nocap: {
          postMessage(json: string) {
            const m = JSON.parse(json) as { id: number; method: string };
            let data: unknown = {};
            if (m.method === 'session.get' || m.method === 'session.refresh') data = { ...session, apiOrigin: 'http://127.0.0.1:8780', account: session.user };
            else if (m.method === 'windows.list' || m.method === 'apps.list' || m.method === 'notify.list') data = [];
            setTimeout(() => (w.__nocap as { reply: (id: number, ok: boolean, d: unknown) => void }).reply(m.id, true, data), 0);
          },
        },
      },
    };
  }, SESSION);
  await page.goto('http://localhost:4174/shell.html?surface=chrome');
  await page.addStyleTag({ content: BACKDROP });
  await page.waitForSelector('.dock', { timeout: 30_000 });
}

/** The share of pixels that differ noticeably between two PNG screenshots. */
async function diff(page: Page, a: Buffer, b: Buffer): Promise<number> {
  return page.evaluate(
    async ([x, y]) => {
      const load = (b64: string) =>
        new Promise<HTMLImageElement>((res, rej) => {
          const i = new Image();
          i.onload = () => res(i);
          i.onerror = rej;
          i.src = `data:image/png;base64,${b64}`;
        });
      const [ia, ib] = await Promise.all([load(x), load(y)]);
      if (ia.width !== ib.width || ia.height !== ib.height) return 1;
      const px = (img: HTMLImageElement) => {
        const c = document.createElement('canvas');
        c.width = img.width;
        c.height = img.height;
        const g = c.getContext('2d')!;
        g.drawImage(img, 0, 0);
        return g.getImageData(0, 0, c.width, c.height).data;
      };
      const da = px(ia);
      const db = px(ib);
      let bad = 0;
      for (let i = 0; i < da.length; i += 4) {
        if (Math.abs(da[i] - db[i]) + Math.abs(da[i + 1] - db[i + 1]) + Math.abs(da[i + 2] - db[i + 2]) > 48) bad++;
      }
      return bad / (da.length / 4);
    },
    [a.toString('base64'), b.toString('base64')] as const,
  );
}

const MAX_DIFF = 0.02; // 2 % of the pixels (text anti-aliasing)

type Step = { name: string; open?: (p: Page) => Promise<void>; shot: (p: Page) => Promise<Buffer> };

const steps: Step[] = [
  { name: 'menu bar or tray', shot: async (p) => (await p.$('.menubar')) ? p.locator('.menubar').screenshot() : p.locator('.dock').screenshot() },
  { name: 'dock', shot: (p) => p.locator('.dock').screenshot() },
  {
    name: 'launcher',
    open: async (p) => {
      await p.locator('.dock-item.launchpad').first().click();
      await p.waitForSelector('.launcher');
      // The backdrop differs on purpose: the browser blurs the desktop
      // behind the app list, while on the screen (a separate window that
      // can't blur another) it's a dark sheet (native/native.css). Give both
      // the same plain backdrop so the comparison is about the app list
      // itself: icons, labels, search, pages and layout.
      await p.addStyleTag({ content: '.launcher { background: #0b0d12 !important; backdrop-filter: none !important; -webkit-backdrop-filter: none !important; }' });
    },
    shot: (p) => p.screenshot(),
  },
  {
    name: 'control center',
    open: async (p) => {
      const btn = p.locator('.mb-status-btn, .tray-status').first();
      await btn.click();
      await p.waitForSelector('.control-center');
    },
    shot: (p) => p.locator('.control-center').first().screenshot(),
  },
  {
    name: 'notification center',
    open: async (p) => {
      await p.locator('[data-notify-bell]').first().click();
      await p.waitForTimeout(300);
    },
    shot: (p) => p.screenshot(),
  },
];

for (const step of steps) {
  test(`same ${step.name} in the browser and on NoCap Desktop`, async ({ browser }) => {
    const opts = { viewport: { width: 1280, height: 800 }, deviceScaleFactor: 1 };
    const ctxWeb = await browser.newContext(opts);
    const ctxNative = await browser.newContext(opts);
    const web = await ctxWeb.newPage();
    const native = await ctxNative.newPage();
    await openWeb(web);
    await openNative(native);
    if (step.open) {
      for (const [label, pg] of [['browser', web], ['NoCap Desktop', native]] as const) {
        try {
          await step.open(pg);
        } catch (e) {
          mkdirSync('test-results/parity', { recursive: true });
          writeFileSync(`test-results/parity/${step.name.replace(/[^a-z]+/gi, '-')}-${label.replace(/ /g, '-')}-failed.png`, await pg.screenshot());
          throw new Error(`${step.name} didn't open on ${label}: ${e instanceof Error ? e.message.split(String.fromCharCode(10))[0] : e}`);
        }
      }
    }
    await web.waitForTimeout(400);
    await native.waitForTimeout(400);
    const a = await step.shot(web);
    const b = await step.shot(native);
    const share = await diff(web, a, b);
    mkdirSync('test-results/parity', { recursive: true });
    const slug = step.name.replace(/[^a-z]+/gi, '-');
    writeFileSync(`test-results/parity/${slug}-browser.png`, a);
    writeFileSync(`test-results/parity/${slug}-desktop.png`, b);
    test.info().attachments.push({ name: `${step.name} (browser)`, body: a, contentType: 'image/png' }, { name: `${step.name} (NoCap Desktop)`, body: b, contentType: 'image/png' });
    expect(share, `${step.name}: ${(share * 100).toFixed(2)} % of the pixels differ`).toBeLessThanOrEqual(MAX_DIFF);
    await ctxWeb.close();
    await ctxNative.close();
  });
}
