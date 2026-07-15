import { expect, type APIRequestContext, type BrowserContext, type Page } from '@playwright/test';

// Live-tier helpers: every call here hits the REAL Go backend through the
// preview proxy — no route stubbing, no cookie seeding. The bootstrap admin
// comes from EnsureBootstrapAdmin (internal/auth/ensure_admin.go): default
// identity admin/admin@astronomer.local, password from
// ASTRONOMER_BOOTSTRAP_PASSWORD (the CI job sets e2e-live-password).

export const ADMIN_EMAIL = process.env.LIVE_ADMIN_EMAIL || 'admin@astronomer.local';
export const ADMIN_PASSWORD = process.env.LIVE_ADMIN_PASSWORD || 'e2e-live-password';

export const SESSION_COOKIE = 'astronomer_session';
export const CSRF_COOKIE = 'astronomer_csrf';
export const CSRF_HEADER = 'X-CSRF-Token';

/**
 * Fill and submit the real credentials form. Does not wait for navigation.
 *
 * Rate-limit aware: /auth/login/ sits behind a fixed-window 5/min/IP limiter
 * (internal/server/middleware/login_rate_limit.go) and every live spec —
 * plus every `retries: 1` re-attempt — logs in as the same admin from the
 * same IP, so back-to-back runs can eat the window. On a 429 we wait out
 * the advertised Retry-After and resubmit instead of failing the spec.
 */
export async function submitLoginForm(page: Page) {
  await page.locator('#identifier').fill(ADMIN_EMAIL);
  await page.locator('#password').fill(ADMIN_PASSWORD);
  for (let attempt = 0; attempt < 3; attempt++) {
    const loginResponse = page.waitForResponse(
      (res) => res.url().endsWith('/auth/login/') && res.request().method() === 'POST',
    );
    await page.getByRole('button', { name: 'Sign in' }).click();
    const res = await loginResponse;
    if (res.status() !== 429) return;
    const retryAfter = Number(res.headers()['retry-after']) || 60;
    await page.waitForTimeout((retryAfter + 1) * 1000);
  }
  throw new Error('login still rate-limited after 3 attempts');
}

/** UI login from a fresh context: /auth/login → dashboard. */
export async function loginViaForm(page: Page) {
  await page.goto('/auth/login');
  await submitLoginForm(page);
  await expect(page).toHaveURL(/\/dashboard/);
}

/** Read the JS-readable CSRF double-submit cookie the backend set on login. */
export async function csrfToken(context: BrowserContext): Promise<string> {
  const csrf = (await context.cookies()).find((c) => c.name === CSRF_COOKIE);
  expect(csrf, 'readable CSRF cookie must exist after login').toBeTruthy();
  return csrf!.value;
}

/**
 * Create a cluster via the real API. `context.request` shares the browser
 * context's cookie jar, so the HttpOnly session cookie rides along; the
 * CSRF token goes in the header exactly like the axios interceptor does.
 */
export async function createClusterViaApi(request: APIRequestContext, csrf: string, name: string) {
  const res = await request.post('/api/v1/clusters/', {
    headers: { [CSRF_HEADER]: csrf },
    data: {
      name,
      display_name: name,
      description: 'created by the live e2e tier',
      environment: 'development',
      provider: 'other',
    },
  });
  expect(res.ok(), `cluster create failed: ${res.status()} ${await res.text()}`).toBe(true);
}

/**
 * Resolve when the SSE stream actually connects (ticket mint → EventSource
 * response headers). Register BEFORE the page.goto that mounts the dashboard
 * layout, then await after, so event publication races nothing.
 */
export function waitForStreamOpen(page: Page) {
  return page.waitForResponse(
    (res) => res.url().includes('/api/v1/events/stream/') && res.status() === 200,
  );
}
