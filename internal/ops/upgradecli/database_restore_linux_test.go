//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
)

type temporaryRestoreFixture struct {
	verifier                        *TemporaryDatabase
	exists                          bool
	oid                             string
	owned                           bool
	created, restores, drops, lists int
	schema                          string
	creationError                   error
	restoreError                    error
	dropError                       error
}

func temporaryFixture(t *testing.T) *temporaryRestoreFixture {
	t.Helper()
	source, _ := databaseBackupFixture(t)
	result := artifactFixture(t, source.fence.commands.options.CaptureDirectory, "kubectl-001.stdout", []byte("synthetic retained bytes; not a PostgreSQL archive"))
	backup := &DatabaseBackup{source: source, artifact: *result.StdoutArtifact, schema: DatabaseSchemaState{Major: 18, Version: 78}}
	verifier, err := NewTemporaryDatabase(backup)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &temporaryRestoreFixture{verifier: verifier, oid: "700", owned: true, schema: "astronomer_upgrade_verify_fixture|18|1|78|f\n"}
	original := source.fence.execute
	source.fence.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
		if tool != source.fence.kubectl {
			t.Fatal("unbound kubectl")
		}
		contains := func(want string) bool {
			for _, a := range args {
				if a == want {
					return true
				}
			}
			return false
		}
		switch {
		case contains(temporaryIdentityScript):
			if args[len(args)-2] != source.options.RestoreDatabase || args[len(args)-1] != verifier.marker {
				t.Fatal("temporary identity binding differs")
			}
			if !fixture.exists {
				return nil, nil
			}
			if !fixture.owned {
				return []byte(fixture.oid + "|t|f\n"), nil
			}
			return []byte(fixture.oid + "|t|t\n"), nil
		case contains(temporaryCreateScript):
			if fixture.exists {
				t.Fatal("create attempted on existing database")
			}
			fixture.created++
			fixture.exists = true
			return nil, fixture.creationError
		case contains(temporarySchemaScript):
			return []byte(fixture.schema), nil
		case contains(temporaryDropScript):
			fixture.drops++
			if args[len(args)-3] != source.options.RestoreDatabase || args[len(args)-2] != verifier.marker || args[len(args)-1] != fixture.oid {
				t.Fatal("drop lacks exact name/marker/OID")
			}
			if fixture.dropError != nil {
				return nil, fixture.dropError
			}
			fixture.exists = false
			return nil, nil
		default:
			return original(ctx, tool, args)
		}
	}
	verifier.input = func(ctx context.Context, tool Tool, args []string, input *process.ArtifactCapture, maximum int64) ([]byte, process.Result, error) {
		if tool != source.fence.kubectl || *input != backup.artifact || maximum != source.options.ArtifactPolicy.MaximumBytes || args[7] != "-i" {
			t.Fatal("retained input, stdin or tool binding differs")
		}
		if args[len(args)-1] == archiveListScript {
			fixture.lists++
			return []byte("synthetic archive listing fixture"), process.Result{}, nil
		}
		if args[len(args)-3] != temporaryRestoreScript || args[len(args)-1] != source.options.RestoreDatabase {
			t.Fatal("restore argv differs")
		}
		fixture.restores++
		return nil, process.Result{}, fixture.restoreError
	}
	return fixture
}

func TestTemporaryRestoreVerifiesArchiveOwnedIdentityAndExactCleanSchema(t *testing.T) {
	f := temporaryFixture(t)
	if err := f.verifier.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.lists != 1 || f.created != 1 || f.restores != 1 || f.drops != 0 || f.verifier.oid != 700 || !f.verifier.restored {
		t.Fatal("required verification stages omitted")
	}
	original := f.verifier.backup.artifact
	if err := f.verifier.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.exists || f.drops != 1 || f.verifier.backup.artifact != original {
		t.Fatal("cleanup did not preserve backup or remove only temporary database")
	}
	if err := f.verifier.Verify(context.Background()); err == nil || f.created != 1 {
		t.Fatal("attempted database silently replayed")
	}
}

