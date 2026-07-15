import { expect, test } from '@playwright/test';

import { createClusterViaApi, csrfToken, loginViaForm, waitForStreamOpen } from './live.helpers';

// Live tier (P7.2): create a cluster via the real API and watch its row
// appear in the clusters list WITHOUT navigating. Proves the whole live
// pipeline: ticket mint → EventSource → Go bus publish (cluster.created) →
// dispatch → query-cache invalidation → refetch → render.

test('cluster created via API appears in the list over SSE', async ({ page, context }) => {
  await loginViaForm(page);

  // Full page load of /dashboard/clusters remounts the layout, so a fresh
  // stream connects; awaiting it means the POST below races nothing.
  const streamOpen = waitForStreamOpen(page);
  await page.goto('/dashboard/clusters');
  await streamOpen;

  const name = `e2e-live-sse-${Date.now()}`;
  await createClusterViaApi(context.request, await csrfToken(context), name);

  // ≤10s: with the stream open, liveAwareRefetchInterval stretches the list
  // poll to ≥60s — an appearance inside 10s can only be the SSE path.
  await expect(page.getByText(name).first()).toBeVisible({ timeout: 10_000 });
});
