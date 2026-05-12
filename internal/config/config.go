package config

import (
	"strings"

	"github.com/spf13/viper"
)

// Config holds all configuration for the application.
type Config struct {
	DatabaseURL     string `mapstructure:"database_url"`
	RedisURL        string `mapstructure:"redis_url"`
	CeleryBrokerURL string `mapstructure:"celery_broker_url"`

	// pgxpool sizing — operator-tunable via the chart's `database.*`
	// values. Zero values fall through to the
	// defaults in internal/db/db.go so existing installs see no change.
	DBMaxConns        int32 `mapstructure:"db_max_conns"`
	DBMinConns        int32 `mapstructure:"db_min_conns"`
	DBMaxConnLifetimeMin int `mapstructure:"db_max_conn_lifetime_minutes"`
	DBMaxConnIdleMin     int `mapstructure:"db_max_conn_idle_minutes"`
	DBHealthCheckPeriodSec int `mapstructure:"db_health_check_period_seconds"`

	SecretKey string `mapstructure:"secret_key"`
	Env       string `mapstructure:"env"`
	Debug     bool   `mapstructure:"debug"`

	CORSAllowedOrigins    string `mapstructure:"cors_allowed_origins"`
	SessionTimeoutMinutes int    `mapstructure:"session_timeout_minutes"`
	AgentTokenExpiryHours int    `mapstructure:"agent_token_expiry_hours"`

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

	AuditLogRetentionMonths int `mapstructure:"audit_log_retention_months"`
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
}

// CORSOrigins returns the allowed origins as a slice.
func (c *Config) CORSOrigins() []string {
	return strings.Split(c.CORSAllowedOrigins, ",")
}

// Load reads configuration from environment variables with sensible defaults.
func Load() (*Config, error) {
	v := viper.New()
	v.AutomaticEnv()

	// Bind env vars for secret/optional fields without defaults so AutomaticEnv resolves them.
	v.BindEnv("astronomer_encryption_key")
	v.BindEnv("github_client_id")
	v.BindEnv("github_client_secret")
	v.BindEnv("google_client_id")
	v.BindEnv("google_client_secret")
	v.BindEnv("oidc_issuer")
	v.BindEnv("oidc_client_id")
	v.BindEnv("oidc_client_secret")
	v.BindEnv("agent_image_repository")
	v.BindEnv("agent_image_tag")
	v.BindEnv("database_url")
	v.BindEnv("redis_url")
	v.BindEnv("secret_key")
	v.BindEnv("server_url")
	v.BindEnv("audit_log_retention_months")
	v.BindEnv("server_metrics_addr")
	v.BindEnv("worker_metrics_addr")
	v.BindEnv("login_failure_threshold")
	v.BindEnv("lockout_duration_minutes")
	v.BindEnv("totp_issuer")
	v.BindEnv("totp_require")
	v.BindEnv("db_max_conns")
	v.BindEnv("db_min_conns")
	v.BindEnv("db_max_conn_lifetime_minutes")
	v.BindEnv("db_max_conn_idle_minutes")
	v.BindEnv("db_health_check_period_seconds")

	v.SetDefault("database_url", "postgres://astronomer:astronomer@localhost:5432/astronomer?sslmode=disable")
	v.SetDefault("redis_url", "redis://localhost:6379/0")
	v.SetDefault("celery_broker_url", "redis://localhost:6379/1")
	v.SetDefault("env", "development")
	v.SetDefault("debug", false)
	v.SetDefault("cors_allowed_origins", "http://localhost:3000")
	v.SetDefault("session_timeout_minutes", 60)
	v.SetDefault("agent_token_expiry_hours", 24)
	v.SetDefault("log_level", "info")
	v.SetDefault("audit_log_retention_months", 13)
	v.SetDefault("login_failure_threshold", 5)
	v.SetDefault("lockout_duration_minutes", 15)
	v.SetDefault("totp_issuer", "Astronomer")
	v.SetDefault("totp_require", false)
	v.SetDefault("server_metrics_addr", ":9090")
	v.SetDefault("worker_metrics_addr", ":9090")
	v.SetDefault("argocd_ui_upstream", "http://argocd-server.argocd.svc.cluster.local:80")
	v.SetDefault("argocd_cluster_proxy_base_url", "http://astronomer-server.astronomer.svc.cluster.local:8000")
	v.BindEnv("argocd_ui_upstream")
	v.BindEnv("argocd_cluster_proxy_base_url")

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
