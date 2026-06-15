export const SESSION_COOKIE = 'astronomer_session';
export const LEGACY_ACCESS_TOKEN_KEY = 'astronomer_token';
export const LEGACY_REFRESH_TOKEN_KEY = 'astronomer_refresh';

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
