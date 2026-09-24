import { test, expect } from '../../frontend/node_modules/@playwright/test/index.mjs';
import fs from 'node:fs';
import path from 'node:path';
import { installStubs } from '../../frontend/tests/e2e-smoke/stubs';
import { seedAuth } from '../../frontend/tests/e2e/helpers/auth';
import { adminStoreUser, SMOKE_CLUSTER_ID } from '../../frontend/tests/e2e-smoke/stub-overrides';

test.beforeEach(async ({ page, context }) => {
  await installStubs(page);
  await seedAuth(context, page, adminStoreUser);
  await page.clock.setFixedTime(new Date('2026-09-24T12:00:00Z'));
});

test('measure current source shell at five widths', async ({ page }, info) => {
  const findings: unknown[] = [];
  for (const width of [390, 412, 768, 1024, 1280]) {
    await page.setViewportSize({ width, height: 900 });
    await page.goto(`/dashboard/clusters/${SMOKE_CLUSTER_ID}`);
    await expect(page.getByTestId('app-shell')).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Smoke East', exact: true })).toBeVisible();
    const controls = await page.locator('header button').evaluateAll((buttons) => buttons.map((el) => {
      const r = el.getBoundingClientRect();
      return { label: el.getAttribute('aria-label') || el.getAttribute('title') || el.textContent?.trim(),
        x: Math.round(r.x), right: Math.round(r.right), width: Math.round(r.width),
        visible: r.width > 0 && r.height > 0, outside: r.x < 0 || r.right > innerWidth };
    }));
    findings.push({ width, controls });
    await page.screenshot({ path: info.outputPath(`shell-${width}.png`), fullPage: false });
  }
  fs.writeFileSync(info.outputPath('shell-geometry.json'), JSON.stringify(findings, null, 2));
  console.log('SHELL_GEOMETRY', JSON.stringify(findings));
});

test('inspect delivery destination selection and page search', async ({ page }, info) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto('/dashboard/delivery/sources');
  await expect(page.getByTestId('app-shell')).toBeVisible();
  const selected = await page.locator('aside [aria-current="page"]').allTextContents();
  await page.keyboard.press('Control+k');
  await expect(page.getByPlaceholder(/Search pages|Search commands|Search anything|Type a command/i)).toBeVisible().catch(() => {});
  await page.screenshot({ path: info.outputPath('delivery-palette.png') });
  const text = await page.locator('body').innerText();
  fs.writeFileSync(info.outputPath('delivery-observation.json'), JSON.stringify({ selected, text }, null, 2));
  console.log('DELIVERY_SELECTED', JSON.stringify(selected));
});

test('capture representative page families from current source', async ({ page }, info) => {
  test.setTimeout(240000);
  await page.setViewportSize({ width: 1280, height: 900 });
  const base = `/dashboard/clusters/${SMOKE_CLUSTER_ID}`;
  const routes = ['/dashboard', '/dashboard/clusters', '/dashboard/projects', '/dashboard/rbac',
    '/dashboard/security', '/dashboard/agents', '/dashboard/alerting', '/dashboard/logging',
    '/dashboard/catalog', '/dashboard/settings', '/dashboard/settings/backup',
    '/dashboard/settings/operations', '/dashboard/cluster-templates',
    `${base}/apps`, `${base}/tools`, `${base}/workloads`, `${base}/custom-resources`,
    `${base}/metrics`, `${base}/logging`, `${base}/image-scans`, `${base}/snapshots`, `${base}/template`];
  const rows: unknown[] = [];
  for (const route of routes) {
    const errors: string[] = [];
    const onError = (error: Error) => errors.push(error.message);
    page.on('pageerror', onError);
    await page.goto(route);
    await expect(page.getByTestId('app-shell')).toBeVisible();
    await page.locator('main').waitFor();
    await page.waitForTimeout(300);
    const main = page.locator('main');
    rows.push({ route, headings: await main.locator('h1,h2').allTextContents(),
      alerts: await main.locator('[role="alert"]').allTextContents(),
      text: (await main.innerText()).slice(0, 7000), errors });
    page.off('pageerror', onError);
    await page.screenshot({ path: info.outputPath(`${route.replaceAll('/', '_')}.png`) });
  }
  fs.writeFileSync(info.outputPath('page-observations.json'), JSON.stringify(rows, null, 2));
  console.log('PAGE_FAMILIES', JSON.stringify(rows.map(({ route, headings, alerts, errors }: any) => ({ route, headings, alerts, errors }))));
});

