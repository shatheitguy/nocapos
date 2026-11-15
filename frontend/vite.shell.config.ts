import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// NoCap Shell: the surfaces of NoCap Desktop on the physical screen (bar, dock,
// roster, control center, banners, OSD, admin prompt, sign-in): one page,
// shell.html?surface=<name>, shown by the native host from nocap-shell://shell/.
// Everything is local: relative URLs, no public/ folder (the brand mark is
// imported), and no inline code (the host's CSP forbids it).
export default defineConfig({
  plugins: [react()],
  base: './',
  publicDir: false,
  build: {
    outDir: 'dist-shell',
    emptyOutDir: true,
    assetsInlineLimit: 0, // keep everything as files: the CSP forbids inline code
    modulePreload: { polyfill: false },
    rollupOptions: { input: { shell: 'shell.html' } },
  },
  server: { port: 5180 },
});
