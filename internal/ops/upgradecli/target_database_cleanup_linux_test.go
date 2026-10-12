//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

type targetCleanupFixture struct {
	temporary     *TemporaryDatabase
	target        *TargetDatabaseObservation
	state         *targetDatabaseFixtureState
	exists, owned bool
	oid           string
	drops         int
	dropError     error
	beforeDrop    func()
}

func targetCleanupFixtureForTest(t *testing.T) *targetCleanupFixture {
	t.Helper()
	source, target, state := targetDatabaseFixture(t)
	if err := target.Capture(context.Background()); err != nil {
		t.Fatal(err)
	}
	capture := artifactFixture(t, source.fence.commands.options.CaptureDirectory, "retained-original.dump", []byte("retained synthetic recovery; not PostgreSQL archive"))
	backup := &DatabaseBackup{source: source, artifact: *capture.StdoutArtifact, schema: DatabaseSchemaState{Major: 18, Version: 78}}
	v, err := NewTemporaryDatabase(backup)
	if err != nil {
		t.Fatal(err)
	}
	v.creationAttempted = true
	v.oid = 700
	fixture := &targetCleanupFixture{temporary: v, target: target, state: state, exists: true, owned: true, oid: "700"}
	previous := source.fence.execute
	source.fence.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
		contains := func(script string) bool {
			for _, arg := range args {
				if arg == script {
					return true
				}
			}
			return false
		}
		if contains(temporaryIdentityScript) {
			if args[len(args)-2] != source.options.RestoreDatabase || args[len(args)-1] != v.marker {
				t.Fatal("target cleanup lost exact temporary identity")
			}
			if !fixture.exists {
				return nil, nil
			}
			if !fixture.owned {
				return []byte(fixture.oid + "|t|f\n"), nil
			}
			return []byte(fixture.oid + "|t|t\n"), nil
		}
		if contains(temporaryDropScript) {
			fixture.drops++
			if args[7] != source.fence.options.DatabasePod || args[8] != "--container=postgres" || args[len(args)-3] != source.options.RestoreDatabase || args[len(args)-2] != v.marker || args[len(args)-1] != fixture.oid {
				t.Fatal("target DROP lost exact Pod/container/name/OID/marker")
			}
			if fixture.beforeDrop != nil {
				fixture.beforeDrop()
			}
			if fixture.dropError != nil {
				return nil, fixture.dropError
			}
			fixture.exists = false
			return nil, nil
		}
		return previous(ctx, tool, args)
	}
	return fixture
}

func TestTargetTemporaryCleanupPreservesOriginalPodFenceAndRetiresOnlyOwnedOID(t *testing.T) {
	f := targetCleanupFixtureForTest(t)
	f.state.podUID = "same-storage-new-pod"
	if f.temporary.Cleanup(context.Background()) == nil || f.drops != 0 {
		t.Fatal("original source cleanup admitted replacement Pod")
	}
	if err := f.temporary.cleanupWithTarget(context.Background(), f.target); err != nil || f.drops != 1 || f.exists {
		t.Fatal("owned target temporary cleanup failed", err)
	}
	if _, err := os.Stat(f.temporary.backup.artifact.Path); err != nil {
		t.Fatal("original retained backup removed", err)
	}
	if f.temporary.oid != 700 {
		t.Fatal("acknowledged temporary identity discarded")
	}
}

