/**
 * TEST-02: mocked permission-gate E2E — reader cannot see destructive controls.
 */
import { test, expect } from '@playwright/test';

test.describe('permission-gated UI', () => {
  test.beforeEach(async ({ page }) => {
    // Minimal mock of /auth/me as a non-superuser with no write grants.
    await page.route('**/api/v1/**', async (route) => {
      const url = route.request().url();
      if (url.includes('/auth/me') || url.endsWith('/me/') || url.includes('/auth/me/')) {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            id: 'u1',
            email: 'reader@example.com',
            is_superuser: false,
            isSuperuser: false,
            roles: {
              global: [
                {
                  roleRules: [{ resource: 'clusters', verbs: ['read', 'list'] }],
                },
              ],
            },
          }),
        });
        return;
      }
      if (url.includes('/backups')) {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ data: [], count: 0 }),
        });
        return;
      }
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ data: [] }),
      });
    });
  });

  test('backups page hides Add Storage for readers without backups write', async ({ page }) => {
    await page.goto('/dashboard/backups');
    // Reader without backups:update should not see primary create.
    await expect(page.getByRole('button', { name: /Add Storage/i })).toHaveCount(0);
  });
});
