import { expect, type Page, test } from '@playwright/test';

import { CSRF_COOKIE, SESSION_COOKIE, seedAuth } from './helpers/auth';

type SmokeUser = {
  id: string;
  username: string;
  email: string;
  displayName: string;
  provider: string;
  globalRoles: string[];
  isSuperuser: boolean;
  roles: {
    global: Array<Record<string, unknown>>;
    cluster: Array<Record<string, unknown>>;
    project: Array<Record<string, unknown>>;
  };
  enabled: boolean;
  lastLogin: string;
  createdAt: string;
};

const adminUser: SmokeUser = {
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

const readOnlyUser = {
  ...adminUser,
  id: 'user-readonly',
  username: 'reader',
  email: 'reader@example.com',
  displayName: 'Read Only',
  globalRoles: ['viewer'],
  isSuperuser: false,
  roles: {
    global: [
      {
        roleRules: [
          { resources: ['clusters'], verbs: ['list', 'read'] },
          { resources: ['argocd'], verbs: ['read'] },
        ],
      },
    ],
    cluster: [],
    project: [],
  },
} satisfies SmokeUser;

const cluster = {
  id: 'cluster-1',
  name: 'prod-east',
  displayName: 'Prod East',
  description: 'Production cluster',
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

const catalogChart = {
  id: 'chart-prometheus',
  repositoryId: 'repo-prometheus',
  repositoryName: 'Prometheus Community',
  name: 'kube-prometheus-stack',
  displayName: 'Kube Prometheus Stack',
  description: 'Prometheus, Grafana, and alerting for Kubernetes.',
  category: 'monitoring',
  keywords: ['monitoring', 'prometheus'],
  homeUrl: 'https://prometheus.io',
  iconUrl: '',
  sources: [],
  maintainers: [],
  createdAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
};

const catalogVersion = {
  id: 'version-prometheus-1',
  chartId: catalogChart.id,
  version: '61.0.0',
  appVersion: '0.75.0',
  defaultValues: 'grafana:\n  enabled: true\n',
  valuesSchema: null,
  readme: 'Install kube-prometheus-stack.',
  createdAt: new Date().toISOString(),
};

function apiResponse<T>(data: T) {
  return { status: 200, data };
}

function paginated<T>(data: T[]) {
  return { data, total: data.length, page: 1, pageSize: 100, totalPages: 1 };
}

async function mockApi(page: Page, user = adminUser) {
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname.replace(/^\/api\/v1/, '').replace(/\/$/, '') || '/';
    const method = request.method();

    if (path === '/events/stream') {
      return route.fulfill({ status: 204, body: '' });
    }
    if (path === '/auth/sso/providers' && method === 'GET') {
      return route.fulfill({ json: apiResponse([]) });
    }
    if (path === '/auth/login' && method === 'POST') {
      return route.fulfill({
        headers: {
          'set-cookie': `${SESSION_COOKIE}=e2e-session; Path=/; SameSite=Lax, ${CSRF_COOKIE}=e2e-csrf; Path=/; SameSite=Lax`,
        },
        json: apiResponse({ token: 'unused-cookie-auth-token', refresh: 'unused-refresh', user: adminUser }),
      });
    }
    if (path === '/auth/logout' && method === 'POST') {
      return route.fulfill({
        headers: {
          'set-cookie': `${SESSION_COOKIE}=; Path=/; Max-Age=0; SameSite=Lax`,
        },
        json: apiResponse({ ok: true }),
      });
    }
    if (path === '/auth/me') {
      return route.fulfill({ json: apiResponse(user) });
    }
    if (path === '/settings/features') {
      return route.fulfill({
        json: apiResponse({
          'feature.catalog': true,
          'feature.projects': true,
          'feature.monitoring': true,
          'feature.argocd': true,
          'feature.security': true,
          'feature.backups': true,
        }),
      });
    }
    if (path === '/clusters' && method === 'GET') {
      return route.fulfill({ json: paginated([cluster]) });
    }
    if (path === '/clusters' && method === 'POST') {
      return route.fulfill({ json: apiResponse({ ...cluster, id: 'cluster-new', name: 'e2e-cluster', displayName: 'E2E Cluster' }) });
    }
    if (path === '/clusters/cluster-new/registration/options' && method === 'PUT') {
      return route.fulfill({ json: apiResponse({ phase: 'created', installBaseline: false, steps: [] }) });
    }
    if (path === '/clusters/cluster-1') {
      return route.fulfill({ json: apiResponse(cluster) });
    }
    if (path.startsWith('/clusters/cluster-1/')) {
      return route.fulfill({ json: apiResponse([]) });
    }
    if (path === '/activity' || path === '/alerting/events' || path === '/tools') {
      return route.fulfill({ json: apiResponse([]) });
    }
    if (path === '/catalog/repositories') {
      return route.fulfill({ json: apiResponse([]) });
    }
    if (path === '/catalog/charts') {
      return route.fulfill({ json: apiResponse([catalogChart]) });
    }
    if (path === `/catalog/charts/${catalogChart.id}/versions`) {
      return route.fulfill({ json: apiResponse([catalogVersion]) });
    }
    if (path === '/catalog/installed' && method === 'GET') {
      return route.fulfill({ json: apiResponse([]) });
    }
    if (path === '/catalog/installed' && method === 'POST') {
      return route.fulfill({
        json: apiResponse({
          id: 'installed-prometheus',
          releaseName: 'kube-prometheus-stack',
          chartName: catalogChart.name,
          chartVersionLabel: catalogVersion.version,
          clusterId: cluster.id,
          clusterName: cluster.displayName,
          namespace: 'monitoring',
          status: 'pending',
          revision: 1,
          installedBy: user.username,
          createdAt: new Date().toISOString(),
        }),
      });
    }
    if (path === '/settings/general') {
      if (method === 'PUT') {
        return route.fulfill({ json: apiResponse(await request.postDataJSON()) });
      }
      return route.fulfill({
        json: apiResponse({
          platformName: 'Astronomer',
          agentHeartbeatInterval: 30,
          defaultSessionTimeout: 60,
          enableAuditLogging: true,
          metricsCollection: true,
        }),
      });
    }
    if (path === '/settings/sso' || path === '/settings/tokens') {
      return route.fulfill({ json: apiResponse([]) });
    }
    if (path === '/audit' || path === '/settings/audit-logs') {
      return route.fulfill({ json: paginated([]) });
    }
    if (path === '/admin/backup-drill') {
      return route.fulfill({ status: 404, json: { code: 'not_found', message: 'No drill has run' } });
    }
    if (path === '/argocd/instances') {
      return route.fulfill({
        json: apiResponse([
          {
            id: 'argocd-1',
            name: 'Built-in ArgoCD',
            apiUrl: 'https://argocd.example.test',
            isHealthy: true,
            verifySsl: true,
            createdAt: new Date().toISOString(),
          },
        ]),
      });
    }

    return route.fulfill({ json: apiResponse([]) });
  });
}

