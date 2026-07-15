/**
 * P7.1 — Negative tests that keep the crawl's detectors honest: if the
 * not-found boundary or the error boundary stopped rendering (or lost their
 * testids), the positive crawl would pass vacuously — these two tests fail
 * instead.
 */
import { expect, test } from '@playwright/test';
import { seedAuth } from '../e2e/helpers/auth';
import { adminStoreUser, SMOKE_CLUSTER_ID } from './stub-overrides';
import { installStubs } from './stubs';

test('a garbage URL renders the not-found boundary', async ({ page, context }) => {
  await seedAuth(context, page, adminStoreUser);
  await installStubs(page);
  await page.goto('/dashboard/definitely-not-a-real-route');
  // The dashboard chrome stays mounted around the 404 panel.
  await expect(page.getByTestId('app-shell')).toBeVisible();
  await expect(page.getByTestId('route-not-found')).toBeVisible();
});

test('a page render crash renders the error boundary', async ({ page, context }) => {
  await seedAuth(context, page, adminStoreUser);
  // Force a render crash: shrink the template detail body to a bare object.
  // (A forced 500 cannot trip a boundary here — no query uses throwOnError,
  // so error states render as empty panels; the boundary detector fires on
  // RENDER crashes, which is exactly what malformed 200 data produces.)
  await installStubs(page, [
    {
      method: 'GET',
      path: `/api/v1/cluster-templates/${SMOKE_CLUSTER_ID}`,
      body: { data: { id: SMOKE_CLUSTER_ID } },
    },
  ]);
  await page.goto(`/dashboard/cluster-templates/${SMOKE_CLUSTER_ID}`);
  await expect(page.getByTestId('route-error-boundary')).toBeVisible();
});
