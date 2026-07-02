package protocol

import (
	"encoding/json"
	"time"
)

// MessageType identifies the kind of message sent over the tunnel.
type MessageType string

const (
	// Connection lifecycle
	MsgConnect    MessageType = "CONNECT"
	MsgConnectAck MessageType = "CONNECT_ACK"
	MsgDisconnect MessageType = "DISCONNECT"
	MsgHeartbeat  MessageType = "HEARTBEAT"
	MsgPong       MessageType = "PONG"

	// K8s API proxy
	MsgK8sRequest  MessageType = "K8S_REQUEST"
	MsgK8sResponse MessageType = "K8S_RESPONSE"

	// K8s API proxy — streaming variant for long-lived responses (Watch).
	// The server sends a K8S_STREAM_REQUEST when it detects a Watch-shaped
	// HTTP request (?watch=true, Accept: stream=watch, /watch/ path). The
	// agent replies with one MsgK8sStreamFrame{Kind:"header"}, zero or more
	// {Kind:"data"} frames, then exactly one {Kind:"end"}.
	MsgK8sStreamRequest MessageType = "K8S_STREAM_REQUEST"
	MsgK8sStreamFrame   MessageType = "K8S_STREAM_FRAME"
	// MsgK8sStreamStop is the server's request to terminate a k8s stream
	// (Watch) early. The agent cancels the per-stream context so its
	// kube-apiserver watch connection and pump goroutine drain, mirroring
	// MsgLogStop. StreamID identifies the stream; no payload is required.
	MsgK8sStreamStop MessageType = "K8S_STREAM_STOP"

	// Helm operations
	MsgHelmInstall   MessageType = "HELM_INSTALL"
	MsgHelmUpgrade   MessageType = "HELM_UPGRADE"
	MsgHelmUninstall MessageType = "HELM_UNINSTALL"
	MsgHelmRollback  MessageType = "HELM_ROLLBACK"
	MsgHelmStatus    MessageType = "HELM_STATUS"
	MsgHelmResult    MessageType = "HELM_RESULT"

	// Pod operations
	MsgExecStart  MessageType = "EXEC_START"
	MsgExecInput  MessageType = "EXEC_INPUT"
	MsgExecOutput MessageType = "EXEC_OUTPUT"
	MsgExecResize MessageType = "EXEC_RESIZE"
	MsgExecEnd    MessageType = "EXEC_END"
	MsgLogStart   MessageType = "LOG_START"
	MsgLogData    MessageType = "LOG_DATA"
	MsgLogEnd     MessageType = "LOG_END"
	// MsgLogStop is the server's request to terminate a log stream early.
	// The agent closes the underlying stream and emits LOG_END.
	MsgLogStop MessageType = "LOG_STOP"

	// Health & metrics
	MsgMetricsReport MessageType = "METRICS_REPORT"
	MsgHealthCheck   MessageType = "HEALTH_CHECK"
	MsgHealthResult  MessageType = "HEALTH_RESULT"

	// RBAC sync
	MsgRBACSyncRequest MessageType = "RBAC_SYNC_REQUEST"
	MsgRBACSyncResult  MessageType = "RBAC_SYNC_RESULT"

	// Service proxy (legacy aliases retained for backwards compatibility).
	MsgProxyRequest  MessageType = "PROXY_REQUEST"
	MsgProxyResponse MessageType = "PROXY_RESPONSE"

	// Service proxy: forwards an HTTP request to an in-cluster Service via the
	// agent. The agent makes the HTTP call to <svc>.<ns>.svc.cluster.local:<port>
	// and returns the response. Bodies are base64-encoded for binary safety.
	MsgServiceProxyRequest  MessageType = "SERVICE_PROXY_REQUEST"
	MsgServiceProxyResponse MessageType = "SERVICE_PROXY_RESPONSE"

	// Metrics / status reporting.
	// MsgMetrics carries detailed cluster CPU/memory/node/namespace metrics on
	// a separate ticker (config.MetricsInterval). Heartbeat carries lightweight
	// liveness data on its own (faster) ticker.
	MsgMetrics          MessageType = "METRICS"
	MsgHelmStatusResult MessageType = "HELM_STATUS_RESULT"
	MsgError            MessageType = "ERROR"

	// MsgStateUpdate is a coarse-grained "an object in the cluster changed"
	// notification fed by the agent's SharedInformerFactory. The server
	// translates a STATE_UPDATE into a `cluster.k8s_changed` SSE event so the
	// dashboard can invalidate its cached resource lists without polling.
	//
	// State updates are best-effort and rate-limited on both sides; subscribers
	// should treat them as invalidation hints, not authoritative deltas. They
	// never carry resource bodies — only enough metadata for the UI to know
	// what to refetch.
	MsgStateUpdate MessageType = "STATE_UPDATE"

	// Cluster decommission. Sent by the server to instruct the agent to
	// uninstall its managed-side resources (Fluent Bit / log forwarders,
	// Velero schedules/backups it owns, the agent's own Deployment) before
	// the server revokes the agent's registration token and severs the WS
	// tunnel. The agent replies with MsgDecommissionAck carrying a summary
	// of what it deleted (or a per-step error). Old agents without the
	// handler simply log "no handler for message type" and ignore — the
	// server logs a warning and falls back to manual-cleanup messaging in
	// the decommission row.
	MsgDecommission    MessageType = "DECOMMISSION"
	MsgDecommissionAck MessageType = "DECOMMISSION_ACK"

	// Agent lifecycle operations. The management plane records durable
	// operation intent, then sends an AGENT_UPGRADE command to the connected
	// agent. The agent patches its own Deployment image and replies with a
	// terminal AGENT_UPGRADE_RESULT so the operation row is no longer just a
	// queued advisory.
	MsgAgentUpgrade       MessageType = "AGENT_UPGRADE"
	MsgAgentUpgradeResult MessageType = "AGENT_UPGRADE_RESULT"

	// MsgMirrorEvent is the sprint-069 CRD-mirror v2 wire format. Unlike
	// MsgStateUpdate (which is a coarse invalidation hint), a MirrorEvent
	// carries the full resource body for one of the five mirrored GVKs so
	// the management plane can refresh its mirrored_* row without a
	// follow-up kubectl call. The agent emits one MirrorEvent per
	// Informer Add/Update/Delete callback for the five GVKs it watches:
	//
	//   - networking.k8s.io/v1 IngressClass
	//   - gateway.networking.k8s.io/v1 GatewayClass
	//   - networking.k8s.io/v1 NetworkPolicy
	//   - v1 ResourceQuota
	//   - v1 LimitRange
	//
	// There is no Result/Ack — the server is the authoritative writer of
	// the mirrored_* table and the agent doesn't need to know whether the
	// upsert succeeded; periodic prune (every 30m) covers any missed
	// deliveries.
	MsgMirrorEvent MessageType = "MIRROR_EVENT"

	// MsgApiserverAudit carries a batch of kube-apiserver audit.k8s.io Event
	// JSON documents the agent tailed from the apiserver audit log. The server
	// persists the batch under the AUTHENTICATED tunnel session's cluster ID —
	// the cluster_id is never taken from the payload, so a compromised agent
	// cannot forge events for another cluster.
	//
	// Delivery is AT-LEAST-ONCE via ack-before-checkpoint: each batch carries a
	// BatchID, and the server replies with a MsgApiserverAuditAck of the same
	// BatchID after attempting to persist. The agent blocks on that ack and only
	// advances its checkpoint on OK=true — on OK=false, timeout, or disconnect it
	// holds and re-forwards (idempotent ingest dedups on (cluster_id, audit_id)).
	MsgApiserverAudit MessageType = "APISERVER_AUDIT"
	// MsgApiserverAuditAck is the server's per-batch acknowledgement of a
	// MsgApiserverAudit frame. It carries the matching BatchID so the agent can
	// route it to the blocked sender. OK=true means the batch was durably
	// persisted (Accepted/Skipped report dedup counts); OK=false carries Error
	// and tells the agent to hold its checkpoint and re-forward.
	MsgApiserverAuditAck MessageType = "APISERVER_AUDIT_ACK"

	// Fleet-style PULL reconcile (sprint: pull-reconcile). The agent is the
	// LOCAL GitOps applier: it requests its DESIRED STATE from the management
	// plane over the existing WS tunnel, server-side-applies the returned
	// manifests into the astronomer-* owned namespaces, prunes managed objects
	// no longer desired, and reports per-manifest status back.
	//
	//   - MsgDesiredStateRequest  (agent -> server): "give me my desired state".
	//     Carries the cluster ID and the revision the agent last applied so the
	//     server could short-circuit (MVP always re-renders + returns).
	//   - MsgDesiredStateResponse (server -> agent): the rendered desired set —
	//     a stable revision hash plus an ordered list of manifest documents,
	//     each tagged with its target astronomer-* namespace.
	//   - MsgApplyStatus          (agent -> server): one-way report of what the
	//     agent applied for a revision (per-manifest applied/error).
	//
	// The whole pull path is gated OFF by default behind PullReconcileEnabled;
	// the DesiredState responder is read-only rendering and may always answer.
	MsgDesiredStateRequest  MessageType = "DESIRED_STATE_REQUEST"
	MsgDesiredStateResponse MessageType = "DESIRED_STATE_RESPONSE"
	MsgApplyStatus          MessageType = "APPLY_STATUS"
)

