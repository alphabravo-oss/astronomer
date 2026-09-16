package rbac

import "strings"

// KubernetesResource maps native Kubernetes resource names to Astronomer's
// coarse authorization families. Inputs may be singular or plural and use the
// explorer's stable route spelling.
func KubernetesResource(resourceType string) (Resource, bool) {
	switch strings.ToLower(strings.TrimSpace(resourceType)) {
	case "services", "service", "endpoints", "endpoint":
		return ResourceServices, true
	case "ingresses", "ingress",
		"gateways", "gateway",
		"httproutes", "httproute",
		"gatewayclasses", "gatewayclass",
		"grpcroutes", "grpcroute",
		"tcproutes", "tcproute",
		"udproutes", "udproute",
		"tlsroutes", "tlsroute",
		"referencegrants", "referencegrant":
		return ResourceIngresses, true
	case "networkpolicies", "networkpolicy":
		return ResourceNetworkPolicies, true
	case "persistentvolumes", "persistentvolume", "pv",
		"persistentvolumeclaims", "persistentvolumeclaim", "pvc",
		"storageclasses", "storageclass":
		return ResourceStorage, true
	case "configmaps", "configmap":
		return ResourceConfigMaps, true
	case "secrets", "secret":
		return ResourceSecrets, true
	case "pods", "pod":
		return ResourcePods, true
	case "nodes", "node":
		return ResourceNodes, true
	case "deployments", "deployment",
		"daemonsets", "daemonset",
		"statefulsets", "statefulset",
		"replicasets", "replicaset",
		"jobs", "job",
		"cronjobs", "cronjob",
		"hpa", "horizontalpodautoscalers", "horizontalpodautoscaler",
		"poddisruptionbudgets", "poddisruptionbudget":
		return ResourceWorkloads, true
	default:
		return "", false
	}
}
