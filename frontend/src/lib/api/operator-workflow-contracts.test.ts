import * as generated from "./generated/client";
import { getCatalogOperation, retryCatalogOperation } from "./catalog";
import {
  getClusterAppValues,
  getClusterAppHistory,
  listCatalogCharts,
  upgradeClusterApp,
} from "./cluster-apps";
import { getSnapshotRestore, getSnapshotRestores } from "./cluster-velero";
vi.mock("./generated/client", () => ({
  getCatalogOperationsById: vi.fn(),
  postCatalogOperationsByIdRetry: vi.fn(),
  getCatalogInstalledByIdValues: vi.fn(),
  getCatalogInstalledByIdRevisions: vi.fn(),
  getCatalogCharts: vi.fn(),
  putCatalogInstalledByIdUpgrade: vi.fn(),
  getClustersByClusterIdSnapshotRestoresById: vi.fn(),
  getClustersByClusterIdSnapshotRestores: vi.fn(),
}));
beforeEach(() => vi.clearAllMocks());
it("unwraps operation read and retry envelopes without dropping persisted stage events", async () => {
  const receipt = {
    id: "op",
    status: "failed",
    journalStatus: "completed",
    deliveryPhase: "failed",
    events: [{ stage: "rollout", message: "Failed" }],
  };
  vi.mocked(generated.getCatalogOperationsById).mockResolvedValue({
    data: receipt,
  } as never);
  vi.mocked(generated.postCatalogOperationsByIdRetry).mockResolvedValue({
    data: receipt,
  } as never);
  expect(await getCatalogOperation("op")).toEqual(receipt);
  expect(await retryCatalogOperation("op")).toEqual(receipt);
});
it("reads saved values and revisions from actual data envelopes, preserving the submitted values", async () => {
  vi.mocked(generated.getCatalogInstalledByIdValues).mockResolvedValue({
    data: {
      values_override: "replicas: 7",
      namespace: "team",
      release_name: "web",
    },
  });
  vi.mocked(generated.getCatalogInstalledByIdRevisions).mockResolvedValue({
    data: {
      revisions: [{ revision: 2 }],
      namespace: "team",
      release_name: "web",
    },
  } as never);
  vi.mocked(generated.putCatalogInstalledByIdUpgrade).mockResolvedValue({
    data: { installation: { id: "release" }, operation: { id: "op" } },
  } as never);
  const values = await getClusterAppValues("release");
  expect(values).toBe("replicas: 7");
  expect((await getClusterAppHistory("release")).revisions).toHaveLength(1);
  await upgradeClusterApp(
    "release",
    { chart_version_id: "v2", values_override: values },
    "stable-key",
  );
  expect(generated.putCatalogInstalledByIdUpgrade).toHaveBeenCalledWith(
    expect.objectContaining({
      headerParams: { "Idempotency-Key": "stable-key" },
      body: { chart_version_id: "v2", values_override: "replicas: 7" },
    }),
  );
  vi.mocked(generated.getCatalogInstalledByIdValues).mockRejectedValue(
    new Error("Forbidden"),
  );
  await expect(getClusterAppValues("release")).rejects.toThrow("Forbidden");
});
it("sends catalog search before pagination and trusts the filtered server count", async () => {
  vi.mocked(generated.getCatalogCharts).mockResolvedValue({
    data: [
      { id: "late", name: "operator", description: "matches on the server" },
    ],
    pagination: {
      limit: 50,
      offset: 50,
      total: 51,
      has_more: false,
      next_offset: null,
    },
  } as never);
  const result = await listCatalogCharts({
    projectId: "project",
    search: "visible after page 1",
    offset: 50,
    limit: 50,
  });
  expect(generated.getCatalogCharts).toHaveBeenCalledWith(
    expect.objectContaining({
      query: {
        project_id: "project",
        search: "visible after page 1",
        offset: 50,
        limit: 50,
      },
    }),
  );
  expect(result.data.map((chart) => chart.id)).toEqual(["late"]);
  expect(result.pagination?.total).toBe(51);
});
it("reads restore IDs in the target-cluster restore domain", async () => {
  vi.mocked(
    generated.getClustersByClusterIdSnapshotRestoresById,
  ).mockResolvedValue({
    data: {
      id: "restore",
      snapshot_id: "backup",
      source_cluster_id: "source",
      target_cluster_id: "target",
      phase: "Pending",
    },
  } as never);
  vi.mocked(generated.getClustersByClusterIdSnapshotRestores).mockResolvedValue(
    {
      data: [],
      pagination: {
        limit: 50,
        offset: 50,
        total: 51,
        has_more: false,
        next_offset: null,
      },
    } as never,
  );
  expect((await getSnapshotRestore("target", "restore")).snapshot_id).toBe(
    "backup",
  );
  expect(
    generated.getClustersByClusterIdSnapshotRestoresById,
  ).toHaveBeenCalledWith(
    expect.objectContaining({ path: { cluster_id: "target", id: "restore" } }),
  );
  await getSnapshotRestores("target", 50);
  expect(generated.getClustersByClusterIdSnapshotRestores).toHaveBeenCalledWith(
    expect.objectContaining({ query: { limit: 50, offset: 50 } }),
  );
});
