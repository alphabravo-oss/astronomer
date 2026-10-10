package observability

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

type generationReaderFixture struct {
	rows          map[uuid.UUID]uuid.UUID
	failure       error
	ignoreContext bool
}

func (r *generationReaderFixture) GetMetricOwnerGeneration(ctx context.Context, params sqlc.GetMetricOwnerGenerationParams) (uuid.UUID, error) {
	if !r.ignoreContext && ctx.Err() != nil {
		return uuid.Nil, ctx.Err()
	}
	if r.failure != nil {
		return uuid.Nil, r.failure
	}
	generation, ok := r.rows[params.OwnerID]
	if !ok {
		return uuid.Nil, errors.New("owned row absent")
	}
	return generation, nil
}
func generationFixture(t *testing.T) *generationReaderFixture {
	t.Helper()
	previous := InstanceID()
	SetInstanceID(t.Name())
	t.Cleanup(func() {
		metricGenerations.Lock()
		for key := range metricGenerations.rows {
			if key.instance == t.Name() {
				retireGeneration(key)
			}
		}
		metricGenerations.Unlock()
		SetInstanceID(previous)
	})
	return &generationReaderFixture{rows: make(map[uuid.UUID]uuid.UUID)}
}
func generationFixtureCapture(t *testing.T, reader *generationReaderFixture, family MetricOwnerFamily, id uuid.UUID) MetricGenerationLease {
	t.Helper()
	lease := CaptureMetricGeneration(context.Background(), reader, family, id)
	if lease.owner == nil {
		t.Fatal("valid generation capture failed")
	}
	return lease
}

func TestMetricGenerationRejectsDeletedRecreatedIDAndLateCallback(t *testing.T) {
	reader := generationFixture(t)
	id := uuid.New()
	reader.rows[id] = uuid.New()
	lease := generationFixtureCapture(t, reader, MetricSIEMForwarder, id)
	writes := 0
	if !lease.Record(context.Background(), func(string, string) { writes++ }) {
		t.Fatal("current callback dropped")
	}
	delete(reader.rows, id)
	if lease.Record(context.Background(), func(string, string) { writes++ }) {
		t.Fatal("deleted callback admitted")
	}
	reader.rows[id] = uuid.New()
	replacement := generationFixtureCapture(t, reader, MetricSIEMForwarder, id)
	if replacement.owner == lease.owner {
		t.Fatal("recreated ID reused captured handle")
	}
	if lease.Record(context.Background(), func(string, string) { writes++ }) {
		t.Fatal("old generation resurrected")
	}
	if !replacement.Record(context.Background(), func(string, string) { writes++ }) || writes != 2 {
		t.Fatal("new incarnation failed to record exactly once")
	}
}

func TestMetricGenerationOrdinaryUpdatesKeepHandleAndCounters(t *testing.T) {
	reader := generationFixture(t)
	id := uuid.New()
	reader.rows[id] = uuid.New()
	first := generationFixtureCapture(t, reader, MetricSSOProvider, id)
	second := generationFixtureCapture(t, reader, MetricSSOProvider, id)
	if first.owner != second.owner {
		t.Fatal("unchanged token reset owner on rename/disable-shaped capture")
	}
	sum := 0
	first.Record(context.Background(), func(string, string) { sum += 2 })
	second.Record(context.Background(), func(string, string) { sum += 3 })
	if sum != 5 {
		t.Fatal("existing current counters were discarded")
	}
}

func TestMetricGenerationRejectsOldReadAfterCompleteRetirement(t *testing.T) {
	reader := generationFixture(t)
	id := uuid.New()
	reader.rows[id] = uuid.New()
	pending := BeginMetricGenerationLookup(MetricCluster)
	snapshot := BeginMetricGenerationLookup(MetricCluster)
	if !snapshot.Publish(nil, time.Now()) {
		t.Fatal("complete empty observation failed")
	}
	if pending.Bind(reader, MetricGenerationRow{id, reader.rows[id]}).owner != nil {
		t.Fatal("unbound pre-retirement read resurrected owner")
	}
}

func TestMetricGenerationProtectsReadmissionAfterSnapshotWatermark(t *testing.T) {
	reader := generationFixture(t)
	id := uuid.New()
	reader.rows[id] = uuid.New()
	first := generationFixtureCapture(t, reader, MetricCluster, id)
	snapshot := BeginMetricGenerationLookup(MetricCluster)
	recent := generationFixtureCapture(t, reader, MetricCluster, id)
	if first.owner != recent.owner {
		t.Fatal("readmission reset a current owner")
	}
	if !snapshot.Publish(nil, time.Now()) {
		t.Fatal("complete older observation failed")
	}
	if !recent.Record(context.Background(), func(string, string) {}) {
		t.Fatal("older absent snapshot retired fresh existing-owner admission")
	}
}

