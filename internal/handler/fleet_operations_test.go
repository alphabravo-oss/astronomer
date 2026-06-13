package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeFleetOperationQuerier is the narrow surface the fleet handler
// tests exercise. Each method holds the same mu so a single-threaded
// test can race against itself without surprising the assertions.
type fakeFleetOperationQuerier struct {
	mu sync.Mutex

	operations     map[uuid.UUID]sqlc.FleetOperation
	targets        map[uuid.UUID]map[uuid.UUID]sqlc.FleetOperationTarget
	idemOperations []sqlc.CreateFleetOperationIdempotentParams
	idemByKey      map[string]sqlc.FleetOperation
}

func newFakeFleetOperationQuerier() *fakeFleetOperationQuerier {
	return &fakeFleetOperationQuerier{
		operations: map[uuid.UUID]sqlc.FleetOperation{},
		targets:    map[uuid.UUID]map[uuid.UUID]sqlc.FleetOperationTarget{},
		idemByKey:  map[string]sqlc.FleetOperation{},
	}
}

func (f *fakeFleetOperationQuerier) CreateFleetOperation(_ context.Context, arg sqlc.CreateFleetOperationParams) (sqlc.FleetOperation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	op := sqlc.FleetOperation{
		ID:                        uuid.New(),
		Name:                      arg.Name,
		Description:               arg.Description,
		OperationType:             arg.OperationType,
		OperationSpec:             arg.OperationSpec,
		Selector:                  arg.Selector,
		Strategy:                  arg.Strategy,
		MaxConcurrent:             arg.MaxConcurrent,
		OnError:                   arg.OnError,
		RespectMaintenanceWindows: arg.RespectMaintenanceWindows,
		Status:                    "pending",
		CreatedBy:                 arg.CreatedBy,
		CreatedAt:                 now,
		UpdatedAt:                 now,
	}
	f.operations[op.ID] = op
	return op, nil
}

func (f *fakeFleetOperationQuerier) CreateFleetOperationIdempotent(_ context.Context, arg sqlc.CreateFleetOperationIdempotentParams) (sqlc.FleetOperation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.idemOperations = append(f.idemOperations, arg)
	key := arg.Scope + "|" + arg.IdempotencyKey
	if op, ok := f.idemByKey[key]; ok {
		return op, nil
	}
	now := time.Now()
	op := sqlc.FleetOperation{
		ID:                        uuid.New(),
		Name:                      arg.Name,
		Description:               arg.Description,
		OperationType:             arg.OperationType,
		OperationSpec:             arg.OperationSpec,
		Selector:                  arg.Selector,
		Strategy:                  arg.Strategy,
		MaxConcurrent:             arg.MaxConcurrent,
		OnError:                   arg.OnError,
		RespectMaintenanceWindows: arg.RespectMaintenanceWindows,
		Status:                    "pending",
		CreatedBy:                 arg.CreatedBy,
		CreatedAt:                 now,
		UpdatedAt:                 now,
	}
	f.operations[op.ID] = op
	f.idemByKey[key] = op
	return op, nil
}

func (f *fakeFleetOperationQuerier) GetFleetOperation(_ context.Context, id uuid.UUID) (sqlc.FleetOperation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	op, ok := f.operations[id]
	if !ok {
		return sqlc.FleetOperation{}, pgx.ErrNoRows
	}
	return op, nil
}

func (f *fakeFleetOperationQuerier) ListFleetOperations(_ context.Context, arg sqlc.ListFleetOperationsParams) ([]sqlc.FleetOperation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlc.FleetOperation, 0, len(f.operations))
	for _, op := range f.operations {
		if arg.Status.Valid && op.Status != arg.Status.String {
			continue
		}
		out = append(out, op)
	}
	return out, nil
}

func (f *fakeFleetOperationQuerier) CountFleetOperations(_ context.Context, status pgtype.Text) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, op := range f.operations {
		if status.Valid && op.Status != status.String {
			continue
		}
		n++
	}
	return n, nil
}

