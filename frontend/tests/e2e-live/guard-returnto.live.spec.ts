import { expect, test } from '@playwright/test';

import { submitLoginForm } from './live.helpers';

// Live tier (P7.2): the D3 deep-link contract end-to-end. An unauthenticated
// deep link WITH a ?tab= search param must survive the guard → login →
// sanitizeReturnTo round-trip byte-for-byte — this is the flow that breaks
// if the guard captures pathname instead of href, or the login page strips
// unknown search params.

const DEEP_LINK = '/dashboard/alerting?tab=channels';

test('unauthenticated ?tab= deep link returns to the exact URL after login', async ({ page }) => {
  await page.goto(DEEP_LINK);

  // Guard bounce: returnTo carries the full href including the tab param.
  await expect(page).toHaveURL(/\/auth\/login\?/);
  const returnTo = new URL(page.url()).searchParams.get('returnTo');
  expect(returnTo).toBe(DEEP_LINK);

  await submitLoginForm(page);

  // Back at the deep link, tab intact — not the /dashboard default.
  await expect(page).toHaveURL(/\/dashboard\/alerting\?tab=channels/);
  await expect(page.getByTestId('app-shell')).toBeVisible();
  // The tab param is honored by the page, not just echoed in the URL: the
  // "Add Channel" action only renders when the channels tab is active.
  await expect(page.getByRole('button', { name: 'Add Channel' })).toBeVisible();
});
