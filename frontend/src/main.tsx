import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';
import { ErrorBoundary } from './components/ErrorBoundary';
// Theme fonts are bundled (served by NoCapOS itself — the CSP blocks font CDNs).
import '@fontsource/orbitron/latin-600.css';
import '@fontsource/rajdhani/latin-500.css';
import '@fontsource/rajdhani/latin-600.css';
import './styles.css';
import './kit.css';
import './styles/files.css';
import './styles/appstore.css';
import './styles/settings.css';
import './styles/devices.css';
import './styles/photos.css';
import './styles/storage.css';
import './styles/containers.css';
import './styles/virtualdesk.css';
import './styles/notifications.css';
import './styles/users.css';
import './styles/shield.css';
import './styles/phone.css';
import './themes.css';
import './glass.css';
import './styles/mobile.css';

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ErrorBoundary title="NoCapOS" fallback="screen">
      <App />
    </ErrorBoundary>
  </StrictMode>,
);