func (f *fakeFleetOperationQuerier) SetFleetOperationStatus(_ context.Context, arg sqlc.SetFleetOperationStatusParams) (sqlc.FleetOperation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	op, ok := f.operations[arg.ID]
	if !ok {
		return sqlc.FleetOperation{}, pgx.ErrNoRows
	}
	op.Status = arg.Status
	op.LastError = arg.LastError
	op.UpdatedAt = time.Now()
	f.operations[arg.ID] = op
	return op, nil
}

func (f *fakeFleetOperationQuerier) ListFleetOperationTargets(_ context.Context, arg sqlc.ListFleetOperationTargetsParams) ([]sqlc.FleetOperationTarget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.targets[arg.OperationID]
	out := make([]sqlc.FleetOperationTarget, 0, len(m))
	for _, t := range m {
		out = append(out, t)
	}
	return out, nil
}

func (f *fakeFleetOperationQuerier) CountFleetOperationTargets(_ context.Context, operationID uuid.UUID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return int64(len(f.targets[operationID])), nil
}

func (f *fakeFleetOperationQuerier) RequeueFailedTargets(_ context.Context, operationID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.targets[operationID]
	for id, t := range m {
		if t.Status == "failed" {
			t.Status = "pending"
			t.SubOperationID = pgtype.UUID{}
			t.LastError = ""
			m[id] = t
		}
	}
	return nil
}

// captureFleetTrigger counts orchestrator nudges.
type captureFleetTrigger struct {
	mu    sync.Mutex
	count int
}

func (c *captureFleetTrigger) TriggerFleetOrchestrate(_ context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count++
}

func (c *captureFleetTrigger) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

// ─────────────────────────────────────────────────────────────────────
// Tests
// ─────────────────────────────────────────────────────────────────────

