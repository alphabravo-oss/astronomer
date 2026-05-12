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
)

// DecommissionPayload tells the agent which managed-side resources to remove.
// Fields are intentionally explicit (rather than an opaque "do everything"
// flag) so the server retains future flexibility — e.g. partial decommission
// where only logging is uninstalled. ManagedLabel narrows label-selector deletes
// (Velero Backup/Schedule CRs are filtered by this label so we don't wipe out
// resources the cluster operator owns).
type DecommissionPayload struct {
	ClusterID            string `json:"cluster_id"`
	RemoveLoggingStack   bool   `json:"remove_logging_stack"`
	RemoveVeleroManaged  bool   `json:"remove_velero_managed"`
	RemoveAgentDeployment bool  `json:"remove_agent_deployment"`
	ManagedLabel         string `json:"managed_label,omitempty"`
	// DryRun, if true, the agent reports what it WOULD delete without
	// touching the cluster. Used by the integration test path.
	DryRun bool `json:"dry_run,omitempty"`
	// AgentNamespace and AgentDeployment let the server name the resources
	// explicitly so the agent doesn't have to guess. Empty values fall back
	// to the agent's defaults ("astronomer-system" + "astronomer-agent").
	AgentNamespace  string `json:"agent_namespace,omitempty"`
	AgentDeployment string `json:"agent_deployment,omitempty"`
}

// DecommissionAckPayload is the agent's response to a Decommission message.
// Each step is reported individually so the server can compose its own
// per-phase status — the agent doesn't make policy decisions about overall
// success/failure beyond "did the K8s API accept my delete?".
type DecommissionAckPayload struct {
	ClusterID string                  `json:"cluster_id"`
	Steps     []DecommissionStepResult `json:"steps"`
	DryRun    bool                    `json:"dry_run,omitempty"`
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
	Accepted      bool   `json:"accepted"`
	Reason        string `json:"reason,omitempty"`
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
//   - Large unary response (FEATURES-051126 T20): the agent splits a
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

// HeartbeatPayload from agent health reports.
type HeartbeatPayload struct {
	Timestamp          string  `json:"timestamp"`
	KubernetesVersion  string  `json:"kubernetes_version"`
	Distribution       string  `json:"distribution"`
	NodeCount          int     `json:"node_count"`
	PodCount           int     `json:"pod_count"`
	CPUUsagePercent    float64 `json:"cpu_usage_percent"`
	MemoryUsagePercent float64 `json:"memory_usage_percent"`
	AgentVersion       string  `json:"agent_version"`
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
