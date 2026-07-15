/**
 * P5.2 pilot — mocked submit-body assertions for the two converted forms:
 *
 *  - credential edit (marker secret variant): an untouched stored secret is
 *    omitted from the PUT body so the backend keeps the existing ciphertext,
 *    while a rotated secret ships its typed value.
 *  - smtp (sentinel secret variant): the `__redacted__` sentinel never leaves
 *    the browser — updateSmtpConfig strips it before the PUT.
 */
import { expect, type Page, test } from '@playwright/test';

import { seedAuth } from './helpers/auth';

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

const awsSpec = {
  provider: 'aws',
  displayName: 'Amazon Web Services',
  description: 'Access key credentials',
  fields: [
    { name: 'accessKeyId', label: 'Access key ID', required: true, secret: true },
    { name: 'secretAccessKey', label: 'Secret access key', required: false, secret: true },
    { name: 'region', label: 'Region', required: true, secret: false },
  ],
};

// Wire shape: snake_case keys camelize into the spec's field names; the
// `__*_set` markers ride along exactly as the backend echoes them.
const credential = {
  id: 'cred-1',
  project_id: 'proj-1',
  name: 'my-aws-keys',
  provider: 'aws',
  description: 'prod keys',
  config: {
    access_key_id: '',
    secret_access_key: '',
    region: 'us-east-1',
    __access_key_id_set: true,
    __secret_access_key_set: true,
  },
  target_refs: [{ cluster_id: 'c1', namespaces: ['default'] }],
  created_at: new Date().toISOString(),
  updated_at: new Date().toISOString(),
};

const smtpConfig = {
  host: 'smtp.old.example.com',
  port: 587,
  username: 'ops',
  password: '__redacted__',
  from_address: 'no-reply@example.com',
  from_name: 'Astronomer',
  auth_mechanism: 'plain',
  encryption: 'starttls',
  require_tls: true,
  timeout_seconds: 30,
};

function apiResponse<T>(data: T) {
  return { status: 200, data };
}

async function mockApi(page: Page) {
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname.replace(/^\/api\/v1/, '').replace(/\/$/, '') || '/';
    const method = request.method();

    if (path === '/events/stream') {
      return route.fulfill({ status: 204, body: '' });
    }
    if (path === '/auth/me') {
      return route.fulfill({ json: apiResponse(adminUser) });
    }
    if (path === '/settings/features') {
      return route.fulfill({ json: apiResponse({ 'feature.projects': true }) });
    }
    if (path === '/cloud-credentials/providers') {
      return route.fulfill({ json: apiResponse([awsSpec]) });
    }
    if (path === '/projects/proj-1') {
      return route.fulfill({
        json: apiResponse({ id: 'proj-1', name: 'proj-one', displayName: 'Project One' }),
      });
    }
    if (path === '/projects/proj-1/cloud-credentials/cred-1') {
      if (method === 'PUT') {
        return route.fulfill({ json: apiResponse(credential) });
      }
      return route.fulfill({ json: apiResponse(credential) });
    }
    if (path === '/projects/proj-1/cloud-credentials') {
      return route.fulfill({ json: apiResponse([credential]) });
    }
    if (path === '/admin/smtp') {
      if (method === 'PUT') {
        return route.fulfill({ json: apiResponse(smtpConfig) });
      }
      return route.fulfill({ json: apiResponse(smtpConfig) });
    }
    if (path === '/admin/emails') {
      return route.fulfill({
        json: { data: [], total: 0, page: 1, pageSize: 25, totalPages: 0 },
      });
    }
    if (path === '/clusters') {
      return route.fulfill({
        json: { data: [], total: 0, page: 1, pageSize: 100, totalPages: 0 },
      });
    }
    return route.fulfill({ json: apiResponse([]) });
  });
}

test.describe('credential edit (marker secret variant)', () => {
  test('PUT body omits untouched stored secret, includes the rotated one, strips markers', async ({
    page,
    context,
  }) => {
    await mockApi(page);
    await seedAuth(context, page, adminUser);

    await page.goto('/dashboard/projects/proj-1/cloud-credentials/cred-1/edit');

    const nameInput = page.getByLabel(/Name/);
    await expect(nameInput).toHaveValue('my-aws-keys');
    await expect(nameInput).toBeDisabled();

    // Rotate exactly one secret; leave the other stored secret untouched.
    await page.getByLabel(/Access key ID/).fill('AKIA-ROTATED');

    const putRequest = page.waitForRequest(
      (req) => req.method() === 'PUT' && req.url().includes('/cloud-credentials/cred-1'),
    );
    await page.getByRole('button', { name: 'Save credential' }).click();
    const body = JSON.parse((await putRequest).postData() ?? '{}');

    expect(body).toEqual({
      name: 'my-aws-keys',
      provider: 'aws',
      description: 'prod keys',
      config: { accessKeyId: 'AKIA-ROTATED', region: 'us-east-1' },
      targetRefs: [{ clusterId: 'c1', namespaces: ['default'] }],
    });
    // The untouched stored secret and the echoed markers never ship.
    expect(body.config).not.toHaveProperty('secretAccessKey');
    expect(Object.keys(body.config).some((k) => /set$/i.test(k))).toBe(false);
  });
});

test.describe('smtp (sentinel secret variant)', () => {
  test('PUT body drops the __redacted__ sentinel unless a new password is typed', async ({
    page,
    context,
  }) => {
    await mockApi(page);
    await seedAuth(context, page, adminUser);

    await page.goto('/dashboard/settings/smtp');

    await expect(page.getByLabel('Host')).toHaveValue('smtp.old.example.com');
    await expect(page.getByText('Stored — type a new value to rotate')).toBeVisible();

    await page.getByLabel('Host').fill('smtp.new.example.com');

    const putRequest = page.waitForRequest(
      (req) => req.method() === 'PUT' && req.url().includes('/admin/smtp'),
    );
    await page.getByRole('button', { name: 'Save changes' }).click();
    const body = JSON.parse((await putRequest).postData() ?? '{}');

    expect(body.host).toBe('smtp.new.example.com');
    expect(body).not.toHaveProperty('password');
  });
});
