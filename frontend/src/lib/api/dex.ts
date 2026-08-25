import {
  deleteAuthDexConnectorsById,
  getAuthDexConnectors,
  getAuthDexConnectorsById,
  getAuthDexConnectorTypes,
  getAuthDexSettings,
  patchAuthDexConnectorsById,
  postAuthDexApply,
  postAuthDexConnectors,
  postAuthDexRegisterAsSso,
  putAuthDexSettings,
} from "@/lib/api/generated/client";
import { idempotencyHeaderParams } from "@/lib/api/idempotency";
import type {
  DexApplyResponse,
  DexConnector,
  DexConnectorTypeSpec,
  DexConnectorWriteRequest,
  DexPublicClient,
  DexRegisterAsSSORequest,
  DexRegisterAsSSOResponse,
  DexSettings,
  DexSettingsWriteRequest,
} from "@/types";
import type { OpenAPIComponents } from "@/types/openapi.generated";

type DexConnectorWire = OpenAPIComponents["schemas"]["DexConnector"];
type DexConnectorTypeWire = OpenAPIComponents["schemas"]["DexConnectorType"];
type DexSettingsWire = OpenAPIComponents["schemas"]["DexSettings"];
type DexOperationWire = OpenAPIComponents["schemas"]["DexOperation"];

function requireData<T>(value: { data?: T }, operation: string): T {
  if (value.data === undefined) {
    throw new Error(`${operation} returned no data payload`);
  }
  return value.data;
}

function mapConnectorType(wire: DexConnectorTypeWire): DexConnectorTypeSpec {
  return {
    type: wire.type,
    displayHint: wire.display_hint,
    required: wire.required,
    optional: wire.optional,
    secret: wire.secret,
    nested: wire.nested,
  };
}

function mapConnector(wire: DexConnectorWire): DexConnector {
  return {
    id: wire.id,
    name: wire.name,
    type: wire.type,
    displayName: wire.display_name,
    config: wire.config,
    enabled: wire.enabled,
    createdAt: wire.created_at,
    updatedAt: wire.updated_at,
  };
}

function mapPublicClient(
  wire: NonNullable<DexSettingsWire["public_clients"]>[number],
): DexPublicClient {
  return {
    id: wire.id ?? "",
    name: wire.name,
    redirectURIs: wire.redirectURIs,
    secret: wire.secret,
    secretConfigured: wire.secret_configured,
    public: wire.public,
  };
}

function mapSettings(wire: DexSettingsWire): DexSettings {
  return {
    issuerUrl: wire.issuer_url ?? "",
    clusterId: wire.cluster_id ?? "",
    namespace: wire.namespace ?? "dex",
    releaseName: wire.release_name ?? "dex",
    runtimeSecretName: wire.runtime_secret_name ?? "astronomer-dex-runtime",
    publicClients: (wire.public_clients ?? []).map(mapPublicClient),
    expiry: wire.expiry ?? {},
    extra: wire.extra ?? {},
    configured: wire.configured ?? false,
    runtimePhase: wire.runtime_phase,
    runtimeGeneration: wire.runtime_generation,
    runtimeStagedGeneration: wire.runtime_staged_generation,
    runtimeAppliedGeneration: wire.runtime_applied_generation,
    updatedAt: wire.updated_at,
  };
}

export async function getDexConnectorTypes(): Promise<DexConnectorTypeSpec[]> {
  const response = await getAuthDexConnectorTypes();
  return requireData(response, "getDexConnectorTypes").map(mapConnectorType);
}

export async function getDexConnectors(): Promise<DexConnector[]> {
  const response = await getAuthDexConnectors();
  return requireData(response, "getDexConnectors").map(mapConnector);
}

export async function getDexConnector(id: string): Promise<DexConnector> {
  return mapConnector(
    requireData(
      await getAuthDexConnectorsById({ path: { id } }),
      "getDexConnector",
    ),
  );
}

export async function createDexConnector(
  data: DexConnectorWriteRequest,
): Promise<DexConnector> {
  const response = await postAuthDexConnectors({
    body: {
      type: data.type,
      name: data.name,
      display_name: data.displayName,
      config: data.config,
      enabled: data.enabled,
    },
  });
  return mapConnector(requireData(response, "createDexConnector"));
}

export async function updateDexConnector(
  id: string,
  data: Partial<DexConnectorWriteRequest>,
): Promise<DexConnector> {
  const response = await patchAuthDexConnectorsById({
    path: { id },
    body: {
      type: data.type,
      name: data.name,
      display_name: data.displayName,
      config: data.config,
      enabled: data.enabled,
    },
  });
  return mapConnector(requireData(response, "updateDexConnector"));
}

export async function deleteDexConnector(id: string): Promise<void> {
  await deleteAuthDexConnectorsById({ path: { id } });
}

export async function getDexSettings(): Promise<DexSettings> {
  return mapSettings(requireData(await getAuthDexSettings(), "getDexSettings"));
}

export async function updateDexSettings(
  data: DexSettingsWriteRequest,
): Promise<DexSettings> {
  const response = await putAuthDexSettings({ body: data });
  return mapSettings(requireData(response, "updateDexSettings"));
}

export async function applyDexConfig(): Promise<DexApplyResponse> {
  const wire = requireData(
    await postAuthDexApply({ headerParams: idempotencyHeaderParams() }),
    "applyDexConfig",
  ) as DexOperationWire;
  return {
    applied: wire.status === "succeeded",
    staged: wire.status !== "failed",
    runtimeState: wire.status === "succeeded" ? "applied" : "staged",
    clusterId: wire.target_id,
    namespace: "",
    runtimeSecretName: "",
    connectorCount: 0,
    runtimeGeneration: wire.runtime_generation,
    appliedAt: wire.completed_at ?? "",
  };
}

function mapRegisterResult(wire: DexOperationWire): DexRegisterAsSSOResponse {
  return {
    provider: "dex",
    id: wire.target_id,
    isEnabled: false,
    clientId: "",
    issuerUrl: "",
    displayName: "",
    verified: false,
    staged: wire.status !== "failed",
    applied: wire.status === "succeeded",
    runtimeState: wire.status === "succeeded" ? "applied" : "staged",
    secretResourceVersion: "",
    runtimeChanged: true,
    runtimeGeneration: wire.runtime_generation,
    created: false,
    updated: false,
  };
}

export async function registerDexAsSSO(
  data: DexRegisterAsSSORequest,
): Promise<DexRegisterAsSSOResponse> {
  const response = await postAuthDexRegisterAsSso({
    headerParams: idempotencyHeaderParams(),
    body: data,
  });
  return mapRegisterResult(requireData(response, "registerDexAsSSO"));
}