test.beforeEach(async ({ page }) => {
  await mockApi(page);
});

test('redirects unauthenticated dashboard users and supports login/logout', async ({ page }) => {
  await page.goto('/dashboard');
  await expect(page).toHaveURL(/\/auth\/login/);
  await expect(page.getByRole('heading', { name: /sign in to astronomer/i })).toBeVisible();

  await page.getByPlaceholder('you@example.com').fill('admin@example.com');
  await page.getByPlaceholder('Enter your password').fill('password');
  await page.getByRole('button', { name: /sign in/i }).click();

  await expect(page).toHaveURL(/\/dashboard$/);
  await expect(page.getByRole('heading', { name: /platform overview/i })).toBeVisible();

  await page.getByRole('button', { name: /user menu/i }).click();
  await page.getByRole('button', { name: /sign out/i }).click();
  await expect(page).toHaveURL(/\/auth\/login/);
});

test('cluster registration wizard creates a cluster and advances to connect step', async ({ context, page }) => {
  await seedAuth(context, page, adminUser);
  await page.goto('/dashboard/clusters/register');
  await expect(page.getByRole('heading', { name: /register an existing cluster/i })).toBeVisible();

  await page.getByPlaceholder('my-cluster').fill('e2e-cluster');
  await page.getByPlaceholder('My Production Cluster').fill('E2E Cluster');
  await page.getByRole('button', { name: /next: get install command/i }).click();

  await expect(page).toHaveURL(/\/dashboard\/clusters\/register\/cluster-new\/connect/);
});

test('read-only cluster detail hides admin-only settings navigation', async ({ context, page }) => {
  await mockApi(page, readOnlyUser);
  await seedAuth(context, page, readOnlyUser);
  await page.goto('/dashboard/clusters/cluster-1');

  await expect(page.getByRole('heading', { name: /prod east/i })).toBeVisible();
  await expect(page.getByRole('link', { name: /^Auth$/ })).toHaveCount(0);
});

test('ArgoCD page lists registered instances for authenticated users', async ({ context, page }) => {
  await seedAuth(context, page, adminUser);
  await page.goto('/dashboard/argocd');

  await expect(page.getByRole('heading', { name: /^ArgoCD$/ })).toBeVisible();
  await expect(page.getByText('Built-in ArgoCD')).toBeVisible();
});

test('catalog install modal remains usable on responsive viewports', async ({ context, page }) => {
  await seedAuth(context, page, adminUser);
  await page.goto('/dashboard/catalog');

  await expect(page.getByRole('heading', { name: /^Catalog$/ })).toBeVisible();
  await page.getByRole('button', { name: /kube prometheus stack/i }).click();
  await expect(page.getByRole('heading', { name: /kube prometheus stack/i })).toBeVisible();
  await page.getByRole('button', { name: /^Install$/ }).click();

  await expect(page.getByRole('heading', { name: /install kube prometheus stack/i })).toBeVisible();
  await page.getByLabel('Target Cluster').selectOption(cluster.id);
  await page.getByLabel('Release Name').fill('platform-monitoring');
  await page.getByLabel('Namespace').fill('monitoring');
  await expect(page.getByRole('button', { name: /install chart/i })).toBeEnabled();
  await page.getByRole('button', { name: /install chart/i }).click();
});

test('settings general form remains usable on responsive viewports', async ({ context, page }) => {
  await seedAuth(context, page, adminUser);
  await page.goto('/dashboard/settings/general');

  await expect(page.getByRole('heading', { name: /^Settings$/ })).toBeVisible();
  await page.getByRole('button', { name: /^General$/ }).click();
  await page.getByLabel('Platform Name').fill('Astronomer Control Plane');
  await page.getByLabel('Agent Heartbeat Interval').selectOption('60');
  await page.getByLabel('Default Session Timeout').selectOption('480');
  await page.getByRole('button', { name: /save settings/i }).click();
  await expect(page.getByLabel('Platform Name')).toHaveValue('Astronomer Control Plane');
});
