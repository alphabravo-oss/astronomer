package agenttemplate

import (
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	fluxdistribution "github.com/alphabravocompany/astronomer-go/deploy/flux"
)

//go:embed install.yaml.template
var installTemplate string

//go:embed system-bootstrap.yaml.template
var systemBootstrapTemplate string

const (
	PrivilegeProfileAnnotation        = "astronomer.io/agent-privilege-profile"
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
	ServerURL         string
	ClusterID         string
	RegistrationToken string
	CACert            string
	// CAChecksum is the hex SHA-256 of the server CA (Rancher CATTLE_CA_CHECKSUM
	// semantics). Rendered into the agent's own Deployment env so a Phase-2
	// self-apply preserves the pin (otherwise self-applying the Deployment would
	// wipe it and silently drop CA pinning on the next restart).
	CAChecksum           string
	AgentImage           string
	PrivilegeProfile     string
	ServiceAccountName   string
	PodLabels            map[string]string
	SystemArtifactURL    string
	SystemArtifactDigest string
	SystemOIDCIssuer     string
	SystemOIDCIdentity   string
}

// CAChecksumFromPEM computes the Rancher CATTLE_CA_CHECKSUM-style pin for a CA
// bundle: the hex SHA-256 of the DER bytes of the FIRST certificate in the PEM.
// This must hash the same certificate the agent's VerifyConnection pins against
// (the chain root / trust anchor presented by the server). Returns "" when the
// bundle is empty or contains no parseable certificate, so a no-CA registration
// yields an empty checksum and the agent stays on the default OS-trust path.
func CAChecksumFromPEM(caPEM string) string {
	caPEM = strings.TrimSpace(caPEM)
	if caPEM == "" {
		return ""
	}
	block, _ := pem.Decode([]byte(caPEM))
	if block == nil {
		return ""
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(cert.Raw))
}

func RenderInstallYAML(data InstallTemplateData) string {
	profile := NormalizePrivilegeProfile(data.PrivilegeProfile)
	serviceAccountName := strings.TrimSpace(data.ServiceAccountName)
	if serviceAccountName == "" {
		serviceAccountName = "astronomer-agent"
	}
	insecure := "false"
	if parsed, err := url.Parse(strings.TrimSpace(data.ServerURL)); err == nil &&
		(parsed.Scheme == "http" || parsed.Scheme == "ws") {
		// Production rejects a plaintext server URL during config validation.
		// Development installs may intentionally use one; carry that explicit
		// URL choice into the agent's separate fail-closed acknowledgement.
		insecure = "true"
	}
	// The CA Secret uses a `data:` field, which is base64-encoded. An empty CA
	// stays empty (the Secret's ca.crt is "", the volume mount is optional, and
	// the agent falls back to OS trust). A configured PEM bundle is base64'd.
	caCert := ""
	if trimmed := strings.TrimSpace(data.CACert); trimmed != "" {
		caCert = base64.StdEncoding.EncodeToString([]byte(trimmed))
	}
	agentManifest := strings.NewReplacer(
		"{{AGENT_INSECURE}}", insecure,
		// L7: every scalar below renders into a double-quoted YAML scalar in
		// install.yaml.template (SERVER_URL/CLUSTER_ID/REGISTRATION_TOKEN/
		// CA_CHECKSUM/AGENT_IMAGE). SERVER_URL and AGENT_IMAGE are
		// operator-influenced config, so an embedded `"` or `\` would break out of the
		// scalar and inject arbitrary YAML. escapeYAMLDoubleQuoted neutralizes
		// both. (caCert is base64; the RBAC/label blocks are already escaped or
		// generated.)
		"{{SERVER_URL}}", escapeYAMLDoubleQuoted(data.ServerURL),
		"{{CLUSTER_ID}}", escapeYAMLDoubleQuoted(data.ClusterID),
		"{{REGISTRATION_TOKEN}}", escapeYAMLDoubleQuoted(data.RegistrationToken),
		"{{CA_CERT}}", caCert,
		"{{CA_CHECKSUM}}", escapeYAMLDoubleQuoted(strings.TrimSpace(data.CAChecksum)),
		"{{AGENT_IMAGE}}", escapeYAMLDoubleQuoted(data.AgentImage),
		"{{AGENT_IMAGE_REPOSITORY}}", escapeYAMLDoubleQuoted(imageRepositoryOf(data.AgentImage)),
		"{{SYSTEM_OIDC_ISSUER}}", escapeYAMLDoubleQuoted(strings.TrimSpace(data.SystemOIDCIssuer)),
		"{{SYSTEM_OIDC_IDENTITY}}", escapeYAMLDoubleQuoted(strings.TrimSpace(data.SystemOIDCIdentity)),
		"{{AGENT_SERVICE_ACCOUNT_NAME}}", serviceAccountName,
		"{{AGENT_POD_LABELS}}", PodLabelsYAML(data.PodLabels),
		"{{PRIVILEGE_PROFILE}}", profile,
		"{{AGENT_RBAC_RULES}}", RBACRulesYAML(profile),
		"{{AGENT_DELIVERY_CLUSTER_RULES}}", DeliveryClusterRulesYAML(profile),
		"{{AGENT_RBAC_BINDING_KIND}}", RBACBindingKind(profile),
		"{{AGENT_RBAC_BINDING_NAMESPACE}}", RBACBindingNamespaceLine(profile),
		"{{AGENT_SELF_MANAGEMENT_NAMESPACED_RULES}}", SelfManagementNamespacedRulesYAML(),
		"{{AGENT_SELF_MANAGEMENT_DEPLOYMENT_RULES}}", SelfManagementOwnDeploymentRulesYAML(),
	).Replace(installTemplate)
	return fluxdistribution.InstallYAML() + "\n" + agentManifest + renderSystemBootstrap(data)
}

