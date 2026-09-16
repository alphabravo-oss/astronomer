/** Shared transport primitives for Flux-native delivery domains. */

import type { OpenAPIResponse } from "@/lib/api/generated/client";
import { createIdempotencyKey } from "@/lib/api/idempotency";
import { mapPage as mapCanonicalPage } from "@/lib/api/pagination";
import { requireEnvelopeData } from "@/lib/api/data-envelope";
import type { PaginatedResponse, PaginationMetadata } from "@/types";
import type { OpenAPIComponents } from "@/types/openapi.generated";

export type DeliveryContracts = OpenAPIComponents["schemas"];
export type DeliveryPage<T> = PaginatedResponse<T>;
export type SignatureProvider = "cosign_key" | "cosign_keyless" | "git";

export interface EntityResponse<T> {
  data: T;
  etag?: string;
}

export interface PageParams {
  limit?: number;
  offset?: number;
}

export interface TrustPolicy {
  allowUnsigned: boolean;
  provider?: SignatureProvider;
  identity?: string;
  issuer?: string;
  keyRef?: string;
}

export interface TrustPolicyRequest {
  allow_unsigned: boolean;
  provider?: SignatureProvider;
  identity?: string;
  issuer?: string;
  key_ref?: string;
}

export function quotedETag(etag: string | number): string {
  if (typeof etag === "number") return `"${etag}"`;
  return etag.startsWith('"') ? etag : `"${etag}"`;
}

export function optionalIdempotencyHeader(key?: string): {
  "Idempotency-Key": string;
} {
  return { "Idempotency-Key": key ?? createIdempotencyKey() };
}

export function mapPage<TWire, TView>(
  page: { data: TWire[]; pagination: PaginationMetadata },
  mapper: (wire: TWire) => TView,
): DeliveryPage<TView> {
  return mapCanonicalPage(page, mapper);
}

export function entityFromResponse<TWire, TView>(
  response: OpenAPIResponse<{ data?: TWire }>,
  mapper: (wire: TWire) => TView,
): EntityResponse<TView> {
  const headers = response.headers as {
    etag?: unknown;
    get?: (name: string) => unknown;
  };
  const value = headers.etag ?? headers.get?.("etag");
  return {
    data: mapper(requireEnvelopeData(response.data, "delivery response")),
    etag: typeof value === "string" ? value : undefined,
  };
}
