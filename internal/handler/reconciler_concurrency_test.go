package handler

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// Catalog/tools/monitoring reconcilers used to hold a
// per-handler mutex around the whole batch loop, so a single 10-minute
// helm op could stall every other cluster's pending work. After the fix the
// mutex is released after the claim phase and executeOperation is fanned
// out via a bounded semaphore. This test confirms the fan-out actually
// runs in parallel by setting helm.Do to sleep and measuring wall-time.

type parallelToolQueries struct {
	*toolQueryRecorder
	pending []sqlc.ToolOperation

	// mu serializes access to the recorder's append-style fields so the
	// race detector is happy when processPendingOperations dispatches in
	// parallel. Production code uses pgxpool which is already
	// concurrent-safe; the fixture just needs to follow suit for tests.
	mu sync.Mutex
}

func (q *parallelToolQueries) ListPendingToolOperations(context.Context, int32) ([]sqlc.ToolOperation, error) {
	return q.pending, nil
}

func (q *parallelToolQueries) MarkToolOperationRunning(_ context.Context, id uuid.UUID) (sqlc.ToolOperation, error) {
	for _, op := range q.pending {
		if op.ID == id {
			op.Status = "running"
			return op, nil
		}
	}
	return sqlc.ToolOperation{}, nil
}

// CreateToolOperationEvent overrides the recorder method so concurrent
// dispatchers don't race on the events slice. Same goes for the other
// append-style methods executeOperation can reach (CreateInstalledChart,
// DeleteInstalledChart). DeleteInstalledChart on the recorder is a
// no-op; CreateInstalledChart appends to `created`, so it gets the same
// treatment.
func (q *parallelToolQueries) CreateToolOperationEvent(ctx context.Context, arg sqlc.CreateToolOperationEventParams) (sqlc.ToolOperationEvent, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.toolQueryRecorder.CreateToolOperationEvent(ctx, arg)
}

func (q *parallelToolQueries) CreateInstalledChart(ctx context.Context, arg sqlc.CreateInstalledChartParams) (sqlc.InstalledChart, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.toolQueryRecorder.CreateInstalledChart(ctx, arg)
}

type sleepyHelmStub struct {
	sleep time.Duration
	doN   atomic.Int64
}

func (h *sleepyHelmStub) Do(ctx context.Context, clusterID string, msgType protocol.MessageType, payload protocol.HelmRequestPayload) (*protocol.HelmResultPayload, error) {
	h.doN.Add(1)
	t := time.NewTimer(h.sleep)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &protocol.HelmResultPayload{Success: true, Status: "uninstalled"}, nil
}

func (h *sleepyHelmStub) History(context.Context, string, string, string) (*protocol.HelmResultPayload, error) {
	return &protocol.HelmResultPayload{Success: true}, nil
}

func (h *sleepyHelmStub) Status(context.Context, string, string, string) (*protocol.HelmResultPayload, error) {
	return &protocol.HelmResultPayload{}, nil
}

func TestProcessPendingToolOperations_ParallelDispatch(t *testing.T) {
	t.Parallel()

	const (
		numOps      = 8
		concurrency = 4
		opSleep     = 120 * time.Millisecond
	)

	clusterID := uuid.New()
	rec := newToolQueryRecorder(clusterID)
	queries := &parallelToolQueries{toolQueryRecorder: rec}

	// Pre-seed installed rows so the uninstall path skips findInstalledTool
	// and goes directly to DeleteInstalledChart after the slow helm.Do.
	for i := 0; i < numOps; i++ {
		chartID := uuid.New()
		envPayload, err := json.Marshal(toolOperationEnvelope{
			ClusterID:      clusterID.String(),
			ToolSlug:       "tool-" + uuid.NewString()[:8],
			ReleaseName:    "rel-" + uuid.NewString()[:8],
			Namespace:      "ns-" + uuid.NewString()[:8],
			InstalledChart: &chartID,
		})
		if err != nil {
			t.Fatalf("marshal envelope: %v", err)
		}
		queries.pending = append(queries.pending, sqlc.ToolOperation{
			ID:            uuid.New(),
			TargetType:    "tool_installation",
			TargetKey:     clusterID.String() + ":" + uuid.NewString(),
			OperationType: "uninstall",
			Payload:       envPayload,
			Status:        "pending",
		})
	}

	helm := &sleepyHelmStub{sleep: opSleep}
	h := &ToolHandler{
		queries:         queries,
		helm:            helm,
		helmConcurrency: concurrency,
		trigger:         make(chan struct{}, 1),
	}

	start := time.Now()
	h.processPendingOperations(context.Background())
	elapsed := time.Since(start)

	if got := helm.doN.Load(); got != int64(numOps) {
		t.Fatalf("helm.Do invocations = %d, want %d", got, numOps)
	}
	// Serial would be ~ numOps*opSleep = 960ms.
	// Parallel with fan-out 4 would be ~ (numOps/concurrency)*opSleep = 240ms.
	// Allow generous slack for goroutine scheduling on a busy CI host.
	expectedWaves := numOps / concurrency
	upperBound := time.Duration(expectedWaves)*opSleep + opSleep // one wave of slack
	if elapsed > upperBound {
		t.Fatalf("processPendingOperations took %v with %d ops at concurrency=%d (helm.Do sleep=%v); want <= %v — serial regression?",
			elapsed, numOps, concurrency, opSleep, upperBound)
	}
}

// Verifies the effectiveHelmConcurrency normalization for the zero / negative
// inputs that the struct-field knob can take. Cheap but easy to break in
// future refactors of the knob.
func TestEffectiveHelmConcurrency(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want int
	}{
		{0, defaultHelmDispatchConcurrency},
		{-1, defaultHelmDispatchConcurrency},
		{1, 1},
		{16, 16},
	}
	for _, c := range cases {
		if got := effectiveHelmConcurrency(c.in); got != c.want {
			t.Errorf("effectiveHelmConcurrency(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}