func renderSystemBootstrap(data InstallTemplateData) string {
	artifactURL := strings.TrimSpace(data.SystemArtifactURL)
	if artifactURL != "" && !strings.HasPrefix(artifactURL, "oci://") {
		artifactURL = "oci://" + artifactURL
	}
	digest := strings.TrimSpace(data.SystemArtifactDigest)
	issuer := strings.TrimSpace(data.SystemOIDCIssuer)
	identity := strings.TrimSpace(data.SystemOIDCIdentity)
	parsedArtifact, artifactErr := url.Parse(artifactURL)
	parsedIssuer, issuerErr := url.Parse(issuer)
	if artifactErr != nil || parsedArtifact.Scheme != "oci" || parsedArtifact.Host == "" || parsedArtifact.User != nil || parsedArtifact.RawQuery != "" || parsedArtifact.Fragment != "" ||
		!regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(digest) || issuerErr != nil || parsedIssuer.Scheme != "https" || parsedIssuer.Host == "" ||
		identity == "" || len(identity) > 512 || strings.ContainsAny(identity, "\r\n\x00") {
		return ""
	}
	return "\n" + strings.NewReplacer(
		"{{SYSTEM_ARTIFACT_URL}}", escapeYAMLDoubleQuoted(artifactURL),
		"{{SYSTEM_ARTIFACT_DIGEST}}", digest,
		"{{SYSTEM_OIDC_ISSUER}}", escapeYAMLDoubleQuoted(issuer),
		"{{SYSTEM_OIDC_IDENTITY}}", escapeYAMLDoubleQuoted(identity),
	).Replace(systemBootstrapTemplate)
}

// imageRepositoryOf strips the tag and/or digest from an image reference,
// leaving the registry+path that the agent's self-upgrade allow-list matches
// on. Deliberately duplicated here rather than imported from internal/agent —
// that package imports this one. The agent re-parses and re-validates whatever
// lands in ASTRONOMER_AGENT_IMAGE_REPOSITORY, so this only has to be a faithful
// split, not a validator.
func imageRepositoryOf(image string) string {
	s := strings.TrimSpace(image)
	if i := strings.Index(s, "@"); i >= 0 {
		s = s[:i]
	}
	// The tag separator is the last ':' after the last '/', so a registry port
	// ("registry.example:5000/astronomer-agent") is not mistaken for a tag.
	if i := strings.LastIndex(s, ":"); i >= 0 && i > strings.LastIndex(s, "/") {
		s = s[:i]
	}
	return s
}

