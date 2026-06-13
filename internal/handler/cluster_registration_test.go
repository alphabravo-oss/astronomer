package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/registration"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

// fakeRegistrationQuerier is a tiny in-memory backing store for the
// handler tests. Covers the surface ClusterRegistrationQuerier
// (registration.Querier + cluster + user lookups +
// UpsertClusterTemplateApplication).
type fakeRegistrationQuerier struct {
	mu sync.Mutex

	clusters map[uuid.UUID]sqlc.Cluster
	regs     map[uuid.UUID]*sqlc.ClusterRegistrationRecord
	steps    []sqlc.ClusterRegistrationStep
	users    map[uuid.UUID]sqlc.User
	templApp map[uuid.UUID]sqlc.ClusterTemplateApplication
}

type fakeRegistrationTaskOutbox struct {
	mu   sync.Mutex
	args []sqlc.UpsertTaskOutboxParams
}

func (f *fakeRegistrationTaskOutbox) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.args = append(f.args, arg)
	return sqlc.TaskOutbox{}, nil
}

func (f *fakeRegistrationTaskOutbox) all() []sqlc.UpsertTaskOutboxParams {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlc.UpsertTaskOutboxParams, len(f.args))
	copy(out, f.args)
	return out
}

func newFakeRegQuerier() *fakeRegistrationQuerier {
	return &fakeRegistrationQuerier{
		clusters: map[uuid.UUID]sqlc.Cluster{},
		regs:     map[uuid.UUID]*sqlc.ClusterRegistrationRecord{},
		users:    map[uuid.UUID]sqlc.User{},
		templApp: map[uuid.UUID]sqlc.ClusterTemplateApplication{},
	}
}

func (f *fakeRegistrationQuerier) GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.clusters[id]
	if !ok {
		return sqlc.Cluster{}, pgx.ErrNoRows
	}
	return c, nil
}

func (f *fakeRegistrationQuerier) GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return sqlc.User{}, pgx.ErrNoRows
	}
	return u, nil
}

func (f *fakeRegistrationQuerier) GetClusterTemplateByID(ctx context.Context, id uuid.UUID) (sqlc.ClusterTemplate, error) {
	return sqlc.ClusterTemplate{ID: id, Spec: []byte(`{}`)}, nil
}

func (f *fakeRegistrationQuerier) UpsertClusterTemplateApplication(ctx context.Context, arg sqlc.UpsertClusterTemplateApplicationParams) (sqlc.ClusterTemplateApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	app := sqlc.ClusterTemplateApplication{
		ClusterID:    arg.ClusterID,
		TemplateID:   arg.TemplateID,
		Status:       "pending",
		SpecSnapshot: arg.SpecSnapshot,
	}
	f.templApp[arg.ClusterID] = app
	return app, nil
}

func (f *fakeRegistrationQuerier) GetClusterRegistrationRecord(ctx context.Context, id uuid.UUID) (sqlc.ClusterRegistrationRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.regs[id]
	if !ok {
		return sqlc.ClusterRegistrationRecord{}, pgx.ErrNoRows
	}
	return *r, nil
}

func (f *fakeRegistrationQuerier) UpdateClusterRegistrationPhase(ctx context.Context, arg sqlc.UpdateClusterRegistrationPhaseParams) (sqlc.ClusterRegistrationRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.regs[arg.ID]
	if !ok {
		return sqlc.ClusterRegistrationRecord{}, pgx.ErrNoRows
	}
	r.RegistrationPhase = arg.Phase
	if !r.RegistrationStartedAt.Valid && arg.StartedAt.Valid {
		r.RegistrationStartedAt = arg.StartedAt
	}
	r.RegistrationCompletedAt = arg.CompletedAt
	return *r, nil
}

func (f *fakeRegistrationQuerier) SetClusterInstallBaseline(ctx context.Context, arg sqlc.SetClusterInstallBaselineParams) (sqlc.ClusterRegistrationRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.regs[arg.ID]
	if !ok {
		return sqlc.ClusterRegistrationRecord{}, pgx.ErrNoRows
	}
	r.InstallBaseline = arg.InstallBaseline
	return *r, nil
}

func (f *fakeRegistrationQuerier) InsertClusterRegistrationStep(ctx context.Context, arg sqlc.InsertClusterRegistrationStepParams) (sqlc.ClusterRegistrationStep, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := sqlc.ClusterRegistrationStep{
		ID:           uuid.New(),
		ClusterID:    arg.ClusterID,
		StepName:     arg.StepName,
		Label:        arg.Label,
		Status:       arg.Status,
		ProgressPct:  arg.ProgressPct,
		DetailJSON:   arg.DetailJSON,
		StartedAt:    arg.StartedAt,
		CompletedAt:  arg.CompletedAt,
		ErrorMessage: arg.ErrorMessage,
		StepOrder:    arg.StepOrder,
	}
	f.steps = append(f.steps, s)
	return s, nil
}

