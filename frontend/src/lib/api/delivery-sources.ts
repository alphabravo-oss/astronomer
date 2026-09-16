/** Delivery source contracts and operations. */

import { camelizeKeys } from "@/lib/camelize";
import * as generated from "@/lib/api/generated/client";
import { requireEnvelopeData } from "@/lib/api/data-envelope";
import {
  mapPage,
  optionalIdempotencyHeader,
  type DeliveryContracts,
  type PageParams,
  type TrustPolicyRequest,
} from "@/lib/api/delivery-common";
import type { OpenAPIComponents } from "@/types/openapi.generated";
import type { CamelizeKeys } from "@/types/wire-contract";

export type DeliverySourceType =
  "git" | "oci_artifact" | "helm_http" | "helm_oci";
export type DeliveryAuthMode =
  "none" | "basic" | "bearer" | "ssh" | "workload_identity";

export interface SourceCredentialInput {
  username?: string;
  password?: string;
  token?: string;
  private_key?: string;
  known_hosts?: string;
  passphrase?: string;
}

export type DeliverySource = CamelizeKeys<
  OpenAPIComponents["schemas"]["DeliverySource"]
>;

export interface CreateDeliverySourceRequest {
  project_id: string;
  name: string;
  description?: string;
  type: DeliverySourceType;
  url: string;
  auth_mode: DeliveryAuthMode;
  credential?: SourceCredentialInput;
  ca_bundle?: string;
  proxy_ref?: string;
  trust_policy: TrustPolicyRequest;
}

export interface RotateSourceCredentialRequest {
  project_id: string;
  auth_mode: DeliveryAuthMode;
  credential: SourceCredentialInput;
}

export interface SourceVerification {
  id: string;
  sourceId: string;
  status: string;
}

function mapSource(wire: DeliveryContracts["DeliverySource"]): DeliverySource {
  return camelizeKeys(wire) as unknown as DeliverySource;
}

export async function listDeliverySources(
  projectId: string,
  params: PageParams & { status?: string } = {},
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliverySources({
    query: {
      project_id: projectId,
      limit: params.limit,
      offset: params.offset,
      status: params.status as
        "pending" | "ready" | "degraded" | "revoked" | undefined,
    },
    signal,
  });
  return mapPage(wire, mapSource);
}

export async function createDeliverySource(
  body: CreateDeliverySourceRequest,
  key?: string,
  signal?: AbortSignal,
) {
  const wire = await generated.postDeliverySources({
    body,
    headerParams: optionalIdempotencyHeader(key),
    signal,
  });
  return mapSource(requireEnvelopeData(wire, "delivery response"));
}

export async function getDeliverySource(
  projectId: string,
  id: string,
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliverySourcesById({
    path: { id },
    query: { project_id: projectId },
    signal,
  });
  return mapSource(requireEnvelopeData(wire, "delivery response"));
}

export async function deleteDeliverySource(
  projectId: string,
  id: string,
  key?: string,
  signal?: AbortSignal,
) {
  await generated.deleteDeliverySourcesById({
    path: { id },
    query: { project_id: projectId },
    headers: optionalIdempotencyHeader(key),
    signal,
  });
}

export async function rotateDeliverySourceCredential(
  id: string,
  body: RotateSourceCredentialRequest,
  key?: string,
  signal?: AbortSignal,
) {
  if (!["basic", "bearer", "ssh"].includes(body.auth_mode)) {
    throw new Error("Credential rotation requires basic, bearer, or SSH auth.");
  }
  const wire = await generated.postDeliverySourcesByIdRotateCredential({
    path: { id },
    body: body as DeliveryContracts["DeliverySourceCredentialRotate"],
    headerParams: optionalIdempotencyHeader(key),
    signal,
  });
  return mapSource(requireEnvelopeData(wire, "delivery response"));
}

export async function verifyDeliverySource(
  id: string,
  body: { project_id: string; requested_revision: string; chart?: string },
  key?: string,
  signal?: AbortSignal,
) {
  const wire = await generated.postDeliverySourcesByIdVerify({
    path: { id },
    body,
    headerParams: optionalIdempotencyHeader(key),
    signal,
  });
  return camelizeKeys(
    requireEnvelopeData(wire, "delivery response"),
  ) as unknown as SourceVerification;
}