// ApiserverAuditPayload is the body of a MsgApiserverAudit frame: a batch of
// raw audit.k8s.io Event JSON documents exactly as the agent read them from
// the apiserver audit log. The cluster the events belong to is NOT in the
// payload — the server derives it from the authenticated tunnel session.
type ApiserverAuditPayload struct {
	Events []json.RawMessage `json:"events"`
	// BatchID correlates this batch with its MsgApiserverAuditAck. Empty on
	// the legacy HTTP path (httpAuditSender), which acks via the HTTP status
	// code rather than a tunnel frame.
	BatchID string `json:"batch_id,omitempty"`
}

// ApiserverAuditAckPayload is the body of a MsgApiserverAuditAck frame: the
// server's per-batch acknowledgement of a MsgApiserverAudit. BatchID echoes the
// originating batch so the agent routes it to the blocked sender. OK=true means
// the batch was durably persisted; OK=false carries Error and tells the agent
// to hold its checkpoint and re-forward.
type ApiserverAuditAckPayload struct {
	BatchID  string `json:"batch_id"`
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Accepted int    `json:"accepted"`
	Skipped  int    `json:"skipped"`
}

// DecommissionPayload tells the agent which managed-side resources to remove.
// Fields are intentionally explicit (rather than an opaque "do everything"
// flag) so the server retains future flexibility — e.g. partial decommission
// where only logging is uninstalled. ManagedLabel narrows label-selector deletes
// (Velero Backup/Schedule CRs are filtered by this label so we don't wipe out
// resources the cluster operator owns).
type DecommissionPayload struct {
	ClusterID             string `json:"cluster_id"`
	RemoveLoggingStack    bool   `json:"remove_logging_stack"`
	RemoveVeleroManaged   bool   `json:"remove_velero_managed"`
	RemoveAgentDeployment bool   `json:"remove_agent_deployment"`
	ManagedLabel          string `json:"managed_label,omitempty"`
	// DryRun, if true, the agent reports what it WOULD delete without
	// touching the cluster. Used by the integration test path.
	DryRun bool `json:"dry_run,omitempty"`
	// AgentNamespace and AgentDeployment let the server name the resources
	// explicitly so the agent doesn't have to guess. Empty values fall back
	// to the agent's defaults ("astronomer-system" + "astronomer-agent").
	AgentNamespace  string `json:"agent_namespace,omitempty"`
	AgentDeployment string `json:"agent_deployment,omitempty"`

	// RemoveFullFootprint, when true, asks the agent to tear down the COMPLETE
	// managed footprint (all baseline namespaces, cluster-scoped RBAC, the
	// namespaced singletons + token Secret, and finally astronomer-system),
	// each strictly label-gated. Additive + default-false so an older agent
	// that doesn't understand it degrades to the legacy three-step behavior
	// (remove_logging_stack + remove_velero_managed + remove_agent_deployment).
	RemoveFullFootprint bool `json:"remove_full_footprint,omitempty"`
	// VeleroLabel is the label selector for managed Velero CRs + BSLs. Defaults
	// in-agent to "app.kubernetes.io/managed-by=astronomer-go" when empty.
	VeleroLabel string `json:"velero_label,omitempty"`
	// ManagedByLabel gates namespace deletion. Defaults in-agent to
	// "app.kubernetes.io/managed-by=astronomer-server" — only namespaces the
	// server created carry it, so an operator-precreated namespace is never
	// deleted.
	ManagedByLabel string `json:"managed_by_label,omitempty"`
	// RBACLabel gates cluster-scoped RBAC + the namespaced singletons. Defaults
	// in-agent to "app.kubernetes.io/part-of=astronomer".
	RBACLabel string `json:"rbac_label,omitempty"`
}

