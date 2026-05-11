package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// stubLoggingRBACQuerier returns canned bindings for the RBAC tests below.
type stubLoggingRBACQuerier struct {
	bindings []rbac.RoleBinding
	err      error
}

func (s stubLoggingRBACQuerier) GetUserBindings(context.Context, string) ([]rbac.RoleBinding, error) {
	return s.bindings, s.err
}

// loggingFakeQuerier is a minimal in-memory implementation of LoggingQuerier
// sufficient for the handler enqueue + reconciler tests below. We don't try
// to model every column; we just record enough to assert state transitions.
type loggingFakeQuerier struct {
	outputs    map[uuid.UUID]sqlc.LoggingOutput
	pipelines  map[uuid.UUID]sqlc.LoggingPipeline
	operations map[uuid.UUID]sqlc.LoggingOperation
	events     []sqlc.LoggingOperationEvent
	// orderedOps tracks insertion order so ListPending behaves deterministically.
	orderedOps []uuid.UUID
}

func newLoggingFakeQuerier() *loggingFakeQuerier {
	return &loggingFakeQuerier{
		outputs:    map[uuid.UUID]sqlc.LoggingOutput{},
		pipelines:  map[uuid.UUID]sqlc.LoggingPipeline{},
		operations: map[uuid.UUID]sqlc.LoggingOperation{},
	}
}

// --- LoggingQuerier outputs ---

