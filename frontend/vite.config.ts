import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
  plugins: [tailwindcss(), svelte()],
  build: {
    outDir: '../app/static/dist',
    emptyOutDir: true,
    sourcemap: false,
  },
  server: {
    proxy: {
      '/api': 'http://192.168.1.210:8088',
      '/terminal/ws': {
        target: 'ws://192.168.1.210:8088',
        ws: true,
      },
    },
  },
});
