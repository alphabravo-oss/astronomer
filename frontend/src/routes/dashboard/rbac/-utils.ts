import type { User } from '@/types';

/** In-app route for the admin user-security detail (unlock, force-logout, ...). */
export function adminUserHref(userId: string): string {
  return `/dashboard/admin/users/${userId}`;
}

/** True while the account is locked out (locked_until is a future timestamp). */
export function isUserLocked(user: Pick<User, 'lockedUntil' | 'locked_until'>): boolean {
  const raw = user.lockedUntil ?? user.locked_until;
  if (!raw) return false;
  const until = Date.parse(raw);
  return Number.isFinite(until) && until > Date.now();
}

/**
 * DNS-1123 label check mirroring the backend's k8svalidation.IsDNS1123Label on
 * POST /rbac/cluster-role-bindings/. An empty value is valid client-side (it
 * means "cluster-wide"); a non-empty value must be a lowercase alphanumeric
 * label (dashes allowed internally), at most 63 characters.
 */
export function isValidNamespace(namespace: string): boolean {
  if (namespace === '') return true;
  return namespace.length <= 63 && /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(namespace);
}
