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

func TestRunLegacyUpgradeHelpAndUsageNeverPrepareOrPublish(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
		help bool
	}{{[]string{"--help"}, 0, true}, {[]string{"--unknown"}, 2, false}, {nil, 2, false}} {
		result, err := RunLegacyUpgrade(nil, tc.args, InvocationOptions{}, "unbound-evidence")
		if result.Help != tc.help || result.ExitCode != tc.code || result.Report.Published || result.Report.Path != "" || result.CloseError != nil || result.PublicationError != nil || (err != nil) == tc.help {
			t.Fatal("early legacy result reached concrete lifecycle", result, err)
		}
	}
}

func TestRunLegacyUpgradeActualVerifiedBackupFailureRetainsOriginalExitAndMode(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		mode  upgrade.Mode
	}{{"default-preview", nil, upgrade.Preview}, {"yes-execute", []string{"--yes"}, upgrade.Execute}, {"dry-overrides-yes", []string{"--yes", "--dry-run-only"}, upgrade.Preview}} {
		t.Run(tc.name, func(t *testing.T) {
			prepared, options, reads := invocationVerifiedRunFixture(t, "")
			if err := prepared.Close(); err != nil {
				t.Fatal(err)
			}
			// Every factory owns distinct captures; immutable tools/chart/trust/server
			// bindings stay identical. Never reuse a prior command's output namespace.
			options.Commands.CaptureDirectory = filepath.Join(t.TempDir(), "captures")
			if err := os.Mkdir(options.Commands.CaptureDirectory, 0700); err != nil {
				t.Fatal(err)
			}
			directory := publicDirectoryFixture(t)
			args := append(append([]string(nil), tc.flags...), options.Plan.Target)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			result, err := RunLegacyUpgrade(ctx, args, options, directory)
			if err == nil || result.Help || result.ExitCode != 94 || result.Result.ExitCode != 94 || result.Result.Mode != tc.mode || result.Result.FailedStage != upgrade.Backup || result.Result.RollbackAttempted || result.CloseError != nil {
				t.Fatal("actual argv/verified Run failure was overwritten", result, err)
			}
			if result.Report.Published || result.Report.Path != "" || result.PublicationError == nil {
				t.Fatal("missing actual readable backup invented public evidence")
			}
			if errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("bounded fixture timed out instead of actual backup failure")
			}
			rows, readErr := os.ReadDir(directory)
			if readErr != nil || len(rows) != 0 {
				t.Fatal("pre-backup failure created a public report", readErr)
			}
			imageReads := reads()
			if len(imageReads) != 7 {
				t.Fatal("legacy runner bypassed actual seven-image crypto verification")
			}
			for _, count := range imageReads {
				if count < 1 {
					t.Fatal("actual image signature transport was not exercised")
				}
			}
		})
	}
}
