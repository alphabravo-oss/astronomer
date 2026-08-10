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
		readDesc("astronomer.installation.summary",
			"Installation identity: installation_id, platform name, astronomer_version, chart_version, namespace, release, kubernetes_version, kubernetes_distribution, and management component health. Use for 'what version of k8s/astronomer are we running'.",
			SourceAstronomerServer, "settings", nil, max),
		readDesc("astronomer.installation.readiness",
			"Boolean readiness of management plane: database, schema version/dirty, queues, and component ready/available counts.",
			SourceAstronomerServer, "settings", nil, max),
		readDesc("astronomer.installation.configuration",
			"Read allowlisted non-secret configuration keys only (keys array). Never returns credentials or raw env.",
			SourceAstronomerDatabase, "settings", []string{"keys"}, max),
		readDesc("astronomer.management.workloads",
			"List Astronomer-owned Deployments and StatefulSets in the install namespace with desired/ready/available replicas, pod restart/OOM/unready counts. Not a full-cluster list.",
			SourceManagementKubernetes, "workloads", []string{"page", "page_size"}, max),
		readDesc("astronomer.management.workload_get",
			"Get one owned workload by workload=deployment|statefulset/<name> including replica and pod health summary.",
			SourceManagementKubernetes, "workloads", []string{"workload"}, max),
		readDesc("astronomer.management.pods",
			"List Astronomer-owned Pods with phase, ready, restarts, node, containers, and owner workload. Optional component prefix filter and phase filter. Prefer this for 'what pods are running / crashlooping'.",
			SourceManagementKubernetes, "workloads", []string{"component", "phase", "page", "page_size"}, max),
		readDesc("astronomer.management.rollout_status",
			"Rollout status for one owned deployment or statefulset: desired/ready/updated/available, generation lag, progressing/available conditions, and whether the rollout is complete or stuck.",
			SourceManagementKubernetes, "workloads", []string{"workload"}, max),
		readDesc("astronomer.management.events",
			"Recent Kubernetes Warning/Normal events for owned components. Filter by component name, since duration, and limit.",
			SourceManagementKubernetes, "workloads", []string{"component", "since", "limit"}, max),
		readDesc("astronomer.management.pod_logs",
			"Bounded redacted log tail for one owned pod and container (default 200 lines, hard size bound). Requires pod and container names from pods/workloads tools first.",
			SourceManagementKubernetes, "logging", []string{"pod", "container", "lines"}, max),
		readDesc("astronomer.management.nodes",
			"Management-plane nodes: server_version, kubelet_version, OS, architecture, capacity, and Ready/pressure conditions.",
			SourceManagementKubernetes, "nodes", nil, max),
		readDesc("astronomer.management.storage",
			"Owned PersistentVolumeClaims phase, capacity, and access modes in the install namespace.",
			SourceManagementKubernetes, "storage", nil, max),
		readDesc("astronomer.management.network",
			"Owned Services and NetworkPolicies in the install namespace (ports, types, selectors).",
			SourceManagementKubernetes, "network_policies", nil, max),
		readDesc("astronomer.database.health",
			"Postgres health for the Astronomer database (connectivity, size, recovery state).",
			SourceAstronomerServer, "monitoring", nil, max),
		readDesc("astronomer.queue.health",
			"Background job queue health and readiness.",
			SourceManagementQueue, "monitoring", nil, max),
		readDesc("astronomer.queue.failed_tasks",
			"Paged list of failed background tasks with types; use before queue.retry_task.",
			SourceManagementQueue, "monitoring", []string{"page", "page_size", "task_type"}, max),
		readDesc("astronomer.argocd.self_management_status",
			"Argo CD self-management Application sync/health status for the Astronomer install.",
			SourceManagementArgo, "argocd", nil, max),
		readDesc("astronomer.migrations.status",
			"Schema migration version, dirty flag, and expected version.",
			SourceAstronomerDatabase, "settings", nil, max),
		readDesc("astronomer.backups.status",
			"Management-plane backup/drill status summaries (no credentials).",
			SourceAstronomerDatabase, "backups", nil, max),
		readDesc("astronomer.tls.status",
			"TLS certificate status for management endpoints.",
			SourceAstronomerServer, "security", nil, max),
		readDesc("astronomer.observability.health",
			"Bounded observability via fixed query_template enum: availability|latency|errors|saturation and optional range 5m|15m|1h|6h. Not free-form PromQL.",
			SourceAstronomerServer, "monitoring", []string{"query_template", "range"}, max),
		readDesc("astronomer.alert.list",
			"List product alerts filtered by status/severity with paging.",
			SourceAstronomerDatabase, "alerts", []string{"status", "severity", "page", "page_size"}, max),
		readDesc("astronomer.alert.get",
			"Get one product alert by alert_id UUID: severity, status, summary fields, and timestamps (no raw secrets).",
			SourceAstronomerDatabase, "alerts", []string{"alert_id"}, max),
		readDesc("astronomer.audit.recent_changes",
			"Recent product audit changes (resource_type/resource_id/since/limit). Content-bounded; not full log dumps.",
			SourceAstronomerDatabase, "audit_logs", []string{"resource_type", "resource_id", "since", "limit"}, max),
		readDesc("astronomer.agent_fleet.summary",
			"Fleet-wide agent connection summary (connected/stale counts). Uses management-plane database telemetry only; never proxies into customer clusters.",
			SourceAstronomerDatabase, "agents", []string{"stale_after_seconds"}, max),
		readDesc("astronomer.agent_fleet.list",
			"List registered cluster agents with environment/region/state filters.",
			SourceAstronomerDatabase, "agents", []string{"environment", "region", "state", "page", "page_size"}, max),
		readDesc("astronomer.agent_fleet.get",
			"One cluster agent record by cluster_id (identity, versions, connection state).",
			SourceAstronomerDatabase, "agents", []string{"cluster_id"}, max),
		readDesc("astronomer.agent_fleet.connection_history",
			"Connection history for one cluster agent (disconnects, auth errors).",
			SourceAstronomerDatabase, "agents", []string{"cluster_id", "since", "limit"}, max),
		readDesc("astronomer.agent_fleet.upgrade_status",
			"Agent upgrade desired vs installed state for one cluster_id.",
			SourceAstronomerDatabase, "agents", []string{"cluster_id"}, max),
		readDesc("astronomer.agent_fleet.ingestion_health",
			"Agent ingestion/command pipeline health for one cluster_id (audit/metrics/state ingestion).",
			SourceAstronomerDatabase, "agents", []string{"cluster_id"}, max),
		readDesc("astronomer.tunnel.health",
			"Management tunnel hub health and ownership.",
			SourceAstronomerServer, "agents", nil, max),
		readDesc("astronomer.tunnel.replica_distribution",
			"Tunnel replica distribution across server replicas.",
			SourceAstronomerServer, "agents", nil, max),
		readDesc("astronomer.tunnel.recent_errors",
			"Recent tunnel errors with optional connection_id filter.",
			SourceAstronomerServer, "agents", []string{"since", "limit", "connection_id"}, max),
	}
}

