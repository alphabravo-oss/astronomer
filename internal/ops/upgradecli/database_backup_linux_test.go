//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
)

func databaseBackupFixture(t *testing.T) (*SourceDatabaseBackup, *int) {
	t.Helper()
	fence, lease := fenceFixture(t)
	original := fence.execute
	observations := 0
	fence.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
		if len(args) > 5 && args[4] == "get" && args[5] == "pod" {
			if args[len(args)-1] != "go-template="+databasePodTemplate {
				t.Fatal("source pod did not use exact legacy release membership")
			}
			return []byte(strings.Join([]string{lease.DatabasePodUID, fence.options.DatabasePod, fence.options.Identity.Namespace, fence.options.Identity.Release, "postgres", "live"}, "\t")), nil
		}
		if len(args) > 4 && args[4] == "exec" {
			observations++
			if args[len(args)-1] != databaseStateScript {
				t.Fatal("unqualified database observation argv")
			}
			return []byte("owned_live|18|1|78|f\n"), nil
		}
		return original(ctx, tool, args)
	}
	backup, err := NewSourceDatabaseBackup(fence, DatabaseBackupOptions{Database: "owned_live", RestoreDatabase: "astronomer_upgrade_verify_fixture", DatabasePodUID: lease.DatabasePodUID, Owner: "owned-run", ExpectedMajor: 18, MinimumSchema: 66, TargetSchema: 78, ArtifactPolicy: ArtifactPolicy{MaximumBytes: 1 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	return backup, &observations
}

func TestDatabaseDumpPreservesBoundRemoteCredentialExpansionAndSourceIdentity(t *testing.T) {
	backup, observations := databaseBackupFixture(t)
	calls := 0
	backup.capture = func(ctx context.Context, tool Tool, args []string, policy ArtifactPolicy) (*process.ArtifactCapture, process.Result, error) {
		calls++
		if tool != backup.fence.kubectl || policy != backup.options.ArtifactPolicy || args[len(args)-1] != databaseDumpScript || args[4] != "exec" || args[5] != "--namespace" || args[7] != backup.fence.options.DatabasePod {
			t.Fatal("database dump argv differs")
		}
		if strings.Contains(strings.Join(args, " "), "database_url") {
			t.Fatal("local database credentials leaked into child argv")
		}
		result := artifactFixture(t, backup.fence.commands.options.CaptureDirectory, "kubectl-001.stdout", []byte("synthetic archive bytes, not a PostgreSQL archive"))
		return result.StdoutArtifact, result, nil
	}
	retained, result, err := backup.Dump(context.Background())
	if err != nil || retained == nil || calls != 1 || *observations != 2 || retained.schema.Version != 78 || result.StdoutArtifact == nil {
		t.Fatal("bound dump capture failed", err)
	}
	if retained.source != backup {
		t.Fatal("retained backup lost original source binding")
	}
}

func TestDatabaseDumpRejectsStaleSourceOrSchemaAndRetainsFailedArtifact(t *testing.T) {
	for _, change := range []string{"pod", "major", "dirty", "multiple", "version", "canceled", "changed-after-dump", "capture-failure"} {
		t.Run(change, func(t *testing.T) {
			backup, observations := databaseBackupFixture(t)
			original := backup.fence.execute
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			backup.fence.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
				raw, err := original(ctx, tool, args)
				if err != nil {
					return nil, err
				}
				if args[4] == "get" && args[5] == "pod" && change == "pod" {
					raw = []byte("recreated\towned-postgres-1\towned-namespace\tastronomer\tpostgres\tlive")
				}
				if args[4] == "exec" {
					switch change {
					case "major":
						raw = []byte("owned_live|17|1|78|f")
					case "dirty":
						raw = []byte("owned_live|18|1|78|t")
					case "multiple":
						raw = []byte("owned_live|18|2|78|f")
					case "version":
						raw = []byte("owned_live|18|1|79|f")
					case "canceled":
						cancel()
					case "changed-after-dump":
						if *observations == 2 {
							raw = []byte("owned_live|18|1|77|f")
						}
					}
				}
				return raw, nil
			}
			backup.capture = func(context.Context, Tool, []string, ArtifactPolicy) (*process.ArtifactCapture, process.Result, error) {
				calls++
				result := artifactFixture(t, backup.fence.commands.options.CaptureDirectory, "kubectl-001.stdout", []byte("retained partial fixture"))
				if change == "capture-failure" {
					result.Kind = process.ChildFailed
					result.StdoutArtifact.Acknowledged = false
					return nil, result, errors.New("synthetic dump failure")
				}
				return result.StdoutArtifact, result, nil
			}
			retained, result, err := backup.Dump(ctx)
			if err == nil || retained != nil {
				t.Fatal("invalid source admitted dump backup")
			}
			want := 0
			if change == "changed-after-dump" || change == "capture-failure" {
				want = 1
				if result.StdoutArtifact == nil {
					t.Fatal("private failed artifact discarded")
				}
			}
			if calls != want {
				t.Fatal("invalid source launched dump")
			}
		})
	}
}

func TestDatabaseSchemaProjectionRejectsMalformedOrOutOfRangeObservations(t *testing.T) {
	for _, raw := range []string{"other|18|1|78|f", "owned_live|18|0|78|f", "owned_live|18|2|78|f", "owned_live|18|1|78|t", "owned_live|18|1|65|f", "owned_live|18|1|79|f", "owned_live|018|1|78|f", "owned_live|18|1|078|f", "owned_live|18|1|4294967296|f", "owned_live|18|1|78|f\r\n", "owned_live|18|1|78|f\n\n", strings.Repeat("x", 257)} {
		if _, err := parseDatabaseState([]byte(raw), "owned_live", 66, 78); err == nil {
			t.Fatal("malformed schema projection admitted")
		}
	}
	for _, major := range []uint32{16, 17, 18, 19} {
		backup, _ := databaseBackupFixture(t)
		options := backup.options
		options.ExpectedMajor = major
		if _, err := NewSourceDatabaseBackup(backup.fence, options); err != nil {
			t.Fatal("explicit historical/future server binding rejected before observation")
		}
	}
}

func TestDatabaseBackupConstructorRejectsAmbiguousSourceAndTemporaryNames(t *testing.T) {
	for _, change := range []string{"source-name", "temp-name", "same-name", "temp-prefix", "pod-uid", "owner", "schema", "major", "budget"} {
		t.Run(change, func(t *testing.T) {
			backup, _ := databaseBackupFixture(t)
			options := backup.options
			switch change {
			case "source-name":
				options.Database = "live;DROP"
			case "temp-name":
				options.RestoreDatabase = "../owned"
			case "same-name":
				options.RestoreDatabase = options.Database
			case "temp-prefix":
				options.RestoreDatabase = "someone_else"
			case "pod-uid":
				options.DatabasePodUID = ""
			case "owner":
				options.Owner = ""
			case "schema":
				options.MinimumSchema = options.TargetSchema + 1
			case "major":
				options.ExpectedMajor = 0
			case "budget":
				options.ArtifactPolicy.MaximumBytes = 0
			}
			if _, err := NewSourceDatabaseBackup(backup.fence, options); err == nil {
				t.Fatal("ambiguous database backup binding admitted")
			}
		})
	}
}