// DecommissionAckPayload is the agent's response to a Decommission message.
// Each step is reported individually so the server can compose its own
// per-phase status — the agent doesn't make policy decisions about overall
// success/failure beyond "did the K8s API accept my delete?".
type DecommissionAckPayload struct {
	ClusterID string                   `json:"cluster_id"`
	Steps     []DecommissionStepResult `json:"steps"`
	DryRun    bool                     `json:"dry_run,omitempty"`
}

// DecommissionStepResult is one row of the agent's per-resource cleanup
// outcome. Name is "remove_logging_stack" / "remove_velero_managed" /
// "remove_agent_deployment". Removed is the count of objects actually
// deleted (or that would be deleted in dry-run mode).
type DecommissionStepResult struct {
	Name    string `json:"name"`
	Success bool   `json:"success"`
	Removed int    `json:"removed"`
	Error   string `json:"error,omitempty"`
	Skipped bool   `json:"skipped,omitempty"`
	// Orphans lists managed resources the agent LISTED but did NOT delete
	// (e.g. Velero BackupStorageLocations whose backing cloud blobs must be
	// cleaned up out-of-band). The server emits an orphan-audit event for
	// these so an operator has a clear manual-cleanup signal.
	Orphans []string `json:"orphans,omitempty"`
}

// AgentUpgradePayload asks the agent to update its own Deployment image.
// OperationID maps back to agent_lifecycle_operations.id.
type AgentUpgradePayload struct {
	OperationID     string `json:"operation_id"`
	ClusterID       string `json:"cluster_id"`
	TargetVersion   string `json:"target_version"`
	TargetImage     string `json:"target_image"`
	AgentNamespace  string `json:"agent_namespace,omitempty"`
	AgentDeployment string `json:"agent_deployment,omitempty"`
}

