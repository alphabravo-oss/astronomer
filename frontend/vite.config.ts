import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { tanstackRouter } from '@tanstack/router-plugin/vite';
import tsconfigPaths from 'vite-tsconfig-paths';

export default defineConfig({
  plugins: [
    tanstackRouter({
      target: 'react',
      routesDirectory: './src/routes',
      generatedRouteTree: './src/routeTree.gen.ts',
      routeFileIgnorePattern: '\\.test\\.(ts|tsx)$',
      autoCodeSplitting: true,
    }), // MUST precede react()
    react(),
    tsconfigPaths(),
  ],
  define: { __APP_VERSION__: JSON.stringify(process.env.VERSION ?? '0.3.0-dev') },
  server: {
    port: Number(process.env.PORT) || 3000,
    proxy: { '/api': { target: process.env.BACKEND_URL ?? 'http://localhost:8000', ws: true } },
  },
  preview: {
    proxy: { '/api': { target: process.env.BACKEND_URL ?? 'http://localhost:8000', ws: true } },
  },
  build: { outDir: 'dist', chunkSizeWarningLimit: 1500 },
});
