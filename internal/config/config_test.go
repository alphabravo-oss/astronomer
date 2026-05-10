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
