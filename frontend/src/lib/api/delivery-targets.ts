/** Delivery target placement contracts and operations. */

import { camelizeKeys } from "@/lib/camelize";
import * as generated from "@/lib/api/generated/client";
import { requireEnvelopeData } from "@/lib/api/data-envelope";
import {
  entityFromResponse,
  mapPage,
  optionalIdempotencyHeader,
  quotedETag,
  type DeliveryContracts,
  type PageParams,
} from "@/lib/api/delivery-common";
import type {
  DriftPolicy,
  ReconciliationPolicy,
} from "@/lib/api/delivery-bundles";
import type { OpenAPIComponents } from "@/types/openapi.generated";
import type {
  AssertNoPhantomWireKeys,
  CamelizeKeys,
} from "@/types/wire-contract";

export type LabelOperator = "In" | "NotIn" | "Exists" | "DoesNotExist";

export interface LabelExpression {
  key: string;
  operator: LabelOperator;
  values?: string[];
}

export interface Placement {
  projectIds?: string[];
  clusterIds?: string[];
  clusterGroupIds?: string[];
  matchLabels?: Record<string, string>;
  matchExpressions?: LabelExpression[];
  excludeClusterIds?: string[];
  allClusters: boolean;
}

export interface PlacementRequest extends Record<string, unknown> {
  project_ids?: string[];
  cluster_ids?: string[];
  cluster_group_ids?: string[];
  match_labels?: Record<string, string>;
  match_expressions?: Array<{
    key: string;
    operator: LabelOperator;
    values?: string[];
  }>;
  exclude_cluster_ids?: string[];
  all_clusters: boolean;
}

export type DeliveryTarget = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["DeliveryTarget"]>,
  | "placement"
  | "rolloutPolicy"
  | "reconciliationPolicy"
  | "maintenanceWindowPolicy"
> & {
  placement: Placement;
  rolloutPolicy: { approvalRequired: boolean };
  reconciliationPolicy: ReconciliationPolicy;
  maintenanceWindowPolicy: Record<string, unknown>;
};

export interface DeliveryTargetRequest {
  project_id: string;
  name: string;
  description?: string;
  bundle_version_id: string;
  placement: PlacementRequest;
  rollout_policy: { approval_required: boolean };
  reconciliation_policy: {
    [key: string]: unknown;
    interval: string;
    retry_interval: string;
    timeout: string;
    prune: boolean;
    wait: boolean;
    drift: DriftPolicy;
  };
  maintenance_window_policy?: Record<string, unknown>;
  overrides?: {
    helm_values?: Record<string, unknown>;
    patches?: string[];
  };
  suspended: boolean;
}

export type PlacementDecisionReason =
  | "selected"
  | "excluded_by_selector"
  | "excluded_explicitly"
  | "unauthorized"
  | "disconnected"
  | "incompatible"
  | "missing_capability"
  | "decommissioning";

export interface PlacementDecision {
  clusterId: string;
  projectId?: string;
  clusterName?: string;
  reason: PlacementDecisionReason;
  matchReasons?: Array<
    | "explicit_cluster"
    | "all_clusters"
    | "cluster_group"
    | "match_labels"
    | "match_expressions"
  >;
  matchedGroupIds?: string[];
  missingCapabilities?: string[];
  compatibilityReason?: string;
}

export interface PlacementPreview {
  targetId: string;
  targetGeneration: number;
  bundleVersionId: string;
  previewDigest: string;
  selectedCount: number;
  excludedCount: number;
  requiresAllConfirmation: boolean;
  decisions: PlacementDecision[];
  decisionCount: number;
  decisionOffset: number;
  decisionPageSize: number;
  hasMoreDecisions: boolean;
  nextCursor: string;
  risks: string[];
}

export interface PlacementPreviewPageParams {
  pageSize?: number;
  cursor?: string;
}

function mapTarget(wire: DeliveryContracts["DeliveryTarget"]): DeliveryTarget {
  return camelizeKeys(wire) as unknown as DeliveryTarget;
}
function mapPreview(
  wire: DeliveryContracts["DeliveryTargetPreview"],
): PlacementPreview {
  return camelizeKeys(wire) as unknown as PlacementPreview;
}

