package tasks

// renderNetworkPolicy expresses the requested isolation mode as a single
// ingress NetworkPolicy. Project isolation limits who may initiate traffic
// into workloads; it must not cut workloads off from DNS, the Kubernetes API,
// registries, or their other declared external dependencies. Unassigned
// namespaces are cluster-admin territory in Astronomer's RBAC model, so they
// provide the equivalent of Rancher's system project for platform operators.
func renderNetworkPolicy(namespace, projectID, mode string) map[string]any {
	platformPeers := []any{
		map[string]any{
			"namespaceSelector": map[string]any{
				"matchExpressions": []any{
					map[string]any{"key": projectNamespaceLabelKey, "operator": "DoesNotExist"},
				},
			},
		},
		map[string]any{
			"namespaceSelector": map[string]any{
				"matchLabels": map[string]any{"kubernetes.io/metadata.name": "kube-system"},
			},
		},
	}
	policy := map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata": map[string]any{
			"name":      managedNetworkPolicyName,
			"namespace": namespace,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": projectFieldManager,
			},
		},
		"spec": map[string]any{
			"podSelector": map[string]any{},
			"policyTypes": []any{"Ingress"},
			"ingress":     []any{map[string]any{"from": platformPeers}},
		},
	}
	if mode == "allow-same-project" {
		peer := map[string]any{
			"namespaceSelector": map[string]any{
				"matchLabels": map[string]any{
					projectNamespaceLabelKey: projectID,
				},
			},
		}
		from := append(append([]any{}, platformPeers...), peer)
		policy["spec"].(map[string]any)["ingress"] = []any{map[string]any{"from": from}}
	}
	// In "isolated" mode, only cluster/platform controllers are admitted.
	// Egress remains governed by user policies and the cluster's own defaults.
	return policy
}
