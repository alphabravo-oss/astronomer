package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/alphabravocompany/astronomer-go/internal/envconfig"
)

// caCertMountPath is where the install manifest mounts the CA Secret
// (deploy/agent/install.yaml.template volumeMount). LoadAgentConfig reads the
// PEM bundle from here when ASTRONOMER_CA_CERT is not set in the environment.
const caCertMountPath = "/etc/astronomer/tls/ca.crt"

// AgentConfig holds configuration for the agent process.
type AgentConfig struct {
	ServerURL  string `mapstructure:"server_url"`  // WebSocket server URL
	ClusterID  string `mapstructure:"cluster_id"`  // Cluster UUID
	AgentToken string `mapstructure:"agent_token"` // Selected runtime credential; env input is bootstrap only
	AgentID    string `mapstructure:"agent_id"`    // Unique agent instance ID

	BootstrapTokenSecretName string `mapstructure:"bootstrap_token_secret_name"` // Installer-owned bootstrap Secret
	BootstrapTokenSecretKey  string `mapstructure:"bootstrap_token_secret_key"`  // Bootstrap token key
	IdentityTokenSecretName  string `mapstructure:"identity_token_secret_name"`  // Active durable identity container
	IdentityTokenSecretKey   string `mapstructure:"identity_token_secret_key"`   // Agent-owned token field
	LegacyTokenSecretName    string `mapstructure:"legacy_token_secret_name"`    // Pre-AGENT-02 durable Secret
	LegacyTokenSecretKey     string `mapstructure:"legacy_token_secret_key"`     // Legacy durable token key
	// IdentityLayoutConfigured records whether the Deployment explicitly carries
	// the AGENT-02 identity-layout marker. It is captured before Viper defaults
	// are applied so an image-only self-upgrade can be distinguished from the
	// current manifest. It never changes in-process.
	IdentityLayoutConfigured bool `mapstructure:"-"`
	// LegacyLayoutConfigured is true only when the pre-AGENT-02 token env is
	// explicitly present and the identity-layout marker is absent. It permits
	// the narrowly scoped image-first compatibility path.
	LegacyLayoutConfigured bool `mapstructure:"-"`
	// TokenSecretName/Key preserve the pre-AGENT-02 environment override. When
	// explicitly set they override the legacy fields; active identity always uses
	// the fixed, separately-owned identity fields.
	TokenSecretName string `mapstructure:"token_secret_name"`
	TokenSecretKey  string `mapstructure:"token_secret_key"`
	// CredentialSource is diagnostic state only (durable_identity,
	// legacy_durable_secret, bootstrap_secret, or environment). It never contains
	// credential material.
	CredentialSource string `mapstructure:"-"`

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
	AuditEnabled        bool   `mapstructure:"audit_enabled"`         // Enable the apiserver-audit tailer (default false)
	AuditLogPath        string `mapstructure:"audit_log_path"`        // Path to the kube-apiserver audit log file
	AuditCheckpointPath string `mapstructure:"audit_checkpoint_path"` // Path where the tail offset is persisted (default audit_log_path + ".checkpoint")
	AuditBatchSize      int    `mapstructure:"audit_batch_size"`      // Max events per forwarded batch (default 100)
	AuditPollInterval   int    `mapstructure:"audit_poll_interval"`   // Seconds between tail polls (default 10)
	AuditDelivery       string `mapstructure:"audit_delivery"`        // How batches are delivered: tunnel (default) | http | stub

	// Fleet-style PULL reconcile (sprint: pull-reconcile). When disabled (the
	// default) the agent does NOT start its local reconcile loop and v0.1.0
	// behavior is unchanged. When enabled the agent periodically (and on a
	// tunnel push) pulls its desired state and server-side-applies it into the
	// astronomer-* owned namespaces. Env: ASTRONOMER_PULL_RECONCILE_ENABLED,
	// ASTRONOMER_PULL_RECONCILE_INTERVAL.
	PullReconcileEnabled  bool `mapstructure:"pull_reconcile_enabled"`  // default false
	PullReconcileInterval int  `mapstructure:"pull_reconcile_interval"` // seconds, default 300

	// Server-CA pinning on the agent tunnel (Rancher CATTLE_CA_CHECKSUM
	// semantics). Both are empty by default, in which case the tunnel dialer
	// uses the OS trust store with standard verification (no behavior change).
	//   - CACert: PEM-encoded CA bundle. Loaded from env ASTRONOMER_CA_CERT or,
	//     when that is unset, from the mounted file /etc/astronomer/tls/ca.crt.
	//   - CAChecksum: hex SHA-256 of the server CA, pinned in VerifyConnection.
	CACert     string `mapstructure:"ca_cert"`
	CAChecksum string `mapstructure:"ca_checksum"`
}

