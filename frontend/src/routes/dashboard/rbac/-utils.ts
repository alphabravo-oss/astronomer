import type { AccessBinding, Cluster, PolicyRule, Project, User } from '@/types';

export type RoleLike = {
  name: string;
  displayName?: string;
  description?: string;
  isBuiltin?: boolean;
  builtin?: boolean;
  rules?: PolicyRule[];
  createdAt?: string;
};

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

export function roleTitle(role: RoleLike): string {
  return role.displayName || role.name;
}

export function isBuiltinRole(role: RoleLike): boolean {
  return role.isBuiltin === true || role.builtin === true;
}

export function crdGrantCount(rules?: PolicyRule[]): number {
  return (rules ?? []).filter((rule) => (rule.apiGroups ?? rule.api_groups ?? []).length > 0).length;
}

export function userLabel(user: Pick<User, 'displayName' | 'username'>): string {
  return user.displayName || user.username;
}

export function clusterLabel(cluster: Pick<Cluster, 'displayName' | 'name'>): string {
  return cluster.displayName || cluster.name;
}

export function projectLabel(project: Pick<Project, 'displayName' | 'name'>): string {
  return project.displayName || project.name;
}

export function bindingSubject(binding: AccessBinding, users: User[]): string {
  if (binding.userId) {
    const user = users.find((u) => u.id === binding.userId);
    return user ? userLabel(user) : binding.userId;
  }
  return binding.group ? `group: ${binding.group}` : '—';
}

export function toAccessBinding(
  scope: AccessBinding['scope'],
  row: {
    id: string;
    userId?: string | null;
    user_id?: string | null;
    group?: string;
    roleId?: string;
    role_id?: string;
    clusterId?: string;
    cluster_id?: string;
    projectId?: string;
    project_id?: string;
    namespace?: string;
    createdAt?: string;
    created_at?: string;
  },
): AccessBinding {
  return {
    id: row.id,
    scope,
    userId: row.userId ?? row.user_id ?? null,
    group: row.group ?? '',
    roleId: row.roleId ?? row.role_id ?? '',
    clusterId: row.clusterId ?? row.cluster_id,
    projectId: row.projectId ?? row.project_id,
    namespace: row.namespace,
    createdAt: row.createdAt ?? row.created_at ?? '',
  };
}

export function bindingTarget(
  binding: AccessBinding,
  clusters: Cluster[],
  projects: Project[],
): string {
  if (binding.scope === 'global') return 'platform';
  if (binding.scope === 'project') {
    const project = projects.find((p) => p.id === binding.projectId);
    return project ? projectLabel(project) : binding.projectId || '—';
  }
  const cluster = clusters.find((c) => c.id === binding.clusterId);
  const name = cluster ? clusterLabel(cluster) : binding.clusterId || 'cluster';
  return binding.namespace ? `${name} / ${binding.namespace}` : name;
}
