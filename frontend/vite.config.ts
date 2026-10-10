import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// The build is embedded into alfad (backend/web/dist), so the daemon ships the
// whole OS as one binary. `npm run dev` proxies API/WS calls to a local alfad
// (ALFA_DEV_API picks another one, e.g. http://127.0.0.1:18099).
const api = process.env.ALFA_DEV_API ?? 'http://127.0.0.1:8080';

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
      '/api': api,
      '/ws': { target: api.replace(/^http/, 'ws'), ws: true },
    },
  },
});
