import {
  deleteRbacClusterRoleBindingsById,
  deleteRbacGlobalRoleBindingsById,
  deleteRbacProjectRoleBindingsById,
  getRbacClusterRoleBindings,
  getRbacClusterRoles,
  getRbacEffectivePermissionsByUserId,
  getRbacGlobalRoleBindings,
  getRbacGlobalRoles,
  getRbacMyPermissions,
  getRbacProjectRoleBindings,
  getRbacProjectRoles,
  postRbacClusterRoleBindings,
  postRbacClusterRoles,
  postRbacGlobalRoleBindings,
  postRbacGlobalRoles,
  postRbacPermissionPreview,
  postRbacProjectRoleBindings,
  postRbacProjectRoles,
} from "@/lib/api/generated/client";
import type {
  AccessBinding,
  ClusterRole,
  EffectivePermissionBinding,
  EffectivePermissionContext,
  EffectivePermissionGrant,
  EffectivePermissionResponse,
  EffectivePermissionSource,
  GlobalRole,
  PermissionPreviewRequest,
  PermissionPreviewResponse,
  PolicyRule,
  ProjectRole,
} from "@/types";
import type { OpenAPIComponents } from "@/types/openapi.generated";

type Schemas = OpenAPIComponents["schemas"];
type RoleWire = Schemas["RBACRole"];
type RuleWire = Schemas["RBACRule"];
type GlobalBindingWire = Schemas["RBACGlobalRoleBinding"];
type ClusterBindingWire = Schemas["RBACClusterRoleBinding"];
type ProjectBindingWire = Schemas["RBACProjectRoleBinding"];
type EffectiveWire = Schemas["RBACEffectivePermissions"];
type PreviewWire = Schemas["RBACPermissionPreview"];

const RBAC_LIST_LIMIT = 200;

function requiredString(value: string | undefined, field: string): string {
  if (!value) throw new Error(`RBAC API response omitted ${field}`);
  return value;
}

function requireData<T>(response: { data?: T }, operation: string): T {
  if (response.data === undefined) {
    throw new Error(`${operation} returned no data payload`);
  }
  return response.data;
}

function mapRule(wire: RuleWire): PolicyRule {
  return {
    resource: wire.resource,
    resources: wire.resources,
    verbs: wire.verbs,
    apiGroups: wire.api_groups,
  };
}

/** Deliberate raw-wire to UI mapping; RBAC never uses global camelization. */
export function mapRole(wire: RoleWire): GlobalRole {
  return {
    id: requiredString(wire.id, "role.id"),
    name: requiredString(wire.name, "role.name"),
    displayName: wire.display_name || wire.name || "",
    description: wire.description,
    isBuiltin: wire.is_builtin ?? false,
    builtin: wire.is_builtin ?? false,
    rules: (wire.rules ?? []).map(mapRule),
    createdAt: requiredString(wire.created_at, "role.created_at"),
  };
}

interface CreateRoleInput {
  name: string;
  displayName: string;
  description?: string;
  rules: Array<PolicyRule | Record<string, unknown>>;
}

function roleRequest(input: CreateRoleInput): Schemas["RBACRoleRequest"] {
  return {
    name: input.name,
    display_name: input.displayName,
    description: input.description,
    rules: input.rules.map((value) => {
      const rule = value as PolicyRule;
      return {
        resource: rule.resource,
        resources: rule.resources,
        verbs: Array.isArray(rule.verbs) ? rule.verbs : [],
        api_groups: rule.apiGroups ?? rule.api_groups,
      };
    }),
  };
}

export async function getGlobalRoles(): Promise<GlobalRole[]> {
  const response = await getRbacGlobalRoles({
    query: { limit: RBAC_LIST_LIMIT },
  });
  return (response.data ?? []).map(mapRole);
}

export async function getClusterRoles(): Promise<ClusterRole[]> {
  const response = await getRbacClusterRoles({
    query: { limit: RBAC_LIST_LIMIT },
  });
  return (response.data ?? []).map(mapRole);
}

export async function getProjectRoles(): Promise<ProjectRole[]> {
  const response = await getRbacProjectRoles({
    query: { limit: RBAC_LIST_LIMIT },
  });
  return (response.data ?? []).map(mapRole);
}

