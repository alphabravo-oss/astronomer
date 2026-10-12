package tasks

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
)

type statusCountsQuerier struct {
	*fakePollQuerier
	writes                int
	warnings, errorsCount int32
}

func (q *statusCountsQuerier) MarkSnapshotPhase(ctx context.Context, p sqlc.MarkSnapshotPhaseParams) error {
	q.writes++
	q.warnings, q.errorsCount = p.WarningsCount, p.ErrorsCount
	return q.fakePollQuerier.MarkSnapshotPhase(ctx, p)
}
func (q *statusCountsQuerier) MarkRestorePhase(ctx context.Context, p sqlc.MarkRestorePhaseParams) error {
	q.writes++
	q.warnings, q.errorsCount = p.WarningsCount, p.ErrorsCount
	return q.fakePollQuerier.MarkRestorePhase(ctx, p)
}

func statusCountsFixture(t *testing.T, kind string, warnings, errorsCount int) (func() error, *statusCountsQuerier, <-chan events.Event, *int) {
	t.Helper()
	q := &statusCountsQuerier{fakePollQuerier: newFakePollQuerier()}
	driver := newFakeDriver()
	clusterID := uuid.New()
	snapshot := sqlc.ClusterSnapshot{ID: uuid.New(), ClusterID: clusterID, VeleroName: "numeric-fixture", VeleroNamespace: "velero", Phase: "InProgress"}
	q.snapshots[snapshot.ID] = snapshot
	restore := sqlc.ClusterRestore{ID: uuid.New(), SnapshotID: snapshot.ID, TargetClusterID: clusterID, VeleroName: "numeric-fixture", VeleroNamespace: "velero", Phase: "InProgress"}
	q.restores[restore.ID] = restore
	driver.backupStatus[snapshot.VeleroName] = VeleroBackupStatusSnapshot{Phase: "Completed", Warnings: warnings, Errors: errorsCount}
	driver.restoreStatus[restore.VeleroName] = VeleroRestoreStatusSnapshot{Phase: "Completed", Warnings: warnings, Errors: errorsCount}
	bus := events.NewBus()
	ctx, cancel := context.WithCancel(t.Context())
	stream := bus.Subscribe(ctx, events.AcceptAll)
	t.Cleanup(func() {
		cancel()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			select {
			case _, open := <-stream:
				if !open {
					return
				}
			case <-timer.C:
				t.Error("fixture subscription cleanup did not join")
				return
			}
		}
	})
	outcomes := new(int)
	previous := recordSnapshotOutcome
	recordSnapshotOutcome = func(string, string) { (*outcomes)++ }
	t.Cleanup(func() { recordSnapshotOutcome = previous })
	deps := ClusterSnapshotDeps{Queries: q, Driver: driver, Bus: bus}
	call := func() error { return pollOneSnapshot(t.Context(), deps, snapshot) }
	if kind == "restore" {
		call = func() error { return pollOneRestore(t.Context(), deps, restore) }
	}
	return call, q, stream, outcomes
}

func invalidStatusCounts(t *testing.T, kind string) {
	t.Helper()
	for _, field := range []string{"warnings", "errors"} {
		for _, edge := range []struct {
			name  string
			value int
		}{
			{"negative", -1}, {"overflow", math.MaxInt32 + 1}, {"native-maximum", math.MaxInt},
		} {
			t.Run(field+"/"+edge.name, func(t *testing.T) {
				warnings, errorsCount := 0, 0
				if field == "warnings" {
					warnings = edge.value
				} else {
					errorsCount = edge.value
				}
				call, q, stream, outcomes := statusCountsFixture(t, kind, warnings, errorsCount)
				if err := call(); err == nil {
					t.Fatalf("invalid Velero status counts accepted: writes=%d outcomes=%d", q.writes, *outcomes)
				}
				if q.writes != 0 || *outcomes != 0 {
					t.Fatalf("invalid observation reached effects: writes=%d outcomes=%d", q.writes, *outcomes)
				}
				select {
				case <-stream:
					t.Fatal("invalid observation published a change event")
				default:
				}
			})
		}
	}
}

func TestSnapshotPollRejectsInvalidStatusCountsBeforeEffects(t *testing.T) {
	invalidStatusCounts(t, "snapshot")
}
func TestRestorePollRejectsInvalidStatusCountsBeforeEffects(t *testing.T) {
	invalidStatusCounts(t, "restore")
}

func TestSnapshotPollPreservesRepresentableStatusCounts(t *testing.T) {
	for _, kind := range []string{"snapshot", "restore"} {
		for _, edge := range []struct {
			name                  string
			warnings, errorsCount int
		}{
			{"zero", 0, 0}, {"normal", 2, 1}, {"maximum", math.MaxInt32, math.MaxInt32},
		} {
			t.Run(kind+"/"+edge.name, func(t *testing.T) {
				call, q, stream, outcomes := statusCountsFixture(t, kind, edge.warnings, edge.errorsCount)
				if err := call(); err != nil {
					t.Fatal("representable observation rejected", err)
				}
				if q.writes != 1 || int(q.warnings) != edge.warnings || int(q.errorsCount) != edge.errorsCount {
					t.Fatal("representable observation changed its stored counts")
				}
				wantOutcomes := 0
				if kind == "snapshot" {
					wantOutcomes = 1
				}
				if *outcomes != wantOutcomes {
					t.Fatal("completion outcome behavior changed")
				}
				select {
				case event := <-stream:
					if event.Type != events.TypeSnapshotChanged {
						t.Fatal("valid observation published the wrong event type")
					}
				default:
					t.Fatal("valid changed observation did not publish")
				}
			})
		}
	}
}
