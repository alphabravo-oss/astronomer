import {
  createSupportBundle as createSupportBundleRequest,
  executeOpenAPIOperationWithResponse,
  getSupportBundleOperation as getSupportBundleOperationRequest,
} from "@/lib/api/generated/client";
import { createIdempotencyKey } from "@/lib/api/idempotency";
import type { OpenAPIComponents } from "@/types/openapi.generated";

export type SupportBundleOperation =
  OpenAPIComponents["schemas"]["SupportBundleOperation"];

function requireOperation(
  operation: SupportBundleOperation | undefined,
): SupportBundleOperation {
  if (!operation) throw new Error("Support bundle operation was not returned");
  return operation;
}

export async function createSupportBundle(
  signal?: AbortSignal,
): Promise<SupportBundleOperation> {
  const response = await createSupportBundleRequest({
    headerParams: { "Idempotency-Key": createIdempotencyKey() },
    signal,
  });
  return requireOperation(response.data);
}

export async function getSupportBundleOperation(
  id: string,
  signal?: AbortSignal,
): Promise<SupportBundleOperation> {
  const response = await getSupportBundleOperationRequest({ path: { id }, signal });
  return requireOperation(response.data);
}

export async function downloadSupportBundleArtifact(
  id: string,
  signal?: AbortSignal,
): Promise<{ blob: Blob; filename: string }> {
  const response = await executeOpenAPIOperationWithResponse(
    "downloadSupportBundle",
    { path: { id }, signal },
  );
  const disposition = String(response.headers["content-disposition"] ?? "");
  const filename =
    /filename="([^"]+)"/.exec(disposition)?.[1] ??
    `astronomer-support-bundle-${id}.zip`;
  return { blob: response.data as unknown as Blob, filename };
}