func WriteCapabilityCatalog() []CapabilityDescriptor {
	return []CapabilityDescriptor{
		writeDescWithTimeout("astronomer.management.workload_restart",
			"Restart one mutable management Deployment (server|worker|frontend). Use when the user asks to restart a management component. resource_id must be a session-scoped resource id from product context (default install scope is 'local'); workload=deployment/<name>; operation_id=any fresh opaque correlator (product replaces it with the trusted action id). Never ask the user for tool names or these ids.",
			SourceManagementKubernetes, "workloads", []string{"resource_id", "workload", "operation_id"}, false, 120),
		writeDescWithTimeout("astronomer.management.workload_rollout",
			"Trigger a rollout restart of one mutable management Deployment. resource_id=session-scoped id (default 'local'); workload=deployment/<name>; operation_id=any fresh opaque correlator.",
			SourceManagementKubernetes, "workloads", []string{"resource_id", "workload", "operation_id"}, false, 120),
		writeDesc("astronomer.management.workload_scale",
			"Scale a mutable management Deployment to replicas in [2,20]. Use when the user asks to scale a management workload. resource_id=session-scoped id from product context (default install scope is 'local'); workload=deployment/<name> e.g. deployment/astronomer-worker; replicas=N; operation_id=any fresh opaque correlator (product binds trusted action id). Do not ask the user for resource_id or operation_id.",
			SourceManagementKubernetes, "workloads", []string{"resource_id", "workload", "replicas", "operation_id"}, false),
		writeDesc("astronomer.argocd.self_management_sync",
			"Request Argo CD sync of the self-management Application (auto-eligible when mode=auto and centrally allowlisted). resource_id=session-scoped id (default 'local'); application name; operation_id=any fresh opaque correlator.",
			SourceManagementArgo, "argocd", []string{"resource_id", "application", "operation_id"}, true),
		writeDesc("astronomer.queue.retry_task",
			"Retry one failed allowlisted queue task by task_id (auto-eligible when mode=auto and centrally allowlisted). resource_id=session-scoped id; task_id from failed_tasks; operation_id=any fresh opaque correlator.",
			SourceManagementQueue, "monitoring", []string{"resource_id", "task_id", "operation_id"}, true),
		writeDesc("astronomer.management.run_job",
			"Run an owned CronJob-derived maintenance Job: management-plane-backup or restore-drill. resource_id=session-scoped id (default 'local'); job enum; operation_id=any fresh opaque correlator.",
			SourceManagementKubernetes, "workloads", []string{"resource_id", "job", "operation_id"}, false),
		writeDescWithTimeout("astronomer.tunnel.restart_component",
			"Restart tunnel-related management component server|worker. resource_id=session-scoped id; component; operation_id=any fresh opaque correlator.",
			SourceManagementKubernetes, "agents", []string{"resource_id", "component", "operation_id"}, false, 120),
	}
}