// AgentUpgradeResultPayload reports whether the self-upgrade command was
// accepted by the Kubernetes API. A successful result means the Deployment
// was patched; rollout completion is confirmed by later heartbeats reporting
// the target agent version.
type AgentUpgradeResultPayload struct {
	OperationID   string `json:"operation_id"`
	ClusterID     string `json:"cluster_id"`
	Success       bool   `json:"success"`
	Message       string `json:"message,omitempty"`
	Error         string `json:"error,omitempty"`
	ObservedImage string `json:"observed_image,omitempty"`
}

// DesiredStateRequestPayload is the agent's request for its desired state.
// CurrentRevision is the revision hash the agent last successfully applied
// (empty on first reconcile). The server ignores the cluster ID in the
// payload for authorization — the authoritative cluster is the authenticated
// tunnel session — but it is echoed for log correlation.
type DesiredStateRequestPayload struct {
	ClusterID       string `json:"cluster_id"`
	CurrentRevision string `json:"current_revision,omitempty"`
}

// DesiredManifest is one rendered desired-state document. Namespace is the
// target astronomer-* namespace the document applies into; it MUST be one of
// deploy/agent.AstronomerOwnedNamespaces (the agent re-validates before apply,
// and prunes only within these namespaces by the managed-by label). Content is
// the raw manifest YAML (one or more YAML documents). Name/Kind are advisory
// metadata for status correlation and logging.
type DesiredManifest struct {
	Name      string `json:"name"`
	Kind      string `json:"kind,omitempty"`
	Namespace string `json:"namespace"`
	Content   string `json:"content"`
}

