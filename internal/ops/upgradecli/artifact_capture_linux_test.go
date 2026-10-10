//go:build linux

package upgradecli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
)

func artifactFixture(t *testing.T, directory, name string, raw []byte) process.Result {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	sha := "sha256:" + hex.EncodeToString(digest[:])
	return process.Result{Kind: process.Success, LeaderReaped: true, StdoutSHA256: sha, StdoutArtifact: &process.ArtifactCapture{Path: path, SHA256: sha, SizeBytes: int64(len(raw)), Acknowledged: true}}
}

func TestArtifactReserveRequiresProjectedDefaultCapacity(t *testing.T) {
	const gib = int64(1 << 30)
	for _, row := range []struct {
		total, available, maximum int64
		allowed                   bool
	}{
		{100 * gib, 30 * gib, 10 * gib, true}, {100 * gib, 30 * gib, 10*gib + 1, false},
		{200 * gib, 50 * gib, 10 * gib, true}, {200 * gib, 50 * gib, 10*gib + 1, false},
		{100*gib + 1, 30 * gib, 10 * gib, false}, {0, 30 * gib, gib, false},
		{100 * gib, 101 * gib, gib, false}, {100 * gib, 30 * gib, 0, false},
		{100 * gib, -1, gib, false}, {1<<63 - 1, 1<<63 - 1, 1, true},
	} {
		if got := artifactReserve(row.total, row.available, row.maximum) == nil; got != row.allowed {
			t.Fatalf("reserve admission differs: %+v", row)
		}
	}
}

func TestArtifactCaptureRejectsUnboundToolAndCapacityBeforeChild(t *testing.T) {
	commands, tool := commandFixture(t)
	tool.Kind = Kubectl
	calls := 0
	commands.run = func(context.Context, process.Spec) (process.Result, error) {
		calls++
		t.Fatal("refused artifact launched child")
		return process.Result{}, nil
	}
	if _, _, err := commands.CaptureArtifact(context.Background(), tool, nil, ArtifactPolicy{MaximumBytes: 1}); err == nil {
		t.Fatal("unverified tool admitted")
	}
	commands.verified[tool] = struct{}{}
	commands.artifactCapacity = func(string, int64) error { return errors.New("synthetic capacity failure") }
	if _, _, err := commands.CaptureArtifact(context.Background(), tool, nil, ArtifactPolicy{MaximumBytes: 1}); err == nil || calls != 0 {
		t.Fatal("capacity failure admitted child")
	}
	tool.Kind = Helm
	if _, _, err := commands.CaptureArtifact(context.Background(), tool, nil, ArtifactPolicy{MaximumBytes: 1}); err == nil || calls != 0 {
		t.Fatal("unqualified bulk tool admitted")
	}
}

func TestArtifactCaptureReopensLargePrivateOutputWithoutDiagnosticExpansion(t *testing.T) {
	commands, tool := commandFixture(t)
	tool.Kind = Kubectl
	commands.verified[tool] = struct{}{}
	capacityCalls := 0
	commands.artifactCapacity = func(directory string, maximum int64) error {
		capacityCalls++
		if directory != commands.options.CaptureDirectory || maximum != 17<<20 {
			t.Fatal("capacity request differs")
		}
		return artifactReserve(100<<30, 30<<30, maximum)
	}
	commands.run = func(_ context.Context, spec process.Spec) (process.Result, error) {
		if spec.MaximumCaptureBytes != 256 || spec.StdoutArtifact.MaximumBytes != 17<<20 {
			t.Fatal("bulk mode changed diagnostic budget")
		}
		name := fmt.Sprintf("%s-%03d.stdout", spec.ID, spec.Sequence)
		path := filepath.Join(spec.CaptureDirectory, name)
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.New()
		block := make([]byte, 32<<10)
		for i := 0; i < 17*32; i++ {
			if _, err := file.Write(block); err != nil {
				t.Fatal(err)
			}
			_, _ = hash.Write(block)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		sha := "sha256:" + hex.EncodeToString(hash.Sum(nil))
		return process.Result{Kind: process.Success, LeaderReaped: true, StdoutSHA256: sha, StdoutArtifact: &process.ArtifactCapture{Path: path, SHA256: sha, SizeBytes: 17 << 20, Acknowledged: true}}, nil
	}
	artifact, result, err := commands.CaptureArtifact(context.Background(), tool, nil, ArtifactPolicy{MaximumBytes: 17 << 20})
	if err != nil || artifact == nil || artifact.SizeBytes != 17<<20 || capacityCalls != 1 {
		t.Fatal("bounded large artifact capture failed", err)
	}
	artifact.SHA256 = "caller mutation"
	if result.StdoutArtifact.SHA256 == artifact.SHA256 {
		t.Fatal("caller alias changes original result binding")
	}
}

func TestArtifactPrivateReopenRejectsAliasesChangesAndExpiredContext(t *testing.T) {
	for _, mode := range []string{"digest", "path", "size", "permission", "hardlink", "symlink", "cancel", "unacknowledged"} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.Chmod(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			name := "kubectl-001.stdout"
			result := artifactFixture(t, directory, name, []byte("bounded private archive fixture"))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "digest":
				result.StdoutSHA256 = "sha256:invalid"
				result.StdoutArtifact.SHA256 = result.StdoutSHA256
			case "path":
				result.StdoutArtifact.Path = filepath.Join(directory, "different")
			case "size":
				result.StdoutArtifact.SizeBytes++
			case "permission":
				if err := os.Chmod(result.StdoutArtifact.Path, 0o644); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(result.StdoutArtifact.Path, filepath.Join(directory, "alias")); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				original := result.StdoutArtifact.Path
				if err := os.Rename(original, original+"-retained"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(original+"-retained", original); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				cancel()
			case "unacknowledged":
				result.StdoutArtifact.Acknowledged = false
			}
			if err := verifyArtifactCapture(ctx, directory, name, 1024, result); err == nil {
				t.Fatal("invalid or expired private artifact accepted")
			}
		})
	}
}

func TestArtifactCapturePreservesFailedPartialResultWithoutPublishingBackup(t *testing.T) {
	commands, tool := commandFixture(t)
	tool.Kind = Kubectl
	commands.verified[tool] = struct{}{}
	commands.artifactCapacity = func(string, int64) error { return nil }
	sentinel := errors.New("synthetic child failure")
	commands.run = func(_ context.Context, spec process.Spec) (process.Result, error) {
		result := artifactFixture(t, spec.CaptureDirectory, fmt.Sprintf("%s-%03d.stdout", spec.ID, spec.Sequence), []byte("partial archive"))
		result.Kind = process.ChildFailed
		result.StdoutArtifact.Acknowledged = false
		return result, sentinel
	}
	artifact, result, err := commands.CaptureArtifact(context.Background(), tool, nil, ArtifactPolicy{MaximumBytes: 1024})
	if artifact != nil || !errors.Is(err, sentinel) || result.StdoutArtifact == nil {
		t.Fatal("failed partial artifact was published or discarded")
	}
	if _, err := os.Stat(result.StdoutArtifact.Path); err != nil {
		t.Fatal("partial recovery capture disappeared")
	}
}
