import {
  deleteAuthDexConnectorsById,
  getAuthDexConnectorTypes,
  getAuthDexSettings,
  patchAuthDexConnectorsById,
  postAuthDexApply,
  postAuthDexConnectors,
  postAuthDexRegisterAsSso,
} from "@/lib/api/generated/client";
import {
  applyDexConfig,
  createDexConnector,
  deleteDexConnector,
  getDexConnectorTypes,
  getDexSettings,
  registerDexAsSSO,
  updateDexConnector,
} from "@/lib/api/dex";

vi.mock("@/lib/api/generated/client", () => ({
  deleteAuthDexConnectorsById: vi.fn(),
  getAuthDexConnectors: vi.fn(),
  getAuthDexConnectorsById: vi.fn(),
  getAuthDexConnectorTypes: vi.fn(),
  getAuthDexSettings: vi.fn(),
  patchAuthDexConnectorsById: vi.fn(),
  postAuthDexApply: vi.fn(),
  postAuthDexConnectors: vi.fn(),
  postAuthDexRegisterAsSso: vi.fn(),
  putAuthDexSettings: vi.fn(),
}));

const connectorWire = {
  id: "bcbaf40e-82c1-4a48-bfa9-20561558efbe",
  name: "corporate-oidc",
  type: "oidc",
  display_name: "Corporate OIDC",
  config: { issuer: "https://id.example.com" },
  enabled: true,
  created_at: "2026-08-24T07:00:00Z",
  updated_at: "2026-08-24T07:00:00Z",
};

describe("Dex generated API boundary", () => {
  beforeEach(() => vi.clearAllMocks());

  it("maps the exact connector-type registry contract", async () => {
    vi.mocked(getAuthDexConnectorTypes).mockResolvedValueOnce({
      data: [
        {
          type: "ldap",
          display_hint: "LDAP",
          required: ["host"],
          optional: ["insecureNoSSL"],
          secret: ["bindPW"],
          nested: [{ parent: "userSearch", keys: ["baseDN", "filter"] }],
        },
      ],
    });

    await expect(getDexConnectorTypes()).resolves.toEqual([
      {
        type: "ldap",
        displayHint: "LDAP",
        required: ["host"],
        optional: ["insecureNoSSL"],
        secret: ["bindPW"],
        nested: [{ parent: "userSearch", keys: ["baseDN", "filter"] }],
      },
    ]);
  });

  it("serializes connector writes and maps raw connector rows", async () => {
    vi.mocked(postAuthDexConnectors).mockResolvedValueOnce({
      data: connectorWire,
    });
    await expect(
      createDexConnector({
        name: "corporate-oidc",
        type: "oidc",
        displayName: "Corporate OIDC",
        config: { issuer: "https://id.example.com" },
        enabled: true,
      }),
    ).resolves.toEqual(
      expect.objectContaining({
        id: connectorWire.id,
        displayName: "Corporate OIDC",
        createdAt: connectorWire.created_at,
      }),
    );
    expect(postAuthDexConnectors).toHaveBeenCalledWith({
      body: expect.objectContaining({
        display_name: "Corporate OIDC",
        enabled: true,
      }),
    });

    vi.mocked(patchAuthDexConnectorsById).mockResolvedValueOnce({
      data: { ...connectorWire, enabled: false },
    });
    await updateDexConnector(connectorWire.id, { enabled: false });
    expect(patchAuthDexConnectorsById).toHaveBeenCalledWith({
      path: { id: connectorWire.id },
      body: {
        type: undefined,
        name: undefined,
        display_name: undefined,
        config: undefined,
        enabled: false,
      },
    });

    vi.mocked(deleteAuthDexConnectorsById).mockResolvedValueOnce({ data: {} });
    await deleteDexConnector(connectorWire.id);
    expect(deleteAuthDexConnectorsById).toHaveBeenCalledWith({
      path: { id: connectorWire.id },
    });
  });

  it("maps settings and runtime application receipts explicitly", async () => {
    vi.mocked(getAuthDexSettings).mockResolvedValueOnce({
      data: {
        configured: true,
        issuer_url: "https://dex.example.com",
        cluster_id: "cluster-1",
        namespace: "identity",
        release_name: "dex",
        runtime_secret_name: "dex-runtime",
        public_clients: [
          {
            id: "dashboard",
            redirectURIs: ["https://console.example.com/callback"],
            secret_configured: true,
          },
        ],
        expiry: { idTokens: "24h" },
        extra: {},
        runtime_phase: "cutover",
        runtime_generation: 7,
      },
    });
    await expect(getDexSettings()).resolves.toEqual(
      expect.objectContaining({
        issuerUrl: "https://dex.example.com",
        clusterId: "cluster-1",
        runtimeSecretName: "dex-runtime",
        publicClients: [
          expect.objectContaining({ id: "dashboard", secretConfigured: true }),
        ],
        runtimeGeneration: 7,
      }),
    );

    vi.mocked(postAuthDexApply).mockResolvedValueOnce({
      data: {
        operation_id: "00000000-0000-4000-8000-000000000020",
        action: "apply",
        target_id: "00000000-0000-4000-8000-000000000021",
        runtime_generation: 7,
        status: "pending",
        phase: "queued",
        attempt_count: 0,
        status_url: "/api/v1/auth/dex/operations/00000000-0000-4000-8000-000000000020/",
        created_at: "2026-08-24T07:05:00Z",
        updated_at: "2026-08-24T07:05:00Z",
      },
    });
    await expect(applyDexConfig()).resolves.toEqual(
      expect.objectContaining({
        applied: false,
        staged: true,
        runtimeState: "staged",
        runtimeGeneration: 7,
      }),
    );
  });

  it("maps Dex SSO registration results", async () => {
    vi.mocked(postAuthDexRegisterAsSso).mockResolvedValueOnce({
      data: {
        operation_id: "00000000-0000-4000-8000-000000000022",
        action: "register_sso",
        target_id: "00000000-0000-4000-8000-000000000023",
        runtime_generation: 8,
        status: "pending",
        phase: "queued",
        attempt_count: 0,
        status_url: "/api/v1/auth/dex/operations/00000000-0000-4000-8000-000000000022/",
        created_at: "2026-08-24T07:06:00Z",
        updated_at: "2026-08-24T07:06:00Z",
      },
    });

    await expect(registerDexAsSSO({ display_name: "Dex" })).resolves.toEqual(
      expect.objectContaining({
        provider: "dex",
        staged: true,
        applied: false,
        runtimeGeneration: 8,
      }),
    );
  });
});
