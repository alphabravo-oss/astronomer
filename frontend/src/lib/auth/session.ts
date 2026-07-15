export const SESSION_COOKIE = 'astronomer_session';
export const CSRF_COOKIE = 'astronomer_csrf';
export const LEGACY_ACCESS_TOKEN_KEY = 'astronomer_token';
export const LEGACY_REFRESH_TOKEN_KEY = 'astronomer_refresh';

/**
 * Synchronous session presence hint for the dashboard route guard. The CSRF
 * cookie is JS-readable and set/cleared in lockstep with the HttpOnly session
 * cookie (internal/handler/auth.go setBrowserSessionCookies /
 * clearBrowserSessionCookies), so its presence mirrors the old middleware's
 * `astronomer_session` check with zero network.
 */
export function hasSessionHint(): boolean {
  if (typeof document === 'undefined') return false;
  return document.cookie.split('; ').some((cookie) => cookie.startsWith(`${CSRF_COOKIE}=`));
}

type TokenStorage = Pick<Storage, 'removeItem'>;

function browserStorage(): TokenStorage | null {
  if (typeof window === 'undefined') return null;
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

export function clearLegacyTokenStorage(storage: TokenStorage | null = browserStorage()): void {
  if (!storage) return;
  try {
    storage.removeItem(LEGACY_ACCESS_TOKEN_KEY);
    storage.removeItem(LEGACY_REFRESH_TOKEN_KEY);
  } catch {
    // Storage may be blocked in hardened browser profiles. Cookie auth still works.
  }
}
