//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
)

func TestArtifactInputRetainsDiagnosticCapsAndRechecksPrivateBytes(t *testing.T) {
	commands, tool := commandFixture(t)
	tool.Kind = Kubectl
	commands.verified[tool] = struct{}{}
	source := artifactFixture(t, commands.options.CaptureDirectory, "kubectl-000.stdout", []byte("retained archive input"))
	calls := 0
	commands.run = func(_ context.Context, spec process.Spec) (process.Result, error) {
		calls++
		if spec.StdinFile != source.StdoutArtifact.Path || spec.MaximumCaptureBytes != 256 || spec.StdoutArtifact.MaximumBytes != 0 {
			t.Fatal("private stdin changed diagnostic contract")
		}
		return captureFixture(t, spec, []byte("bounded list fixture")), nil
	}
	raw, _, err := commands.ExecuteArtifactInput(context.Background(), tool, nil, source.StdoutArtifact, 1024)
	if err != nil || string(raw) != "bounded list fixture" || calls != 1 {
		t.Fatal("bound private artifact input failed", err)
	}
}

func TestArtifactInputRejectsUnacknowledgedChangedOrForeignCaptureBeforeChild(t *testing.T) {
	for _, change := range []string{"unacknowledged", "changed", "foreign", "budget", "unverified"} {
		t.Run(change, func(t *testing.T) {
			commands, tool := commandFixture(t)
			tool.Kind = Kubectl
			commands.verified[tool] = struct{}{}
			source := artifactFixture(t, commands.options.CaptureDirectory, "kubectl-000.stdout", []byte("retained archive input"))
			maximum := int64(1024)
			switch change {
			case "unacknowledged":
				source.StdoutArtifact.Acknowledged = false
			case "changed":
				if err := os.WriteFile(source.StdoutArtifact.Path, []byte("changed bytes"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "foreign":
				source.StdoutArtifact.Path = "/tmp/other-private-file"
			case "budget":
				maximum = 1
			case "unverified":
				delete(commands.verified, tool)
			}
			commands.run = func(context.Context, process.Spec) (process.Result, error) {
				t.Fatal("invalid private input launched child")
				return process.Result{}, nil
			}
			if raw, _, err := commands.ExecuteArtifactInput(context.Background(), tool, nil, source.StdoutArtifact, maximum); err == nil || raw != nil {
				t.Fatal("invalid artifact input admitted")
			}
		})
	}
}

func TestArtifactInputRejectsMutationOrCancellationAfterJoinedChild(t *testing.T) {
	for _, change := range []string{"mutation", "cancel"} {
		t.Run(change, func(t *testing.T) {
			commands, tool := commandFixture(t)
			tool.Kind = Kubectl
			commands.verified[tool] = struct{}{}
			source := artifactFixture(t, commands.options.CaptureDirectory, "kubectl-000.stdout", []byte("retained archive input"))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			commands.run = func(_ context.Context, spec process.Spec) (process.Result, error) {
				result := captureFixture(t, spec, []byte("private child output"))
				if change == "mutation" {
					if err := os.WriteFile(source.StdoutArtifact.Path, []byte("changed archive input"), 0o600); err != nil {
						t.Fatal(err)
					}
				} else {
					cancel()
				}
				return result, nil
			}
			raw, result, err := commands.ExecuteArtifactInput(ctx, tool, nil, source.StdoutArtifact, 1024)
			if err == nil || raw != nil || !result.LeaderReaped {
				t.Fatal("expired or changed input published success")
			}
			if change == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("input command cancellation identity lost")
			}
		})
	}
}
