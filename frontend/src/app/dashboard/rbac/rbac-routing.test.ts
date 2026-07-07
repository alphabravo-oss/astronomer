import { adminUserHref, isUserLocked, isValidNamespace } from './page';

// F-03 regression: the RBAC users table must link each row to the admin
// user-security detail, and lock state must be derivable for the badge.

describe('adminUserHref', () => {
  it('targets the admin user-security detail route', () => {
    expect(adminUserHref('u-123')).toBe('/dashboard/admin/users/u-123');
    expect(adminUserHref('abc-def')).toBe('/dashboard/admin/users/abc-def');
  });
});

describe('isUserLocked', () => {
  it('is false when there is no lock timestamp', () => {
    expect(isUserLocked({})).toBe(false);
    expect(isUserLocked({ lockedUntil: null })).toBe(false);
  });

  it('is true when locked_until is in the future', () => {
    const future = new Date(Date.now() + 60_000).toISOString();
    expect(isUserLocked({ lockedUntil: future })).toBe(true);
    expect(isUserLocked({ locked_until: future })).toBe(true);
  });

  it('is false when the lock has already expired', () => {
    const past = new Date(Date.now() - 60_000).toISOString();
    expect(isUserLocked({ lockedUntil: past })).toBe(false);
  });

  it('is false for an unparseable timestamp', () => {
    expect(isUserLocked({ lockedUntil: 'not-a-date' })).toBe(false);
  });
});

// DIR-04: the cluster-binding create form gates submit on a DNS-1123 namespace
// (empty == cluster-wide), mirroring the backend validation on
// POST /rbac/cluster-role-bindings/.
describe('isValidNamespace', () => {
  it('allows an empty namespace (cluster-wide)', () => {
    expect(isValidNamespace('')).toBe(true);
  });

  it('accepts valid DNS-1123 labels', () => {
    expect(isValidNamespace('kube-system')).toBe(true);
    expect(isValidNamespace('default')).toBe(true);
    expect(isValidNamespace('a')).toBe(true);
    expect(isValidNamespace('ns1')).toBe(true);
  });

  it('rejects invalid labels', () => {
    expect(isValidNamespace('Bad_NS')).toBe(false);
    expect(isValidNamespace('UPPER')).toBe(false);
    expect(isValidNamespace('-leading')).toBe(false);
    expect(isValidNamespace('trailing-')).toBe(false);
    expect(isValidNamespace('has space')).toBe(false);
    expect(isValidNamespace('a'.repeat(64))).toBe(false);
  });
});
