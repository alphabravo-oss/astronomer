import type { User } from '@/types';

type RoleRule = {
  resource?: string;
  resources?: string[];
  verb?: string;
  verbs?: string[];
};

type RoleBindingLike = {
  id?: string;
  roleId?: string;
  role_id?: string;
  roleName?: string;
  role_name?: string;
  roleRules?: RoleRule[];
  role_rules?: RoleRule[];
  clusterId?: string;
  cluster_id?: string;
  projectId?: string;
  project_id?: string;
};

type UserWithAuthz = User & {
  isSuperuser?: boolean;
  is_superuser?: boolean;
  roles?: {
    global?: RoleBindingLike[];
    cluster?: RoleBindingLike[];
    project?: RoleBindingLike[];
  };
};

export type PermissionVerb =
  | 'create'
  | 'read'
  | 'update'
  | 'delete'
  | 'list'
  | 'watch'
  | 'scale'
  | 'restart'
  | 'exec'
  | 'logs'
  | 'proxy'
  | 'sync'
  | 'manage';

export type PermissionScope =
  | { type: 'global' }
  | { type: 'cluster'; id?: string }
  | { type: 'project'; id?: string };

export type PermissionDecision = {
  allowed: boolean;
  permission: string;
  scope: PermissionScope;
  scopeLabel: string;
  reason: string;
  disabledReason?: string;
  grantedBy: string[];
  requestAccessHint: string;
};

export function isSuperuser(user: User | null | undefined): boolean {
  const u = user as UserWithAuthz | null | undefined;
  return Boolean(u?.isSuperuser || u?.is_superuser);
}

export function can(
  user: User | null | undefined,
  resource: string,
  verb: PermissionVerb | '*',
  scope: PermissionScope = { type: 'global' }
): boolean {
  return explainPermission(user, resource, verb, scope).allowed;
}

export function explainPermission(
  user: User | null | undefined,
  resource: string,
  verb: PermissionVerb | '*',
  scope: PermissionScope = { type: 'global' }
): PermissionDecision {
  const u = user as UserWithAuthz | null | undefined;
  const permission = `${resource}:${verb}`;
  const scopeLabel = describeScope(scope);
  const requestAccessHint = accessRequestHint(scope);

  if (!u) {
    const reason = `Sign in to use ${permission}.`;
    return {
      allowed: false,
      permission,
      scope,
      scopeLabel,
      reason,
      disabledReason: reason,
      grantedBy: [],
      requestAccessHint,
    };
  }

  if (isSuperuser(u)) {
    return {
      allowed: true,
      permission,
      scope,
      scopeLabel,
      reason: `Superuser access grants ${permission} ${scopePhrase(scope)}.`,
      grantedBy: ['Superuser'],
      requestAccessHint,
    };
  }

  const candidates = permissionCandidates(u, scope);
  const grantedBy = candidates
    .filter((candidate) =>
      roleRules(candidate.binding).some((rule) => ruleAllows(rule, resource, verb))
    )
    .map((candidate) => bindingLabel(candidate.binding, candidate.scope));

  if (grantedBy.length > 0) {
    return {
      allowed: true,
      permission,
      scope,
      scopeLabel,
      reason: `Granted by ${unique(grantedBy).join(', ')}.`,
      grantedBy: unique(grantedBy),
      requestAccessHint,
    };
  }

  const reason = `Requires ${permission} ${scopePhrase(scope)}. ${requestAccessHint}`;
  return {
    allowed: false,
    permission,
    scope,
    scopeLabel,
    reason,
    disabledReason: reason,
    grantedBy: [],
    requestAccessHint,
  };
}

function permissionCandidates(
  user: UserWithAuthz,
  scope: PermissionScope
): Array<{ binding: RoleBindingLike; scope: PermissionScope['type'] }> {
  return [
    ...(user.roles?.global ?? []).map((binding) => ({ binding, scope: 'global' as const })),
    ...(scope.type === 'cluster'
      ? scopedBindings(user.roles?.cluster, 'cluster', scope.id).map((binding) => ({
          binding,
          scope: 'cluster' as const,
        }))
      : []),
    ...(scope.type === 'project'
      ? scopedBindings(user.roles?.project, 'project', scope.id).map((binding) => ({
          binding,
          scope: 'project' as const,
        }))
      : []),
  ];
}

function scopedBindings(bindings: RoleBindingLike[] | undefined, kind: 'cluster' | 'project', id?: string): RoleBindingLike[] {
  if (!bindings?.length) return [];
  if (!id) return bindings;
  const idKey = kind === 'cluster' ? ['clusterId', 'cluster_id'] : ['projectId', 'project_id'];
  return bindings.filter((binding) => {
    const a = binding[idKey[0] as keyof RoleBindingLike];
    const b = binding[idKey[1] as keyof RoleBindingLike];
    return a === id || b === id;
  });
}

function roleRules(binding: RoleBindingLike): RoleRule[] {
  return binding.roleRules ?? binding.role_rules ?? [];
}

function ruleAllows(rule: RoleRule, resource: string, verb: PermissionVerb | '*'): boolean {
  const resources = rule.resources ?? (rule.resource ? [rule.resource] : []);
  const verbs = rule.verbs ?? (rule.verb ? [rule.verb] : []);
  return matches(resources, resource) && matches(verbs, verb);
}

function matches(values: string[], expected: string): boolean {
  return values.includes('*') || values.includes(expected);
}

function bindingLabel(binding: RoleBindingLike, scope: PermissionScope['type']): string {
  const name =
    binding.roleName ||
    binding.role_name ||
    binding.roleId ||
    binding.role_id ||
    binding.id ||
    'role binding';
  const target = scope === 'cluster'
    ? binding.clusterId || binding.cluster_id
    : scope === 'project'
      ? binding.projectId || binding.project_id
      : '';
  return target ? `${name} (${scope}:${target})` : name;
}

function describeScope(scope: PermissionScope): string {
  if (scope.type === 'global') return 'global';
  if (scope.id) return `${scope.type}:${scope.id}`;
  return scope.type;
}

function scopePhrase(scope: PermissionScope): string {
  if (scope.type === 'global') return 'globally';
  if (scope.id) return `for ${scope.type} ${scope.id}`;
  return `for ${scope.type} scope`;
}

function accessRequestHint(scope: PermissionScope): string {
  if (scope.type === 'cluster') return 'Request access from a cluster owner or platform administrator.';
  if (scope.type === 'project') return 'Request access from a project owner or platform administrator.';
  return 'Request access from a platform administrator.';
}

function unique(values: string[]): string[] {
  return Array.from(new Set(values.filter(Boolean)));
}
