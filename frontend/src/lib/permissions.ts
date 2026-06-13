import type { User } from '@/types';

type RoleRule = {
  resource?: string;
  resources?: string[];
  verb?: string;
  verbs?: string[];
};

type RoleBindingLike = {
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
  const u = user as UserWithAuthz | null | undefined;
  if (!u) return false;
  if (isSuperuser(u)) return true;

  const groups: RoleBindingLike[] = [
    ...(u.roles?.global ?? []),
    ...(scope.type === 'cluster' ? scopedBindings(u.roles?.cluster, 'cluster', scope.id) : []),
    ...(scope.type === 'project' ? scopedBindings(u.roles?.project, 'project', scope.id) : []),
  ];

  return groups.some((binding) =>
    roleRules(binding).some((rule) => ruleAllows(rule, resource, verb))
  );
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
