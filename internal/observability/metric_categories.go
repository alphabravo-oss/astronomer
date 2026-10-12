package observability

func registrationOutcomeLabel(outcome string) string {
	switch outcome {
	case "success", "failed":
		return outcome
	default:
		return "other"
	}
}

func vaultOutcomeLabel(outcome string) string {
	switch outcome {
	case VaultOutcomeSucceeded, VaultOutcomeFailed:
		return outcome
	default:
		return "other"
	}
}

func registrationPhaseLabel(phase string) string {
	switch phase {
	case "created", "awaiting_agent", "connected", "provisioning", "ready", "failed":
		return phase
	default:
		return "other"
	}
}

// StateMetricKind bounds metric labels to the agent's maintained informer
// families. It never changes the protocol payload, SSE event or limiter key.
// Discovered Gatekeeper resource types are emitted as the literal Constraint;
// future/custom peer values share other until a maintained projection adds them.
func StateMetricKind(kind string) string {
	switch kind {
	case "Pod", "Service", "Node", "ConfigMap", "Secret", "Deployment", "ReplicaSet", "StatefulSet", "DaemonSet", "Event",
		"Namespace", "Job", "CronJob", "Ingress", "NetworkPolicy", "PersistentVolume", "HorizontalPodAutoscaler", "ServiceAccount", "Role", "RoleBinding", "ClusterRole", "ClusterRoleBinding", "ResourceQuota",
		"Backup", "Restore", "Schedule", "VulnerabilityReport", "Constraint":
		return kind
	default:
		return "other"
	}
}
