//go:build linux

package upgradecli

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
)

// These exercise the actual Invocation.Run/Machine/FileStore and joined child
// failure paths after genuine local cryptographic preparation. The tools remain
// explicitly synthetic; no attestation acceptance, official CLI or database
// backup/restore/upgrade success is inferred from their controlled exit codes.
func TestInvocationRunActualJoinedVerificationFailureRetainsCheckpointAndRecovery(t *testing.T) {
	invocation, options, fixture := invocationRunFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result := invocation.Run(ctx)
	if result.ExitCode != 95 || result.Result != "failed" || result.FailedStage != upgrade.Verify || result.CleanupFailed || result.RollbackAttempted {
		t.Fatal("concrete Driver did not retain the joined synthetic attestation failure", result)
	}
	state, err := invocation.store.Load(ctx)
	if err != nil || state == nil || state.Pending != upgrade.Verify || len(state.Completed) != 0 || state.Applied || state.Replaced {
		t.Fatal("actual private checkpoint falsely acknowledged verification or mutation", err)
	}
	if invocation.driver.backup.attempted || invocation.driver.target.database.anchor != nil || invocation.driver.target.database.capturing {
		t.Fatal("failed Verify launched backup or database capture")
	}
	before, err := os.ReadDir(options.Commands.CaptureDirectory)
	if err != nil || len(before) == 0 {
		t.Fatal("joined child originals were not retained", err)
	}
	if replay := invocation.Run(ctx); replay.ExitCode == 0 {
		t.Fatal("failed single-use invocation was replayed", replay)
	}
	after, err := os.ReadDir(options.Commands.CaptureDirectory)
	if err != nil || len(after) != len(before) {
		t.Fatal("second Run launched additional children", err)
	}
	if err := invocation.Close(); err != nil {
		t.Fatal("actual owned close failed", err)
	}
	for _, name := range []string{"release-manifest.json", "release-manifest.sigstore.json", "astronomer-1.2.2.tgz", "RELEASE_IMAGES", "SHA256SUMS"} {
		if _, err := os.Stat(filepath.Join(fixture.directory, name)); err != nil {
			t.Fatal("Run/Close removed retained release input", err)
		}
	}
	for _, name := range []string{"checkpoint.json", "checkpoint.lock"} {
		info, err := os.Lstat(filepath.Join(options.CheckpointDirectory, name))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
			t.Fatal("Run/Close removed private recovery checkpoint", err)
		}
	}
}

func TestInvocationRunActualCancellationCannotLaunchOrReplayConcreteStages(t *testing.T) {
	invocation, options, _ := invocationRunFixture(t)
	before, err := os.ReadDir(options.Commands.CaptureDirectory)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := invocation.Run(ctx)
	if result.ExitCode != 130 || result.Result != "failed" || result.FailedStage != upgrade.Verify {
		t.Fatal("actual canceled invocation did not retain its cancellation outcome", result)
	}
	if replay := invocation.Run(context.Background()); replay.ExitCode == 0 {
		t.Fatal("canceled single-use invocation was replayed", replay)
	}
	after, err := os.ReadDir(options.Commands.CaptureDirectory)
	if err != nil || len(after) != len(before) || invocation.driver.backup.attempted {
		t.Fatal("canceled/replayed Run launched a concrete child or backup", err)
	}
}

func invocationRunFixture(t *testing.T) (*Invocation, InvocationOptions, metadataCryptoFixture) {
	t.Helper()
	address := os.Getenv("ASTRONOMER_HELM_HTTP_PRIVATE_FIXTURE_IP")
	if address == "" {
		t.Skip("owned private canonical-proxy fixture address is not configured")
	}
	ip := net.ParseIP(address)
	if ip == nil || !ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		t.Fatal("owned proxy address invalid")
	}
	fixture := newLocalChartCryptoFixture(t, false, "17")
	policy, repositoryOptions := invocationCanonicalProxy(t, ip, fixture.chart.client)
	options := invocationActualChildren(t, fixture.metadata, policy, repositoryOptions)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	invocation, err := PrepareInvocation(ctx, options)
	if err != nil {
		t.Fatal("actual run fixture preparation failed", err)
	}
	t.Cleanup(func() {
		if err := invocation.Close(); err != nil {
			t.Error("owned invocation cleanup failed", err)
		}
	})
	return invocation, options, fixture.metadata
}
