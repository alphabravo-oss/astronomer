package tunnel

import "math"

// agentInventoryCounts projects the agent's existing wire integers to the
// database's count domain. Zero remains valid for degraded or empty inventory.
func agentInventoryCounts(nodeCount, podCount int) (nodes, pods int32, valid bool) {
	if nodeCount < 0 || nodeCount > math.MaxInt32 || podCount < 0 || podCount > math.MaxInt32 {
		return 0, 0, false
	}
	return int32(nodeCount), int32(podCount), true
}
