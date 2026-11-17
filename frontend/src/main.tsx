import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';
import { ErrorBoundary } from './components/ErrorBoundary';
import './styles/all';

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ErrorBoundary title="NoCapOS" fallback="screen">
      <App />
    </ErrorBoundary>
  </StrictMode>,
);
