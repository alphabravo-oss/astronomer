package config

import (
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/envconfig"
	"github.com/alphabravocompany/astronomer-go/internal/releasecontract"
	"github.com/alphabravocompany/astronomer-go/internal/sessionpolicy"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

// Config holds all configuration for the application.
type Config struct {
	DatabaseURL string `mapstructure:"database_url"`
	RedisURL    string `mapstructure:"redis_url"`
	// EventRelayQueueCapacity bounds local events waiting for cross-replica
	// Redis fan-out. The events package applies a hard maximum even when an
	// environment value is larger.
	EventRelayQueueCapacity int `mapstructure:"event_relay_queue_capacity"`

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

	CORSAllowedOrigins string `mapstructure:"cors_allowed_origins"`
	// TrustedProxyCIDRs is the explicit set of reverse-proxy networks allowed
	// to assert X-Forwarded-For. Empty means forwarded client IPs are ignored.
	TrustedProxyCIDRs     string `mapstructure:"trusted_proxy_cidrs"`
	SessionTimeoutMinutes int    `mapstructure:"session_timeout_minutes"`
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
	// GitopsWebhookSecret is the shared secret a git-provider push webhook must
	// present (X-Astronomer-Webhook-Secret) to trigger an immediate gitops sync.
	// Empty (default) leaves the webhook endpoint disabled.
	GitopsWebhookSecret string `mapstructure:"gitops_webhook_secret"`

	// ServerURL is the externally-reachable URL of this Astronomer install.
	// It seeds platform_configuration.server_url on first boot and is used in
	// signed downstream registration manifests. Operators may set it later when
	// it is intentionally omitted from a development install.
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
	// ReleaseManifestPath points at the signed, generated compatibility unit
	// mounted by the packaged chart. When present it is authoritative for every
	// artifact/version field below; mutable operator overrides cannot split the
	// release into unqualified component combinations.
	ReleaseManifestPath      string `mapstructure:"release_manifest_path"`
	ReleaseMirrorMappingPath string `mapstructure:"release_mirror_mapping_path"`

	// Signed, immutable downstream delivery artifacts. Production requires both
	// artifacts and exact verification identities; disconnected installations
	// point the repositories at their verified internal mirror while preserving
	// the release digests and signing policy.
	DeliveryEnabled                             bool   `mapstructure:"delivery_enabled"`
	DeliveryKubernetesMinMinor                  string `mapstructure:"delivery_kubernetes_min_minor"`
	DeliveryKubernetesMaxMinor                  string `mapstructure:"delivery_kubernetes_max_minor"`
	DeliveryFluxVersion                         string `mapstructure:"delivery_flux_version"`
	DeliveryFluxDistributionRepository          string `mapstructure:"delivery_flux_distribution_repository"`
	DeliveryFluxDistributionDigest              string `mapstructure:"delivery_flux_distribution_digest"`
	DeliveryFluxDistributionAssetPath           string `mapstructure:"delivery_flux_distribution_asset_path"`
	DeliveryFluxDistributionCertificateIdentity string `mapstructure:"delivery_flux_distribution_certificate_identity"`
	DeliveryFluxDistributionOIDCIssuer          string `mapstructure:"delivery_flux_distribution_oidc_issuer"`
	DeliveryBundleRepository                    string `mapstructure:"delivery_bundle_repository"`
	DeliveryBundleDigest                        string `mapstructure:"delivery_bundle_digest"`
	DeliveryBundleCertificateIdentity           string `mapstructure:"delivery_bundle_certificate_identity"`
	DeliveryBundleOIDCIssuer                    string `mapstructure:"delivery_bundle_oidc_issuer"`
	DeliverySourceAllowedPrivateHosts           string `mapstructure:"delivery_source_allowed_private_hosts"`
	DeliverySourceEgressCIDRs                   string `mapstructure:"delivery_source_egress_cidrs"`
	DeliverySourceProxyURL                      string `mapstructure:"delivery_source_proxy_url"`
	DeliverySourceAllowSSH                      bool   `mapstructure:"delivery_source_allow_ssh"`
	DeliverySourceCAFile                        string `mapstructure:"delivery_source_ca_file"`
	DeliverySourceMaxArtifactBytes              int64  `mapstructure:"delivery_source_max_artifact_bytes"`
	DeliverySourceMaxHelmChartBytes             int64  `mapstructure:"delivery_source_max_helm_chart_bytes"`
	DeliverySourceTrustDirectory                string `mapstructure:"delivery_source_trust_directory"`
	DeliveryCosignPath                          string `mapstructure:"delivery_cosign_path"`

	LogLevel string `mapstructure:"log_level"`

	AuditLogRetentionMonths int `mapstructure:"audit_log_retention_months"`
	// ClusterTombstoneRetentionDays is how long decommissioned cluster rows
	// (tombstones) are kept before the worker retention sweep hard-deletes
	// them. Conservative 90d default; chart-tunable via
	// CLUSTER_TOMBSTONE_RETENTION_DAYS.
	ClusterTombstoneRetentionDays int    `mapstructure:"cluster_tombstone_retention_days"`
	ServerMetricsAddr             string `mapstructure:"server_metrics_addr"`
	WorkerMetricsAddr             string `mapstructure:"worker_metrics_addr"`

	// Charlie MCP is a separate, private mTLS listener. The chart mounts these
	// files from the installation-owned Secret; an empty address leaves the
	// listener manager completely dormant in local/test processes.
	CharlieMCPListenAddress        string `mapstructure:"charlie_mcp_listen_address"`
	CharlieMCPTLSCertFile          string `mapstructure:"charlie_mcp_tls_cert_file"`
	CharlieMCPTLSKeyFile           string `mapstructure:"charlie_mcp_tls_key_file"`
	CharlieMCPClientCAFile         string `mapstructure:"charlie_mcp_client_ca_file"`
	CharlieMCPActionSigningKeyFile string `mapstructure:"charlie_mcp_action_signing_key_file"`
	// Charlie Product Bridge is the only Astronomer-to-agent runtime transport.
	// It is fixed to the cluster-local Service and uses a distinct client
	// identity from the MCP server identity.
	CharlieBridgeTLSCertFile string `mapstructure:"charlie_bridge_tls_cert_file"`
	CharlieBridgeTLSKeyFile  string `mapstructure:"charlie_bridge_tls_key_file"`
	CharlieBridgeCAFile      string `mapstructure:"charlie_bridge_ca_file"`
	CharlieAgentNamespace    string `mapstructure:"charlie_agent_namespace"`

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
	KubectlShellEnabled bool `mapstructure:"kubectl_shell_enabled"`
	// ControlPlaneSnapshotsEnabled gates the control-plane (etcd) DR snapshot
	// subsystem. Default false: the feature triggers PRIVILEGED node Jobs on
	// self-managed clusters, so it stays fully off (routes unregistered, worker
	// sweep inert) until an operator opts in.
	ControlPlaneSnapshotsEnabled bool `mapstructure:"control_plane_snapshots_enabled"`
	// NativeRBACEnabled gates the native per-CRD RBAC allow layer. Default
	// false: when off the k8s-proxy authz hook is byte-for-byte unchanged
	// (no native authorizer injected) and the rule-authoring API is
	// unregistered. It only ever GRANTS explicitly-authored access, so
	// enabling it with zero rules is a no-op.
	NativeRBACEnabled bool `mapstructure:"native_rbac_enabled"`
	// NamespaceScopedRBACEnabled gates namespace/project-scoped cluster resource
	// reads. Default ON: the two conditions plan 009 / DIR-04 deferred it on are
	// both met — the project→namespace authoring surface ships (backend
	// POST /projects/{id}/add-namespace/ + the project Namespaces tab), and
	// namespace-filtered watches reassemble whole events on both proxy paths
	// (internal/tunnel/nsfilter.go watchLineFilter). When ON, cluster list
	// results are filtered to the caller's authorized namespaces and
	// kubectl-shell sessions are scoped to the caller's grants (see
	// internal/handler/kubectl_shell_scope.go); the seams fail closed for scoped
	// users while superusers/cluster-wide grants still see everything.
	// Set NAMESPACE_SCOPED_RBAC_ENABLED=false to fall back to the pre-parity
	// behavior, where a namespace-restricted caller is 403'd off the cluster
	// list/watch routes entirely instead of getting a filtered page, and
	// kubectl-shell sessions are not scoped to the caller's grants.
	//
	// Turning it off does NOT make project grants inert. The project→namespace
	// expansion is unconditional (a property of the binding model, not of this
	// flag) and requireK8sProxyPermission's primary CheckPermission is not
	// flag-guarded, so a project member still reaches namespace-explicit paths
	// either way. See warnInertProjectBindings in internal/server for the
	// startup message that spells this out.
	//
	// Read once at startup (server.go wires it into the workload handler, the
	// router closure, and kubectl-shell); changing it requires a restart.
	NamespaceScopedRBACEnabled      bool   `mapstructure:"namespace_scoped_rbac_enabled"`
	KubectlShellImage               string `mapstructure:"kubectl_shell_image"`
	KubectlShellIdleTimeoutMinutes  int    `mapstructure:"kubectl_shell_idle_timeout_minutes"`
	KubectlShellSessionHardCapHours int    `mapstructure:"kubectl_shell_session_hard_cap_hours"`

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

	// A4 — tunnel connect rate-limit + replay defense. The connect limiter is a
	// FAILURE-keyed fixed-window counter (per source IP): an IP is throttled only
	// after it accumulates AuthFailureLimit failed CONNECT validations inside
	// AuthFailureWindowMinutes, and any SUCCESSFUL connect resets that IP's
	// counter to zero. Defaults are deliberately generous so a healthy fleet
	// behind one egress IP (which emits ~0 auth failures) is never throttled;
	// only credential-probing traffic trips it.
	TunnelConnectAuthFailureLimit         int `mapstructure:"tunnel_connect_auth_failure_limit"`
	TunnelConnectAuthFailureWindowMinutes int `mapstructure:"tunnel_connect_auth_failure_window_minutes"`
	// TunnelConnectClockSkewMinutes bounds how far the CONNECT envelope timestamp
	// may drift from the server clock before the handshake is rejected as a
	// possible replay (L13). Lenient symmetric window; <=0 disables the check.
	// An older agent that does not stamp a timestamp is never hard-rejected.
	TunnelConnectClockSkewMinutes int `mapstructure:"tunnel_connect_clock_skew_minutes"`
	// TunnelRegisterRateLimitPerMinute caps requests to the public
	// GET /register/{token} bootstrap-manifest endpoint per source IP (L3).
	TunnelRegisterRateLimitPerMinute int `mapstructure:"tunnel_register_rate_limit_per_minute"`
	// TunnelWorkerConcurrency is the number of tunnel-bound worker tasks (cluster
	// apply/drift/decommission/gatekeeper/etc.) a server pod runs at once (M11).
	// Was hardcoded to 2, so two long helm --wait installs (up to ~10m each)
	// starved every short tunnel RPC. Default 8. Per-pod; scales with replicas.
	TunnelWorkerConcurrency int `mapstructure:"tunnel_worker_concurrency"`
	// ServerReplicas is the configured server replica count (Helm injects it from
	// .Values.server.replicaCount). Used for the L19 HA self-check: with >1
	// replica and a RedisURL set but no ASTRONOMER_POD_IP, the cross-pod tunnel
	// locator silently disables and non-owning replicas 503 — so readiness fails
	// loudly instead. Defaults to 1 (single-replica; locator not required).
	ServerReplicas int `mapstructure:"server_replicas"`
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
		"release_manifest_path",
		"release_mirror_mapping_path",
		"delivery_enabled",
		"delivery_kubernetes_min_minor",
		"delivery_kubernetes_max_minor",
		"delivery_flux_version",
		"delivery_flux_distribution_repository",
		"delivery_flux_distribution_digest",
		"delivery_flux_distribution_asset_path",
		"delivery_flux_distribution_certificate_identity",
		"delivery_flux_distribution_oidc_issuer",
		"delivery_bundle_repository",
		"delivery_bundle_digest",
		"delivery_bundle_certificate_identity",
		"delivery_bundle_oidc_issuer",
		"delivery_source_allowed_private_hosts",
		"delivery_source_egress_cidrs",
		"delivery_source_proxy_url",
		"delivery_source_allow_ssh",
		"delivery_source_ca_file",
		"delivery_source_max_artifact_bytes",
		"delivery_source_max_helm_chart_bytes",
		"delivery_source_trust_directory",
		"delivery_cosign_path",
		"database_url",
		"redis_url",
		"event_relay_queue_capacity",
		"secret_key",
		"trusted_proxy_cidrs",
		"server_url",
		"audit_log_retention_months",
		"cluster_tombstone_retention_days",
		"server_metrics_addr",
		"worker_metrics_addr",
		"charlie_mcp_listen_address",
		"charlie_mcp_tls_cert_file",
		"charlie_mcp_tls_key_file",
		"charlie_mcp_client_ca_file",
		"charlie_mcp_action_signing_key_file",
		"charlie_bridge_tls_cert_file",
		"charlie_bridge_tls_key_file",
		"charlie_bridge_ca_file",
		"charlie_agent_namespace",
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
		"control_plane_snapshots_enabled",
		"native_rbac_enabled",
		"namespace_scoped_rbac_enabled",
		"manifest_signing_secret",
		"gitops_webhook_secret",
		"dex_bundled_enabled",
		"auth_local_password_only",
		"astronomer_catalog_url",
		"tunnel_connect_auth_failure_limit",
		"tunnel_connect_auth_failure_window_minutes",
		"tunnel_connect_clock_skew_minutes",
		"tunnel_register_rate_limit_per_minute",
		"server_replicas",
		"tunnel_worker_concurrency",
	); err != nil {
		return nil, err
	}

	envconfig.SetDefaults(v,
		envconfig.Default{Key: "database_url", Value: "postgres://astronomer:astronomer@localhost:5432/astronomer?sslmode=disable"},
		envconfig.Default{Key: "redis_url", Value: "redis://localhost:6379/0"},
		envconfig.Default{Key: "event_relay_queue_capacity", Value: 1024},
		envconfig.Default{Key: "env", Value: "development"},
		envconfig.Default{Key: "debug", Value: false},
		envconfig.Default{Key: "cors_allowed_origins", Value: "http://localhost:3000"},
		envconfig.Default{Key: "trusted_proxy_cidrs", Value: ""},
		envconfig.Default{Key: "session_timeout_minutes", Value: sessionpolicy.DefaultMinutes},
		envconfig.Default{Key: "registration_token_ttl_hours", Value: 1},
		envconfig.Default{Key: "delivery_enabled", Value: true},
		envconfig.Default{Key: "delivery_kubernetes_min_minor", Value: "1.33"},
		envconfig.Default{Key: "delivery_kubernetes_max_minor", Value: "1.35"},
		envconfig.Default{Key: "delivery_flux_version", Value: "v2.9.3"},
		envconfig.Default{Key: "delivery_source_allowed_private_hosts", Value: "[]"},
		envconfig.Default{Key: "delivery_source_egress_cidrs", Value: "[]"},
		envconfig.Default{Key: "delivery_source_allow_ssh", Value: false},
		envconfig.Default{Key: "delivery_source_max_artifact_bytes", Value: int64(512 << 20)},
		envconfig.Default{Key: "delivery_source_max_helm_chart_bytes", Value: int64(100 << 20)},
		envconfig.Default{Key: "delivery_source_trust_directory", Value: "/etc/astronomer/delivery-trust"},
		envconfig.Default{Key: "delivery_cosign_path", Value: "/usr/local/bin/cosign"},
		envconfig.Default{Key: "log_level", Value: "info"},
		envconfig.Default{Key: "audit_log_retention_months", Value: 13},
		envconfig.Default{Key: "cluster_tombstone_retention_days", Value: 90},
		envconfig.Default{Key: "login_failure_threshold", Value: 5},
		envconfig.Default{Key: "lockout_duration_minutes", Value: 15},
		envconfig.Default{Key: "totp_issuer", Value: "Astronomer"},
		envconfig.Default{Key: "totp_require", Value: false},
		envconfig.Default{Key: "kubectl_shell_enabled", Value: false},
		envconfig.Default{Key: "control_plane_snapshots_enabled", Value: false},
		envconfig.Default{Key: "native_rbac_enabled", Value: false},
		// Default ON: a project-scoped grant that resolves to nothing is the
		// parity bug, not the safe state. Set NAMESPACE_SCOPED_RBAC_ENABLED=false
		// to disable namespace-filtered reads + kubectl-shell caller scoping.
		envconfig.Default{Key: "namespace_scoped_rbac_enabled", Value: true},
		envconfig.Default{Key: "kubectl_shell_image", Value: "astronomer-shell:dev"},
		envconfig.Default{Key: "kubectl_shell_idle_timeout_minutes", Value: 30},
		envconfig.Default{Key: "kubectl_shell_session_hard_cap_hours", Value: 4},
		envconfig.Default{Key: "server_metrics_addr", Value: ":9090"},
		envconfig.Default{Key: "worker_metrics_addr", Value: ":9090"},
		envconfig.Default{Key: "dex_bundled_enabled", Value: false},
		envconfig.Default{Key: "auth_local_password_only", Value: false},
		// A4 — generous tunnel-connect failure limiter + lenient replay window.
		envconfig.Default{Key: "tunnel_connect_auth_failure_limit", Value: 50},
		envconfig.Default{Key: "tunnel_connect_auth_failure_window_minutes", Value: 5},
		envconfig.Default{Key: "tunnel_connect_clock_skew_minutes", Value: 5},
		envconfig.Default{Key: "tunnel_register_rate_limit_per_minute", Value: 30},
		envconfig.Default{Key: "server_replicas", Value: 1},
		envconfig.Default{Key: "tunnel_worker_concurrency", Value: 8},
	)

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.ReleaseManifestPath) != "" {
		_, release, err := releasecontract.Load(cfg.ReleaseManifestPath, version.Version)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(cfg.ReleaseMirrorMappingPath) != "" {
			release, err = releasecontract.ApplyMirrorMapping(cfg.ReleaseMirrorMappingPath, cfg.ReleaseManifestPath, release)
			if err != nil {
				return nil, err
			}
		}
		cfg.AgentImageRepository = release.AgentImage
		cfg.AgentImageTag = release.Version
		cfg.DeliveryKubernetesMinMinor = release.MinimumKubernetesMinor
		cfg.DeliveryKubernetesMaxMinor = release.MaximumKubernetesMinor
		cfg.DeliveryFluxVersion = release.FluxVersion
		cfg.DeliveryFluxDistributionRepository = release.FluxRepository
		cfg.DeliveryFluxDistributionDigest = release.FluxDigest
		cfg.DeliveryFluxDistributionOIDCIssuer = release.CertificateOIDCIssuer
		cfg.DeliveryFluxDistributionCertificateIdentity = release.CertificateIdentity
		cfg.DeliveryBundleRepository = release.BundleRepository
		cfg.DeliveryBundleDigest = release.BundleDigest
		cfg.DeliveryBundleOIDCIssuer = release.CertificateOIDCIssuer
		cfg.DeliveryBundleCertificateIdentity = release.CertificateIdentity
	}
	return cfg, nil
}
