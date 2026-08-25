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

  it("normalizes sent email items envelopes into paginated responses", async () => {
    mockedAdminEmailsList.mockResolvedValueOnce({
      data: {
        items: [],
        limit: 25,
        offset: 25,
        total: 60,
      },
    } as never);

    await expect(listSentEmails({ page: 2, page_size: 25 })).resolves.toEqual(
      expect.objectContaining({
        data: [],
        page: 2,
        pageSize: 25,
        total: 60,
        totalPages: 3,
      }),
    );
    expect(mockedAdminEmailsList).toHaveBeenCalledWith({
      query: { limit: 25, offset: 25 },
      signal: undefined,
    });
  });
});
