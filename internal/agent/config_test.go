package agent

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"testing"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
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
	if cfg.PrivilegeProfile != agenttemplate.PrivilegeProfileViewer {
		t.Errorf("PrivilegeProfile = %q, want %q", cfg.PrivilegeProfile, agenttemplate.PrivilegeProfileViewer)
	}
	if cfg.BootstrapTokenSecretName != "astronomer-agent-registration-token" {
		t.Errorf("BootstrapTokenSecretName = %q", cfg.BootstrapTokenSecretName)
	}
	if cfg.DurableTokenSecretName != "astronomer-agent-token" {
		t.Errorf("DurableTokenSecretName = %q", cfg.DurableTokenSecretName)
	}
	if cfg.BootstrapTokenSecretKey != "token" || cfg.DurableTokenSecretKey != "token" {
		t.Errorf("credential keys = bootstrap:%q durable:%q, want token", cfg.BootstrapTokenSecretKey, cfg.DurableTokenSecretKey)
	}
	if cfg.CredentialSource != credentialSourceEnvironment {
		t.Errorf("CredentialSource = %q, want %q", cfg.CredentialSource, credentialSourceEnvironment)
	}
}

func TestCredentialSourceDiagnosticNeverLogsMaterial(t *testing.T) {
	const marker = "bootstrap-sensitive-material-000001"
	t.Setenv("ASTRONOMER_SERVER_URL", "wss://example.com")
	t.Setenv("ASTRONOMER_CLUSTER_ID", "test-cluster")
	t.Setenv("ASTRONOMER_AGENT_TOKEN", marker)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	if _, err := LoadAgentConfigWithLogger(logger); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "credential_source=environment") {
		t.Fatalf("credential source diagnostic missing: %s", logs.String())
	}
	if strings.Contains(logs.String(), marker) {
		t.Fatalf("credential material leaked in startup diagnostic: %s", logs.String())
	}
}

func TestResolveConfiguredPrivilegeProfile(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		explicit bool
		want     string
		wantLog  string
	}{
		{name: "omitted", want: agenttemplate.PrivilegeProfileViewer, wantLog: "is unset"},
		{name: "empty explicit", explicit: true, want: agenttemplate.PrivilegeProfileViewer},
		{name: "whitespace explicit", raw: "  \t", explicit: true, want: agenttemplate.PrivilegeProfileViewer},
		{name: "unknown explicit", raw: "cluster-owner", explicit: true, want: agenttemplate.PrivilegeProfileViewer, wantLog: "failing closed"},
		{name: "viewer", raw: "viewer", explicit: true, want: agenttemplate.PrivilegeProfileViewer},
		{name: "operator", raw: "operator", explicit: true, want: agenttemplate.PrivilegeProfileOperator},
		{name: "namespace viewer", raw: "namespace_viewer", explicit: true, want: agenttemplate.PrivilegeProfileNamespaceViewer},
		{name: "namespace operator", raw: "namespace operator", explicit: true, want: agenttemplate.PrivilegeProfileNamespaceOperator},
		{name: "custom", raw: "custom", explicit: true, want: agenttemplate.PrivilegeProfileCustom},
		{name: "explicit admin", raw: " ADMIN ", explicit: true, want: agenttemplate.PrivilegeProfileAdmin},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			got := resolveConfiguredPrivilegeProfile(tt.raw, tt.explicit, logger)
			if got != tt.want {
				t.Fatalf("resolveConfiguredPrivilegeProfile(%q, %t) = %q, want %q", tt.raw, tt.explicit, got, tt.want)
			}
			if tt.wantLog == "" && logs.Len() != 0 {
				t.Fatalf("unexpected startup warning: %s", logs.String())
			}
			if tt.wantLog != "" && !strings.Contains(logs.String(), tt.wantLog) {
				t.Fatalf("startup warning = %q, want substring %q", logs.String(), tt.wantLog)
			}
		})
	}
}