// DesiredStateResponsePayload is the server's rendered desired state for a
// cluster. Revision is a stable, deterministic hash of the rendered set so the
// agent can skip re-applying an unchanged revision and report status against a
// concrete version. Manifests is the ordered set of documents to apply, each
// bounded to an astronomer-* namespace.
type DesiredStateResponsePayload struct {
	ClusterID string            `json:"cluster_id"`
	Revision  string            `json:"revision"`
	Manifests []DesiredManifest `json:"manifests"`
}

// ApplyStatusPayload is the agent's one-way report of an apply pass for a
// revision. Results is per-manifest (keyed by DesiredManifest.Name). Success is
// the aggregate (all manifests applied). Pruned is the count of managed objects
// the agent deleted because they were no longer desired.
type ApplyStatusPayload struct {
	ClusterID string             `json:"cluster_id"`
	Revision  string             `json:"revision"`
	Success   bool               `json:"success"`
	Pruned    int                `json:"pruned,omitempty"`
	Results   []ApplyResultEntry `json:"results,omitempty"`
	Error     string             `json:"error,omitempty"`
}

// ApplyResultEntry is the outcome of applying a single desired manifest.
type ApplyResultEntry struct {
	Name    string `json:"name"`
	Applied bool   `json:"applied"`
	Error   string `json:"error,omitempty"`
}

// Message is the envelope for all tunnel communication.
type Message struct {
	Type      MessageType     `json:"type"`
	StreamID  string          `json:"stream_id,omitempty"`
	RequestID string          `json:"request_id,omitempty"`
	ClusterID string          `json:"cluster_id,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// ConnectPayload is sent by the agent when establishing a connection.
type ConnectPayload struct {
	ClusterID    string `json:"cluster_id"`
	AgentID      string `json:"agent_id"`
	AgentVersion string `json:"agent_version"`
	Token        string `json:"token"`
}

// ConnectAckPayload is sent by the server to acknowledge a connection.
type ConnectAckPayload struct {
	SessionID     string `json:"session_id"`
	ServerVersion string `json:"server_version"`
	AgentToken    string `json:"agent_token,omitempty"`
	// AuditIngestToken is the scoped outbound API token (clusters:write only)
	// the agent uses with httpAuditSender to POST audit batches over plain
	// HTTP instead of the WS tunnel (PATH A). Empty when the server does not
	// issue one — the agent then falls back to the tunnel sender. Delivered
	// once on connect; treated as a credential and never logged.
	AuditIngestToken string `json:"audit_ingest_token,omitempty"`
	Accepted         bool   `json:"accepted"`
	Reason           string `json:"reason,omitempty"`
}

// K8sRequestPayload represents a proxied Kubernetes API request.
type K8sRequestPayload struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"` // base64 encoded
}

// K8sResponsePayload represents the result of a proxied Kubernetes API request.
type K8sResponsePayload struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"` // base64 encoded
}

// K8sStreamFrameKind is the discriminator for K8sStreamFrame.
type K8sStreamFrameKind string

