import { expect, test, type BrowserContext, type Page } from '@playwright/test';

// Verifies that the DataTable's `virtualized` opt-in (a DIV-based ARIA grid that
// windows the full filtered+sorted row model) works end-to-end in a real
// browser against the CIS scan-findings list at
// /dashboard/security/scans/[scanId] (FindingsSection). That section renders a
// DataTable over the *fully loaded* `scan.findings` array (getCISScan returns
// every finding in one response — not serverSide), so it is a verified
// fetch-all list and a good virtualization target.
//
// Auth + API are faked via cookies + route interception (no backend), mirroring
// data-table.spec.ts. The scan is mocked with 1000+ findings; we assert that
// the grid windows them (a bounded number of body rows in the DOM) and that
// scrolling reveals later rows.

const SESSION_COOKIE = 'astronomer_session';
const CSRF_COOKIE = 'astronomer_csrf';

const SCAN_ID = 'scan-virtualized-e2e';
const CLUSTER_ID = 'cluster-01';
const FINDING_COUNT = 1200;

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
  return { data, total: data.length, page: 1, pageSize: 200, totalPages: 1 };
}

const cluster = {
  id: CLUSTER_ID,
  name: 'cluster-01',
  displayName: 'Cluster 01',
  description: '',
  status: 'active',
  health: { status: 'active', lastCheck: new Date().toISOString(), components: [] },
  provider: 'aws',
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

const severities = ['critical', 'high', 'medium', 'low', 'info'];
const statuses = ['fail', 'warn', 'pass', 'skip'];

// 1200 findings → far exceeds anything that would render fully in the DOM, so a
// bounded rendered-row count proves the grid is virtualizing.
const findings = Array.from({ length: FINDING_COUNT }, (_, i) => {
  const n = String(i + 1).padStart(4, '0');
  return {
    testId: `1.2.${n}`,
    severity: severities[i % severities.length],
    status: statuses[i % statuses.length],
    description: `Finding number ${n} — ensure control ${n} is configured correctly`,
    remediation: `Apply remediation for control ${n}`,
  };
});

const scanDetail = {
  id: SCAN_ID,
  clusterId: CLUSTER_ID,
  scanType: 'cis-1.8',
  status: 'completed',
  passed: 300,
  failed: 300,
  warned: 300,
  skipped: 300,
  startedAt: new Date().toISOString(),
  completedAt: new Date().toISOString(),
  clusterScanName: 'scan-report-e2e',
  createdAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
  findings,
};

async function mockApi(page: Page) {
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname.replace(/^\/api\/v1/, '').replace(/\/$/, '') || '/';
    const method = route.request().method();

    if (path === '/events/stream') return route.fulfill({ status: 204, body: '' });
    if (path === '/auth/me') return route.fulfill({ json: apiResponse(adminUser) });
    if (path === '/settings/features') return route.fulfill({ json: apiResponse({}) });
    if (path === '/clusters' && method === 'GET') return route.fulfill({ json: paginated([cluster]) });
    // The detail endpoint returns a flat object (not an envelope) — getCISScan
    // tolerates both, so a flat body is the faithful mock.
    if (path === `/security/scans/${SCAN_ID}` && method === 'GET') {
      return route.fulfill({ json: scanDetail });
    }
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

test.beforeEach(async ({ page }) => {
  await mockApi(page);
});

test('DataTable(virtualized): windows a 1200-row CIS findings list', async ({ context, page }) => {
  await authenticate(context, page);
  await page.goto(`/dashboard/security/scans/${SCAN_ID}`);

  // The findings heading reports the full filtered count, proving all 1200 rows
  // are loaded client-side (a fetch-all list, not server-paginated).
  await expect(page.getByRole('heading', { name: `Findings (${FINDING_COUNT})` })).toBeVisible();

  // The virtualized branch renders a DIV-based ARIA grid.
  const grid = page.getByRole('grid');
  await expect(grid).toBeVisible();
  // aria-rowcount reflects the *full* body-row count, not what's in the DOM.
  await expect(grid).toHaveAttribute('aria-rowcount', String(FINDING_COUNT));

  // role="row" inside the grid includes the sticky header row + the windowed
  // body rows. With 1200 findings, only a small window is mounted — assert the
  // count stays bounded (well under the full dataset).
  const gridRows = grid.getByRole('row');
  const renderedRowCount = await gridRows.count();
  expect(renderedRowCount).toBeGreaterThan(1); // header + at least some body rows
  expect(renderedRowCount).toBeLessThan(60); // NOT all 1200 in the DOM

  // The first body row is the first finding (1.2.0001).
  await expect(grid.getByText('1.2.0001')).toBeVisible();
  // A far-down finding is NOT in the DOM yet (it's outside the window).
  await expect(grid.getByText('1.2.0800')).toHaveCount(0);

  // Scroll the grid container down — the virtualizer should mount later rows
  // and recycle the early ones.
  await grid.evaluate((el) => {
    el.scrollTop = el.scrollHeight;
  });

  // After scrolling to the bottom, the last finding becomes visible and the
  // very first one is no longer mounted — proof of windowing/recycling.
  await expect(grid.getByText(`1.2.${String(FINDING_COUNT).padStart(4, '0')}`)).toBeVisible();
  await expect(grid.getByText('1.2.0001')).toHaveCount(0);

  // The rendered-row count is still bounded after scrolling.
  expect(await gridRows.count()).toBeLessThan(60);
});
