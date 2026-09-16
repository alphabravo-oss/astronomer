package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/astrocli"
	"github.com/spf13/cobra"
)

func TestDestructiveCommandsRequireExplicitConfirmation(t *testing.T) {
	commands := [][]string{
		{"users", "delete", operatorTestID},
		{"cluster", "delete", operatorTestID},
		{"admin", "webhooks", "delete", operatorTestID},
		{"admin", "vault", "delete", operatorTestID},
		{"admin", "network-policy-templates", "delete", operatorTestID},
		{"catalog", "repositories", "delete", operatorTestID},
		{"catalog", "installed", "uninstall", operatorTestID},
		{"workloads", "delete", operatorTestID, "Deployment", "payments", "api"},
		{"workloads", "pod", "delete", operatorTestID, "payments", "api-123"},
	}
	for _, args := range commands {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				w.WriteHeader(http.StatusForbidden)
			}))
			defer server.Close()
			saveCLIConfig(t, &astrocli.Config{ServerURL: server.URL, AccessToken: "test-token"})
			t.Setenv("ASTRO_API_TOKEN", "")
			for _, answer := range []string{"", "\n", "no\n", "yes please\n", "y no\n"} {
				root := newRootCmd()
				var output bytes.Buffer
				root.SetOut(&output)
				root.SetErr(io.Discard)
				root.SetIn(strings.NewReader(answer))
				root.SetArgs(args)
				if err := root.Execute(); err == nil || err.Error() != "aborted" {
					t.Errorf("answer %q: error = %v", answer, err)
				}
				if !strings.Contains(output.String(), operatorTestID) {
					t.Errorf("confirmation does not identify exact target: %q", output.String())
				}
			}
			if requests != 0 {
				t.Errorf("declined actions sent %d requests", requests)
			}
		})
	}
}

func TestConfirmActionAcceptsOnlyWholeAffirmativeAnswer(t *testing.T) {
	for _, answer := range []string{"y\n", " YES \n", "yes"} {
		cmd := &cobra.Command{}
		cmd.SetIn(strings.NewReader(answer))
		var output bytes.Buffer
		cmd.SetOut(&output)
		if err := confirmAction(cmd, "Delete user alice?"); err != nil {
			t.Fatal(err)
		}
		if got, want := output.String(), "Delete user alice? [y/N] "; got != want {
			t.Errorf("prompt = %q, want %q", got, want)
		}
	}
}

type confirmationReadError struct{}

func (confirmationReadError) Read([]byte) (int, error) { return 0, errors.New("input disconnected") }

func TestConfirmActionPropagatesIOErrors(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetIn(confirmationReadError{})
	cmd.SetOut(io.Discard)
	if err := confirmAction(cmd, "Delete?"); err == nil || !strings.Contains(err.Error(), "input disconnected") {
		t.Fatalf("error = %v", err)
	}
	cmd.SetIn(strings.NewReader("yes\n"))
	cmd.SetOut(failingWriter{})
	if err := confirmAction(cmd, "Delete?"); err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("error = %v", err)
	}
}
