import { getCatalogControllerStatus } from "@/lib/api/generated/client";
import type { OpenAPIReadComponents } from "@/types/categories.generated";

export type CatalogControllerOperationWindow =
  OpenAPIReadComponents["schemas"]["CatalogControllerOperationWindow"];

export function parseCatalogControllerWindow(value: unknown): CatalogControllerOperationWindow | null {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error("Catalog controller summary is unavailable");
  }
  if (!("operationSummary" in value)) return null;
  const window = value.operationSummary;
  if (typeof window !== "object" || window === null || Array.isArray(window)) {
    throw new Error("Catalog controller operation window is malformed");
  }
  const fields = window as Record<string, unknown>;
  if (
    (fields.scope !== "global" && fields.scope !== "authorized-clusters") ||
    fields.windowLimit !== 1000 ||
    typeof fields.returned !== "number" || !Number.isSafeInteger(fields.returned) || fields.returned < 0 || fields.returned > 1000 ||
    (fields.total !== null && (typeof fields.total !== "number" || !Number.isSafeInteger(fields.total) || fields.total < 0)) ||
    typeof fields.complete !== "boolean" ||
    typeof fields.notice !== "string" || Array.from(fields.notice).length > 512 ||
    (fields.complete && fields.total !== fields.returned)
  ) throw new Error("Catalog controller operation window is malformed");
  return {
    scope: fields.scope,
    windowLimit: 1000,
    returned: fields.returned,
    total: fields.total as number | null,
    complete: fields.complete,
    notice: fields.notice,
  };
}

export async function getCatalogControllerWindow(signal?: AbortSignal): Promise<CatalogControllerOperationWindow | null> {
  const response = await getCatalogControllerStatus({ signal });
  return parseCatalogControllerWindow(response.data);
}
