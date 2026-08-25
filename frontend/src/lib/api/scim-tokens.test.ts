import {
  deleteAdminScimTokensById,
  getAdminScimTokens,
  postAdminScimTokens,
} from "@/lib/api/generated/client";
import {
  createSCIMToken,
  deleteSCIMToken,
  listSCIMTokens,
} from "@/lib/api/scim-tokens";

vi.mock("@/lib/api/generated/client", () => ({
  deleteAdminScimTokensById: vi.fn(),
  getAdminScimTokens: vi.fn(),
  postAdminScimTokens: vi.fn(),
}));

const tokenWire = {
  id: "1e5a4fe6-b2d1-4d97-bf8c-a8b239f963dd",
  name: "Okta provisioning",
  prefix: "astro_scim_AbCd",
  last_used_at: null,
  created_at: "2026-08-24T07:00:00Z",
};

describe("SCIM token generated API boundary", () => {
  beforeEach(() => vi.clearAllMocks());

  it("maps the handler's data-wrapped list response", async () => {
    vi.mocked(getAdminScimTokens).mockResolvedValueOnce({
      data: { tokens: [tokenWire] },
    });

    await expect(listSCIMTokens()).resolves.toEqual([
      {
        id: tokenWire.id,
        name: tokenWire.name,
        prefix: tokenWire.prefix,
        lastUsedAt: null,
        createdAt: tokenWire.created_at,
      },
    ]);
  });

  it("serializes create and preserves the one-time secret", async () => {
    vi.mocked(postAdminScimTokens).mockResolvedValueOnce({
      data: {
        ...tokenWire,
        token: "astro_scim_plaintext-once",
      },
    });

    await expect(createSCIMToken("Okta provisioning")).resolves.toEqual({
      id: tokenWire.id,
      name: tokenWire.name,
      prefix: tokenWire.prefix,
      lastUsedAt: null,
      createdAt: tokenWire.created_at,
      token: "astro_scim_plaintext-once",
    });
    expect(postAdminScimTokens).toHaveBeenCalledWith({
      body: { name: "Okta provisioning" },
    });
  });

  it("passes the token identifier through the generated delete path", async () => {
    vi.mocked(deleteAdminScimTokensById).mockResolvedValueOnce(undefined);
    await deleteSCIMToken(tokenWire.id);
    expect(deleteAdminScimTokensById).toHaveBeenCalledWith({
      path: { id: tokenWire.id },
    });
  });
});
