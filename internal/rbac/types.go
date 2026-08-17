package rbac

// Resource represents a protected resource type
type Resource string

const (
	ResourceClusters       Resource = "clusters"
	ResourceProjects       Resource = "projects"
	ResourceWorkloads      Resource = "workloads"
	ResourcePods           Resource = "pods"
	ResourceMonitoring     Resource = "monitoring"
	ResourceAlerts         Resource = "alerts"
	ResourceCatalog        Resource = "catalog"
	ResourceLogging        Resource = "logging"
	ResourceBackups        Resource = "backups"
	ResourceSecurity       Resource = "security"
	ResourceRBAC           Resource = "rbac"
	ResourceSettings       Resource = "settings"
	ResourceSSO            Resource = "sso"
	ResourceUsers          Resource = "users"
	ResourceAuditLogs      Resource = "audit_logs"
	ResourceAgents         Resource = "agents"
	ResourceSecrets        Resource = "secrets"
	ResourceConfigMaps     Resource = "configmaps"
	ResourceServices       Resource = "services"
	ResourceIngresses      Resource = "ingresses"
	ResourceStorage        Resource = "storage"
	ResourceNodes          Resource = "nodes"
	ResourceServiceMesh    Resource = "service_mesh"
	ResourceSupportBundles Resource = "support_bundles"
	// Delivery sources and bundles are separate project-scoped authority
	// boundaries: holding project metadata access must not implicitly reveal
	// source locations or permit credential rotation/version publication.
	ResourceDeliverySources     Resource = "delivery_sources"
	ResourceDeliveryBundles     Resource = "delivery_bundles"
	ResourceDeliveryTargets     Resource = "delivery_targets"
	ResourceDeliveryRollouts    Resource = "delivery_rollouts"
	ResourceDeliveryDeployments Resource = "delivery_deployments"
	ResourceDeliveryInventory   Resource = "delivery_inventory"
	ResourceDeliveryApprovals   Resource = "delivery_approvals"
	ResourceDeliveryRollbacks   Resource = "delivery_rollbacks"
	ResourceDeliveryOrphans     Resource = "delivery_orphans"
	ResourceDeliveryPlatform    Resource = "delivery_platform"
	// ResourceClusterTemplates gates the /api/v1/cluster-templates/* CRUD
	// (migration 049). The cluster bind/detach endpoints reuse
	// ResourceClusters + VerbUpdate so an operator who can already update a
	// cluster doesn't need a second permission to apply a template to it.
	ResourceClusterTemplates Resource = "cluster_templates"
	// ResourceNetworkPolicies gates the /api/v1/admin/network-policy-templates/*
	// CRUD (migration 068). The per-cluster apply endpoints reuse
	// ResourceClusters + VerbUpdate so an operator who can already update
	// a cluster can apply a template to one of its namespaces.
	ResourceNetworkPolicies Resource = "network_policies"
	// ResourceCustomResources gates k8s-proxy access to custom resources
	// (CRDs / arbitrary non-core apigroups under apis/<group>/<version>/...).
	// Previously such requests collapsed to the generic ResourceClusters
	// permission, so per-resource RBAC could not distinguish CRD access from
	// any other cluster read/write. Mapping them to a dedicated resource lets
	// operators grant or withhold CRD access deliberately (F2 / M3).
	ResourceCustomResources Resource = "custom_resources"
	// ResourceAuditIngest gates the agent's apiserver-audit ingest POST
	// (/api/v1/clusters/{cluster_id}/apiserver-audit/). Distinct from
	// ResourceAuditLogs, which gates reading that evidence back: ingest is a
	// machine-only write held by the per-cluster agent token, and giving it its
	// own resource keeps that credential from satisfying clusters:update — the
	// verb that also opens exec tickets and k8s-proxy writes.
	ResourceAuditIngest Resource = "audit_ingest"
	// ResourceCharlie gates the optional, local-agent-mediated Charlie SRE
	// integration. It never grants access to an underlying management resource;
	// every Charlie action must also pass that resource's live RBAC check.
	ResourceCharlie  Resource = "charlie"
	ResourceWildcard Resource = "*"
)

