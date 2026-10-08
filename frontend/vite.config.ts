import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// The build is embedded into alfad (backend/web/dist), so the daemon ships the
// whole OS as one binary. `npm run dev` proxies API/WS calls to a local alfad.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../backend/web/dist',
    emptyOutDir: true,
    assetsInlineLimit: 0, // keep everything as files: the CSP forbids inline code
    chunkSizeWarningLimit: 800,
  },
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://127.0.0.1:8080',
      '/ws': { target: 'ws://127.0.0.1:8080', ws: true },
    },
  },
});
