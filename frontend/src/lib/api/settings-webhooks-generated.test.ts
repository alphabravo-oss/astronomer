import {
  adminWebhookDeliveries,
  adminWebhooksCreate,
  adminWebhooksList,
  adminWebhookTest,
} from "@/lib/api/generated/client";
import {
  createWebhook,
  listWebhookDeliveries,
  listWebhooks,
  testWebhook,
} from "./settings-webhooks";

vi.mock("@/lib/api/generated/client", () => ({
  adminWebhookDelete: vi.fn(),
  adminWebhookDeliveries: vi.fn(),
  adminWebhookGet: vi.fn(),
  adminWebhookRetryDelivery: vi.fn(),
  adminWebhooksCreate: vi.fn(),
  adminWebhooksList: vi.fn(),
  adminWebhookTest: vi.fn(),
  adminWebhookUpdate: vi.fn(),
}));

const subscriptionWire = {
  id: "00000000-0000-4000-8000-000000000001",
  name: "Operations",
  url: "https://hooks.example.test/astronomer",
  secret: "<encrypted>",
  secret_configured: true,
  event_filters: ["cluster.*"],
  payload_template: "",
  extra_headers: { "X-Team": "platform" },
  enabled: true,
  max_retries: 5,
  timeout_seconds: 10,
  created_at: "2026-08-24T00:00:00Z",
  updated_at: "2026-08-24T01:00:00Z",
};

describe("generated webhooks API", () => {
  beforeEach(() => vi.clearAllMocks());

  it("maps subscription wire fields and propagates cancellation", async () => {
    const signal = new AbortController().signal;
    vi.mocked(adminWebhooksList).mockResolvedValueOnce({
      data: { items: [subscriptionWire], total: 1 },
    });

    await expect(listWebhooks({ signal })).resolves.toEqual([
      expect.objectContaining({
        id: subscriptionWire.id,
        template: "generic",
        filters: { events: ["cluster.*"] },
        secretConfigured: true,
      }),
    ]);
    expect(adminWebhooksList).toHaveBeenCalledWith({ signal });
  });

  it("translates the UI preset and filters into the exact generated body", async () => {
    vi.mocked(adminWebhooksCreate).mockResolvedValueOnce({
      data: subscriptionWire,
    });

    await createWebhook({
      name: "Operations",
      url: subscriptionWire.url,
      template: "slack",
      secret: "new-secret",
      enabled: true,
      filters: { events: ["cluster.*"] },
    });

    expect(adminWebhooksCreate).toHaveBeenCalledWith({
      body: expect.objectContaining({
        name: "Operations",
        url: subscriptionWire.url,
        secret: "new-secret",
        enabled: true,
        event_filters: ["cluster.*"],
        payload_template: expect.stringContaining("{{ .event_name }}"),
      }),
      signal: undefined,
    });
  });

  it("translates page pagination into bounded limit and offset", async () => {
    const delivery = {
      id: "00000000-0000-4000-8000-000000000003",
      event_name: "cluster.unhealthy",
      event_id: "event-1",
      status: "delivered" as const,
      attempts: 1,
      payload_size: 128,
      response_status: 204,
      response_body: "",
      last_error: "",
      delivered_at: "2026-08-24T02:00:00Z",
      next_attempt_at: null,
      created_at: "2026-08-24T01:59:00Z",
    };
    vi.mocked(adminWebhookDeliveries).mockResolvedValueOnce({
      data: { items: [delivery], total: 51, limit: 25, offset: 25 },
    });

    await expect(
      listWebhookDeliveries(subscriptionWire.id, { page: 2, page_size: 25 }),
    ).resolves.toEqual(
      expect.objectContaining({
        page: 2,
        pageSize: 25,
        total: 51,
        totalPages: 3,
        data: [
          expect.objectContaining({ status: "delivered", responseCode: 204 }),
        ],
      }),
    );
    expect(adminWebhookDeliveries).toHaveBeenCalledWith({
      path: { id: subscriptionWire.id },
      query: { limit: 25, offset: 25 },
      signal: undefined,
    });
  });

  it("returns the truthful asynchronous test receipt", async () => {
    vi.mocked(adminWebhookTest).mockResolvedValueOnce({
      data: {
        id: "00000000-0000-4000-8000-000000000003",
        event_name: "webhook.test",
        event_id: "event-1",
        status: "pending",
        attempts: 0,
        payload_size: 0,
        response_status: 0,
        response_body: "",
        last_error: "",
        delivered_at: null,
        next_attempt_at: null,
        created_at: "2026-08-24T02:00:00Z",
      },
    });

    await expect(testWebhook(subscriptionWire.id)).resolves.toEqual({
      deliveryId: "00000000-0000-4000-8000-000000000003",
      subscriptionId: subscriptionWire.id,
      queuedAt: "2026-08-24T02:00:00Z",
      message: "pending",
    });
  });
});
