import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// The phone setup slideshow for the certificate page phones open (internal/certpage):
// one self-contained script with its CSS injected, embedded in the Go binary.
export default defineConfig({
  plugins: [svelte({ compilerOptions: { css: 'injected' } })],
  publicDir: false,
  build: {
    outDir: '../internal/certpage/guide',
    emptyOutDir: true,
    lib: { entry: 'src/certguide/main.svelte.ts', formats: ['iife'], name: 'ppGuideBundle', fileName: () => 'guide.js' },
  },
});