func TestLoadAgentConfigNormalizesEffectivePrivilegeProfile(t *testing.T) {
	tests := map[string]string{
		"   ":                agenttemplate.PrivilegeProfileViewer,
		"not-a-profile":      agenttemplate.PrivilegeProfileViewer,
		"viewer":             agenttemplate.PrivilegeProfileViewer,
		"operator":           agenttemplate.PrivilegeProfileOperator,
		"namespace-viewer":   agenttemplate.PrivilegeProfileNamespaceViewer,
		"namespace-operator": agenttemplate.PrivilegeProfileNamespaceOperator,
		"custom":             agenttemplate.PrivilegeProfileCustom,
		"admin":              agenttemplate.PrivilegeProfileAdmin,
	}
	for raw, want := range tests {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("ASTRONOMER_SERVER_URL", "wss://example.com")
			t.Setenv("ASTRONOMER_CLUSTER_ID", "c1")
			t.Setenv("ASTRONOMER_AGENT_TOKEN", "tok")
			t.Setenv("ASTRONOMER_PRIVILEGE_PROFILE", raw)
			cfg, err := LoadAgentConfig()
			if err != nil {
				t.Fatalf("LoadAgentConfig: %v", err)
			}
			if cfg.PrivilegeProfile != want {
				t.Fatalf("PrivilegeProfile = %q, want %q", cfg.PrivilegeProfile, want)
			}
		})
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
	t.Setenv("ASTRONOMER_PRIVILEGE_PROFILE", "operator")
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
	if cfg.PrivilegeProfile != "operator" {
		t.Errorf("PrivilegeProfile = %q, want %q", cfg.PrivilegeProfile, "operator")
	}
	if cfg.DurableTokenSecretName != "custom-secret" {
		t.Errorf("DurableTokenSecretName = %q, want %q", cfg.DurableTokenSecretName, "custom-secret")
	}
	if cfg.DurableTokenSecretKey != "agent-token" {
		t.Errorf("DurableTokenSecretKey = %q, want %q", cfg.DurableTokenSecretKey, "agent-token")
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
			wantErr: "ASTRONOMER_AGENT_TOKEN is required when no durable agent token exists",
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
				_ = os.Unsetenv(key)
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

// TestLoadAgentConfig_RejectsPlaintextScheme proves the agent fails closed on a
// plaintext (ws:// / http://) ServerURL unless the ASTRONOMER_INSECURE escape
// hatch is set. https:// and wss:// are always accepted. FAILS without the
// scheme-validation change.
func TestLoadAgentConfig_RejectsPlaintextScheme(t *testing.T) {
	cases := []struct {
		name      string
		serverURL string
		insecure  string
		wantErr   bool
	}{
		{name: "ws rejected by default", serverURL: "ws://example.com", wantErr: true},
		{name: "http rejected by default", serverURL: "http://example.com", wantErr: true},
		{name: "wss always allowed", serverURL: "wss://example.com", wantErr: false},
		{name: "https always allowed", serverURL: "https://example.com", wantErr: false},
		{name: "ws allowed with ASTRONOMER_INSECURE", serverURL: "ws://example.com", insecure: "true", wantErr: false},
		{name: "ws still rejected when INSECURE not exactly true", serverURL: "ws://example.com", insecure: "yes", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ASTRONOMER_SERVER_URL", tc.serverURL)
			t.Setenv("ASTRONOMER_CLUSTER_ID", "c1")
			t.Setenv("ASTRONOMER_AGENT_TOKEN", "tok")
			if tc.insecure != "" {
				t.Setenv("ASTRONOMER_INSECURE", tc.insecure)
			} else {
				_ = os.Unsetenv("ASTRONOMER_INSECURE")
			}

			_, err := LoadAgentConfig()
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for %q (insecure=%q), got nil", tc.serverURL, tc.insecure)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for %q (insecure=%q): %v", tc.serverURL, tc.insecure, err)
			}
		})
	}
}

// TestLoadAgentConfig_CAFromEnv verifies ASTRONOMER_CA_CERT / _CHECKSUM are
// loaded into the config, and that the default (unset) path leaves them empty.
func TestLoadAgentConfig_CAFromEnv(t *testing.T) {
	t.Setenv("ASTRONOMER_SERVER_URL", "wss://example.com")
	t.Setenv("ASTRONOMER_CLUSTER_ID", "c1")
	t.Setenv("ASTRONOMER_AGENT_TOKEN", "tok")
	t.Setenv("ASTRONOMER_CA_CERT", "-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----")
	t.Setenv("ASTRONOMER_CA_CHECKSUM", "DEADBEEF")

	cfg, err := LoadAgentConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.CACert == "" {
		t.Error("CACert should be populated from ASTRONOMER_CA_CERT")
	}
	if cfg.CAChecksum != "DEADBEEF" {
		t.Errorf("CAChecksum = %q, want DEADBEEF", cfg.CAChecksum)
	}
}

func TestLoadAgentConfig_CADefaultsEmpty(t *testing.T) {
	for _, k := range []string{"ASTRONOMER_CA_CERT", "ASTRONOMER_CA_CHECKSUM"} {
		_ = os.Unsetenv(k)
	}
	t.Setenv("ASTRONOMER_SERVER_URL", "wss://example.com")
	t.Setenv("ASTRONOMER_CLUSTER_ID", "c1")
	t.Setenv("ASTRONOMER_AGENT_TOKEN", "tok")

	cfg, err := LoadAgentConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The mounted file path does not exist in the test env, so both stay empty —
	// the default OS-trust path.
	if cfg.CACert != "" || cfg.CAChecksum != "" {
		t.Fatalf("default CA config should be empty, got cert=%q checksum=%q", cfg.CACert, cfg.CAChecksum)
	}
}
