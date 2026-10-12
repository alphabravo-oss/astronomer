//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
)

func fenceFixture(t *testing.T) (*OwnershipFence, *upgrade.DisposableLease) {
	t.Helper()
	commands, tool := commandFixture(t)
	tool.Kind = Kubectl
	tool.Version = "v1.37.1"
	config := filepath.Join(commands.options.Directory, "kubeconfig")
	if err := os.WriteFile(config, []byte("synthetic private input; never a live kubeconfig"), 0o600); err != nil {
		t.Fatal(err)
	}
	id := upgrade.Identity{Context: "owned-context", APIServer: "https://127.0.0.1:6443", Namespace: "owned-namespace", NamespaceUID: "namespace-uid", Release: "astronomer", ReleaseUID: "original-secret-uid"}
	fence, err := NewOwnershipFence(commands, tool, FenceOptions{Identity: id, Kubeconfig: config, Revision: 1, DatabasePod: "owned-postgres-1"})
	if err != nil {
		t.Fatal(err)
	}
	lease := &upgrade.DisposableLease{NamespaceUID: id.NamespaceUID, DatabasePodUID: "original-pod-uid", DatabaseName: "owned_live", RestoreDatabaseName: "owned_restored", Owner: "owned-run"}
	fence.execute = func(ctx context.Context, _ Tool, args []string) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(args) < 7 || args[0] != "--kubeconfig" || args[1] != config || args[2] != "--context" || args[3] != id.Context {
			t.Fatal("explicit context/reference binding lost")
		}
		if args[4] == "config" {
			return []byte(id.APIServer), nil
		}
		if args[4] != "get" {
			t.Fatal("ownership observation mutated cluster")
		}
		switch args[5] {
		case "namespace":
			return []byte(strings.Join([]string{id.NamespaceUID, id.Namespace, "live"}, "\t")), nil
		case "secret":
			if strings.Contains(args[len(args)-1], ".data") {
				t.Fatal("fence read private release data")
			}
			name := "sh.helm.release.v1." + id.Release + ".v" + strconv.FormatUint(fence.options.Revision, 10)
			if args[6] != name {
				t.Fatal("fence followed latest revision instead of immutable anchor")
			}
			return []byte(strings.Join([]string{id.ReleaseUID, name, id.Namespace, "helm.sh/release.v1", "helm", id.Release, "1", "live"}, "\t")), nil
		case "pod":
			return []byte(strings.Join([]string{lease.DatabasePodUID, fence.options.DatabasePod, id.Namespace, lease.Owner, "live"}, "\t")), nil
		default:
			t.Fatal("unexpected ownership read")
		}
		return nil, errors.New("unreachable fixture")
	}
	return fence, lease
}

func TestOwnershipFenceRetainsOriginalRevisionAcrossUpgrade(t *testing.T) {
	fence, lease := fenceFixture(t)
	// A newly deployed revision is deliberately not consulted. The original
	// history Secret remains the anchor before apply, observe and rollback.
	for _, stage := range []upgrade.Stage{upgrade.DryRun, upgrade.Apply, upgrade.Observe, upgrade.Rollback} {
		t.Run(string(stage), func(t *testing.T) {
			if err := fence.Fence(context.Background(), fence.options.Identity, lease); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOwnershipFenceRejectsRecreatedOrDeletingResources(t *testing.T) {
	for _, resource := range []string{"namespace", "secret", "pod"} {
		for _, change := range []string{"uid", "deleting"} {
			t.Run(resource+"/"+change, func(t *testing.T) {
				fence, lease := fenceFixture(t)
				read := fence.execute
				fence.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
					raw, err := read(ctx, tool, args)
					if err == nil && args[4] == "get" && args[5] == resource {
						fields := strings.Split(string(raw), "\t")
						if change == "uid" {
							fields[0] = "recreated-uid"
						} else {
							fields[len(fields)-1] = "deleted"
						}
						raw = []byte(strings.Join(fields, "\t"))
					}
					return raw, err
				}
				if err := fence.Fence(context.Background(), fence.options.Identity, lease); err == nil {
					t.Fatal("lost resource ownership admitted")
				}
			})
		}
	}
}

func TestOwnershipFenceRejectsOwnerMismatchAndIncompleteDisposableLease(t *testing.T) {
	fence, lease := fenceFixture(t)
	read := fence.execute
	fence.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
		raw, err := read(ctx, tool, args)
		if err == nil && args[4] == "get" && args[5] == "pod" {
			raw = []byte(strings.Replace(string(raw), lease.Owner, "another-run", 1))
		}
		return raw, err
	}
	if err := fence.Fence(context.Background(), fence.options.Identity, lease); err == nil {
		t.Fatal("unowned database pod admitted")
	}
	fence.execute = read
	lease.RestoreDatabaseName = lease.DatabaseName
	if err := fence.Fence(context.Background(), fence.options.Identity, lease); err == nil {
		t.Fatal("non-disposable database identity admitted")
	}
}

func TestOwnershipFencePreservesCancellationAndInputBinding(t *testing.T) {
	fence, _ := fenceFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	read := fence.execute
	fence.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
		raw, err := read(ctx, tool, args)
		if args[4] == "get" && args[5] == "secret" {
			cancel()
		}
		return raw, err
	}
	if err := fence.Fence(ctx, fence.options.Identity, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("context-ignoring final read published expired ownership")
	}
	fence.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
		raw, err := read(ctx, tool, args)
		if args[4] == "get" && args[5] == "secret" {
			if err := os.WriteFile(fence.options.Kubeconfig, []byte("changed private operator input"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return raw, err
	}
	if err := fence.Fence(context.Background(), fence.options.Identity, nil); err == nil {
		t.Fatal("changed private input binding admitted")
	}
}

func TestOwnershipFenceRejectsPrivateInputChangesBetweenStages(t *testing.T) {
	for _, change := range []string{"bytes", "inode"} {
		t.Run(change, func(t *testing.T) {
			fence, _ := fenceFixture(t)
			read := fence.execute
			calls := 0
			fence.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
				calls++
				return read(ctx, tool, args)
			}
			if err := fence.Fence(context.Background(), fence.options.Identity, nil); err != nil {
				t.Fatal(err)
			}
			previousCalls := calls
			path := fence.options.Kubeconfig
			if change == "bytes" {
				if err := os.WriteFile(path, []byte("changed between-stage operator input"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				replacement := path + ".replacement"
				if err := os.WriteFile(replacement, raw, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(replacement, path); err != nil {
					t.Fatal(err)
				}
			}
			if err := fence.Fence(context.Background(), fence.options.Identity, nil); err == nil {
				t.Fatal("between-stage private input change re-admitted")
			}
			if calls != previousCalls {
				t.Fatal("changed private input reached the external command boundary")
			}
		})
	}
}
