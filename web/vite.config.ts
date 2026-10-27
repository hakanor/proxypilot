import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';
import { fileURLToPath } from 'node:url';

// Dev: run the daemon, then `PROXYPILOT_TOKEN=$(cat <data-dir>/token) npm run dev`.
const api = process.env.PROXYPILOT_API ?? 'http://127.0.0.1:9091';
const token = process.env.PROXYPILOT_TOKEN ?? '';

export default defineConfig({
  plugins: [tailwindcss(), svelte()],
  resolve: {
    alias: { $lib: fileURLToPath(new URL('./src/lib', import.meta.url)) },
  },
  base: './',
  build: {
    outDir: '../internal/api/webdist',
    emptyOutDir: true,
    assetsInlineLimit: 0,
    chunkSizeWarningLimit: 300,
  },
  server: {
    proxy: {
      '/api': {
        target: api,
        ws: true,
        changeOrigin: true,
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      },
    },
  },
});
