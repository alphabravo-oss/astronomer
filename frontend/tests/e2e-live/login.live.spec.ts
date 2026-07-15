import { expect, test } from '@playwright/test';

import { CSRF_COOKIE, SESSION_COOKIE, loginViaForm } from './live.helpers';

// Live tier (P7.2): bootstrap admin → HttpOnly cookie session → dashboard →
// logout → guard bounces. End-to-ends the real credential path the mocked
// tier can only stub: bcrypt verify, cookie issuance, revocation on logout.

test('bootstrap admin login issues HttpOnly session, logout revokes it', async ({
  page,
  context,
}) => {
  await loginViaForm(page);
  await expect(page.getByTestId('app-shell')).toBeVisible();

  // The session cookie is HttpOnly: present in the context's jar, invisible
  // to page JS. The CSRF double-submit cookie is deliberately JS-readable.
  const cookies = await context.cookies();
  const session = cookies.find((c) => c.name === SESSION_COOKIE);
  expect(session, 'backend must set the session cookie').toBeTruthy();
  expect(session!.httpOnly).toBe(true);
  const csrf = cookies.find((c) => c.name === CSRF_COOKIE);
  expect(csrf, 'backend must set the CSRF cookie').toBeTruthy();
  expect(csrf!.httpOnly).toBe(false);
  const documentCookie = await page.evaluate(() => document.cookie);
  expect(documentCookie).not.toContain(`${SESSION_COOKIE}=`);

  // Logout via the user menu: POST /auth/logout revokes server-side and
  // clears both cookies; the SPA lands back on the login page.
  await page.getByRole('button', { name: 'User menu' }).click();
  await page.getByRole('button', { name: 'Sign out' }).click();
  await expect(page).toHaveURL(/\/auth\/login/);

  // Guard bounce: with the cookies gone, a dashboard deep link redirects to
  // login with the attempted location captured in returnTo.
  await page.goto('/dashboard');
  await expect(page).toHaveURL(/\/auth\/login\?.*returnTo=/);

  // Logout bumps the per-user revocation cutoff, and JWT iat is
  // second-precision with iat <= cutoff rejected (internal/auth/jwt.go
  // checkRevocations) — a login by the NEXT spec inside the same wall-clock
  // second would mint an already-revoked session. Let the cutoff second
  // elapse before handing the (serial) worker to the next spec.
  await page.waitForTimeout(1100);
});
