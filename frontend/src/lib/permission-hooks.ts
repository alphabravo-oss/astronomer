import {
  explainPermission,
  type PermissionDecision,
  type PermissionScope,
  type PermissionVerb,
} from '@/lib/permissions';
import { useAuthStore } from '@/lib/store';

export function usePermissionDecision(
  resource: string,
  verb: PermissionVerb | '*',
  scope: PermissionScope = { type: 'global' }
): PermissionDecision {
  const user = useAuthStore((state) => state.user);
  return explainPermission(user, resource, verb, scope);
}