export async function listDeliveryTargets(
  projectId: string,
  params: PageParams = {},
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliveryTargets({
    query: { project_id: projectId, ...params },
    signal,
  });
  return mapPage(wire, mapTarget);
}

export async function createDeliveryTarget(
  body: DeliveryTargetRequest & DeliveryContracts["DeliveryTargetWrite"],
  key?: string,
  signal?: AbortSignal,
) {
  const response = await generated.executeOpenAPIOperationWithResponse(
    "postDeliveryTargets",
    {
      body,
      headerParams: optionalIdempotencyHeader(key),
      signal,
    },
  );
  return entityFromResponse(response, mapTarget);
}

export async function getDeliveryTarget(
  projectId: string,
  id: string,
  signal?: AbortSignal,
) {
  const response = await generated.executeOpenAPIOperationWithResponse(
    "getDeliveryTargetsById",
    {
      path: { id },
      query: { project_id: projectId },
      signal,
    },
  );
  return entityFromResponse(response, mapTarget);
}

export async function updateDeliveryTarget(
  id: string,
  body: Partial<DeliveryTargetRequest>,
  etag: string | number,
  key?: string,
  signal?: AbortSignal,
) {
  if (!body.project_id) {
    throw new Error("A project_id is required to update a delivery target.");
  }
  const response = await generated.executeOpenAPIOperationWithResponse(
    "patchDeliveryTargetsById",
    {
      path: { id },
      query: { project_id: body.project_id },
      body,
      headerParams: {
        "If-Match": quotedETag(etag),
        ...optionalIdempotencyHeader(key),
      },
      signal,
    },
  );
  return entityFromResponse(response, mapTarget);
}

export async function deleteDeliveryTarget(
  projectId: string,
  id: string,
  etag: string | number,
  key?: string,
  signal?: AbortSignal,
) {
  const wire = await generated.deleteDeliveryTargetsById({
    path: { id },
    query: { project_id: projectId },
    headerParams: {
      "If-Match": quotedETag(etag),
      ...optionalIdempotencyHeader(key),
    },
    signal,
  });
  return camelizeKeys(
    requireEnvelopeData(wire, "delivery response"),
  ) as unknown as {
    id: string;
    deletionState: string;
    resourceVersion: number;
    deploymentCount: number;
  };
}

export async function orphanDeliveryTarget(
  projectId: string,
  id: string,
  etag: string | number,
  key?: string,
  signal?: AbortSignal,
) {
  const wire = await generated.postDeliveryTargetsByIdOrphan({
    path: { id },
    query: { project_id: projectId },
    headerParams: {
      "If-Match": quotedETag(etag),
      ...optionalIdempotencyHeader(key),
    },
    signal,
  });
  return camelizeKeys(
    requireEnvelopeData(wire, "delivery response"),
  ) as unknown as {
    id: string;
    deletionState: string;
    resourceVersion: number;
  };
}

export async function previewDeliveryTarget(
  projectId: string,
  id: string,
  params: PlacementPreviewPageParams = {},
  signal?: AbortSignal,
) {
  const wire = await generated.postDeliveryTargetsByIdPreview({
    path: { id },
    query: {
      project_id: projectId,
      ...(params.pageSize ? { page_size: params.pageSize } : {}),
      ...(params.cursor ? { cursor: params.cursor } : {}),
    },
    signal,
  });
  return mapPreview(requireEnvelopeData(wire, "delivery response"));
}

const deliveryTargetMatchesWire: AssertNoPhantomWireKeys<
  DeliveryTarget,
  DeliveryContracts["DeliveryTarget"]
> = true;
const placementDecisionMatchesWire: AssertNoPhantomWireKeys<
  PlacementDecision,
  DeliveryContracts["DeliveryPreviewDecision"]
> = true;
const placementPreviewMatchesWire: AssertNoPhantomWireKeys<
  PlacementPreview,
  DeliveryContracts["DeliveryTargetPreview"]
> = true;

void [
  deliveryTargetMatchesWire,
  placementDecisionMatchesWire,
  placementPreviewMatchesWire,
];
