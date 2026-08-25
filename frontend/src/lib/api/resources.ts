import {
  getClustersByClusterIdResourcesDiscovery,
  getClustersByClusterIdResourcesSchema,
} from "@/lib/api/generated/client";
import type {
  OpenAPIComponents,
  OpenAPIOperations,
} from "@/types/openapi.generated";

type WireEntry = OpenAPIComponents["schemas"]["ResourceDiscoveryEntry"];
type WirePolicy = OpenAPIComponents["schemas"]["ResourcePolicyMetadata"];

export type ResourceType =
  OpenAPIOperations["getClustersByClusterIdResourcesSchema"]["arguments"]["query"]["resource_type"];

export interface ResourcePolicyView {
  secret: WirePolicy["secret"];
  privilegeEscalating: WirePolicy["privilege_escalating"];
  destructiveDelete: WirePolicy["destructive_delete"];
  forceConflictPermission: WirePolicy["force_conflict_permission"];
}

export interface ResourceDiscoveryEntryView {
  resourceType: WireEntry["resource_type"];
  apiBase: WireEntry["api_base"];
  apiGroup?: WireEntry["api_group"];
  apiVersion: WireEntry["api_version"];
  kind: WireEntry["kind"];
  plural: WireEntry["plural"];
  namespaced: WireEntry["namespaced"];
  verbs: WireEntry["verbs"];
  shortNames?: WireEntry["short_names"];
  categories?: WireEntry["categories"];
  policy: ResourcePolicyView;
  source: WireEntry["source"];
}

export interface ResourceDiscoveryView {
  clusterId: string;
  resources: ResourceDiscoveryEntryView[];
  crds: Array<Record<string, unknown>>;
  partial: boolean;
  errors: Record<string, string>;
}

export interface ResourceSchemaView {
  resource: ResourceDiscoveryEntryView;
  schema: Record<string, unknown>;
  schemaName?: string;
  schemaAvailable: boolean;
  definitions: Record<string, Record<string, unknown>>;
  definitionsTruncated: boolean;
}

function mapPolicy(wire: WirePolicy): ResourcePolicyView {
  return {
    secret: wire.secret,
    privilegeEscalating: wire.privilege_escalating,
    destructiveDelete: wire.destructive_delete,
    forceConflictPermission: wire.force_conflict_permission,
  };
}

function mapEntry(wire: WireEntry): ResourceDiscoveryEntryView {
  return {
    resourceType: wire.resource_type,
    apiBase: wire.api_base,
    apiGroup: wire.api_group,
    apiVersion: wire.api_version,
    kind: wire.kind,
    plural: wire.plural,
    namespaced: wire.namespaced,
    verbs: wire.verbs,
    shortNames: wire.short_names,
    categories: wire.categories,
    policy: mapPolicy(wire.policy),
    source: wire.source,
  };
}

export async function getResourceDiscovery(
  clusterId: string,
): Promise<ResourceDiscoveryView> {
  const { data } = await getClustersByClusterIdResourcesDiscovery({
    path: { cluster_id: clusterId },
  });
  return {
    clusterId: data.cluster_id,
    resources: data.resources.map(mapEntry),
    crds: data.crds,
    partial: data.partial,
    errors: data.errors,
  };
}

export async function getResourceSchema(
  clusterId: string,
  resourceType: ResourceType,
): Promise<ResourceSchemaView> {
  const { data } = await getClustersByClusterIdResourcesSchema({
    path: { cluster_id: clusterId },
    query: { resource_type: resourceType },
  });
  return {
    resource: mapEntry(data.resource),
    // Kubernetes object-schema keys are deliberately raw; camelizing fields
    // such as x-kubernetes-* or default object properties corrupts the schema.
    schema: data.schema,
    schemaName: data.schema_name,
    schemaAvailable: data.schema_available,
    definitions: data.definitions ?? {},
    definitionsTruncated: data.definitions_truncated ?? false,
  };
}