const (
	// K8sStreamFrameHeader carries StatusCode + Headers. Sent first.
	K8sStreamFrameHeader K8sStreamFrameKind = "header"
	// K8sStreamFrameData carries one chunk of body bytes.
	K8sStreamFrameData K8sStreamFrameKind = "data"
	// K8sStreamFrameEnd terminates the stream. May carry an Error string if
	// the upstream connection failed mid-flight.
	K8sStreamFrameEnd K8sStreamFrameKind = "end"
)

// K8sStreamFrame is one frame of a streaming k8s proxy response. The lifecycle
// is exactly one Header, then zero or more Data frames, then exactly one End.
// All Data frames carry base64-encoded chunks; chunk size is agent-chosen and
// MUST NOT be assumed to align with watch-event boundaries — the consumer is
// responsible for re-framing the JSON-on-newlines protocol that k8s emits.
//
// Two distinct uses share this frame shape:
//   - Watch (the original): the agent sends a continuous stream as the
//     upstream k8s API emits events. Triggered by MsgK8sStreamRequest.
//   - Large unary response: the agent splits a
//     large response body into ≤K8sChunkSizeBytes data frames in
//     response to a normal MsgK8sRequest. The server's k8s_requester
//     auto-detects this shape and reassembles the body before
//     returning a single K8sResponsePayload to the caller.
type K8sStreamFrame struct {
	Kind       K8sStreamFrameKind `json:"kind"`
	StatusCode int                `json:"status_code,omitempty"`
	Headers    map[string]string  `json:"headers,omitempty"`
	Body       string             `json:"body,omitempty"` // base64 (data frames)
	Error      string             `json:"error,omitempty"`
}

// K8sChunkSizeBytes is the agent's threshold + per-data-frame body cap.
// Bodies <= this size travel as a single K8sResponse; larger bodies are
// split into K8sStreamFrame{Kind:"data"} chunks at exactly this length
// (the final chunk may be smaller). 256 KiB keeps assembled buffer
// growth predictable and stays well under the 16 MiB per-WS-frame cap.
const K8sChunkSizeBytes = 256 * 1024

// HelmRequestPayload represents a Helm operation request.
type HelmRequestPayload struct {
	ReleaseName string         `json:"release_name"`
	Namespace   string         `json:"namespace"`
	ChartURL    string         `json:"chart_url,omitempty"`
	ChartName   string         `json:"chart_name,omitempty"`
	RepoURL     string         `json:"repo_url,omitempty"`
	Version     string         `json:"version,omitempty"`
	Values      map[string]any `json:"values,omitempty"`
	Revision    int            `json:"revision,omitempty"` // for rollback
	Timeout     int            `json:"timeout,omitempty"`  // seconds
}

// HelmResultPayload is the response for any Helm operation.
type HelmResultPayload struct {
	Success     bool   `json:"success"`
	ReleaseName string `json:"release_name"`
	Namespace   string `json:"namespace"`
	Status      string `json:"status,omitempty"`
	Revision    int    `json:"revision,omitempty"`
	Error       string `json:"error,omitempty"`
}

// ErrorPayload carries error details.
type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

const HeartbeatSchemaVersion = 2

// HeartbeatPayload from agent health reports.
type HeartbeatPayload struct {
	SchemaVersion          int      `json:"schema_version"`
	Timestamp              string   `json:"timestamp"`
	KubernetesVersion      string   `json:"kubernetes_version"`
	Distribution           string   `json:"distribution"`
	NodeCount              int      `json:"node_count"`
	PodCount               int      `json:"pod_count"`
	CPUUsagePercent        float64  `json:"cpu_usage_percent"`
	MemoryUsagePercent     float64  `json:"memory_usage_percent"`
	AgentVersion           string   `json:"agent_version"`
	AgentBuildSHA          string   `json:"agent_build_sha,omitempty"`
	PrivilegeProfile       string   `json:"privilege_profile,omitempty"`
	AvailableAPIs          []string `json:"available_apis,omitempty"`
	EnabledFeatures        []string `json:"enabled_features,omitempty"`
	DeniedFeatures         []string `json:"denied_features,omitempty"`
	LastSuccessfulAction   string   `json:"last_successful_action,omitempty"`
	LastSuccessfulActionAt string   `json:"last_successful_action_at,omitempty"`
	DegradedReasons        []string `json:"degraded_reasons,omitempty"`
}

