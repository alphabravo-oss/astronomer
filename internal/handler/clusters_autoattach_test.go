package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
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
	upsertErr    error
	createCalled int
	auditOps     []string
}

func newFakeAutoAttachClusterQuerier() *fakeAutoAttachClusterQuerier {
	return &fakeAutoAttachClusterQuerier{templates: map[uuid.UUID]sqlc.ClusterTemplate{}}
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
func (q *fakeAutoAttachClusterQuerier) GetClusterByID(context.Context, uuid.UUID) (sqlc.Cluster, error) {
	return sqlc.Cluster{}, pgx.ErrNoRows
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
func (q *fakeAutoAttachClusterQuerier) CreateClusterDecommission(context.Context, sqlc.CreateClusterDecommissionParams) (sqlc.ClusterDecommission, error) {
	return sqlc.ClusterDecommission{}, nil
}
func (q *fakeAutoAttachClusterQuerier) GetLatestClusterDecommissionByCluster(context.Context, uuid.UUID) (sqlc.ClusterDecommission, error) {
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
func (q *fakeAutoAttachClusterQuerier) GetClusterRegistryConfig(context.Context, uuid.UUID) (sqlc.ClusterRegistryConfig, error) {
	return sqlc.ClusterRegistryConfig{}, pgx.ErrNoRows
}
func (q *fakeAutoAttachClusterQuerier) UpsertClusterRegistryConfig(context.Context, sqlc.UpsertClusterRegistryConfigParams) (sqlc.ClusterRegistryConfig, error) {
	return sqlc.ClusterRegistryConfig{}, nil
}
func (q *fakeAutoAttachClusterQuerier) DeleteClusterRegistryConfig(context.Context, uuid.UUID) error {
	return nil
}

// createReq builds a minimal POST body. The Create handler validates
// the name shape (RFC-1123), so we always pick a clean lowercase
// identifier here — the auto-attach hook lives after validation.
func createReq(t *testing.T, name string) *http.Request {
	t.Helper()
	body := []byte(`{"name":"` + name + `","environment":"production"}`)
	return httptest.NewRequest(http.MethodPost, "/api/v1/clusters/", bytes.NewReader(body))
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
		"cluster.create":                false,
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
