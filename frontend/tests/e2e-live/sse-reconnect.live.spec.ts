import { expect, test } from '@playwright/test';

import { createClusterViaApi, csrfToken, loginViaForm, waitForStreamOpen } from './live.helpers';

// Live tier (P7.2): the resume story. Invalidate-on-reconnect (not
// Last-Event-ID replay) is how the client heals missed events, so this is
// the spec that fails if resume is broken: after an offline blip the stream
// must re-mint a single-use ticket, reconnect, and bulk-invalidate active
// queries — a cluster created around the blip appears either via the fresh
// stream's cluster.created or via the reconnect invalidation refetch.

test('stream recovers from an offline blip: re-mint + invalidate-on-reconnect', async ({
  page,
  context,
}) => {
  await loginViaForm(page);

  const streamOpen = waitForStreamOpen(page);
  await page.goto('/dashboard/clusters');
  await streamOpen;

  // Sever the network long enough for the EventSource to error and the
  // stream to enter its 1s→30s backoff loop.
  await context.setOffline(true);
  await page.waitForTimeout(3000);
  await context.setOffline(false);

  // The API create is out-of-band (context.request runs outside the
  // browser's offline emulation, and the network is back by now anyway).
  const name = `e2e-live-reconnect-${Date.now()}`;
  await createClusterViaApi(context.request, await csrfToken(context), name);

  // ≤15s covers the early backoff steps (1s → 2s → 4s) plus one refetch.
  await expect(page.getByText(name).first()).toBeVisible({ timeout: 15_000 });
});
