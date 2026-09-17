import { getActivity, listAuditLogs } from "@/lib/api/generated/client";
import {
  getActivityFeed,
  getAuditLogExportURL,
  getAuditLogs,
} from "@/lib/api/audit";

vi.mock("@/lib/api/generated/client", () => ({
  getActivity: vi.fn(),
  listAuditLogs: vi.fn(),
}));

const mockedListAuditLogs = vi.mocked(listAuditLogs);
const mockedGetActivity = vi.mocked(getActivity);

describe("audit API generated boundary", () => {
  beforeEach(() => vi.clearAllMocks());

  it("maps generated wire rows and computes stable pagination", async () => {
    const signal = new AbortController().signal;
    mockedListAuditLogs.mockResolvedValueOnce({
      data: [
        {
          id: "audit-1",
          user: "operator@example.com",
          action: "cluster.update",
          action_class: "mutation",
          resource_type: "cluster",
          resource_name: "prod",
          source_ip: "203.0.113.7",
          status: "success",
          timestamp: "2026-08-24T07:00:00Z",
          detail: { field: "labels" },
        },
      ],
      pagination: {
        total: 60,
        limit: 25,
        offset: 25,
        has_more: true,
        next_offset: 50,
      },
    });

    await expect(
      getAuditLogs(
        {
          page: 2,
          pageSize: 25,
          q: "prod",
          audience: "people",
          result: "success",
        },
        signal,
      ),
    ).resolves.toEqual(
      expect.objectContaining({
        pagination: {
          total: 60,
          limit: 25,
          offset: 25,
          has_more: true,
          next_offset: 50,
        },
        data: [
          expect.objectContaining({
            id: "audit-1",
            actionClass: "mutation",
            resourceName: "prod",
            sourceIP: "203.0.113.7",
            detail: { field: "labels" },
          }),
        ],
      }),
    );
    expect(mockedListAuditLogs).toHaveBeenCalledWith({
      query: expect.objectContaining({
        limit: 25,
        offset: 25,
        q: "prod",
        audience: "people",
        result: "success",
      }),
      signal,
    });
  });

  it("maps the raw generated activity collection", async () => {
    mockedGetActivity.mockResolvedValueOnce([
      {
        id: "activity-1",
        type: "cluster",
        action: "created",
        message: "Cluster created",
        resource: "prod",
        timestamp: "2026-08-24T07:00:00Z",
      },
    ]);

    await expect(getActivityFeed({ limit: 10 })).resolves.toEqual([
      expect.objectContaining({
        id: "activity-1",
        type: "cluster",
        resource: "prod",
      }),
    ]);
    expect(mockedGetActivity).toHaveBeenCalledWith({ query: { limit: 10 } });
  });

  it("preserves composable filters in CSV export URLs", () => {
    const url = getAuditLogExportURL({ q: "login", audience: "system" });
    expect(url).toContain("format=csv");
    expect(url).toContain("q=login");
    expect(url).toContain("audience=system");
  });
});
