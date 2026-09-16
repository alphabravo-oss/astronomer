/** Delivery rollout contracts and operations. */

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
  type TrustPolicy,
} from "@/lib/api/delivery-common";
import type {
  DeliveryAuthMode,
  DeliverySourceType,
} from "@/lib/api/delivery-sources";
import type { Placement, PlacementRequest } from "@/lib/api/delivery-targets";
import type { OpenAPIComponents } from "@/types/openapi.generated";
import type {
  AssertNoPhantomWireKeys,
  CamelizeKeys,
} from "@/types/wire-contract";

export type RolloutStrategyType =
  "all_at_once" | "rolling" | "canary" | "partitioned";
export type RolloutFailureAction = "pause" | "abort" | "rollback";
export type AmountType = "count" | "percent";

export type RolloutState =
  | "draft"
  | "resolving"
  | "awaiting_approval"
  | "rejected"
  | "queued"
  | "progressing"
  | "paused"
  | "aborted"
  | "succeeded"
  | "failed"
  | "rolling_back"
  | "rolled_back"
  | "rollback_failed";

export type RolloutClusterState =
  | "pending"
  | "released"
  | "acknowledged"
  | "reconciling"
  | "ready"
  | "blocked"
  | "timed_out"
  | "failed"
  | "skipped"
  | "rolling_back"
  | "ready_previous"
  | "rollback_failed"
  | "aborted";

export interface Amount {
  type: AmountType;
  value: number;
}

export interface RolloutStrategy {
  type: RolloutStrategyType;
  maxConcurrent: number;
  maxUnavailable: Amount;
  minReady: string;
  progressDeadline: string;
  failureThreshold: Amount;
  onFailure: RolloutFailureAction;
  respectMaintenanceWindows: boolean;
  shuffleSeed?: string;
  canary?: {
    size: Amount;
    clusterIds?: string[];
    approvalAfterCanary: boolean;
    soak: string;
  };
  partitions?: Array<{
    name: string;
    selector: Placement;
    approvalRequired: boolean;
    soak: string;
  }>;
}

export interface RolloutStrategyRequest extends Record<string, unknown> {
  type: RolloutStrategyType;
  max_concurrent: number;
  max_unavailable: Amount;
  min_ready: string;
  progress_deadline: string;
  failure_threshold: Amount;
  on_failure: RolloutFailureAction;
  respect_maintenance_windows: boolean;
  shuffle_seed?: string;
  canary?: {
    size: Amount;
    cluster_ids?: string[];
    approval_after_canary: boolean;
    soak: string;
  };
  partitions?: Array<{
    name: string;
    selector: PlacementRequest;
    approval_required: boolean;
    soak: string;
  }>;
}

export type DeliveryRollout = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["DeliveryRollout"]>,
  "strategy" | "state"
> & {
  strategy: RolloutStrategy;
  state: RolloutState;
};

export type DeliveryRolloutApproval = CamelizeKeys<
  OpenAPIComponents["schemas"]["DeliveryRolloutApprovalRecord"]
>;

export type DeliveryRolloutEvent = CamelizeKeys<
  OpenAPIComponents["schemas"]["DeliveryRolloutEvent"]
>;

export interface DeliveryFrozenPlan {
  id: string;
  targetId: string;
  projectId: string;
  targetGeneration: number;
  desired: {
    bundleVersionId: string;
    specDigest: string;
    source: {
      sourceId: string;
      type: DeliverySourceType;
      url: string;
      authMode: DeliveryAuthMode;
      trust: TrustPolicy;
      revision: { kind: string; value: string; artifactDigest: string };
    };
  };
  placementDigest: string;
  strategy: RolloutStrategy;
  strategyDigest: string;
  approval: { required: boolean; digest: string };
  actor: string;
  requestDigest: string;
  createdAt: string;
  deadline: string;
  cohorts: Array<{
    index: number;
    name: string;
    clusterIds: string[];
    approvalRequired: boolean;
    approvalDigest?: string;
    soakAfter: string;
  }>;
  planDigest: string;
}

export type DeliveryRolloutDetail = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["DeliveryRolloutDetail"]>,
  "rollout" | "frozenPlan" | "approvals" | "timeline"