// TestFleetOperation_CRUD exercises Create + List + Get on the
// fleet operations endpoints. The orchestrator isn't wired, so the
// row stays at "pending" — that's the right thing to assert on the
// handler tier.
func TestFleetOperation_CRUD(t *testing.T) {
	q := newFakeFleetOperationQuerier()
	h := NewFleetOperationHandler(q)

	body := mustJSON(t, map[string]any{
		"name":           "rolling-cert-manager-upgrade",
		"description":    "Roll cert-manager to v1.14.5 across staging",
		"operation_type": "tool_upgrade",
		"operation_spec": map[string]any{
			"slug":           "cert-manager",
			"target_version": "v1.14.5",
		},
		"selector": map[string]any{
			"matchLabels": map[string]string{"tier": "staging"},
		},
		"max_concurrent": 3,
		"on_error":       "abort",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fleet-operations/", bytes.NewReader(body))
	h.Create(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var createResp struct {
		Data FleetOperationResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if createResp.Data.Name != "rolling-cert-manager-upgrade" {
		t.Errorf("name=%q", createResp.Data.Name)
	}
	if createResp.Data.Status != "pending" {
		t.Errorf("expected pending status, got %q", createResp.Data.Status)
	}
	if createResp.Data.Strategy != "parallel" {
		t.Errorf("expected default strategy parallel, got %q", createResp.Data.Strategy)
	}
	id := createResp.Data.ID

	// List
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/fleet-operations/", nil)
	h.List(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status=%d", rec.Code)
	}
	var listResp struct {
		Data  []FleetOperationResponse `json:"data"`
		Count int64                    `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if listResp.Count != 1 || len(listResp.Data) != 1 {
		t.Errorf("list: count=%d len=%d", listResp.Count, len(listResp.Data))
	}

	// Get
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/fleet-operations/"+id+"/", nil)
	req = withChiParams(req, map[string]string{"id": id})
	h.Get(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestFleetOperationCreateUsesIdempotencyKey(t *testing.T) {
	q := newFakeFleetOperationQuerier()
	h := NewFleetOperationHandler(q)
	body := mustJSON(t, map[string]any{
		"name":           "retryable-fleet-op",
		"operation_type": "tool_upgrade",
		"operation_spec": map[string]any{"slug": "cert-manager"},
		"selector":       map[string]any{"matchLabels": map[string]string{"tier": "staging"}},
	})

	var firstID string
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/fleet-operations/", bytes.NewReader(body))
		req.Header.Set("Idempotency-Key", "fleet-retry-1")
		h.Create(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %d: status=%d body=%s", i, rec.Code, rec.Body.String())
		}
		var resp struct {
			Data FleetOperationResponse `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode %d: %v", i, err)
		}
		if i == 0 {
			firstID = resp.Data.ID
		} else if resp.Data.ID != firstID {
			t.Fatalf("operation id = %s then %s, want replay to return original", firstID, resp.Data.ID)
		}
	}
	if len(q.operations) != 1 {
		t.Fatalf("operations = %d, want one durable row", len(q.operations))
	}
	if len(q.idemOperations) != 2 {
		t.Fatalf("idempotent calls = %d, want 2", len(q.idemOperations))
	}
}

// TestFleetOperation_Validation runs the validator against several
// malformed bodies. The handler must reject all of these with a 400.
func TestFleetOperation_Validation(t *testing.T) {
	q := newFakeFleetOperationQuerier()
	h := NewFleetOperationHandler(q)

	cases := []struct {
		name string
		body map[string]any
		want string
	}{
		{"empty name", map[string]any{
			"operation_type": "tool_upgrade",
			"operation_spec": map[string]any{"slug": "x"},
			"selector":       map[string]any{"matchLabels": map[string]string{"t": "p"}},
		}, "name is required"},
		{"empty selector", map[string]any{
			"name":           "x",
			"operation_type": "tool_upgrade",
			"operation_spec": map[string]any{"slug": "x"},
			"selector":       map[string]any{},
		}, "selector must contain"},
		{"unknown op type", map[string]any{
			"name":           "x",
			"operation_type": "blasterize",
			"selector":       map[string]any{"matchLabels": map[string]string{"t": "p"}},
		}, "not a known fleet operation"},
		{"reserved op type", map[string]any{
			"name":           "x",
			"operation_type": "drain_namespaces",
			"selector":       map[string]any{"matchLabels": map[string]string{"t": "p"}},
		}, "reserved but not yet implemented"},
		{"missing slug", map[string]any{
			"name":           "x",
			"operation_type": "tool_upgrade",
			"operation_spec": map[string]any{},
			"selector":       map[string]any{"matchLabels": map[string]string{"t": "p"}},
		}, "operation_spec.slug is required"},
		{"max_concurrent too big", map[string]any{
			"name":           "x",
			"operation_type": "tool_upgrade",
			"operation_spec": map[string]any{"slug": "x"},
			"selector":       map[string]any{"matchLabels": map[string]string{"t": "p"}},
			"max_concurrent": 999,
		}, "max_concurrent must be"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(mustJSON(t, tc.body)))
			h.Create(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Errorf("want body to mention %q, got %s", tc.want, rec.Body.String())
			}
		})
	}
}

// TestFleetOperation_PauseResume walks the running → paused → running
// state transition. Pause from a non-running status is a 409.
func TestFleetOperation_PauseResume(t *testing.T) {
	q := newFakeFleetOperationQuerier()
	h := NewFleetOperationHandler(q)
	trig := &captureFleetTrigger{}
	h.SetTrigger(trig)

	// Seed a running operation.
	id := uuid.New()
	q.operations[id] = sqlc.FleetOperation{ID: id, Name: "x", Status: "running"}

	// Pause: 202.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = withChiParams(req, map[string]string{"id": id.String()})
	h.Pause(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("pause: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if q.operations[id].Status != "paused" {
		t.Errorf("expected paused, got %q", q.operations[id].Status)
	}

	// Pause again: 409 invalid transition.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/", nil)
	req = withChiParams(req, map[string]string{"id": id.String()})
	h.Pause(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("pause-twice: status=%d, want 409", rec.Code)
	}

	// Resume: 202, status back to running.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/", nil)
	req = withChiParams(req, map[string]string{"id": id.String()})
	h.Resume(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("resume: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if q.operations[id].Status != "running" {
		t.Errorf("expected running, got %q", q.operations[id].Status)
	}
	if trig.Count() < 2 {
		t.Errorf("expected orchestrator to be nudged at least twice, got %d", trig.Count())
	}
}

// TestFleetOperation_Abort verifies abort moves any non-terminal
// status to aborted and refuses to abort an already-completed run.
func TestFleetOperation_Abort(t *testing.T) {
	q := newFakeFleetOperationQuerier()
	h := NewFleetOperationHandler(q)

	id := uuid.New()
	q.operations[id] = sqlc.FleetOperation{ID: id, Name: "x", Status: "running"}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = withChiParams(req, map[string]string{"id": id.String()})
	h.Abort(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("abort: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if q.operations[id].Status != "aborted" {
		t.Errorf("expected aborted, got %q", q.operations[id].Status)
	}

	// Re-abort a terminal-status row is 409.
	id2 := uuid.New()
	q.operations[id2] = sqlc.FleetOperation{ID: id2, Name: "done", Status: "completed"}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/", nil)
	req = withChiParams(req, map[string]string{"id": id2.String()})
	h.Abort(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("abort completed: status=%d, want 409", rec.Code)
	}
}

// TestFleetOperation_RetryFailed verifies the retry-failed endpoint
// re-enqueues every failed target back to pending and resets the
// parent operation status when it was terminal-failed.
func TestFleetOperation_RetryFailed(t *testing.T) {
	q := newFakeFleetOperationQuerier()
	h := NewFleetOperationHandler(q)
	trig := &captureFleetTrigger{}
	h.SetTrigger(trig)

	opID := uuid.New()
	q.operations[opID] = sqlc.FleetOperation{ID: opID, Name: "x", Status: "failed"}
	q.targets[opID] = map[uuid.UUID]sqlc.FleetOperationTarget{}
	t1, t2 := uuid.New(), uuid.New()
	q.targets[opID][t1] = sqlc.FleetOperationTarget{ID: t1, OperationID: opID, Status: "failed", LastError: "boom"}
	q.targets[opID][t2] = sqlc.FleetOperationTarget{ID: t2, OperationID: opID, Status: "completed"}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = withChiParams(req, map[string]string{"id": opID.String()})
	h.RetryFailed(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("retry: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if q.targets[opID][t1].Status != "pending" {
		t.Errorf("failed target not requeued, status=%q", q.targets[opID][t1].Status)
	}
	if q.targets[opID][t2].Status != "completed" {
		t.Errorf("completed target should not be touched, status=%q", q.targets[opID][t2].Status)
	}
	if q.operations[opID].Status != "running" {
		t.Errorf("parent op should be running, got %q", q.operations[opID].Status)
	}
	if trig.Count() == 0 {
		t.Errorf("retry did not nudge orchestrator")
	}
}

// TestFleetOperation_ListTargets exercises the targets pagination
// endpoint.
func TestFleetOperation_ListTargets(t *testing.T) {
	q := newFakeFleetOperationQuerier()
	h := NewFleetOperationHandler(q)

	opID := uuid.New()
	q.operations[opID] = sqlc.FleetOperation{ID: opID, Name: "x", Status: "running"}
	q.targets[opID] = map[uuid.UUID]sqlc.FleetOperationTarget{}
	for i := 0; i < 3; i++ {
		tID := uuid.New()
		q.targets[opID][tID] = sqlc.FleetOperationTarget{
			ID:          tID,
			OperationID: opID,
			ClusterID:   uuid.New(),
			Status:      "pending",
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParams(req, map[string]string{"id": opID.String()})
	h.ListTargets(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data  []FleetOperationTargetResponse `json:"data"`
		Count int64                          `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Count != 3 || len(resp.Data) != 3 {
		t.Errorf("expected 3 targets, got count=%d len=%d", resp.Count, len(resp.Data))
	}
}

// TestFleetOperation_ListTargets_404 ensures the handler 404s when
// the operation doesn't exist (rather than returning an empty list).
func TestFleetOperation_ListTargets_404(t *testing.T) {
	q := newFakeFleetOperationQuerier()
	h := NewFleetOperationHandler(q)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParams(req, map[string]string{"id": uuid.NewString()})
	h.ListTargets(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", rec.Code)
	}
}
