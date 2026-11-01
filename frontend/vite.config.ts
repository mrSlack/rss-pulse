import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { serviceWorker } from './vite-plugin-service-worker.ts';

export default defineConfig({
  plugins: [react(), serviceWorker()],
  build: {
    rolldownOptions: {
      output: {
        codeSplitting: {
          // Third-party code (React, i18next) changes far less often than the
          // app, so it gets its own long-cached chunk that survives app deploys.
          groups: [{ name: 'vendor', test: /[\\/]node_modules[\\/]/ }],
        },
      },
    },
  },
  server: {
    host: '0.0.0.0',
    port: 5173,
    strictPort: true,
    watch: { usePolling: true },
    proxy: process.env.PLAYWRIGHT_API_PROXY_TARGET
      ? { '/api': { target: process.env.PLAYWRIGHT_API_PROXY_TARGET } }
      : undefined,
  },
  test: {
    environment: 'jsdom',
    include: ['test/**/*.test.{ts,tsx}'],
    setupFiles: ['test/setup.ts'],
  },
});
