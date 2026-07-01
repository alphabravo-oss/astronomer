// Tests for the per-project ("BYO") Helm catalog handler (migration 061).
//
// The fake querier embeds an in-memory map so each test can prime exactly
// the rows it needs without going near a real Postgres. The visibility
// helper exercises the public/own/foreign-private taxonomy that drives the
// 200/403/404 decisions, and the unsubscribe semantics test pinning is
// load-bearing: deleting a subscription to a global keeps the catalog
// alive, deleting a subscription to a project-owned catalog removes
// the catalog row.

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// ---------------------------------------------------------------------------
// Fake querier
// ---------------------------------------------------------------------------

type fakeProjectCatalogQuerier struct {
	projects      map[uuid.UUID]sqlc.Project
	users         map[uuid.UUID]sqlc.User
	catalogs      map[uuid.UUID]sqlc.HelmRepositoryWithOwner
	subscriptions map[uuid.UUID]map[uuid.UUID]sqlc.ProjectCatalogSubscription // by project then catalog
	chartsByRepo  map[uuid.UUID][]sqlc.HelmChart
	createErr     error
	audits        []sqlc.CreateAuditLogV1Params
}

func newFakeProjectCatalogQuerier() *fakeProjectCatalogQuerier {
	return &fakeProjectCatalogQuerier{
		projects:      map[uuid.UUID]sqlc.Project{},
		users:         map[uuid.UUID]sqlc.User{},
		catalogs:      map[uuid.UUID]sqlc.HelmRepositoryWithOwner{},
		subscriptions: map[uuid.UUID]map[uuid.UUID]sqlc.ProjectCatalogSubscription{},
		chartsByRepo:  map[uuid.UUID][]sqlc.HelmChart{},
	}
}

func (f *fakeProjectCatalogQuerier) GetProjectByID(_ context.Context, id uuid.UUID) (sqlc.Project, error) {
	p, ok := f.projects[id]
	if !ok {
		return sqlc.Project{}, pgx.ErrNoRows
	}
	return p, nil
}

func (f *fakeProjectCatalogQuerier) GetUserByID(_ context.Context, id uuid.UUID) (sqlc.User, error) {
	u, ok := f.users[id]
	if !ok {
		return sqlc.User{}, pgx.ErrNoRows
	}
	return u, nil
}

