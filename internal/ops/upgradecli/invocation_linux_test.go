//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
)

func TestPrepareInvocationRejectsUnboundModesBeforeToolOrPrivateWrites(t *testing.T) {
	root := t.TempDir()
	plan := upgrade.Plan{Target: "v1.3.0", Previous: "v1.2.0", Mode: upgrade.Preview, Identity: upgrade.Identity{Context: "owned", APIServer: "https://kubernetes.example.test", Namespace: "astronomer", NamespaceUID: "namespace-original", Release: "astronomer", ReleaseUID: "revision-original"}, Timeout: time.Minute, CleanupTimeout: time.Second}
	options := InvocationOptions{Plan: plan, Helm: Tool{Kind: Helm}, Kubectl: Tool{Kind: Kubectl}, GH: Tool{Kind: GitHubCLI}, Fence: FenceOptions{Identity: plan.Identity}, ReleaseDirectory: root, TrustDirectory: root, CheckpointDirectory: root, ContentPolicy: ArtifactPolicy{MaximumBytes: 1024}, RecoveryPolicy: ArtifactPolicy{MaximumBytes: 1024}, Database: DatabaseBackupOptions{ArtifactPolicy: ArtifactPolicy{MaximumBytes: 1024}}}
	for _, change := range []string{"replacement", "identity", "tool-kind", "relative-checkpoint", "no-content-budget", "no-dump-budget"} {
		t.Run(change, func(t *testing.T) {
			candidate := options
			switch change {
			case "replacement":
				candidate.Plan.Mode = upgrade.Execute
				candidate.Plan.Replace = &upgrade.DisposableLease{NamespaceUID: plan.Identity.NamespaceUID, DatabasePodUID: "pod-original", DatabaseName: "astronomer", RestoreDatabaseName: "restore", Owner: "fixture"}
			case "identity":
				candidate.Fence.Identity.NamespaceUID = "recreated"
			case "tool-kind":
				candidate.GH.Kind = Helm
			case "relative-checkpoint":
				candidate.CheckpointDirectory = "checkpoint"
			case "no-content-budget":
				candidate.ContentPolicy.MaximumBytes = 0
			case "no-dump-budget":
				candidate.Database.ArtifactPolicy.MaximumBytes = 0
			}
			if _, err := PrepareInvocation(context.Background(), candidate); err == nil {
				t.Fatal("partial operator bindings admitted")
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatal("invalid admission wrote private resources", err)
			}
			if _, err := os.Stat(filepath.Join(root, "checkpoint.lock")); !os.IsNotExist(err) {
				t.Fatal("invalid admission acquired checkpoint")
			}
		})
	}
}

func TestPrepareInvocationPreservesCancellationBeforeOwnedCommands(t *testing.T) {
	plan := upgrade.Plan{Target: "v1.3.0", Previous: "v1.2.0", Mode: upgrade.Preview, Identity: upgrade.Identity{Context: "owned", APIServer: "https://kubernetes.example.test", Namespace: "astronomer", NamespaceUID: "namespace-original", Release: "astronomer", ReleaseUID: "revision-original"}, Timeout: time.Minute, CleanupTimeout: time.Second}
	options := InvocationOptions{Plan: plan, Helm: Tool{Kind: Helm}, Kubectl: Tool{Kind: Kubectl}, GH: Tool{Kind: GitHubCLI}, Fence: FenceOptions{Identity: plan.Identity}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PrepareInvocation(ctx, options); !errors.Is(err, context.Canceled) {
		t.Fatal("initial cancellation identity lost", err)
	}
	if _, err := PrepareInvocation(nil, options); err == nil {
		t.Fatal("nil preparation context admitted")
	}
}

func TestInvocationDoesNotReplayAfterCloseOrAdmitPartialDriver(t *testing.T) {
	var absent *Invocation
	if absent.Close() != nil || absent.Run(context.Background()).ExitCode == 0 {
		t.Fatal("nil invocation admitted")
	}
	zero := &Invocation{plan: upgrade.Plan{Mode: upgrade.Preview}}
	if zero.Run(nil).ExitCode == 0 || zero.Run(context.Background()).ExitCode == 0 {
		t.Fatal("unbound invocation ran")
	}
	if zero.Close() != nil || zero.Close() != nil || zero.Run(context.Background()).ExitCode == 0 {
		t.Fatal("closed invocation replayed")
	}
}
