import type { MockedFunction } from "vitest";
import {
  adminVaultConnectionTest,
  adminVaultConnectionsCreate,
  adminVaultConnectionsList,
  putProjectsByIdDefaultVaultConnection,
} from "@/lib/api/generated/client";
import {
  createVaultConnection,
  listVaultConnections,
  setProjectDefaultVault,
  testVaultConnection,
} from "./vault";

vi.mock("@/lib/api/generated/client", () => ({
  adminVaultConnectionDelete: vi.fn(),
  adminVaultConnectionGet: vi.fn(),
  adminVaultConnectionHealth: vi.fn(),
  adminVaultConnectionsCreate: vi.fn(),
  adminVaultConnectionsList: vi.fn(),
  adminVaultConnectionTest: vi.fn(),
  adminVaultConnectionUpdate: vi.fn(),
  getProjectsByIdDefaultVaultConnection: vi.fn(),
  putProjectsByIdDefaultVaultConnection: vi.fn(),
}));

const mockedList = adminVaultConnectionsList as MockedFunction<
  typeof adminVaultConnectionsList
>;
const mockedCreate = adminVaultConnectionsCreate as MockedFunction<
  typeof adminVaultConnectionsCreate
>;
const mockedTest = adminVaultConnectionTest as MockedFunction<
  typeof adminVaultConnectionTest
>;
const mockedSetDefault =
  putProjectsByIdDefaultVaultConnection as MockedFunction<
    typeof putProjectsByIdDefaultVaultConnection
  >;

const wireConnection = {
  id: "11111111-1111-4111-8111-111111111111",
  name: "primary",
  description: "prod",
  addr: "https://vault.example.test",
  auth_method: "token" as const,
  auth: { token: "<encrypted>" },
  namespace: "platform",
  tls_skip_verify: false,
  ca_cert_pem: "",
  default_mount: "secret",
  enabled: true,
  last_health_ok: true,
  created_at: "2026-08-24T00:00:00Z",
  updated_at: "2026-08-24T00:00:00Z",
};

describe("vault generated API boundary", () => {
  beforeEach(() => vi.clearAllMocks());

  it("maps redacted wire records and forwards AbortSignal", async () => {
    const controller = new AbortController();
    mockedList.mockResolvedValueOnce({ data: { items: [wireConnection] } });

    const result = await listVaultConnections({ signal: controller.signal });

    expect(mockedList).toHaveBeenCalledWith({ signal: controller.signal });
    expect(result[0]).toMatchObject({
      authMethod: "token",
      tlsSkipVerify: false,
      defaultMount: "secret",
      lastHealthOk: true,
    });
  });

  it("passes the exact generated create body", async () => {
    mockedCreate.mockResolvedValueOnce({ data: wireConnection });
    const body = {
      name: "primary",
      addr: "https://vault.example.test",
      auth_method: "token" as const,
      auth: { token: "secret" },
    };

    await createVaultConnection(body);

    expect(mockedCreate).toHaveBeenCalledWith({
      body,
      signal: undefined,
    });
  });

  it("maps probe results without exposing transport envelopes", async () => {
    mockedTest.mockResolvedValueOnce({
      data: {
        ok: true,
        reachable: true,
        auth_ok: true,
        latency_ms: 8,
        message: "ok",
        probe_path: "secret/data/health",
      },
    });

    const result = await testVaultConnection(
      wireConnection.id,
      "secret/data/health",
    );

    expect(result).toEqual({
      ok: true,
      reachable: true,
      authOk: true,
      latencyMs: 8,
      message: "ok",
      probePath: "secret/data/health",
    });
  });

  it("preserves explicit null when clearing a project default", async () => {
    mockedSetDefault.mockResolvedValueOnce({ data: { connection_id: null } });

    await setProjectDefaultVault("project-1", null);

    expect(mockedSetDefault).toHaveBeenCalledWith({
      path: { id: "project-1" },
      body: { connection_id: null },
      signal: undefined,
    });
  });
});
