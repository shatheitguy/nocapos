// NoCap Shell entry: renders the one surface named in ?surface= (shell/PROTOCOL.md).
import { createRoot } from 'react-dom/client';
import type { ComponentType } from 'react';
import '@fontsource/orbitron/latin-600.css';
import '../styles.css';
import '../kit.css';
import '../glass.css';
import './deskshell.css';
import { isMock, startBridge } from './bridge';
import { applyTheme } from './hooks';
import { SURFACES, type SurfaceName } from './protocol';
import { Bar } from './surfaces/Bar';
import { Center } from './surfaces/Center';
import { Dock } from './surfaces/Dock';
import { Launcher } from './surfaces/Launcher';
import { Banners } from './surfaces/Banners';
import { Osd } from './surfaces/Osd';
import { Polkit } from './surfaces/Polkit';
import { Greeter } from './surfaces/Greeter';

const VIEWS: Record<SurfaceName, ComponentType> = {
  bar: Bar,
  dock: Dock,
  launcher: Launcher,
  center: Center,
  banners: Banners,
  osd: Osd,
  polkit: Polkit,
  greeter: Greeter,
};

startBridge(); // window.__nocap must exist before the host talks to the page

const asked = new URLSearchParams(location.search).get('surface') ?? '';
const surface = (SURFACES as readonly string[]).includes(asked) ? (asked as SurfaceName) : null;
const root = document.documentElement;
applyTheme('dark');
root.dataset.surface = surface ?? 'none';
// In a normal browser (the mock host) the page gets a backdrop and the surface's real size.
if (isMock()) root.dataset.preview = '';

const View = surface ? VIEWS[surface] : null;
createRoot(document.getElementById('root')!).render(View ? <View /> : null);
if (!View) console.error(`nocap: unknown surface "${asked}"`);
