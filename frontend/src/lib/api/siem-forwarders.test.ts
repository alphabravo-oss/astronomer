import {
  adminSIEMForwarderDelete,
  adminSIEMForwardersCreate,
  adminSIEMForwardersList,
  adminSIEMForwarderStatus,
  adminSIEMForwarderTest,
} from "@/lib/api/generated/client";
import {
  createSIEMForwarder,
  deleteSIEMForwarder,
  getSIEMForwarderStatus,
  listSIEMForwarders,
  testSIEMForwarder,
} from "./siem-forwarders";

vi.mock("@/lib/api/generated/client", () => ({
  adminSIEMForwarderDelete: vi.fn(),
  adminSIEMForwarderGet: vi.fn(),
  adminSIEMForwardersCreate: vi.fn(),
  adminSIEMForwardersList: vi.fn(),
  adminSIEMForwarderStatus: vi.fn(),
  adminSIEMForwarderTest: vi.fn(),
  adminSIEMForwarderUpdate: vi.fn(),
}));

const wire = {
  id: "00000000-0000-4000-8000-000000000001",
  name: "Splunk",
  transport: "splunk_hec",
  endpoint: "https://siem.example.test",
  auth: "<encrypted>",
  auth_configured: true,
  event_filters: ["audit.*"],
  format: "ndjson",
  tls_skip_verify: false,
  ca_cert_configured: false,
  batch_size: 100,
  flush_interval_ms: 1000,
  timeout_seconds: 10,
  enabled: true,
  created_at: "2026-08-24T00:00:00Z",
  updated_at: "2026-08-24T01:00:00Z",
};

describe("generated SIEM forwarder API", () => {
  beforeEach(() => vi.clearAllMocks());

  it("maps the exact list wire shape and propagates cancellation", async () => {
    const signal = new AbortController().signal;
    vi.mocked(adminSIEMForwardersList).mockResolvedValueOnce({
      data: { items: [wire], total: 1 },
    });

    await expect(listSIEMForwarders({ signal })).resolves.toEqual([
      expect.objectContaining({
        id: wire.id,
        authConfigured: true,
        eventFilters: ["audit.*"],
        flushIntervalMs: 1000,
      }),
    ]);
    expect(adminSIEMForwardersList).toHaveBeenCalledWith({ signal });
  });

  it("sends the generated snake-case request without inventing headers", async () => {
    vi.mocked(adminSIEMForwardersCreate).mockResolvedValueOnce({ data: wire });
    const body = {
      name: "Splunk",
      transport: "splunk_hec" as const,
      endpoint: wire.endpoint,
      tls_skip_verify: false,
    };

    await createSIEMForwarder(body);

    expect(adminSIEMForwardersCreate).toHaveBeenCalledWith({
      body,
      signal: undefined,
    });
  });

  it("returns the truthful 202 queue receipt", async () => {
    vi.mocked(adminSIEMForwarderTest).mockResolvedValueOnce({
      data: {
        operation_id: "00000000-0000-4000-8000-000000000004",
        forwarder_id: wire.id,
        status: "pending",
        status_url: "/api/v1/admin/siem-forwarders/00000000-0000-4000-8000-000000000001/test-operations/00000000-0000-4000-8000-000000000004/",
        created_at: "2026-08-24T02:00:00Z",
      },
    });

    await expect(testSIEMForwarder(wire.id)).resolves.toEqual({
      queueId: "00000000-0000-4000-8000-000000000004",
      forwarderId: wire.id,
      queuedAt: "2026-08-24T02:00:00Z",
      message: "pending",
    });
  });

  it("maps unwrapped status and calls generated delete", async () => {
    vi.mocked(adminSIEMForwarderStatus).mockResolvedValueOnce({
      forwarder_id: wire.id,
      last_sent_at: null,
      last_error: "",
      queue_depth: 2,
      dropped_total: 3,
      dispatched_total: 10,
      updated_at: "2026-08-24T02:00:00Z",
    });
    vi.mocked(adminSIEMForwarderDelete).mockResolvedValueOnce(undefined);

    await expect(getSIEMForwarderStatus(wire.id)).resolves.toEqual(
      expect.objectContaining({ queueDepth: 2, droppedTotal: 3 }),
    );
    await deleteSIEMForwarder(wire.id);
    expect(adminSIEMForwarderDelete).toHaveBeenCalledWith({
      path: { id: wire.id },
      signal: undefined,
    });
  });
});
