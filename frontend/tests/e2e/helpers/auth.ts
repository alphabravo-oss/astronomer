import type { BrowserContext, Page } from '@playwright/test';

// Shared e2e auth seed: the session/CSRF cookies the middleware checks plus
// the persisted `astronomer-auth` zustand envelope the auth store hydrates
// from. The localStorage shape is load-bearing (see D21): keep it
// byte-compatible with the store's persist config.

export const SESSION_COOKIE = 'astronomer_session';
export const CSRF_COOKIE = 'astronomer_csrf';

export async function seedAuth(context: BrowserContext, page: Page, user: unknown) {
  await context.addCookies([
    { name: SESSION_COOKIE, value: 'e2e-session', domain: '127.0.0.1', path: '/' },
    { name: CSRF_COOKIE, value: 'e2e-csrf', domain: '127.0.0.1', path: '/' },
  ]);
  await page.addInitScript((storedUser) => {
    window.localStorage.setItem(
      'astronomer-auth',
      JSON.stringify({ state: { user: storedUser, isAuthenticated: true }, version: 2 }),
    );
  }, user);
}
