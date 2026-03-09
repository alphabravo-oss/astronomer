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

	// Health & metrics
	MsgMetricsReport MessageType = "METRICS_REPORT"
	MsgHealthCheck   MessageType = "HEALTH_CHECK"
	MsgHealthResult  MessageType = "HEALTH_RESULT"

	// RBAC sync
	MsgRBACSyncRequest MessageType = "RBAC_SYNC_REQUEST"
	MsgRBACSyncResult  MessageType = "RBAC_SYNC_RESULT"

	// Service proxy
	MsgProxyRequest  MessageType = "PROXY_REQUEST"
	MsgProxyResponse MessageType = "PROXY_RESPONSE"

	// Metrics / status reporting
	MsgMetrics         MessageType = "METRICS"
	MsgHelmStatusResult MessageType = "HELM_STATUS_RESULT"
	MsgError           MessageType = "ERROR"
)

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
type LogStartPayload struct {
	Namespace  string `json:"namespace"`
	Pod        string `json:"pod"`
	Container  string `json:"container"`
	Follow     bool   `json:"follow"`
	TailLines  int    `json:"tail_lines,omitempty"`
	Timestamps bool   `json:"timestamps,omitempty"`
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