func (f *fakeRegistrationQuerier) UpdateClusterRegistrationStep(ctx context.Context, arg sqlc.UpdateClusterRegistrationStepParams) (sqlc.ClusterRegistrationStep, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.steps {
		if f.steps[i].ID == arg.ID {
			f.steps[i].Status = arg.Status
			f.steps[i].ProgressPct = arg.ProgressPct
			if len(arg.DetailJSON) > 0 {
				f.steps[i].DetailJSON = arg.DetailJSON
			}
			f.steps[i].CompletedAt = arg.CompletedAt
			f.steps[i].ErrorMessage = arg.ErrorMessage
			return f.steps[i], nil
		}
	}
	return sqlc.ClusterRegistrationStep{}, pgx.ErrNoRows
}

func (f *fakeRegistrationQuerier) ListClusterRegistrationSteps(ctx context.Context, clusterID uuid.UUID) ([]sqlc.ClusterRegistrationStep, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []sqlc.ClusterRegistrationStep{}
	for _, s := range f.steps {
		if s.ClusterID == clusterID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeRegistrationQuerier) GetClusterRegistrationStep(ctx context.Context, id uuid.UUID) (sqlc.ClusterRegistrationStep, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.steps {
		if s.ID == id {
			return s, nil
		}
	}
	return sqlc.ClusterRegistrationStep{}, pgx.ErrNoRows
}

func (f *fakeRegistrationQuerier) CloseRunningStepsForCluster(ctx context.Context, arg sqlc.CloseRunningStepsForClusterParams) error {
	return nil
}

func (f *fakeRegistrationQuerier) MaxStepOrderForCluster(ctx context.Context, clusterID uuid.UUID) (int32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var max int32
	for _, s := range f.steps {
		if s.ClusterID == clusterID && s.StepOrder > max {
			max = s.StepOrder
		}
	}
	return max, nil
}

func setupHandler(t *testing.T) (*ClusterRegistrationHandler, *fakeRegistrationQuerier, uuid.UUID) {
	t.Helper()
	q := newFakeRegQuerier()
	id := uuid.New()
	q.clusters[id] = sqlc.Cluster{ID: id, Name: "test-cluster"}
	q.regs[id] = &sqlc.ClusterRegistrationRecord{
		ClusterID:         id,
		RegistrationPhase: string(registration.PhaseCreated),
	}
	bus := events.NewBus()
	h := NewClusterRegistrationHandler(q, bus)
	return h, q, id
}

func routerForRegistration(h *ClusterRegistrationHandler) *chi.Mux {
	r := chi.NewRouter()
	r.Get("/api/v1/clusters/{id}/registration/status/", h.GetStatus)
	r.Put("/api/v1/clusters/{id}/registration/options/", h.PutOptions)
	r.Post("/api/v1/clusters/{id}/registration/confirm/", h.PostConfirm)
	r.Post("/api/v1/clusters/{id}/registration/retry/{step_id}/", h.PostRetry)
	r.Post("/api/v1/clusters/{id}/registration/cancel/", h.PostCancel)
	return r
}

// TestRegistrationWizard_ConfirmAdvancesPhaseHandler — the end-to-end
// happy path for the wizard's page 2 → page 3 hand-off.
func TestRegistrationWizard_ConfirmAdvancesPhaseHandler(t *testing.T) {
	h, q, id := setupHandler(t)
	router := routerForRegistration(h)

	// PUT options first so install_baseline isn't NULL.
	body := bytes.NewBufferString(`{"install_baseline": false}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/clusters/"+id.String()+"/registration/options/", body)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("options PUT: status=%d body=%s", w.Code, w.Body.String())
	}

	// POST confirm.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+id.String()+"/registration/confirm/", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("confirm POST: status=%d body=%s", w.Code, w.Body.String())
	}

	rec, _ := q.GetClusterRegistrationRecord(context.Background(), id)
	if rec.RegistrationPhase != string(registration.PhaseAwaitingAgent) {
		t.Fatalf("want awaiting_agent, got %s", rec.RegistrationPhase)
	}
}

func TestRegistrationWizard_ConfirmBaselineWritesTaskOutbox(t *testing.T) {
	h, q, id := setupHandler(t)
	h.SetBaselineTemplateID(uuid.New())
	outbox := &fakeRegistrationTaskOutbox{}
	h.SetTaskOutbox(outbox)
	router := routerForRegistration(h)

	body := bytes.NewBufferString(`{"install_baseline": true}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/clusters/"+id.String()+"/registration/options/", body)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("options PUT: status=%d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+id.String()+"/registration/confirm/", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("confirm POST: status=%d body=%s", w.Code, w.Body.String())
	}

	args := outbox.all()
	if len(args) != 1 {
		t.Fatalf("outbox writes = %d, want 1", len(args))
	}
	if args[0].TaskType != "cluster_template:apply" {
		t.Fatalf("task type = %q", args[0].TaskType)
	}
	if args[0].QueueName != "tunnel" {
		t.Fatalf("queue = %q, want tunnel", args[0].QueueName)
	}
	wantDedupe := "cluster_registration:confirm:cluster_template_apply:" + id.String()
	if !args[0].DedupeKey.Valid || args[0].DedupeKey.String != wantDedupe {
		t.Fatalf("dedupe = %#v, want %q", args[0].DedupeKey, wantDedupe)
	}
	if string(args[0].Payload) != `{"cluster_id":"`+id.String()+`"}` {
		t.Fatalf("payload = %s", string(args[0].Payload))
	}
	if _, ok := q.templApp[id]; !ok {
		t.Fatalf("expected template application row to be upserted")
	}
}

func TestRegistrationWizard_RetryArgoCDAdoptionWritesTaskOutbox(t *testing.T) {
	h, q, id := setupHandler(t)
	q.regs[id].RegistrationPhase = string(registration.PhaseFailed)
	stepID := uuid.New()
	q.steps = append(q.steps, sqlc.ClusterRegistrationStep{
		ID:          stepID,
		ClusterID:   id,
		StepName:    "argocd_registration_failed",
		Label:       "ArgoCD registration failed",
		Status:      "failed",
		ProgressPct: 0,
		StepOrder:   2,
	})
	outbox := &fakeRegistrationTaskOutbox{}
	h.SetTaskOutbox(outbox)
	router := routerForRegistration(h)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+id.String()+"/registration/retry/"+stepID.String()+"/", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("retry POST: status=%d body=%s", w.Code, w.Body.String())
	}
	args := outbox.all()
	if len(args) != 1 {
		t.Fatalf("outbox writes = %d, want 1", len(args))
	}
	if args[0].TaskType != tasks.ArgoCDAutoRegisterClusterType {
		t.Fatalf("task type = %q, want %q", args[0].TaskType, tasks.ArgoCDAutoRegisterClusterType)
	}
	if args[0].QueueName != "default" || args[0].MaxRetry != 5 {
		t.Fatalf("queue/max_retry = %s/%d, want default/5", args[0].QueueName, args[0].MaxRetry)
	}
	var payload tasks.ArgoCDAutoRegisterClusterPayload
	if err := json.Unmarshal(args[0].Payload, &payload); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	if payload.ClusterID != id.String() {
		t.Fatalf("payload cluster_id = %q, want %s", payload.ClusterID, id)
	}
	step, err := q.GetClusterRegistrationStep(context.Background(), stepID)
	if err != nil {
		t.Fatalf("step lookup: %v", err)
	}
	if step.Status != "pending" || step.ProgressPct != 0 {
		t.Fatalf("step after retry = %+v, want pending progress 0", step)
	}
}