// ExecStartPayload to initiate pod exec.
type ExecStartPayload struct {
	Namespace string   `json:"namespace"`
	Pod       string   `json:"pod"`
	Container string   `json:"container"`
	Command   []string `json:"command"`
	TTY       bool     `json:"tty"`
	Stdin     bool     `json:"stdin"`
}

// ExecResizePayload for terminal resize.
type ExecResizePayload struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// LogStartPayload to initiate log streaming.
//
// TailLines and SinceSeconds map directly to k8s PodLogOptions. When
// SinceSeconds is non-nil the agent passes it through to kubelet; when nil,
// the older line-count path is used (Rancher-style: the UI picks one or the
// other, never both).
type LogStartPayload struct {
	Namespace    string `json:"namespace"`
	Pod          string `json:"pod"`
	Container    string `json:"container"`
	Follow       bool   `json:"follow"`
	TailLines    int    `json:"tail_lines,omitempty"`
	SinceSeconds *int64 `json:"since_seconds,omitempty"`
	Timestamps   bool   `json:"timestamps,omitempty"`
}

// RBACSyncRequestPayload contains RBAC resources to apply.
type RBACSyncRequestPayload struct {
	ClusterRoles        []json.RawMessage `json:"cluster_roles,omitempty"`
	ClusterRoleBindings []json.RawMessage `json:"cluster_role_bindings,omitempty"`
	Roles               []json.RawMessage `json:"roles,omitempty"`
	RoleBindings        []json.RawMessage `json:"role_bindings,omitempty"`
	// ManagedLabel is used for garbage collection of removed resources.
	ManagedLabel string `json:"managed_label,omitempty"`
}

// RBACSyncResultPayload reports the result of an RBAC sync.
type RBACSyncResultPayload struct {
	Applied int      `json:"applied"`
	Removed int      `json:"removed"`
	Errors  []string `json:"errors,omitempty"`
}

// ServiceProxyRequestPayload is sent by the server to ask the agent to forward
// an HTTP request to an in-cluster Service. The agent dials
// <ServiceName>.<Namespace>.svc.cluster.local:<Port>.
//
// Body is base64-encoded for binary safety (matches Go's K8sRequestPayload
// convention). The Python-era `body_encoding` field is omitted; bodies on the
// Go protocol are ALWAYS base64.
type ServiceProxyRequestPayload struct {
	ServiceName string            `json:"service_name"`
	Namespace   string            `json:"namespace"`
	Port        int               `json:"port"`
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	Headers     map[string]string `json:"headers,omitempty"`
	Body        string            `json:"body,omitempty"` // base64 encoded
	TimeoutSecs int               `json:"timeout_secs,omitempty"`
}

// ServiceProxyResponsePayload is the agent's response. Body is always
// base64-encoded.
type ServiceProxyResponsePayload struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"` // base64 encoded
	Error      string            `json:"error,omitempty"`
}

// StateUpdateOp is the kind of mutation observed by an informer.
type StateUpdateOp string

const (
	StateUpdateOpAdded    StateUpdateOp = "added"
	StateUpdateOpModified StateUpdateOp = "modified"
	StateUpdateOpDeleted  StateUpdateOp = "deleted"
)

