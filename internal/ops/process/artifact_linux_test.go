//go:build linux

package process

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOwnedArtifactHelper(t *testing.T) {
	mode := os.Getenv("OPS_ARTIFACT_HELPER")
	if mode == "" {
		return
	}
	if mode == "bulk" {
		chunk := []byte(strings.Repeat("x", 32<<10))
		for i := 0; i < 544; i++ {
			if _, err := os.Stdout.Write(chunk); err != nil {
				os.Exit(2)
			}
		}
		os.Exit(0)
	}
	if _, err := os.Stdout.Write([]byte(strings.Repeat("x", 8192))); err != nil {
		os.Exit(2)
	}
	switch mode {
	case "stderr-overflow":
		_, _ = os.Stderr.Write([]byte(strings.Repeat("synthetic private diagnostic", 8192)))
		os.Exit(0)
	case "failed":
		os.Exit(17)
	case "wait":
		if err := os.WriteFile(os.Getenv("OPS_ARTIFACT_READY"), []byte("owned child output completed"), 0o600); err != nil {
			os.Exit(2)
		}
		for {
			time.Sleep(time.Second)
		}
	default:
		os.Exit(3)
	}
}

func artifactFixture(t *testing.T, mode string) Spec {
	t.Helper()
	spec := processFixture(t, "")
	spec.Args = []string{"-test.run=^TestOwnedArtifactHelper$"}
	spec.Environment = []Environment{{Name: "OPS_ARTIFACT_HELPER", Literal: mode}}
	spec.StdoutArtifact = ArtifactOutput{MaximumBytes: 18 << 20}
	return spec
}

func assertArtifactBinding(t *testing.T, result Result, acknowledged bool) {
	t.Helper()
	artifact := result.StdoutArtifact
	if !result.LeaderReaped || artifact == nil || artifact.Acknowledged != acknowledged || artifact.SHA256 != result.StdoutSHA256 {
		t.Fatal("joined private artifact acknowledgment differs")
	}
	file, err := os.Open(artifact.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer func(close func() error) { _ = close() }(file.Close)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() != artifact.SizeBytes {
		t.Fatal("artifact identity/size/private mode differs")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		t.Fatal(err)
	}
	if "sha256:"+hex.EncodeToString(hash.Sum(nil)) != artifact.SHA256 {
		t.Fatal("retained bytes differ")
	}
}

func TestOwnedProcessStreamsLargePrivateArtifactWithoutDiagnosticBudgetChange(t *testing.T) {
	spec := artifactFixture(t, "bulk")
	result, err := Run(context.Background(), spec)
	if err != nil || result.Kind != Success {
		t.Fatalf("streaming child did not succeed: %v", err)
	}
	assertArtifactBinding(t, result, true)
	if result.StdoutArtifact.SizeBytes != 17<<20 {
		t.Fatal("streaming output truncated or altered")
	}
	// Original stdout diagnostics remain bounded by the unchanged policy.
	spec = artifactFixture(t, "bulk")
	spec.StdoutArtifact = ArtifactOutput{}
	result, err = Run(context.Background(), spec)
	if err == nil || result.Kind != CaptureFailed || result.StdoutArtifact != nil {
		t.Fatal("ordinary diagnostic cap was bypassed")
	}
}

func TestOwnedProcessArtifactAndStderrBudgetsRemainIndependent(t *testing.T) {
	for _, mode := range []string{"bulk", "stderr-overflow"} {
		t.Run(mode, func(t *testing.T) {
			spec := artifactFixture(t, mode)
			if mode == "bulk" {
				spec.StdoutArtifact.MaximumBytes = 4096
			}
			result, err := Run(context.Background(), spec)
			if err == nil || result.Kind != CaptureFailed {
				t.Fatal("finite stream/diagnostic cap not enforced")
			}
			assertArtifactBinding(t, result, false)
			if result.StdoutArtifact.SizeBytes > spec.StdoutArtifact.MaximumBytes {
				t.Fatal("artifact exceeded its explicit bound")
			}
			info, statErr := os.Stat(filepath.Join(spec.CaptureDirectory, "fixture-000.stderr"))
			if statErr != nil || info.Size() > spec.MaximumCaptureBytes {
				t.Fatal("diagnostic cap changed")
			}
		})
	}
}

func TestOwnedProcessRetainsFailedPartialArtifactWithoutReplay(t *testing.T) {
	spec := artifactFixture(t, "failed")
	result, err := Run(context.Background(), spec)
	if err == nil || result.ChildExitCode != 17 || result.Kind != ChildFailed {
		t.Fatal("original child failure was lost")
	}
	assertArtifactBinding(t, result, false)
	again, againErr := Run(context.Background(), spec)
	if againErr == nil || again.Started {
		t.Fatal("retained partial output overwritten or replayed")
	}
	assertArtifactBinding(t, result, false)
}

func TestOwnedProcessCancellationJoinsAndRetainsPartialArtifact(t *testing.T) {
	spec := artifactFixture(t, "wait")
	ready := filepath.Join(spec.Directory, "ready")
	spec.Environment = append(spec.Environment, Environment{Name: "OPS_ARTIFACT_READY", Literal: ready})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		result Result
		err    error
	}
	finished := make(chan outcome, 1)
	go func() { result, err := Run(ctx, spec); finished <- outcome{result, err} }()
	// Cleanup owns the child even if readiness/further assertions fail.
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Error("artifact child cleanup did not join")
		}
	})
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actual child never reached output barrier")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case done := <-finished:
		finished <- done // retain the joined acknowledgment for cleanup
		if !errors.Is(done.err, context.Canceled) || done.result.Kind != Cancelled {
			t.Fatal("cancellation identity lost")
		}
		assertArtifactBinding(t, done.result, false)
	case <-time.After(3 * time.Second):
		t.Fatal("owned artifact cancellation did not join")
	}
}

func TestOwnedProcessRejectsInvalidArtifactBudgetBeforeChild(t *testing.T) {
	spec := artifactFixture(t, "bulk")
	spec.StdoutArtifact.MaximumBytes = -1
	result, err := Run(context.Background(), spec)
	if err == nil || result.Started {
		t.Fatal("negative artifact budget admitted a child")
	}
	spec.StdoutArtifact.MaximumBytes = 18 << 20
	spec.MaximumCaptureBytes = 17 << 20
	result, err = Run(context.Background(), spec)
	if err == nil || result.Started {
		t.Fatal("artifact option raised the diagnostic policy cap")
	}
}
