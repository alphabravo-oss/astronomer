package registration

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

type capturingRegistrationMetrics struct{ phases int }

func (m *capturingRegistrationMetrics) RecordPhaseTransition(string, string, string) { m.phases++ }
func (m *capturingRegistrationMetrics) RecordDuration(string, string, bool, float64) {}

func TestBufferedServiceDefersEventsAndMetricsUntilFlush(t *testing.T) {
	q := newFakeQuerier()
	clusterID := uuid.New()
	q.seed(clusterID, PhaseCreated, nil)
	publisher := &capturingPublisher{}
	metrics := &capturingRegistrationMetrics{}
	service := New(q, publisher)
	service.SetMetricsHook(metrics)

	transactionService, flush := service.Buffered(q)
	if _, err := transactionService.Advance(context.Background(), clusterID, EventConfirm); err != nil {
		t.Fatal(err)
	}
	if len(publisher.snapshot()) != 0 || metrics.phases != 0 {
		t.Fatalf("effects escaped before commit: events=%v metrics=%d", publisher.snapshot(), metrics.phases)
	}
	flush()
	if events := publisher.snapshot(); len(events) != 1 || events[0] != "cluster.registration.phase" {
		t.Fatalf("flushed events=%v", events)
	}
	if metrics.phases != 1 {
		t.Fatalf("flushed phase metrics=%d, want 1", metrics.phases)
	}
}
