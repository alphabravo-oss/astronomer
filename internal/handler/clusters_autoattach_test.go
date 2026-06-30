package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

// fakeAutoAttachClusterQuerier is the minimal ClusterQuerier used by
// the sprint-074 auto-attach tests. It tracks every
// UpsertClusterTemplateApplication call so the assertions can verify
// the cluster Create handler attached the right template (or didn't,
// when expected). All non-sprint-074 methods return zero values — they
// satisfy the interface so the handler compiles, but aren't exercised
// by these tests.
type fakeAutoAttachClusterQuerier struct {
	mu sync.Mutex

	// Sprint 074 state.
	config       sqlc.PlatformConfiguration
	templates    map[uuid.UUID]sqlc.ClusterTemplate
	templateErr  error
	configErr    error
	upserts      []sqlc.UpsertClusterTemplateApplicationParams
	upsertErr     error
	createCalled  int
	createErr     error
	lastCreateArg sqlc.CreateClusterParams
	auditOps      []string

	clusters         map[uuid.UUID]sqlc.Cluster
	latestDecoms     map[uuid.UUID]sqlc.ClusterDecommission
	createDecomID    uuid.UUID
	createDecoms     []sqlc.CreateClusterDecommissionParams
	createdDecomRows []sqlc.ClusterDecommission
}

func newFakeAutoAttachClusterQuerier() *fakeAutoAttachClusterQuerier {
	return &fakeAutoAttachClusterQuerier{
		templates:     map[uuid.UUID]sqlc.ClusterTemplate{},
		clusters:      map[uuid.UUID]sqlc.Cluster{},
		latestDecoms:  map[uuid.UUID]sqlc.ClusterDecommission{},
		createDecomID: uuid.Nil,
	}
}

func (q *fakeAutoAttachClusterQuerier) GetPlatformConfig(context.Context) (sqlc.PlatformConfiguration, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.configErr != nil {
		return sqlc.PlatformConfiguration{}, q.configErr
	}
	return q.config, nil
}

func (q *fakeAutoAttachClusterQuerier) GetClusterTemplateByID(_ context.Context, id uuid.UUID) (sqlc.ClusterTemplate, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.templateErr != nil {
		return sqlc.ClusterTemplate{}, q.templateErr
	}
	t, ok := q.templates[id]
	if !ok {
		return sqlc.ClusterTemplate{}, pgx.ErrNoRows
	}
	return t, nil
}

func (q *fakeAutoAttachClusterQuerier) UpsertClusterTemplateApplication(_ context.Context, arg sqlc.UpsertClusterTemplateApplicationParams) (sqlc.ClusterTemplateApplication, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.upsertErr != nil {
		return sqlc.ClusterTemplateApplication{}, q.upsertErr
	}
	q.upserts = append(q.upserts, arg)
	return sqlc.ClusterTemplateApplication{
		ClusterID:    arg.ClusterID,
		TemplateID:   arg.TemplateID,
		Status:       "pending",
		SpecSnapshot: arg.SpecSnapshot,
	}, nil
}

// CreateCluster is the OTHER write hit by the Create handler. We
// echo the params back as a freshly-IDed Cluster row so the rest of
// the handler can render the response.
func (q *fakeAutoAttachClusterQuerier) CreateCluster(_ context.Context, arg sqlc.CreateClusterParams) (sqlc.Cluster, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.createCalled++
	q.lastCreateArg = arg
	if q.createErr != nil {
		return sqlc.Cluster{}, q.createErr
	}
	return sqlc.Cluster{
		ID:           uuid.New(),
		Name:         arg.Name,
		DisplayName:  arg.DisplayName,
		Description:  arg.Description,
		Environment:  arg.Environment,
		Region:       arg.Region,
		Provider:     arg.Provider,
		Distribution: arg.Distribution,
		CreatedByID:  arg.CreatedByID,
		Status:       "pending",
	}, nil
}

// Audit writer — captures the action names recordAudit ends up writing.
func (q *fakeAutoAttachClusterQuerier) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.auditOps = append(q.auditOps, arg.Action)
	return nil
}

