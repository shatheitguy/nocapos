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
import { nativeSurface } from './platform';

startBridge(); // window.__nocap must exist before the host talks to the page

const surface = nativeSurface();
document.documentElement.dataset.nativeSurface = surface ?? 'none';

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
  root.render(
    <StrictMode>
      <ErrorBoundary title="NoCapOS" fallback="screen">
        <App />
      </ErrorBoundary>
    </StrictMode>,
  );
}

void start();