func NormalizePrivilegeProfile(profile string) string {
	normalized := strings.NewReplacer("_", "-", " ", "-").Replace(strings.ToLower(strings.TrimSpace(profile)))
	switch normalized {
	case "":
		// Default to least-privilege read-only viewer. An adopted cluster
		// should grant the agent the minimum to observe; broadening to
		// operator/admin is an explicit, auditable opt-in chosen at
		// registration. This keeps a no-annotation registration safe by
		// default and trivially removable (read-only ClusterRole, no
		// mutation surface).
		return PrivilegeProfileViewer
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
		// An explicitly-supplied but UNRECOGNIZED value is a misconfiguration
		// (e.g. a typo) — fail closed to read-only viewer rather than silently
		// granting admin. The empty/unspecified case is handled above.
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

// DeliveryClusterRulesYAML is the agent's separate, product-owned delivery
// axis. It grants only the object kinds emitted by the typed materializer; it
// never grants generic workload writes. Platform-scope ClusterRoleBinding
// writes remain exclusive to the explicit admin enrollment profile.
func DeliveryClusterRulesYAML(profile string) string {
	rules := deliveryClusterRulesYAML
	if NormalizePrivilegeProfile(profile) == PrivilegeProfileAdmin {
		rules += deliveryPlatformBindingRuleYAML
	}
	return rules
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

// AstronomerOwnedNamespaces is the exact set of namespaces Astronomer fully owns
// and deploys its own footprint into (agent, logging stack, baseline platform
// components). The self-management RBAC scope is bounded to precisely these
// namespaces — write access is granted HERE and nowhere else.
//
// Two-axis RBAC model:
//   - User-cluster scope (the privilege profile: viewer/operator/admin/
//     namespace-*) governs the agent's reach into the CUSTOMER's resources
//     (default, kube-system, app namespaces). A viewer stays strictly read-only
//     there.
//   - Astronomer self-management scope (this set + the agent's own Deployment)
//     governs the agent's reach into ASTRONOMER's OWN footprint. It is granted
//     for EVERY profile — including viewer — so the agent can always manage
//     Astronomer's components regardless of how little the user profile grants.
//
// Keeping these axes independent is what lets "viewer" mean "observe the
// customer cluster read-only, but Astronomer still fully manages its own
// components."
var AstronomerOwnedNamespaces = []string{
	"astronomer-system",
	"astronomer-monitoring",
	"astronomer-trivy-system",
	"astronomer-logging",
	"astronomer-ingress-nginx",
	"astronomer-cert-manager",
	"astronomer-gatekeeper-system",
}

// SelfManagementNamespacedRulesYAML returns the rules block granting full
// management (create/get/list/watch/update/patch/delete) over the namespaced
// resources Astronomer deploys into its OWN namespaces. This is emitted as a
// Role in each AstronomerOwnedNamespaces entry for EVERY profile (it is NOT
// profile-gated) — see AstronomerOwnedNamespaces for the two-axis rationale.
func SelfManagementNamespacedRulesYAML() string {
	return selfManagementNamespacedRulesYAML
}

// SelfManagementOwnDeploymentRulesYAML returns the rules block letting the agent
// patch/update its OWN Deployment (resourceName-scoped to "astronomer-agent"),
// so an Argo-driven version bump can roll the agent. Like the token Role, this
// is operational self-management and is granted for EVERY profile. Mirrors the
// astronomer-agent-token Role's resourceName-scoping so it never widens to
// other Deployments in the namespace.
func SelfManagementOwnDeploymentRulesYAML() string {
	return selfManagementOwnDeploymentRulesYAML
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
  # NOTE: secrets are intentionally absent — the viewer profile must not be able
  # to read secret data (the agent skips its secret informer under viewer).
  - apiGroups: [""]
    resources: ["configmaps", "endpoints", "events", "limitranges", "namespaces", "nodes", "persistentvolumeclaims", "persistentvolumes", "pods", "pods/log", "replicationcontrollers", "resourcequotas", "services", "serviceaccounts"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["apps"]
    resources: ["controllerrevisions", "daemonsets", "deployments", "replicasets", "statefulsets"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["batch"]
    resources: ["cronjobs", "jobs"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["autoscaling"]
    resources: ["horizontalpodautoscalers"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["events.k8s.io"]
    resources: ["events"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["networking.k8s.io"]
    resources: ["ingresses", "ingressclasses", "networkpolicies"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["policy"]
    resources: ["poddisruptionbudgets"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["apiextensions.k8s.io"]
    resources: ["customresourcedefinitions"]
    verbs: ["get", "list", "watch"]
  # Resource-usage metrics (metrics-server). Read-only usage data is exactly
  # what a viewer should see, and the agent needs it to report cluster CPU/memory
  # health. metrics.k8s.io serves only get/list (no watch).
  - apiGroups: ["metrics.k8s.io"]
    resources: ["nodes", "pods"]
    verbs: ["get", "list"]
  # Optional inventory mirrors — harmless if the CRDs are absent (the rule simply
  # grants nothing). Present so the agent's GatewayClass / Trivy informers don't
  # log RBAC denials where those operators are installed.
  - apiGroups: ["gateway.networking.k8s.io"]
    resources: ["gatewayclasses"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["aquasecurity.github.io"]
    resources: ["vulnerabilityreports"]
    verbs: ["get", "list", "watch"]
  - nonResourceURLs: ["/healthz", "/livez", "/readyz", "/metrics", "/version"]
    verbs: ["get"]`

const namespaceViewerRBACRulesYAML = `  # Namespace-scoped read-only inventory and logs in astronomer-system.
  - apiGroups: [""]
    resources: ["configmaps", "endpoints", "events", "persistentvolumeclaims", "pods", "pods/log", "replicationcontrollers", "services", "serviceaccounts"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["apps"]
    resources: ["controllerrevisions", "daemonsets", "deployments", "replicasets", "statefulsets"]
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

const operatorRBACRulesYAML = `  # HONEST SCOPE (H4): "operator" is a PRIVILEGED, near-cluster-admin tier — not a
  # safely-contained one. It grants cluster-wide secrets READ+WRITE and pod
  # exec/attach/portforward, which are textbook INDIRECT cluster-admin primitives:
  # reading every namespace's secrets exposes every ServiceAccount token (incl.
  # cluster-admin-bound SAs), and exec into a kube-system / control-plane pod
  # yields that pod's identity. It does NOT grant rbac.authorization.k8s.io WRITE
  # (no DIRECT self-escalation) and does not create CRDs/webhooks/storage classes,
  # but do not mistake that for containment. It is a deliberate, audited,
  # non-default opt-in (default is viewer). Trimming it to a truly-contained tier
  # vs splitting a "privileged-operator" is an open product decision (see D1).
  #
  # CLUSTER-WIDE READ is required for complete resource inventory, including
  # CRDs installed after adoption. An enumerated allowlist cannot remain complete.
  # The delta this adds over the enumerated reads below is small in practice: this
  # tier ALREADY reads every Secret in every namespace (see above), so the marginal
  # exposure is CRD//built-in types we hadn't enumerated. WRITE stays strictly on
  # the scoped allowlists that follow — this rule is read-only.
  - apiGroups: ["*"]
    resources: ["*"]
    verbs: ["get", "list", "watch"]
  # The enumerated reads below are subsumed by the cluster-wide read above. They
  # are retained as the explicit floor: they document the minimum this tier needs
  # for its own inventory/exec surface and keep working
  # if the wildcard is ever narrowed.
  - apiGroups: [""]
    resources: ["configmaps", "endpoints", "events", "namespaces", "nodes", "persistentvolumeclaims", "persistentvolumes", "pods", "pods/log", "replicationcontrollers", "secrets", "services", "serviceaccounts"]
    verbs: ["get", "list", "watch"]
  - apiGroups: [""]
    resources: ["configmaps", "events", "namespaces", "persistentvolumeclaims", "pods", "pods/attach", "pods/exec", "pods/portforward", "secrets", "services", "serviceaccounts"]
    verbs: ["create", "update", "patch", "delete"]
  - apiGroups: ["apps"]
    resources: ["controllerrevisions", "daemonsets", "deployments", "replicasets", "statefulsets"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["apps"]
    resources: ["daemonsets", "deployments", "replicasets", "statefulsets"]
    verbs: ["create", "update", "patch", "delete"]
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
  - apiGroups: ["metrics.k8s.io"]
    resources: ["nodes", "pods"]
    verbs: ["get", "list"]
  # RBAC objects are read-only here: granting write would let the operator
  # profile self-escalate by binding broader roles. RBAC write belongs to the
  # explicit admin profile only.
  - apiGroups: ["rbac.authorization.k8s.io"]
    resources: ["clusterroles", "clusterrolebindings", "roles", "rolebindings"]
    verbs: ["get", "list", "watch"]
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
    resources: ["controllerrevisions", "daemonsets", "deployments", "replicasets", "statefulsets"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["apps"]
    resources: ["daemonsets", "deployments", "replicasets", "statefulsets"]
    verbs: ["create", "update", "patch", "delete"]
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
  # RBAC objects are read-only: write would allow self-escalation within the
  # namespace. RBAC write belongs to the explicit admin profile only.
  - apiGroups: ["rbac.authorization.k8s.io"]
    resources: ["roles", "rolebindings"]
    verbs: ["get", "list", "watch"]`

const customRBACRulesYAML = `  # No default Kubernetes permissions. Bind explicit custom RBAC outside this manifest.
  []`

// selfManagementNamespacedRulesYAML is the write surface for Astronomer's own
// footprint, bounded to AstronomerOwnedNamespaces via a namespaced Role. It is
// the SAME for every profile — viewer included — because it governs the
// self-management axis, not the user-profile axis. It deliberately excludes
// rbac.authorization.k8s.io write (no self-escalation) and is namespaced (never
// a ClusterRole) so it can never reach the customer's namespaces.
const selfManagementNamespacedRulesYAML = `  # Astronomer manages its own footprint (agent, logging, baseline components)
  # inside these namespaces for EVERY profile. Granted via a namespaced Role so
  # it can never touch customer namespaces (default/kube-system/app namespaces).
  # NOTE: secrets are intentionally EXCLUDED. The agent's own rotated token
  # Secret is covered by the resourceName-scoped astronomer-agent-token Role;
  # any component that manages its own Secrets does so via the component's own
  # RBAC, not this self-management scope. No rbac.authorization.k8s.io write
  # either (no self-escalation).
  - apiGroups: [""]
    resources: ["configmaps", "endpoints", "persistentvolumeclaims", "pods", "services", "serviceaccounts"]
    verbs: ["create", "get", "list", "watch", "update", "patch", "delete"]
  - apiGroups: ["apps"]
    resources: ["daemonsets", "deployments", "replicasets", "statefulsets"]
    verbs: ["create", "get", "list", "watch", "update", "patch", "delete"]
  - apiGroups: ["batch"]
    resources: ["cronjobs", "jobs"]
    verbs: ["create", "get", "list", "watch", "update", "patch", "delete"]
  - apiGroups: ["autoscaling"]
    resources: ["horizontalpodautoscalers"]
    verbs: ["create", "get", "list", "watch", "update", "patch", "delete"]
  - apiGroups: ["networking.k8s.io"]
    resources: ["ingresses", "networkpolicies"]
    verbs: ["create", "get", "list", "watch", "update", "patch", "delete"]
  - apiGroups: ["policy"]
    resources: ["poddisruptionbudgets"]
    verbs: ["create", "get", "list", "watch", "update", "patch", "delete"]`

// selfManagementOwnDeploymentRulesYAML lets the agent manage ONLY its own
// Deployment (resourceName-scoped), mirroring the astronomer-agent-token Role.
// This is what lets an Argo-driven version bump roll the agent without granting
// write over other Deployments. Granted for every profile.
const selfManagementOwnDeploymentRulesYAML = `  - apiGroups: ["apps"]
    resources: ["deployments"]
    resourceNames: ["astronomer-agent"]
    verbs: ["get", "list", "watch", "update", "patch"]`

const deliveryClusterRulesYAML = `  # Closed workload-delivery graph. The agent
  # binary validates a typed full snapshot and deterministic ownership fences
  # before using these permissions; no raw manifest endpoint can reach them.
  - apiGroups: [""]
    resources: ["namespaces"]
    verbs: ["get", "create", "patch", "update"]
  - apiGroups: [""]
    resources: ["secrets", "serviceaccounts"]
    verbs: ["get", "list", "create", "patch", "update", "delete"]
  - apiGroups: ["rbac.authorization.k8s.io"]
    resources: ["roles", "rolebindings"]
    verbs: ["get", "list", "create", "patch", "update", "delete"]
  - apiGroups: ["source.toolkit.fluxcd.io"]
    resources: ["gitrepositories", "ocirepositories", "helmrepositories"]
    verbs: ["get", "list", "watch", "create", "patch", "update", "delete"]
  - apiGroups: ["kustomize.toolkit.fluxcd.io"]
    resources: ["kustomizations"]
    verbs: ["get", "list", "watch", "create", "patch", "update", "delete"]
  - apiGroups: ["helm.toolkit.fluxcd.io"]
    resources: ["helmreleases"]
    verbs: ["get", "list", "watch", "create", "patch", "update", "delete"]`

const deliveryPlatformBindingRuleYAML = `
  # Platform-scope assignments are independently gated in the signed release,
  # protocol validator, and admin enrollment profile. Assignment data can bind
  # only the fixed bootstrap-owned platform applier ClusterRole.
  - apiGroups: ["rbac.authorization.k8s.io"]
    resources: ["clusterrolebindings"]
    verbs: ["get", "list", "create", "patch", "update", "delete"]`
