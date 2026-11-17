import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// NoCap Desktop: NoCap OS natively on the machine's own screen. The same app
// as the browser build (vite.config.ts), with shell.html as its entry
// (src/native/main.tsx), installed to /usr/share/nocapos/shell and shown by
// the NoCap Shell host from nocap-shell://shell/. Everything is local:
// relative URLs, the same public/ files (wallpapers, sounds, icons), and no
// inline code (the host's CSP forbids it).
export default defineConfig({
  plugins: [react()],
  base: './',
  build: {
    outDir: 'dist-shell',
    emptyOutDir: true,
    assetsInlineLimit: 0, // keep everything as files: the CSP forbids inline code
    chunkSizeWarningLimit: 800,
    modulePreload: { polyfill: false },
    rollupOptions: { input: { shell: 'shell.html' } },
  },
  server: { port: 5180 },
});
