import { adminEmailsList, adminWebhooksList } from "@/lib/api/generated/client";
import { listSentEmails, listWebhooks } from "./settings";

vi.mock("@/lib/api/generated/client", () => ({
  adminEmailsList: vi.fn(),
  adminWebhooksList: vi.fn(),
}));

const mockedAdminWebhooksList = vi.mocked(adminWebhooksList);
const mockedAdminEmailsList = vi.mocked(adminEmailsList);

describe("settings API client", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("normalizes webhooks items envelopes into arrays", async () => {
    mockedAdminWebhooksList.mockResolvedValueOnce({
      data: {
        items: [
          {
            id: "webhook-1",
            name: "Ops",
            url: "https://hooks.example.com",
            secret: "<encrypted>",
            secret_configured: true,
            event_filters: [],
            payload_template: "",
            extra_headers: {},
            enabled: true,
            max_retries: 5,
            timeout_seconds: 10,
            created_at: "2026-06-15T00:00:00Z",
            updated_at: "2026-06-15T00:00:00Z",
          },
        ],
        total: 1,
      },
    });

    await expect(listWebhooks()).resolves.toEqual([
      expect.objectContaining({ id: "webhook-1", name: "Ops" }),
    ]);
  });

  it("preserves canonical sent-email pagination metadata", async () => {
    mockedAdminEmailsList.mockResolvedValueOnce({
      data: [],
      pagination: {
        limit: 25,
        offset: 25,
        total: 60,
        has_more: true,
        next_offset: 50,
      },
    });

    await expect(listSentEmails({ page: 2, page_size: 25 })).resolves.toEqual(
      expect.objectContaining({
        data: [],
        pagination: {
          limit: 25,
          offset: 25,
          total: 60,
          has_more: true,
          next_offset: 50,
        },
      }),
    );
    expect(mockedAdminEmailsList).toHaveBeenCalledWith({
      query: { limit: 25, offset: 25 },
      signal: undefined,
    });
  });
});
