package rbac

// Resource represents a protected resource type
type Resource string

const (
	ResourceClusters   Resource = "clusters"
	ResourceProjects   Resource = "projects"
	ResourceWorkloads  Resource = "workloads"
	ResourcePods       Resource = "pods"
	ResourceMonitoring Resource = "monitoring"
	ResourceAlerts     Resource = "alerts"
	ResourceCatalog    Resource = "catalog"
	ResourceLogging    Resource = "logging"
	ResourceBackups    Resource = "backups"
	ResourceSecurity   Resource = "security"
	ResourceRBAC       Resource = "rbac"
	ResourceSettings   Resource = "settings"
	ResourceArgoCD     Resource = "argocd"
	ResourceSSO        Resource = "sso"
	ResourceUsers      Resource = "users"
	ResourceAuditLogs  Resource = "audit_logs"
	ResourceAgents     Resource = "agents"
	// ResourceClusterTemplates gates the /api/v1/cluster-templates/* CRUD
	// (migration 049). The cluster bind/detach endpoints reuse
	// ResourceClusters + VerbUpdate so an operator who can already update a
	// cluster doesn't need a second permission to apply a template to it.
	ResourceClusterTemplates Resource = "cluster_templates"
	ResourceWildcard         Resource = "*"
)

// Verb represents an action on a resource
type Verb string

const (
	VerbCreate   Verb = "create"
	VerbRead     Verb = "read"
	VerbUpdate   Verb = "update"
	VerbDelete   Verb = "delete"
	VerbList     Verb = "list"
	VerbWatch    Verb = "watch"
	VerbScale    Verb = "scale"
	VerbRestart  Verb = "restart"
	VerbExec     Verb = "exec"
	VerbLogs     Verb = "logs"
	VerbProxy    Verb = "proxy"
	VerbSync     Verb = "sync"
	VerbManage   Verb = "manage"
	VerbWildcard Verb = "*"
)

// Rule represents a permission rule from a role's rules JSONB
type Rule struct {
	Resource string   `json:"resource"`
	Verbs    []string `json:"verbs"`
}

// RoleBinding ties a user/group to a role at a specific scope
type RoleBinding struct {
	UserID    string
	Group     string
	RoleRules []Rule
	// Scope context
	ClusterID string // empty for global
	ProjectID string // empty for global/cluster
	// IsSuperuser short-circuits permission checks to true. The middleware
	// emits a single synthetic binding with this flag when the underlying
	// user record has is_superuser=true on the users table.
	IsSuperuser bool
}
