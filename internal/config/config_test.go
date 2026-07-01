package config

import "testing"

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

// The opt-in feature flags must be bound so AutomaticEnv resolves them —
// without a BindEnv entry viper never reads the env var and the flag is stuck
// off (regression: control-plane snapshots + native RBAC could not be enabled).
func TestFeatureFlagEnvBinding(t *testing.T) {
	t.Setenv("NATIVE_RBAC_ENABLED", "true")
	t.Setenv("CONTROL_PLANE_SNAPSHOTS_ENABLED", "true")

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

	// And default OFF when unset.
	t.Setenv("NATIVE_RBAC_ENABLED", "")
	t.Setenv("CONTROL_PLANE_SNAPSHOTS_ENABLED", "")
	def, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if def.NativeRBACEnabled || def.ControlPlaneSnapshotsEnabled {
		t.Fatal("feature flags must default OFF when env is unset")
	}
}
