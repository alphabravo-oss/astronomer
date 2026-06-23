package handler

import (
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// recordingEnqueuer captures the options passed to Enqueue so the
// gitops-unique-taskid fix can be asserted: the argocd auto-register
// re-enqueue must dedup on a stable TaskID keyed by cluster id rather than
// asynq.Unique, which would never collapse because the enriched payload
// carries a per-request correlation_id.
type recordingEnqueuer struct {
	mu   sync.Mutex
	opts []asynq.Option
}

func (e *recordingEnqueuer) Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.opts = append([]asynq.Option(nil), opts...)
	return &asynq.TaskInfo{}, nil
}

func TestEnqueueArgoCDAutoRegister_UsesStableTaskID(t *testing.T) {
	rec := &recordingEnqueuer{}
	h := &ClusterHandler{}
	h.SetArgoCDRefreshQueue(rec)

	clusterID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	req := httptest.NewRequest("PUT", "/api/v1/clusters/"+clusterID.String()+"/", nil)

	h.enqueueArgoCDAutoRegister(req, clusterID)

	var taskID string
	for _, o := range rec.opts {
		switch o.Type() {
		case asynq.TaskIDOpt:
			taskID, _ = o.Value().(string)
		case asynq.UniqueOpt:
			t.Fatalf("auto-register re-enqueue must not use asynq.Unique; the per-request correlation_id in the payload defeats payload-based dedup")
		}
	}

	want := "argocd-auto-register:" + clusterID.String()
	if taskID != want {
		t.Fatalf("task id = %q, want %q", taskID, want)
	}
}
