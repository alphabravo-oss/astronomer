//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
)

func TestReadyzServiceRequiresOneExactNamespaceIdentity(t *testing.T) {
	name, uid, err := readyzService([]byte("server\tservice-uid\towned\n"), "owned")
	if err != nil || name != "server" || uid != "service-uid" {
		t.Fatal("exact owned Service rejected")
	}
	for _, raw := range []string{"", "server\tservice-uid\tother\n", "server\t\towned\n", "server\tservice-uid\towned\nserver\tservice-uid\towned\n"} {
		if _, _, err := readyzService([]byte(raw), "owned"); err == nil {
			t.Fatal("ambiguous Service admitted")
		}
	}
}

func TestReadyzProgressRejectsAliasedOversizedAndWrongModeFiles(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "symlink", "hardlink", "oversized", "public", "expired"} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.Chmod(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			name := "kubectl-001.stdout"
			path := filepath.Join(directory, name)
			banner := "Forwarding from 127.0.0.1:12345 -> 8000"
			if mode != "missing" {
				raw := []byte(banner + "\n")
				if mode == "oversized" {
					raw = make([]byte, (64<<10)+1)
				}
				if err := os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "symlink":
				if err := os.Rename(path, path+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".original", path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, path+".alias"); err != nil {
					t.Fatal(err)
				}
			case "public":
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "expired" {
				cancel()
			}
			ready, err := forwardBanner(ctx, directory, name, banner)
			if mode == "valid" {
				if !ready || err != nil {
					t.Fatal("exact private banner rejected")
				}
			} else if mode == "missing" {
				if ready || err != nil {
					t.Fatal("not-yet-created progress changed contract")
				}
			} else if err == nil || ready {
				t.Fatal("unbound progress granted readiness")
			}
		})
	}
}

// This exercises real loopback HTTP plus a context-joined synthetic command
// seam. It does not execute kubectl, own a Kubernetes Service or qualify an
// operator release. Every branch must await the seam's actual return.
func TestReadyzForwardJoinsOnHealthCancellationAndEarlyExit(t *testing.T) {
	for _, mode := range []string{"health", "cancel", "early-exit"} {
		t.Run(mode, func(t *testing.T) {
			commands, tool := commandFixture(t)
			tool.Kind = Kubectl
			commands.verified[tool] = struct{}{}
			// This synthetic output fixture does not qualify actual disk-reserve behavior.
			commands.artifactCapacity = func(string, int64) error { return nil }
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			if err := listener.Close(); err != nil {
				t.Fatal(err)
			}
			started := make(chan struct{})
			joined := make(chan struct{})
			commands.run = func(ctx context.Context, spec process.Spec) (process.Result, error) {
				defer close(joined)
				if mode == "early-exit" {
					close(started)
					return process.Result{Kind: process.Success, LeaderReaped: true}, nil
				}
				address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
				owned, listenErr := net.Listen("tcp4", address)
				if listenErr != nil {
					close(started)
					return process.Result{}, listenErr
				}
				server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/readyz" {
						http.NotFound(w, r)
						return
					}
					_, _ = w.Write([]byte(`{"status":"ok"}`))
				}), ReadHeaderTimeout: time.Second}
				served := make(chan struct{})
				go func() { defer close(served); _ = server.Serve(owned) }()
				defer func() { _ = server.Close(); <-served }()
				name := fmt.Sprintf("%s-%03d.stdout", spec.ID, spec.Sequence)
				banner := fmt.Sprintf("Forwarding from 127.0.0.1:%d -> 8000\n", port)
				if err := os.WriteFile(filepath.Join(spec.CaptureDirectory, name), []byte(banner), 0o600); err != nil {
					close(started)
					return process.Result{}, err
				}
				close(started)
				<-ctx.Done()
				return process.Result{Kind: process.Cancelled, LeaderReaped: true}, ctx.Err()
			}
			observation := &ReadyzObservation{release: &HelmRelease{fence: &OwnershipFence{commands: commands, kubectl: tool}}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				done <- observation.forward(ctx, []string{"synthetic-owned-forward"}, port)
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case <-finished:
				case <-time.After(6 * time.Second):
					t.Error("synthetic invocation not joined during cleanup")
				}
			})
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("synthetic forwarder never started")
			}
			if mode == "cancel" {
				cancel()
			}
			var result error
			select {
			case result = <-done:
			case <-time.After(6 * time.Second):
				t.Fatal("forwarder did not join within bound")
			}
			select {
			case <-joined:
			default:
				t.Fatal("forward returned before command joined")
			}
			if mode == "health" {
				if result != nil {
					t.Fatal(result)
				}
				raw, err := os.ReadFile(filepath.Join(commands.options.CaptureDirectory, "readyz-001.json"))
				if err != nil || strings.TrimSpace(string(raw)) != `{"status":"ok"}` {
					t.Fatal("health body not privately retained")
				}
			} else if mode == "cancel" {
				if !errors.Is(result, context.Canceled) {
					t.Fatal("cancellation identity lost")
				}
			} else if result == nil {
				t.Fatal("early child exit admitted health")
			}
		})
	}
}

func TestReadyzForwardRetainsTwoSuccessfulObservations(t *testing.T) {
	commands, tool := commandFixture(t)
	tool.Kind = Kubectl
	commands.verified[tool] = struct{}{}
	commands.artifactCapacity = func(string, int64) error { return nil }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(server.Close)
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(endpoint.Port())
	if err != nil {
		t.Fatal(err)
	}
	observation := &ReadyzObservation{release: &HelmRelease{fence: &OwnershipFence{commands: commands, kubectl: tool}}}
	for call := 1; call <= 2; call++ {
		joined := make(chan struct{})
		commands.run = func(ctx context.Context, spec process.Spec) (process.Result, error) {
			defer close(joined)
			name := fmt.Sprintf("%s-%03d.stdout", spec.ID, spec.Sequence)
			banner := fmt.Sprintf("Forwarding from 127.0.0.1:%d -> 8000\n", port)
			if err := os.WriteFile(filepath.Join(spec.CaptureDirectory, name), []byte(banner), 0o600); err != nil {
				return process.Result{}, err
			}
			<-ctx.Done()
			return process.Result{Kind: process.Cancelled, LeaderReaped: true}, ctx.Err()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		t.Cleanup(func() {
			cancel()
			select {
			case <-joined:
			case <-time.After(time.Second):
				t.Error("second-call fixture command not joined")
			}
		})
		if err := observation.forward(ctx, []string{"synthetic-owned-forward"}, port); err != nil {
			t.Fatal(err)
		}
		cancel()
		select {
		case <-joined:
		default:
			t.Fatal("health call returned before its command joined")
		}
		for sequence := 1; sequence <= call; sequence++ {
			name := fmt.Sprintf("readyz-%03d.json", sequence)
			raw, err := os.ReadFile(filepath.Join(commands.options.CaptureDirectory, name))
			if err != nil || string(raw) != `{"status":"ok"}` {
				t.Fatal("earlier private health original was lost")
			}
		}
	}
	if commands.sequence != 2 {
		t.Fatal("two successful calls were not tied to distinct captured process sequences")
	}
}
