import {
  deleteAdminManagementBackupDestinationsById,
  getAdminBackupDrill,
  getAdminBackupDrillHistory,
  getAdminManagementBackup,
  postAdminManagementBackupDestinationsByIdRun,
} from "@/lib/api/generated/client";
import {
  deleteManagementBackupDestination,
  getLatestBackupDrill,
  getManagementBackupStatus,
  listBackupDrillHistory,
  runManagementBackupDestination,
} from "./settings-backup-drill";

vi.mock("@/lib/api/generated/client", () => ({
  deleteAdminManagementBackupDestinationsById: vi.fn(),
  getAdminBackupDrill: vi.fn(),
  getAdminBackupDrillHistory: vi.fn(),
  getAdminManagementBackup: vi.fn(),
  getAdminManagementBackupOperationById: vi.fn(),
  postAdminManagementBackupDestinations: vi.fn(),
  postAdminManagementBackupDestinationsByIdRun: vi.fn(),
  postAdminManagementBackupDestinationsByIdTest: vi.fn(),
  putAdminManagementBackupDestinationsById: vi.fn(),
}));

const destinationId = "00000000-0000-4000-8000-000000000001";
const drill = {
  id: destinationId,
  started_at: "2026-09-22T01:00:00Z",
  finished_at: "2026-09-22T01:00:01Z",
  status: "success" as const,
  backup_key: "snapshot",
  schema_version: 1,
  error_message: "",
  created_at: "2026-09-22T01:00:00Z",
};

describe("generated management backup API", () => {
  beforeEach(() => vi.clearAllMocks());

  it.each([
    { id: "" },
    { started_at: "1970-01-01T00:00:00Z" },
    { started_at: "invalid" },
    { finished_at: null },
    { finished_at: "2026-09-21T00:00:00Z" },
  ])("rejects success without valid drill evidence: %j", async (invalid) => {
    vi.mocked(getAdminBackupDrill).mockResolvedValueOnce({
      data: {
        latest: { ...drill, ...invalid },
        latest_success: null,
        latest_success_age_seconds: null,
      },
    });
    await expect(getLatestBackupDrill()).rejects.toThrow(
      /success cannot be verified/,
    );
  });

  it("distinguishes absent, running and completed drill results", async () => {
    for (const latest of [
      null,
      { ...drill, status: "running" as const, finished_at: null },
      drill,
    ]) {
      vi.mocked(getAdminBackupDrill).mockResolvedValueOnce({
        data: {
          latest,
          latest_success: null,
          latest_success_age_seconds: null,
        },
      });
      const result = await getLatestBackupDrill();
      expect(result.latest?.status ?? null).toBe(latest?.status ?? null);
    }
  });

  it("maps nullable destination status and propagates cancellation", async () => {
    const signal = new AbortController().signal;
    vi.mocked(getAdminManagementBackup).mockResolvedValueOnce({
      data: {
        enabled: false,
        destinations: null,
        encryption_key_backup: { wrapping_configured: false },
      },
    });

    await expect(getManagementBackupStatus({ signal })).resolves.toEqual(
      expect.objectContaining({
        enabled: false,
        destinations: [],
        encryptionKeyBackup: { wrappingConfigured: false },
      }),
    );
    expect(getAdminManagementBackup).toHaveBeenCalledWith({ signal });
  });

  it("translates page semantics into the server limit/offset contract", async () => {
    vi.mocked(getAdminBackupDrillHistory).mockResolvedValueOnce({
      data: [],
      pagination: {
        total: 45,
        limit: 20,
        offset: 40,
        has_more: false,
        next_offset: null,
      },
    });
    await expect(
      listBackupDrillHistory({ page: 3, page_size: 20 }),
    ).resolves.toEqual(
      expect.objectContaining({
        pagination: {
          total: 45,
          limit: 20,
          offset: 40,
          has_more: false,
          next_offset: null,
        },
      }),
    );
    expect(getAdminBackupDrillHistory).toHaveBeenCalledWith({
      query: { limit: 20, offset: 40 },
      signal: undefined,
    });
  });

  it("returns the truthful 202 delete receipt and preserves run idempotency", async () => {
    vi.mocked(
      deleteAdminManagementBackupDestinationsById,
    ).mockResolvedValueOnce({
      data: {
        destination_id: destinationId,
        generation: 4,
        desired_state: "deleted",
        status: "pending",
      },
    });
    vi.mocked(
      postAdminManagementBackupDestinationsByIdRun,
    ).mockResolvedValueOnce({
      data: {
        id: "00000000-0000-4000-8000-000000000002",
        destination_id: destinationId,
        operation_type: "management_backup_run",
        status: "pending",
      },
    });

    await expect(
      deleteManagementBackupDestination(destinationId),
    ).resolves.toEqual({
      destinationId,
      generation: 4,
      desiredState: "deleted",
      status: "pending",
    });
    await runManagementBackupDestination(destinationId, {
      idempotencyKey: "request-1",
    });
    expect(postAdminManagementBackupDestinationsByIdRun).toHaveBeenCalledWith({
      path: { id: destinationId },
      headerParams: { "Idempotency-Key": "request-1" },
      signal: undefined,
    });
  });
});
