package registration

import (
	"context"
	"errors"
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

func TestServiceAdvanceCommitsBeforePublishing(t *testing.T) {
	q := newFakeQuerier()
	clusterID := uuid.New()
	q.seed(clusterID, PhaseCreated, nil)
	publisher := &capturingPublisher{}
	service := New(q, publisher)
	inTransaction := false
	service.SetRunTx(func(ctx context.Context, work func(Querier) error) error {
		inTransaction = true
		if err := work(q); err != nil {
			return err
		}
		if events := publisher.snapshot(); len(events) != 0 {
			t.Fatalf("events published before commit: %v", events)
		}
		inTransaction = false
		return nil
	})

	record, err := service.Advance(context.Background(), clusterID, EventConfirm)
	if err != nil {
		t.Fatal(err)
	}
	if inTransaction || record.RegistrationPhase != string(PhaseAwaitingAgent) {
		t.Fatalf("transaction=%v record phase=%q", inTransaction, record.RegistrationPhase)
	}
	if events := publisher.snapshot(); len(events) != 1 || events[0] != "cluster.registration.phase" {
		t.Fatalf("post-commit events=%v", events)
	}
}

func TestServiceAdvanceDoesNotPublishAfterCommitFailure(t *testing.T) {
	q := newFakeQuerier()
	clusterID := uuid.New()
	q.seed(clusterID, PhaseCreated, nil)
	publisher := &capturingPublisher{}
	service := New(q, publisher)
	commitErr := errors.New("commit failed")
	service.SetRunTx(func(_ context.Context, work func(Querier) error) error {
		if err := work(q); err != nil {
			return err
		}
		return commitErr
	})

	if _, err := service.Advance(context.Background(), clusterID, EventConfirm); !errors.Is(err, commitErr) {
		t.Fatalf("advance error=%v, want %v", err, commitErr)
	}
	if events := publisher.snapshot(); len(events) != 0 {
		t.Fatalf("events published after failed commit: %v", events)
	}
}

func TestServiceMutationsRequireTransactionRunner(t *testing.T) {
	q := newFakeQuerier()
	clusterID := uuid.New()
	q.seed(clusterID, PhaseCreated, nil)
	service := New(q, nil)

	if _, err := service.Advance(context.Background(), clusterID, EventConfirm); !errors.Is(err, ErrTransactionRunnerUnavailable) {
		t.Fatalf("Advance error=%v, want %v", err, ErrTransactionRunnerUnavailable)
	}
	if _, err := service.SetInstallBaseline(context.Background(), clusterID, true); !errors.Is(err, ErrTransactionRunnerUnavailable) {
		t.Fatalf("SetInstallBaseline error=%v, want %v", err, ErrTransactionRunnerUnavailable)
	}
	if _, err := service.WriteStep(context.Background(), clusterID, StepInput{StepName: "cluster_created"}); !errors.Is(err, ErrTransactionRunnerUnavailable) {
		t.Fatalf("WriteStep error=%v, want %v", err, ErrTransactionRunnerUnavailable)
	}
	if _, err := service.UpdateStep(context.Background(), UpdateStepInput{StepID: uuid.New()}); !errors.Is(err, ErrTransactionRunnerUnavailable) {
		t.Fatalf("UpdateStep error=%v, want %v", err, ErrTransactionRunnerUnavailable)
	}
}

func TestServiceRejectsRunnerThatSkipsTransactionCallback(t *testing.T) {
	q := newFakeQuerier()
	clusterID := uuid.New()
	q.seed(clusterID, PhaseCreated, nil)
	service := New(q, nil)
	service.SetRunTx(func(context.Context, func(Querier) error) error { return nil })

	if _, err := service.Advance(context.Background(), clusterID, EventConfirm); !errors.Is(err, errTransactionCallbackNotExecuted) {
		t.Fatalf("Advance error=%v, want %v", err, errTransactionCallbackNotExecuted)
	}
	record, err := q.GetClusterRegistrationRecord(context.Background(), clusterID)
	if err != nil || record.RegistrationPhase != string(PhaseCreated) {
		t.Fatalf("unexecuted callback changed phase: record=%+v err=%v", record, err)
	}
}
