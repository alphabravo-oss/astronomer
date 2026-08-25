import { beforeEach, describe, expect, it, vi } from "vitest";

const { request } = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock("@/lib/api/transport", () => ({ default: { request } }));

import {
  GITOPS_AUTH_SENTINEL,
  createGitOpsSource,
  deleteGitOpsSource,
  getGitOpsSource,
  listGitOpsSourceClusters,
  listGitOpsSources,
  previewGitOpsSource,
  syncGitOpsSource,
  updateGitOpsSource,
  type GitOpsSourceWriteRequest,
} from "./gitops";

const SOURCE = {
  id: "10000000-0000-0000-0000-000000000001",
  name: "platform-clusters",
  repo_url: "https://github.com/example/clusters.git",
  branch: "main",
  path_prefix: "clusters",
  auth_mode: "https_token",
  auth: GITOPS_AUTH_SENTINEL,
  auth_configured: true,
  sync_mode: "interval",
  sync_interval_seconds: 60,
  on_delete: "decommission",
  enabled: true,
  allow_mass_decommission: false,
  created_at: "2026-08-23T00:00:00Z",
  updated_at: "2026-08-23T00:00:00Z",
} as const;

const CLUSTER = {
  cluster_id: "20000000-0000-0000-0000-000000000001",
  cluster_name: "production-west",
  display_name: "Production West",
  repo_path: "clusters/production-west.yaml",
  last_yaml_sha: "abc123",
  last_applied_at: "2026-08-23T01:00:00Z",
  status: "active",
} as const;

const PREVIEW = {
  head_sha: "def456",
  source_id: SOURCE.id,
  source_name: SOURCE.name,
  applies: [
    {
      cluster_name: CLUSTER.cluster_name,
      cluster_id: CLUSTER.cluster_id,
      repo_path: CLUSTER.repo_path,
      created: false,
      updated: true,
      no_op: false,
      template_bound: false,
      registries: [],
      tool_presets: [],
      restored_active: false,
    },
  ],
  would_miss: [],
  would_restore: [],
  on_delete_policy: "decommission",
} as const;

const BODY: GitOpsSourceWriteRequest = {
  name: SOURCE.name,
  repo_url: SOURCE.repo_url,
  branch: SOURCE.branch,
  path_prefix: SOURCE.path_prefix,
  auth_mode: SOURCE.auth_mode,
  auth: "rotated-secret",
  sync_mode: SOURCE.sync_mode,
  sync_interval_seconds: SOURCE.sync_interval_seconds,
  on_delete: SOURCE.on_delete,
  enabled: true,
};

function lastRequest() {
  return request.mock.calls.at(-1)?.[0];
}

beforeEach(() => {
  request.mockReset();
  request.mockImplementation(async (config) => {
    const url = String(config.url);
    if (config.method === "DELETE") return { data: undefined };
    if (url === "/api/v1/admin/gitops-sources" && config.method === "GET") {
      return { data: { data: [SOURCE], pagination: {} } };
    }
    if (url.endsWith("/clusters")) {
      return { data: { data: [CLUSTER], pagination: {} } };
    }
    if (url.endsWith("/preview")) return { data: { data: PREVIEW } };
    if (url.endsWith("/sync")) {
      return {
        data: {
          data: {
            status: "queued",
            task_id: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee",
          },
        },
      };
    }
    return { data: { data: SOURCE } };
  });
});

describe("generated GitOps source boundary", () => {
  it("uses real pagination for source and managed-cluster lists", async () => {
    await expect(listGitOpsSources()).resolves.toEqual([SOURCE]);
    expect(lastRequest()).toEqual(
      expect.objectContaining({
        method: "GET",
        url: "/api/v1/admin/gitops-sources",
        params: { limit: 200 },
      }),
    );
    await expect(listGitOpsSourceClusters(SOURCE.id)).resolves.toEqual([
      CLUSTER,
    ]);
    expect(lastRequest()?.params).toEqual({ limit: 200 });
  });

  it("preserves exact snake_case mutation bodies and paths", async () => {
    await expect(createGitOpsSource(BODY)).resolves.toEqual(SOURCE);
    expect(lastRequest()).toEqual(
      expect.objectContaining({
        method: "POST",
        url: "/api/v1/admin/gitops-sources",
        data: BODY,
      }),
    );
    await updateGitOpsSource(SOURCE.id, {
      allow_mass_decommission: true,
    });
    expect(lastRequest()?.data).toEqual({ allow_mass_decommission: true });
    await deleteGitOpsSource(SOURCE.id);
    expect(lastRequest()?.method).toBe("DELETE");
  });

  it("returns exact preview data and validates sync envelopes", async () => {
    await expect(previewGitOpsSource(SOURCE.id)).resolves.toEqual(PREVIEW);
    await expect(syncGitOpsSource(SOURCE.id)).resolves.toEqual({
      status: "queued",
      task_id: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee",
    });
    request.mockResolvedValueOnce({ data: {} });
    await expect(syncGitOpsSource(SOURCE.id)).rejects.toThrow(
      "syncGitOpsSource returned no data payload",
    );
  });

  it("fails closed when a single-source envelope is malformed", async () => {
    request.mockResolvedValueOnce({ data: {} });
    await expect(getGitOpsSource(SOURCE.id)).rejects.toThrow(
      "getGitOpsSource returned no data payload",
    );
  });
});