// Remaining ClusterQuerier methods — boilerplate zero returns.
func (q *fakeAutoAttachClusterQuerier) GetClusterByID(_ context.Context, id uuid.UUID) (sqlc.Cluster, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	row, ok := q.clusters[id]
	if !ok {
		return sqlc.Cluster{}, pgx.ErrNoRows
	}
	return row, nil
}
func (q *fakeAutoAttachClusterQuerier) GetClusterByName(context.Context, string) (sqlc.Cluster, error) {
	return sqlc.Cluster{}, pgx.ErrNoRows
}
func (q *fakeAutoAttachClusterQuerier) ListClusters(context.Context, sqlc.ListClustersParams) ([]sqlc.Cluster, error) {
	return nil, nil
}
func (q *fakeAutoAttachClusterQuerier) UpdateCluster(context.Context, sqlc.UpdateClusterParams) (sqlc.Cluster, error) {
	return sqlc.Cluster{}, nil
}
func (q *fakeAutoAttachClusterQuerier) DeleteCluster(context.Context, uuid.UUID) error {
	return nil
}
func (q *fakeAutoAttachClusterQuerier) CountClusters(context.Context) (int64, error) { return 0, nil }
func (q *fakeAutoAttachClusterQuerier) CreateClusterDecommission(_ context.Context, arg sqlc.CreateClusterDecommissionParams) (sqlc.ClusterDecommission, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	id := q.createDecomID
	if id == uuid.Nil {
		id = uuid.New()
	}
	row := sqlc.ClusterDecommission{
		ID:            id,
		ClusterID:     arg.ClusterID,
		RequestedByID: arg.RequestedByID,
		ClusterName:   arg.ClusterName,
		Status:        "pending",
		Attempts:      0,
		Phases:        json.RawMessage(`{}`),
	}
	q.createDecoms = append(q.createDecoms, arg)
	q.createdDecomRows = append(q.createdDecomRows, row)
	q.latestDecoms[arg.ClusterID] = row
	return row, nil
}
func (q *fakeAutoAttachClusterQuerier) GetLatestClusterDecommissionByCluster(_ context.Context, clusterID uuid.UUID) (sqlc.ClusterDecommission, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	row, ok := q.latestDecoms[clusterID]
	if !ok {
		return sqlc.ClusterDecommission{}, pgx.ErrNoRows
	}
	return row, nil
}
func (q *fakeAutoAttachClusterQuerier) ListPendingClusterDecommissions(context.Context, int32) ([]sqlc.ClusterDecommission, error) {
	return nil, nil
}

func (q *fakeAutoAttachClusterQuerier) SetClusterDecommissionForce(context.Context, uuid.UUID) (sqlc.ClusterDecommission, error) {
	return sqlc.ClusterDecommission{}, nil
}
func (q *fakeAutoAttachClusterQuerier) GetClusterHealthStatus(context.Context, uuid.UUID) (sqlc.ClusterHealthStatus, error) {
	return sqlc.ClusterHealthStatus{}, nil
}
func (q *fakeAutoAttachClusterQuerier) ListClusterConditions(context.Context, uuid.UUID) ([]sqlc.ClusterCondition, error) {
	return nil, nil
}
func (q *fakeAutoAttachClusterQuerier) CreateClusterRegistrationToken(context.Context, sqlc.CreateClusterRegistrationTokenParams) (sqlc.ClusterRegistrationToken, error) {
	return sqlc.ClusterRegistrationToken{}, nil
}
func (q *fakeAutoAttachClusterQuerier) GetRegistrationTokenByToken(context.Context, string) (sqlc.ClusterRegistrationToken, error) {
	return sqlc.ClusterRegistrationToken{}, pgx.ErrNoRows
}
func (q *fakeAutoAttachClusterQuerier) MarkRegistrationTokenUsed(context.Context, uuid.UUID) error {
	return nil
}
func (q *fakeAutoAttachClusterQuerier) SetClusterAgentTokenRotationPending(context.Context, uuid.UUID) (int64, error) {
	return 0, nil
}
func (q *fakeAutoAttachClusterQuerier) RevokeClusterAgentToken(context.Context, uuid.UUID) (int64, error) {
	return 0, nil
}
func (q *fakeAutoAttachClusterQuerier) GetClusterRegistryConfig(context.Context, uuid.UUID) (sqlc.ClusterRegistryConfig, error) {
	return sqlc.ClusterRegistryConfig{}, pgx.ErrNoRows
}
func (q *fakeAutoAttachClusterQuerier) UpsertClusterRegistryConfig(context.Context, sqlc.UpsertClusterRegistryConfigParams) (sqlc.ClusterRegistryConfig, error) {
	return sqlc.ClusterRegistryConfig{}, nil
}
func (q *fakeAutoAttachClusterQuerier) DeleteClusterRegistryConfig(context.Context, uuid.UUID) error {
	return nil
}
func (q *fakeAutoAttachClusterQuerier) GetPlatformSetting(context.Context, string) (sqlc.PlatformSetting, error) {
	return sqlc.PlatformSetting{}, pgx.ErrNoRows
}
func (q *fakeAutoAttachClusterQuerier) ListArgoCDManagedClustersByCluster(context.Context, uuid.UUID) ([]sqlc.ArgocdManagedCluster, error) {
	return nil, nil
}
func (q *fakeAutoAttachClusterQuerier) ListArgoCDApplicationsByManagedClusterTargets(context.Context, sqlc.ListArgoCDApplicationsByManagedClusterTargetsParams) ([]sqlc.ArgocdApplication, error) {
	return nil, nil
}
func (q *fakeAutoAttachClusterQuerier) ListClusterConditionRemediationByCluster(context.Context, uuid.UUID) ([]sqlc.ClusterConditionRemediationAttempt, error) {
	return nil, nil
}

