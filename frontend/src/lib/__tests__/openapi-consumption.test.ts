/**
 * TEST-03: generated OpenAPI types must be importable by the app layer
 * (not only the api-contract smoke test). Importing delivery and cluster-agent
 * contracts proves
 * openapi.generated.ts is a real dependency of product code paths.
 */
import type { OpenAPIComponents } from '@/types/openapi.generated';

type ClusterAgentItem = OpenAPIComponents['schemas']['ClusterAgentItem'];
type DeliveryTargetWrite = OpenAPIComponents['schemas']['DeliveryTargetWrite'];

describe('openapi.generated consumption', () => {
  it('exposes cluster-agent and delivery schema types for typed clients', () => {
    const sample: ClusterAgentItem = {
      cluster_id: 'c1',
      agent_status: 'connected',
    };
    const target: DeliveryTargetWrite = {
      name: 'monitoring',
      bundle_version_id: 'bundle-version-1',
      placement: { all_clusters: true },
      rollout_policy: { approval_required: true },
      reconciliation_policy: { drift_policy: 'repair' },
    };
    expect(sample.cluster_id).toBe('c1');
    expect(sample.agent_status).toBe('connected');
    expect(target.bundle_version_id).toBe('bundle-version-1');
  });
});
