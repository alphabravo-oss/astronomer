//go:build linux

package upgradecli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
)

func TestCommandCredentialInitialBindingRejectsBetweenStageMutation(t *testing.T) {
	directory := t.TempDir()
	secret := filepath.Join(directory, "credential")
	if err := os.WriteFile(secret, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := NewCommands(CommandOptions{Directory: directory, CaptureDirectory: directory, Timeout: time.Second, TerminationGrace: time.Second, MaximumCaptureBytes: 1024, Environment: []process.Environment{{Name: "GH_TOKEN", SecretFile: secret}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.validateCredentialEnvironment(context.Background()); err != nil {
		t.Fatal("initial credential refused")
	}
	if err := os.WriteFile(secret, []byte("later"), 0600); err != nil {
		t.Fatal(err)
	}
	c.run = func(context.Context, process.Spec) (process.Result, error) {
		t.Fatal("changed credential reached child")
		return process.Result{}, nil
	}
	if _, _, err := c.executeBound(context.Background(), Tool{}, nil, process.ArtifactOutput{}, nil, 0); err == nil {
		t.Fatal("between-stage credential mutation re-admitted")
	}
}
func TestCommandCredentialRecreatedPrivateFileCannotRebind(t *testing.T) {
	directory := t.TempDir()
	secret := filepath.Join(directory, "credential")
	if err := os.WriteFile(secret, []byte("same"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := NewCommands(CommandOptions{Directory: directory, CaptureDirectory: directory, Timeout: time.Second, TerminationGrace: time.Second, MaximumCaptureBytes: 1024, Environment: []process.Environment{{Name: "GH_TOKEN", SecretFile: secret}}})
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(directory, "replacement")
	if err := os.WriteFile(replacement, []byte("same"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, secret); err != nil {
		t.Fatal(err)
	}
	if err := c.validateCredentialEnvironment(context.Background()); err == nil {
		t.Fatal("same-byte recreated credential re-admitted")
	}
}