// LoadAgentConfig reads agent configuration from environment variables with
// sensible defaults. Environment variables are prefixed with ASTRONOMER_,
// e.g. ASTRONOMER_SERVER_URL, ASTRONOMER_CLUSTER_ID.
func LoadAgentConfig() (*AgentConfig, error) {
	return LoadAgentConfigWithLogger(slog.Default())
}

// LoadAgentConfigWithLogger emits source-only credential diagnostics through
// the process logger. It never attaches token or Secret data to a log record.
func LoadAgentConfigWithLogger(log *slog.Logger) (*AgentConfig, error) {
	if log == nil {
		log = slog.Default()
	}
	_, privilegeProfileExplicit := os.LookupEnv("ASTRONOMER_PRIVILEGE_PROFILE")
	_, identityLayoutConfigured := os.LookupEnv("ASTRONOMER_IDENTITY_TOKEN_SECRET_NAME")
	_, legacyTokenEnvironmentConfigured := os.LookupEnv("ASTRONOMER_AGENT_TOKEN")
	v := envconfig.NewViper("ASTRONOMER")
	envconfig.SetDefaults(v,
		envconfig.Default{Key: "server_url", Value: ""},
		envconfig.Default{Key: "cluster_id", Value: ""},
		envconfig.Default{Key: "agent_token", Value: ""},
		envconfig.Default{Key: "agent_id", Value: ""},
		envconfig.Default{Key: "bootstrap_token_secret_name", Value: "astronomer-agent-registration-token"},
		envconfig.Default{Key: "bootstrap_token_secret_key", Value: "token"},
		envconfig.Default{Key: "identity_token_secret_name", Value: "astronomer-agent-identity"},
		envconfig.Default{Key: "identity_token_secret_key", Value: "token"},
		envconfig.Default{Key: "legacy_token_secret_name", Value: "astronomer-agent-token"},
		envconfig.Default{Key: "legacy_token_secret_key", Value: "token"},
		envconfig.Default{Key: "token_secret_name", Value: ""},
		envconfig.Default{Key: "token_secret_key", Value: ""},
		envconfig.Default{Key: "reconnect_backoff", Value: 5},
		envconfig.Default{Key: "max_reconnect", Value: 300},
		envconfig.Default{Key: "heartbeat_interval", Value: 30},
		envconfig.Default{Key: "metrics_interval", Value: 60},
		envconfig.Default{Key: "health_addr", Value: ":8081"},
		envconfig.Default{Key: "privilege_profile", Value: agenttemplate.PrivilegeProfileViewer},
		envconfig.Default{Key: "audit_enabled", Value: false},
		envconfig.Default{Key: "audit_log_path", Value: ""},
		envconfig.Default{Key: "audit_checkpoint_path", Value: ""},
		envconfig.Default{Key: "audit_batch_size", Value: 100},
		envconfig.Default{Key: "audit_poll_interval", Value: 10},
		envconfig.Default{Key: "audit_delivery", Value: "tunnel"},
		envconfig.Default{Key: "pull_reconcile_enabled", Value: false},
		envconfig.Default{Key: "pull_reconcile_interval", Value: 300},
		envconfig.Default{Key: "ca_cert", Value: ""},
		envconfig.Default{Key: "ca_checksum", Value: ""},
	)

	cfg := &AgentConfig{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("unmarshal agent config: %w", err)
	}
	cfg.IdentityLayoutConfigured = identityLayoutConfigured
	cfg.LegacyLayoutConfigured = !identityLayoutConfigured && legacyTokenEnvironmentConfigured

	if cfg.ServerURL == "" {
		return nil, fmt.Errorf("ASTRONOMER_SERVER_URL is required")
	}
	if cfg.ClusterID == "" {
		return nil, fmt.Errorf("ASTRONOMER_CLUSTER_ID is required")
	}
	if cfg.TokenSecretName != "" {
		cfg.LegacyTokenSecretName = cfg.TokenSecretName
	}
	if cfg.TokenSecretKey != "" {
		cfg.LegacyTokenSecretKey = cfg.TokenSecretKey
	}
	cfg.BootstrapTokenSecretName = strings.TrimSpace(cfg.BootstrapTokenSecretName)
	cfg.BootstrapTokenSecretKey = strings.TrimSpace(cfg.BootstrapTokenSecretKey)
	cfg.IdentityTokenSecretName = strings.TrimSpace(cfg.IdentityTokenSecretName)
	cfg.IdentityTokenSecretKey = strings.TrimSpace(cfg.IdentityTokenSecretKey)
	cfg.LegacyTokenSecretName = strings.TrimSpace(cfg.LegacyTokenSecretName)
	cfg.LegacyTokenSecretKey = strings.TrimSpace(cfg.LegacyTokenSecretKey)
	if cfg.BootstrapTokenSecretName == "" || cfg.BootstrapTokenSecretKey == "" ||
		cfg.IdentityTokenSecretName == "" || cfg.IdentityTokenSecretKey == "" ||
		cfg.LegacyTokenSecretName == "" || cfg.LegacyTokenSecretKey == "" {
		return nil, fmt.Errorf("bootstrap, identity, and legacy agent token Secret names and keys are required")
	}
	credentialCtx, cancel := context.WithTimeout(context.Background(), credentialReadTimeout)
	defer cancel()
	if err := resolveStartupCredential(credentialCtx, cfg, log); err != nil {
		return nil, err
	}
	if cfg.AgentToken == "" {
		return nil, fmt.Errorf("agent credential is required; off-cluster compatibility may set ASTRONOMER_AGENT_TOKEN")
	}
	log.Info("agent credential selected", "credential_source", cfg.CredentialSource)
	cfg.PrivilegeProfile = resolveConfiguredPrivilegeProfile(cfg.PrivilegeProfile, privilegeProfileExplicit, log)

	// Reject plaintext tunnels by default. Only https:// and wss:// are allowed
	// transports; ws:// and http:// expose the agent token and proxied traffic
	// in cleartext. The ASTRONOMER_INSECURE=true escape hatch is for local dev
	// only and logs loudly so it can never be a silent production default.
	if !strings.HasPrefix(cfg.ServerURL, "https://") && !strings.HasPrefix(cfg.ServerURL, "wss://") {
		if os.Getenv("ASTRONOMER_INSECURE") != "true" {
			return nil, fmt.Errorf("ASTRONOMER_SERVER_URL must use https:// or wss://; got %q (set ASTRONOMER_INSECURE=true to override)", cfg.ServerURL)
		}
		log.Warn("INSECURE: plaintext ServerURL allowed only because ASTRONOMER_INSECURE=true; the agent token and tunnel traffic are unencrypted", "server_url", cfg.ServerURL)
	}

	// CA bundle priority: explicit env wins; otherwise fall back to the mounted
	// Secret file. Empty when neither is present (default OS-trust path).
	cfg.CACert = strings.TrimSpace(cfg.CACert)
	if cfg.CACert == "" {
		if b, err := os.ReadFile(caCertMountPath); err == nil {
			cfg.CACert = strings.TrimSpace(string(b))
		}
	}
	cfg.CAChecksum = strings.TrimSpace(cfg.CAChecksum)

	return cfg, nil
}

