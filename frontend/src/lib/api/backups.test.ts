import * as generated from "@/lib/api/generated/client";
import type { OpenAPIComponents } from "@/types/openapi.generated";
import {
  b2CreateRestore,
  b2GetRestore,
  b2ListStorageLocations,
  b2UpdateSchedule,
} from "./backups";

vi.mock("@/lib/api/generated/client", () => ({
  deleteBackupsSchedulesById: vi.fn(),
  deleteBackupsStorageById: vi.fn(),
  getBackups: vi.fn(),
  getBackupsById: vi.fn(),
  getBackupsRestores: vi.fn(),
  getBackupsRestoresById: vi.fn(),
  getBackupsSchedules: vi.fn(),
  getBackupsSchedulesById: vi.fn(),
  getBackupsStorage: vi.fn(),
  getBackupsStorageById: vi.fn(),
  postBackupsByIdRestore: vi.fn(),
  postBackupsSchedules: vi.fn(),
  postBackupsSchedulesByIdTriggerNow: vi.fn(),
  postBackupsStorage: vi.fn(),
  postBackupsStorageByIdTest: vi.fn(),
  putBackupsSchedulesById: vi.fn(),
  putBackupsStorageById: vi.fn(),
}));

type Schemas = OpenAPIComponents["schemas"];

const restoreWire: Schemas["RestoreOperationResponse"] = {
  id: "restore-1",
  backup_id: "backup-1",
  status: "running",
  cluster_id: "cluster-1",
  included_namespaces: ["payments"],
  namespace_mapping: { payments: "payments-restored" },
  created_at: "2026-08-23T00:00:00Z",
  updated_at: "2026-08-23T00:01:00Z",
};

describe("backup generated API boundary", () => {
  beforeEach(() => vi.clearAllMocks());

  it("converts UI page pagination to the generated limit/offset contract and maps raw wire casing", async () => {
    vi.mocked(generated.getBackupsStorage).mockResolvedValueOnce({
      data: [
        {
          id: "storage-1",
          name: "Primary",
          storage_type: "s3",
          bucket: "prod-backups",
          endpoint_url: "https://s3.example.com",
          is_default: true,
          has_credentials: true,
          cluster_id: "cluster-1",
          created_at: "2026-08-23T00:00:00Z",
          updated_at: "2026-08-23T00:01:00Z",
        },
      ],
      count: 41,
      next: "/api/v1/backups/storage?limit=20&offset=40",
      previous: "/api/v1/backups/storage?limit=20&offset=0",
    });

    await expect(
      b2ListStorageLocations({ page: 2, page_size: 20 }),
    ).resolves.toEqual(
      expect.objectContaining({
        total: 41,
        page: 2,
        pageSize: 20,
        totalPages: 3,
        data: [
          expect.objectContaining({
            id: "storage-1",
            storageType: "s3",
            endpointUrl: "https://s3.example.com",
            isDefault: true,
            hasCredentials: true,
            clusterId: "cluster-1",
          }),
        ],
      }),
    );
    expect(generated.getBackupsStorage).toHaveBeenCalledWith({
      query: { limit: 20, offset: 20 },
    });
  });

  it("polls the object endpoint advertised by the restore Location header", async () => {
    vi.mocked(generated.getBackupsRestoresById).mockResolvedValueOnce({
      data: restoreWire,
    });

    await expect(b2GetRestore("restore-1")).resolves.toEqual(
      expect.objectContaining({
        id: "restore-1",
        backupId: "backup-1",
        clusterId: "cluster-1",
        includedNamespaces: ["payments"],
        namespaceMapping: { payments: "payments-restored" },
      }),
    );
    expect(generated.getBackupsRestoresById).toHaveBeenCalledWith({
      path: { id: "restore-1" },
    });
  });

  it("uses the generated restore path/body contract and omits unsupported UI-only fields", async () => {
    vi.mocked(generated.postBackupsByIdRestore).mockResolvedValueOnce({
      data: restoreWire,
    });

    await b2CreateRestore({
      backup_id: "backup-1",
      included_namespaces: ["payments"],
      namespace_mapping: { payments: "payments-restored" },
      restore_pvs: true,
    });

    expect(generated.postBackupsByIdRestore).toHaveBeenCalledWith({
      path: { id: "backup-1" },
      body: {
        included_namespaces: ["payments"],
        namespace_mapping: { payments: "payments-restored" },
      },
    });
  });

  it("rejects unsafe partial schedule replacement before issuing PUT", async () => {
    await expect(
      b2UpdateSchedule("schedule-1", { enabled: false }),
    ).rejects.toThrow("partial PUT is unsafe");
    expect(generated.putBackupsSchedulesById).not.toHaveBeenCalled();
  });
});
