import {
  clearLegacyTokenStorage,
  LEGACY_ACCESS_TOKEN_KEY,
  LEGACY_REFRESH_TOKEN_KEY,
  sanitizeReturnTo,
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

describe('sanitizeReturnTo (D3 open-redirect guard)', () => {
  it('honors same-origin absolute paths, including query strings', () => {
    expect(sanitizeReturnTo('/dashboard/clusters/c1?tab=nodes')).toBe(
      '/dashboard/clusters/c1?tab=nodes',
    );
  });

  it('rejects protocol-relative URLs', () => {
    expect(sanitizeReturnTo('//evil.example.com/dashboard')).toBe('/dashboard');
  });

  it('rejects absolute URLs', () => {
    expect(sanitizeReturnTo('https://evil.example.com')).toBe('/dashboard');
    expect(sanitizeReturnTo('javascript:alert(1)')).toBe('/dashboard');
  });

  it('falls back for missing or non-string values', () => {
    expect(sanitizeReturnTo(undefined)).toBe('/dashboard');
    expect(sanitizeReturnTo(null)).toBe('/dashboard');
    expect(sanitizeReturnTo(123)).toBe('/dashboard');
    expect(sanitizeReturnTo('')).toBe('/dashboard');
  });
});
