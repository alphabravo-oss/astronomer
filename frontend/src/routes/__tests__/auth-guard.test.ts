/**
 * Dashboard auth guard (P2.1/P2.4): the `beforeLoad` cookie-presence check is
 * the entire guard — it must bounce unauthenticated navigations to
 * `/auth/login` carrying the exact deep link (path + search) as `returnTo`,
 * and let cookie-holding sessions straight through. `must_change_password`
 * is deliberately NOT here: it is async query data, asserted at the
 * layout-component level, never in the guard.
 */
import { beforeEach, describe, expect, it } from 'vitest';
import { createMemoryHistory, createRouter } from '@tanstack/react-router';
import { routeTree } from '@/routeTree.gen';
import { CSRF_COOKIE } from '@/lib/auth/session';

function buildRouter(initialUrl: string) {
  return createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [initialUrl] }),
  });
}

function clearSessionHint() {
  document.cookie = `${CSRF_COOKIE}=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/`;
}

function setSessionHint() {
  document.cookie = `${CSRF_COOKIE}=test-token; path=/`;
}

describe('dashboard auth guard', () => {
  beforeEach(() => {
    clearSessionHint();
  });

  it('redirects unauthenticated navigations to login with the exact returnTo (incl. search)', async () => {
    const deepLink = '/dashboard/clusters/c1/apps?install=grafana&section=monitoring';
    const router = buildRouter(deepLink);
    await router.load();

    expect(router.state.location.pathname).toBe('/auth/login');
    expect((router.state.location.search as { returnTo?: string }).returnTo).toBe(deepLink);
  });

  it('redirects a bare dashboard navigation with its path as returnTo', async () => {
    const router = buildRouter('/dashboard');
    await router.load();

    expect(router.state.location.pathname).toBe('/auth/login');
    expect((router.state.location.search as { returnTo?: string }).returnTo).toBe('/dashboard');
  });

  it('lets an authenticated session through', async () => {
    setSessionHint();
    const router = buildRouter('/dashboard/clusters');
    await router.load();

    expect(router.state.location.pathname).toBe('/dashboard/clusters');
  });

  it('does not guard auth routes', async () => {
    const router = buildRouter('/auth/login');
    await router.load();

    expect(router.state.location.pathname).toBe('/auth/login');
    expect((router.state.location.search as { returnTo?: string }).returnTo).toBeUndefined();
  });
});
