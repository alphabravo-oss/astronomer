package tasks

import (
	"context"
	"errors"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"sync"
	"testing"
	"time"
)

type capturedGaugeFixture struct {
	mu     sync.Mutex
	values []float64
}

func (*capturedGaugeFixture) Outcome(context.Context, string) {}
func (f *capturedGaugeFixture) InFlight(_ context.Context, value float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values = append(f.values, value)
}
func TestSnapshotGaugeOlderCaptureCannotReplaceNewerZeroingHandle(t *testing.T) {
	owners := newSnapshotMetricGaugeOwners()
	id := uuid.New()
	old, newer := &capturedGaugeFixture{}, &capturedGaugeFixture{}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer func() {
		once.Do(func() { close(release) })
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("owned old capture did not join")
		}
	}()
	go func() {
		defer close(done)
		owners.update(context.Background(), ClusterSnapshotDeps{CaptureMetrics: func(context.Context, uuid.UUID) SnapshotMetricRecorder { close(entered); <-release; return old }}, map[string]int{id.String(): 2}, owners.serial.Add(1))
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("old capture not reached")
	}
	owners.update(context.Background(), ClusterSnapshotDeps{CaptureMetrics: func(context.Context, uuid.UUID) SnapshotMetricRecorder { return newer }}, map[string]int{id.String(): 1}, owners.serial.Add(1))
	once.Do(func() { close(release) })
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("old capture did not join")
	}
	owners.update(context.Background(), ClusterSnapshotDeps{CaptureMetrics: func(context.Context, uuid.UUID) SnapshotMetricRecorder { return newer }}, map[string]int{}, owners.serial.Add(1))
	if len(old.values) != 0 || len(newer.values) != 2 || newer.values[0] != 1 || newer.values[1] != 0 {
		t.Fatal("out-of-order capture replaced current handle or zeroed wrong generation")
	}
}
func TestSnapshotGaugePublicationWaitHonorsCancellationAndBatchBudget(t *testing.T) {
	owners := newSnapshotMetricGaugeOwners()
	owners.publish <- struct{}{}
	defer func() { <-owners.publish }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	deps := ClusterSnapshotDeps{CaptureMetrics: func(context.Context, uuid.UUID) SnapshotMetricRecorder { calls++; return &capturedGaugeFixture{} }}
	owners.update(ctx, deps, map[string]int{uuid.NewString(): 1}, owners.serial.Add(1))
	oversized := make(map[string]int)
	for i := 0; i <= snapshotPollBatchSize; i++ {
		oversized[uuid.NewString()] = 1
	}
	owners.update(context.Background(), deps, oversized, owners.serial.Add(1))
	if calls != 0 || len(owners.rows) != 0 {
		t.Fatal("expired or oversized observation performed captures/publication")
	}
}

type heldPendingMetricQuery struct {
	*fakePollQuerier
	entered, release chan struct{}
	fail             bool
}

