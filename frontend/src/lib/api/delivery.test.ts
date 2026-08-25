import * as generated from "@/lib/api/generated/client";
import {
  actOnClusterDeployment,
  createDeliverySource,
  createDeliveryTarget,
  getDeliveryEstate,
  listDeliverySources,
  previewDeliveryTarget,
  startDeliveryRollout,
  type CreateDeliverySourceRequest,
  type DeliveryTargetRequest,
  type RolloutStrategyRequest,
} from "./delivery";

vi.mock("@/lib/api/generated/client", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("@/lib/api/generated/client")>();
  return {
    ...actual,
    getDeliverySources: vi.fn(),
    postDeliverySources: vi.fn(),
    postDeliveryTargetsByIdPreview: vi.fn(),
    postDeliveryTargetsByIdRollouts: vi.fn(),
    getDeliveryEstate: vi.fn(),
    executeOpenAPIOperationWithResponse: vi.fn(),
  };
});

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

describe("delivery generated API boundary", () => {
  beforeEach(() => vi.clearAllMocks());

  it("passes project filters and cancellation to the generated list operation", async () => {
    vi.mocked(generated.getDeliverySources).mockResolvedValueOnce({
      data: [],
      count: 0,
      next: null,
      previous: null,
      total_known: true,
    });
    const controller = new AbortController();

    await listDeliverySources(
      "project-1",
      { limit: 25, offset: 50, status: "ready" },
      controller.signal,
    );

    expect(generated.getDeliverySources).toHaveBeenCalledWith({
      query: {
        project_id: "project-1",
        limit: 25,
        offset: 50,
        status: "ready",
      },
      signal: controller.signal,
    });
  });

  it("keeps write-only source credentials in the body and maps wire casing", async () => {
    const request: CreateDeliverySourceRequest = {
      project_id: "project-1",
      name: "private-charts",
      type: "helm_oci",
      url: "oci://registry.example.test/charts",
      auth_mode: "bearer",
      credential: { token: "fixture-token" },
      trust_policy: { allow_unsigned: false, provider: "cosign_keyless" },
    };
    vi.mocked(generated.postDeliverySources).mockResolvedValueOnce({
      data: {
        id: "source-1",
        project_id: "project-1",
        name: "private-charts",
        type: "helm_oci",
        url: "oci://registry.example.test/charts",
        auth_mode: "bearer",
        trust_policy: { allow_unsigned: false },
        credential: { configured: true, key_version: 1, epoch: 1 },
        status: "ready",
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      },
    });

    const result = await createDeliverySource(request, "request-1");

    expect(generated.postDeliverySources).toHaveBeenCalledWith({
      body: request,
      headerParams: { "Idempotency-Key": "request-1" },
      signal: undefined,
    });
    expect(result.credential.keyVersion).toBe(1);
    expect(result).not.toHaveProperty("token");
  });

  it("preserves response ETags for target mutations", async () => {
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
    vi.mocked(
      generated.executeOpenAPIOperationWithResponse,
    ).mockResolvedValueOnce({
      data: { data: { id: "target-1", bundle_version_id: "version-1" } },
      headers: { etag: '"7"' },
      status: 201,
    } as never);

    await expect(createDeliveryTarget(request, "request-2")).resolves.toEqual({
      data: expect.objectContaining({
        id: "target-1",
        bundleVersionId: "version-1",
      }),
      etag: '"7"',
    });
  });

  it("generation-fences a rollout and sends its idempotency key", async () => {
    vi.mocked(generated.postDeliveryTargetsByIdRollouts).mockResolvedValueOnce({
      data: {
        id: "rollout-1",
        target_id: "target-1",
        project_id: "project-1",
        target_generation: 7,
        desired: {
          bundle_version_id: "version-1",
          spec_digest:
            "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
          source: {
            source_id: "source-1",
            type: "git",
            url: "https://example.test/repo.git",
            auth_mode: "none",
            trust_policy: { allow_unsigned: true },
            revision: {
              kind: "git_commit",
              value: "abc",
              artifact_digest:
                "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
            },
          },
        },
        placement_digest:
          "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
        strategy: {
          type: "rolling",
          max_concurrent: 2,
          max_unavailable: { type: "count", value: 1 },
          min_ready: "30s",
          progress_deadline: "30m",
          failure_threshold: { type: "count", value: 1 },
          on_failure: "pause",
          respect_maintenance_windows: true,
        },
        strategy_digest:
          "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
        approval: {
          required: false,
          digest:
            "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
        },
        actor: "user-1",
        idempotency_key: "must-not-enter-view-model",
        request_digest:
          "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
        created_at: "2026-01-01T00:00:00Z",
        deadline: "2026-01-02T00:00:00Z",
        cohorts: [],
        clusters: [],
        plan_digest:
          "sha256:1111111111111111111111111111111111111111111111111111111111111111",
      },
    });

    const result = await startDeliveryRollout(
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

    expect(generated.postDeliveryTargetsByIdRollouts).toHaveBeenCalledWith(
      expect.objectContaining({
        path: { id: "target-1" },
        headerParams: {
          "If-Match": '"7"',
          "Idempotency-Key": "request-3",
        },
      }),
    );
    expect(result.desired.source.trust.allowUnsigned).toBe(true);
    expect(result).not.toHaveProperty("idempotencyKey");
  });

  it("uses generated preview and deployment-control operations", async () => {
    vi.mocked(generated.postDeliveryTargetsByIdPreview).mockResolvedValueOnce({
      data: {
        target_id: "target-1",
        target_generation: 1,
        bundle_version_id: "version-1",
        preview_digest: "sha256:preview",
        selected_count: 0,
        excluded_count: 0,
        requires_all_confirmation: false,
        decisions: [],
        decision_count: 0,
        decision_offset: 0,
        decision_page_size: 100,
        has_more_decisions: false,
        next_cursor: "",
        risks: [],
      },
    });
    vi.mocked(
      generated.executeOpenAPIOperationWithResponse,
    ).mockResolvedValueOnce({
      data: { data: { deployment: {}, event: {} } },
      headers: { etag: '"10"' },
      status: 200,
    } as never);

    const preview = await previewDeliveryTarget("project-1", "target-1", {
      pageSize: 100,
      cursor: "opaque-cursor",
    });
    await actOnClusterDeployment(
      "project-1",
      "deployment-1",
      "reconcile",
      10,
      "manual_reconcile",
      "request-5",
    );

    expect(preview.previewDigest).toBe("sha256:preview");
    expect(generated.executeOpenAPIOperationWithResponse).toHaveBeenCalledWith(
      "postDeliveryDeploymentsByIdReconcile",
      expect.objectContaining({
        headerParams: {
          "If-Match": '"10"',
          "Idempotency-Key": "request-5",
        },
      }),
    );
  });

  it("maps the estate scoreboard from raw wire casing", async () => {
    vi.mocked(generated.getDeliveryEstate).mockResolvedValueOnce({
      data: {
        summary: {
          adopted_clusters: 2,
          flux_ready: 2,
          incompatible: 0,
          disconnected: 0,
          stale: 0,
          assignments: 4,
          drifted: 0,
          failed: 0,
          degraded: 0,
          active_rollouts: 0,
        },
        clusters: [],
        attention: [],
        distributions: {
          compatibility: [],
          privilege: [],
          assignment_phases: [],
        },
      },
    });

    await expect(getDeliveryEstate()).resolves.toEqual(
      expect.objectContaining({
        summary: expect.objectContaining({ adoptedClusters: 2, fluxReady: 2 }),
      }),
    );
  });
});