// TestRegistrationWizard_SuperuserRequiredOnCancel — non-superuser
// callers should get 403 even when otherwise authenticated.
func TestRegistrationWizard_SuperuserRequiredOnCancel(t *testing.T) {
	h, q, id := setupHandler(t)
	// Move past `created` so cancel is legal.
	q.regs[id].RegistrationPhase = string(registration.PhaseAwaitingAgent)

	router := routerForRegistration(h)

	regularUserID := uuid.New()
	q.users[regularUserID] = sqlc.User{ID: regularUserID, IsSuperuser: false}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+id.String()+"/registration/cancel/", nil)
	ctx := middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID:    regularUserID.String(),
		Email: "user@example.com",
	})
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-superuser cancel: want 403, got %d body=%s", w.Code, w.Body.String())
	}

	// Now retry as a superuser — should succeed.
	adminID := uuid.New()
	q.users[adminID] = sqlc.User{ID: adminID, IsSuperuser: true}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+id.String()+"/registration/cancel/", nil)
	ctx = middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID:    adminID.String(),
		Email: "admin@example.com",
	})
	req = req.WithContext(ctx)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("superuser cancel: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	rec, _ := q.GetClusterRegistrationRecord(context.Background(), id)
	if rec.RegistrationPhase != string(registration.PhaseFailed) {
		t.Errorf("after cancel: want failed, got %s", rec.RegistrationPhase)
	}
}