var canonicalResources = []Resource{
	ResourceClusters,
	ResourceProjects,
	ResourceWorkloads,
	ResourcePods,
	ResourceMonitoring,
	ResourceAlerts,
	ResourceCatalog,
	ResourceLogging,
	ResourceBackups,
	ResourceSecurity,
	ResourceRBAC,
	ResourceSettings,
	ResourceSSO,
	ResourceUsers,
	ResourceAuditLogs,
	ResourceAgents,
	ResourceSecrets,
	ResourceConfigMaps,
	ResourceServices,
	ResourceIngresses,
	ResourceStorage,
	ResourceNodes,
	ResourceServiceMesh,
	ResourceSupportBundles,
	ResourceDeliverySources,
	ResourceDeliveryBundles,
	ResourceDeliveryTargets,
	ResourceDeliveryRollouts,
	ResourceDeliveryDeployments,
	ResourceDeliveryInventory,
	ResourceDeliveryApprovals,
	ResourceDeliveryRollbacks,
	ResourceDeliveryOrphans,
	ResourceDeliveryPlatform,
	ResourceClusterTemplates,
	ResourceNetworkPolicies,
	ResourceCustomResources,
	ResourceAuditIngest,
	ResourceCharlie,
	ResourceWildcard,
}

// Verb represents an action on a resource
type Verb string

const (
	VerbCreate  Verb = "create"
	VerbRead    Verb = "read"
	VerbUpdate  Verb = "update"
	VerbDelete  Verb = "delete"
	VerbList    Verb = "list"
	VerbWatch   Verb = "watch"
	VerbScale   Verb = "scale"
	VerbRestart Verb = "restart"
	VerbExec    Verb = "exec"
	VerbLogs    Verb = "logs"
	VerbProxy   Verb = "proxy"
	VerbSync    Verb = "sync"
	VerbManage  Verb = "manage"
	// VerbApprove authorizes deciding one exact, expiring Charlie action. The
	// target resource verb remains independently mandatory at dispatch time.
	VerbApprove  Verb = "approve"
	VerbRollback Verb = "rollback"
	VerbOrphan   Verb = "orphan"
	VerbWildcard Verb = "*"
)

var canonicalVerbs = []Verb{
	VerbCreate,
	VerbRead,
	VerbUpdate,
	VerbDelete,
	VerbList,
	VerbWatch,
	VerbScale,
	VerbRestart,
	VerbExec,
	VerbLogs,
	VerbProxy,
	VerbSync,
	VerbManage,
	VerbApprove,
	VerbRollback,
	VerbOrphan,
	VerbWildcard,
}

// Rule represents a permission rule from a role's rules JSONB
type Rule struct {
	Resource string   `json:"resource"`
	Verbs    []string `json:"verbs"`
}

// CanonicalResources returns the stable RBAC resource vocabulary. The wildcard
// resource is included because built-in owner/admin templates intentionally use
// it.
func CanonicalResources() []Resource {
	return append([]Resource(nil), canonicalResources...)
}

// CanonicalVerbs returns the stable RBAC action vocabulary. The wildcard verb
// is included because built-in owner/admin templates intentionally use it.
func CanonicalVerbs() []Verb {
	return append([]Verb(nil), canonicalVerbs...)
}

func IsCanonicalResource(resource string) bool {
	for _, candidate := range canonicalResources {
		if resource == string(candidate) {
			return true
		}
	}
	return false
}

func IsCanonicalVerb(verb string) bool {
	for _, candidate := range canonicalVerbs {
		if verb == string(candidate) {
			return true
		}
	}
	return false
}

// RoleBinding ties a user/group to a role at a specific scope
type RoleBinding struct {
	UserID    string
	Group     string
	RoleRules []Rule
	BindingID string
	RoleID    string
	RoleName  string
	Scope     string
	// Scope context
	ClusterID string // empty for global
	ProjectID string // empty for global/cluster
	// Namespace optionally narrows a cluster/project binding to one Kubernetes
	// namespace. Empty means the binding applies to the full cluster/project
	// scope. Non-empty namespace bindings fail closed when the request does not
	// carry the same namespace context.
	Namespace string
	// IsSuperuser short-circuits permission checks to true. The middleware
	// emits a single synthetic binding with this flag when the underlying
	// user record has is_superuser=true on the users table.
	IsSuperuser bool
}
