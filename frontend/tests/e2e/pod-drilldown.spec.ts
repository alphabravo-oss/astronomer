import { expect, test, type Page } from '@playwright/test';

import { seedAuth } from './helpers/auth';

// GATE B pod detail: clicking a Pod row opens the generic ResourceDetail with
// a pod-specific Overview (containers) and pod-only Logs/Exec tabs. The Logs
// tab renders PodLogsViewer with logs mocked. Auth + API are faked via cookies
// + route interception (no backend), mirroring resource-drilldown.spec.ts.

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
const POD_NAME = 'web-abc123';
const POD_NS = 'default';
const CONTAINER = 'app';

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

// Pod list-row shape (the /clusters/{id}/pods endpoint).
const podRow = {
  name: POD_NAME,
  namespace: POD_NS,
  clusterId: CLUSTER_ID,
  phase: 'Running',
  status: 'Running',
  ready: '1/1',
  restarts: 0,
  node: 'node-1',
  ip: '10.1.2.3',
  containers: [{ name: CONTAINER, image: 'nginx:1.25', status: 'running', ready: true, restartCount: 0 }],
  conditions: [],
  createdAt: new Date().toISOString(),
  age: '5m',
};

// Single Pod object returned by the k8s proxy GET (raw upstream JSON).
const podObject = {
  apiVersion: 'v1',
  kind: 'Pod',
  metadata: {
    name: POD_NAME,
    namespace: POD_NS,
    uid: 'pod-uid-123',
    creationTimestamp: '2024-01-01T00:00:00Z',
  },
  spec: {
    nodeName: 'node-1',
    containers: [{ name: CONTAINER, image: 'nginx:1.25' }],
  },
  status: {
    phase: 'Running',
    podIP: '10.1.2.3',
    containerStatuses: [
      { name: CONTAINER, image: 'nginx:1.25', ready: true, restartCount: 0, state: { running: {} } },
    ],
  },
};

// Pod logs returned by the initial fetch (the WS stream is allowed to fail).
const podLogs = [
  { timestamp: '2024-01-01T00:00:01Z', message: 'server started on :8080', container: CONTAINER },
];

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
    if (path === `/clusters/${CLUSTER_ID}/pods` && method === 'GET') {
      return route.fulfill({ json: apiResponse([podRow]) });
    }
    // Raw pod list via the k8s proxy — seeds the pods TanStack DB collection
    // behind the Pods table (P4.7). The SSE watch that follows is allowed to
    // fail (the ticket mint below returns no ticket); the table renders from
    // this seed alone.
    if (path === `/clusters/${CLUSTER_ID}/k8s/api/v1/pods` && method === 'GET') {
      return route.fulfill({ json: { items: [podObject] } });
    }
    // Pod logs initial fetch.
    if (path === `/workloads/pods/${CLUSTER_ID}/${POD_NS}/${POD_NAME}/logs`) {
      return route.fulfill({ json: apiResponse(podLogs) });
    }
    // Single Pod via the k8s proxy.
    if (path === `/clusters/${CLUSTER_ID}/k8s/api/v1/namespaces/${POD_NS}/pods/${POD_NAME}`) {
      return route.fulfill({ json: podObject });
    }
    return route.fulfill({ json: apiResponse([]) });
  });
}

test.beforeEach(async ({ page }) => {
  await mockApi(page);
});

test('pod drilldown: row opens pod detail with containers + Logs tab renders', async ({ context, page }) => {
  await seedAuth(context, page, adminUser);
  await page.goto(`/dashboard/clusters/${CLUSTER_ID}/pods`);

  await expect(page.getByRole('heading', { name: 'Pods' })).toBeVisible();

  // The row is clickable: click a non-link, non-action cell (the Node column).
  const row = page.locator('tbody tr').filter({ hasText: POD_NAME }).first();
  await expect(row).toBeVisible();
  await row.getByText('node-1').click();

  // Shareable detail URL: namespaced -> .../pods/<ns>/<name>.
  await expect(page).toHaveURL(new RegExp(`/dashboard/clusters/${CLUSTER_ID}/pods/${POD_NS}/${POD_NAME}$`));

  // Pod overview: header + pod summary + per-container row.
  await expect(page.getByRole('heading', { name: POD_NAME })).toBeVisible();
  await expect(page.getByText('Kind: Pod')).toBeVisible();
  await expect(page.getByText('Containers')).toBeVisible();
  await expect(page.getByText('nginx:1.25')).toBeVisible();
  // Pod summary fields.
  await expect(page.getByText('node-1')).toBeVisible();

  // Logs tab is present (pods:logs allowed for admin) and renders the viewer.
  await page.getByRole('button', { name: 'Logs' }).click();
  await expect(page.getByText('server started on :8080')).toBeVisible();

  // Exec tab is also present for a pod (pods:exec allowed for admin).
  await expect(page.getByRole('button', { name: 'Exec' })).toBeVisible();
});
