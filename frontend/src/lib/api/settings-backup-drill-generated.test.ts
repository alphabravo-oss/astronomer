import {
  deleteAdminManagementBackupDestinationsById,
  getAdminBackupDrillHistory,
  getAdminManagementBackup,
  postAdminManagementBackupDestinationsByIdRun,
} from "@/lib/api/generated/client";
import {
  deleteManagementBackupDestination,
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

describe("generated management backup API", () => {
  beforeEach(() => vi.clearAllMocks());

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
      count: 45,
      next: null,
      previous: "/previous",
    });
    await expect(
      listBackupDrillHistory({ page: 3, page_size: 20 }),
    ).resolves.toEqual(expect.objectContaining({ page: 3, totalPages: 3 }));
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
