package qualification

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testScaleConfig() ScaleConfig {
	config := DefaultScaleConfig()
	config.Clusters = 100
	config.ConnectedAgents = 50
	config.ServerReplicas = 3
	config.WorkerReplicas = 3
	config.Deployments = 1_000
	config.StatusEventsPerCluster = 3
	config.Iterations = 2
	config.Warmup = 1
	config.WarmPreviewP95 = time.Minute
	config.ColdPreviewP95 = time.Minute
	config.MinimumReleasesMinute = 1
	config.Commit = "test-commit"
	return config
}

func TestRunScaleExercisesCapacityInvariants(t *testing.T) {
	report := RunScale(context.Background(), testScaleConfig())
	if report.Status != "passed" || report.ReleaseEligible {
		t.Fatalf("status=%s releaseEligible=%t errors=%v", report.Status, report.ReleaseEligible, report.Errors)
	}
	for invariant, passed := range report.Invariants {
		if !passed {
			t.Errorf("invariant %s failed", invariant)
		}
	}
	if got := report.Metrics["status_coalescing_ratio"].P95; got != 3 {
		t.Fatalf("coalescing ratio = %v", got)
	}
}

func TestRunScaleRejectsUnboundedConfiguration(t *testing.T) {
	config := testScaleConfig()
	config.Deployments = 1_000_001
	report := RunScale(context.Background(), config)
	if report.Status != "failed" || len(report.Errors) == 0 {
		t.Fatalf("report = %+v", report)
	}
}

func TestRunSoakCheckpointsShortTestWithoutClaimingReleaseEvidence(t *testing.T) {
	checkpoints := 0
	config := SoakConfig{Duration: 20 * time.Millisecond, Interval: 5 * time.Millisecond, Scale: testScaleConfig(),
		Checkpoint: func(report SoakReport) error { checkpoints++; return nil }}
	report := RunSoak(context.Background(), config)
	if report.Status != "passed" || report.ReleaseEligible || len(report.Samples) == 0 || checkpoints < 3 {
		t.Fatalf("status=%s releaseEligible=%t samples=%d checkpoints=%d errors=%v", report.Status, report.ReleaseEligible, len(report.Samples), checkpoints, report.Errors)
	}
	if report.Invariants["release_duration_24h"] {
		t.Fatal("short soak was marked as satisfying the 24-hour criterion")
	}
}

func TestWriteJSONAtomicRejectsSymlinkAndWritesPrivateFile(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "report.json")
	if err := WriteJSONAtomic(target, map[string]string{"status": "passed"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	link := filepath.Join(directory, "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSONAtomic(link, map[string]string{"status": "failed"}); err == nil {
		t.Fatal("symlink output was accepted")
	}
}
