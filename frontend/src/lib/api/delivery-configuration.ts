/** Flux delivery configuration templates and ordered override sets. */

import { camelizeKeys } from "@/lib/camelize";
import * as generated from "@/lib/api/generated/client";
import { requireEnvelopeData } from "@/lib/api/data-envelope";
import {
  optionalIdempotencyHeader,
  quotedETag,
  type DeliveryPage,
  type PageParams,
} from "@/lib/api/delivery-common";
import type { OpenAPIComponents } from "@/types/openapi.generated";
import type { CamelizeKeys } from "@/types/wire-contract";

type Contracts = OpenAPIComponents["schemas"];

export type DeliveryConfigurationTemplate = CamelizeKeys<
  Contracts["DeliveryConfigurationTemplate"]
>;
export type DeliveryConfigurationTemplateWrite =
  Contracts["DeliveryConfigurationTemplateWrite"] & { project_id: string };
export type DeliveryOverrideSet = CamelizeKeys<
  Contracts["DeliveryOverrideSet"]
>;
export type DeliveryOverrideSetWrite = Contracts["DeliveryOverrideSetWrite"] & {
  project_id: string;
};
export type DeliveryEffectiveConfiguration = CamelizeKeys<
  Contracts["DeliveryEffectiveConfiguration"]
>;

function mapConfigurationTemplate(
  wire: Contracts["DeliveryConfigurationTemplate"],
): DeliveryConfigurationTemplate {
  return camelizeKeys(wire) as unknown as DeliveryConfigurationTemplate;
}

function mapOverrideSet(
  wire: Contracts["DeliveryOverrideSet"],
): DeliveryOverrideSet {
  return camelizeKeys(wire) as unknown as DeliveryOverrideSet;
}

function mapLegacyPage<TWire, TView>(
  page: {
    data: TWire[];
    count: number;
    next: string | null;
    total_known: boolean;
  },
  params: PageParams,
  mapper: (wire: TWire) => TView,
): DeliveryPage<TView> {
  const offset = params.offset ?? 0;
  const limit = params.limit ?? Math.max(page.data.length, 1);
  const hasMore = page.next !== null;
  return {
    data: page.data.map(mapper),
    pagination: {
      total: page.total_known ? page.count : undefined,
      limit,
      offset,
      has_more: hasMore,
      next_offset: hasMore ? offset + page.data.length : null,
    },
  };
}

export async function listDeliveryConfigurationTemplates(
  projectId: string,
  params: PageParams = {},
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliveryConfigurationTemplates({
    query: { project_id: projectId, ...params },
    signal,
  });
  return mapLegacyPage(wire, params, mapConfigurationTemplate);
}

export async function createDeliveryConfigurationTemplate(
  body: DeliveryConfigurationTemplateWrite,
  key?: string,
  signal?: AbortSignal,
) {
  const wire = await generated.postDeliveryConfigurationTemplates({
    body,
    headerParams: optionalIdempotencyHeader(key),
    signal,
  });
  return mapConfigurationTemplate(
    requireEnvelopeData(wire, "delivery configuration template response"),
  );
}

export async function updateDeliveryConfigurationTemplate(
  projectId: string,
  id: string,
  generation: number,
  body: DeliveryConfigurationTemplateWrite,
  key?: string,
  signal?: AbortSignal,
) {
  const wire = await generated.putDeliveryConfigurationTemplatesById({
    path: { id },
    query: { project_id: projectId },
    body,
    headerParams: {
      "If-Match": quotedETag(generation),
      ...optionalIdempotencyHeader(key),
    },
    signal,
  });
  return mapConfigurationTemplate(
    requireEnvelopeData(wire, "delivery configuration template response"),
  );
}

export async function deleteDeliveryConfigurationTemplate(
  projectId: string,
  id: string,
  generation: number,
  key?: string,
  signal?: AbortSignal,
) {
  return generated.deleteDeliveryConfigurationTemplatesById({
    path: { id },
    query: { project_id: projectId },
    headerParams: {
      "If-Match": quotedETag(generation),
      ...optionalIdempotencyHeader(key),
    },
    signal,
  });
}

export async function listDeliveryOverrideSets(
  projectId: string,
  params: PageParams = {},
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliveryOverrideSets({
    query: { project_id: projectId, ...params },
    signal,
  });
  return mapLegacyPage(wire, params, mapOverrideSet);
}

export async function createDeliveryOverrideSet(
  body: DeliveryOverrideSetWrite,
  key?: string,
  signal?: AbortSignal,
) {
  const wire = await generated.postDeliveryOverrideSets({
    body,
    headerParams: optionalIdempotencyHeader(key),
    signal,
  });
  return mapOverrideSet(
    requireEnvelopeData(wire, "delivery override set response"),
  );
}

export async function updateDeliveryOverrideSet(
  projectId: string,
  id: string,
  generation: number,
  body: DeliveryOverrideSetWrite,
  key?: string,
  signal?: AbortSignal,
) {
  const wire = await generated.putDeliveryOverrideSetsById({
    path: { id },
    query: { project_id: projectId },
    body,
    headerParams: {
      "If-Match": quotedETag(generation),
      ...optionalIdempotencyHeader(key),
    },
    signal,
  });
  return mapOverrideSet(
    requireEnvelopeData(wire, "delivery override set response"),
  );
}

export async function deleteDeliveryOverrideSet(
  projectId: string,
  id: string,
  generation: number,
  key?: string,
  signal?: AbortSignal,
) {
  return generated.deleteDeliveryOverrideSetsById({
    path: { id },
    query: { project_id: projectId },
    headerParams: {
      "If-Match": quotedETag(generation),
      ...optionalIdempotencyHeader(key),
    },
    signal,
  });
}

export async function resolveDeliveryEffectiveConfiguration(
  projectId: string,
  baseValues: Record<string, unknown>,
  overrideIds: string[],
  signal?: AbortSignal,
): Promise<DeliveryEffectiveConfiguration> {
  const wire = await generated.postDeliveryOverrideSetsEffective({
    body: {
      project_id: projectId,
      base_values: baseValues,
      override_ids: overrideIds,
    },
    signal,
  });
  return camelizeKeys(
    requireEnvelopeData(wire, "effective delivery configuration response"),
  ) as unknown as DeliveryEffectiveConfiguration;
}
