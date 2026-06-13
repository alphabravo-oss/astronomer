package main

import (
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadScaleProfileAppliesMediumProfile(t *testing.T) {
	profile, err := loadScaleProfile(filepath.Join("profiles", "medium.yaml"))
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}
	cfg := &config{}
	if err := profile.apply(cfg); err != nil {
		t.Fatalf("apply profile: %v", err)
	}
	if cfg.profileName != "medium" {
		t.Fatalf("profileName=%q", cfg.profileName)
	}
	if cfg.clusters != 50 || cfg.rps != 500 {
		t.Fatalf("clusters/rps = %d/%d", cfg.clusters, cfg.rps)
	}
	if cfg.duration != 30*time.Minute {
		t.Fatalf("duration=%s", cfg.duration)
	}
	if !cfg.reconnectStorm.Enabled || cfg.reconnectStorm.BatchPercent != 100 {
		t.Fatalf("storm config = %+v", cfg.reconnectStorm)
	}
	if cfg.resources.PodsPerCluster != 250 || cfg.resources.ServicesPerCluster != 75 {
		t.Fatalf("resources = %+v", cfg.resources)
	}
	if len(cfg.day2FailureDrill) == 0 {
		t.Fatalf("expected day2 drills")
	}
}

func TestLoadScaleProfileRejectsInvalidValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte(`name: bad
clusters: 0
rps: -1
duration: nope
agents:
  mode: real
`), 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	if _, err := loadScaleProfile(path); err == nil {
		t.Fatalf("expected invalid profile error")
	}
}

func TestBackoffWithJitterStaysWithinBounds(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	got := backoffWithJitter(2, 1, 30, rng)
	if got < 3*time.Second || got > 5*time.Second {
		t.Fatalf("backoff=%s, want around 4s with 25%% jitter", got)
	}
}
