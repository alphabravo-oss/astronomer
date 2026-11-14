// Read-only source preview: deliberately omit router generation and builds.
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
const require = createRequire(new URL('../../frontend/package.json', import.meta.url));
const { createServer } = await import(require.resolve('vite'));
const { default: react } = await import(require.resolve('@vitejs/plugin-react'));
const { default: tailwind } = await import(require.resolve('@tailwindcss/vite'));
const root = fileURLToPath(new URL('../../frontend/', import.meta.url));
const server = await createServer({
  configFile: false,
  root,
  plugins: [tailwind(), react()],
  resolve: { alias: { '@': `${root}src` }, tsconfigPaths: true },
  define: {
    __APP_VERSION__: JSON.stringify('1.2.0'),
    __BUILD_COMMIT__: JSON.stringify('22f633ec-working-tree-audit'),
    __BUILD_DATE__: JSON.stringify('2026-09-24'),
    __BUILD_NODE_VERSION__: JSON.stringify(process.version),
  },
  server: { host: '127.0.0.1', port: 32127, strictPort: true, hmr: false },
});
await server.listen();
console.log('Read-only source audit preview: http://127.0.0.1:32127');
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, async () => {
  await server.close(); process.exit(0);
});
