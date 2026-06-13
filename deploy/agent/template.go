package agenttemplate

import (
	_ "embed"
	"strings"
)

//go:embed install.yaml.template
var installTemplate string

const (
	PrivilegeProfileAnnotation = "astronomer.io/agent-privilege-profile"

	PrivilegeProfileAdmin    = "admin"
	PrivilegeProfileOperator = "operator"
	PrivilegeProfileViewer   = "viewer"
)

type InstallTemplateData struct {
	ServerURL         string
	ClusterID         string
	RegistrationToken string
	CACert            string
	AgentImage        string
	PrivilegeProfile  string
}

func RenderInstallYAML(data InstallTemplateData) string {
	return strings.NewReplacer(
		"{{SERVER_URL}}", data.ServerURL,
		"{{CLUSTER_ID}}", data.ClusterID,
		"{{REGISTRATION_TOKEN}}", data.RegistrationToken,
		"{{CA_CERT}}", data.CACert,
		"{{AGENT_IMAGE}}", data.AgentImage,
		"{{AGENT_RBAC_RULES}}", RBACRulesYAML(data.PrivilegeProfile),
	).Replace(installTemplate)
}

func NormalizePrivilegeProfile(profile string) string {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case PrivilegeProfileViewer:
		return PrivilegeProfileViewer
	case PrivilegeProfileOperator:
		return PrivilegeProfileOperator
	default:
		return PrivilegeProfileAdmin
	}
}

func RBACRulesYAML(profile string) string {
	switch NormalizePrivilegeProfile(profile) {
	case PrivilegeProfileViewer:
		return viewerRBACRulesYAML
	case PrivilegeProfileOperator:
		return operatorRBACRulesYAML
	default:
		return adminRBACRulesYAML
	}
}

const adminRBACRulesYAML = `  # Full access is the compatibility profile for existing clusters.
  - apiGroups: ["*"]
    resources: ["*"]
    verbs: ["*"]
  - nonResourceURLs: ["*"]
    verbs: ["*"]`

const viewerRBACRulesYAML = `  # Read-only inventory, logs, and health endpoints.
  - apiGroups: [""]
    resources: ["configmaps", "endpoints", "events", "namespaces", "nodes", "persistentvolumeclaims", "persistentvolumes", "pods", "pods/log", "replicationcontrollers", "secrets", "services", "serviceaccounts"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["apps"]
    resources: ["daemonsets", "deployments", "replicasets", "statefulsets"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["batch"]
    resources: ["cronjobs", "jobs"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["autoscaling"]
    resources: ["horizontalpodautoscalers"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["networking.k8s.io"]
    resources: ["ingresses", "networkpolicies"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["policy"]
    resources: ["poddisruptionbudgets"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["apiextensions.k8s.io"]
    resources: ["customresourcedefinitions"]
    verbs: ["get", "list", "watch"]
  - nonResourceURLs: ["/healthz", "/livez", "/readyz", "/metrics", "/version"]
    verbs: ["get"]`

const operatorRBACRulesYAML = `  # Common workload operations without cluster-admin or RBAC escalation.
  - apiGroups: [""]
    resources: ["configmaps", "endpoints", "events", "namespaces", "nodes", "persistentvolumeclaims", "persistentvolumes", "pods", "pods/log", "replicationcontrollers", "secrets", "services", "serviceaccounts"]
    verbs: ["get", "list", "watch"]
  - apiGroups: [""]
    resources: ["configmaps", "events", "namespaces", "persistentvolumeclaims", "pods", "pods/attach", "pods/exec", "pods/portforward", "secrets", "services", "serviceaccounts"]
    verbs: ["create", "update", "patch", "delete"]
  - apiGroups: ["apps"]
    resources: ["daemonsets", "deployments", "replicasets", "statefulsets"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: ["batch"]
    resources: ["cronjobs", "jobs"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: ["autoscaling"]
    resources: ["horizontalpodautoscalers"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: ["networking.k8s.io"]
    resources: ["ingresses", "networkpolicies"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: ["policy"]
    resources: ["poddisruptionbudgets"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: ["rbac.authorization.k8s.io"]
    resources: ["roles", "rolebindings"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: ["apiextensions.k8s.io"]
    resources: ["customresourcedefinitions"]
    verbs: ["get", "list", "watch"]
  - nonResourceURLs: ["/healthz", "/livez", "/readyz", "/metrics", "/version"]
    verbs: ["get"]`
