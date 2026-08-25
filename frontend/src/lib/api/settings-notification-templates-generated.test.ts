import {
  adminNotificationTemplatePreview,
  adminNotificationTemplateReset,
  adminNotificationTemplatesList,
} from "@/lib/api/generated/client";
import {
  listNotificationTemplates,
  previewNotificationTemplate,
  resetNotificationTemplate,
} from "./settings-notification-templates";

vi.mock("@/lib/api/generated/client", () => ({
  adminNotificationTemplateGet: vi.fn(),
  adminNotificationTemplatePreview: vi.fn(),
  adminNotificationTemplateReset: vi.fn(),
  adminNotificationTemplatesList: vi.fn(),
  adminNotificationTemplateUpdate: vi.fn(),
  adminNotificationTemplateVariables: vi.fn(),
}));

describe("generated notification template API", () => {
  beforeEach(() => vi.clearAllMocks());

  it("maps list wire fields and propagates cancellation", async () => {
    const signal = new AbortController().signal;
    vi.mocked(adminNotificationTemplatesList).mockResolvedValueOnce({
      data: {
        items: [
          {
            key: "email.account_locked",
            channel: "email",
            description: "Account locked",
            body_format: "html",
            has_override: true,
            enabled: true,
            updated_at: "2026-08-24T00:00:00Z",
          },
        ],
        total: 1,
      },
    });
    await expect(listNotificationTemplates({ signal })).resolves.toEqual([
      expect.objectContaining({
        bodyFormat: "html",
        hasOverride: true,
        updatedAt: "2026-08-24T00:00:00Z",
      }),
    ]);
    expect(adminNotificationTemplatesList).toHaveBeenCalledWith({ signal });
  });

  it("sends the exact preview body and maps the envelope", async () => {
    const body = {
      subject: "Hello {{.name}}",
      body: "Body",
      body_format: "text" as const,
      variables: { name: "Ada" },
    };
    vi.mocked(adminNotificationTemplatePreview).mockResolvedValueOnce({
      data: { subject: "Hello Ada", body: "Body" },
    });
    await expect(
      previewNotificationTemplate("email.account_locked", body),
    ).resolves.toEqual({
      subject: "Hello Ada",
      body: "Body",
    });
    expect(adminNotificationTemplatePreview).toHaveBeenCalledWith({
      path: { key: "email.account_locked" },
      body,
      signal: undefined,
    });
  });

  it("uses the generated 204 reset operation", async () => {
    vi.mocked(adminNotificationTemplateReset).mockResolvedValueOnce(undefined);
    await resetNotificationTemplate("email.account_locked");
    expect(adminNotificationTemplateReset).toHaveBeenCalledWith({
      path: { key: "email.account_locked" },
      signal: undefined,
    });
  });
});
