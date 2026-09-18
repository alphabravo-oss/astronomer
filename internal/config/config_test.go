package config

import (
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/sessionpolicy"
)

func TestLoadDefaultSessionTimeoutUsesCanonicalValue(t *testing.T) {
	t.Setenv("SESSION_TIMEOUT_MINUTES", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SessionTimeoutMinutes != sessionpolicy.DefaultMinutes {
		t.Fatalf("SessionTimeoutMinutes = %d, want %d", cfg.SessionTimeoutMinutes, sessionpolicy.DefaultMinutes)
	}
}

func TestLoadDefaultsWorkerMetricsAddr(t *testing.T) {
	t.Setenv("SERVER_METRICS_ADDR", "")
	t.Setenv("WORKER_METRICS_ADDR", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ServerMetricsAddr != ":9090" {
		t.Fatalf("ServerMetricsAddr = %q, want %q", cfg.ServerMetricsAddr, ":9090")
	}
	if cfg.WorkerMetricsAddr != ":9090" {
		t.Fatalf("WorkerMetricsAddr = %q, want %q", cfg.WorkerMetricsAddr, ":9090")
	}
}

func TestEventRelayQueueCapacityConfig(t *testing.T) {
	t.Setenv("EVENT_RELAY_QUEUE_CAPACITY", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.EventRelayQueueCapacity != 1024 {
		t.Fatalf("default EventRelayQueueCapacity = %d, want 1024", cfg.EventRelayQueueCapacity)
	}

	t.Setenv("EVENT_RELAY_QUEUE_CAPACITY", "2048")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.EventRelayQueueCapacity != 2048 {
		t.Fatalf("configured EventRelayQueueCapacity = %d, want 2048", cfg.EventRelayQueueCapacity)
	}
}

// The opt-in feature flags must be bound so AutomaticEnv resolves them —
// without a BindEnv entry viper never reads the env var and the flag is stuck
// off (regression: control-plane snapshots + native RBAC could not be enabled).
func TestFeatureFlagEnvBinding(t *testing.T) {
	t.Setenv("NATIVE_RBAC_ENABLED", "true")
	t.Setenv("CONTROL_PLANE_SNAPSHOTS_ENABLED", "true")
	t.Setenv("MANAGEMENT_BACKUP_ENABLED", "true")
	t.Setenv("CRD_ENABLED", "true")
	t.Setenv("NAMESPACE_SCOPED_RBAC_ENABLED", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.NativeRBACEnabled {
		t.Fatal("NATIVE_RBAC_ENABLED=true not resolved into cfg.NativeRBACEnabled")
	}
	if !cfg.ControlPlaneSnapshotsEnabled {
		t.Fatal("CONTROL_PLANE_SNAPSHOTS_ENABLED=true not resolved into cfg.ControlPlaneSnapshotsEnabled")
	}
	if !cfg.ManagementBackupEnabled {
		t.Fatal("MANAGEMENT_BACKUP_ENABLED=true not resolved into cfg.ManagementBackupEnabled")
	}
	if !cfg.CRDEnabled {
		t.Fatal("CRD_ENABLED=true not resolved into cfg.CRDEnabled")
	}
	if !cfg.NamespaceScopedRBACEnabled {
		t.Fatal("NAMESPACE_SCOPED_RBAC_ENABLED=true not resolved into cfg.NamespaceScopedRBACEnabled")
	}

	// native_rbac and control_plane_snapshots stay opt-in.
	// namespace_scoped_rbac is no longer one of them: it defaults ON now that
	// the project→namespace authoring surface and the watch reassembly it was
	// deferred on have both shipped.
	t.Setenv("NATIVE_RBAC_ENABLED", "")
	t.Setenv("CONTROL_PLANE_SNAPSHOTS_ENABLED", "")
	t.Setenv("MANAGEMENT_BACKUP_ENABLED", "")
	t.Setenv("CRD_ENABLED", "")
	t.Setenv("NAMESPACE_SCOPED_RBAC_ENABLED", "")
	def, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if def.NativeRBACEnabled || def.ControlPlaneSnapshotsEnabled || def.ManagementBackupEnabled || def.CRDEnabled {
		t.Fatal("native_rbac / control_plane_snapshots / management_backup / crd must default OFF when env is unset")
	}
	if !def.NamespaceScopedRBACEnabled {
		t.Fatal("namespace_scoped_rbac_enabled must default ON")
	}
}

// TestNamespaceScopedRBACEnabledDefaultsOn pins the promoted default: the
// authorization filter is ON unless an operator explicitly turns it off. It
// replaces TestNamespaceScopedRBACEnabledOptIn, which asserted the pre-parity
// default where a project-scoped grant resolved to nothing.
func TestNamespaceScopedRBACEnabledDefaultsOn(t *testing.T) {
	// Default (unset) → ON.
	t.Setenv("NAMESPACE_SCOPED_RBAC_ENABLED", "")
	on, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !on.NamespaceScopedRBACEnabled {
		t.Fatal("namespace_scoped_rbac_enabled must default ON")
	}

	// Explicit opt-out → OFF, so a deployment can revert without a rebuild.
	t.Setenv("NAMESPACE_SCOPED_RBAC_ENABLED", "false")
	off, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if off.NamespaceScopedRBACEnabled {
		t.Fatal("NAMESPACE_SCOPED_RBAC_ENABLED=false must turn the filter OFF")
	}
}

func TestLoadCentralizesRuntimeCompositionEnvironment(t *testing.T) {
	t.Setenv("ASTRONOMER_POD_IP", "10.0.0.7")
	t.Setenv("POD_NAMESPACE", "platform-system")
	t.Setenv("RELEASE_NAME", "elite")
	t.Setenv("HOSTNAME", "server-2")
	t.Setenv("CRD_WATCH_NAMESPACE", "management")
	t.Setenv("DEX_BUNDLED_ENABLED", "true")
	t.Setenv("DEX_BUNDLED_ISSUER_URL", "https://example.test/dex")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "0.25")
	t.Setenv("CHART_RATING_BAYESIAN_AVG", "4.2")
	t.Setenv("ASTRONOMER_GATEWAY_NAMESPACE", "platform-gateway")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.PodIP != "10.0.0.7" || cfg.PodNamespace != "platform-system" || cfg.ReleaseName != "elite" || cfg.ProcessHostname != "server-2" {
		t.Fatalf("process identity was not loaded: %#v", cfg)
	}
	if cfg.CRDWatchNamespace != "management" || !cfg.DexBundledEnabled || cfg.DexBundledIssuerURL != "https://example.test/dex" {
		t.Fatalf("runtime integration config was not loaded: %#v", cfg)
	}
	if cfg.OTELSamplerRatio != 0.25 || cfg.ChartRatingBayesianAverage != 4.2 {
		t.Fatalf("numeric runtime config was not loaded: %#v", cfg)
	}
	if cfg.GatewayNamespace != "platform-gateway" {
		t.Fatalf("GatewayNamespace = %q, want platform-gateway", cfg.GatewayNamespace)
	}
}