// TestRegistrationWizard_RequiresClustersUpdateOnWrites — verifies the
// route table (server/routes.go) gates writes on writeClusters +
// VerbUpdate and reads on VerbRead. We grep the file rather than
// reach into the route resolution machinery; the assertion catches
// drift in either direction.
func TestRegistrationWizard_RequiresClustersUpdateOnWrites(t *testing.T) {
	// routes.go lives at ../server/routes.go relative to the
	// handler test directory.
	candidates := []string{
		filepath.Join("..", "server", "routes.go"),
		filepath.Join("..", "..", "internal", "server", "routes.go"),
	}
	var content string
	for _, c := range candidates {
		if b, err := os.ReadFile(c); err == nil {
			content = string(b)
			break
		}
	}
	if content == "" {
		t.Skip("routes.go not found in expected locations")
	}
	// The writes (options/confirm/retry/cancel) MUST require
	// VerbUpdate; the read (status) MUST require VerbRead.
	if !strings.Contains(content, `Put("/{id}/registration/options/"`) {
		t.Error("PUT /registration/options/ route missing")
	}
	if !strings.Contains(content, `Post("/{id}/registration/confirm/"`) {
		t.Error("POST /registration/confirm/ route missing")
	}
	// Each write route must have a writeClusters middleware AND a
	// VerbUpdate permission check.
	for _, op := range []string{"options", "confirm", "retry", "cancel"} {
		// crude but effective: locate the route line and check both
		// markers appear before it.
		anchor := "registration/" + op
		idx := strings.Index(content, anchor)
		if idx < 0 {
			t.Errorf("route for %s not found", op)
			continue
		}
		// Slice back to the preceding 400 chars and check.
		start := idx - 400
		if start < 0 {
			start = 0
		}
		windowed := content[start:idx]
		if !strings.Contains(windowed, "writeClusters") {
			t.Errorf("route %s missing writeClusters middleware", op)
		}
		if !strings.Contains(windowed, "VerbUpdate") {
			t.Errorf("route %s missing VerbUpdate permission gate", op)
		}
	}
	// The read route requires VerbRead.
	statusIdx := strings.Index(content, "registration/status")
	if statusIdx < 0 {
		t.Fatal("status route missing")
	}
	start := statusIdx - 400
	if start < 0 {
		start = 0
	}
	if !strings.Contains(content[start:statusIdx], "VerbRead") {
		t.Error("status route should require VerbRead")
	}
}

// TestRegistrationWizard_OptionsValidation — null body or missing
// install_baseline returns 400.
func TestRegistrationWizard_OptionsValidation(t *testing.T) {
	h, _, id := setupHandler(t)
	router := routerForRegistration(h)

	for _, body := range []string{`{}`, `{"foo": true}`, ``} {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/clusters/"+id.String()+"/registration/options/", bytes.NewBufferString(body))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body=%q: want 400, got %d", body, w.Code)
		}
	}
}

// TestRegistrationWizard_StatusResponseShape — the GET /status/
// response is the contract the wizard frontend depends on. Keep it
// stable.
func TestRegistrationWizard_StatusResponseShape(t *testing.T) {
	h, q, id := setupHandler(t)
	q.regs[id].RegistrationPhase = string(registration.PhaseConnected)
	q.regs[id].InstallBaseline = pgtype.Bool{Bool: true, Valid: true}
	q.steps = append(q.steps, sqlc.ClusterRegistrationStep{
		ID: uuid.New(), ClusterID: id, StepName: "cluster_created", Status: "success", StepOrder: 1,
	})

	router := routerForRegistration(h)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+id.String()+"/registration/status/", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d %s", w.Code, w.Body.String())
	}
	var envelope struct {
		Data registration.Status `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if envelope.Data.Phase != "connected" {
		t.Errorf("phase: %s", envelope.Data.Phase)
	}
	if envelope.Data.InstallBaseline == nil || !*envelope.Data.InstallBaseline {
		t.Errorf("install_baseline: %v", envelope.Data.InstallBaseline)
	}
	if len(envelope.Data.Steps) != 1 || envelope.Data.Steps[0].StepName != "cluster_created" {
		t.Errorf("steps: %+v", envelope.Data.Steps)
	}
}
