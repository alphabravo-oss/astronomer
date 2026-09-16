import { defineConfig } from 'vite';
import packageMetadata from './package.json' with { type: 'json' };
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import { tanstackRouter } from '@tanstack/router-plugin/vite';

export default defineConfig({
  plugins: [
    tailwindcss(),
    tanstackRouter({
      target: 'react',
      routesDirectory: './src/routes',
      generatedRouteTree: './src/routeTree.gen.ts',
      routeFileIgnorePattern: '\\.test\\.(ts|tsx)$',
      autoCodeSplitting: true,
    }), // MUST precede react()
    react(),
  ],
  resolve: { tsconfigPaths: true },
  define: { __APP_VERSION__: JSON.stringify(process.env.VERSION ?? packageMetadata.version) },
  server: {
    port: Number(process.env.PORT) || 3000,
    proxy: { '/api': { target: process.env.BACKEND_URL ?? 'http://localhost:8001', ws: true } },
  },
  preview: {
    proxy: { '/api': { target: process.env.BACKEND_URL ?? 'http://localhost:8001', ws: true } },
  },
  build: {
    manifest: true,
    outDir: 'dist',
    chunkSizeWarningLimit: 650,
  },
});
