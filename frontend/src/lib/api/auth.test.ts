import { beforeEach, describe, expect, it, vi } from "vitest";
import * as generated from "@/lib/api/generated/client";
import {
  changeOwnPassword,
  createStreamTicket,
  getCurrentUser,
} from "@/lib/api/auth";

vi.mock("@/lib/api/generated/client", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("@/lib/api/generated/client")>();
  return {
    ...actual,
    getAuthMe: vi.fn(),
    postAuthChangePassword: vi.fn(),
    postStreamsTickets: vi.fn(),
  };
});

describe("auth generated API boundary", () => {
  beforeEach(() => vi.clearAllMocks());

  it("maps auth/me identity and role bindings into the session model", async () => {
    vi.mocked(generated.getAuthMe).mockResolvedValueOnce({
      data: {
        id: "1fa85f64-5717-4562-b3fc-2c963f66afa6",
        username: "ada",
        email: "ada@example.com",
        first_name: "Ada",
        last_name: "Lovelace",
        is_active: true,
        is_superuser: false,
        must_change_password: true,
        date_joined: "2026-08-01T00:00:00Z",
        last_login: null,
        roles: {
          global: [
            {
              id: "2fa85f64-5717-4562-b3fc-2c963f66afa6",
              role_id: "3fa85f64-5717-4562-b3fc-2c963f66afa6",
              role_name: "viewer",
              role_rules: [{ resources: ["clusters"], verbs: ["read"] }],
              group: "",
            },
          ],
          cluster: [],
          project: [],
        },
      },
    });

    await expect(getCurrentUser()).resolves.toEqual(
      expect.objectContaining({
        displayName: "Ada Lovelace",
        enabled: true,
        globalRoles: ["viewer"],
        mustChangePassword: true,
        roles: {
          global: [
            expect.objectContaining({
              roleName: "viewer",
              roleRules: [
                expect.objectContaining({
                  resources: ["clusters"],
                  verbs: ["read"],
                }),
              ],
            }),
          ],
          cluster: [],
          project: [],
        },
      }),
    );
  });

  it("uses exact generated bodies for password changes and stream tickets", async () => {
    vi.mocked(generated.postAuthChangePassword).mockResolvedValueOnce({
      detail: "Password updated",
      must_change_password: false,
    });
    vi.mocked(generated.postStreamsTickets).mockResolvedValueOnce({
      data: {
        ticket: "single-use-ticket",
        expires_at: "2026-08-23T00:01:00Z",
      },
    });

    await changeOwnPassword("old password", "new password");
    await expect(createStreamTicket("exec", "cluster-1")).resolves.toEqual({
      ticket: "single-use-ticket",
      expiresAt: "2026-08-23T00:01:00Z",
    });
    expect(generated.postAuthChangePassword).toHaveBeenCalledWith({
      body: {
        current_password: "old password",
        new_password: "new password",
      },
    });
    expect(generated.postStreamsTickets).toHaveBeenCalledWith({
      body: { stream_type: "exec", cluster_id: "cluster-1" },
    });
  });
});