func (q *heldPendingMetricQuery) ListPendingClusterSnapshots(ctx context.Context, _ int32) ([]sqlc.ClusterSnapshot, error) {
	close(q.entered)
	select {
	case <-q.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if q.fail {
		return nil, errors.New("synthetic owned pending query failure")
	}
	return nil, nil
}
func TestSnapshotGaugeActualOlderPendingQueryCannotOverwriteNewPublication(t *testing.T) {
	owners := newSnapshotMetricGaugeOwners()
	metric := &capturedGaugeFixture{}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	q := &heldPendingMetricQuery{fakePollQuerier: newFakePollQuerier(), entered: entered, release: release}
	capture := func(context.Context, uuid.UUID) SnapshotMetricRecorder { return metric }
	var once sync.Once
	defer func() {
		once.Do(func() { close(release) })
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("owned held query did not join")
		}
	}()
	go func() {
		defer close(done)
		result <- pollSnapshots(context.Background(), ClusterSnapshotDeps{Queries: q, CaptureMetrics: capture, metricGaugeOwners: owners})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("actual pending query not reached")
	}
	current := newFakePollQuerier()
	driver := newFakeDriver()
	id := uuid.New()
	if _, err := current.CreateClusterSnapshot(context.Background(), sqlc.CreateClusterSnapshotParams{ClusterID: id, VeleroName: "owned-current", Phase: "InProgress"}); err != nil {
		t.Fatal("create synthetic pending row")
	}
	driver.backupStatus["owned-current"] = VeleroBackupStatusSnapshot{Phase: "InProgress"}
	if err := pollSnapshots(context.Background(), ClusterSnapshotDeps{Queries: current, Driver: driver, CaptureMetrics: capture, metricGaugeOwners: owners}); err != nil {
		t.Fatal("newer actual worker poll failed")
	}
	once.Do(func() { close(release) })
	select {
	case <-done:
		if <-result != nil {
			t.Fatal("older actual worker poll failed")
		}
	case <-time.After(time.Second):
		t.Fatal("older actual worker poll not joined")
	}
	if len(metric.values) != 1 || metric.values[0] != 1 {
		t.Fatal("old pending query zeroed a newer actual publication")
	}
	// A genuine pending query failure must preserve the publication.
	failed := &heldPendingMetricQuery{fakePollQuerier: newFakePollQuerier(), entered: make(chan struct{}), release: make(chan struct{}), fail: true}
	close(failed.release)
	if err := pollSnapshots(context.Background(), ClusterSnapshotDeps{Queries: failed, CaptureMetrics: capture, metricGaugeOwners: owners}); err == nil {
		t.Fatal("pending query failure not surfaced")
	}
	if len(metric.values) != 1 {
		t.Fatal("failed pending query changed publication")
	}
}
func TestSnapshotGaugeWaitingPublicationGateCancelsAndJoins(t *testing.T) {
	owners := newSnapshotMetricGaugeOwners()
	owners.publish <- struct{}{}
	defer func() { <-owners.publish }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	captured, done := make(chan struct{}), make(chan struct{})
	deps := ClusterSnapshotDeps{CaptureMetrics: func(context.Context, uuid.UUID) SnapshotMetricRecorder {
		close(captured)
		return &capturedGaugeFixture{}
	}}
	go func() {
		defer close(done)
		owners.update(ctx, deps, map[string]int{uuid.NewString(): 1}, owners.serial.Add(1))
	}()
	select {
	case <-captured:
	case <-time.After(time.Second):
		t.Fatal("fresh-context capture was not reached")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("publication wait did not cancel/join while gate remains held")
	}
	if len(owners.rows) != 0 {
		t.Fatal("canceled publication replaced prior state")
	}
}

func TestSnapshotGaugeNilCapturePreservesPriorHandleForEmptyBatch(t *testing.T) {
	owners := newSnapshotMetricGaugeOwners()
	id := uuid.New()
	original := &capturedGaugeFixture{}
	captures := 0
	deps := ClusterSnapshotDeps{CaptureMetrics: func(context.Context, uuid.UUID) SnapshotMetricRecorder {
		captures++
		if captures == 1 {
			return original
		}
		return nil // Transient failure for the same present owner.
	}}
	owners.update(context.Background(), deps, map[string]int{id.String(): 1}, owners.serial.Add(1))
	firstPublication := owners.published
	owners.update(context.Background(), deps, map[string]int{id.String(): 2}, owners.serial.Add(1))
	if owners.published != firstPublication || owners.rows[id.String()] != original || len(original.values) != 1 || original.values[0] != 1 {
		t.Fatal("nil capture replaced the prior publication or remembered generation")
	}
	owners.update(context.Background(), deps, map[string]int{}, owners.serial.Add(1))
	if captures != 2 || len(owners.rows) != 0 || len(original.values) != 2 || original.values[1] != 0 {
		t.Fatal("empty batch failed to zero the original generation without a fresh capture")
	}
}