// resolveConfiguredPrivilegeProfile applies the same fail-closed profile
// semantics as install-manifest RBAC rendering. The explicit bit is kept
// separate from the value because Viper supplies the safe viewer default when
// the environment variable is absent; operators upgrading an old manifest
// still need a visible warning that implicit admin is no longer supported.
func resolveConfiguredPrivilegeProfile(raw string, explicitlyConfigured bool, log *slog.Logger) string {
	profile := agenttemplate.NormalizePrivilegeProfile(raw)
	if log == nil {
		log = slog.Default()
	}
	if !explicitlyConfigured {
		log.Warn(
			"ASTRONOMER_PRIVILEGE_PROFILE is unset; using least-privilege viewer (set admin explicitly before upgrading only if full-management access is intentionally required)",
			"effective_privilege_profile", profile,
		)
		return profile
	}

	trimmed := strings.TrimSpace(raw)
	if trimmed != "" && profile == agenttemplate.PrivilegeProfileViewer && !strings.EqualFold(trimmed, agenttemplate.PrivilegeProfileViewer) {
		log.Warn(
			"unrecognized ASTRONOMER_PRIVILEGE_PROFILE; failing closed to viewer",
			"configured_privilege_profile", trimmed,
			"effective_privilege_profile", profile,
		)
	}
	return profile
}
