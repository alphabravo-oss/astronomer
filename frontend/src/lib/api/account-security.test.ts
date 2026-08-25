import { beforeEach, describe, expect, it, vi } from "vitest";

const operations = vi.hoisted(() => ({
  login: vi.fn(),
  enrollStart: vi.fn(),
  enrollConfirm: vi.fn(),
  resync: vi.fn(),
  status: vi.fn(),
}));

vi.mock("@/lib/api/generated/client", () => ({
  postAuthLogin: operations.login,
  postAuthTotpEnrollStart: operations.enrollStart,
  postAuthTotpEnrollConfirm: operations.enrollConfirm,
  postAdminUsersByIdResyncGroups: operations.resync,
  getAuthTotpStatus: operations.status,
}));

import {
  adminResyncUserGroups,
  confirmTotpEnrollment,
  getTotpStatus,
  loginWithCredentialsChallengeAware,
  startTotpEnrollment,
} from "./account-security";

describe("account security generated API boundary", () => {
  beforeEach(() => vi.clearAllMocks());

  it("returns a typed TOTP challenge from the generated transport 423", async () => {
    operations.login.mockRejectedValue({
      status: 423,
      response: {
        status: 423,
        data: { error: "totp_required", challenge_token: "challenge-token" },
      },
    });
    await expect(
      loginWithCredentialsChallengeAware("operator@example.com", "password"),
    ).resolves.toEqual({
      kind: "challenge",
      challenge: {
        error: "totp_required",
        challengeToken: "challenge-token",
      },
    });
  });

  it("maps a successful login's raw user wire shape", async () => {
    operations.login.mockResolvedValue({
      data: {
        token: "access",
        refresh: "refresh",
        user: {
          id: "user-1",
          username: "operator",
          email: "operator@example.com",
          first_name: "Ada",
          last_name: "Lovelace",
          is_active: true,
          is_superuser: true,
          roles: { global: [], cluster: [], project: [] },
        },
      },
    });
    await expect(
      loginWithCredentialsChallengeAware("operator@example.com", "password"),
    ).resolves.toEqual(
      expect.objectContaining({
        kind: "ok",
        token: "access",
        user: expect.objectContaining({
          id: "user-1",
          displayName: "Ada Lovelace",
          isSuperuser: true,
        }),
      }),
    );
  });

  it("retains both enrollment proofs through confirm", async () => {
    operations.enrollStart.mockResolvedValue({
      otpauth_url: "otpauth://totp/example",
      qr_data_url: "data:image/png;base64,abc",
      challenge_token: "signed-token",
      challenge: "encrypted-secret",
    });
    operations.enrollConfirm.mockResolvedValue({ recovery_codes: ["code-1"] });
    const enrollment = await startTotpEnrollment();
    await expect(
      confirmTotpEnrollment(
        enrollment.sessionToken,
        enrollment.challenge,
        "123456",
      ),
    ).resolves.toEqual({ recoveryCodes: ["code-1"] });
    expect(operations.enrollConfirm).toHaveBeenCalledWith({
      body: {
        challenge_token: "signed-token",
        challenge: "encrypted-secret",
        code: "123456",
      },
      signal: undefined,
    });
  });

  it("uses the generated admin resync operation with the exact path", async () => {
    operations.resync.mockResolvedValue({ success: true });
    await adminResyncUserGroups("user/1");
    expect(operations.resync).toHaveBeenCalledWith({
      path: { id: "user/1" },
      signal: undefined,
    });
  });

  it("propagates AbortSignal on generated reads", async () => {
    const controller = new AbortController();
    operations.status.mockResolvedValue({
      enrolled: true,
      recovery_codes_remaining: 4,
    });
    await getTotpStatus({ signal: controller.signal });
    expect(operations.status).toHaveBeenCalledWith({
      signal: controller.signal,
    });
  });
});
