import * as generated from "@/lib/api/generated/client";
import {
  createSnapshot,
  createSnapshotSchedule,
  deleteSnapshot,
  deleteSnapshotSchedule,
  getVeleroStatus,
  listSnapshotSchedules,
  listSnapshots,
  restoreSnapshot,
  updateSnapshotSchedule,
} from "./cluster-detail";

vi.mock("@/lib/api/generated/client", () => ({
  getClustersByClusterIdVeleroStatus: vi.fn(),
  getClustersByClusterIdSnapshots: vi.fn(),
  postClustersByClusterIdSnapshots: vi.fn(),
  deleteClustersByClusterIdSnapshotsById: vi.fn(),
  postClustersByClusterIdSnapshotsByIdRestore: vi.fn(),
  getClustersByClusterIdSnapshotSchedules: vi.fn(),
  postClustersByClusterIdSnapshotSchedules: vi.fn(),
  putClustersByClusterIdSnapshotSchedulesById: vi.fn(),
  deleteClustersByClusterIdSnapshotSchedulesById: vi.fn(),
  postClustersByClusterIdVulnerabilitiesRescan: vi.fn(),
  getWorkloadsOperationsById: vi.fn(),
}));

const snapshotWire = {
  id: "snapshot-1",
  cluster_id: "cluster-1",
  velero_name: "backup-one",
  velero_namespace: "velero",
  source: "manual",
  spec: { includedNamespaces: ["apps"], snapshotVolumes: true },
  phase: "Completed",
  start_time: "2026-08-24T10:00:00Z",
  completion_time: "2026-08-24T10:01:00Z",
  expires_at: "2026-09-24T10:00:00Z",
  warnings_count: 2,
  errors_count: 0,
  created_at: "2026-08-24T10:00:00Z",
  updated_at: "2026-08-24T10:01:00Z",
};

const scheduleWire = {
  id: "schedule-1",
  cluster_id: "cluster-1",
  name: "nightly",
  cron_schedule: "0 3 * * *",
  spec: { includedNamespaces: ["apps"] },
  enabled: true,
  last_run_at: "2026-08-24T03:00:00Z",
  created_at: "2026-08-01T00:00:00Z",
  updated_at: "2026-08-24T03:00:00Z",
};

describe("cluster-detail snapshot generated boundary", () => {
  beforeEach(() => vi.clearAllMocks());

  it("maps snapshot wire fields and forwards cancellation", async () => {
    vi.mocked(generated.getClustersByClusterIdSnapshots).mockResolvedValueOnce({
      data: { items: [snapshotWire] },
    } as never);
    const controller = new AbortController();

    await expect(
      listSnapshots("cluster-1", controller.signal),
    ).resolves.toEqual([
      expect.objectContaining({
        id: "snapshot-1",
        name: "backup-one",
        source: "adhoc",
        startTimestamp: "2026-08-24T10:00:00Z",
        warnings: 2,
      }),
    ]);
    expect(generated.getClustersByClusterIdSnapshots).toHaveBeenCalledWith({
      path: { cluster_id: "cluster-1" },
      signal: controller.signal,
    });
  });

  it("flattens the public snapshot spec into the exact request body", async () => {
    vi.mocked(generated.postClustersByClusterIdSnapshots).mockResolvedValueOnce(
      {
        data: snapshotWire,
      } as never,
    );

    await createSnapshot("cluster-1", {
      spec: { includedNamespaces: ["apps"], ttl: "168h" },
    });

    expect(generated.postClustersByClusterIdSnapshots).toHaveBeenCalledWith({
      path: { cluster_id: "cluster-1" },
      headerParams: { "Idempotency-Key": expect.any(String) },
      body: { includedNamespaces: ["apps"], ttl: "168h" },
      signal: undefined,
    });
  });

  it("maps schedule cron fields and preserves partial updates", async () => {
    vi.mocked(
      generated.getClustersByClusterIdSnapshotSchedules,
    ).mockResolvedValueOnce({ data: { items: [scheduleWire] } } as never);
    vi.mocked(
      generated.postClustersByClusterIdSnapshotSchedules,
    ).mockResolvedValueOnce({ data: scheduleWire } as never);
    vi.mocked(
      generated.putClustersByClusterIdSnapshotSchedulesById,
    ).mockResolvedValueOnce({ data: scheduleWire } as never);

    await expect(listSnapshotSchedules("cluster-1")).resolves.toEqual([
      expect.objectContaining({ cron: "0 3 * * *" }),
    ]);
    await createSnapshotSchedule("cluster-1", {
      name: "nightly",
      cron: "0 3 * * *",
      spec: {},
    });
    await updateSnapshotSchedule("cluster-1", "schedule-1", {
      enabled: false,
    });

    expect(
      generated.postClustersByClusterIdSnapshotSchedules,
    ).toHaveBeenCalledWith(
      expect.objectContaining({
        body: expect.objectContaining({ cron_schedule: "0 3 * * *" }),
      }),
    );
    expect(
      generated.putClustersByClusterIdSnapshotSchedulesById,
    ).toHaveBeenCalledWith({
      path: { cluster_id: "cluster-1", id: "schedule-1" },
      body: {
        name: undefined,
        cron_schedule: undefined,
        enabled: false,
        spec: undefined,
      },
      signal: undefined,
    });
  });

  it("maps status and restore responses and uses generated deletes", async () => {
    vi.mocked(
      generated.getClustersByClusterIdVeleroStatus,
    ).mockResolvedValueOnce({
      data: {
        installed: true,
        namespace: "velero",
        storage_ready: true,
        storage_locations: [
          {
            name: "primary",
            provider: "aws",
            default: true,
            phase: "Available",
            bucket: "backups",
          },
        ],
      },
    } as never);
    vi.mocked(
      generated.postClustersByClusterIdSnapshotsByIdRestore,
    ).mockResolvedValueOnce({
      data: {
        id: "restore-1",
        snapshot_id: "snapshot-1",
        target_cluster_id: "cluster-2",
        velero_name: "restore-one",
        phase: "New",
        warnings_count: 0,
        errors_count: 0,
      },
    } as never);

    await expect(getVeleroStatus("cluster-1")).resolves.toEqual(
      expect.objectContaining({ storageReady: true }),
    );
    await expect(
      restoreSnapshot("cluster-1", "snapshot-1", {
        target_cluster_id: "cluster-2",
      }),
    ).resolves.toEqual(
      expect.objectContaining({
        name: "restore-one",
        targetClusterId: "cluster-2",
      }),
    );
    await deleteSnapshot("cluster-1", "snapshot-1");
    await deleteSnapshotSchedule("cluster-1", "schedule-1");

    expect(
      generated.deleteClustersByClusterIdSnapshotsById,
    ).toHaveBeenCalledWith({
      path: { cluster_id: "cluster-1", id: "snapshot-1" },
      signal: undefined,
    });
    expect(
      generated.deleteClustersByClusterIdSnapshotSchedulesById,
    ).toHaveBeenCalledWith({
      path: { cluster_id: "cluster-1", id: "schedule-1" },
      signal: undefined,
    });
  });
});
