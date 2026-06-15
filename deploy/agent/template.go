package agenttemplate

import (
	_ "embed"
	"sort"
	"strings"
)

//go:embed install.yaml.template
var installTemplate string

const (
	PrivilegeProfileAnnotation        = "astronomer.io/agent-privilege-profile"
	AgentImageAnnotation              = "management.astronomer.io/agent-image"
	AgentServiceAccountNameAnnotation = "management.astronomer.io/agent-service-account-name"
	AgentPodLabelsAnnotation          = "management.astronomer.io/agent-pod-labels"

	PrivilegeProfileAdmin             = "admin"
	PrivilegeProfileOperator          = "operator"
	PrivilegeProfileViewer            = "viewer"
	PrivilegeProfileNamespaceOperator = "namespace-operator"
	PrivilegeProfileNamespaceViewer   = "namespace-viewer"
	PrivilegeProfileCustom            = "custom"
)

type InstallTemplateData struct {
	ServerURL          string
	ClusterID          string
	RegistrationToken  string
	CACert             string
	AgentImage         string
	PrivilegeProfile   string
	ServiceAccountName string
	PodLabels          map[string]string
}

func RenderInstallYAML(data InstallTemplateData) string {
	profile := NormalizePrivilegeProfile(data.PrivilegeProfile)
	serviceAccountName := strings.TrimSpace(data.ServiceAccountName)
	if serviceAccountName == "" {
		serviceAccountName = "astronomer-agent"
	}
	return strings.NewReplacer(
		"{{SERVER_URL}}", data.ServerURL,
		"{{CLUSTER_ID}}", data.ClusterID,
		"{{REGISTRATION_TOKEN}}", data.RegistrationToken,
		"{{CA_CERT}}", data.CACert,
		"{{AGENT_IMAGE}}", data.AgentImage,
		"{{AGENT_SERVICE_ACCOUNT_NAME}}", serviceAccountName,
		"{{AGENT_POD_LABELS}}", PodLabelsYAML(data.PodLabels),
		"{{PRIVILEGE_PROFILE}}", profile,
		"{{AGENT_RBAC_RULES}}", RBACRulesYAML(profile),
		"{{AGENT_RBAC_BINDING_KIND}}", RBACBindingKind(profile),
		"{{AGENT_RBAC_BINDING_NAMESPACE}}", RBACBindingNamespaceLine(profile),
	).Replace(installTemplate)
}

func NormalizePrivilegeProfile(profile string) string {
	normalized := strings.NewReplacer("_", "-", " ", "-").Replace(strings.ToLower(strings.TrimSpace(profile)))
	switch normalized {
	case PrivilegeProfileAdmin:
		return PrivilegeProfileAdmin
	case PrivilegeProfileViewer:
		return PrivilegeProfileViewer
	case PrivilegeProfileOperator:
		return PrivilegeProfileOperator
	case PrivilegeProfileNamespaceViewer, "namespaced-viewer":
		return PrivilegeProfileNamespaceViewer
	case PrivilegeProfileNamespaceOperator, "namespaced-operator":
		return PrivilegeProfileNamespaceOperator
	case PrivilegeProfileCustom:
		return PrivilegeProfileCustom
	default:
		// Fail closed to least privilege: an empty or unrecognized profile
		// resolves to read-only viewer, never cluster-admin. Choosing a
		// broader profile (admin/operator) must be explicit.
		return PrivilegeProfileViewer
	}
}

func RBACRulesYAML(profile string) string {
	switch NormalizePrivilegeProfile(profile) {
	case PrivilegeProfileViewer:
		return viewerRBACRulesYAML
	case PrivilegeProfileOperator:
		return operatorRBACRulesYAML
	case PrivilegeProfileNamespaceViewer:
		return namespaceViewerRBACRulesYAML
	case PrivilegeProfileNamespaceOperator:
		return namespaceOperatorRBACRulesYAML
	case PrivilegeProfileCustom:
		return customRBACRulesYAML
	default:
		return adminRBACRulesYAML
	}
}

func RBACBindingKind(profile string) string {
	switch NormalizePrivilegeProfile(profile) {
	case PrivilegeProfileNamespaceViewer, PrivilegeProfileNamespaceOperator:
		return "RoleBinding"
	default:
		return "ClusterRoleBinding"
	}
}

func RBACBindingNamespaceLine(profile string) string {
	switch NormalizePrivilegeProfile(profile) {
	case PrivilegeProfileNamespaceViewer, PrivilegeProfileNamespaceOperator:
		return "  namespace: astronomer-system\n"
	default:
		return ""
	}
}

func PodLabelsYAML(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		if strings.TrimSpace(key) == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		b.WriteString("        ")
		b.WriteString(key)
		b.WriteString(": \"")
		b.WriteString(escapeYAMLDoubleQuoted(labels[key]))
		b.WriteString("\"\n")
	}
	return b.String()
}

func escapeYAMLDoubleQuoted(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `"`, `\"`)
}

const adminRBACRulesYAML = `  # Full access is the compatibility profile for existing clusters.
  - apiGroups: ["*"]
    resources: ["*"]
    verbs: ["*"]
  - nonResourceURLs: ["*"]
    verbs: ["*"]`

const viewerRBACRulesYAML = `  # Read-only inventory, logs, and health endpoints.
  - apiGroups: [""]
    resources: ["configmaps", "endpoints", "events", "namespaces", "nodes", "persistentvolumeclaims", "persistentvolumes", "pods", "pods/log", "replicationcontrollers", "services", "serviceaccounts"]
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

const namespaceViewerRBACRulesYAML = `  # Namespace-scoped read-only inventory and logs in astronomer-system.
  - apiGroups: [""]
    resources: ["configmaps", "endpoints", "events", "persistentvolumeclaims", "pods", "pods/log", "replicationcontrollers", "services", "serviceaccounts"]
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
    verbs: ["get", "list", "watch"]`

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

const namespaceOperatorRBACRulesYAML = `  # Namespace-scoped workload operations in astronomer-system.
  - apiGroups: [""]
    resources: ["configmaps", "endpoints", "events", "persistentvolumeclaims", "pods", "pods/log", "replicationcontrollers", "services", "serviceaccounts"]
    verbs: ["get", "list", "watch"]
  - apiGroups: [""]
    resources: ["configmaps", "events", "persistentvolumeclaims", "pods", "pods/attach", "pods/exec", "pods/portforward", "services", "serviceaccounts"]
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
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]`

const customRBACRulesYAML = `  # No default Kubernetes permissions. Bind explicit custom RBAC outside this manifest.
  []`