type fakeAtomicDecommissionQuerier struct {
	*fakeAutoAttachClusterQuerier
	atomicDecoms []sqlc.CreateClusterDecommissionWithTaskOutboxParams
}

func (q *fakeAtomicDecommissionQuerier) CreateClusterDecommissionWithTaskOutbox(_ context.Context, arg sqlc.CreateClusterDecommissionWithTaskOutboxParams) (sqlc.ClusterDecommission, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	row := sqlc.ClusterDecommission{
		ID:            arg.ID,
		ClusterID:     arg.ClusterID,
		RequestedByID: arg.RequestedByID,
		ClusterName:   arg.ClusterName,
		Status:        "pending",
		Attempts:      0,
		Phases:        json.RawMessage(`{}`),
	}
	q.atomicDecoms = append(q.atomicDecoms, arg)
	q.createdDecomRows = append(q.createdDecomRows, row)
	q.latestDecoms[arg.ClusterID] = row
	return row, nil
}

// createReq builds a minimal POST body. The Create handler validates
// the name shape (RFC-1123), so we always pick a clean lowercase
// identifier here — the auto-attach hook lives after validation.
func createReq(t *testing.T, name string) *http.Request {
	t.Helper()
	body := []byte(`{"name":"` + name + `","environment":"production"}`)
	return httptest.NewRequest(http.MethodPost, "/api/v1/clusters/", bytes.NewReader(body))
}

