/** Generated GitOps registration-source API boundary. */
import {
  deleteAdminGitopsSourcesById,
  getAdminGitopsSources,
  getAdminGitopsSourcesById,
  getAdminGitopsSourcesByIdClusters,
  getAdminGitopsSourcesByIdPreview,
  postAdminGitopsSources,
  postAdminGitopsSourcesByIdSync,
  putAdminGitopsSourcesById,
} from "@/lib/api/generated/client";
import { idempotencyHeaderParams } from "@/lib/api/idempotency";
import type { OpenAPIComponents } from "@/types/openapi.generated";

export type {
  GitOpsApplyPreview,
  GitOpsManagedCluster,
  GitOpsPreviewResult,
  GitOpsSource,
  GitOpsSyncResult,
} from "@/types/openapi.generated";

type Contracts = OpenAPIComponents["schemas"];

export type GitOpsAuthMode = Contracts["GitOpsSource"]["auth_mode"];
export type GitOpsSyncMode = Contracts["GitOpsSource"]["sync_mode"];
export type GitOpsOnDelete = Contracts["GitOpsSource"]["on_delete"];
export type GitOpsSourceWriteRequest = Omit<
  Contracts["GitOpsSourceRequest"],
  "name" | "repo_url"
> & {
  name: string;
  repo_url: string;
};

/** Redacted value returned when a source has stored authentication material. */
export const GITOPS_AUTH_SENTINEL = "<encrypted>";

function requireData<T>(response: { data?: T }, operation: string): T {
  if (response.data === undefined) {
    throw new Error(`${operation} returned no data payload`);
  }
  return response.data;
}

export async function listGitOpsSources() {
  const page = await getAdminGitopsSources({ query: { limit: 200 } });
  return page.data;
}

export async function getGitOpsSource(id: string) {
  return requireData(
    await getAdminGitopsSourcesById({ path: { id } }),
    "getGitOpsSource",
  );
}

export async function createGitOpsSource(body: GitOpsSourceWriteRequest) {
  return requireData(
    await postAdminGitopsSources({ body }),
    "createGitOpsSource",
  );
}

export async function updateGitOpsSource(
  id: string,
  body: Partial<GitOpsSourceWriteRequest>,
) {
  return requireData(
    await putAdminGitopsSourcesById({ path: { id }, body }),
    "updateGitOpsSource",
  );
}

export async function deleteGitOpsSource(id: string): Promise<void> {
  await deleteAdminGitopsSourcesById({ path: { id } });
}

export async function syncGitOpsSource(id: string) {
  return requireData(
    await postAdminGitopsSourcesByIdSync({
      path: { id },
      headerParams: idempotencyHeaderParams(),
    }),
    "syncGitOpsSource",
  );
}

export async function previewGitOpsSource(id: string) {
  return requireData(
    await getAdminGitopsSourcesByIdPreview({ path: { id } }),
    "previewGitOpsSource",
  );
}

export async function listGitOpsSourceClusters(id: string) {
  const page = await getAdminGitopsSourcesByIdClusters({
    path: { id },
    query: { limit: 200 },
  });
  return page.data;
}
