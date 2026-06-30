package config

import (
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/envconfig"
)

// Config holds all configuration for the application.
type Config struct {
	DatabaseURL     string `mapstructure:"database_url"`
	RedisURL        string `mapstructure:"redis_url"`
	CeleryBrokerURL string `mapstructure:"celery_broker_url"`

	// pgxpool sizing — operator-tunable via the chart's `database.*`
	// values. Zero values fall through to the
	// defaults in internal/db/db.go so existing installs see no change.
	DBMaxConns             int32 `mapstructure:"db_max_conns"`
	DBMinConns             int32 `mapstructure:"db_min_conns"`
	DBMaxConnLifetimeMin   int   `mapstructure:"db_max_conn_lifetime_minutes"`
	DBMaxConnIdleMin       int   `mapstructure:"db_max_conn_idle_minutes"`
	DBHealthCheckPeriodSec int   `mapstructure:"db_health_check_period_seconds"`

	SecretKey string `mapstructure:"secret_key"`
	Env       string `mapstructure:"env"`
	Debug     bool   `mapstructure:"debug"`

	CORSAllowedOrigins    string `mapstructure:"cors_allowed_origins"`
	SessionTimeoutMinutes int    `mapstructure:"session_timeout_minutes"`
	AgentTokenExpiryHours int    `mapstructure:"agent_token_expiry_hours"`
	// RegistrationTokenTTLHours (task A3) is the single, documented TTL applied
	// to every operator-facing registration-token mint path (POST /register/,
	// GetManifest, the signed-manifest mint, and the worker reissue). Default 1h
	// keeps the join blast-radius tight. Note: the in-process localcluster token
	// (internal/server/localcluster.go, 30d) is a deliberate exception — it
	// never leaves the pod and is not operator-facing.
	RegistrationTokenTTLHours int `mapstructure:"registration_token_ttl_hours"`

	// ManifestSigningSecret keys the HMAC over (cluster_id, expiry) that
	// gates the short-TTL signed manifest-download URL
	// (GET /api/v1/register/signed/{cluster_id}). Empty falls back to
	// SecretKey at wiring time so a single-secret install still works.
	ManifestSigningSecret string `mapstructure:"manifest_signing_secret"`

	// ServerURL is the externally-reachable URL of this Astronomer install
	// (e.g. http://astronomer.example.com:8080). It seeds
	// platform_configuration.server_url on first boot, which the local
	// ArgoCD self-management loop reads to discover what hostname to put on
	// the self-manage HTTPRoute. Optional — if empty, the operator can set
	// it later from /dashboard/settings.
	ServerURL string `mapstructure:"server_url"`

	EncryptionKey string `mapstructure:"astronomer_encryption_key"`

	GithubClientID     string `mapstructure:"github_client_id"`
	GithubClientSecret string `mapstructure:"github_client_secret"`

	GoogleClientID     string `mapstructure:"google_client_id"`
	GoogleClientSecret string `mapstructure:"google_client_secret"`

	OIDCIssuer       string `mapstructure:"oidc_issuer"`
	OIDCClientID     string `mapstructure:"oidc_client_id"`
	OIDCClientSecret string `mapstructure:"oidc_client_secret"`

	AgentImageRepository string `mapstructure:"agent_image_repository"`
	AgentImageTag        string `mapstructure:"agent_image_tag"`

	LogLevel string `mapstructure:"log_level"`

	AuditLogRetentionMonths int    `mapstructure:"audit_log_retention_months"`
	ServerMetricsAddr       string `mapstructure:"server_metrics_addr"`
	WorkerMetricsAddr       string `mapstructure:"worker_metrics_addr"`

	// Account-lockout policy (migration 039 / NIST 800-53 AC-7).
	// LoginFailureThreshold defaults to 5 when zero/negative; the
	// duration defaults to 15 minutes. Both are chart-tunable via
	// LOGIN_FAILURE_THRESHOLD / LOCKOUT_DURATION_MINUTES env vars.
	LoginFailureThreshold  int `mapstructure:"login_failure_threshold"`
	LockoutDurationMinutes int `mapstructure:"lockout_duration_minutes"`

	// 2FA policy (migration 043). Issuer is the brand string shown
	// inside the user's authenticator app (e.g. "Astronomer"). Require
	// flips the chart-tunable "every local-password user must enroll"
	// switch — when true, login refuses to hand back a session for a
	// not-yet-enrolled user; instead a short-lived
	// PurposeTOTPEnrollOnly challenge is returned and the SPA drives
	// the QR flow before retrying.
	TOTPIssuer  string `mapstructure:"totp_issuer"`
	TOTPRequire bool   `mapstructure:"totp_require"`

	// In-browser kubectl shell (migration 065 / sprint 17). Default
	// false — operators flip this on per-install once their audit-log
	// retention is sized for the kubectl_session_commands rows. When
	// disabled the handler is not wired and the routes return 404.
	KubectlShellEnabled             bool   `mapstructure:"kubectl_shell_enabled"`
	KubectlShellImage               string `mapstructure:"kubectl_shell_image"`
	KubectlShellIdleTimeoutMinutes  int    `mapstructure:"kubectl_shell_idle_timeout_minutes"`
	KubectlShellSessionHardCapHours int    `mapstructure:"kubectl_shell_session_hard_cap_hours"`

	// ArgoCDUIUpstream is the in-cluster URL of argocd-server. The /argocd/*
	// reverse proxy mounted on the public Astronomer router forwards browser
	// traffic here after Astronomer's JWT/cookie auth has cleared. ArgoCD
	// must be deployed with `server.rootpath: /argocd` so its SPA emits
	// asset and API URLs under that prefix.
	ArgoCDUIUpstream string `mapstructure:"argocd_ui_upstream"`

	// ArgoCDClusterProxyBaseURL is the base URL upstream ArgoCD should use
	// when talking to Astronomer-managed remote clusters through the tunnel
	// proxy. The registration handler appends /api/v1/clusters/{id}/k8s.
	ArgoCDClusterProxyBaseURL string `mapstructure:"argocd_cluster_proxy_base_url"`

	// ArgoCDInternalProxyAddr is a dedicated, non-public listen address serving
	// ONLY the ArgoCD->adopted-cluster k8s proxy. ArgoCD's GitOps apply path
	// sends no per-request credential (kubectl treats discovery/apply as
	// anonymous), so this route cannot be token-gated. Instead it runs on its
	// own port that the public ingress never maps and a NetworkPolicy restricts
	// to the argocd namespace — network isolation IS the authentication. The
	// public :8000 listener keeps the token-gated route for any other caller.
	ArgoCDInternalProxyAddr string `mapstructure:"argocd_internal_proxy_addr"`

	// DexBundledEnabled mirrors the chart's dex.enabled runtime switch.
	// AuthLocalPasswordOnly is the production acknowledgement required when no
	// bundled Dex is deployed.
	DexBundledEnabled     bool `mapstructure:"dex_bundled_enabled"`
	AuthLocalPasswordOnly bool `mapstructure:"auth_local_password_only"`

	// CatalogURL points at the astronomer-catalog repo's catalog.yaml (raw
	// HTTPS). On boot the server fetches it and reconciles the platform-default
	// helm_repositories + catalog_blessed_charts overlays. Empty = skip (keep
	// whatever defaults are already seeded). Fetch failures are non-fatal.
	CatalogURL string `mapstructure:"astronomer_catalog_url"`

	// PullReconcileEnabled gates the Fleet-style PULL reconcile subsystem. When
	// false (the default) NOTHING changes: the existing tunnel self-upgrade +
	// Argo-baseline push paths keep owning the footprint. When true the agent
	// runs its local reconcile loop and owns the astronomer-* footprint via
	// pull; the server must not double-manage the same footprint via Argo push.
	// The DesiredState responder is read-only rendering and is unaffected by
	// this flag — only behavior that would mutate ownership is gated.
	PullReconcileEnabled bool `mapstructure:"pull_reconcile_enabled"`
}

