import {
  getClusterAgents,
  getClusterAgentsByClusterIdDiagnosticsBundle,
  postClusterAgentsByClusterIdUpgradePlan,
  postClustersByIdRegister,
} from "@/lib/api/generated/client";
import {
  createAgentUpgradePlan,
  downloadAgentDiagnosticsBundle,
  getClusterAgents as listClusterAgents,
  registerCluster,
} from "./cluster-agents";

vi.mock("@/lib/api/generated/client", () => ({
  getClusterAgents: vi.fn(),
  getClusterAgentsByClusterIdDiagnostics: vi.fn(),
  getClusterAgentsByClusterIdDiagnosticsBundle: vi.fn(),
  getClusterAgentsByClusterIdOperations: vi.fn(),
  postClusterAgentsByClusterIdSelfTest: vi.fn(),
  postClusterAgentsByClusterIdUpgrade: vi.fn(),
  postClusterAgentsByClusterIdUpgradePlan: vi.fn(),
  postClustersByIdRegister: vi.fn(),
}));

const clusterId = "00000000-0000-4000-8000-000000000001";

describe("generated cluster agents API", () => {
  beforeEach(() => vi.clearAllMocks());

  it("maps list wire casing and propagates cancellation", async () => {
    const signal = new AbortController().signal;
    vi.mocked(getClusterAgents).mockResolvedValueOnce({
      data: {
        summary: {
          total_clusters: 1,
          connected: 1,
          degraded: 0,
          disconnected: 0,
          versions: { "1.0.0": 1 },
          profiles: { operator: 1 },
          statuses: { active: 1 },
          compatibility: { supported: 1 },
          server_version: "1.0.0",
          minimum_supported_agent_version: "1.0.0",
          minimum_compatible_agent_version: "1.0.0",
          generated_at: "2026-08-24T00:00:00Z",
        },
        items: [
          {
            cluster_id: clusterId,
            cluster_name: "cluster-one",
            cluster_display_name: "Cluster One",
            cluster_status: "active",
            is_local: false,
            agent_status: "connected",
            node_count: 3,
            privilege_profile: "operator",
            capabilities: {},
            compatibility_status: "supported",
          },
        ],
        limit: 100,
        offset: 0,
      },
    });

    await expect(
      listClusterAgents({ limit: 100 }, { signal }),
    ).resolves.toEqual(
      expect.objectContaining({
        items: [expect.objectContaining({ clusterId })],
      }),
    );
    expect(getClusterAgents).toHaveBeenCalledWith({
      query: { limit: 100 },
      signal,
    });
  });

  it("serializes the server's JSON diagnostic attachment into a Blob", async () => {
    vi.mocked(
      getClusterAgentsByClusterIdDiagnosticsBundle,
    ).mockResolvedValueOnce({
      version: "1",
      generated_at: "2026-08-24T00:00:00Z",
      cluster_id: clusterId,
      cluster_name: "cluster-one",
      diagnostics: {
        generated_at: "2026-08-24T00:00:00Z",
        agent: {} as never,
        recent_connections: [],
        conditions: [],
        recommendations: [],
        redactions: [],
        upgrade_recommendation: { status: "supported", message: "Current" },
      },
      notes: ["redacted"],
    });

    const blob = await downloadAgentDiagnosticsBundle(clusterId);
    expect(blob.type).toBe("application/json");
    expect(blob.size).toBeGreaterThan(0);
    expect(getClusterAgentsByClusterIdDiagnosticsBundle).toHaveBeenCalledWith({
      path: { cluster_id: clusterId },
      signal: undefined,
    });
  });

  it("maps upgrade input and returns the real registration-token receipt", async () => {
    vi.mocked(postClusterAgentsByClusterIdUpgradePlan).mockResolvedValueOnce({
      data: {
        cluster_id: clusterId,
        cluster_name: "cluster-one",
        target_version: "1.1.0",
        target_image: "agent:1.1.0",
        privilege_profile: "operator",
        strategy: "agent_self_rollout",
        batch_size: 1,
        max_unavailable: 1,
        ready: true,
        preflight_checks: [],
        steps: [],
        post_upgrade_health_checks: [],
        validation: [],
        rollback: [],
      },
    });
    vi.mocked(postClustersByIdRegister).mockResolvedValueOnce({
      data: {
        id: "token-1",
        cluster_id: clusterId,
        token: "secret",
        expires_at: "later",
      },
    });

    await createAgentUpgradePlan(clusterId, { targetVersion: "1.1.0" });
    expect(postClusterAgentsByClusterIdUpgradePlan).toHaveBeenCalledWith({
      path: { cluster_id: clusterId },
      body: expect.objectContaining({ target_version: "1.1.0" }),
      signal: undefined,
    });
    await expect(registerCluster(clusterId)).resolves.toEqual({
      id: "token-1",
      clusterId,
      token: "secret",
      expiresAt: "later",
    });
  });
});