> & {
  rollout: DeliveryRollout;
  frozenPlan: DeliveryFrozenPlan;
  approvals: DeliveryRolloutApproval[];
  timeline: DeliveryRolloutEvent[];
};

export type DeliveryFrozenRollout = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["DeliveryFrozenRollout"]>,
  | "desired"
  | "strategy"
  | "approval"
  | "cohorts"
  | "clusters"
  | "idempotencyKey"
> & {
  desired: DeliveryFrozenPlan["desired"];
  strategy: RolloutStrategy;
  approval: DeliveryFrozenPlan["approval"];
  cohorts: DeliveryFrozenPlan["cohorts"];
  clusters: Array<{
    clusterId: string;
    cohort: number;
    order: number;
    previous?: Record<string, unknown>;
  }>;
};

export type DeliveryRolloutCluster = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["DeliveryRolloutCluster"]>,
  "state"
> & { state: RolloutClusterState };

function mapRollout(
  wire: DeliveryContracts["DeliveryRollout"],
): DeliveryRollout {
  return camelizeKeys(wire) as unknown as DeliveryRollout;
}
function mapRolloutEvent(
  wire: DeliveryContracts["DeliveryRolloutEvent"],
): DeliveryRolloutEvent {
  return camelizeKeys(wire) as unknown as DeliveryRolloutEvent;
}
function mapRolloutCluster(
  wire: DeliveryContracts["DeliveryRolloutCluster"],
): DeliveryRolloutCluster {
  return camelizeKeys(wire) as unknown as DeliveryRolloutCluster;
}
function mapFrozenRollout(
  wire: DeliveryContracts["DeliveryFrozenRollout"],
): DeliveryFrozenRollout {
  const mapped = camelizeKeys(wire) as unknown as CamelizeKeys<typeof wire>;
  const { idempotencyKey: _idempotencyKey, ...safe } = mapped;
  const { trustPolicy, ...source } = mapped.desired.source;
  return {
    ...safe,
    desired: {
      ...mapped.desired,
      source: { ...source, trust: trustPolicy },
    },
  } as DeliveryFrozenRollout;
}
function mapRolloutDetail(
  wire: DeliveryContracts["DeliveryRolloutDetail"],
): DeliveryRolloutDetail {
  const mapped = camelizeKeys(wire) as unknown as CamelizeKeys<typeof wire>;
  return {
    ...mapped,
    rollout: mapRollout(wire.rollout),
    frozenPlan: mapFrozenRollout(wire.frozen_plan),
    approvals: wire.approvals.map((item) =>
      camelizeKeys(item),
    ) as unknown as DeliveryRolloutApproval[],
    timeline: wire.timeline.map(mapRolloutEvent),
  } as DeliveryRolloutDetail;
}

export async function startDeliveryRollout(
  targetId: string,
  body: {
    project_id: string;
    preview_digest: string;
    confirm_all_clusters: boolean;
    strategy: RolloutStrategyRequest;
  } & DeliveryContracts["DeliveryRolloutStart"],
  targetGeneration: number,
  key: string,
  signal?: AbortSignal,
) {
  const wire = await generated.postDeliveryTargetsByIdRollouts({
    path: { id: targetId },
    body,
    headerParams: {
      "If-Match": quotedETag(targetGeneration),
      "Idempotency-Key": key,
    },
    signal,
  });
  return mapFrozenRollout(requireEnvelopeData(wire, "delivery response"));
}

export async function listDeliveryRollouts(
  projectId: string,
  params: PageParams & { state?: RolloutState } = {},
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliveryRollouts({
    query: { project_id: projectId, ...params },
    signal,
  });
  return mapPage(wire, mapRollout);
}

export async function getDeliveryRollout(
  projectId: string,
  id: string,
  signal?: AbortSignal,
) {
  const response = await generated.executeOpenAPIOperationWithResponse(
    "getDeliveryRolloutsById",
    {
      path: { id },
      query: { project_id: projectId },
      signal,
    },
  );
  return entityFromResponse(response, mapRolloutDetail);
}

export async function listDeliveryRolloutClusters(
  projectId: string,
  id: string,
  params: PageParams = {},
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliveryRolloutsByIdClusters({
    path: { id },
    query: { project_id: projectId, ...params },
    signal,
  });
  return mapPage(wire, mapRolloutCluster);
}

