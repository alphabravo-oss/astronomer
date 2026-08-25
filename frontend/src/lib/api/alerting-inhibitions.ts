/**
 * Alertmanager-style inhibition rules API client (P-03).
 *
 * Backend (mirrors the control_plane_silences model):
 *   GET    /api/v1/admin/alerting/inhibitions/         -> list
 *   POST   /api/v1/admin/alerting/inhibitions/         (admin) -> create
 *   GET    /api/v1/admin/alerting/inhibitions/{id}/    -> get
 *   PUT    /api/v1/admin/alerting/inhibitions/{id}/    (admin) -> update
 *   DELETE /api/v1/admin/alerting/inhibitions/{id}/    (admin) -> delete
 *
 * Generated operations retain the handler's snake_case wire JSON. Explicit
 * mappers below produce the camelCase view model used by the UI.
 *
 * Re-exported from ../api.ts via `export * from './api/alerting-inhibitions'`.
 */
import {
  deleteAdminAlertingInhibitionsById,
  getAdminAlertingInhibitions,
  getAdminAlertingInhibitionsById,
  postAdminAlertingInhibitions,
  putAdminAlertingInhibitionsById,
} from "@/lib/api/generated/client";
import type { AlertInhibition, InhibitionMatcher } from "@/types";
import type { OpenAPIComponents } from "@/types/openapi.generated";

type InhibitionWire = OpenAPIComponents["schemas"]["InhibitionResponse"];

function mapMatcher(
  matcher: OpenAPIComponents["schemas"]["InhibitionMatcherResponse"],
): InhibitionMatcher {
  return {
    label: matcher.label,
    value: matcher.value,
    isRegex: matcher.is_regex,
  };
}

function mapInhibition(wire: InhibitionWire): AlertInhibition {
  return {
    id: wire.id,
    name: wire.name,
    sourceMatchers: wire.source_matchers.map(mapMatcher),
    targetMatchers: wire.target_matchers.map(mapMatcher),
    equalLabels: wire.equal_labels,
    enabled: wire.enabled,
    createdAt: wire.created_at,
    updatedAt: wire.updated_at,
  };
}

// Write payload — snake_case matchers to match the Go handler json tags.
export interface InhibitionWriteRequest {
  name: string;
  source_matchers: Array<{ label: string; value: string; is_regex: boolean }>;
  target_matchers: Array<{ label: string; value: string; is_regex: boolean }>;
  equal_labels: string[];
  enabled: boolean;
}

function toWireMatchers(
  matchers: InhibitionMatcher[],
): Array<{ label: string; value: string; is_regex: boolean }> {
  return matchers.map((m) => ({
    label: m.label,
    value: m.value,
    is_regex: m.isRegex,
  }));
}

export interface InhibitionFormValues {
  name: string;
  sourceMatchers: InhibitionMatcher[];
  targetMatchers: InhibitionMatcher[];
  equalLabels: string[];
  enabled: boolean;
}

export function toInhibitionWriteRequest(
  values: InhibitionFormValues,
): InhibitionWriteRequest {
  return {
    name: values.name,
    source_matchers: toWireMatchers(values.sourceMatchers),
    target_matchers: toWireMatchers(values.targetMatchers),
    equal_labels: values.equalLabels,
    enabled: values.enabled,
  };
}

export async function listInhibitions(): Promise<AlertInhibition[]> {
  const response = await getAdminAlertingInhibitions();
  return response.data.map(mapInhibition);
}

export async function getInhibition(id: string): Promise<AlertInhibition> {
  const response = await getAdminAlertingInhibitionsById({ path: { id } });
  return mapInhibition(response.data);
}

export async function createInhibition(
  body: InhibitionWriteRequest,
): Promise<AlertInhibition> {
  const response = await postAdminAlertingInhibitions({ body });
  return mapInhibition(response.data);
}

export async function updateInhibition(
  id: string,
  body: InhibitionWriteRequest,
): Promise<AlertInhibition> {
  const response = await putAdminAlertingInhibitionsById({
    path: { id },
    body,
  });
  return mapInhibition(response.data);
}

export async function deleteInhibition(id: string): Promise<void> {
  await deleteAdminAlertingInhibitionsById({ path: { id } });
}
