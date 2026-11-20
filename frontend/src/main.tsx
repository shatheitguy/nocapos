import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';
import { Installer } from './installer/Installer';
import { ErrorBoundary } from './components/ErrorBoundary';
import './styles/all';

// The USB stick's setup screen (cmd/nocap-installer serves it at /installer).
const installer = window.location.pathname.startsWith('/installer');

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ErrorBoundary title="NoCapOS" fallback="screen">
      {installer ? <Installer /> : <App />}
    </ErrorBoundary>
  </StrictMode>,
);