export async function listDeliveryRolloutEvents(
  projectId: string,
  id: string,
  params: PageParams = {},
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliveryRolloutsByIdEvents({
    path: { id },
    query: { project_id: projectId, ...params },
    signal,
  });
  return mapPage(wire, mapRolloutEvent);
}

export async function actOnDeliveryRollout(
  projectId: string,
  id: string,
  action: "pause" | "resume" | "abort" | "retry" | "rollback",
  etag: string | number,
  reasonCode: string,
  key?: string,
  signal?: AbortSignal,
) {
  const common = {
    path: { id },
    body: { project_id: projectId, reason_code: reasonCode },
    signal,
  };
  const ifMatch = quotedETag(etag);
  const response =
    action === "pause"
      ? await generated.executeOpenAPIOperationWithResponse(
          "postDeliveryRolloutsByIdPause",
          {
            ...common,
            headerParams: {
              "If-Match": ifMatch,
              ...optionalIdempotencyHeader(key),
            },
          },
        )
      : action === "resume"
        ? await generated.executeOpenAPIOperationWithResponse(
            "postDeliveryRolloutsByIdResume",
            {
              ...common,
              headerParams: {
                "If-Match": ifMatch,
                ...optionalIdempotencyHeader(key),
              },
            },
          )
        : action === "abort"
          ? await generated.executeOpenAPIOperationWithResponse(
              "postDeliveryRolloutsByIdAbort",
              {
                ...common,
                headerParams: {
                  "If-Match": ifMatch,
                  ...optionalIdempotencyHeader(key),
                },
              },
            )
          : action === "retry"
            ? await generated.executeOpenAPIOperationWithResponse(
                "postDeliveryRolloutsByIdRetry",
                {
                  ...common,
                  headerParams: {
                    "If-Match": ifMatch,
                    ...optionalIdempotencyHeader(key),
                  },
                },
              )
            : await generated.executeOpenAPIOperationWithResponse(
                "postDeliveryRolloutsByIdRollback",
                {
                  ...common,
                  headerParams: {
                    "If-Match": ifMatch,
                    ...optionalIdempotencyHeader(key),
                  },
                },
              );
  return camelizeKeys(requireEnvelopeData(response.data, "delivery response"));
}

export async function approveDeliveryRollout(
  id: string,
  body: {
    project_id: string;
    cohort: number;
    binding_digest: string;
    decision: "approved" | "rejected";
    expires_at: string;
  } & DeliveryContracts["DeliveryRolloutApproval"],
  etag: string | number,
  key?: string,
  signal?: AbortSignal,
) {
  const response = await generated.executeOpenAPIOperationWithResponse(
    "postDeliveryRolloutsByIdApprove",
    {
      path: { id },
      body,
      headerParams: {
        "If-Match": quotedETag(etag),
        ...optionalIdempotencyHeader(key),
      },
      signal,
    },
  );
  return camelizeKeys(requireEnvelopeData(response.data, "delivery response"));
}

export function rolloutIsTerminal(state: RolloutState): boolean {
  return [
    "rejected",
    "aborted",
    "succeeded",
    "failed",
    "rolled_back",
    "rollback_failed",
  ].includes(state);
}

const frozenRolloutMatchesWire: AssertNoPhantomWireKeys<
  DeliveryFrozenRollout,
  DeliveryContracts["DeliveryFrozenRollout"]
> = true;
const deliveryRolloutMatchesWire: AssertNoPhantomWireKeys<
  DeliveryRollout,
  DeliveryContracts["DeliveryRollout"]
> = true;
const deliveryRolloutEventMatchesWire: AssertNoPhantomWireKeys<
  DeliveryRolloutEvent,
  DeliveryContracts["DeliveryRolloutEvent"]
> = true;
const rolloutClusterMatchesWire: AssertNoPhantomWireKeys<
  DeliveryRolloutCluster,
  DeliveryContracts["DeliveryRolloutCluster"]
> = true;

void [
  frozenRolloutMatchesWire,
  deliveryRolloutMatchesWire,
  deliveryRolloutEventMatchesWire,
  rolloutClusterMatchesWire,
];
