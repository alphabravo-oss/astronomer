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
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
)

func commandFixture(t *testing.T) (*Commands, Tool) {
	t.Helper()
	directory := t.TempDir()
	captures := filepath.Join(directory, "captures")
	if err := os.Mkdir(captures, 0o700); err != nil {
		t.Fatal(err)
	}
	toolPath := filepath.Join(directory, "bound-fixture-tool")
	if err := os.WriteFile(toolPath, []byte("synthetic tool bytes, not an official CLI"), 0o700); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte("synthetic tool bytes, not an official CLI"))
	commands, err := NewCommands(CommandOptions{Directory: directory, CaptureDirectory: captures, Timeout: time.Second, TerminationGrace: 100 * time.Millisecond, MaximumCaptureBytes: 256})
	if err != nil {
		t.Fatal(err)
	}
	return commands, Tool{Kind: Helm, Path: toolPath, Version: "v4.3.0", SHA256: "sha256:" + hex.EncodeToString(hash[:])}
}

func captureFixture(t *testing.T, spec process.Spec, raw []byte) process.Result {
	t.Helper()
	name := fmt.Sprintf("%s-%03d.stdout", spec.ID, spec.Sequence)
	if err := os.WriteFile(filepath.Join(spec.CaptureDirectory, name), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	return process.Result{Kind: process.Success, ExitCode: 0, ChildExitCode: 0, Started: true, LeaderReaped: true, StdoutSHA256: "sha256:" + hex.EncodeToString(hash[:])}
}

func TestBoundCommandsRequireActualVersionBeforeAdmission(t *testing.T) {
	commands, tool := commandFixture(t)
	calls := 0
	output := []byte("v4.2.0")
	commands.run = func(_ context.Context, spec process.Spec) (process.Result, error) {
		calls++
		if len(spec.Args) != 3 || spec.Args[0] != "version" || spec.Args[2] != "{{.Version}}" {
			t.Fatal("version preflight arguments differ")
		}
		return captureFixture(t, spec, output), nil
	}
	if _, _, err := commands.Execute(context.Background(), tool, []string{"upgrade"}); err == nil || calls != 0 {
		t.Fatal("unverified tool was admitted")
	}
	if err := commands.VerifyTool(context.Background(), tool); err == nil {
		t.Fatal("wrong actual version admitted")
	}
	if _, _, err := commands.Execute(context.Background(), tool, []string{"upgrade"}); err == nil || calls != 1 {
		t.Fatal("failed preflight granted admission")
	}
	output = []byte(tool.Version)
	if err := commands.VerifyTool(context.Background(), tool); err != nil {
		t.Fatal(err)
	}
}

func TestBoundCommandsRejectToolMutationAndCaptureMismatch(t *testing.T) {
	for _, mode := range []string{"tool", "capture"} {
		t.Run(mode, func(t *testing.T) {
			commands, tool := commandFixture(t)
			commands.verified[tool] = struct{}{}
			commands.run = func(_ context.Context, spec process.Spec) (process.Result, error) {
				result := captureFixture(t, spec, []byte("private child output"))
				if mode == "tool" {
					if err := os.WriteFile(tool.Path, []byte("changed fixture tool"), 0o700); err != nil {
						t.Fatal(err)
					}
				} else {
					result.StdoutSHA256 = "sha256:" + string(make([]byte, 64))
				}
				return result, nil
			}
			if raw, _, err := commands.Execute(context.Background(), tool, []string{"version"}); err == nil || raw != nil {
				t.Fatal("unbound bytes exposed")
			}
		})
	}
}

func TestBoundCommandsPreserveCancellationAndFiniteAdmission(t *testing.T) {
	commands, tool := commandFixture(t)
	commands.verified[tool] = struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	commands.run = func(_ context.Context, spec process.Spec) (process.Result, error) {
		result := captureFixture(t, spec, []byte("bounded"))
		cancel()
		return result, nil
	}
	if raw, _, err := commands.Execute(ctx, tool, []string{"version"}); !errors.Is(err, context.Canceled) || raw != nil {
		t.Fatal("expired command published output")
	}
	commands.sequence = 1000
	commands.run = func(context.Context, process.Spec) (process.Result, error) {
		t.Fatal("count budget launched child")
		return process.Result{}, nil
	}
	if _, _, err := commands.Execute(context.Background(), tool, nil); err == nil {
		t.Fatal("count budget ignored")
	}
}

func TestBoundCommandsRejectOversizedAndAliasedCaptures(t *testing.T) {
	commands, tool := commandFixture(t)
	commands.verified[tool] = struct{}{}
	commands.run = func(_ context.Context, spec process.Spec) (process.Result, error) {
		return captureFixture(t, spec, make([]byte, spec.MaximumCaptureBytes+1)), nil
	}
	if raw, _, err := commands.Execute(context.Background(), tool, nil); err == nil || raw != nil {
		t.Fatal("capture budget ignored")
	}
	alias := filepath.Join(commands.options.Directory, "capture-alias")
	if err := os.Symlink(commands.options.CaptureDirectory, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := readCapture(alias, "helm-001.stdout", 256, ""); err == nil {
		t.Fatal("symlink capture parent admitted")
	}
}
