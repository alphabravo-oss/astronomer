//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
)

// These are synthetic private captures through the actual bound command
// adapter, not a live Helm/Kubernetes backup or full Driver qualification.
func helmRecoveryFixture(t *testing.T) (*HelmRecovery, *int) {
	t.Helper()
	observation, _ := releaseStateFixture(t)
	r := observation.release
	// This recovery fixture emits a smaller metadata projection and keeps its
	// independently asserted256B diagnostic budget unchanged.
	r.fence.commands.options.MaximumCaptureBytes = 256
	r.fence.commands.verified[r.fence.kubectl] = struct{}{}
	calls := 0
	r.fence.commands.artifactCapacity = func(string, int64) error { return nil }
	r.fence.commands.run = func(_ context.Context, spec process.Spec) (process.Result, error) {
		if spec.StdoutArtifact.MaximumBytes == 0 {
			return captureFixture(t, spec, []byte(`{"name":"astronomer","chart":"astronomer","version":"1.2.1","appVersion":"v1.2.1","namespace":"owned-namespace","revision":1,"status":"deployed"}`)), nil
		}
		calls++
		if spec.MaximumCaptureBytes != 256 || spec.StdoutArtifact.MaximumBytes != 1024 {
			t.Fatal("private backup changed diagnostic or explicit byte budget")
		}
		if strings.Join(spec.Args[:4], "\x00") != strings.Join([]string{"--kubeconfig", r.fence.options.Kubeconfig, spec.Args[2], r.plan.Identity.Context}, "\x00") {
			t.Fatal("private backup lost explicit inputs/context")
		}
		return artifactFixture(t, spec.CaptureDirectory, fmt.Sprintf("%s-%03d.stdout", spec.ID, spec.Sequence), []byte("synthetic private recovery "+strings.Join(spec.Args, " "))), nil
	}
	backup, err := NewHelmRecovery(observation, ArtifactPolicy{MaximumBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	return backup, &calls
}

func TestHelmRecoveryRetainsBoundPrivateLegacyFilesAndOriginalCaptures(t *testing.T) {
	backup, calls := helmRecoveryFixture(t)
	if err := backup.Capture(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *calls != 5 || len(backup.artifacts) != 5 {
		t.Fatal("private recovery capture set differs")
	}
	directory := backup.observation.release.fence.commands.options.CaptureDirectory
	for _, name := range []string{"values-all.yaml", "values-user.yaml", "manifest-before.yaml", "history.json", "release-secrets.yaml"} {
		info, err := os.Lstat(filepath.Join(directory, name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatal("private legacy file missing")
		}
	}
	if err := backup.Verify(context.Background()); err != nil {
		t.Fatal("acknowledged private recovery set changed", err)
	}
	if err := backup.Capture(context.Background()); err == nil || *calls != 5 {
		t.Fatal("backup was replayed or overwritten")
	}
	if _, err := NewHelmRecovery(backup.observation, ArtifactPolicy{}); err == nil {
		t.Fatal("implicit byte policy admitted")
	}
}

func TestHelmRecoveryRetainsPartialFilesOnFailureAndOwnerChange(t *testing.T) {
	for _, mode := range []string{"child", "revision", "cancel", "capacity"} {
		t.Run(mode, func(t *testing.T) {
			backup, calls := helmRecoveryFixture(t)
			commands := backup.observation.release.fence.commands
			original := commands.run
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			commands.run = func(ctx context.Context, spec process.Spec) (process.Result, error) {
				result, err := original(ctx, spec)
				if spec.StdoutArtifact.MaximumBytes > 0 && *calls == 2 {
					switch mode {
					case "child":
						return result, errors.New("synthetic failed retained child")
					case "cancel":
						cancel()
					case "revision":
						backup.observation.release.fence.options.Revision = 2
					case "capacity":
						commands.artifactCapacity = func(string, int64) error { return errors.New("synthetic reserve failure") }
					}
				}
				return result, err
			}
			if err := backup.Capture(ctx); err == nil {
				t.Fatal("failed partial recovery acknowledged")
			}
			if _, err := os.Stat(filepath.Join(commands.options.CaptureDirectory, "values-all.yaml")); err != nil {
				t.Fatal("prior recovery material removed")
			}
			if err := backup.Verify(context.Background()); err == nil {
				t.Fatal("partial recovery usable as complete backup")
			}
			before := *calls
			if err := backup.Capture(context.Background()); err == nil || before != *calls {
				t.Fatal("failed private capture replayed")
			}
		})
	}
}

func TestHelmRecoveryPrivateCopiesRejectExistingAliasesAndChangedBytes(t *testing.T) {
	for _, mode := range []string{"existing", "symlink", "tamper", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.Chmod(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			input := artifactFixture(t, directory, "helm-001.stdout", []byte("private original recovery")).StdoutArtifact
			name := "values-all.yaml"
			target := filepath.Join(directory, name)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "existing":
				if err := os.WriteFile(target, []byte("retain existing"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(input.Path, target); err != nil {
					t.Fatal(err)
				}
			case "tamper":
				if err := os.WriteFile(input.Path, []byte("wrong retained bytes"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				cancel()
			}
			if _, err := copyRecoveryArtifact(ctx, directory, name, *input, 1024); err == nil {
				t.Fatal("unsafe private recovery copy admitted")
			}
			if _, err := os.Stat(input.Path); err != nil {
				t.Fatal("original failed recovery deleted")
			}
		})
	}
}

func TestHelmRecoveryVerificationRejectsTamperedSetAndChangedRelease(t *testing.T) {
	for _, mode := range []string{"tamper", "missing", "revision"} {
		t.Run(mode, func(t *testing.T) {
			backup, _ := helmRecoveryFixture(t)
			if err := backup.Capture(context.Background()); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "tamper":
				if err := os.WriteFile(backup.artifacts["values-user.yaml"].Path, []byte("different private credentials"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				delete(backup.artifacts, "history.json")
			case "revision":
				backup.observation.release.fence.options.Revision = 2
			}
			if err := backup.Verify(context.Background()); err == nil {
				t.Fatal("changed recovery or owner state accepted")
			}
		})
	}
}
