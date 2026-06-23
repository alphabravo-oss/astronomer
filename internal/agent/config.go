package agent

import (
	"fmt"

	"github.com/alphabravocompany/astronomer-go/internal/envconfig"
)

// AgentConfig holds configuration for the agent process.
type AgentConfig struct {
	ServerURL         string `mapstructure:"server_url"`         // WebSocket server URL
	ClusterID         string `mapstructure:"cluster_id"`         // Cluster UUID
	AgentToken        string `mapstructure:"agent_token"`        // Registration token
	AgentID           string `mapstructure:"agent_id"`           // Unique agent instance ID
	TokenSecretName   string `mapstructure:"token_secret_name"`  // K8s Secret holding the durable token
	TokenSecretKey    string `mapstructure:"token_secret_key"`   // Secret key to rewrite on rotation
	ReconnectBackoff  int    `mapstructure:"reconnect_backoff"`  // Base backoff seconds (default 5)
	MaxReconnect      int    `mapstructure:"max_reconnect"`      // Max backoff seconds (default 300)
	HeartbeatInterval int    `mapstructure:"heartbeat_interval"` // Seconds (default 30)
	MetricsInterval   int    `mapstructure:"metrics_interval"`   // Seconds (default 60)
	HealthAddr        string `mapstructure:"health_addr"`        // Health server address (default :8081)
	PrivilegeProfile  string `mapstructure:"privilege_profile"`  // viewer|operator|namespace-viewer|namespace-operator|custom|admin

	// kube-apiserver audit-log forwarding (opt-in; disabled by default).
	// Requires a cluster-admin prerequisite: the apiserver must be started
	// with --audit-policy-file + --audit-log-path, and that log path must be
	// mounted into the agent pod via a hostPath volume. See docs.
	AuditEnabled       bool   `mapstructure:"audit_enabled"`        // Enable the apiserver-audit tailer (default false)
	AuditLogPath       string `mapstructure:"audit_log_path"`       // Path to the kube-apiserver audit log file
	AuditCheckpointPath string `mapstructure:"audit_checkpoint_path"` // Path where the tail offset is persisted (default audit_log_path + ".checkpoint")
	AuditBatchSize     int    `mapstructure:"audit_batch_size"`     // Max events per forwarded batch (default 100)
	AuditPollInterval  int    `mapstructure:"audit_poll_interval"`  // Seconds between tail polls (default 10)
	AuditDelivery      string `mapstructure:"audit_delivery"`       // How batches are delivered: tunnel (default) | http | stub
}

// LoadAgentConfig reads agent configuration from environment variables with
// sensible defaults. Environment variables are prefixed with ASTRONOMER_,
// e.g. ASTRONOMER_SERVER_URL, ASTRONOMER_CLUSTER_ID.
func LoadAgentConfig() (*AgentConfig, error) {
	v := envconfig.NewViper("ASTRONOMER")
	envconfig.SetDefaults(v,
		envconfig.Default{Key: "server_url", Value: ""},
		envconfig.Default{Key: "cluster_id", Value: ""},
		envconfig.Default{Key: "agent_token", Value: ""},
		envconfig.Default{Key: "agent_id", Value: ""},
		envconfig.Default{Key: "token_secret_name", Value: "astronomer-agent-token"},
		envconfig.Default{Key: "token_secret_key", Value: "token"},
		envconfig.Default{Key: "reconnect_backoff", Value: 5},
		envconfig.Default{Key: "max_reconnect", Value: 300},
		envconfig.Default{Key: "heartbeat_interval", Value: 30},
		envconfig.Default{Key: "metrics_interval", Value: 60},
		envconfig.Default{Key: "health_addr", Value: ":8081"},
		envconfig.Default{Key: "privilege_profile", Value: "admin"},
		envconfig.Default{Key: "audit_enabled", Value: false},
		envconfig.Default{Key: "audit_log_path", Value: ""},
		envconfig.Default{Key: "audit_checkpoint_path", Value: ""},
		envconfig.Default{Key: "audit_batch_size", Value: 100},
		envconfig.Default{Key: "audit_poll_interval", Value: 10},
		envconfig.Default{Key: "audit_delivery", Value: "tunnel"},
	)

	cfg := &AgentConfig{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("unmarshal agent config: %w", err)
	}

	if cfg.ServerURL == "" {
		return nil, fmt.Errorf("ASTRONOMER_SERVER_URL is required")
	}
	if cfg.ClusterID == "" {
		return nil, fmt.Errorf("ASTRONOMER_CLUSTER_ID is required")
	}
	if cfg.AgentToken == "" {
		return nil, fmt.Errorf("ASTRONOMER_AGENT_TOKEN is required")
	}

	return cfg, nil
}
