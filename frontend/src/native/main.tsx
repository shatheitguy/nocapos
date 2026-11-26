// NoCap Desktop's entry (shell.html?surface=...): NoCap OS itself, natively
// on the screen. desktop, chrome and app are the very same App as in the
// browser (native/platform.ts says what differs); the sign-in screen and the
// admin password prompt are NoCap Shell's own pages.
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from '../App';
import { ErrorBoundary } from '../components/ErrorBoundary';
import { startBridge } from '../deskshell/bridge';
import '../styles/all';
import './native.css';
import './perf.css';
import { startFrost } from './frost';
import { startVersionWatch } from './versionWatch';
import { startWmStyle } from './wmStyle';
import { preloadApp } from '../apps/components';
import { hostCall, nativeSurface, softwareRendering } from './platform';

startBridge(); // window.__nocap must exist before the host talks to the page

const surface = nativeSurface();
document.documentElement.dataset.nativeSurface = surface ?? 'none';
if (softwareRendering()) document.documentElement.dataset.render = 'software';
// An app's window: its code loads while the window signs in.
if (surface === 'app') preloadApp(new URLSearchParams(location.search).get('app') ?? '');

async function start() {
  const root = createRoot(document.getElementById('root')!);
  if (surface === 'greeter' || surface === 'polkit') {
    await import('../deskshell/deskshell.css');
    const { applyTheme } = await import('../deskshell/hooks');
    applyTheme('dark');
    const View = surface === 'greeter' ? (await import('../deskshell/surfaces/Greeter')).Greeter : (await import('../deskshell/surfaces/Polkit')).Polkit;
    root.render(<View />);
    return;
  }
  if (surface === 'app' || surface === 'chrome') startFrost();
  // The desktop and the chrome keep running across updates: they follow a new version by themselves.
  if (surface === 'desktop' || surface === 'chrome') startVersionWatch();
  if (surface === 'chrome') startWmStyle();
  root.render(
    <StrictMode>
      <ErrorBoundary title="NoCapOS" fallback="screen">
        <App />
      </ErrorBoundary>
    </StrictMode>,
  );
  // Tells the host this surface is up (its test mode waits for every one).
  hostCall('shell.info').catch(() => undefined);
}

void start();
