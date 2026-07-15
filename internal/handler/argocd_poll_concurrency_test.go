package handler

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// pollRunningOperations previously ran serial upstream GetApp calls while
// holding h.mu, so one slow/unreachable instance blocked the single reconciler
// goroutine for the full client timeout per op — starving pending-op claiming.
// After the fix the per-op poll work is dispatched through the bounded pool
// outside the lock. Peak concurrent upstream requests > 1 proves the fan-out;
// a serial loop would show exactly 1.
func TestPollRunningOperations_FansOutConcurrently(t *testing.T) {
	var inFlight, maxSeen int32
	h, rec, _ := newArgoCDFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		cur := atomic.AddInt32(&inFlight, 1)
		for {
			m := atomic.LoadInt32(&maxSeen)
			if cur <= m || atomic.CompareAndSwapInt32(&maxSeen, m, cur) {
				break
			}
		}
		time.Sleep(80 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"metadata":{"name":"myapp","uid":"upstream-myapp-uid"},"status":{"sync":{"status":"Synced"},"health":{"status":"Healthy"},"operationState":{"phase":"Running"}}}`))
	})
	h.helmConcurrency = 8

	const n = 8
	ops := make([]sqlc.ArgocdOperation, n)
	for i := range ops {
		ops[i] = sqlc.ArgocdOperation{
			ID:            uuid.New(),
			OperationType: "sync",
			Status:        "running",
			Payload:       mustJSON(t, argocdOperationEnvelope{ApplicationID: rec.app.ID.String(), InstanceID: rec.instance.ID.String(), UpstreamUID: rec.app.UpstreamUid}),
		}
	}
	rec.runningOps = ops

	h.pollRunningOperations(context.Background())

	if maxSeen < 2 {
		t.Fatalf("peak concurrent upstream GetApp = %d, want > 1 (fan-out); a serial poll would show 1", maxSeen)
	}
	// Every claimed running sync op is polled: phase=Running folds into an async
	// progress update, so all n ops must be accounted for.
	if len(rec.progress) != n {
		t.Fatalf("progress updates = %d, want %d (all running ops polled)", len(rec.progress), n)
	}
}
