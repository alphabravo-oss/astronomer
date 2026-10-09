import type { CategoryValue } from "@/types/categories.generated";
/** Per-cluster delivery deployment contracts and operations. */

import { camelizeWire } from "@/lib/camelize";
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
import type { OpenAPIReadComponents as OpenAPIComponents } from "@/types/categories.generated";
import type {
  AssertNoPhantomWireKeys,
  CamelizeKeys,
} from "@/types/wire-contract";

export type DeploymentPhase = CategoryValue<"DeliveryDeploymentPhase">;

export type DeliveryConditionView = CamelizeKeys<
  DeliveryContracts["DeliveryCondition"]
>;

export type ClusterDeployment = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["ClusterDeployment"]>,
  "conditions"
> & { conditions: DeliveryConditionView[] };

export type ClusterDeploymentEvent = CamelizeKeys<
  OpenAPIComponents["schemas"]["ClusterDeploymentEvent"]
>;

export type ClusterDeploymentDetail = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["ClusterDeploymentDetail"]>,
  "deployment" | "events"
> & {
  deployment: ClusterDeployment;
  events: ClusterDeploymentEvent[];
};

export function mapClusterDeployment(
  wire: DeliveryContracts["ClusterDeployment"],
): ClusterDeployment {
  return camelizeWire(wire);
}
function mapClusterDeploymentEvent(
  wire: DeliveryContracts["ClusterDeploymentEvent"],
): ClusterDeploymentEvent {
  return camelizeWire(wire);
}
function mapClusterDeploymentDetail(
  wire: DeliveryContracts["ClusterDeploymentDetail"],
): ClusterDeploymentDetail {
  return {
    ...camelizeWire(wire),
    deployment: mapClusterDeployment(wire.deployment),
    events: wire.events.map(mapClusterDeploymentEvent),
  } as ClusterDeploymentDetail;
}

export async function listClusterDeployments(
  projectId: string,
  params: PageParams & { cluster_id?: string; phase?: DeploymentPhase } = {},
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliveryDeployments({
    query: { project_id: projectId, ...params },
    signal,
  });
  return mapPage(wire, mapClusterDeployment);
}

export async function getClusterDeployment(
  projectId: string,
  id: string,
  signal?: AbortSignal,
) {
  const response = await generated.executeOpenAPIOperationWithResponse(
    "getDeliveryDeploymentsById",
    {
      path: { id },
      query: { project_id: projectId },
      signal,
    },
  );
  return entityFromResponse(response, mapClusterDeploymentDetail);
}

export async function listClusterDeploymentEvents(
  projectId: string,
  id: string,
  params: PageParams = {},
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliveryDeploymentsByIdEvents({
    path: { id },
    query: { project_id: projectId, ...params },
    signal,
  });
  return mapPage(wire, mapClusterDeploymentEvent);
}

export async function actOnClusterDeployment(
  projectId: string,
  id: string,
  action: "reconcile" | "suspend",
  etag: string | number,
  reasonCode: string,
  key?: string,
  signal?: AbortSignal,
) {
  const args = {
    path: { id },
    body: { project_id: projectId, reason_code: reasonCode },
    headerParams: {
      "If-Match": quotedETag(etag),
      ...optionalIdempotencyHeader(key),
    },
    signal,
  };
  const response =
    action === "reconcile"
      ? await generated.executeOpenAPIOperationWithResponse(
          "postDeliveryDeploymentsByIdReconcile",
          args,
        )
      : await generated.executeOpenAPIOperationWithResponse(
          "postDeliveryDeploymentsByIdSuspend",
          args,
        );
  return camelizeWire(requireEnvelopeData(response.data, "delivery response"));
}

const clusterDeploymentMatchesWire: AssertNoPhantomWireKeys<
  ClusterDeployment,
  DeliveryContracts["ClusterDeployment"]
> = true;
const deploymentEventMatchesWire: AssertNoPhantomWireKeys<
  ClusterDeploymentEvent,
  DeliveryContracts["ClusterDeploymentEvent"]
> = true;

void [clusterDeploymentMatchesWire, deploymentEventMatchesWire];
