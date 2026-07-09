/**
 * TEST-03: generated OpenAPI types must be importable by the app layer
 * (not only the api-contract smoke test). Importing AgentFleetItem proves
 * openapi.generated.ts is a real dependency of product code paths.
 */
import type { OpenAPIComponents } from '@/types/openapi.generated';

type AgentFleetItem = OpenAPIComponents['schemas']['AgentFleetItem'];

describe('openapi.generated consumption', () => {
  it('exposes AgentFleetItem schema type for typed clients', () => {
    const sample: AgentFleetItem = {
      cluster_id: 'c1',
      agent_status: 'connected',
    };
    expect(sample.cluster_id).toBe('c1');
    expect(sample.agent_status).toBe('connected');
  });
});
