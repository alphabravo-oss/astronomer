import assert from 'node:assert/strict';
import test from 'node:test';
import { gzipSync } from 'node:zlib';
import { eagerAssets, measureEagerClosure } from './check-bundle-budget.mjs';

test('eager closure follows transitive static imports, deduplicates cycles/CSS, and excludes lazy features', () => {
  const manifest = {
    'index.html': { file: 'assets/index-HASH.js', imports: ['vendor'], css: ['assets/style-HASH.css'], dynamicImports: ['charts'] },
    'src/login.tsx?tsr-split=component': { file: 'assets/login-HASH.js', imports: ['index.html', 'forms'], css: ['assets/style-HASH.css'] },
    vendor: { file: 'assets/vendor-HASH.js', imports: ['utils'] },
    utils: { file: 'assets/utils-HASH.js', imports: ['vendor'] },
    forms: { file: 'assets/forms-HASH.js', imports: ['utils'] },
    charts: { file: 'assets/charts-HASH.js' },
  };
  const roots = ['index.html', 'src/login.tsx?tsr-split=component'];
  const files = eagerAssets(manifest, roots);
  assert.deepEqual(files, ['forms-HASH.js', 'index-HASH.js', 'login-HASH.js', 'style-HASH.css', 'utils-HASH.js', 'vendor-HASH.js'].map((file) => `assets/${file}`));
  const payload = Buffer.from('repeatable fixture content '.repeat(20));
  assert.deepEqual(measureEagerClosure(manifest, roots, () => payload), {
    files, raw: payload.length * files.length, gzip: gzipSync(payload).length * files.length,
  });
});

test('a shared utility dragging a chart chunk into login is counted', () => {
  const manifest = {
    login: { file: 'assets/login-ONE.js', imports: ['utility'] },
    utility: { file: 'assets/utils-TWO.js', imports: ['chart'] },
    chart: { file: 'assets/chart-THREE.js' },
  };
  assert.equal(eagerAssets(manifest, ['login']).length, 3);
  manifest.chart.file = 'assets/chart-NEW-HASH.js';
  assert.ok(eagerAssets(manifest, ['login']).includes('assets/chart-NEW-HASH.js'));
});

test('missing roots and missing transitive imports fail closed', () => {
  assert.throws(() => eagerAssets({}, ['login']), /Missing bundle manifest entry: login/);
  assert.throws(() => eagerAssets({ login: { file: 'login.js', imports: ['missing'] } }, ['login']), /Missing bundle manifest entry: missing/);
});
