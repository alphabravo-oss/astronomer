// --- User & Auth Types ---

export interface User {
  id: string;
  username: string;
  email: string;
  displayName: string;
  avatarUrl?: string;
  provider: "local" | "github" | "google" | "oidc" | "saml";
  globalRoles?: string[];
  isSuperuser?: boolean;
  is_superuser?: boolean;
  roles?: {
    global: UserRoleBinding[];
    cluster: UserRoleBinding[];
    project: UserRoleBinding[];
  };
  enabled: boolean;
  lastLogin: string;
  createdAt: string;
  // Set (to a future RFC3339 timestamp) while the account is locked out —
  // brute-force lock or MFA lockout. Surfaced as a "Locked" badge in the RBAC
  // users table so admins can find who needs unlocking. Backend emits
  // `locked_until`; the users API adapter maps it to `lockedUntil`.
  lockedUntil?: string | null;
  locked_until?: string | null;
  // True when an admin has forced a password rotation. The dashboard
  // middleware redirects any other route to /auth/change-password while this
  // is set. Backend emits the field as `must_change_password` (snake_case);
  // the user object is stored as-received, so callers read the snake_case key.
  must_change_password?: boolean;
  mustChangePassword?: boolean;
}

export interface UserRoleBinding {
  id: string;
  roleId?: string;
  role_id?: string;
  roleName?: string;
  role_name?: string;
  roleRules?: PolicyRule[];
  role_rules?: PolicyRule[];
  group?: string;
  clusterId?: string;
  cluster_id?: string;
  projectId?: string;
  project_id?: string;
}

export interface GlobalRole {
  id: string;
  name: string;
  displayName: string;
  description?: string;
  isBuiltin?: boolean;
  builtin?: boolean;
  rules?: PolicyRule[];
  createdAt: string;
}

export interface ClusterRole {
  id: string;
  name: string;
  displayName: string;
  description?: string;
  isBuiltin?: boolean;
  builtin?: boolean;
  rules?: PolicyRule[];
  createdAt: string;
}

export interface ProjectRole {
  id: string;
  name: string;
  displayName: string;
  description?: string;
  isBuiltin?: boolean;
  builtin?: boolean;
  rules?: PolicyRule[];
  createdAt: string;
}

export interface PolicyRule {
  resource?: string;
  resources?: string[];
  verbs: string[];
  apiGroups?: string[];
  api_groups?: string[];
  resourceNames?: string[];
}

export interface RoleBinding {
  id: string;
  name: string;
  roleType: "global" | "cluster" | "project";
  roleName: string;
  subjects: RoleBindingSubject[];
  scope?: {
    clusterId?: string;
    clusterName?: string;
    projectId?: string;
    projectName?: string;
  };
  createdAt: string;
}

export interface RoleBindingSubject {
  kind: "User" | "Group" | "ServiceAccount";
  name: string;
  namespace?: string;
}

// ClusterRoleBinding is the mapped GET/POST /rbac/cluster-role-bindings/
// row. Prefer AccessBinding in UI code. An empty `namespace` means cluster-wide.
export type BindingScope = "global" | "cluster" | "project";

export interface AccessBinding {
  id: string;
  scope: BindingScope;
  userId: string | null;
  group: string;
  roleId: string;
  clusterId?: string;
  projectId?: string;
  namespace?: string;
  createdAt: string;
}

/** @deprecated Use AccessBinding. Kept for existing call sites during the fold. */
export interface ClusterRoleBinding {
  id: string;
  userId?: string | null;
  user_id?: string | null;
  group: string;
  roleId?: string;
  role_id?: string;
  clusterId?: string;
  cluster_id?: string;
  namespace: string;
  createdAt?: string;
  created_at?: string;
}

export interface RBACEngineRule {
  resource: string;
  verbs: string[];
}

export interface EffectivePermissionSource {
  scope: string;
  bindingId?: string;
  roleId?: string;
  roleName?: string;
  clusterId?: string;
  projectId?: string;
  namespace?: string;
}

export interface EffectivePermissionGrant {
  resource: string;
  verb: string;
  appliesToContext?: boolean;
  inherited?: boolean;
  inheritedFrom?: string;
  sources: EffectivePermissionSource[];
}

export interface EffectivePermissionBinding {
  scope: string;
  bindingId?: string;
  roleId?: string;
  roleName?: string;
  group?: string;
  clusterId?: string;
  projectId?: string;
  namespace?: string;
  superuser?: boolean;
  rules?: RBACEngineRule[];
}

export interface EffectivePermissionContext {
  clusterId?: string;
  projectId?: string;
  namespace?: string;
  namespaceScopedBindingsSupported: boolean;
  warnings?: string[];
}

export interface EffectivePermissionResponse {
  subject: {
    userId: string;
    self: boolean;
  };
  superuser?: boolean;
  context: EffectivePermissionContext;
  bindings: EffectivePermissionBinding[];
  permissions: EffectivePermissionGrant[];
}

export interface PermissionPreviewRequest {
  scope: "global" | "cluster" | "project";
  roleId?: string;
  templateName?: string;
  rules?: RBACEngineRule[];
  clusterId?: string;
  projectId?: string;
}

export interface PermissionPreviewResponse {
  scope: string;
  roleId?: string;
  roleName?: string;
  templateName?: string;
  riskLevel: "low" | "medium" | "high" | "critical" | string;
  warnings?: string[];
  permissions: EffectivePermissionGrant[];
  sensitiveFlags: {
    wildcard: boolean;
    canMutate: boolean;
    canDelete: boolean;
    canExec: boolean;
    canProxy: boolean;
    canReadSecrets: boolean;
    canManageRbac: boolean;
    canRestore: boolean;
  };
}