export async function createGlobalRole(
  input: CreateRoleInput,
): Promise<GlobalRole> {
  const response = await postRbacGlobalRoles({ body: roleRequest(input) });
  return mapRole(requireData(response, "createGlobalRole"));
}

export async function createClusterRole(
  input: CreateRoleInput,
): Promise<ClusterRole> {
  const response = await postRbacClusterRoles({ body: roleRequest(input) });
  return mapRole(requireData(response, "createClusterRole"));
}

export async function createProjectRole(
  input: CreateRoleInput,
): Promise<ProjectRole> {
  const response = await postRbacProjectRoles({ body: roleRequest(input) });
  return mapRole(requireData(response, "createProjectRole"));
}

function mapAccessBinding(
  scope: AccessBinding["scope"],
  wire: GlobalBindingWire | ClusterBindingWire | ProjectBindingWire,
): AccessBinding {
  const cluster = wire as ClusterBindingWire;
  const project = wire as ProjectBindingWire;
  return {
    id: requiredString(wire.id, "binding.id"),
    scope,
    userId: wire.user_id ?? null,
    group: wire.group ?? "",
    roleId: requiredString(wire.role_id, "binding.role_id"),
    clusterId: cluster.cluster_id,
    projectId: project.project_id,
    namespace: cluster.namespace,
    createdAt: requiredString(wire.created_at, "binding.created_at"),
  };
}

export async function listClusterRoleBindings(params?: {
  cluster_id?: string;
}): Promise<AccessBinding[]> {
  const response = await getRbacClusterRoleBindings({
    query: { limit: RBAC_LIST_LIMIT, cluster_id: params?.cluster_id },
  });
  return (response.data ?? []).map((wire) =>
    mapAccessBinding("cluster", wire),
  );
}

export async function listGlobalRoleBindings(): Promise<AccessBinding[]> {
  const response = await getRbacGlobalRoleBindings({
    query: { limit: RBAC_LIST_LIMIT },
  });
  return (response.data ?? []).map((wire) =>
    mapAccessBinding("global", wire),
  );
}

export async function listProjectRoleBindings(params?: {
  project_id?: string;
}): Promise<AccessBinding[]> {
  const response = await getRbacProjectRoleBindings({
    query: { limit: RBAC_LIST_LIMIT, project_id: params?.project_id },
  });
  return (response.data ?? []).map((wire) =>
    mapAccessBinding("project", wire),
  );
}

export async function createClusterRoleBinding(input: {
  user_id: string;
  role_id: string;
  cluster_id: string;
  namespace?: string;
}): Promise<AccessBinding> {
  const response = await postRbacClusterRoleBindings({ body: input });
  return mapAccessBinding(
    "cluster",
    requireData(response, "createClusterRoleBinding"),
  );
}

export async function createGlobalRoleBinding(input: {
  user_id: string;
  role_id: string;
}): Promise<AccessBinding> {
  const response = await postRbacGlobalRoleBindings({ body: input });
  return mapAccessBinding(
    "global",
    requireData(response, "createGlobalRoleBinding"),
  );
}

export async function createProjectRoleBinding(input: {
  user_id: string;
  role_id: string;
  project_id: string;
}): Promise<AccessBinding> {
  const response = await postRbacProjectRoleBindings({ body: input });
  return mapAccessBinding(
    "project",
    requireData(response, "createProjectRoleBinding"),
  );
}

export async function deleteClusterRoleBinding(id: string): Promise<void> {
  await deleteRbacClusterRoleBindingsById({ path: { id } });
}

export async function deleteGlobalRoleBinding(id: string): Promise<void> {
  await deleteRbacGlobalRoleBindingsById({ path: { id } });
}

export async function deleteProjectRoleBinding(id: string): Promise<void> {
  await deleteRbacProjectRoleBindingsById({ path: { id } });
}

export interface EffectivePermissionParams {
  clusterId?: string;
  projectId?: string;
  namespace?: string;
}

function effectiveQuery(params?: EffectivePermissionParams) {
  return {
    cluster_id: params?.clusterId,
    project_id: params?.projectId,
    namespace: params?.namespace,
  };
}

