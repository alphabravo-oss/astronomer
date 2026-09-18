package registration

import (
	"context"
	"errors"
	"fmt"
)

// RunTx executes registration writes on a transaction-bound query set.
// Non-database effects are buffered by Service and emitted only after commit.
type RunTx func(context.Context, func(Querier) error) error

// ErrTransactionRunnerUnavailable prevents registration writes from escaping
// the database transaction that also owns their generated timeline steps.
var ErrTransactionRunnerUnavailable = errors.New("registration transaction runner unavailable")

var errTransactionCallbackNotExecuted = errors.New("registration transaction runner did not execute callback")

type bufferedPublishEvent struct {
	eventType string
	data      any
}

type bufferedPhaseMetric struct {
	clusterID string
	from      string
	to        string
}

type bufferedDurationMetric struct {
	clusterID string
	outcome   string
	baseline  bool
	seconds   float64
}

// bufferedSideEffects captures process-local SSE and metrics effects while a
// caller executes registration writes inside a database transaction. Flush is
// called only after commit, so a rolled-back transition cannot leak a phase or
// timeline event to clients.
type bufferedSideEffects struct {
	pub       Publisher
	metrics   MetricsHook
	events    []bufferedPublishEvent
	phases    []bufferedPhaseMetric
	durations []bufferedDurationMetric
}

func (b *bufferedSideEffects) Publish(eventType string, data any) {
	b.events = append(b.events, bufferedPublishEvent{eventType: eventType, data: data})
}

func (b *bufferedSideEffects) RecordPhaseTransition(clusterID, from, to string) {
	b.phases = append(b.phases, bufferedPhaseMetric{clusterID: clusterID, from: from, to: to})
}

func (b *bufferedSideEffects) RecordDuration(clusterID, outcome string, baseline bool, seconds float64) {
	b.durations = append(b.durations, bufferedDurationMetric{clusterID: clusterID, outcome: outcome, baseline: baseline, seconds: seconds})
}

func (b *bufferedSideEffects) flush() {
	if b.pub != nil {
		for _, event := range b.events {
			b.pub.Publish(event.eventType, event.data)
		}
	}
	if b.metrics != nil {
		for _, phase := range b.phases {
			b.metrics.RecordPhaseTransition(phase.clusterID, phase.from, phase.to)
		}
		for _, duration := range b.durations {
			b.metrics.RecordDuration(duration.clusterID, duration.outcome, duration.baseline, duration.seconds)
		}
	}
}

// Buffered returns a transaction-scoped service that uses q for every read and
// write and buffers non-database effects. The returned flush function must be
// called exactly once after the surrounding transaction commits; it is safe to
// discard on rollback.
func (s *Service) Buffered(q Querier) (*Service, func()) {
	if s == nil {
		return &Service{q: q, transactionScoped: true}, func() {}
	}
	buffer := &bufferedSideEffects{pub: s.pub, metrics: s.metrics}
	return &Service{q: q, pub: buffer, metrics: buffer, transactionScoped: true}, buffer.flush
}

// SetRunTx makes each phase transition and its auto-generated step one atomic
// mutation. Callers already inside a broader transaction use Buffered instead.
func (s *Service) SetRunTx(runTx RunTx) {
	if s != nil {
		s.runTx = runTx
	}
}

func runInTransaction[T any](ctx context.Context, s *Service, work func(*Service) (T, error)) (T, error) {
	var zero T
	if s == nil || s.q == nil {
		return zero, fmt.Errorf("registration service not configured")
	}
	if s.transactionScoped {
		return work(s)
	}
	if s.runTx == nil {
		return zero, ErrTransactionRunnerUnavailable
	}

	var flush func()
	var result T
	executed := false
	err := s.runTx(ctx, func(q Querier) error {
		executed = true
		txService, flushEffects := s.Buffered(q)
		flush = flushEffects
		var workErr error
		result, workErr = work(txService)
		return workErr
	})
	if err != nil {
		return zero, err
	}
	if !executed || flush == nil {
		return zero, errTransactionCallbackNotExecuted
	}
	flush()
	return result, nil
}

func (s *Service) withinTransaction(ctx context.Context, work func(*Service) error) error {
	_, err := runInTransaction(ctx, s, func(txService *Service) (struct{}, error) {
		return struct{}{}, work(txService)
	})
	return err
}