test('reproduce custom-resource back destination', async ({ page }, info) => {
  const base = `/dashboard/clusters/${SMOKE_CLUSTER_ID}`;
  await page.route('**/k8s/apis/cert-manager.io/v1/namespaces/default/certificates/review-cert**', route => route.fulfill({ json: {
    apiVersion: 'cert-manager.io/v1', kind: 'Certificate',
    metadata: { name: 'review-cert', namespace: 'default', uid: 'review-cert-uid' },
    spec: { secretName: 'review-cert-tls' }, status: { conditions: [] },
  }}));
  await page.goto(`${base}/custom-resources/cert-manager.io/v1/certificates/default/review-cert`);
  await expect(page.getByRole('heading', { name: 'review-cert', exact: true })).toBeVisible();
  const back = page.getByRole('link', { name: 'Back', exact: true });
  const href = await back.getAttribute('href');
  await back.click();
  await expect(page.getByText(/Unknown resource type/i)).toBeVisible();
  fs.writeFileSync(info.outputPath('custom-back.json'), JSON.stringify({ href, resultingUrl: page.url() }, null, 2));
  await page.screenshot({ path: info.outputPath('custom-back.png') });
  console.log('CUSTOM_BACK', href, page.url());
});

test('reproduce namespace disagreement on custom resources', async ({ page }, info) => {
  await page.route('**/api/v1/clusters/*/namespaces/**', route => route.fulfill({ json: {
    data: ['team-a', 'team-b'].map(name => ({ name, clusterId: SMOKE_CLUSTER_ID, status: 'Active', createdAt: '2026-01-01T00:00:00Z' })),
    pagination: { limit: 200, offset: 0, total: 2, has_more: false, next_offset: null },
  }}));
  const requests: string[] = [];
  await page.route('**/k8s/apis/cert-manager.io/v1/certificates**', route => {
    requests.push(route.request().url());
    return route.fulfill({ json: { apiVersion: 'cert-manager.io/v1', kind: 'CertificateList', metadata: {},
      items: ['team-a', 'team-b'].map(namespace => ({ metadata: { name: `cert-${namespace}`, namespace } })),
    }});
  });
  await page.goto(`/dashboard/clusters/${SMOKE_CLUSTER_ID}/custom-resources/cert-manager.io/v1/certificates?namespaces=team-a`);
  await expect(page.getByRole('button', { name: 'Namespace scope: team-a', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'cert-team-b', exact: true })).toBeVisible();
  fs.writeFileSync(info.outputPath('custom-scope.json'), JSON.stringify({ requests, url: page.url() }, null, 2));
  await page.screenshot({ path: info.outputPath('custom-scope.png') });
  console.log('CUSTOM_SCOPE_MISMATCH', JSON.stringify(requests));
});

