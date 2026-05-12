package main

// scenario is one HTTP request profile in the load mix. weight is summed into
// a cumulative probability table at startup; path is appended to the server
// base URL.
//
// The chosen mix reflects the dashboard's hot path:
//   - cluster list / auth/me dominate because the frontend polls them on a
//     short interval
//   - resources/pods is the heaviest endpoint (routes through the agent
//     tunnel) — 25% gives us meaningful pressure on the WS path without
//     letting agent-side serialization dominate the run
//   - audit-logs / admin/queues exercise the worker + Asynq paths
type scenario struct {
	name   string
	path   string
	weight float64
}

// defaultScenarios returns the dashboard-shaped workload profile referenced by
// docs/scale-baseline.md. Keep this in sync with the doc table.
func defaultScenarios() []scenario {
	return []scenario{
		{name: "cluster_list", path: "/api/v1/clusters/", weight: 0.30},
		{name: "cluster_pods", path: "/api/v1/clusters/00000000-0000-0000-0000-000000000000/k8s/api/v1/pods", weight: 0.25},
		{name: "project_list", path: "/api/v1/projects/", weight: 0.10},
		{name: "audit_logs", path: "/api/v1/audit-logs/", weight: 0.10},
		{name: "admin_queues", path: "/api/v1/admin/queues/", weight: 0.05},
		{name: "auth_me", path: "/api/v1/auth/me/", weight: 0.20},
	}
}

// pickScenario maps a uniform [0,1) draw to a scenario using the weights as a
// cumulative distribution. The list MUST be non-empty.
func pickScenario(scs []scenario, draw float64) scenario {
	var cum float64
	for _, sc := range scs {
		cum += sc.weight
		if draw < cum {
			return sc
		}
	}
	// Fallthrough for floating-point rounding — return the last element.
	return scs[len(scs)-1]
}
