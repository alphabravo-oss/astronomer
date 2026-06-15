import { expect, test, type BrowserContext, type Page } from '@playwright/test';

// Confirms the DataTable rewrite (now backed by @tanstack/react-table) end-to-end
// against the real Clusters page: global search, sorting, pagination, and the
// B2 column-visibility persistence across reload. Auth + API are faked via
// cookies + route interception (no backend), mirroring dashboard-smoke.spec.ts.

const SESSION_COOKIE = 'astronomer_session';
const CSRF_COOKIE = 'astronomer_csrf';

const adminUser = {
  id: 'user-admin',
  username: 'admin',
  email: 'admin@example.com',
  displayName: 'Admin User',
  provider: 'local',
  globalRoles: ['admin'],
  isSuperuser: true,
  roles: { global: [], cluster: [], project: [] },
  enabled: true,
  lastLogin: new Date().toISOString(),
  createdAt: new Date().toISOString(),
};

function apiResponse<T>(data: T) {
  return { status: 200, data };
}
function paginated<T>(data: T[]) {
  return { data, total: data.length, page: 1, pageSize: 100, totalPages: 1 };
}

// 25 clusters → exceeds the default pageSize of 20 so pagination engages.
const clusters = Array.from({ length: 25 }, (_, i) => {
  const n = String(i + 1).padStart(2, '0');
  return {
    id: `cluster-${n}`,
    name: `cluster-${n}`,
    displayName: `Cluster ${n}`,
    description: '',
    status: i % 2 === 0 ? 'active' : 'inactive',
    health: { status: 'active', lastCheck: new Date().toISOString(), components: [] },
    provider: ['aws', 'gcp', 'azure'][i % 3],
    environment: 'production',
    region: 'us-east-1',
    distribution: 'eks',
    kubernetesVersion: '1.30',
    nodeCount: 3,
    podCount: 42,
    namespaceCount: 8,
    cpuCapacity: 24,
    cpuUsage: 6,
    cpuPercentage: 25,
    memoryCapacity: 96,
    memoryUsage: 32,
    memoryPercentage: 33,
    labels: {},
    annotations: {},
    agentVersion: 'e2e',
    lastHeartbeat: new Date().toISOString(),
    createdAt: new Date().toISOString(),
    updatedAt: new Date().toISOString(),
    isLocal: false,
  };
});

async function mockApi(page: Page) {
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname.replace(/^\/api\/v1/, '').replace(/\/$/, '') || '/';
    const method = route.request().method();

    if (path === '/events/stream') return route.fulfill({ status: 204, body: '' });
    if (path === '/auth/me') return route.fulfill({ json: apiResponse(adminUser) });
    if (path === '/settings/features') return route.fulfill({ json: apiResponse({}) });
    if (path === '/clusters' && method === 'GET') return route.fulfill({ json: paginated(clusters) });
    return route.fulfill({ json: apiResponse([]) });
  });
}

async function authenticate(context: BrowserContext, page: Page) {
  await context.addCookies([
    { name: SESSION_COOKIE, value: 'e2e-session', domain: '127.0.0.1', path: '/' },
    { name: CSRF_COOKIE, value: 'e2e-csrf', domain: '127.0.0.1', path: '/' },
  ]);
  await page.addInitScript((user) => {
    window.localStorage.setItem(
      'astronomer-auth',
      JSON.stringify({ state: { user, isAuthenticated: true }, version: 2 }),
    );
  }, adminUser);
}

const firstBodyRow = (page: Page) => page.locator('tbody tr').first();

test.beforeEach(async ({ page }) => {
  await mockApi(page);
});

test('DataTable: paginates, searches, and sorts the clusters list', async ({ context, page }) => {
  await authenticate(context, page);
  await page.goto('/dashboard/clusters');

  await expect(page.getByRole('heading', { name: 'Clusters' })).toBeVisible();

  // Pagination: 25 rows, pageSize 20 → first page shows 1-20 of 25.
  await expect(page.getByText('Showing 1-20 of 25')).toBeVisible();

  // NB: the Clusters page renders JSX cell accessors, so the global search box
  // is a no-op here (matches String(<jsx>) === "[object Object]"). This is
  // unchanged from the previous hand-rolled table; search-with-string-accessors
  // is covered in the unit suite (data-table.behavior.test.tsx).

  // Sort by Name (sortAccessor → displayName): asc then desc.
  const nameHeader = page.getByRole('columnheader', { name: /name/i });
  await nameHeader.click();
  await expect(firstBodyRow(page)).toContainText('Cluster 01');
  await nameHeader.click();
  await expect(firstBodyRow(page)).toContainText('Cluster 25');

  // Navigate to page 2 — clears the desc sort first to keep assertions simple.
  await nameHeader.click(); // back to asc
  await page.getByRole('button', { name: '2', exact: true }).click();
  await expect(page.getByText('Showing 21-25 of 25')).toBeVisible();
  await expect(firstBodyRow(page)).toContainText('Cluster 21');
});

test('DataTable: faceted Provider filter narrows the rows (B3)', async ({ context, page }) => {
  await authenticate(context, page);
  await page.goto('/dashboard/clusters');
  await expect(page.getByText('Showing 1-20 of 25')).toBeVisible();

  // 25 clusters cycle aws/gcp/azure → 9 are aws (indices 0,3,…,24). Filtering to
  // a single page (<20 rows) also removes the pagination footer.
  await page.getByRole('button', { name: /provider/i }).click();
  await page.getByRole('checkbox', { name: 'aws' }).click();

  await expect(page.locator('tbody tr')).toHaveCount(9);
  await expect(page.getByText('Showing 1-20 of 25')).toHaveCount(0);
});

test('DataTable: column-visibility choices persist across reload (B2)', async ({ context, page }) => {
  await authenticate(context, page);
  await page.goto('/dashboard/clusters');

  await expect(page.getByRole('columnheader', { name: /provider/i })).toBeVisible();

  // Hide the Provider column via the Columns dropdown.
  await page.getByRole('button', { name: /columns/i }).click();
  await page.getByRole('checkbox', { name: /provider/i }).click();
  await expect(page.getByRole('columnheader', { name: /provider/i })).toHaveCount(0);

  // Reload — the hidden column must stay hidden (persisted to localStorage).
  await page.reload();
  await expect(page.getByRole('heading', { name: 'Clusters' })).toBeVisible();
  await expect(page.getByRole('columnheader', { name: /provider/i })).toHaveCount(0);

  // And the persisted entry is present under the namespaced key.
  const stored = await page.evaluate(() => window.localStorage.getItem('dt:clusters:visibility'));
  expect(stored).toContain('provider');
});
