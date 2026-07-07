import { adminUserHref, isUserLocked } from './page';

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
