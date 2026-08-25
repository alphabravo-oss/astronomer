import transport from "@/lib/api/transport";
import {
  SMTP_REDACTED_SENTINEL,
  getSmtpConfig,
  testSmtpConfig,
  updateSmtpConfig,
} from "./settings-email";

vi.mock("@/lib/api/transport", () => ({
  default: {
    request: vi.fn(),
    get: vi.fn(),
  },
}));

const request = vi.mocked(transport.request);

beforeEach(() => {
  request.mockReset();
});

it("maps the generated SMTP wire response to the stable view model", async () => {
  request.mockResolvedValue({
    data: {
      data: {
        host: "smtp.example.test",
        port: 587,
        username: "mailer",
        password: SMTP_REDACTED_SENTINEL,
        from_address: "ops@example.test",
        from_name: "Operations",
        auth_mechanism: "plain",
        encryption: "starttls",
        require_tls: true,
        timeout_seconds: 30,
        updated_at: "2026-08-24T00:00:00Z",
      },
    },
  } as never);

  await expect(getSmtpConfig()).resolves.toEqual({
    host: "smtp.example.test",
    port: 587,
    username: "mailer",
    password: SMTP_REDACTED_SENTINEL,
    fromAddress: "ops@example.test",
    fromName: "Operations",
    authMechanism: "plain",
    encryption: "starttls",
    requireTls: true,
    timeoutSeconds: 30,
    updatedAt: "2026-08-24T00:00:00Z",
  });
  expect(request).toHaveBeenCalledWith(
    expect.objectContaining({
      method: "GET",
      url: "/api/v1/admin/smtp",
    }),
  );
});

it("uses generated request casing and never sends the redaction sentinel", async () => {
  request.mockResolvedValue({ data: { data: {} } } as never);
  await updateSmtpConfig({
    password: SMTP_REDACTED_SENTINEL,
    fromAddress: "ops@example.test",
    requireTls: true,
  });
  expect(request).toHaveBeenCalledWith(
    expect.objectContaining({
      method: "PUT",
      data: expect.objectContaining({
        password: undefined,
        from_address: "ops@example.test",
        require_tls: true,
      }),
    }),
  );

  request.mockResolvedValue({
    data: { success: true, recipient: "admin@example.test" },
  } as never);
  await expect(testSmtpConfig({ to: "admin@example.test" })).resolves.toEqual({
    success: true,
    message: "Test email sent to admin@example.test",
    durationMs: 0,
  });
  expect(request).toHaveBeenLastCalledWith(
    expect.objectContaining({
      method: "POST",
      url: "/api/v1/admin/smtp/test",
      data: { recipient: "admin@example.test" },
    }),
  );
});
