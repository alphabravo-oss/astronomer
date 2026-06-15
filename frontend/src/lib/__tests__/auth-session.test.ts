import {
  clearLegacyTokenStorage,
  LEGACY_ACCESS_TOKEN_KEY,
  LEGACY_REFRESH_TOKEN_KEY,
  SESSION_COOKIE,
} from '@/lib/auth/session';

describe('auth session helpers', () => {
  it('exports the canonical browser session cookie name', () => {
    expect(SESSION_COOKIE).toBe('astronomer_session');
  });

  it('clears legacy token storage keys', () => {
    const removed: string[] = [];
    clearLegacyTokenStorage({
      removeItem: (key: string) => {
        removed.push(key);
      },
    });

    expect(removed).toEqual([LEGACY_ACCESS_TOKEN_KEY, LEGACY_REFRESH_TOKEN_KEY]);
  });

  it('swallows storage failures because cookie auth is authoritative', () => {
    expect(() =>
      clearLegacyTokenStorage({
        removeItem: () => {
          throw new Error('blocked');
        },
      }),
    ).not.toThrow();
  });
});