func readDesc(name, description string, source CapabilitySource, resource string, fields []string, maxBytes int) CapabilityDescriptor {
	return CapabilityDescriptor{
		Name: name, Description: description, SchemaVersion: "1",
		Effect: EffectRead, Risk: "low", TargetBounds: "astronomer_management_plane_only", Impact: "none",
		Reversibility: "not_applicable", Rollback: "not_applicable", Source: source, RBACResource: resource,
		RBACVerb: "read", AcceptedFields: fields, MaxResponseBytes: maxBytes, TimeoutSeconds: 10,
	}
}

func writeDesc(name, description string, source CapabilitySource, resource string, fields []string, auto bool) CapabilityDescriptor {
	return CapabilityDescriptor{
		Name: name, Description: description, SchemaVersion: "1",
		Effect: EffectWrite, Risk: "medium", TargetBounds: "allowlisted_astronomer_management_component_only",
		Impact: "bounded_management_plane_change", Reversibility: "adapter_declared", Rollback: "stop_and_operator_reconcile",
		Source: source, RBACResource: resource,
		RBACVerb: "update", AcceptedFields: fields, MaxResponseBytes: 64 << 10,
		TimeoutSeconds: 30, Destructive: false, AutoEligible: auto, Idempotent: true,
		RequiresPrecondition: true, RequiresVerification: true,
	}
}

func writeDescWithTimeout(name, description string, source CapabilitySource, resource string, fields []string, auto bool, timeoutSeconds int) CapabilityDescriptor {
	descriptor := writeDesc(name, description, source, resource, fields, auto)
	descriptor.TimeoutSeconds = timeoutSeconds
	return descriptor
}
