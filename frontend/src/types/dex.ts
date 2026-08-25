// === Phase B4: Dex ===
//
// The generated OpenAPI schemas are the source of truth for response shapes.
// The Dex API adapter maps snake_case wire values into the camelCase view types
// exported here. Connector `config` payloads remain `Record<string, unknown>`
// because their schema is selected at runtime from the connector registry.

import type { OpenAPIComponents } from "@/types/openapi.generated";
import type { CamelizeKeys } from "@/types/wire-contract";

/** Connector type registry entry. GET /api/v1/auth/dex/connector-types/ */
export interface DexConnectorTypeSpec {
  type: string;
  displayHint: string;
  required: string[];
  optional: string[];
  /** Field names whose values are sensitive. The wizard renders these as
   *  password inputs; the API redacts them and sets `__<name>_set` on read. */
  secret: string[];
  /** Required nested fields, e.g. `userSearch.{baseDN,...}` for ldap. */
  nested: Array<{ parent: string; keys: string[] }>;
}

/** Configured connector row. GET /api/v1/auth/dex/connectors/ */
export type DexConnector = CamelizeKeys<
  OpenAPIComponents["schemas"]["DexConnector"]
>;

/** Body for POST /connectors/ and PATCH /connectors/{id}/ */
export interface DexConnectorWriteRequest {
  type: string;
  name: string;
  displayName: string;
  config: Record<string, unknown>;
  enabled?: boolean;
}

/** Public client entry under DexSettings.publicClients. */
export interface DexPublicClient {
  id: string;
  name?: string;
  redirectURIs?: string[];
  secret?: string;
  secretConfigured?: boolean;
  public?: boolean;
}

/** Singleton settings. GET / PUT /api/v1/auth/dex/settings/.
 * Required view fields are defaults applied by the Dex API adapter. */
export type DexSettings = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["DexSettings"]>,
  | "issuerUrl"
  | "clusterId"
  | "namespace"
  | "releaseName"
  | "runtimeSecretName"
  | "publicClients"
  | "expiry"
  | "extra"
  | "configured"
> & {
  issuerUrl: string;
  clusterId: string;
  namespace: string;
  releaseName: string;
  runtimeSecretName: string;
  publicClients: DexPublicClient[];
  expiry: Record<string, unknown>;
  extra: Record<string, unknown>;
  configured: boolean;
  runtimePhase?: "fresh" | "prepare" | "cutover";
  runtimeGeneration?: number;
  runtimeStagedGeneration?: number;
  runtimeAppliedGeneration?: number;
  updatedAt?: string;
};

/** Body for PUT /settings/. snake_case to match the Go handler exactly —
 *  the request interceptor does not transform outbound bodies. */
export interface DexSettingsWriteRequest {
  issuer_url: string;
  cluster_id?: string;
  namespace?: string;
  release_name?: string;
  chart_release_name?: string;
  deployment_name?: string;
  service_name?: string;
  runtime_secret_name?: string;
  public_clients?: DexPublicClient[];
  expiry?: Record<string, unknown>;
  extra?: Record<string, unknown>;
}

/** Response from POST /apply/ */
export interface DexApplyResponse {
  applied: boolean;
  staged: boolean;
  runtimeState: "staged" | "applied";
  clusterId: string;
  namespace: string;
  runtimeSecretName: string;
  connectorCount: number;
  runtimeGeneration?: number;
  appliedAt: string;
}

/** Body for POST /register-as-sso/ */
export interface DexRegisterAsSSORequest {
  client_id?: string;
  client_secret?: string;
  display_name?: string;
}

/** Response from POST /register-as-sso/ */
export interface DexRegisterAsSSOResponse {
  provider: string;
  id: string;
  isEnabled: boolean;
  clientId: string;
  issuerUrl: string;
  displayName: string;
  verified: boolean;
  staged?: boolean;
  applied?: boolean;
  runtimeState?: "staged" | "applied";
  secretResourceVersion: string;
  runtimeChanged?: boolean;
  runtimeGeneration?: number;
  created?: boolean;
  updated?: boolean;
}