// StateUpdatePayload is the body of a STATE_UPDATE message. It carries only
// metadata: never an object body. ConfigMaps and Secrets in particular MUST
// have their `data` fields stripped on the agent before serialization — the
// dashboard treats this as an invalidation hint and refetches via the normal
// k8s proxy with the user's RBAC scope.
//
// CoalesceKey is an optional hint for the server-side rate limiter. When
// empty, the server falls back to `kind|namespace|name`. Callers that want to
// collapse e.g. all pods in a Deployment to a single key can pre-compute it.
type StateUpdatePayload struct {
	Op              StateUpdateOp `json:"op"`
	Kind            string        `json:"kind"`
	APIGroup        string        `json:"api_group,omitempty"`
	APIVersion      string        `json:"api_version,omitempty"`
	Namespace       string        `json:"namespace,omitempty"`
	Name            string        `json:"name"`
	ResourceVersion string        `json:"resource_version,omitempty"`
	CoalesceKey     string        `json:"coalesce_key,omitempty"`
}

// NodeMetrics is per-node usage data.
type NodeMetrics struct {
	Name              string  `json:"name"`
	CPUUsageMillicore int64   `json:"cpu_usage_millicore"`
	CPUCapacityMilli  int64   `json:"cpu_capacity_millicore"`
	MemoryUsageBytes  int64   `json:"memory_usage_bytes"`
	MemoryCapacity    int64   `json:"memory_capacity_bytes"`
	CPUPercent        float64 `json:"cpu_percent"`
	MemoryPercent     float64 `json:"memory_percent"`
}

// NamespaceMetrics is per-namespace usage data.
type NamespaceMetrics struct {
	Name             string `json:"name"`
	PodCount         int    `json:"pod_count"`
	CPUUsageMilli    int64  `json:"cpu_usage_millicore"`
	MemoryUsageBytes int64  `json:"memory_usage_bytes"`
}

// MetricsPayload carries cluster-wide and per-node/namespace metrics. Sent on
// a separate ticker from heartbeat so observability tools can ingest it
// independently.
type MetricsPayload struct {
	Timestamp          string             `json:"timestamp"`
	MetricsAvailable   bool               `json:"metrics_available"`
	ClusterCPUUsage    float64            `json:"cluster_cpu_usage"`
	ClusterMemoryUsage float64            `json:"cluster_memory_usage"`
	ClusterPodCount    int                `json:"cluster_pod_count"`
	ClusterNodeCount   int                `json:"cluster_node_count"`
	Nodes              []NodeMetrics      `json:"nodes,omitempty"`
	Namespaces         []NamespaceMetrics `json:"namespaces,omitempty"`
}

// MirrorEventOp is the kind of mutation observed by an agent-side
// informer, mirrored verbatim from StateUpdateOp. We keep a sibling
// type rather than reusing StateUpdateOp so a future change to one
// flow (e.g. adding a "resync" op only to MirrorEvent) doesn't have
// to ripple through both.
type MirrorEventOp string

const (
	MirrorOpAdded    MirrorEventOp = "added"
	MirrorOpModified MirrorEventOp = "modified"
	MirrorOpDeleted  MirrorEventOp = "deleted"
)

// MirrorEventPayload carries one Add/Update/Delete event for one of the
// sprint-069 mirrored GVKs (IngressClass / GatewayClass / NetworkPolicy
// / ResourceQuota / LimitRange). Kind is the bare Kubernetes Kind name
// — matches the crd.Kind* constants on the server side. Object is the
// full unstructured.Unstructured JSON marshalled body; on a delete
// event, the server only consults Name + Namespace + Kind and the
// Object may be omitted.
//
// The server is the authoritative writer; there is no Ack. Periodic
// prune (every 30m) handles missed deliveries — agents re-send every
// object on reconnect, so a row that hasn't been touched in >1h is
// unambiguously gone.
type MirrorEventPayload struct {
	Op        MirrorEventOp `json:"op"`
	Kind      string        `json:"kind"`
	Namespace string        `json:"namespace,omitempty"`
	Name      string        `json:"name"`
	// Object is the raw unstructured JSON body of the resource. Empty on
	// delete events. The server passes this through to
	// internal/crd.Ingest{Kind} via unstructured.Unstructured{Object: …}.
	Object json.RawMessage `json:"object,omitempty"`
}
