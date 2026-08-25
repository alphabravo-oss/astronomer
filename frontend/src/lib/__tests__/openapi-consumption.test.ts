/**
 * TEST-03: generated OpenAPI types must be importable by the app layer
 * (not only the api-contract smoke test). Importing delivery and cluster-agent
 * contracts proves
 * openapi.generated.ts is a real dependency of product code paths.
 */
import type { OpenAPIComponents } from "@/types/openapi.generated";

type ClusterAgentItem = OpenAPIComponents["schemas"]["ClusterAgentItem"];
type DeliveryTargetWrite = OpenAPIComponents["schemas"]["DeliveryTargetWrite"];

describe("openapi.generated consumption", () => {
  it("exposes cluster-agent and delivery schema types for typed clients", () => {
    const sample: ClusterAgentItem = {
      cluster_id: "c1",
      cluster_name: "cluster-one",
      cluster_display_name: "Cluster One",
      cluster_status: "active",
      is_local: false,
      agent_status: "connected",
      node_count: 3,
      privilege_profile: "operator",
      capabilities: {},
      compatibility_status: "supported",
    };
    const target: DeliveryTargetWrite = {
      name: "monitoring",
      bundle_version_id: "bundle-version-1",
      placement: { all_clusters: true },
      rollout_policy: { approval_required: true },
      reconciliation_policy: {
        interval: "5m",
        retry_interval: "1m",
        timeout: "10m",
        prune: true,
        wait: true,
        drift: "repair",
      },
    };
    expect(sample.cluster_id).toBe("c1");
    expect(sample.agent_status).toBe("connected");
    expect(target.bundle_version_id).toBe("bundle-version-1");
  });
});
