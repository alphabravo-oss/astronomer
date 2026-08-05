package charlie

// CapabilitySource is the only subsystem an Astronomer MCP adapter may read or
// mutate. No value represents a downstream tunnel or downstream Kubernetes API.
type CapabilitySource string

const (
	SourceAstronomerDatabase   CapabilitySource = "astronomer_database"
	SourceAstronomerServer     CapabilitySource = "astronomer_server_telemetry"
	SourceManagementKubernetes CapabilitySource = "management_kubernetes"
	SourceManagementArgo       CapabilitySource = "management_argocd"
	SourceManagementQueue      CapabilitySource = "management_queue"
)

type CapabilityDescriptor struct {
	Name             string
	Description      string
	SchemaVersion    string
	Effect           Effect
	Risk             string
	TargetBounds     string
	Impact           string
	Reversibility    string
	Rollback         string
	Source           CapabilitySource
	RBACResource     string
	RBACVerb         string
	AcceptedFields   []string
	MaxResponseBytes int
	TimeoutSeconds   int
	// Destructive is a product-owned catalog fact, never a model-supplied risk
	// label. Destructive or irreversible operations are denied in every Charlie
	// mode; v1 intentionally publishes none.
	Destructive          bool
	AutoEligible         bool
	Idempotent           bool
	RequiresPrecondition bool
	RequiresVerification bool
	ManagedTargetAccess  bool
}

func ReadCapabilityCatalog() []CapabilityDescriptor {
	const max = 64 << 10
	return []CapabilityDescriptor{
		read("astronomer.installation.summary", SourceAstronomerServer, "settings", nil, max),
		read("astronomer.installation.readiness", SourceAstronomerServer, "settings", nil, max),
		read("astronomer.installation.configuration", SourceAstronomerDatabase, "settings", []string{"keys"}, max),
		read("astronomer.management.workloads", SourceManagementKubernetes, "workloads", []string{"page", "page_size"}, max),
		read("astronomer.management.workload_get", SourceManagementKubernetes, "workloads", []string{"workload"}, max),
		read("astronomer.management.events", SourceManagementKubernetes, "workloads", []string{"component", "since", "limit"}, max),
		read("astronomer.management.pod_logs", SourceManagementKubernetes, "logging", []string{"pod", "container", "lines"}, max),
		read("astronomer.management.nodes", SourceManagementKubernetes, "nodes", nil, max),
		read("astronomer.management.storage", SourceManagementKubernetes, "storage", nil, max),
		read("astronomer.management.network", SourceManagementKubernetes, "network_policies", nil, max),
		read("astronomer.database.health", SourceAstronomerServer, "monitoring", nil, max),
		read("astronomer.queue.health", SourceManagementQueue, "monitoring", nil, max),
		read("astronomer.queue.failed_tasks", SourceManagementQueue, "monitoring", []string{"page", "page_size", "task_type"}, max),
		read("astronomer.argocd.self_management_status", SourceManagementArgo, "argocd", nil, max),
		read("astronomer.migrations.status", SourceAstronomerDatabase, "settings", nil, max),
		read("astronomer.backups.status", SourceAstronomerDatabase, "backups", nil, max),
		read("astronomer.tls.status", SourceAstronomerServer, "security", nil, max),
		read("astronomer.observability.health", SourceAstronomerServer, "monitoring", []string{"query_template", "range"}, max),
		read("astronomer.alert.list", SourceAstronomerDatabase, "alerts", []string{"status", "severity", "page", "page_size"}, max),
		read("astronomer.alert.get", SourceAstronomerDatabase, "alerts", []string{"alert_id"}, max),
		read("astronomer.audit.recent_changes", SourceAstronomerDatabase, "audit_logs", []string{"resource_type", "resource_id", "since", "limit"}, max),
		read("astronomer.agent_fleet.summary", SourceAstronomerDatabase, "agents", []string{"stale_after_seconds"}, max),
		read("astronomer.agent_fleet.list", SourceAstronomerDatabase, "agents", []string{"environment", "region", "state", "page", "page_size"}, max),
		read("astronomer.agent_fleet.get", SourceAstronomerDatabase, "agents", []string{"cluster_id"}, max),
		read("astronomer.agent_fleet.connection_history", SourceAstronomerDatabase, "agents", []string{"cluster_id", "since", "limit"}, max),
		read("astronomer.agent_fleet.upgrade_status", SourceAstronomerDatabase, "agents", []string{"cluster_id"}, max),
		read("astronomer.agent_fleet.ingestion_health", SourceAstronomerDatabase, "agents", []string{"cluster_id"}, max),
		read("astronomer.tunnel.health", SourceAstronomerServer, "agents", nil, max),
		read("astronomer.tunnel.replica_distribution", SourceAstronomerServer, "agents", nil, max),
		read("astronomer.tunnel.recent_errors", SourceAstronomerServer, "agents", []string{"since", "limit", "connection_id"}, max),
	}
}

func WriteCapabilityCatalog() []CapabilityDescriptor {
	return []CapabilityDescriptor{
		write("astronomer.management.workload_restart", SourceManagementKubernetes, "workloads", []string{"resource_id", "workload", "operation_id"}, false),
		write("astronomer.management.workload_rollout", SourceManagementKubernetes, "workloads", []string{"resource_id", "workload", "operation_id"}, false),
		write("astronomer.management.workload_scale", SourceManagementKubernetes, "workloads", []string{"resource_id", "workload", "replicas", "operation_id"}, false),
		write("astronomer.argocd.self_management_sync", SourceManagementArgo, "argocd", []string{"resource_id", "application", "operation_id"}, true),
		write("astronomer.queue.retry_task", SourceManagementQueue, "monitoring", []string{"resource_id", "task_id", "operation_id"}, true),
		write("astronomer.management.run_job", SourceManagementKubernetes, "workloads", []string{"resource_id", "job", "operation_id"}, false),
		write("astronomer.tunnel.restart_component", SourceManagementKubernetes, "agents", []string{"resource_id", "component", "operation_id"}, false),
	}
}

func read(name string, source CapabilitySource, resource string, fields []string, maxBytes int) CapabilityDescriptor {
	return CapabilityDescriptor{
		Name: name, Description: "Read bounded, redacted Astronomer-owned management-plane state", SchemaVersion: "1",
		Effect: EffectRead, Risk: "low", TargetBounds: "astronomer_management_plane_only", Impact: "none",
		Reversibility: "not_applicable", Rollback: "not_applicable", Source: source, RBACResource: resource,
		RBACVerb: "read", AcceptedFields: fields, MaxResponseBytes: maxBytes, TimeoutSeconds: 10,
	}
}

func write(name string, source CapabilitySource, resource string, fields []string, auto bool) CapabilityDescriptor {
	return CapabilityDescriptor{
		Name: name, Description: "Execute one bounded Astronomer-owned management-plane operation", SchemaVersion: "1",
		Effect: EffectWrite, Risk: "medium", TargetBounds: "allowlisted_astronomer_management_component_only",
		Impact: "bounded_management_plane_change", Reversibility: "adapter_declared", Rollback: "stop_and_operator_reconcile",
		Source: source, RBACResource: resource,
		RBACVerb: "update", AcceptedFields: fields, MaxResponseBytes: 64 << 10,
		TimeoutSeconds: 30, Destructive: false, AutoEligible: auto, Idempotent: true,
		RequiresPrecondition: true, RequiresVerification: true,
	}
}