// TestClusterHandler_Create_PersistsAnnotations ensures the create handler
// binds and forwards the annotations body (notably the agent-privilege-profile)
// to CreateClusterParams — without this the Viewer/Admin picker is a no-op.
func TestClusterHandler_Create_PersistsAnnotations(t *testing.T) {
	q := newFakeAutoAttachClusterQuerier()
	h := NewClusterHandler(q)

	body := []byte(`{"name":"with-anno","environment":"testing","annotations":{"astronomer.io/agent-privilege-profile":"admin"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("Create status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(string(q.lastCreateArg.Annotations), "admin") {
		t.Fatalf("CreateCluster annotations = %q, want the admin profile annotation", string(q.lastCreateArg.Annotations))
	}
}

// TestClusterHandler_Create_DuplicateNameReturns409 ensures a unique-name
// constraint violation is surfaced as a 409 Conflict with a clear message,
// not an opaque 500 ("Failed to create cluster").
func TestClusterHandler_Create_DuplicateNameReturns409(t *testing.T) {
	// Cover both the typed *pgconn.PgError path AND the string-only path: the
	// real pooled pgx error often does not unwrap to *pgconn.PgError via
	// errors.As, so isUniqueViolation must also match the SQLSTATE text.
	cases := map[string]error{
		"typed":  &pgconn.PgError{Code: "23505"},
		"wrapped string": errors.New(
			`ERROR: duplicate key value violates unique constraint "clusters_name_key" (SQLSTATE 23505)`),
	}
	for name, createErr := range cases {
		t.Run(name, func(t *testing.T) {
			q := newFakeAutoAttachClusterQuerier()
			q.createErr = createErr
			h := NewClusterHandler(q)

			w := httptest.NewRecorder()
			h.Create(w, createReq(t, "dup-cluster"))

			if w.Code != http.StatusConflict {
				t.Fatalf("Create status = %d, want 409; body=%s", w.Code, w.Body.String())
			}
			var resp map[string]any
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			errMap, _ := resp["error"].(map[string]any)
			if errMap["code"] != "conflict" {
				t.Errorf("error code = %v, want conflict", errMap["code"])
			}
		})
	}
}

// TestPlatformDefaultTemplate_ClusterCreateAutoAttachesDefault confirms
// the happy path: a cluster Create with a configured platform default
// writes exactly one cluster_template_applications row pointing at the
// default, and stamps the audit trail with the auto_attached action.
func TestPlatformDefaultTemplate_ClusterCreateAutoAttachesDefault(t *testing.T) {
	templateID := uuid.New()
	q := newFakeAutoAttachClusterQuerier()
	q.config = sqlc.PlatformConfiguration{
		ID:                       1,
		DefaultClusterTemplateID: pgtype.UUID{Bytes: templateID, Valid: true},
	}
	q.templates[templateID] = sqlc.ClusterTemplate{
		ID:   templateID,
		Name: "Platform baseline",
		Spec: json.RawMessage(`{"tools":[{"slug":"trivy-operator"}]}`),
	}

	h := NewClusterHandler(q)

	w := httptest.NewRecorder()
	h.Create(w, createReq(t, "prod-1"))

	if w.Code != http.StatusCreated {
		t.Fatalf("Create status = %d, body=%s", w.Code, w.Body.String())
	}
	if q.createCalled != 1 {
		t.Fatalf("CreateCluster called %d times, want 1", q.createCalled)
	}
	if len(q.upserts) != 1 {
		t.Fatalf("auto-attach upserts = %d, want 1", len(q.upserts))
	}
	if q.upserts[0].TemplateID != templateID {
		t.Errorf("auto-attached template_id = %s, want %s", q.upserts[0].TemplateID, templateID)
	}

	// Audit trail must show both the cluster.create and the template
	// auto-attach actions. Order is create-first, attach-second.
	wantActions := map[string]bool{
		"cluster.create":                 false,
		"cluster.template.auto_attached": false,
	}
	for _, a := range q.auditOps {
		if _, ok := wantActions[a]; ok {
			wantActions[a] = true
		}
	}
	for action, seen := range wantActions {
		if !seen {
			t.Errorf("audit trail missing action %q (saw %v)", action, q.auditOps)
		}
	}
}

func TestClusterDeleteWritesDecommissionToTaskOutbox(t *testing.T) {
	clusterID := uuid.New()
	userID := uuid.New()
	decommissionID := uuid.New()
	q := newFakeAutoAttachClusterQuerier()
	q.createDecomID = decommissionID
	q.clusters[clusterID] = sqlc.Cluster{
		ID:          clusterID,
		Name:        "prod-1",
		DisplayName: "Production 1",
		Status:      "connected",
		IsLocal:     false,
	}
	outbox := &fakeRegistrationTaskOutbox{}
	h := NewClusterHandler(q)
	h.SetTaskOutbox(outbox)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/clusters/"+clusterID.String()+"/", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", clusterID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID:         userID.String(),
		AuthMethod: "jwt",
	}))
	w := httptest.NewRecorder()

	h.Delete(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("Delete status = %d, body=%s", w.Code, w.Body.String())
	}
	if len(q.createDecoms) != 1 {
		t.Fatalf("CreateClusterDecommission calls = %d, want 1", len(q.createDecoms))
	}
	if q.createDecoms[0].RequestedByID.Bytes != userID || !q.createDecoms[0].RequestedByID.Valid {
		t.Fatalf("RequestedByID = %+v, want %s", q.createDecoms[0].RequestedByID, userID)
	}
	args := outbox.all()
	if len(args) != 1 {
		t.Fatalf("outbox writes = %d, want 1", len(args))
	}
	got := args[0]
	if got.TaskType != tasks.ClusterDecommissionType {
		t.Fatalf("TaskType = %q, want %q", got.TaskType, tasks.ClusterDecommissionType)
	}
	if !got.DedupeKey.Valid || got.DedupeKey.String != "cluster_decommission:"+decommissionID.String() {
		t.Fatalf("DedupeKey = %+v", got.DedupeKey)
	}
	if got.QueueName != tasks.ClusterTemplateApplyQueueName {
		t.Fatalf("QueueName = %q, want tunnel", got.QueueName)
	}
	if got.MaxRetry != 3 {
		t.Fatalf("MaxRetry = %d, want 3", got.MaxRetry)
	}
	if got.MaxDeliveryAttempts != 20 {
		t.Fatalf("MaxDeliveryAttempts = %d, want 20", got.MaxDeliveryAttempts)
	}
	var payload tasks.ClusterDecommissionPayload
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	if payload.DecommissionID != decommissionID.String() {
		t.Fatalf("payload decommission_id = %q, want %s", payload.DecommissionID, decommissionID)
	}
}

func TestClusterDeleteCreatesDecommissionAndTaskOutboxAtomically(t *testing.T) {
	clusterID := uuid.New()
	userID := uuid.New()
	base := newFakeAutoAttachClusterQuerier()
	base.clusters[clusterID] = sqlc.Cluster{
		ID:      clusterID,
		Name:    "prod-atomic",
		Status:  "connected",
		IsLocal: false,
	}
	q := &fakeAtomicDecommissionQuerier{fakeAutoAttachClusterQuerier: base}
	outbox := &fakeRegistrationTaskOutbox{}
	h := NewClusterHandler(q)
	h.SetTaskOutbox(outbox)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/clusters/"+clusterID.String()+"/", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", clusterID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID:         userID.String(),
		AuthMethod: "jwt",
	}))
	w := httptest.NewRecorder()

	h.Delete(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("Delete status = %d, body=%s", w.Code, w.Body.String())
	}
	if len(q.atomicDecoms) != 1 {
		t.Fatalf("atomic decommission writes = %d, want 1", len(q.atomicDecoms))
	}
	if len(q.createDecoms) != 0 {
		t.Fatalf("non-atomic decommission writes = %d, want 0", len(q.createDecoms))
	}
	if len(outbox.all()) != 0 {
		t.Fatalf("separate outbox writes = %d, want 0", len(outbox.all()))
	}
	arg := q.atomicDecoms[0]
	if arg.ID == uuid.Nil {
		t.Fatalf("atomic decommission id is nil")
	}
	if !arg.DedupeKey.Valid || arg.DedupeKey.String != "cluster_decommission:"+arg.ID.String() {
		t.Fatalf("dedupe key = %+v", arg.DedupeKey)
	}
	if arg.TaskType != tasks.ClusterDecommissionType || arg.QueueName != tasks.ClusterTemplateApplyQueueName || arg.MaxRetry != 3 || arg.MaxDeliveryAttempts != 20 {
		t.Fatalf("task metadata = %s/%s/%d/%d", arg.TaskType, arg.QueueName, arg.MaxRetry, arg.MaxDeliveryAttempts)
	}
	var payload tasks.ClusterDecommissionPayload
	if err := json.Unmarshal(arg.Payload, &payload); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	if payload.DecommissionID != arg.ID.String() {
		t.Fatalf("payload decommission_id = %q, want %s", payload.DecommissionID, arg.ID)
	}
}

func TestPlatformDefaultTemplate_ClusterCreateAutoAttachWritesTaskOutbox(t *testing.T) {
	templateID := uuid.New()
	q := newFakeAutoAttachClusterQuerier()
	q.config = sqlc.PlatformConfiguration{
		ID:                       1,
		DefaultClusterTemplateID: pgtype.UUID{Bytes: templateID, Valid: true},
	}
	q.templates[templateID] = sqlc.ClusterTemplate{
		ID:   templateID,
		Name: "Platform baseline",
		Spec: json.RawMessage(`{"tools":[{"slug":"trivy-operator"}]}`),
	}
	outbox := &fakeRegistrationTaskOutbox{}
	h := NewClusterHandler(q)
	h.SetTaskOutbox(outbox)

	w := httptest.NewRecorder()
	h.Create(w, createReq(t, "prod-apply"))

	if w.Code != http.StatusCreated {
		t.Fatalf("Create status = %d, body=%s", w.Code, w.Body.String())
	}
	if len(q.upserts) != 1 {
		t.Fatalf("auto-attach upserts = %d, want 1", len(q.upserts))
	}
	clusterID := q.upserts[0].ClusterID
	args := outbox.all()
	if len(args) != 1 {
		t.Fatalf("outbox writes = %d, want 1", len(args))
	}
	arg := args[0]
	if arg.TaskType != tasks.ClusterTemplateApplyType {
		t.Fatalf("TaskType = %q, want %q", arg.TaskType, tasks.ClusterTemplateApplyType)
	}
	if !arg.DedupeKey.Valid || arg.DedupeKey.String != "cluster_template_apply:"+clusterID.String() {
		t.Fatalf("DedupeKey = %+v", arg.DedupeKey)
	}
	if arg.QueueName != tasks.ClusterTemplateApplyQueueName || arg.MaxRetry != 3 || arg.MaxDeliveryAttempts != 20 {
		t.Fatalf("outbox options queue/max_retry/max_delivery = %s/%d/%d", arg.QueueName, arg.MaxRetry, arg.MaxDeliveryAttempts)
	}
	var payload tasks.ClusterTemplateApplyPayload
	if err := json.Unmarshal(arg.Payload, &payload); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	if payload.ClusterID != clusterID.String() {
		t.Fatalf("payload cluster_id = %q, want %s", payload.ClusterID, clusterID)
	}
}

// TestPlatformDefaultTemplate_ClusterCreateNoAttachWhenDefaultNotSet
// verifies the legacy/opt-out path: when the operator hasn't
// configured a default, the cluster comes up bare and no application
// row is written.
func TestPlatformDefaultTemplate_ClusterCreateNoAttachWhenDefaultNotSet(t *testing.T) {
	q := newFakeAutoAttachClusterQuerier()
	q.config = sqlc.PlatformConfiguration{ID: 1} // Valid:false

	h := NewClusterHandler(q)

	w := httptest.NewRecorder()
	h.Create(w, createReq(t, "prod-2"))

	if w.Code != http.StatusCreated {
		t.Fatalf("Create status = %d, body=%s", w.Code, w.Body.String())
	}
	if len(q.upserts) != 0 {
		t.Errorf("auto-attach upserts = %d, want 0 (no default configured)", len(q.upserts))
	}
	for _, a := range q.auditOps {
		if a == "cluster.template.auto_attached" {
			t.Errorf("audit trail contains auto_attached despite no default: %v", q.auditOps)
		}
	}
}

// TestPlatformDefaultTemplate_ClusterCreateAutoAttachFailureDoesNotFailCreate
// is the sprint-074 best-effort guarantee: every failure mode in the
// auto-attach path (stale template FK target, upsert error, even the
// platform_configuration fetch dying) MUST result in a successful
// 201 on the cluster create. The reconciler is the retry path.
func TestPlatformDefaultTemplate_ClusterCreateAutoAttachFailureDoesNotFailCreate(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*fakeAutoAttachClusterQuerier)
	}{
		{
			name: "platform_configuration fetch fails",
			setup: func(q *fakeAutoAttachClusterQuerier) {
				q.configErr = errors.New("db dead")
			},
		},
		{
			name: "default points at nonexistent template",
			setup: func(q *fakeAutoAttachClusterQuerier) {
				q.config = sqlc.PlatformConfiguration{
					ID:                       1,
					DefaultClusterTemplateID: pgtype.UUID{Bytes: uuid.New(), Valid: true},
				}
				// templates map is empty — lookup returns pgx.ErrNoRows.
			},
		},
		{
			name: "upsert of application row fails",
			setup: func(q *fakeAutoAttachClusterQuerier) {
				id := uuid.New()
				q.config = sqlc.PlatformConfiguration{
					ID:                       1,
					DefaultClusterTemplateID: pgtype.UUID{Bytes: id, Valid: true},
				}
				q.templates[id] = sqlc.ClusterTemplate{ID: id, Name: "Platform baseline"}
				q.upsertErr = errors.New("deadlock detected, rolled back")
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := newFakeAutoAttachClusterQuerier()
			tc.setup(q)

			h := NewClusterHandler(q)
			w := httptest.NewRecorder()
			h.Create(w, createReq(t, "prod-flaky"))

			if w.Code != http.StatusCreated {
				t.Fatalf("Create status = %d, want 201 (best-effort guarantee); body=%s", w.Code, w.Body.String())
			}
			if q.createCalled != 1 {
				t.Errorf("CreateCluster calls = %d, want 1", q.createCalled)
			}
		})
	}
}