func TestTargetTemporaryCleanupRefusesChangedStorageMarkerOIDAndForeignObserver(t *testing.T) {
	for _, change := range []string{"controller", "claim", "volume", "marker", "oid", "foreign", "unattempted", "uncaptured", "dirty"} {
		t.Run(change, func(t *testing.T) {
			f := targetCleanupFixtureForTest(t)
			target := f.target
			switch change {
			case "controller":
				f.state.controllerUID = "recreated"
			case "claim":
				f.state.claimUID = "recreated"
			case "volume":
				f.state.volumeUID = "recreated"
			case "marker":
				f.owned = false
			case "oid":
				f.oid = "701"
			case "foreign":
				_, target, _ = targetDatabaseFixture(t)
			case "unattempted":
				f.temporary.creationAttempted = false
			case "uncaptured":
				target = &TargetDatabaseObservation{source: f.target.source}
			case "dirty":
				f.state.schema = "owned_live|18|1|78|t"
			}
			if f.temporary.cleanupWithTarget(context.Background(), target) == nil || f.drops != 0 {
				t.Fatal("unbound target temporary DROP admitted")
			}
			if _, err := os.Stat(f.temporary.backup.artifact.Path); err != nil {
				t.Fatal("failed cleanup lost backup")
			}
		})
	}
}

func TestTargetTemporaryCleanupRetainsBackupOnFailedUnknownOrCanceledCommand(t *testing.T) {
	for _, change := range []string{"failed", "outcome-unknown", "canceled", "storage-changed-after-drop"} {
		t.Run(change, func(t *testing.T) {
			f := targetCleanupFixtureForTest(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failure := errors.New("synthetic retained command failure")
			switch change {
			case "failed":
				f.dropError = failure
			case "outcome-unknown":
				f.dropError = failure
				f.beforeDrop = func() { f.exists = false }
			case "canceled":
				f.beforeDrop = cancel
			case "storage-changed-after-drop":
				f.beforeDrop = func() { f.state.volumeUID = "recreated" }
			}
			if err := f.temporary.cleanupWithTarget(ctx, f.target); err == nil || f.drops != 1 {
				t.Fatal("failed/unknown cleanup acknowledged")
			}
			if f.temporary.oid != 700 {
				t.Fatal("unknown cleanup lost retained identity")
			}
			if _, err := os.Stat(f.temporary.backup.artifact.Path); err != nil {
				t.Fatal("unknown cleanup lost original backup")
			}
		})
	}
}

func TestTargetDatabaseWiringKeepsOriginalConstructorAndRejectsOwnerMismatch(t *testing.T) {
	observation, _ := releaseStateFixture(t)
	f := observation.release.fence
	f.options.DatabasePod = "owned-postgres-0"
	source, err := NewSourceDatabaseBackup(f, DatabaseBackupOptions{Database: "owned_live", RestoreDatabase: "astronomer_upgrade_verify_fixture", DatabasePodUID: "initial-pod", Owner: "owned-run", ExpectedMajor: 18, MinimumSchema: 66, TargetSchema: 78, ArtifactPolicy: ArtifactPolicy{MaximumBytes: 1 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	observation.source = source
	readyz := &ReadyzObservation{release: observation.release}
	legacy, err := NewTargetWorkloads(observation, readyz)
	if err != nil || legacy.database != nil {
		t.Fatal("original target constructor changed", err)
	}
	target, err := NewTargetDatabaseObservation(source)
	if err != nil {
		t.Fatal(err)
	}
	current, err := NewTargetWorkloadsWithDatabase(observation, readyz, target)
	if err != nil || current.database != target {
		t.Fatal("co-owned target database composition failed", err)
	}
	if _, err := NewTargetWorkloadsWithDatabase(observation, readyz, &TargetDatabaseObservation{source: &SourceDatabaseBackup{}}); err == nil {
		t.Fatal("foreign target observer admitted")
	}
	if err := observation.confirmTarget(context.Background(), &TargetDatabaseObservation{source: &SourceDatabaseBackup{}}); err == nil {
		t.Fatal("foreign observer reached actual release read")
	}
	backup := &ReleaseBackup{source: source}
	if err := backup.cleanupWithTarget(context.Background(), target); err != nil {
		t.Fatal("missing temporary handle did not preserve recovery")
	}
	if strings.Contains(source.arguments(false, databaseStateScript)[7], "replacement") {
		t.Fatal("original source arguments rewritten")
	}
}
