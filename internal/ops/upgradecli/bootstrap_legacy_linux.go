//go:build linux

package upgradecli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/safeconv"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
)

// LegacyBootstrapInputs are explicit application composition inputs. Selection
// comes from the named old wrapper variables captured once; UIDs, revisions,
// database state, signing roots and executable hashes never come from them.
// Kit is an authenticated offline release/toolchain bundle, not a trusted PATH.
type LegacyBootstrapInputs struct {
	Directory          string
	Lookup             func(string) (string, bool)
	Kubeconfig         string
	ToolchainKit       string
	SourceCommit       string
	ChildEnvironment   []process.Environment
	MaximumBackupBytes int64
}

// BootstrapLegacyUpgrade is the concrete executable path for the bundled
// installation. Early help/usage reads no environment, creates no directory and
// does no tool/network/cluster work. All writes belong to a fresh private run;
// source/backup/checkpoint/tool/capture originals remain retained after failures.
// Existing shell remains authoritative until external/replacement/resume and
// full wrapper parity plus real operator stages have been qualified.
func BootstrapLegacyUpgrade(ctx context.Context, args []string, input LegacyBootstrapInputs) (LegacyRunResult, error) {
	parsed, err := ParseLegacyUpgradeArguments(args)
	if err != nil {
		return LegacyRunResult{ExitCode: 2}, err
	}
	if parsed.Help {
		return LegacyRunResult{Help: true, ExitCode: 0}, nil
	}
	if ctx == nil || !filepath.IsAbs(input.Directory) || !filepath.IsAbs(input.Kubeconfig) || !filepath.IsAbs(input.ToolchainKit) || !bootstrapSourceCommit.MatchString(input.SourceCommit) || input.MaximumBackupBytes < 0 {
		return LegacyRunResult{ExitCode: 1}, errors.New("explicit compiled source/private bootstrap and nonnegative backup selection required")
	}
	selection, err := CaptureLegacyEnvironment(input.Lookup, input.Directory)
	if err != nil {
		return LegacyRunResult{ExitCode: 1}, err
	}
	// Preview/dry-run-only preserve legacy precedence and do not replace a DB.
	if parsed.Mode != upgrade.Execute {
		selection.ReplaceDatabase = false
	}
	if !kubeName.MatchString(selection.Release) || !kubeName.MatchString(selection.Namespace) {
		return LegacyRunResult{ExitCode: 1}, errors.New("release/namespace names are invalid")
	}
	if _, _, err := privateBoundInput(input.Kubeconfig); err != nil {
		return LegacyRunResult{ExitCode: 1}, err
	}
	// A random per-run name is only an exclusivity token, never a cluster/DB
	// authorization claim. Owned SQL still binds exact namespace/pod/OID/marker.
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return LegacyRunResult{ExitCode: 1}, errors.New("create exclusive upgrade run identity")
	}
	runID := hex.EncodeToString(token)
	if err := createPrivateBootstrapParent(selection.BackupRoot); err != nil {
		return LegacyRunResult{ExitCode: 1}, errors.New("create private upgrade parent")
	}
	if err := privateBootstrapDirectory(selection.BackupRoot); err != nil {
		return LegacyRunResult{ExitCode: 1}, err
	}
	available, err := bootstrapFreeKiB(selection.BackupRoot)
	if err != nil {
		return LegacyRunResult{ExitCode: 1}, err
	}
	if selection.MinimumBackupFreeKiB > available {
		return LegacyRunResult{ExitCode: 1}, errors.New("private backup space below requested MIN_BACKUP_FREE_KIB")
	}
	maximum, err := FreshBackupBudget(selection.BackupRoot, input.MaximumBackupBytes)
	if err != nil {
		return LegacyRunResult{ExitCode: 1}, err
	}
	directory := filepath.Join(selection.BackupRoot, selection.Release+"-"+runID)

	if err := os.Mkdir(directory, 0700); err != nil {
		return LegacyRunResult{ExitCode: 1}, errors.New("create exclusive private upgrade run")
	}
	for _, name := range []string{"tools", "captures", "checkpoint", "environment", "release-assets"} {
		if err := os.Mkdir(filepath.Join(directory, name), 0700); err != nil {
			return LegacyRunResult{ExitCode: 1}, errors.New("create exclusive private upgrade child scope")
		}
	}
	bounded, cancel := context.WithTimeout(ctx, selection.Timeout)
	defer cancel()
	toolkit, err := BootstrapTools(bounded, input.ToolchainKit, filepath.Join(directory, "tools"), input.SourceCommit, parsed.Target)
	if err != nil {
		return LegacyRunResult{ExitCode: 1}, err
	}
	environment, err := CaptureLegacyChildEnvironment(bounded, input.Lookup, filepath.Join(directory, "environment"), input.Directory, input.Kubeconfig)
	if err != nil {
		return LegacyRunResult{ExitCode: 1}, err
	}
	if len(input.ChildEnvironment) > 0 {
		return LegacyRunResult{ExitCode: 1}, errors.New("operator child environment must use one captured named binding set")
	}
	commandOptions := CommandOptions{Directory: directory, CaptureDirectory: filepath.Join(directory, "captures"), Environment: environment, Timeout: selection.Timeout, TerminationGrace: time.Second, MaximumCaptureBytes: 16 << 20}
	commands, err := NewCommands(commandOptions)
	if err != nil {
		return LegacyRunResult{ExitCode: 1}, err
	}
	for _, tool := range []Tool{toolkit.Helm, toolkit.Kubectl, toolkit.GH} {
		if err := commands.VerifyTool(bounded, tool); err != nil {
			return LegacyRunResult{ExitCode: 1}, err
		}
	}
	observer, err := NewInitialObserver(commands, toolkit.Helm, toolkit.Kubectl, selection, input.Kubeconfig)
	if err != nil {
		return LegacyRunResult{ExitCode: 1}, err
	}
	initial, err := observer.Observe(bounded, parsed.Target)
	if err != nil {
		return LegacyRunResult{ExitCode: 1}, err
	}
	var replacement *upgrade.DisposableLease
	if selection.ReplaceDatabase {
		leasePath, _ := input.Lookup("UPGRADE_DISPOSABLE_LEASE_FILE")
		if leasePath != "" && !filepath.IsAbs(leasePath) {
			leasePath = filepath.Join(input.Directory, leasePath)
		}
		replacement, err = ReadDisposableReplacementLease(bounded, leasePath, initial)
		if err != nil {
			return LegacyRunResult{ExitCode: 1}, err
		}
	}
	assets := filepath.Join(directory, "release-assets")
	if err := AcquireReleaseAssets(bounded, commands, toolkit.GH, selection, parsed.Target, assets); err != nil {
		return LegacyRunResult{ExitCode: 1}, err
	}
	options := InvocationOptions{Plan: upgrade.Plan{Target: parsed.Target, Previous: initial.CurrentVersion, Mode: parsed.Mode, Identity: initial.Identity, Timeout: selection.Timeout, CleanupTimeout: 2 * time.Minute}, Commands: commandOptions, Helm: toolkit.Helm, Kubectl: toolkit.Kubectl, GH: toolkit.GH, Fence: initial.fenceOptions(input.Kubeconfig), Database: DatabaseBackupOptions{Database: initial.Database, DatabasePodUID: initial.DatabasePodUID, RestoreDatabase: "astronomer_upgrade_verify_" + runID, Owner: "upgrade-" + runID, ExpectedMajor: initial.DatabaseState.Major, ArtifactPolicy: ArtifactPolicy{MaximumBytes: maximum}, DecryptProofWebhookID: selection.DecryptProofWebhookID}, ReleaseDirectory: assets, TrustDirectory: toolkit.TrustDirectory, CheckpointDirectory: filepath.Join(directory, "checkpoint"), ContentPolicy: ArtifactPolicy{MaximumBytes: 64 << 20}, RecoveryPolicy: ArtifactPolicy{MaximumBytes: 64 << 20}, MinimumReadyNodes: selection.MinimumReadyNodes, ChartReference: selection.ChartReference, ReleaseRepository: selection.ReleaseRepository, HealthPort: selection.HealthPort}
	if replacement != nil {
		options.Plan.Replace = replacement
		options.Database.RestoreDatabase = replacement.RestoreDatabaseName
		options.Database.Owner = replacement.Owner
	}
	if initial.CloudNativePG {
		seconds := safeconv.Uint64(selection.Timeout / time.Second)
		if seconds == 0 || seconds > uint64(^uint32(0)) {
			return LegacyRunResult{ExitCode: 1}, errors.New("CNPG verifier wait exceeds supported seconds")
		}
		options.cnpg = initial.cnpg
		options.CNPGRestore = CloudNativePGRestoreOptions{Pod: "astronomer-upgrade-verify-" + runID, Owner: options.Database.Owner, Database: options.Database.RestoreDatabase, MaximumStorageBytes: maximum, WaitSeconds: safeconv.Uint32(selection.Timeout / time.Second)}
	}
	if initial.ExternalDatabase {
		options.ExternalDatabase = &ExternalDatabaseOptions{BackupConfirmed: selection.ExternalBackupConfirmed, SchemaVersion: initial.DatabaseState.Version, ObservedValuesSHA256: initial.DatabaseValuesSHA256}
	}
	if err := selection.MatchesObservedIdentity(options); err != nil {
		return LegacyRunResult{ExitCode: 1}, err
	}
	// Both old requested free space and the stronger maintained capture reserve
	// are admission checks. No numeric narrowing or clipping hides operator input.
	available, err = bootstrapFreeKiB(directory)
	if err != nil {
		return LegacyRunResult{ExitCode: 1}, err
	}
	if selection.MinimumBackupFreeKiB > available {
		return LegacyRunResult{ExitCode: 1}, errors.New("private backup space below requested MIN_BACKUP_FREE_KIB")
	}
	return RunLegacyUpgrade(ctx, args, options, selection.EvidenceDirectory)
}

func bootstrapFreeKiB(directory string) (uint64, error) {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(directory, &fs); err != nil || fs.Bsize <= 0 || fs.Bavail > fs.Blocks {
		return 0, errors.New("private backup capacity unavailable")
	}
	block := uint64(fs.Bsize)
	if fs.Bavail > ^uint64(0)/block {
		return 0, errors.New("private backup capacity exceeds supported arithmetic")
	}
	return fs.Bavail * block / 1024, nil
}
