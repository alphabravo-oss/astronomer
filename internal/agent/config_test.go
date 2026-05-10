package agent

import (
	"os"
	"testing"
)

func TestLoadAgentConfig_Defaults(t *testing.T) {
	// Set required env vars.
	t.Setenv("ASTRONOMER_SERVER_URL", "wss://example.com")
	t.Setenv("ASTRONOMER_CLUSTER_ID", "test-cluster-123")
	t.Setenv("ASTRONOMER_AGENT_TOKEN", "test-token-abc")

	cfg, err := LoadAgentConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ServerURL != "wss://example.com" {
		t.Errorf("ServerURL = %q, want %q", cfg.ServerURL, "wss://example.com")
	}
	if cfg.ClusterID != "test-cluster-123" {
		t.Errorf("ClusterID = %q, want %q", cfg.ClusterID, "test-cluster-123")
	}
	if cfg.AgentToken != "test-token-abc" {
		t.Errorf("AgentToken = %q, want %q", cfg.AgentToken, "test-token-abc")
	}
	if cfg.ReconnectBackoff != 5 {
		t.Errorf("ReconnectBackoff = %d, want 5", cfg.ReconnectBackoff)
	}
	if cfg.MaxReconnect != 300 {
		t.Errorf("MaxReconnect = %d, want 300", cfg.MaxReconnect)
	}
	if cfg.HeartbeatInterval != 30 {
		t.Errorf("HeartbeatInterval = %d, want 30", cfg.HeartbeatInterval)
	}
	if cfg.MetricsInterval != 60 {
		t.Errorf("MetricsInterval = %d, want 60", cfg.MetricsInterval)
	}
	if cfg.HealthAddr != ":8081" {
		t.Errorf("HealthAddr = %q, want %q", cfg.HealthAddr, ":8081")
	}
	if cfg.TokenSecretName != "astronomer-agent-token" {
		t.Errorf("TokenSecretName = %q, want %q", cfg.TokenSecretName, "astronomer-agent-token")
	}
	if cfg.TokenSecretKey != "token" {
		t.Errorf("TokenSecretKey = %q, want %q", cfg.TokenSecretKey, "token")
	}
}

func TestLoadAgentConfig_CustomValues(t *testing.T) {
	t.Setenv("ASTRONOMER_SERVER_URL", "wss://custom.example.com")
	t.Setenv("ASTRONOMER_CLUSTER_ID", "custom-cluster")
	t.Setenv("ASTRONOMER_AGENT_TOKEN", "custom-token")
	t.Setenv("ASTRONOMER_AGENT_ID", "agent-42")
	t.Setenv("ASTRONOMER_RECONNECT_BACKOFF", "10")
	t.Setenv("ASTRONOMER_MAX_RECONNECT", "600")
	t.Setenv("ASTRONOMER_HEARTBEAT_INTERVAL", "15")
	t.Setenv("ASTRONOMER_METRICS_INTERVAL", "120")
	t.Setenv("ASTRONOMER_HEALTH_ADDR", ":9090")
	t.Setenv("ASTRONOMER_TOKEN_SECRET_NAME", "custom-secret")
	t.Setenv("ASTRONOMER_TOKEN_SECRET_KEY", "agent-token")

	cfg, err := LoadAgentConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.AgentID != "agent-42" {
		t.Errorf("AgentID = %q, want %q", cfg.AgentID, "agent-42")
	}
	if cfg.ReconnectBackoff != 10 {
		t.Errorf("ReconnectBackoff = %d, want 10", cfg.ReconnectBackoff)
	}
	if cfg.MaxReconnect != 600 {
		t.Errorf("MaxReconnect = %d, want 600", cfg.MaxReconnect)
	}
	if cfg.HeartbeatInterval != 15 {
		t.Errorf("HeartbeatInterval = %d, want 15", cfg.HeartbeatInterval)
	}
	if cfg.MetricsInterval != 120 {
		t.Errorf("MetricsInterval = %d, want 120", cfg.MetricsInterval)
	}
	if cfg.HealthAddr != ":9090" {
		t.Errorf("HealthAddr = %q, want %q", cfg.HealthAddr, ":9090")
	}
	if cfg.TokenSecretName != "custom-secret" {
		t.Errorf("TokenSecretName = %q, want %q", cfg.TokenSecretName, "custom-secret")
	}
	if cfg.TokenSecretKey != "agent-token" {
		t.Errorf("TokenSecretKey = %q, want %q", cfg.TokenSecretKey, "agent-token")
	}
}

func TestLoadAgentConfig_MissingRequired(t *testing.T) {
	tests := []struct {
		name    string
		envVars map[string]string
		wantErr string
	}{
		{
			name:    "missing server_url",
			envVars: map[string]string{},
			wantErr: "ASTRONOMER_SERVER_URL is required",
		},
		{
			name: "missing cluster_id",
			envVars: map[string]string{
				"ASTRONOMER_SERVER_URL": "wss://example.com",
			},
			wantErr: "ASTRONOMER_CLUSTER_ID is required",
		},
		{
			name: "missing agent_token",
			envVars: map[string]string{
				"ASTRONOMER_SERVER_URL": "wss://example.com",
				"ASTRONOMER_CLUSTER_ID": "test-cluster",
			},
			wantErr: "ASTRONOMER_AGENT_TOKEN is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear all ASTRONOMER_ env vars.
			for _, key := range []string{
				"ASTRONOMER_SERVER_URL",
				"ASTRONOMER_CLUSTER_ID",
				"ASTRONOMER_AGENT_TOKEN",
				"ASTRONOMER_AGENT_ID",
			} {
				os.Unsetenv(key)
			}

			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			_, err := LoadAgentConfig()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if err.Error() != tt.wantErr {
				t.Errorf("error = %q, want %q", err.Error(), tt.wantErr)
			}
		})
	}
}