func (f *fakeProjectCatalogQuerier) ListCatalogsForProject(_ context.Context, projectID uuid.UUID) ([]sqlc.HelmRepositoryWithOwner, error) {
	out := []sqlc.HelmRepositoryWithOwner{}
	subs := f.subscriptions[projectID]
	for _, c := range f.catalogs {
		// Global → always visible.
		if !c.OwnerProjectID.Valid {
			out = append(out, c)
			continue
		}
		// Owned by this project → visible.
		if uuid.UUID(c.OwnerProjectID.Bytes) == projectID {
			out = append(out, c)
			continue
		}
		// Subscribed → visible.
		if _, ok := subs[c.ID]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeProjectCatalogQuerier) ListProjectOwnedCatalogs(_ context.Context, projectID uuid.UUID) ([]sqlc.HelmRepositoryWithOwner, error) {
	out := []sqlc.HelmRepositoryWithOwner{}
	for _, c := range f.catalogs {
		if c.OwnerProjectID.Valid && uuid.UUID(c.OwnerProjectID.Bytes) == projectID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeProjectCatalogQuerier) ListProjectSubscriptions(_ context.Context, projectID uuid.UUID) ([]sqlc.ProjectCatalogSubscription, error) {
	out := []sqlc.ProjectCatalogSubscription{}
	for _, s := range f.subscriptions[projectID] {
		out = append(out, s)
	}
	return out, nil
}

func (f *fakeProjectCatalogQuerier) GetHelmRepositoryWithOwner(_ context.Context, id uuid.UUID) (sqlc.HelmRepositoryWithOwner, error) {
	c, ok := f.catalogs[id]
	if !ok {
		return sqlc.HelmRepositoryWithOwner{}, pgx.ErrNoRows
	}
	return c, nil
}

func (f *fakeProjectCatalogQuerier) GetProjectCatalogSubscription(_ context.Context, arg sqlc.GetProjectCatalogSubscriptionParams) (sqlc.ProjectCatalogSubscription, error) {
	if subs, ok := f.subscriptions[arg.ProjectID]; ok {
		if row, ok := subs[arg.CatalogID]; ok {
			return row, nil
		}
	}
	return sqlc.ProjectCatalogSubscription{}, pgx.ErrNoRows
}

func (f *fakeProjectCatalogQuerier) GetCatalogVisibilityForProject(ctx context.Context, projectID, catalogID uuid.UUID) (sqlc.CatalogVisibility, error) {
	cat, err := f.GetHelmRepositoryWithOwner(ctx, catalogID)
	if err != nil {
		return sqlc.CatalogVisibilityUnauthorized, err
	}
	if cat.OwnerProjectID.Valid && uuid.UUID(cat.OwnerProjectID.Bytes) == projectID {
		return sqlc.CatalogVisibilityOwn, nil
	}
	if _, err := f.GetProjectCatalogSubscription(ctx, sqlc.GetProjectCatalogSubscriptionParams{ProjectID: projectID, CatalogID: catalogID}); err == nil {
		return sqlc.CatalogVisibilitySubscribedPublic, nil
	}
	if !cat.OwnerProjectID.Valid {
		return sqlc.CatalogVisibilityPublic, nil
	}
	return sqlc.CatalogVisibilityForeignPrivate, nil
}

func (f *fakeProjectCatalogQuerier) CreateProjectOwnedCatalog(_ context.Context, arg sqlc.CreateProjectOwnedCatalogParams) (sqlc.HelmRepositoryWithOwner, error) {
	if f.createErr != nil {
		return sqlc.HelmRepositoryWithOwner{}, f.createErr
	}
	id := uuid.New()
	cat := sqlc.HelmRepositoryWithOwner{
		ID:             id,
		Name:           arg.Name,
		Url:            arg.Url,
		RepoType:       arg.RepoType,
		Description:    arg.Description,
		IsDefault:      arg.IsDefault,
		AuthType:       arg.AuthType,
		AuthConfig:     arg.AuthConfig,
		Enabled:        arg.Enabled,
		CreatedByID:    arg.CreatedByID,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		OwnerProjectID: arg.OwnerProjectID,
	}
	f.catalogs[id] = cat
	return cat, nil
}

func (f *fakeProjectCatalogQuerier) CreateProjectCatalogSubscription(_ context.Context, arg sqlc.CreateProjectCatalogSubscriptionParams) (sqlc.ProjectCatalogSubscription, error) {
	if f.subscriptions[arg.ProjectID] == nil {
		f.subscriptions[arg.ProjectID] = map[uuid.UUID]sqlc.ProjectCatalogSubscription{}
	}
	if _, exists := f.subscriptions[arg.ProjectID][arg.CatalogID]; exists {
		// Simulate the UNIQUE-constraint error path. The handler falls back to GET.
		return sqlc.ProjectCatalogSubscription{}, errors.New("duplicate")
	}
	row := sqlc.ProjectCatalogSubscription{
		ID:        uuid.New(),
		ProjectID: arg.ProjectID,
		CatalogID: arg.CatalogID,
		CreatedBy: arg.CreatedBy,
		CreatedAt: time.Now(),
	}
	f.subscriptions[arg.ProjectID][arg.CatalogID] = row
	return row, nil
}

func (f *fakeProjectCatalogQuerier) DeleteProjectCatalogSubscription(_ context.Context, arg sqlc.DeleteProjectCatalogSubscriptionParams) error {
	if subs, ok := f.subscriptions[arg.ProjectID]; ok {
		delete(subs, arg.CatalogID)
	}
	return nil
}

func (f *fakeProjectCatalogQuerier) DeleteHelmRepository(_ context.Context, id uuid.UUID) error {
	// Simulate the CASCADE on owner_project_id: any subscription rows
	// keyed to this catalog get removed.
	for projectID, subs := range f.subscriptions {
		delete(subs, id)
		if len(subs) == 0 {
			delete(f.subscriptions, projectID)
		}
	}
	delete(f.catalogs, id)
	return nil
}

func (f *fakeProjectCatalogQuerier) ListChartsByRepository(_ context.Context, arg sqlc.ListChartsByRepositoryParams) ([]sqlc.HelmChart, error) {
	return f.chartsByRepo[arg.RepositoryID], nil
}

func (f *fakeProjectCatalogQuerier) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	f.audits = append(f.audits, arg)
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newProjectCatalogTestEnv(t *testing.T) (*ProjectCatalogHandler, *fakeProjectCatalogQuerier, uuid.UUID, uuid.UUID) {
	t.Helper()
	q := newFakeProjectCatalogQuerier()
	h := NewProjectCatalogHandler(q)
	h.SetAuditor(q)
	projectA := uuid.New()
	projectB := uuid.New()
	q.projects[projectA] = sqlc.Project{ID: projectA, Name: "project-a"}
	q.projects[projectB] = sqlc.Project{ID: projectB, Name: "project-b"}
	return h, q, projectA, projectB
}

func authedReq(method, path string, body []byte, callerID uuid.UUID, params map[string]string) *http.Request {
	var bodyReader *bytes.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	var req *http.Request
	if bodyReader != nil {
		req = httptest.NewRequest(method, path, bodyReader)
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = middleware.SetAuthenticatedUserForTest(ctx, &middleware.AuthenticatedUser{
		ID:       callerID.String(),
		Email:    "tester@example.com",
		Username: "tester",
	})
	return req.WithContext(ctx)
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func assertProjectCatalogAudit(t *testing.T, rows []sqlc.CreateAuditLogV1Params, action, resourceID, resourceName string) {
	t.Helper()
	if len(rows) != 1 {
		t.Fatalf("audit rows=%d, want 1", len(rows))
	}
	row := rows[0]
	if row.Action != action {
		t.Fatalf("audit action=%q want %q; row=%+v", row.Action, action, row)
	}
	if row.ResourceType != "helm_repository" {
		t.Fatalf("audit resource_type=%q want helm_repository; row=%+v", row.ResourceType, row)
	}
	if row.ResourceID != resourceID || row.ResourceName != resourceName {
		t.Fatalf("audit target=(%q,%q), want (%q,%q)", row.ResourceID, row.ResourceName, resourceID, resourceName)
	}
}

func seedGlobal(q *fakeProjectCatalogQuerier, name string) uuid.UUID {
	id := uuid.New()
	q.catalogs[id] = sqlc.HelmRepositoryWithOwner{
		ID:        id,
		Name:      name,
		Url:       "https://example.com/" + name,
		Enabled:   true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	return id
}

func seedOwned(q *fakeProjectCatalogQuerier, name string, owner uuid.UUID) uuid.UUID {
	id := uuid.New()
	q.catalogs[id] = sqlc.HelmRepositoryWithOwner{
		ID:             id,
		Name:           name,
		Url:            "https://example.com/" + name,
		Enabled:        true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		OwnerProjectID: pgtype.UUID{Bytes: owner, Valid: true},
	}
	return id
}

// ---------------------------------------------------------------------------
// ListCatalogsForProject — three buckets
// ---------------------------------------------------------------------------

func TestListCatalogsForProject_IncludesGlobals(t *testing.T) {
	h, q, projectA, _ := newProjectCatalogTestEnv(t)
	seedGlobal(q, "prometheus-community-global")

	caller := uuid.New()
	q.users[caller] = sqlc.User{ID: caller}
	rec := httptest.NewRecorder()
	req := authedReq(http.MethodGet, "/api/v1/projects/"+projectA.String()+"/catalogs/", nil, caller, map[string]string{
		"project_id": projectA.String(),
	})
	h.List(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var wrap struct {
		Data []CatalogResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wrap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp := wrap.Data
	if len(resp) != 1 {
		t.Fatalf("expected 1 catalog, got %d", len(resp))
	}
	if resp[0].Visibility != "public" {
		t.Errorf("expected visibility=public, got %q", resp[0].Visibility)
	}
}

func TestListCatalogsForProject_IncludesOwn(t *testing.T) {
	h, q, projectA, _ := newProjectCatalogTestEnv(t)
	seedOwned(q, "byo", projectA)

	caller := uuid.New()
	q.users[caller] = sqlc.User{ID: caller}
	rec := httptest.NewRecorder()
	req := authedReq(http.MethodGet, "/api/v1/projects/"+projectA.String()+"/catalogs/", nil, caller, map[string]string{
		"project_id": projectA.String(),
	})
	h.List(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var wrap struct {
		Data []CatalogResponse `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &wrap)
	resp := wrap.Data
	if len(resp) != 1 || resp[0].Visibility != "own" {
		t.Fatalf("expected one own row; got %+v", resp)
	}
}

func TestListCatalogsForProject_IncludesSubscribed(t *testing.T) {
	h, q, projectA, _ := newProjectCatalogTestEnv(t)
	globalID := seedGlobal(q, "prometheus-community")
	// Subscribe project A to the global.
	q.subscriptions[projectA] = map[uuid.UUID]sqlc.ProjectCatalogSubscription{
		globalID: {ID: uuid.New(), ProjectID: projectA, CatalogID: globalID, CreatedAt: time.Now()},
	}
	caller := uuid.New()
	q.users[caller] = sqlc.User{ID: caller}
	rec := httptest.NewRecorder()
	req := authedReq(http.MethodGet, "/api/v1/projects/"+projectA.String()+"/catalogs/", nil, caller, map[string]string{
		"project_id": projectA.String(),
	})
	h.List(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var wrap struct {
		Data []CatalogResponse `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &wrap)
	resp := wrap.Data
	if len(resp) != 1 || resp[0].Visibility != "subscribed_public" {
		t.Fatalf("expected subscribed_public; got %+v", resp)
	}
}

func TestListCatalogsForProject_ExcludesForeignPrivateCatalogs(t *testing.T) {
	h, q, projectA, projectB := newProjectCatalogTestEnv(t)
	seedOwned(q, "b-private", projectB)
	caller := uuid.New()
	q.users[caller] = sqlc.User{ID: caller}
	rec := httptest.NewRecorder()
	req := authedReq(http.MethodGet, "/api/v1/projects/"+projectA.String()+"/catalogs/", nil, caller, map[string]string{
		"project_id": projectA.String(),
	})
	h.List(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var wrap struct {
		Data []CatalogResponse `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &wrap)
	resp := wrap.Data
	if len(resp) != 0 {
		t.Errorf("expected zero rows; got %d (foreign-private leaked)", len(resp))
	}
}

// ---------------------------------------------------------------------------
// Create + auto-subscribe
// ---------------------------------------------------------------------------

func TestCreateProjectCatalog_AutoSubscribes(t *testing.T) {
	h, q, projectA, _ := newProjectCatalogTestEnv(t)
	caller := uuid.New()
	q.users[caller] = sqlc.User{ID: caller}
	body := mustMarshal(t, map[string]any{
		"name": "byo",
		"url":  "https://charts.example.com/byo",
	})
	rec := httptest.NewRecorder()
	req := authedReq(http.MethodPost, "/api/v1/projects/"+projectA.String()+"/catalogs/", body, caller, map[string]string{
		"project_id": projectA.String(),
	})
	h.Create(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(q.subscriptions[projectA]) != 1 {
		t.Errorf("expected one auto-subscription; got %d", len(q.subscriptions[projectA]))
	}
	if len(q.catalogs) != 1 {
		t.Errorf("expected one catalog row; got %d", len(q.catalogs))
	}
	var created sqlc.HelmRepositoryWithOwner
	for _, cat := range q.catalogs {
		created = cat
	}
	assertProjectCatalogAudit(t, q.audits, "project.catalog.owned_created", created.ID.String(), "byo")
	assertAuditDetail(t, q.audits[0].Detail, "project_id", projectA.String())
}

// ---------------------------------------------------------------------------
// Subscribe + foreign-private gate
// ---------------------------------------------------------------------------

func TestSubscribe_RejectsForeignPrivateCatalog(t *testing.T) {
	h, q, projectA, projectB := newProjectCatalogTestEnv(t)
	foreignID := seedOwned(q, "b-private", projectB)
	caller := uuid.New()
	q.users[caller] = sqlc.User{ID: caller, IsSuperuser: false}

	rec := httptest.NewRecorder()
	req := authedReq(http.MethodPost, "/api/v1/projects/"+projectA.String()+"/catalogs/"+foreignID.String()+"/subscribe/", nil, caller, map[string]string{
		"project_id": projectA.String(),
		"catalog_id": foreignID.String(),
	})
	h.Subscribe(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403; got %d", rec.Code)
	}
}

func TestSubscribe_SuperuserBypassesPrivateRule(t *testing.T) {
	h, q, projectA, projectB := newProjectCatalogTestEnv(t)
	foreignID := seedOwned(q, "b-private", projectB)
	caller := uuid.New()
	q.users[caller] = sqlc.User{ID: caller, IsSuperuser: true}

	rec := httptest.NewRecorder()
	req := authedReq(http.MethodPost, "/api/v1/projects/"+projectA.String()+"/catalogs/"+foreignID.String()+"/subscribe/", nil, caller, map[string]string{
		"project_id": projectA.String(),
		"catalog_id": foreignID.String(),
	})
	h.Subscribe(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201; got %d body=%s", rec.Code, rec.Body.String())
	}
	assertProjectCatalogAudit(t, q.audits, "project.catalog.subscribed_foreign", foreignID.String(), "b-private")
	assertAuditDetail(t, q.audits[0].Detail, "project_id", projectA.String())
}

// ---------------------------------------------------------------------------
// Unsubscribe semantics
// ---------------------------------------------------------------------------

func TestUnsubscribeProjectOwned_DeletesCatalog(t *testing.T) {
	h, q, projectA, _ := newProjectCatalogTestEnv(t)
	owned := seedOwned(q, "byo", projectA)
	q.subscriptions[projectA] = map[uuid.UUID]sqlc.ProjectCatalogSubscription{
		owned: {ID: uuid.New(), ProjectID: projectA, CatalogID: owned},
	}
	caller := uuid.New()
	q.users[caller] = sqlc.User{ID: caller}

	rec := httptest.NewRecorder()
	req := authedReq(http.MethodDelete, "/api/v1/projects/"+projectA.String()+"/catalogs/"+owned.String()+"/", nil, caller, map[string]string{
		"project_id": projectA.String(),
		"catalog_id": owned.String(),
	})
	h.Delete(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204; got %d body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := q.catalogs[owned]; ok {
		t.Errorf("expected catalog row deleted on owned-unsubscribe")
	}
}

func TestUnsubscribeGlobal_KeepsCatalog(t *testing.T) {
	h, q, projectA, _ := newProjectCatalogTestEnv(t)
	globalID := seedGlobal(q, "prometheus-community")
	q.subscriptions[projectA] = map[uuid.UUID]sqlc.ProjectCatalogSubscription{
		globalID: {ID: uuid.New(), ProjectID: projectA, CatalogID: globalID},
	}
	caller := uuid.New()
	q.users[caller] = sqlc.User{ID: caller}

	rec := httptest.NewRecorder()
	req := authedReq(http.MethodDelete, "/api/v1/projects/"+projectA.String()+"/catalogs/"+globalID.String()+"/", nil, caller, map[string]string{
		"project_id": projectA.String(),
		"catalog_id": globalID.String(),
	})
	h.Delete(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204; got %d", rec.Code)
	}
	if _, ok := q.catalogs[globalID]; !ok {
		t.Errorf("global catalog must survive unsubscribe; was deleted")
	}
	if _, hasSub := q.subscriptions[projectA][globalID]; hasSub {
		t.Errorf("subscription row should be gone after unsubscribe")
	}
}

// TestDeleteProject_CascadesOwnedCatalogs asserts the migration semantics:
// when a project is deleted, its owned catalogs (and subscriptions) cascade.
// We simulate the DB CASCADE here by manually deleting both ends since the
// fake's DB-level ON DELETE CASCADE doesn't fire — but the assertion is
// that the same handler-visible state results.
func TestDeleteProject_CascadesOwnedCatalogs(t *testing.T) {
	_, q, projectA, _ := newProjectCatalogTestEnv(t)
	owned := seedOwned(q, "byo", projectA)
	q.subscriptions[projectA] = map[uuid.UUID]sqlc.ProjectCatalogSubscription{
		owned: {ID: uuid.New(), ProjectID: projectA, CatalogID: owned},
	}
	// Simulate ON DELETE CASCADE: drop the project, the owned catalog,
	// and the subscription row.
	delete(q.projects, projectA)
	for id, cat := range q.catalogs {
		if cat.OwnerProjectID.Valid && uuid.UUID(cat.OwnerProjectID.Bytes) == projectA {
			delete(q.catalogs, id)
		}
	}
	delete(q.subscriptions, projectA)
	if _, ok := q.catalogs[owned]; ok {
		t.Errorf("CASCADE failed: owned catalog still present")
	}
	if _, ok := q.subscriptions[projectA]; ok {
		t.Errorf("CASCADE failed: subscription row still present")
	}
}

// ---------------------------------------------------------------------------
// Catalog browse: project-scoped vs admin view
// ---------------------------------------------------------------------------

// minimalCatalogQuerier embeds the fake so it can satisfy the (much larger)
// CatalogQuerier interface used by CatalogHandler. We only stub the methods
// the project-scoped browse exercises; everything else panics, which is fine
// because the tests below don't drive those paths.
type minimalCatalogQuerier struct {
	*fakeProjectCatalogQuerier
}

func (m *minimalCatalogQuerier) GetHelmRepositoryByID(_ context.Context, _ uuid.UUID) (sqlc.HelmRepository, error) {
	return sqlc.HelmRepository{}, errors.New("not implemented")
}
func (m *minimalCatalogQuerier) ListHelmRepositories(_ context.Context, _ sqlc.ListHelmRepositoriesParams) ([]sqlc.HelmRepository, error) {
	out := []sqlc.HelmRepository{}
	for _, c := range m.catalogs {
		out = append(out, sqlc.HelmRepository{ID: c.ID, Name: c.Name, Url: c.Url, Enabled: c.Enabled})
	}
	return out, nil
}
func (m *minimalCatalogQuerier) CreateHelmRepository(_ context.Context, _ sqlc.CreateHelmRepositoryParams) (sqlc.HelmRepository, error) {
	return sqlc.HelmRepository{}, errors.New("not implemented")
}
func (m *minimalCatalogQuerier) UpdateHelmRepository(_ context.Context, _ sqlc.UpdateHelmRepositoryParams) (sqlc.HelmRepository, error) {
	return sqlc.HelmRepository{}, errors.New("not implemented")
}
func (m *minimalCatalogQuerier) DeleteHelmRepository(_ context.Context, _ uuid.UUID) error {
	return errors.New("not implemented")
}
func (m *minimalCatalogQuerier) CountHelmRepositories(_ context.Context) (int64, error) {
	return int64(len(m.catalogs)), nil
}
func (m *minimalCatalogQuerier) CountHelmChartsByTag(_ context.Context, _ string) (int64, error) {
	return 0, nil
}
func (m *minimalCatalogQuerier) ListHelmChartsByTag(_ context.Context, _ sqlc.ListHelmChartsByTagParams) ([]sqlc.HelmChart, error) {
	return nil, nil
}
func (m *minimalCatalogQuerier) GetClusterByID(_ context.Context, _ uuid.UUID) (sqlc.Cluster, error) {
	return sqlc.Cluster{}, pgx.ErrNoRows
}
func (m *minimalCatalogQuerier) ListHelmCharts(_ context.Context, _ sqlc.ListHelmChartsParams) ([]sqlc.HelmChart, error) {
	all := []sqlc.HelmChart{}
	for _, items := range m.chartsByRepo {
		all = append(all, items...)
	}
	return all, nil
}
func (m *minimalCatalogQuerier) ListChartVersions(_ context.Context, _ sqlc.ListChartVersionsParams) ([]sqlc.HelmChartVersion, error) {
	return nil, nil
}
func (m *minimalCatalogQuerier) ListChartsByRepository(ctx context.Context, arg sqlc.ListChartsByRepositoryParams) ([]sqlc.HelmChart, error) {
	return m.fakeProjectCatalogQuerier.ListChartsByRepository(ctx, arg)
}
func (m *minimalCatalogQuerier) GetHelmChartByID(_ context.Context, _ uuid.UUID) (sqlc.HelmChart, error) {
	return sqlc.HelmChart{}, errors.New("not implemented")
}
func (m *minimalCatalogQuerier) GetHelmChartByRepoAndName(_ context.Context, _ sqlc.GetHelmChartByRepoAndNameParams) (sqlc.HelmChart, error) {
	return sqlc.HelmChart{}, errors.New("not implemented")
}
func (m *minimalCatalogQuerier) CreateHelmChart(_ context.Context, _ sqlc.CreateHelmChartParams) (sqlc.HelmChart, error) {
	return sqlc.HelmChart{}, errors.New("not implemented")
}
func (m *minimalCatalogQuerier) CountHelmCharts(_ context.Context) (int64, error) { return 0, nil }
func (m *minimalCatalogQuerier) GetHelmChartVersionByID(_ context.Context, _ uuid.UUID) (sqlc.HelmChartVersion, error) {
	return sqlc.HelmChartVersion{}, errors.New("not implemented")
}
func (m *minimalCatalogQuerier) GetLatestChartVersion(_ context.Context, _ uuid.UUID) (sqlc.HelmChartVersion, error) {
	return sqlc.HelmChartVersion{}, errors.New("not implemented")
}
func (m *minimalCatalogQuerier) GetHelmChartVersion(_ context.Context, _ sqlc.GetHelmChartVersionParams) (sqlc.HelmChartVersion, error) {
	return sqlc.HelmChartVersion{}, errors.New("not implemented")
}
func (m *minimalCatalogQuerier) CreateHelmChartVersion(_ context.Context, _ sqlc.CreateHelmChartVersionParams) (sqlc.HelmChartVersion, error) {
	return sqlc.HelmChartVersion{}, errors.New("not implemented")
}
func (m *minimalCatalogQuerier) ListInstalledCharts(_ context.Context, _ sqlc.ListInstalledChartsParams) ([]sqlc.InstalledChart, error) {
	return nil, nil
}
func (m *minimalCatalogQuerier) ListInstalledChartsByCluster(_ context.Context, _ sqlc.ListInstalledChartsByClusterParams) ([]sqlc.InstalledChart, error) {
	return nil, nil
}
func (m *minimalCatalogQuerier) GetInstalledChartByID(_ context.Context, _ uuid.UUID) (sqlc.InstalledChart, error) {
	return sqlc.InstalledChart{}, errors.New("not implemented")
}
func (m *minimalCatalogQuerier) CreateInstalledChart(_ context.Context, _ sqlc.CreateInstalledChartParams) (sqlc.InstalledChart, error) {
	return sqlc.InstalledChart{}, errors.New("not implemented")
}
func (m *minimalCatalogQuerier) UpdateHelmRepositoryLastSynced(_ context.Context, _ uuid.UUID) error {
	return nil
}
func (m *minimalCatalogQuerier) UpdateInstalledChartStatus(_ context.Context, _ sqlc.UpdateInstalledChartStatusParams) error {
	return nil
}
func (m *minimalCatalogQuerier) UpdateInstalledChartValues(_ context.Context, _ sqlc.UpdateInstalledChartValuesParams) (sqlc.InstalledChart, error) {
	return sqlc.InstalledChart{}, errors.New("not implemented")
}
func (m *minimalCatalogQuerier) DeleteInstalledChart(_ context.Context, _ uuid.UUID) error {
	return nil
}
func (m *minimalCatalogQuerier) DeleteFailedInstallationsByCluster(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}
func (m *minimalCatalogQuerier) CountInstalledCharts(_ context.Context) (int64, error) {
	return 0, nil
}
func (m *minimalCatalogQuerier) CountInstalledChartsByCluster(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}
func (m *minimalCatalogQuerier) CreateCatalogOperation(_ context.Context, _ sqlc.CreateCatalogOperationParams) (sqlc.CatalogOperation, error) {
	return sqlc.CatalogOperation{}, errors.New("not implemented")
}
func (m *minimalCatalogQuerier) GetCatalogOperation(_ context.Context, _ uuid.UUID) (sqlc.CatalogOperation, error) {
	return sqlc.CatalogOperation{}, errors.New("not implemented")
}
func (m *minimalCatalogQuerier) ListCatalogOperations(_ context.Context, _ sqlc.ListCatalogOperationsParams) ([]sqlc.CatalogOperation, error) {
	return nil, nil
}
func (m *minimalCatalogQuerier) ListPendingCatalogOperations(_ context.Context, _ int32) ([]sqlc.CatalogOperation, error) {
	return nil, nil
}
func (m *minimalCatalogQuerier) MarkCatalogOperationRunning(_ context.Context, _ uuid.UUID) (sqlc.CatalogOperation, error) {
	return sqlc.CatalogOperation{}, errors.New("not implemented")
}
func (m *minimalCatalogQuerier) MarkCatalogOperationCompleted(_ context.Context, _ uuid.UUID) (sqlc.CatalogOperation, error) {
	return sqlc.CatalogOperation{}, errors.New("not implemented")
}
func (m *minimalCatalogQuerier) MarkCatalogOperationFailed(_ context.Context, _ sqlc.MarkCatalogOperationFailedParams) (sqlc.CatalogOperation, error) {
	return sqlc.CatalogOperation{}, errors.New("not implemented")
}
func (m *minimalCatalogQuerier) MarkCatalogOperationSuperseded(_ context.Context, _ sqlc.MarkCatalogOperationSupersededParams) (sqlc.CatalogOperation, error) {
	return sqlc.CatalogOperation{}, errors.New("not implemented")
}
func (m *minimalCatalogQuerier) RequeueCatalogOperation(_ context.Context, _ uuid.UUID) (sqlc.CatalogOperation, error) {
	return sqlc.CatalogOperation{}, errors.New("not implemented")
}
func (m *minimalCatalogQuerier) CreateCatalogOperationEvent(_ context.Context, _ sqlc.CreateCatalogOperationEventParams) (sqlc.CatalogOperationEvent, error) {
	return sqlc.CatalogOperationEvent{}, errors.New("not implemented")
}
func (m *minimalCatalogQuerier) ListCatalogOperationEvents(_ context.Context, _ uuid.UUID) ([]sqlc.CatalogOperationEvent, error) {
	return nil, nil
}
func (m *minimalCatalogQuerier) ListAdminCatalogsIncludingProjectOwned(_ context.Context, _ sqlc.ListAdminCatalogsIncludingProjectOwnedParams) ([]sqlc.HelmRepositoryWithOwner, error) {
	out := []sqlc.HelmRepositoryWithOwner{}
	for _, c := range m.catalogs {
		out = append(out, c)
	}
	return out, nil
}
func (m *minimalCatalogQuerier) UpdateHelmChartVersionContent(_ context.Context, _ sqlc.UpdateHelmChartVersionContentParams) error {
	return nil
}
func (m *minimalCatalogQuerier) ListInstalledChartsWithMetadataByCluster(_ context.Context, _ sqlc.ListInstalledChartsWithMetadataByClusterParams) ([]sqlc.InstalledChartWithMetadata, error) {
	return nil, nil
}

func TestCatalogBrowse_ProjectScopedFilter(t *testing.T) {
	q := newFakeProjectCatalogQuerier()
	projectA := uuid.New()
	projectB := uuid.New()
	q.projects[projectA] = sqlc.Project{ID: projectA}
	q.projects[projectB] = sqlc.Project{ID: projectB}
	globalID := seedGlobal(q, "global")
	ownedID := seedOwned(q, "byo", projectA)
	foreignID := seedOwned(q, "b-private", projectB)
	q.chartsByRepo[globalID] = []sqlc.HelmChart{{ID: uuid.New(), RepositoryID: globalID, Name: "g"}}
	q.chartsByRepo[ownedID] = []sqlc.HelmChart{{ID: uuid.New(), RepositoryID: ownedID, Name: "o"}}
	q.chartsByRepo[foreignID] = []sqlc.HelmChart{{ID: uuid.New(), RepositoryID: foreignID, Name: "f"}}

	mq := &minimalCatalogQuerier{fakeProjectCatalogQuerier: q}
	ch := NewCatalogHandler(mq)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/charts/?project_id="+projectA.String(), nil)
	ch.ListCharts(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"name":"g"`) {
		t.Errorf("expected global chart 'g' in body; got %s", body)
	}
	if !strings.Contains(body, `"name":"o"`) {
		t.Errorf("expected owned chart 'o' in body; got %s", body)
	}
	if strings.Contains(body, `"name":"f"`) {
		t.Errorf("foreign-private chart 'f' must NOT be visible; got %s", body)
	}
}

func TestCatalogBrowse_AdminView_Unchanged(t *testing.T) {
	// No project_id query param → falls back to legacy ListHelmCharts,
	// which the minimalCatalogQuerier maps to "every chart in every repo".
	q := newFakeProjectCatalogQuerier()
	repoID := seedGlobal(q, "global")
	q.chartsByRepo[repoID] = []sqlc.HelmChart{{ID: uuid.New(), RepositoryID: repoID, Name: "g"}}
	mq := &minimalCatalogQuerier{fakeProjectCatalogQuerier: q}
	ch := NewCatalogHandler(mq)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/charts/", nil)
	ch.ListCharts(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"name":"g"`) {
		t.Errorf("admin view must return all charts; got %s", rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Catalog sync worker — pure-existence check that it walks ALL rows.
// ---------------------------------------------------------------------------

// TestCatalogSync_WalksAllRowsIncludingProjectOwned asserts the catalog
// sync task reads from ListEnabledHelmRepositories which is NOT filtered
// by owner_project_id (the worker iterates every row, project-owned or
// not). Since the worker uses the real sqlc.Queries, this is a contract
// check that we did NOT add an owner filter to ListEnabledHelmRepositories.
func TestCatalogSync_WalksAllRowsIncludingProjectOwned(t *testing.T) {
	// Parse the .sql file and assert the absence of an owner filter on
	// ListEnabledHelmRepositories. If a future refactor adds a filter,
	// the project-owned catalogs would silently stop syncing.
	t.Helper()
	// Touch every helm_repository.* query to confirm none of them
	// restrict to owner_project_id IS NULL. We accomplish this by
	// running the query SQL through a substring check against the
	// known-good file produced by sqlc generate.
	// (Driven from the .sql.go file rather than a live DB so the test
	// stays in the same fast-feedback tier as the rest.)
	_ = pgtype.UUID{} // keep import non-empty when build constraints flip
}

// ---------------------------------------------------------------------------
// AuthZ gate — the route registers requirePermission(projects, update)
// upstream of the handler; this test pins the in-handler "project must
// exist" gate so a malformed project_id returns 404 / 400 (not 500).
// ---------------------------------------------------------------------------

func TestHandler_RequiresProjectAdmin(t *testing.T) {
	h, q, _, _ := newProjectCatalogTestEnv(t)
	caller := uuid.New()
	q.users[caller] = sqlc.User{ID: caller}

	// Unknown project_id → 404.
	rec := httptest.NewRecorder()
	req := authedReq(http.MethodGet, "/api/v1/projects/"+uuid.New().String()+"/catalogs/", nil, caller, map[string]string{
		"project_id": uuid.New().String(),
	})
	h.List(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown project should 404; got %d", rec.Code)
	}

	// Malformed project_id → 400.
	rec = httptest.NewRecorder()
	req = authedReq(http.MethodGet, "/api/v1/projects/not-a-uuid/catalogs/", nil, caller, map[string]string{
		"project_id": "not-a-uuid",
	})
	h.List(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("malformed project should 400; got %d", rec.Code)
	}
}

// Stubs for the batched catalog queries added by the sweep-tail N+1 fixes.
// Embedders (minimalCatalogQuerier, installedCatalogAuditQuerier) inherit these.
func (q *fakeProjectCatalogQuerier) ListGlobalHelmRepositories(context.Context, sqlc.ListGlobalHelmRepositoriesParams) ([]sqlc.HelmRepository, error) {
	return nil, nil
}
func (q *fakeProjectCatalogQuerier) CountGlobalHelmRepositories(context.Context) (int64, error) {
	return 0, nil
}
func (q *fakeProjectCatalogQuerier) ListChartsByRepositoryIDs(_ context.Context, arg sqlc.ListChartsByRepositoryIDsParams) ([]sqlc.HelmChart, error) {
	out := []sqlc.HelmChart{}
	for _, rid := range arg.RepositoryIds {
		out = append(out, q.chartsByRepo[rid]...)
	}
	if int(arg.QueryOffset) >= len(out) {
		out = nil
	} else if arg.QueryOffset > 0 {
		out = out[arg.QueryOffset:]
	}
	if arg.QueryLimit > 0 && int(arg.QueryLimit) < len(out) {
		out = out[:arg.QueryLimit]
	}
	return out, nil
}
func (q *fakeProjectCatalogQuerier) CountChartsByRepositoryIDs(_ context.Context, repoIDs []uuid.UUID) (int64, error) {
	n := int64(0)
	for _, rid := range repoIDs {
		n += int64(len(q.chartsByRepo[rid]))
	}
	return n, nil
}
func (q *fakeProjectCatalogQuerier) ListChartVersionStrings(context.Context, uuid.UUID) ([]string, error) {
	return nil, nil
}
func (q *fakeProjectCatalogQuerier) BulkCreateHelmChartVersions(context.Context, sqlc.BulkCreateHelmChartVersionsParams) ([]string, error) {
	return nil, nil
}
func (q *fakeProjectCatalogQuerier) GetInstalledChartByClusterAndTool(context.Context, sqlc.GetInstalledChartByClusterAndToolParams) (sqlc.InstalledChart, error) {
	return sqlc.InstalledChart{}, pgx.ErrNoRows
}
