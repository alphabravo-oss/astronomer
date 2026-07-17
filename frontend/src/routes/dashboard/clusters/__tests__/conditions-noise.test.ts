import { isNoisyCapabilityCondition } from '@/routes/dashboard/clusters/$id/index';
import type { ClusterCondition } from '@/types';

const cond = (type: string, status: string): ClusterCondition =>
  ({ type, status, reason: '', message: '', last_transition_time: '' }) as ClusterCondition;

describe('cluster condition noise filter', () => {
  it('hides a capability the cluster simply does not have', () => {
    // Most clusters never install the Gateway API CRDs. That is the norm, not a
    // finding, and it must not sit in the header as a red failure.
    expect(isNoisyCapabilityCondition(cond('GatewayAPISupported', 'False'))).toBe(true);
    expect(isNoisyCapabilityCondition(cond('GatewayAPISupported', 'Unknown'))).toBe(true);
  });

  it('keeps a capability the cluster does have', () => {
    expect(isNoisyCapabilityCondition(cond('GatewayAPISupported', 'True'))).toBe(false);
  });

  it('never hides a health condition — a failing one is the whole point', () => {
    for (const type of ['Connected', 'AgentReachable', 'ArgoCDAdopted', 'MetricsAvailable']) {
      for (const status of ['True', 'False', 'Unknown']) {
        expect(isNoisyCapabilityCondition(cond(type, status))).toBe(false);
      }
    }
  });
});
