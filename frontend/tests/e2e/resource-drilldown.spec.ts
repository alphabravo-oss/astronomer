import { expect, test, type BrowserContext, type Page } from '@playwright/test';

// GATE A drill-down: a resource table row is clickable into the generic
// ResourceDetail (Overview + YAML tabs), while a per-row action button does
// NOT navigate. Auth + API are faked via cookies + route interception (no
// backend), mirroring data-table.spec.ts.

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

const CLUSTER_ID = 'cluster-01';
const SERVICE_NAME = 'my-svc';
const SERVICE_NS = 'default';

function apiResponse<T>(data: T) {
  return { status: 200, data };
}

const cluster = {
  id: CLUSTER_ID,
  name: CLUSTER_ID,
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

// Service list-row shape (the /resources/services endpoint).
const serviceRow = {
  name: SERVICE_NAME,
  namespace: SERVICE_NS,
  type: 'ClusterIP',
  clusterIP: '10.0.0.10',
  ports: [{ port: 80, protocol: 'TCP' }],
  createdAt: new Date().toISOString(),
};

// Single Service object returned by the k8s proxy GET (raw upstream JSON).
// Single-word label keys survive the client's snake->camel key transform.
const serviceObject = {
  apiVersion: 'v1',
  kind: 'Service',
  metadata: {
    name: SERVICE_NAME,
    namespace: SERVICE_NS,
    uid: 'svc-uid-123',
    creationTimestamp: '2024-01-01T00:00:00Z',
    labels: { team: 'platform' },
  },
  spec: { type: 'ClusterIP', clusterIP: '10.0.0.10', ports: [{ port: 80, protocol: 'TCP' }] },
  status: {},
};

async function mockApi(page: Page) {
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname.replace(/^\/api\/v1/, '').replace(/\/$/, '') || '/';
    const method = route.request().method();

    if (path === '/events/stream') return route.fulfill({ status: 204, body: '' });
    if (path === '/auth/me') return route.fulfill({ json: apiResponse(adminUser) });
    if (path === '/settings/features') return route.fulfill({ json: apiResponse({}) });
    if (path === `/clusters/${CLUSTER_ID}` && method === 'GET') {
      return route.fulfill({ json: apiResponse(cluster) });
    }
    if (path === `/clusters/${CLUSTER_ID}/resources/services` && method === 'GET') {
      return route.fulfill({ json: apiResponse([serviceRow]) });
    }
    // Single Service via the k8s proxy: /clusters/{id}/k8s/api/v1/namespaces/{ns}/services/{name}
    if (path === `/clusters/${CLUSTER_ID}/k8s/api/v1/namespaces/${SERVICE_NS}/services/${SERVICE_NAME}`) {
      return route.fulfill({ json: serviceObject });
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

test('drilldown: clicking a Service row opens its detail (Overview + YAML)', async ({ context, page }) => {
  await authenticate(context, page);
  await page.goto(`/dashboard/clusters/${CLUSTER_ID}/services`);

  await expect(page.getByRole('heading', { name: 'Services' })).toBeVisible();

  // The row is clickable: click somewhere that's not the name link or actions.
  const row = page.locator('tbody tr').filter({ hasText: SERVICE_NAME }).first();
  await expect(row).toBeVisible();
  await row.getByText('10.0.0.10').click();

  // Shareable detail URL: namespaced -> .../services/<ns>/<name>.
  await expect(page).toHaveURL(new RegExp(`/dashboard/clusters/${CLUSTER_ID}/services/${SERVICE_NS}/${SERVICE_NAME}$`));

  // Overview: header shows the name; Metadata + Labels render.
  await expect(page.getByRole('heading', { name: SERVICE_NAME })).toBeVisible();
  await expect(page.getByText('Kind: Service')).toBeVisible();
  await expect(page.getByText('Metadata')).toBeVisible();
  await expect(page.getByText('Labels')).toBeVisible();
  await expect(page.getByText('platform')).toBeVisible();

  // YAML tab renders the panel (View/Edit toggle + editor toolbar).
  await page.getByRole('button', { name: 'YAML' }).click();
  await expect(page.getByRole('button', { name: 'Edit' })).toBeVisible();
  await expect(page.getByText('YAML', { exact: true }).last()).toBeVisible();
});

test('drilldown: clicking the row action button does NOT navigate', async ({ context, page }) => {
  await authenticate(context, page);
  await page.goto(`/dashboard/clusters/${CLUSTER_ID}/services`);

  await expect(page.getByRole('heading', { name: 'Services' })).toBeVisible();

  const row = page.locator('tbody tr').filter({ hasText: SERVICE_NAME }).first();
  await expect(row).toBeVisible();

  // The per-row actions trigger (ActionMenu) lives in the last cell. Clicking
  // it opens the menu but must NOT drill into the detail route.
  await row.locator('button').last().click();

  // Still on the list page (the action's stopPropagation prevented row click).
  await expect(page).toHaveURL(new RegExp(`/dashboard/clusters/${CLUSTER_ID}/services$`));
  await expect(page.getByRole('heading', { name: 'Services' })).toBeVisible();
  // The action menu opened (View YAML item visible) — confirms we hit the button.
  await expect(page.getByText('View YAML').first()).toBeVisible();
});
