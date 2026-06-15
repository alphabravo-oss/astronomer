import { expect, test, type BrowserContext, type Page } from '@playwright/test';

// Confirms DataTable server-side mode (B4) against the real Audit page: each
// page is a separate offset-based fetch, and the table footer reports the
// server total — not the number of loaded rows.

const SESSION_COOKIE = 'astronomer_session';
const CSRF_COOKIE = 'astronomer_csrf';
const TOTAL = 125;
const PAGE_SIZE = 50;

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

function auditPage(offset: number, limit: number) {
  const end = Math.min(offset + limit, TOTAL);
  const data = [];
  for (let idx = offset; idx < end; idx++) {
    data.push({
      id: `audit-${idx}`,
      createdAt: new Date(1700000000000 + idx * 1000).toISOString(),
      timestamp: new Date(1700000000000 + idx * 1000).toISOString(),
      user: `user-${idx}`,
      action: `action.${idx}`,
      actionClass: 'mutation',
      resourceName: `res-${idx}`,
      resourceType: 'cluster',
      result: 'success',
      source: 'web',
    });
  }
  return { data, count: TOTAL };
}

async function mockApi(page: Page) {
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname.replace(/^\/api\/v1/, '').replace(/\/$/, '') || '/';

    if (path === '/events/stream') return route.fulfill({ status: 204, body: '' });
    if (path === '/auth/me') return route.fulfill({ json: { status: 200, data: adminUser } });
    if (path === '/settings/features') return route.fulfill({ json: { status: 200, data: {} } });
    if (path === '/audit') {
      const offset = Number(url.searchParams.get('offset') ?? '0');
      const limit = Number(url.searchParams.get('limit') ?? String(PAGE_SIZE));
      return route.fulfill({ json: auditPage(offset, limit) });
    }
    return route.fulfill({ json: { status: 200, data: [] } });
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

test('DataTable server-side: audit paginates via per-page offset requests (B4)', async ({ context, page }) => {
  const offsets: number[] = [];
  page.on('request', (req) => {
    const u = new URL(req.url());
    if (u.pathname.replace(/\/$/, '').endsWith('/audit')) {
      offsets.push(Number(u.searchParams.get('offset') ?? '0'));
    }
  });

  await authenticate(context, page);
  await page.goto('/dashboard/audit');

  // Footer reports the SERVER total (125), not the 50 loaded rows.
  await expect(page.getByText('Showing 1-50 of 125')).toBeVisible();
  await expect(page.getByText('action.0', { exact: true })).toBeVisible();

  // Page 2 → a fresh request with offset=50 → different rows.
  await page.getByRole('button', { name: '2' }).click();
  await expect(page.getByText('Showing 51-100 of 125')).toBeVisible();
  await expect(page.getByText('action.50', { exact: true })).toBeVisible();
  await expect(page.getByText('action.0', { exact: true })).toHaveCount(0);

  // Confirm the offset=50 request actually went to the server (true server-side).
  expect(offsets).toContain(50);
});
