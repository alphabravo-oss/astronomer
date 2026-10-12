package scanner

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/events"
)

type summaryBeginSpy struct{ calls int }

func (s *summaryBeginSpy) Begin(context.Context) (pgx.Tx, error) {
	s.calls++
	return nil, errors.New("unexpected transaction begin")
}

func TestIngestRejectsUnrepresentableSummaryBeforeSideEffects(t *testing.T) {
	for _, field := range []string{"critical", "high", "medium", "low", "unknown"} {
		for _, value := range []int64{-1, int64(math.MaxInt32) + 1} {
			for _, transactional := range []bool{false, true} {
				t.Run(field+"/"+map[bool]string{false: "direct", true: "transactional"}[transactional]+"/"+map[bool]string{false: "negative", true: "overflow"}[value > 0], func(t *testing.T) {
					q, recorder, beginner := newFakeQuerier(), newRecorder(), &summaryBeginSpy{}
					auditCalls := 0
					var tx TxBeginner
					if transactional {
						tx = beginner
					}
					ing := NewIngester(q, tx, recorder, func(context.Context, uuid.UUID, string, string) { auditCalls++ })
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					bus := events.NewBus()
					messages := bus.Subscribe(ctx, events.AcceptAll)
					ing.SetEventBus(bus)
					report := sampleReport("invalid-summary", 0)
					fields := map[string]*int{"critical": &report.Report.Summary.CriticalCount, "high": &report.Report.Summary.HighCount, "medium": &report.Report.Summary.MediumCount, "low": &report.Report.Summary.LowCount, "unknown": &report.Report.Summary.UnknownCount}
					*fields[field] = int(value)
					if err := ing.Ingest(ctx, uuid.New(), report); err == nil {
						t.Fatal("invalid summary accepted")
					}
					if beginner.calls != 0 || q.upsertCalls != 0 || q.deleteCalls != 0 || q.batchCalls != 0 || auditCalls != 0 || len(recorder.totals) != 0 || len(recorder.outcomes) != 0 {
						t.Fatalf("invalid summary crossed boundary: begin=%d upsert=%d delete=%d batch=%d audit=%d metrics=%v/%v", beginner.calls, q.upsertCalls, q.deleteCalls, q.batchCalls, auditCalls, recorder.totals, recorder.outcomes)
					}
					select {
					case <-messages:
						t.Fatal("invalid summary published an event")
					default:
					}
				})
			}
		}
	}
}

func TestIngestPreservesRepresentableUpstreamSummaryWithoutCVERows(t *testing.T) {
	q := newFakeQuerier()
	report := sampleReport("valid-summary", 0)
	report.Report.Summary = TrivySummary{CriticalCount: math.MaxInt32, HighCount: 2, MediumCount: 3, LowCount: 4, UnknownCount: 5}
	if err := NewIngester(q, nil, newRecorder(), nil).Ingest(context.Background(), uuid.New(), report); err != nil {
		t.Fatal(err)
	}
	stored := q.onlyReport(t)
	if stored.CriticalCount != math.MaxInt32 || stored.HighCount != 2 || stored.MediumCount != 3 || stored.LowCount != 4 || stored.UnknownCount != 5 {
		t.Fatalf("valid upstream summary changed: %+v", stored)
	}
}

// The tunnel supplies authenticatedClusterID separately from the mirrored
// object. A small unstructured CRD can carry an oversized count; neither it
// nor a payload-selected cluster may cross the persistence boundary.
func TestIngestUnstructuredSummaryBoundaryPreservesAuthenticatedScope(t *testing.T) {
	authenticatedClusterID, payloadClusterID := uuid.New(), uuid.New()
	q, beginner := newFakeQuerier(), &summaryBeginSpy{}
	raw := map[string]any{
		"metadata":   map[string]any{"name": "small-summary"},
		"cluster_id": payloadClusterID.String(),
		"report":     map[string]any{"summary": map[string]any{"criticalCount": int64(math.MaxInt32) + 1}},
	}
	if err := NewIngester(q, beginner, nil, nil).IngestUnstructured(context.Background(), authenticatedClusterID, raw); err == nil {
		t.Fatal("oversized unstructured summary accepted")
	}
	if beginner.calls != 0 || q.upsertCalls != 0 || q.deleteCalls != 0 || q.batchCalls != 0 {
		t.Fatal("oversized unstructured summary reached persistence")
	}
	raw["report"] = map[string]any{"summary": map[string]any{"criticalCount": 7}}
	if err := NewIngester(q, nil, nil, nil).IngestUnstructured(context.Background(), authenticatedClusterID, raw); err != nil {
		t.Fatal(err)
	}
	stored := q.onlyReport(t)
	if stored.ClusterID != authenticatedClusterID || stored.CriticalCount != 7 {
		t.Fatal("payload selected scope or valid upstream count changed")
	}
}