func TestTemporaryRestoreRejectsUnownedRecreatedOrMismatchedSchema(t *testing.T) {
	for _, which := range []string{"existing", "unmarked", "recreated", "dirty", "major", "schema", "disappeared"} {
		t.Run(which, func(t *testing.T) {
			f := temporaryFixture(t)
			original := f.verifier.input
			switch which {
			case "existing":
				f.exists = true
			case "unmarked":
				f.owned = false
			case "dirty":
				f.schema = "astronomer_upgrade_verify_fixture|18|1|78|t"
			case "major":
				f.schema = "astronomer_upgrade_verify_fixture|17|1|78|f"
			case "schema":
				f.schema = "astronomer_upgrade_verify_fixture|18|1|77|f"
			}
			f.verifier.input = func(ctx context.Context, tool Tool, args []string, input *process.ArtifactCapture, max int64) ([]byte, process.Result, error) {
				raw, result, err := original(ctx, tool, args, input, max)
				if f.restores == 1 && which == "recreated" {
					f.oid = "701"
				}
				if f.restores == 1 && which == "disappeared" {
					f.exists = false
				}
				return raw, result, err
			}
			if err := f.verifier.Verify(context.Background()); err == nil || f.verifier.restored {
				t.Fatal("invalid verification admitted")
			}
			if f.drops != 0 {
				t.Fatal("verification error automatically dropped recovery material")
			}
		})
	}
}

func TestTemporaryRestoreUnknownCreationAndFailedRestoreRequireFreshReconciliation(t *testing.T) {
	for _, which := range []string{"ack-lost-owned", "ack-lost-unmarked", "restore-failed", "drop-failed", "recreated-before-cleanup"} {
		t.Run(which, func(t *testing.T) {
			f := temporaryFixture(t)
			switch which {
			case "ack-lost-owned", "ack-lost-unmarked":
				f.creationError = errors.New("synthetic joined child acknowledgement lost")
			case "restore-failed":
				f.restoreError = errors.New("synthetic restore failed")
			}
			if which == "ack-lost-unmarked" {
				f.owned = false
			}
			err := f.verifier.Verify(context.Background())
			if which == "drop-failed" || which == "recreated-before-cleanup" {
				if err != nil {
					t.Fatal(err)
				}
				if which == "drop-failed" {
					f.dropError = errors.New("synthetic joined cleanup failure")
				} else {
					f.oid = "701"
				}
			} else if err == nil {
				t.Fatal("failed mutation reported success")
			}
			artifact := f.verifier.backup.artifact
			if err := f.verifier.Cleanup(context.Background()); which == "ack-lost-unmarked" || which == "drop-failed" || which == "recreated-before-cleanup" {
				if err == nil || !f.exists {
					t.Fatal("unresolved/foreign cleanup did not preserve database")
				}
			} else if err != nil || f.exists {
				t.Fatal("fresh owned reconciliation could not clean exact temporary database", err)
			}
			if f.verifier.backup.artifact != artifact {
				t.Fatal("private backup removed on failed/unknown outcome")
			}
			if which == "ack-lost-unmarked" && f.drops != 0 {
				t.Fatal("unmarked CREATE result authorized DROP")
			}
		})
	}
}

func TestTemporaryRestoreCancellationAndArchiveFailurePreventCreation(t *testing.T) {
	for _, which := range []string{"canceled", "archive-failed", "cancel-after-list"} {
		t.Run(which, func(t *testing.T) {
			f := temporaryFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			original := f.verifier.input
			f.verifier.input = func(ctx context.Context, tool Tool, args []string, input *process.ArtifactCapture, max int64) ([]byte, process.Result, error) {
				if which == "archive-failed" {
					return nil, process.Result{}, errors.New("synthetic invalid archive")
				}
				raw, result, err := original(ctx, tool, args, input, max)
				if which == "cancel-after-list" {
					cancel()
				}
				return raw, result, err
			}
			if which == "canceled" {
				cancel()
			}
			err := f.verifier.Verify(ctx)
			if err == nil || f.created != 0 || f.restores != 0 || f.drops != 0 {
				t.Fatal("invalid archive or canceled context mutated database")
			}
			if which != "archive-failed" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation identity lost", err)
			}
		})
	}
}

func TestTemporaryDatabaseIdentityParserAndConstructorFailClosed(t *testing.T) {
	for _, raw := range []string{"0|t|t", "0700|t|t", "700|f|t", "700|t|f", "700|t|t\n\n", "700|t|t\r\n", "700|t|t|extra", strings.Repeat("x", 129)} {
		if _, _, err := parseTemporaryIdentity([]byte(raw)); err == nil {
			t.Fatal("malformed identity accepted")
		}
	}
	if _, exists, err := parseTemporaryIdentity(nil); err != nil || exists {
		t.Fatal("absence became owned database")
	}
	if _, err := NewTemporaryDatabase(nil); err == nil {
		t.Fatal("nil backup accepted")
	}
	f := temporaryFixture(t)
	copy := *f.verifier.backup
	copy.artifact.Acknowledged = false
	if _, err := NewTemporaryDatabase(&copy); err == nil {
		t.Fatal("unacknowledged capture became restore input")
	}
	copy = *f.verifier.backup
	copy.source.options.Owner = "invalid\nowner"
	if _, err := NewTemporaryDatabase(&copy); err == nil {
		t.Fatal("unsafe marker accepted")
	}
}