function mapSource(
  wire: Schemas["RBACEffectivePermissionSource"],
): EffectivePermissionSource {
  return {
    scope: wire.scope,
    bindingId: wire.binding_id,
    roleId: wire.role_id,
    roleName: wire.role_name,
    clusterId: wire.cluster_id,
    projectId: wire.project_id,
    namespace: wire.namespace,
  };
}

function mapGrant(
  wire: Schemas["RBACEffectivePermissionGrant"],
): EffectivePermissionGrant {
  return {
    resource: wire.resource,
    verb: wire.verb,
    appliesToContext: wire.applies_to_context,
    inherited: wire.inherited,
    inheritedFrom: wire.inherited_from,
    sources: wire.sources.map(mapSource),
  };
}

function mapBinding(
  wire: Schemas["RBACEffectivePermissionBinding"],
): EffectivePermissionBinding {
  return {
    scope: wire.scope,
    bindingId: wire.binding_id,
    roleId: wire.role_id,
    roleName: wire.role_name,
    group: wire.group,
    clusterId: wire.cluster_id,
    projectId: wire.project_id,
    namespace: wire.namespace,
    superuser: wire.superuser,
    rules: wire.rules.map(mapRule).map((rule) => ({
      resource: rule.resource ?? rule.resources?.[0] ?? "",
      verbs: rule.verbs,
    })),
  };
}

function mapContext(
  wire: Schemas["RBACEffectivePermissionContext"],
): EffectivePermissionContext {
  return {
    clusterId: wire.cluster_id,
    projectId: wire.project_id,
    namespace: wire.namespace,
    namespaceScopedBindingsSupported:
      wire.namespace_scoped_bindings_supported,
    warnings: wire.warnings,
  };
}

export function mapEffectivePermissions(
  wire: EffectiveWire,
): EffectivePermissionResponse {
  return {
    subject: { userId: wire.subject.user_id, self: wire.subject.self },
    superuser: wire.superuser,
    context: mapContext(wire.context),
    bindings: wire.bindings.map(mapBinding),
    permissions: wire.permissions.map(mapGrant),
  };
}

export async function getMyEffectivePermissions(
  params?: EffectivePermissionParams,
): Promise<EffectivePermissionResponse> {
  const response = await getRbacMyPermissions({ query: effectiveQuery(params) });
  return mapEffectivePermissions(
    requireData(response, "getMyEffectivePermissions"),
  );
}

export async function getEffectivePermissionsForUser(
  userId: string,
  params?: EffectivePermissionParams,
): Promise<EffectivePermissionResponse> {
  const response = await getRbacEffectivePermissionsByUserId({
    path: { user_id: userId },
    query: effectiveQuery(params),
  });
  return mapEffectivePermissions(
    requireData(response, "getEffectivePermissionsForUser"),
  );
}

function previewRequest(
  input: PermissionPreviewRequest,
): Schemas["RBACPermissionPreviewRequest"] {
  return {
    scope: input.scope,
    role_id: input.roleId,
    template_name: input.templateName,
    cluster_id: input.clusterId,
    project_id: input.projectId,
    rules: input.rules?.map((rule) => ({
      resource: rule.resource,
      verbs: rule.verbs,
    })),
  };
}

export function mapPermissionPreview(
  wire: PreviewWire,
): PermissionPreviewResponse {
  return {
    scope: wire.scope,
    roleId: wire.role_id,
    roleName: wire.role_name,
    templateName: wire.template_name,
    riskLevel: wire.risk_level,
    warnings: wire.warnings,
    permissions: wire.permissions.map(mapGrant),
    sensitiveFlags: {
      wildcard: wire.sensitive_flags.wildcard,
      canMutate: wire.sensitive_flags.can_mutate,
      canDelete: wire.sensitive_flags.can_delete,
      canExec: wire.sensitive_flags.can_exec,
      canProxy: wire.sensitive_flags.can_proxy,
      canReadSecrets: wire.sensitive_flags.can_read_secrets,
      canManageRbac: wire.sensitive_flags.can_manage_rbac,
      canRestore: wire.sensitive_flags.can_restore,
    },
  };
}

export async function previewPermissions(
  input: PermissionPreviewRequest,
): Promise<PermissionPreviewResponse> {
  const response = await postRbacPermissionPreview({
    body: previewRequest(input),
  });
  return mapPermissionPreview(requireData(response, "previewPermissions"));
}