// CORSOrigins returns the allowed origins as a slice.
func (c *Config) CORSOrigins() []string {
	return strings.Split(c.CORSAllowedOrigins, ",")
}

// Load reads configuration from environment variables with sensible defaults.
func Load() (*Config, error) {
	v := envconfig.NewViper("")

	// Bind env vars for secret/optional fields without defaults so AutomaticEnv resolves them.
	if err := envconfig.BindEnv(v,
		"astronomer_encryption_key",
		"github_client_id",
		"github_client_secret",
		"google_client_id",
		"google_client_secret",
		"oidc_issuer",
		"oidc_client_id",
		"oidc_client_secret",
		"agent_image_repository",
		"agent_image_tag",
		"database_url",
		"redis_url",
		"secret_key",
		"server_url",
		"audit_log_retention_months",
		"server_metrics_addr",
		"worker_metrics_addr",
		"login_failure_threshold",
		"lockout_duration_minutes",
		"totp_issuer",
		"totp_require",
		"db_max_conns",
		"db_min_conns",
		"db_max_conn_lifetime_minutes",
		"db_max_conn_idle_minutes",
		"db_health_check_period_seconds",
		"kubectl_shell_enabled",
		"kubectl_shell_image",
		"kubectl_shell_idle_timeout_minutes",
		"kubectl_shell_session_hard_cap_hours",
		"argocd_ui_upstream",
		"argocd_cluster_proxy_base_url",
		"argocd_internal_proxy_addr",
		"manifest_signing_secret",
		"dex_bundled_enabled",
		"auth_local_password_only",
		"astronomer_catalog_url",
		"pull_reconcile_enabled",
	); err != nil {
		return nil, err
	}

	envconfig.SetDefaults(v,
		envconfig.Default{Key: "database_url", Value: "postgres://astronomer:astronomer@localhost:5432/astronomer?sslmode=disable"},
		envconfig.Default{Key: "redis_url", Value: "redis://localhost:6379/0"},
		envconfig.Default{Key: "celery_broker_url", Value: "redis://localhost:6379/1"},
		envconfig.Default{Key: "env", Value: "development"},
		envconfig.Default{Key: "debug", Value: false},
		envconfig.Default{Key: "cors_allowed_origins", Value: "http://localhost:3000"},
		envconfig.Default{Key: "session_timeout_minutes", Value: 60},
		envconfig.Default{Key: "agent_token_expiry_hours", Value: 24},
		envconfig.Default{Key: "registration_token_ttl_hours", Value: 1},
		envconfig.Default{Key: "log_level", Value: "info"},
		envconfig.Default{Key: "audit_log_retention_months", Value: 13},
		envconfig.Default{Key: "login_failure_threshold", Value: 5},
		envconfig.Default{Key: "lockout_duration_minutes", Value: 15},
		envconfig.Default{Key: "totp_issuer", Value: "Astronomer"},
		envconfig.Default{Key: "totp_require", Value: false},
		envconfig.Default{Key: "kubectl_shell_enabled", Value: false},
		envconfig.Default{Key: "kubectl_shell_image", Value: "astronomer-shell:dev"},
		envconfig.Default{Key: "kubectl_shell_idle_timeout_minutes", Value: 30},
		envconfig.Default{Key: "kubectl_shell_session_hard_cap_hours", Value: 4},
		envconfig.Default{Key: "server_metrics_addr", Value: ":9090"},
		envconfig.Default{Key: "worker_metrics_addr", Value: ":9090"},
		envconfig.Default{Key: "argocd_ui_upstream", Value: "http://astro-argocd-server.astronomer.svc.cluster.local:80"},
		// Adopted clusters register against the dedicated internal proxy port
		// (network-isolated, tokenless) — not the public :8000 listener.
		envconfig.Default{Key: "argocd_cluster_proxy_base_url", Value: "http://astronomer-server.astronomer.svc.cluster.local:8090"},
		envconfig.Default{Key: "argocd_internal_proxy_addr", Value: ":8090"},
		envconfig.Default{Key: "dex_bundled_enabled", Value: false},
		envconfig.Default{Key: "auth_local_password_only", Value: false},
		// Fleet-style PULL reconcile is OFF by default — opt-in per install.
		envconfig.Default{Key: "pull_reconcile_enabled", Value: false},
	)

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