test('reproduce filtering after workload pagination', async ({ page }, info) => {
  await page.route('**/api/v1/clusters/*/namespaces/**', route => route.fulfill({ json: {
    data: ['team-a', 'team-b'].map(name => ({ name, clusterId: SMOKE_CLUSTER_ID, status: 'Active', createdAt: '2026-01-01T00:00:00Z' })),
    pagination: { limit: 200, offset: 0, total: 2, has_more: false, next_offset: null },
  }}));
  const requests: string[] = [];
  await page.route(/\/api\/v1\/clusters\/[^/]+\/workloads\/?(?:\?.*)?$/, route => {
    requests.push(route.request().url());
    const u = new URL(route.request().url());
    const scoped = u.searchParams.get('namespace') === 'team-b';
    return route.fulfill({ json: { data: Array.from({ length: scoped ? 1 : 50 }, (_, index) => ({ id: `workload-review-${index}`, name: scoped ? 'wanted-app' : `unrelated-app-${index}`,
      namespace: scoped ? 'team-b' : 'team-a', clusterId: SMOKE_CLUSTER_ID, kind: 'Deployment',
      status: 'Running', replicas: 1, desiredReplicas: 1, createdAt: '2026-01-01T00:00:00Z' })),
      pagination: { limit: 50, offset: 0, total: scoped ? 1 : 51, has_more: !scoped, next_offset: scoped ? null : 50 },
    }});
  });
  await page.goto(`/dashboard/clusters/${SMOKE_CLUSTER_ID}/deployments?namespaces=team-b`);
  await expect(page.getByRole('button', { name: 'Namespace scope: team-b', exact: true })).toBeVisible();
  await expect(page.getByText('No deployments found', { exact: true })).toBeVisible();
  expect(requests.length).toBeGreaterThan(0);
  expect(requests.every(url => !new URL(url).searchParams.has('namespace'))).toBeTruthy();
  fs.writeFileSync(info.outputPath('workload-scope.json'), JSON.stringify({ requests, text: await page.locator('main').innerText() }, null, 2));
  await page.screenshot({ path: info.outputPath('workload-scope.png') });
  console.log('WORKLOAD_SCOPE_MISMATCH', JSON.stringify(requests));
});

test('reproduce shared stacks double-active navigation', async ({ page }, info) => {
  await page.goto('/dashboard/monitoring/stacks');
  await expect(page.getByTestId('app-shell')).toBeVisible();
  const sidebar = page.locator('aside');
  await expect(sidebar.getByRole('link', { name: 'Shared stacks', exact: true })).toBeVisible();
  const links = await sidebar.locator('a[aria-current="page"]').evaluateAll(items => items.map(el => ({ text: el.textContent?.trim(), href: el.getAttribute('href'), class: el.className })));
  expect(links.some(link => link.text === 'Metrics')).toBeTruthy();
  expect(links.some(link => link.text === 'Shared stacks')).toBeTruthy();
  fs.writeFileSync(info.outputPath('active-links.json'), JSON.stringify(links, null, 2));
  console.log('DOUBLE_ACTIVE', JSON.stringify(links));
});

test('reproduce forbidden Velero status as not installed', async ({ page }, info) => {
  let statusRequests = 0;
  await page.route('**/api/v1/clusters/*/velero-status**', route => {
    statusRequests++;
    return route.fulfill({ status: 403, json: { error: { code: 'FORBIDDEN', message: 'Review fixture: access denied' } } });
  });
  await page.goto(`/dashboard/clusters/${SMOKE_CLUSTER_ID}/snapshots`);
  await expect(page.getByText('Velero is not installed', { exact: true })).toBeVisible();
  expect(statusRequests).toBeGreaterThan(0);
  await page.screenshot({ path: info.outputPath('velero-failed-read.png') });
  fs.writeFileSync(info.outputPath('velero-failed-read.json'), JSON.stringify({ text: await page.locator('main').innerText() }, null, 2));
});

test('reproduce keyboard focus entering closed mobile sidebar', async ({ page }, info) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`/dashboard/clusters/${SMOKE_CLUSTER_ID}`);
  await expect(page.getByTestId('app-shell')).toBeVisible();
  await page.getByRole('link', { name: 'Skip to main content', exact: true }).focus();
  await page.keyboard.press('Tab');
  const focus = await page.evaluate(() => {
    const el = document.activeElement as HTMLElement;
    const r = el.getBoundingClientRect();
    return { label: el.textContent?.trim(), inSidebar: Boolean(el.closest('aside')), x: r.x, right: r.right };
  });
  expect(focus.inSidebar).toBeTruthy();
  expect(focus.right).toBeLessThanOrEqual(0);
  fs.writeFileSync(info.outputPath('hidden-sidebar-focus.json'), JSON.stringify(focus, null, 2));
  console.log('HIDDEN_SIDEBAR_FOCUS', JSON.stringify(focus));
});
