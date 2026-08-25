import {
  deleteSecurityPoliciesById,
  deleteSecurityTemplatesById,
  getSecurityPolicies,
  getSecurityTemplates,
  postSecurityPolicies,
  postSecurityPoliciesByIdApply,
  postSecurityTemplates,
  putSecurityTemplatesById,
} from "@/lib/api/generated/client";
import type {
  ClusterSecurityPolicy,
  PodSecurityTemplate,
} from "@/types";
import type { OpenAPIComponents } from "@/types/openapi.generated";

type Schemas = OpenAPIComponents["schemas"];

function requireData<T>(response: { data?: T }, operation: string): T {
  if (response.data === undefined) {
    throw new Error(`${operation} returned no data payload`);
  }
  return response.data;
}

function requiredName(name: string | undefined): string {
  const value = name?.trim();
  if (!value) throw new Error("Security template name is required");
  return value;
}

/** Explicit raw-wire mapper for the generated security template contract. */
export function mapPodSecurityTemplate(
  wire: Schemas["PodSecurityTemplate"],
): PodSecurityTemplate {
  return {
    id: wire.id,
    name: wire.name,
    description: wire.description || undefined,
    isDefault: wire.is_default,
    isBuiltin: wire.is_builtin,
    enforceLevel: wire.enforce_level,
    enforceVersion: wire.enforce_version,
    auditLevel: wire.audit_level,
    auditVersion: wire.audit_version,
    warnLevel: wire.warn_level,
    warnVersion: wire.warn_version,
    exemptUsernames: wire.exempt_usernames,
    exemptRuntimeClasses: wire.exempt_runtime_classes,
    exemptNamespaces: wire.exempt_namespaces,
    createdById: wire.created_by_id,
    createdAt: wire.created_at,
    updatedAt: wire.updated_at,
  };
}

function templateBody(
  data: Partial<PodSecurityTemplate>,
): Schemas["PodSecurityTemplateWriteRequest"] {
  return {
    name: requiredName(data.name),
    description: data.description,
    is_default: data.isDefault,
    enforce_level: data.enforceLevel,
    enforce_version: data.enforceVersion,
    audit_level: data.auditLevel,
    audit_version: data.auditVersion,
    warn_level: data.warnLevel,
    warn_version: data.warnVersion,
    exempt_usernames: data.exemptUsernames,
    exempt_runtime_classes: data.exemptRuntimeClasses,
    exempt_namespaces: data.exemptNamespaces,
  };
}

export function mapClusterSecurityPolicy(
  wire: Schemas["ClusterSecurityPolicy"],
): ClusterSecurityPolicy {
  return {
    id: wire.id,
    clusterId: wire.cluster_id,
    templateId: wire.template_id,
    appliedAt: wire.applied_at,
    syncStatus: wire.sync_status,
    errorMessage: wire.error_message,
    createdAt: wire.created_at,
    updatedAt: wire.updated_at,
  };
}

export async function getPodSecurityTemplates(): Promise<
  PodSecurityTemplate[]
> {
  const response = await getSecurityTemplates({ query: { limit: 200 } });
  return (response.data ?? []).map(mapPodSecurityTemplate);
}

export async function createPodSecurityTemplate(
  data: Partial<PodSecurityTemplate>,
): Promise<PodSecurityTemplate> {
  const response = await postSecurityTemplates({ body: templateBody(data) });
  return mapPodSecurityTemplate(
    requireData(response, "createPodSecurityTemplate"),
  );
}

export async function updatePodSecurityTemplate(
  id: string,
  data: Partial<PodSecurityTemplate>,
): Promise<PodSecurityTemplate> {
  const response = await putSecurityTemplatesById({
    path: { id },
    body: templateBody(data),
  });
  return mapPodSecurityTemplate(
    requireData(response, "updatePodSecurityTemplate"),
  );
}

export async function deletePodSecurityTemplate(id: string): Promise<void> {
  await deleteSecurityTemplatesById({ path: { id } });
}

export async function getClusterSecurityPolicies(): Promise<
  ClusterSecurityPolicy[]
> {
  const response = await getSecurityPolicies({ query: { limit: 200 } });
  return (response.data ?? []).map(mapClusterSecurityPolicy);
}

export async function assignSecurityPolicy(data: {
  cluster_id: string;
  template_id: string;
}): Promise<ClusterSecurityPolicy> {
  const response = await postSecurityPolicies({ body: data });
  return mapClusterSecurityPolicy(
    requireData(response, "assignSecurityPolicy"),
  );
}

export async function applySecurityPolicy(
  id: string,
): Promise<ClusterSecurityPolicy> {
  const response = await postSecurityPoliciesByIdApply({ path: { id } });
  return mapClusterSecurityPolicy(requireData(response, "applySecurityPolicy"));
}

export async function removeSecurityPolicy(id: string): Promise<void> {
  await deleteSecurityPoliciesById({ path: { id } });
}
