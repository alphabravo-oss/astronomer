import {
  deleteAdminNetworkPolicyTemplatesById,
  deleteClustersByClusterIdNetworkPoliciesApplicationsById,
  getAdminNetworkPolicyTemplates,
  getAdminNetworkPolicyTemplatesById,
  getClustersByClusterIdNetworkPoliciesApplications,
  postAdminNetworkPolicyTemplates,
  postClustersByClusterIdNetworkPoliciesApplications,
  postClustersByClusterIdNetworkPoliciesApplicationsByIdReapply,
  putAdminNetworkPolicyTemplatesById,
} from "@/lib/api/generated/client";
import { idempotencyHeaderParams } from "@/lib/api/idempotency";

// ────────────────────────────────────────────────────────────────────────
// Network policy templates (migration 068)
// ────────────────────────────────────────────────────────────────────────

export interface NetworkPolicyTemplate {
  id: string;
  slug: string;
  name: string;
  description: string;
  kind: "builtin" | "custom";
  spec_template: string;
  enabled: boolean;
  created_by?: string;
  created_at: string;
  updated_at: string;
}

export interface NetworkPolicyApplication {
  id: string;
  template_id: string;
  template_slug?: string;
  cluster_id: string;
  namespace: string;
  policy_name: string;
  status: "pending" | "applied" | "failed" | "drifting";
  last_applied_at?: string;
  last_error?: string;
  applied_by?: string;
  created_at: string;
  updated_at: string;
}

export interface NetworkPolicyTemplateWriteRequest {
  slug?: string;
  name: string;
  description?: string;
  spec_template: string;
  enabled?: boolean;
  clone_from?: string;
}

export interface ApplyNetworkPolicyRequest {
  template_id: string;
  namespace?: string;
  namespaces?: string[];
}

function optionalString(value: unknown): string | undefined {
  return typeof value === "string" ? value : undefined;
}

function networkPolicyTemplateFromWire(
  wire: Record<string, unknown>,
): NetworkPolicyTemplate {
  return {
    id: String(wire.id ?? ""),
    slug: String(wire.slug ?? ""),
    name: String(wire.name ?? ""),
    description: String(wire.description ?? ""),
    kind: wire.kind === "builtin" ? "builtin" : "custom",
    spec_template: String(wire.spec_template ?? ""),
    enabled: wire.enabled === true,
    created_by: optionalString(wire.created_by),
    created_at: String(wire.created_at ?? ""),
    updated_at: String(wire.updated_at ?? ""),
  };
}

function networkPolicyApplicationFromWire(
  wire: Record<string, unknown>,
): NetworkPolicyApplication {
  const status = String(wire.status ?? "pending");
  return {
    id: String(wire.id ?? ""),
    template_id: String(wire.template_id ?? ""),
    template_slug: optionalString(wire.template_slug),
    cluster_id: String(wire.cluster_id ?? ""),
    namespace: String(wire.namespace ?? ""),
    policy_name: String(wire.policy_name ?? ""),
    status:
      status === "applied" || status === "failed" || status === "drifting"
        ? status
        : "pending",
    last_applied_at: optionalString(wire.last_applied_at),
    last_error: optionalString(wire.last_error),
    applied_by: optionalString(wire.applied_by),
    created_at: String(wire.created_at ?? ""),
    updated_at: String(wire.updated_at ?? ""),
  };
}

/** GET /admin/network-policy-templates/ */
export async function listNetworkPolicyTemplates(): Promise<
  NetworkPolicyTemplate[]
> {
  const response = await getAdminNetworkPolicyTemplates();
  return (response.data ?? []).map(networkPolicyTemplateFromWire);
}

/** GET /admin/network-policy-templates/{id}/ */
export async function getNetworkPolicyTemplate(
  id: string,
): Promise<NetworkPolicyTemplate> {
  const response = await getAdminNetworkPolicyTemplatesById({ path: { id } });
  return networkPolicyTemplateFromWire(response.data ?? {});
}

/** POST /admin/network-policy-templates/ */
export async function createNetworkPolicyTemplate(
  body: NetworkPolicyTemplateWriteRequest,
): Promise<NetworkPolicyTemplate> {
  const response = await postAdminNetworkPolicyTemplates({ body });
  return networkPolicyTemplateFromWire(response.data ?? {});
}

/** PUT /admin/network-policy-templates/{id}/ */
export async function updateNetworkPolicyTemplate(
  id: string,
  body: NetworkPolicyTemplateWriteRequest,
): Promise<NetworkPolicyTemplate> {
  const response = await putAdminNetworkPolicyTemplatesById({
    path: { id },
    body,
  });
  return networkPolicyTemplateFromWire(response.data ?? {});
}

/** DELETE /admin/network-policy-templates/{id}/ */
export async function deleteNetworkPolicyTemplate(id: string): Promise<void> {
  await deleteAdminNetworkPolicyTemplatesById({ path: { id } });
}

/** GET /clusters/{cluster_id}/network-policies/applications/ */
export async function listNetworkPolicyApplications(
  clusterID: string,
): Promise<NetworkPolicyApplication[]> {
  const response = await getClustersByClusterIdNetworkPoliciesApplications({
    path: { cluster_id: clusterID },
  });
  return (response.data ?? []).map(networkPolicyApplicationFromWire);
}

/** POST /clusters/{cluster_id}/network-policies/applications/ */
export async function applyNetworkPolicy(
  clusterID: string,
  body: ApplyNetworkPolicyRequest,
): Promise<NetworkPolicyApplication[]> {
  const response = await postClustersByClusterIdNetworkPoliciesApplications({
    path: { cluster_id: clusterID },
    headerParams: idempotencyHeaderParams(),
    body,
  });
  return (response.data ?? []).map(networkPolicyApplicationFromWire);
}

/** DELETE /clusters/{cluster_id}/network-policies/applications/{id}/ */
export async function deleteNetworkPolicyApplication(
  clusterID: string,
  applicationID: string,
): Promise<void> {
  await deleteClustersByClusterIdNetworkPoliciesApplicationsById({
    path: { cluster_id: clusterID, id: applicationID },
  });
}

/** POST /clusters/{cluster_id}/network-policies/applications/{id}/reapply/ */
export async function reapplyNetworkPolicyApplication(
  clusterID: string,
  applicationID: string,
): Promise<NetworkPolicyApplication> {
  const response =
    await postClustersByClusterIdNetworkPoliciesApplicationsByIdReapply({
      path: { cluster_id: clusterID, id: applicationID },
      headerParams: idempotencyHeaderParams(),
    });
  return networkPolicyApplicationFromWire(response.data ?? {});
}