func TestMetricGenerationEnforcesAdmissionCapAcrossIncompleteSnapshots(t *testing.T) {
	reader := generationFixture(t)
	metricGenerations.Lock()
	prior := metricGenerations.counts[MetricClusterGroup]
	metricGenerations.Unlock()
	for i := prior; i < MetricOwnerMax; i++ {
		id := uuid.New()
		reader.rows[id] = uuid.New()
		generationFixtureCapture(t, reader, MetricClusterGroup, id)
	}
	extra := uuid.New()
	reader.rows[extra] = uuid.New()
	if CaptureMetricGeneration(context.Background(), reader, MetricClusterGroup, extra).owner != nil {
		t.Fatal("hard owner registry budget exceeded")
	}
	oversized := make([]MetricGenerationRow, MetricOwnerMax+1)
	if BeginMetricGenerationLookup(MetricClusterGroup).Publish(oversized, time.Now()) {
		t.Fatal("incomplete over-budget snapshot retired prior state")
	}
	if CaptureMetricGeneration(context.Background(), reader, MetricClusterGroup, extra).owner != nil {
		t.Fatal("failed observation bypassed budget")
	}
}

func TestMetricGenerationRefusesPartialExpiredAndInvalidValuePublications(t *testing.T) {
	reader := generationFixture(t)
	id := uuid.New()
	reader.rows[id] = uuid.New()
	lease := generationFixtureCapture(t, reader, MetricClusterGroup, id)
	initial := time.Now()
	lookup := BeginMetricGenerationLookup(MetricClusterGroup)
	wrote := 0
	if lookup.PublishValues([]MetricGenerationRow{{id, reader.rows[id]}, {id, reader.rows[id]}}, initial, func(string, string) { wrote++ }) {
		t.Fatal("duplicate observation changed publication")
	}
	if lookup.PublishValues([]MetricGenerationRow{{id, uuid.Nil}}, initial, func(string, string) { wrote++ }) || wrote != 0 {
		t.Fatal("invalid observation partially published")
	}
	reader.failure = errors.New("synthetic incomplete read")
	if lease.Record(context.Background(), func(string, string) { wrote++ }) {
		t.Fatal("failed current-generation lookup recorded")
	}
	reader.failure = nil
	if !lease.Record(context.Background(), func(string, string) { wrote++ }) || wrote != 1 {
		t.Fatal("failed read retired preceding publication")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reader.ignoreContext = true
	if lease.Record(ctx, func(string, string) { wrote++ }) || CaptureMetricGeneration(ctx, reader, MetricClusterGroup, uuid.New()).owner != nil {
		t.Fatal("context-ignoring reader admitted expired update")
	}
}

func TestMetricGenerationOutOfOrderSnapshotsCannotRetireNewPublication(t *testing.T) {
	reader := generationFixture(t)
	id := uuid.New()
	reader.rows[id] = uuid.New()
	older := BeginMetricGenerationLookup(MetricClusterGroup)
	newer := BeginMetricGenerationLookup(MetricClusterGroup)
	updates := 0
	if !newer.PublishValues([]MetricGenerationRow{{id, reader.rows[id]}}, time.Now(), func(string, string) { updates++ }) {
		t.Fatal("complete newest publication failed")
	}
	if older.Publish(nil, time.Now()) || updates != 1 {
		t.Fatal("older snapshot replaced newer current state")
	}
	lease := generationFixtureCapture(t, reader, MetricClusterGroup, id)
	if !lease.Record(context.Background(), func(string, string) {}) {
		t.Fatal("newest published owner was lost")
	}
}

func TestMetricGenerationCaptureStatusIsNotCurrentOwnership(t *testing.T) {
	reader := generationFixture(t)
	id := uuid.New()
	if (MetricGenerationLease{}).IsCaptured() {
		t.Fatal("zero lease reports a captured handle")
	}
	reader.rows[id] = uuid.New()
	lease := generationFixtureCapture(t, reader, MetricCluster, id)
	delete(reader.rows, id)
	if !lease.IsCaptured() {
		t.Fatal("capture status discarded the original handle")
	}
	if lease.Record(context.Background(), func(string, string) { t.Error("deleted generation recorded") }) {
		t.Fatal("capture status was treated as current ownership")
	}
}
