package queries

import (
	"os"
	"strings"
	"testing"
)

// TestFleetScaledManyQueriesAreBounded prevents high-cardinality workers and
// list endpoints from drifting back to full-table materialization. Queries
// bounded by an explicit input array are intentionally not listed here.
func TestFleetScaledManyQueriesAreBounded(t *testing.T) {
	tests := map[string][]string{
		"agents.sql": {
			"ListActiveConnections", "ListClusterConnectionStatus",
		},
		"kubectl_sessions.sql": {
			"ListActiveKubectlSessionsByCluster", "ListActiveKubectlSessionClusters",
			"ListAllActiveKubectlSessionsPage", "ListExpiredKubectlSessions",
		},
		"cluster_condition_remediation.sql": {"ListClusterConditionsByStatus"},
		"crd_mirror_v2.sql": {
			"ListMirroredIngressClasses", "ListMirroredGatewayClasses",
			"ListMirroredNetworkPolicies", "ListMirroredNetworkPoliciesByNamespace",
			"ListMirroredResourceQuotas", "ListMirroredResourceQuotasByNamespace",
			"ListMirroredLimitRanges", "ListMirroredLimitRangesByNamespace",
		},
		"delivery.sql": {
			"ListDeliveryPlanningCandidates", "ListDeliveryRolloutRuntime",
			"ListClusterDeliveryAssignments", "ListDeliveryEstateClusters",
		},
	}

	for file, names := range tests {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		text := string(raw)
		for _, name := range names {
			t.Run(name, func(t *testing.T) {
				start := strings.Index(text, "-- name: "+name+" :many")
				if start < 0 {
					t.Fatalf("query %s not found in %s", name, file)
				}
				block := text[start:]
				if next := strings.Index(block[len("-- name: "):], "-- name: "); next >= 0 {
					block = block[:len("-- name: ")+next]
				}
				if !strings.Contains(strings.ToUpper(block), "LIMIT") {
					t.Fatalf("fleet-scaled query %s in %s has no LIMIT", name, file)
				}
			})
		}
	}
}