func (q *loggingFakeQuerier) ListLoggingOutputs(_ context.Context, _ sqlc.ListLoggingOutputsParams) ([]sqlc.LoggingOutput, error) {
	items := make([]sqlc.LoggingOutput, 0, len(q.outputs))
	for _, o := range q.outputs {
		items = append(items, o)
	}
	return items, nil
}
func (q *loggingFakeQuerier) ListOutputsByCluster(_ context.Context, arg sqlc.ListOutputsByClusterParams) ([]sqlc.LoggingOutput, error) {
	items := make([]sqlc.LoggingOutput, 0, len(q.outputs))
	for _, o := range q.outputs {
		if o.ClusterID == arg.ClusterID {
			items = append(items, o)
		}
	}
	return items, nil
}
func (q *loggingFakeQuerier) GetLoggingOutputByID(_ context.Context, id uuid.UUID) (sqlc.LoggingOutput, error) {
	if o, ok := q.outputs[id]; ok {
		return o, nil
	}
	return sqlc.LoggingOutput{}, errors.New("not found")
}
func (q *loggingFakeQuerier) CreateLoggingOutput(_ context.Context, arg sqlc.CreateLoggingOutputParams) (sqlc.LoggingOutput, error) {
	o := sqlc.LoggingOutput{
		ID:            uuid.New(),
		Name:          arg.Name,
		OutputType:    arg.OutputType,
		Configuration: arg.Configuration,
		ClusterID:     arg.ClusterID,
		Enabled:       arg.Enabled,
		CreatedByID:   arg.CreatedByID,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	q.outputs[o.ID] = o
	return o, nil
}
func (q *loggingFakeQuerier) UpdateLoggingOutput(_ context.Context, arg sqlc.UpdateLoggingOutputParams) (sqlc.LoggingOutput, error) {
	o, ok := q.outputs[arg.ID]
	if !ok {
		return sqlc.LoggingOutput{}, errors.New("not found")
	}
	o.Name = arg.Name
	o.OutputType = arg.OutputType
	o.Configuration = arg.Configuration
	o.Enabled = arg.Enabled
	o.UpdatedAt = time.Now()
	q.outputs[arg.ID] = o
	return o, nil
}
func (q *loggingFakeQuerier) DeleteLoggingOutput(_ context.Context, id uuid.UUID) error {
	delete(q.outputs, id)
	return nil
}
func (q *loggingFakeQuerier) CountLoggingOutputs(context.Context) (int64, error) {
	return int64(len(q.outputs)), nil
}

// --- LoggingQuerier pipelines ---

func (q *loggingFakeQuerier) ListLoggingPipelines(context.Context, sqlc.ListLoggingPipelinesParams) ([]sqlc.LoggingPipeline, error) {
	items := make([]sqlc.LoggingPipeline, 0, len(q.pipelines))
	for _, p := range q.pipelines {
		items = append(items, p)
	}
	return items, nil
}
func (q *loggingFakeQuerier) ListPipelinesByCluster(context.Context, sqlc.ListPipelinesByClusterParams) ([]sqlc.LoggingPipeline, error) {
	return q.ListLoggingPipelines(context.Background(), sqlc.ListLoggingPipelinesParams{})
}
func (q *loggingFakeQuerier) GetLoggingPipelineByID(_ context.Context, id uuid.UUID) (sqlc.LoggingPipeline, error) {
	if p, ok := q.pipelines[id]; ok {
		return p, nil
	}
	return sqlc.LoggingPipeline{}, errors.New("not found")
}
func (q *loggingFakeQuerier) CreateLoggingPipeline(_ context.Context, arg sqlc.CreateLoggingPipelineParams) (sqlc.LoggingPipeline, error) {
	p := sqlc.LoggingPipeline{
		ID:          uuid.New(),
		Name:        arg.Name,
		ClusterID:   arg.ClusterID,
		Namespaces:  arg.Namespaces,
		Labels:      arg.Labels,
		Filters:     arg.Filters,
		Enabled:     arg.Enabled,
		CreatedByID: arg.CreatedByID,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	q.pipelines[p.ID] = p
	return p, nil
}
func (q *loggingFakeQuerier) UpdateLoggingPipeline(_ context.Context, arg sqlc.UpdateLoggingPipelineParams) (sqlc.LoggingPipeline, error) {
	p, ok := q.pipelines[arg.ID]
	if !ok {
		return sqlc.LoggingPipeline{}, errors.New("not found")
	}
	p.Name = arg.Name
	p.Namespaces = arg.Namespaces
	p.Labels = arg.Labels
	p.Filters = arg.Filters
	p.Enabled = arg.Enabled
	q.pipelines[arg.ID] = p
	return p, nil
}
func (q *loggingFakeQuerier) DeleteLoggingPipeline(_ context.Context, id uuid.UUID) error {
	delete(q.pipelines, id)
	return nil
}
func (q *loggingFakeQuerier) CountLoggingPipelines(context.Context) (int64, error) {
	return int64(len(q.pipelines)), nil
}

// --- LoggingQuerier operations ---

func (q *loggingFakeQuerier) CreateLoggingOperation(_ context.Context, arg sqlc.CreateLoggingOperationParams) (sqlc.LoggingOperation, error) {
	op := sqlc.LoggingOperation{
		ID:            uuid.New(),
		TargetType:    arg.TargetType,
		TargetKey:     arg.TargetKey,
		OperationType: arg.OperationType,
		Payload:       arg.Payload,
		Status:        arg.Status,
		CreatedByID:   arg.CreatedByID,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	q.operations[op.ID] = op
	q.orderedOps = append(q.orderedOps, op.ID)
	return op, nil
}
func (q *loggingFakeQuerier) GetLoggingOperation(_ context.Context, id uuid.UUID) (sqlc.LoggingOperation, error) {
	if op, ok := q.operations[id]; ok {
		return op, nil
	}
	return sqlc.LoggingOperation{}, errors.New("not found")
}
func (q *loggingFakeQuerier) ListLoggingOperations(context.Context, sqlc.ListLoggingOperationsParams) ([]sqlc.LoggingOperation, error) {
	items := make([]sqlc.LoggingOperation, 0, len(q.orderedOps))
	// Most recent first to match real query semantics.
	for i := len(q.orderedOps) - 1; i >= 0; i-- {
		items = append(items, q.operations[q.orderedOps[i]])
	}
	return items, nil
}
func (q *loggingFakeQuerier) ListPendingLoggingOperations(_ context.Context, _ int32) ([]sqlc.LoggingOperation, error) {
	items := []sqlc.LoggingOperation{}
	for _, id := range q.orderedOps {
		op := q.operations[id]
		if op.Status == "pending" || op.Status == "running" {
			items = append(items, op)
		}
	}
	return items, nil
}
func (q *loggingFakeQuerier) MarkLoggingOperationRunning(_ context.Context, id uuid.UUID) (sqlc.LoggingOperation, error) {
	op := q.operations[id]
	op.Status = "running"
	op.AttemptCount++
	op.StartedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	op.UpdatedAt = time.Now()
	q.operations[id] = op
	return op, nil
}
func (q *loggingFakeQuerier) MarkLoggingOperationCompleted(_ context.Context, id uuid.UUID) (sqlc.LoggingOperation, error) {
	op := q.operations[id]
	op.Status = "completed"
	op.CompletedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	op.UpdatedAt = time.Now()
	q.operations[id] = op
	return op, nil
}
func (q *loggingFakeQuerier) MarkLoggingOperationFailed(_ context.Context, arg sqlc.MarkLoggingOperationFailedParams) (sqlc.LoggingOperation, error) {
	op := q.operations[arg.ID]
	op.Status = "failed"
	op.ErrorMessage = arg.ErrorMessage
	op.CompletedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	op.UpdatedAt = time.Now()
	q.operations[arg.ID] = op
	return op, nil
}
func (q *loggingFakeQuerier) MarkLoggingOperationSuperseded(_ context.Context, arg sqlc.MarkLoggingOperationSupersededParams) (sqlc.LoggingOperation, error) {
	op := q.operations[arg.ID]
	op.Status = "superseded"
	op.ErrorMessage = arg.ErrorMessage
	op.CompletedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	op.UpdatedAt = time.Now()
	q.operations[arg.ID] = op
	return op, nil
}
func (q *loggingFakeQuerier) RequeueLoggingOperation(_ context.Context, id uuid.UUID) (sqlc.LoggingOperation, error) {
	op := q.operations[id]
	op.Status = "pending"
	op.StartedAt = pgtype.Timestamptz{}
	op.CompletedAt = pgtype.Timestamptz{}
	op.ErrorMessage = ""
	op.UpdatedAt = time.Now()
	q.operations[id] = op
	return op, nil
}
func (q *loggingFakeQuerier) CreateLoggingOperationEvent(_ context.Context, arg sqlc.CreateLoggingOperationEventParams) (sqlc.LoggingOperationEvent, error) {
	ev := sqlc.LoggingOperationEvent{
		ID:          uuid.New(),
		OperationID: arg.OperationID,
		Level:       arg.Level,
		Stage:       arg.Stage,
		Message:     arg.Message,
		Detail:      arg.Detail,
		CreatedAt:   time.Now(),
	}
	q.events = append(q.events, ev)
	return ev, nil
}
func (q *loggingFakeQuerier) ListLoggingOperationEvents(_ context.Context, operationID uuid.UUID) ([]sqlc.LoggingOperationEvent, error) {
	out := []sqlc.LoggingOperationEvent{}
	for _, ev := range q.events {
		if ev.OperationID == operationID {
			out = append(out, ev)
		}
	}
	return out, nil
}

// --- Test K8s requester ---

type loggingFakeRequester struct {
	calls    []loggingFakeCall
	failOn   string // method+path prefix that should return 500
	respMap  map[string]*protocol.K8sResponsePayload
	defaultS int
}

type loggingFakeCall struct {
	method string
	path   string
	body   []byte
}

func (r *loggingFakeRequester) Do(_ context.Context, _, method, path string, body []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	r.calls = append(r.calls, loggingFakeCall{method: method, path: path, body: append([]byte(nil), body...)})
	key := method + " " + path
	if resp, ok := r.respMap[key]; ok {
		return resp, nil
	}
	if r.failOn != "" && strings.HasPrefix(key, r.failOn) {
		return &protocol.K8sResponsePayload{StatusCode: http.StatusInternalServerError, Body: base64.StdEncoding.EncodeToString([]byte("boom"))}, nil
	}
	status := r.defaultS
	if status == 0 {
		// PATCH on a missing ConfigMap returns 404 so applyNamedResource
		// falls through to POST; POST creating it returns 201.
		if method == http.MethodPatch {
			status = http.StatusNotFound
		} else {
			status = http.StatusCreated
		}
	}
	return &protocol.K8sResponsePayload{StatusCode: status, Body: base64.StdEncoding.EncodeToString([]byte("{}"))}, nil
}

// --- Tests ---

func TestCreateOutputEnqueuesPendingApplyOperation(t *testing.T) {
	q := newLoggingFakeQuerier()
	h := NewLoggingHandler(q)

	clusterID := uuid.New()
	body := map[string]any{
		"name":          "primary-stdout",
		"output_type":   "stdout",
		"configuration": map[string]any{"key": "value"},
		"cluster_id":    clusterID.String(),
		"enabled":       true,
	}
	raw, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/logging/outputs/", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	h.CreateOutput(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(q.operations) != 1 {
		t.Fatalf("expected 1 operation enqueued, got %d", len(q.operations))
	}
	var op sqlc.LoggingOperation
	for _, v := range q.operations {
		op = v
	}
	if op.Status != "pending" {
		t.Fatalf("operation status = %q, want pending", op.Status)
	}
	if op.TargetType != "output" {
		t.Fatalf("target_type = %q, want output", op.TargetType)
	}
	if op.OperationType != "apply" {
		t.Fatalf("operation_type = %q, want apply", op.OperationType)
	}
	var env loggingOperationEnvelope
	if err := json.Unmarshal(op.Payload, &env); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if env.ClusterID != clusterID.String() {
		t.Fatalf("payload cluster_id = %q, want %q", env.ClusterID, clusterID.String())
	}
	if env.OutputType != "stdout" {
		t.Fatalf("payload output_type = %q, want stdout", env.OutputType)
	}
}

func TestDeleteOutputEnqueuesDeleteBeforeRowGone(t *testing.T) {
	q := newLoggingFakeQuerier()
	h := NewLoggingHandler(q)
	clusterID := uuid.New()
	outputID := uuid.New()
	q.outputs[outputID] = sqlc.LoggingOutput{
		ID:            outputID,
		Name:          "drop-me",
		OutputType:    "stdout",
		Configuration: json.RawMessage(`{}`),
		ClusterID:     pgtype.UUID{Bytes: clusterID, Valid: true},
		Enabled:       true,
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/logging/outputs/"+outputID.String()+"/", nil)
	// chi.URLParam needs a route context; install it manually.
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", outputID.String())
	req = req.WithContext(addRouteCtx(req.Context(), rc))
	rec := httptest.NewRecorder()
	h.DeleteOutput(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(q.operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(q.operations))
	}
	var op sqlc.LoggingOperation
	for _, v := range q.operations {
		op = v
	}
	if op.OperationType != "delete" {
		t.Fatalf("operation_type = %q, want delete", op.OperationType)
	}
}

func TestReconcilerAppliesPendingApplyOperationViaConfigMap(t *testing.T) {
	q := newLoggingFakeQuerier()
	h := NewLoggingHandler(q)
	requester := &loggingFakeRequester{respMap: map[string]*protocol.K8sResponsePayload{}}
	h.SetK8sRequester(requester)

	clusterID := uuid.New()
	outputID := uuid.New()
	env := loggingOperationEnvelope{
		ClusterID:     clusterID.String(),
		TargetID:      outputID.String(),
		TargetType:    "output",
		Name:          "primary",
		OutputType:    "stdout",
		Enabled:       true,
		Configuration: json.RawMessage(`{"k":"v"}`),
	}
	payload, _ := json.Marshal(env)
	op, err := q.CreateLoggingOperation(context.Background(), sqlc.CreateLoggingOperationParams{
		TargetType:    "output",
		TargetKey:     outputID.String(),
		OperationType: "apply",
		Payload:       payload,
		Status:        "pending",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	h.processPendingOperations(context.Background())

	completed := q.operations[op.ID]
	if completed.Status != "completed" {
		t.Fatalf("status = %q, want completed (err=%q)", completed.Status, completed.ErrorMessage)
	}
	if len(requester.calls) == 0 {
		t.Fatalf("expected requester to be invoked")
	}
	// Find the POST /configmaps call and verify the body shape.
	foundPost := false
	for _, c := range requester.calls {
		if c.method == http.MethodPost && strings.HasSuffix(c.path, "/configmaps") {
			foundPost = true
			var body map[string]any
			if err := json.Unmarshal(c.body, &body); err != nil {
				t.Fatalf("body decode: %v", err)
			}
			if body["kind"] != "ConfigMap" {
				t.Fatalf("kind = %v, want ConfigMap", body["kind"])
			}
			meta, _ := body["metadata"].(map[string]any)
			if meta["namespace"] != LoggingNamespace {
				t.Fatalf("namespace = %v, want %q", meta["namespace"], LoggingNamespace)
			}
			expectedName := loggingConfigMapName("output", outputID.String())
			if meta["name"] != expectedName {
				t.Fatalf("name = %v, want %q", meta["name"], expectedName)
			}
			data, _ := body["data"].(map[string]any)
			if _, ok := data["meta.json"]; !ok {
				t.Fatalf("data missing meta.json; got keys=%v", keysOf(data))
			}
		}
	}
	if !foundPost {
		t.Fatalf("expected a POST /configmaps call, got: %+v", requester.calls)
	}

	// At least one event should be persisted.
	if len(q.events) == 0 {
		t.Fatal("expected reconciler to emit at least one event")
	}
}

func TestReconcilerFailsOperationWhenRequesterUnconfigured(t *testing.T) {
	q := newLoggingFakeQuerier()
	h := NewLoggingHandler(q)
	// Deliberately do NOT call SetK8sRequester.

	clusterID := uuid.New()
	outputID := uuid.New()
	env := loggingOperationEnvelope{
		ClusterID:  clusterID.String(),
		TargetID:   outputID.String(),
		TargetType: "output",
		Name:       "primary",
	}
	payload, _ := json.Marshal(env)
	op, _ := q.CreateLoggingOperation(context.Background(), sqlc.CreateLoggingOperationParams{
		TargetType:    "output",
		TargetKey:     outputID.String(),
		OperationType: "apply",
		Payload:       payload,
		Status:        "pending",
	})

	h.processPendingOperations(context.Background())

	got := q.operations[op.ID]
	if got.Status != "failed" {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if got.ErrorMessage == "" {
		t.Fatal("expected non-empty error message")
	}
}

// addRouteCtx installs a chi.RouteContext on the request context — needed
// because httptest doesn't run the router that normally fills it in.
func addRouteCtx(ctx context.Context, rc *chi.Context) context.Context {
	return context.WithValue(ctx, chi.RouteCtxKey, rc)
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// seedLoggingOperation persists a pending apply operation for the given
// target cluster + output. Returns the created operation row.
func seedLoggingOperation(t *testing.T, q *loggingFakeQuerier, clusterID uuid.UUID) sqlc.LoggingOperation {
	t.Helper()
	outputID := uuid.New()
	env := loggingOperationEnvelope{
		ClusterID:  clusterID.String(),
		TargetID:   outputID.String(),
		TargetType: "output",
		Name:       "rbac-fixture",
	}
	payload, _ := json.Marshal(env)
	op, err := q.CreateLoggingOperation(context.Background(), sqlc.CreateLoggingOperationParams{
		TargetType:    "output",
		TargetKey:     outputID.String(),
		OperationType: "apply",
		Payload:       payload,
		Status:        "failed",
	})
	if err != nil {
		t.Fatalf("seed operation: %v", err)
	}
	return op
}

// TestListLoggingOperationsFiltersByPerClusterRBAC seeds operations across
// three clusters and asserts that ListOperations drops rows whose target
// cluster the caller can't read. Mirrors the cross-cluster-search RBAC
// filtering test: silently exclude, never 403.
func TestListLoggingOperationsFiltersByPerClusterRBAC(t *testing.T) {
	q := newLoggingFakeQuerier()
	h := NewLoggingHandler(q)

	clusterA := uuid.New()
	clusterB := uuid.New()
	clusterC := uuid.New()

	opA := seedLoggingOperation(t, q, clusterA)
	_ = seedLoggingOperation(t, q, clusterB) // not visible to caller
	_ = seedLoggingOperation(t, q, clusterC) // not visible to caller

	// Caller has logging:read on clusterA only. Unrelated workloads grant
	// on clusterB proves the filter discriminates on resource, not scope.
	h.SetAuthorization(rbac.NewEngine(), stubLoggingRBACQuerier{
		bindings: []rbac.RoleBinding{
			{
				ClusterID: clusterA.String(),
				RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceLogging), Verbs: []string{string(rbac.VerbRead)}}},
			},
			{
				ClusterID: clusterB.String(),
				RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceWorkloads), Verbs: []string{string(rbac.VerbRead)}}},
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/logging/operations/", nil)
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: uuid.NewString()}))
	rec := httptest.NewRecorder()

	h.ListOperations(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(envelope.Data) != 1 {
		t.Fatalf("expected exactly 1 visible operation, got %d (body=%s)", len(envelope.Data), rec.Body.String())
	}
	if envelope.Data[0]["id"] != opA.ID.String() {
		t.Fatalf("expected only clusterA op %s, got %v", opA.ID, envelope.Data[0]["id"])
	}
}

// TestRetryLoggingOperationDeniedWithoutClusterUpdate asserts a caller
// with logging:read but no logging:update on the target cluster gets a 403
// (not a requeue). Sanity-check the "update" verb path for RetryOperation.
func TestRetryLoggingOperationDeniedWithoutClusterUpdate(t *testing.T) {
	q := newLoggingFakeQuerier()
	h := NewLoggingHandler(q)

	clusterID := uuid.New()
	op := seedLoggingOperation(t, q, clusterID)

	// Caller has read but not update on the target cluster — the
	// authorizeClusterAction call inside RetryOperation must 403.
	h.SetAuthorization(rbac.NewEngine(), stubLoggingRBACQuerier{
		bindings: []rbac.RoleBinding{
			{
				ClusterID: clusterID.String(),
				RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceLogging), Verbs: []string{string(rbac.VerbRead)}}},
			},
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/logging/operations/"+op.ID.String()+"/retry/", nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", op.ID.String())
	ctx := addRouteCtx(req.Context(), rc)
	ctx = middleware.SetAuthenticatedUserForTest(ctx, &middleware.AuthenticatedUser{ID: uuid.NewString()})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.RetryOperation(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rec.Code, rec.Body.String())
	}
	// The row must not have been requeued.
	got := q.operations[op.ID]
	if got.Status != "failed" {
		t.Fatalf("status = %q, want failed (operation should not have been requeued)", got.Status)
	}
}
