import type { Mocked } from "vitest";
import api from "@/lib/api";
import {
  actOnClusterDeployment,
  approveDeliveryRollout,
  createDeliverySource,
  createDeliveryTarget,
  getDeliveryFleet,
  listDeliverySources,
  previewDeliveryTarget,
  startDeliveryRollout,
  verifyDeliverySource,
  type CreateDeliverySourceRequest,
  type DeliveryTargetRequest,
  type RolloutStrategyRequest,
} from "./delivery";

vi.mock("@/lib/api", () => ({
  __esModule: true,
  default: {
    get: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
    delete: vi.fn(),
  },
}));

const mockedApi = api as Mocked<typeof api>;

const strategy: RolloutStrategyRequest = {
  type: "rolling",
  max_concurrent: 2,
  max_unavailable: { type: "count", value: 1 },
  min_ready: "30s",
  progress_deadline: "30m",
  failure_threshold: { type: "count", value: 1 },
  on_failure: "pause",
  respect_maintenance_windows: true,
};

describe("delivery API client", () => {
  beforeEach(() => vi.clearAllMocks());

  it("keeps list scope and pagination server-side", async () => {
    mockedApi.get.mockResolvedValueOnce({
      data: {
        data: [],
        count: 0,
        next: null,
        previous: null,
        totalKnown: true,
      },
    });

    await listDeliverySources("project-1", {
      limit: 25,
      offset: 50,
      status: "ready",
    });

    expect(mockedApi.get).toHaveBeenCalledWith("/delivery/sources/", {
      params: {
        project_id: "project-1",
        limit: 25,
        offset: 50,
        status: "ready",
      },
    });
  });

  it("sends source credentials only on writes with an idempotency key", async () => {
    const request: CreateDeliverySourceRequest = {
      project_id: "project-1",
      name: "private-charts",
      type: "helm_oci",
      url: "oci://registry.example.test/charts",
      auth_mode: "bearer",
      credential: { token: "write-only-token" },
      trust_policy: { allow_unsigned: false, provider: "cosign_keyless" },
    };
    mockedApi.post.mockResolvedValueOnce({
      data: {
        data: {
          id: "source-1",
          credential: { configured: true, keyVersion: 1, epoch: 1 },
        },
      },
    });

    const result = await createDeliverySource(request, "request-1");

    expect(mockedApi.post).toHaveBeenCalledWith("/delivery/sources/", request, {
      headers: { "Idempotency-Key": "request-1" },
    });
    expect(result).not.toHaveProperty("token");
    expect(result.credential.configured).toBe(true);
  });

  it("creates targets from an immutable version and preserves the response ETag", async () => {
    const request: DeliveryTargetRequest = {
      project_id: "project-1",
      name: "monitoring",
      bundle_version_id: "version-1",
      placement: { all_clusters: false, cluster_group_ids: ["group-1"] },
      rollout_policy: { approval_required: true },
      reconciliation_policy: {
        interval: "5m",
        retry_interval: "1m",
        timeout: "10m",
        prune: true,
        wait: true,
        drift: "repair",
      },
      suspended: false,
    };
    mockedApi.post.mockResolvedValueOnce({
      data: { data: { id: "target-1", bundleVersionId: "version-1" } },
      headers: { get: (name: string) => (name === "etag" ? '"7"' : undefined) },
    });

    await expect(createDeliveryTarget(request, "request-2")).resolves.toEqual({
      data: expect.objectContaining({ id: "target-1" }),
      etag: '"7"',
    });
  });

  it("binds rollout launch to preview digest, generation, and idempotency", async () => {
    mockedApi.post.mockResolvedValueOnce({
      data: { data: { id: "rollout-1" } },
    });

    await startDeliveryRollout(
      "target-1",
      {
        project_id: "project-1",
        preview_digest: "sha256:preview",
        confirm_all_clusters: false,
        strategy,
      },
      7,
      "request-3",
    );

    expect(mockedApi.post).toHaveBeenCalledWith(
      "/delivery/targets/target-1/rollouts/",
      expect.objectContaining({ preview_digest: "sha256:preview", strategy }),
      { headers: { "If-Match": '"7"', "Idempotency-Key": "request-3" } },
    );
  });

  it("requests placement decisions with the digest-bound cursor contract", async () => {
    mockedApi.post.mockResolvedValueOnce({
      data: {
        data: {
          targetId: "target-1",
          previewDigest: "sha256:preview",
          decisions: [],
          decisionCount: 125,
          decisionOffset: 100,
          decisionPageSize: 100,
          hasMoreDecisions: false,
          nextCursor: "",
        },
      },
    });

    await previewDeliveryTarget("project-1", "target-1", {
      pageSize: 100,
      cursor: "opaque-digest-bound-cursor",
    });

    expect(mockedApi.post).toHaveBeenCalledWith(
      "/delivery/targets/target-1/preview/",
      undefined,
      {
        params: {
          project_id: "project-1",
          page_size: 100,
          cursor: "opaque-digest-bound-cursor",
        },
      },
    );
  });

  it("treats source verification as a bounded queued operation", async () => {
    mockedApi.post.mockResolvedValueOnce({
      data: {
        data: { id: "resolution-1", sourceId: "source-1", status: "pending" },
      },
    });

    await expect(
      verifyDeliverySource(
        "source-1",
        { project_id: "project-1", requested_revision: "v1.2.3" },
        "request-verify",
      ),
    ).resolves.toEqual({
      id: "resolution-1",
      sourceId: "source-1",
      status: "pending",
    });
  });

  it("generation-fences approval and deployment controls", async () => {
    mockedApi.post
      .mockResolvedValueOnce({
        data: { data: { rollout: {}, approval: {}, event: {} } },
        headers: { etag: '"9"' },
      })
      .mockResolvedValueOnce({
        data: { data: { deployment: {}, event: {} } },
        headers: { etag: '"10"' },
      });

    await approveDeliveryRollout(
      "rollout-1",
      {
        project_id: "project-1",
        cohort: 1,
        binding_digest: "sha256:binding",
        decision: "approved",
        expires_at: "2026-08-17T12:00:00Z",
      },
      9,
      "request-4",
    );
    await actOnClusterDeployment(
      "project-1",
      "deployment-1",
      "reconcile",
      10,
      "manual_reconcile",
      "request-5",
    );

    expect(mockedApi.post).toHaveBeenNthCalledWith(
      1,
      "/delivery/rollouts/rollout-1/approve/",
      expect.objectContaining({
        binding_digest: "sha256:binding",
        expires_at: "2026-08-17T12:00:00Z",
      }),
      { headers: { "If-Match": '"9"', "Idempotency-Key": "request-4" } },
    );
    expect(mockedApi.post).toHaveBeenNthCalledWith(
      2,
      "/delivery/deployments/deployment-1/reconcile/",
      { project_id: "project-1", reason_code: "manual_reconcile" },
      { headers: { "If-Match": '"10"', "Idempotency-Key": "request-5" } },
    );
  });

  it("loads the fleet scoreboard without a project scope", async () => {
    mockedApi.get.mockResolvedValueOnce({
      data: {
        data: {
          summary: {
            adoptedClusters: 2,
            fluxReady: 2,
            incompatible: 0,
            disconnected: 0,
            stale: 0,
            assignments: 4,
            drifted: 0,
            failed: 0,
            degraded: 0,
            activeRollouts: 0,
          },
          clusters: [],
          attention: [],
          distributions: {
            compatibility: [],
            privilege: [],
            assignmentPhases: [],
          },
        },
      },
    });

    await getDeliveryFleet();

    expect(mockedApi.get).toHaveBeenCalledWith("/delivery/fleet/");
  });
});
