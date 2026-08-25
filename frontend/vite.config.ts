import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { tanstackRouter } from '@tanstack/router-plugin/vite';
import tsconfigPaths from 'vite-tsconfig-paths';

function vendorChunk(id: string): string | undefined {
  if (!id.includes('/node_modules/')) return undefined;
  if (/\/(react|react-dom|scheduler)\//.test(id)) return 'vendor-react';
  if (id.includes('/node_modules/@tanstack/')) return 'vendor-tanstack';
  if (/\/(recharts|d3-[^/]+|victory-vendor)\//.test(id)) return 'vendor-charts';
  if (/\/(react-markdown|remark-[^/]+|micromark[^/]*|mdast-[^/]*|unified|unist-[^/]*)\//.test(id)) return 'vendor-markdown';
  if (/\/(lucide-react|cmdk|sonner)\//.test(id)) return 'vendor-ui';
  return undefined;
}

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
  build: {
    outDir: 'dist',
    chunkSizeWarningLimit: 650,
    rollupOptions: { output: { manualChunks: vendorChunk } },
  },
});
