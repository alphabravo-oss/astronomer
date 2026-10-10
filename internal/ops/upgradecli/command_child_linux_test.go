//go:build linux

package upgradecli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
)

// This is an actual owned child control for the adapter boundary. It does not
// qualify official Helm/kubectl behavior or a live Kubernetes mutation.
func TestCommandAdapterActualChildExitAndCancellationRetainPrivateCaptures(t *testing.T) {
	commands, _ := commandFixture(t)
	const privateMarker = "synthetic-private-diagnostic-marker"
	script := "#!/bin/sh\ncase \"$1\" in\nversion) printf 'Client Version: v1.37.1\\nKustomize Version: v5.7.1\\n';;\nfail) printf '" + privateMarker + "' >&2; exit 7;;\nwait) printf 'ready'; /bin/sleep 60 & wait;;\nesac\n"
	path := filepath.Join(commands.options.Directory, "fixture-kubectl")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(script))
	tool := Tool{Kind: Kubectl, Path: path, Version: "v1.37.1", SHA256: "sha256:" + hex.EncodeToString(hash[:])}
	if err := commands.VerifyTool(context.Background(), tool); err != nil {
		t.Fatal(err)
	}
	_, result, err := commands.Execute(context.Background(), tool, []string{"fail"})
	var child *process.Error
	if !errors.As(err, &child) || child.ExitCode() != 7 || result.ChildExitCode != 7 || !result.LeaderReaped || result.StderrSHA256 == "" {
		t.Fatalf("child failure contract differs: kind=%s exit=%d", result.Kind, result.ChildExitCode)
	}
	if strings.Contains(err.Error(), privateMarker) {
		t.Fatal("private child diagnostic escaped public error")
	}
	ctx, stop := context.WithCancel(context.Background())
	commands.options.Timeout = 3 * time.Second
	done := make(chan struct{})
	go func() {
		_, result, err = commands.Execute(ctx, tool, []string{"wait"})
		close(done)
	}()
	t.Cleanup(func() {
		stop()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("fixture child did not join during cleanup")
		}
	})
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ready := false
	for !ready {
		select {
		case <-done:
			t.Fatal("fixture child exited before cancellation readiness")
		case <-deadline.C:
			t.Fatal("fixture child readiness was not established")
		case <-ticker.C:
			raw, readErr := os.ReadFile(filepath.Join(commands.options.CaptureDirectory, "kubectl-003.stdout"))
			ready = readErr == nil && string(raw) == "ready"
		}
	}
	start := time.Now()
	stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("owned fixture cancellation did not join")
	}
	if !errors.Is(err, context.Canceled) || !result.LeaderReaped || time.Since(start) > 2*time.Second {
		t.Fatalf("owned cancellation was not bounded and joined: kind=%s", result.Kind)
	}
}
