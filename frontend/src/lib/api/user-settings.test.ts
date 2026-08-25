import { beforeEach, describe, expect, it, vi } from "vitest";
import * as generated from "@/lib/api/generated/client";
import {
  createSSOProvider,
  createUser,
  getAPITokens,
  getUsers,
  resetUserPassword,
  updateUser,
} from "@/lib/api/user-settings";

vi.mock("@/lib/api/generated/client", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("@/lib/api/generated/client")>();
  return {
    ...actual,
    getUsers: vi.fn(),
    postUsers: vi.fn(),
    putUsersById: vi.fn(),
    postUsersByIdResetPassword: vi.fn(),
    getSettingsTokens: vi.fn(),
    postSettingsSso: vi.fn(),
  };
});

const userWire = {
  id: "1fa85f64-5717-4562-b3fc-2c963f66afa6",
  username: "ada",
  email: "ada@example.com",
  displayName: "Ada Lovelace",
  provider: "local",
  globalRoles: [],
  is_superuser: true,
  enabled: true,
  lastLogin: "2026-08-23T00:00:00Z",
  createdAt: "2026-08-22T00:00:00Z",
};

describe("user/settings generated API boundary", () => {
  beforeEach(() => vi.clearAllMocks());

  it("maps paginated user rows and preserves the real superuser flag", async () => {
    vi.mocked(generated.getUsers).mockResolvedValueOnce({
      data: [userWire],
      count: 1,
      next: null,
      previous: null,
    });

    const result = await getUsers({ page: 1, pageSize: 50 });

    expect(generated.getUsers).toHaveBeenCalledWith({
      query: { limit: 50, offset: 0 },
    });
    expect(result.data[0]).toEqual(
      expect.objectContaining({
        displayName: "Ada Lovelace",
        isSuperuser: true,
        enabled: true,
      }),
    );
  });

  it("translates durable user write fields instead of ignored UI fields", async () => {
    vi.mocked(generated.postUsers).mockResolvedValueOnce({ data: userWire });
    vi.mocked(generated.putUsersById).mockResolvedValueOnce({ data: userWire });

    await createUser({
      username: "ada",
      email: "ada@example.com",
      displayName: "Ada Lovelace",
      password: "correct horse battery staple",
    });
    await updateUser(userWire.id, {
      displayName: "Augusta Ada King",
      email: "ada@example.com",
      enabled: false,
    });

    expect(generated.postUsers).toHaveBeenCalledWith({
      body: expect.objectContaining({
        first_name: "Ada",
        last_name: "Lovelace",
      }),
    });
    expect(generated.putUsersById).toHaveBeenCalledWith({
      path: { id: userWire.id },
      body: {
        email: "ada@example.com",
        is_active: false,
        first_name: "Augusta",
        last_name: "Ada King",
      },
    });
  });

  it("maps one-time secrets and snake-case settings requests explicitly", async () => {
    vi.mocked(generated.postUsersByIdResetPassword).mockResolvedValueOnce({
      data: { temporary_password: "generated-secret" },
    });
    vi.mocked(generated.getSettingsTokens).mockResolvedValueOnce({
      data: [
        {
          id: "2fa85f64-5717-4562-b3fc-2c963f66afa6",
          name: "automation",
          prefix: "ast_123",
          is_revoked: false,
          scopes: ["read"],
        },
      ],
      count: 1,
      next: null,
      previous: null,
    });
    vi.mocked(generated.postSettingsSso).mockResolvedValueOnce({
      data: {
        id: "3fa85f64-5717-4562-b3fc-2c963f66afa6",
        type: "oidc",
        name: "Corporate",
        enabled: true,
        config: {},
      },
    });

    await expect(resetUserPassword(userWire.id)).resolves.toEqual({
      temporaryPassword: "generated-secret",
    });
    await expect(getAPITokens()).resolves.toEqual([
      expect.objectContaining({
        name: "automation",
        isRevoked: false,
        scopes: ["read"],
      }),
    ]);
    await createSSOProvider({
      type: "oidc",
      name: "Corporate",
      enabled: true,
      config: {
        clientId: "client-id",
        clientSecret: "client-secret",
        metadataUrl: "https://issuer.example/.well-known/openid-configuration",
      },
    });
    expect(generated.postSettingsSso).toHaveBeenCalledWith({
      body: expect.objectContaining({
        config: expect.objectContaining({
          client_id: "client-id",
          client_secret: "client-secret",
          metadata_url:
            "https://issuer.example/.well-known/openid-configuration",
        }),
      }),
    });
  });
});
