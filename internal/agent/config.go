package agent

import (
	"fmt"

	"github.com/spf13/viper"
)

// AgentConfig holds configuration for the agent process.
type AgentConfig struct {
	ServerURL         string `mapstructure:"server_url"`         // WebSocket server URL
	ClusterID         string `mapstructure:"cluster_id"`         // Cluster UUID
	AgentToken        string `mapstructure:"agent_token"`        // Registration token
	AgentID           string `mapstructure:"agent_id"`           // Unique agent instance ID
	ReconnectBackoff  int    `mapstructure:"reconnect_backoff"`  // Base backoff seconds (default 5)
	MaxReconnect      int    `mapstructure:"max_reconnect"`      // Max backoff seconds (default 300)
	HeartbeatInterval int    `mapstructure:"heartbeat_interval"` // Seconds (default 30)
	MetricsInterval   int    `mapstructure:"metrics_interval"`   // Seconds (default 60)
	HealthAddr        string `mapstructure:"health_addr"`        // Health server address (default :8081)
}

// LoadAgentConfig reads agent configuration from environment variables with
// sensible defaults. Environment variables are prefixed with ASTRONOMER_,
// e.g. ASTRONOMER_SERVER_URL, ASTRONOMER_CLUSTER_ID.
func LoadAgentConfig() (*AgentConfig, error) {
	v := viper.New()
	v.SetEnvPrefix("ASTRONOMER")
	v.AutomaticEnv()

	v.SetDefault("server_url", "")
	v.SetDefault("cluster_id", "")
	v.SetDefault("agent_token", "")
	v.SetDefault("agent_id", "")
	v.SetDefault("reconnect_backoff", 5)
	v.SetDefault("max_reconnect", 300)
	v.SetDefault("heartbeat_interval", 30)
	v.SetDefault("metrics_interval", 60)
	v.SetDefault("health_addr", ":8081")

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
