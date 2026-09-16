/** Delivery controller, inventory, compatibility, and estate operations. */

import { camelizeKeys } from "@/lib/camelize";
import * as generated from "@/lib/api/generated/client";
import { requireEnvelopeData } from "@/lib/api/data-envelope";
import type { DeliveryContracts } from "@/lib/api/delivery-common";
import {
  mapClusterDeployment,
  type ClusterDeployment,
} from "@/lib/api/delivery-deployments";
import type { OpenAPIComponents } from "@/types/openapi.generated";
import type {
  AssertNoPhantomWireKeys,
  CamelizeKeys,
} from "@/types/wire-contract";

export type DeliveryControllerInventory = CamelizeKeys<
  OpenAPIComponents["schemas"]["DeliveryControllerInventory"]
>;

export type ClusterDeliveryInventory = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["DeliveryClusterInventory"]>,
  "deployments"
> & { deployments: ClusterDeployment[] };

export type DeliverySystemCompatibility = CamelizeKeys<
  OpenAPIComponents["schemas"]["DeliverySystemCompatibility"]
>;

export type DeliveryEstateCount = CamelizeKeys<
  OpenAPIComponents["schemas"]["DeliveryEstateCount"]
>;

export type DeliveryEstateSummary = CamelizeKeys<
  OpenAPIComponents["schemas"]["DeliveryEstateSummary"]
>;

export type DeliveryEstateCluster = CamelizeKeys<
  OpenAPIComponents["schemas"]["DeliveryEstateCluster"]
>;

export type DeliveryEstateAttention = CamelizeKeys<
  OpenAPIComponents["schemas"]["DeliveryEstateAttention"]
>;

export type DeliveryEstateDistributions = CamelizeKeys<
  OpenAPIComponents["schemas"]["DeliveryEstateDistributions"]
>;

export type DeliveryEstate = CamelizeKeys<
  OpenAPIComponents["schemas"]["DeliveryEstate"]
>;

function mapInventory(
  wire: DeliveryContracts["DeliveryClusterInventory"],
): ClusterDeliveryInventory {
  return {
    ...camelizeKeys(wire),
    deployments: wire.deployments.map(mapClusterDeployment),
  } as unknown as ClusterDeliveryInventory;
}
function mapCompatibility(
  wire: DeliveryContracts["DeliverySystemCompatibility"],
): DeliverySystemCompatibility {
  return camelizeKeys(wire) as unknown as DeliverySystemCompatibility;
}
function mapEstate(wire: DeliveryContracts["DeliveryEstate"]): DeliveryEstate {
  return camelizeKeys(wire) as unknown as DeliveryEstate;
}

export async function getClusterDeliveryInventory(
  projectId: string,
  clusterId: string,
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliveryClustersByClusterIdInventory({
    path: { clusterId },
    query: { project_id: projectId },
    signal,
  });
  return mapInventory(requireEnvelopeData(wire, "delivery response"));
}

export async function getDeliverySystemCompatibility(signal?: AbortSignal) {
  const wire = await generated.getDeliverySystemCompatibility({ signal });
  return mapCompatibility(requireEnvelopeData(wire, "delivery response"));
}

export async function getDeliveryEstate(signal?: AbortSignal) {
  const wire = await generated.getDeliveryEstate({ signal });
  return mapEstate(requireEnvelopeData(wire, "delivery response"));
}

const controllerInventoryMatchesWire: AssertNoPhantomWireKeys<
  DeliveryControllerInventory,
  DeliveryContracts["DeliveryControllerInventory"]
> = true;
const clusterInventoryMatchesWire: AssertNoPhantomWireKeys<
  ClusterDeliveryInventory,
  DeliveryContracts["DeliveryClusterInventory"]
> = true;
const systemCompatibilityMatchesWire: AssertNoPhantomWireKeys<
  DeliverySystemCompatibility,
  DeliveryContracts["DeliverySystemCompatibility"]
> = true;
const deliveryFleetMatchesWire: AssertNoPhantomWireKeys<
  DeliveryEstate,
  DeliveryContracts["DeliveryEstate"]
> = true;

void [
  controllerInventoryMatchesWire,
  clusterInventoryMatchesWire,
  systemCompatibilityMatchesWire,
  deliveryFleetMatchesWire,
];
