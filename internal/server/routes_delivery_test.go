package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	deliveryhandler "github.com/alphabravocompany/astronomer-go/internal/handler/delivery"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

type deliveryRouteRBACQuerier struct {
	bindings []rbac.RoleBinding
}

func (q deliveryRouteRBACQuerier) GetUserBindings(context.Context, string) ([]rbac.RoleBinding, error) {
	return append([]rbac.RoleBinding(nil), q.bindings...), nil
}

type deliverySourceRouteStore struct {
	projectID       uuid.UUID
	sourceID        uuid.UUID
	resolutionID    uuid.UUID
	listCalls       int
	createCalls     int
	getCalls        int
	resolutionCalls int
	lastGet         sqlc.GetDeliverySourceParams
	lastResolution  sqlc.CreateDeliverySourceResolutionAndOutboxParams
}

func (s *deliverySourceRouteStore) CountDeliverySources(context.Context, sqlc.CountDeliverySourcesParams) (int64, error) {
	return 0, nil
}

func (s *deliverySourceRouteStore) ListDeliverySources(_ context.Context, arg sqlc.ListDeliverySourcesParams) ([]sqlc.ListDeliverySourcesRow, error) {
	s.listCalls++
	if arg.ProjectID != s.projectID {
		return nil, pgx.ErrNoRows
	}
	return []sqlc.ListDeliverySourcesRow{}, nil
}

func (s *deliverySourceRouteStore) CreateDeliverySource(_ context.Context, arg sqlc.CreateDeliverySourceParams) (sqlc.CreateDeliverySourceRow, error) {
	s.createCalls++
	if arg.ProjectID != s.projectID {
		return sqlc.CreateDeliverySourceRow{}, pgx.ErrNoRows
	}
	now := time.Now().UTC()
	return sqlc.CreateDeliverySourceRow{
		ID: s.sourceID, ProjectID: arg.ProjectID, Name: arg.Name, Description: arg.Description,
		SourceType: arg.SourceType, Url: arg.Url, AuthMode: arg.AuthMode,
		CredentialKeyVersion: arg.CredentialKeyVersion, CredentialEpoch: arg.CredentialEpoch,
		ProxyRef: arg.ProxyRef, TrustPolicy: arg.TrustPolicy, Status: "pending", CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (s *deliverySourceRouteStore) GetDeliverySource(_ context.Context, arg sqlc.GetDeliverySourceParams) (sqlc.GetDeliverySourceRow, error) {
	s.getCalls++
	s.lastGet = arg
	if arg.ID != s.sourceID || arg.ProjectID != s.projectID {
		return sqlc.GetDeliverySourceRow{}, pgx.ErrNoRows
	}
	now := time.Now().UTC()
	return sqlc.GetDeliverySourceRow{
		ID: s.sourceID, ProjectID: s.projectID, Name: "source", SourceType: "git",
		Url: "https://git.example.test/platform/config.git", AuthMode: "none",
		TrustPolicy: json.RawMessage(`{"allow_unsigned":true}`), Status: "ready", CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (s *deliverySourceRouteStore) DeleteDeliverySource(_ context.Context, arg sqlc.DeleteDeliverySourceParams) (int64, error) {
	if arg.ID != s.sourceID || arg.ProjectID != s.projectID {
		return 0, nil
	}
	return 1, nil
}

func (s *deliverySourceRouteStore) UpdateDeliverySource(context.Context, sqlc.UpdateDeliverySourceParams) (sqlc.UpdateDeliverySourceRow, error) {
	return sqlc.UpdateDeliverySourceRow{}, pgx.ErrNoRows
}

func (s *deliverySourceRouteStore) RotateDeliverySourceCredential(context.Context, sqlc.RotateDeliverySourceCredentialParams) (sqlc.RotateDeliverySourceCredentialRow, error) {
	return sqlc.RotateDeliverySourceCredentialRow{}, pgx.ErrNoRows
}

func (s *deliverySourceRouteStore) CreateDeliverySourceResolutionAndOutbox(_ context.Context, arg sqlc.CreateDeliverySourceResolutionAndOutboxParams) (sqlc.CreateDeliverySourceResolutionAndOutboxRow, error) {
	s.resolutionCalls++
	s.lastResolution = arg
	if arg.SourceID != s.sourceID {
		return sqlc.CreateDeliverySourceResolutionAndOutboxRow{}, pgx.ErrNoRows
	}
	return sqlc.CreateDeliverySourceResolutionAndOutboxRow{
		ID:                s.resolutionID,
		SourceID:          arg.SourceID,
		BundleVersionID:   arg.BundleVersionID,
		RequestedRevision: arg.RequestedRevision,
		ChartName:         arg.ChartName,
		Status:            "pending",
	}, nil
}

type deliveryBundleRouteStore struct {
	projectID uuid.UUID
	bundleID  uuid.UUID
	listCalls int
}

func (s *deliveryBundleRouteStore) CountComponentBundles(context.Context, uuid.UUID) (int64, error) {
	return 0, nil
}

func (s *deliveryBundleRouteStore) ListComponentBundles(_ context.Context, arg sqlc.ListComponentBundlesParams) ([]sqlc.ComponentBundle, error) {
	s.listCalls++
	if arg.ProjectID != s.projectID {
		return nil, pgx.ErrNoRows
	}
	return []sqlc.ComponentBundle{}, nil
}

func (s *deliveryBundleRouteStore) CreateComponentBundle(context.Context, sqlc.CreateComponentBundleParams) (sqlc.ComponentBundle, error) {
	return sqlc.ComponentBundle{}, pgx.ErrNoRows
}

func (s *deliveryBundleRouteStore) GetComponentBundle(context.Context, sqlc.GetComponentBundleParams) (sqlc.ComponentBundle, error) {
	return sqlc.ComponentBundle{}, pgx.ErrNoRows
}

func (s *deliveryBundleRouteStore) UpdateComponentBundle(context.Context, sqlc.UpdateComponentBundleParams) (sqlc.ComponentBundle, error) {
	return sqlc.ComponentBundle{}, pgx.ErrNoRows
}

func (s *deliveryBundleRouteStore) DeleteComponentBundle(context.Context, sqlc.DeleteComponentBundleParams) (int64, error) {
	return 0, nil
}

func (s *deliveryBundleRouteStore) CreateComponentBundleVersion(context.Context, sqlc.CreateComponentBundleVersionParams) (sqlc.ComponentBundleVersion, error) {
	return sqlc.ComponentBundleVersion{}, pgx.ErrNoRows
}

func (s *deliveryBundleRouteStore) ListComponentBundleVersions(context.Context, sqlc.ListComponentBundleVersionsParams) ([]sqlc.ComponentBundleVersion, error) {
	return nil, nil
}

func (s *deliveryBundleRouteStore) GetComponentBundleVersion(context.Context, sqlc.GetComponentBundleVersionParams) (sqlc.ComponentBundleVersion, error) {
	return sqlc.ComponentBundleVersion{}, pgx.ErrNoRows
}

func (s *deliveryBundleRouteStore) GetDeliverySource(context.Context, sqlc.GetDeliverySourceParams) (sqlc.GetDeliverySourceRow, error) {
	return sqlc.GetDeliverySourceRow{}, pgx.ErrNoRows
}

func (s *deliveryBundleRouteStore) CreateDeliverySourceResolutionAndOutbox(context.Context, sqlc.CreateDeliverySourceResolutionAndOutboxParams) (sqlc.CreateDeliverySourceResolutionAndOutboxRow, error) {
	return sqlc.CreateDeliverySourceResolutionAndOutboxRow{}, pgx.ErrNoRows
}

func (s *deliveryBundleRouteStore) FailComponentBundleVersion(context.Context, sqlc.FailComponentBundleVersionParams) (sqlc.ComponentBundleVersion, error) {
	return sqlc.ComponentBundleVersion{}, pgx.ErrNoRows
}

func TestDeliveryRoutesRequireAuthentication(t *testing.T) {
	projectID := uuid.New()
	store := &deliverySourceRouteStore{projectID: projectID, sourceID: uuid.New()}
	router, _ := newDeliveryRouteTestRouter(t, nil, store, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/delivery/sources/?project_id="+projectID.String(), nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.listCalls != 0 {
		t.Fatal("unauthenticated request reached delivery persistence")
	}
}

func TestDeliveryRoutesEnforceDedicatedProjectScopedRBAC(t *testing.T) {
	projectA := uuid.New()
	projectB := uuid.New()
	sourceStore := &deliverySourceRouteStore{projectID: projectA, sourceID: uuid.New()}
	bundleStore := &deliveryBundleRouteStore{projectID: projectA, bundleID: uuid.New()}
	bindings := deliveryProjectBinding(projectA, rbac.ResourceDeliverySources, rbac.VerbList)
	router, token := newDeliveryRouteTestRouter(t, bindings, sourceStore, bundleStore)

	allowed := authenticatedDeliveryRequest(http.MethodGet, "/api/v1/delivery/sources/?project_id="+projectA.String(), nil, token)
	allowedRecorder := httptest.NewRecorder()
	router.ServeHTTP(allowedRecorder, allowed)
	if allowedRecorder.Code != http.StatusOK || sourceStore.listCalls != 1 {
		t.Fatalf("allowed status=%d calls=%d body=%s", allowedRecorder.Code, sourceStore.listCalls, allowedRecorder.Body.String())
	}

	wrongProject := authenticatedDeliveryRequest(http.MethodGet, "/api/v1/delivery/sources/?project_id="+projectB.String(), nil, token)
	wrongProjectRecorder := httptest.NewRecorder()
	router.ServeHTTP(wrongProjectRecorder, wrongProject)
	if wrongProjectRecorder.Code != http.StatusForbidden || sourceStore.listCalls != 1 {
		t.Fatalf("cross-project status=%d calls=%d body=%s", wrongProjectRecorder.Code, sourceStore.listCalls, wrongProjectRecorder.Body.String())
	}

	wrongResource := authenticatedDeliveryRequest(http.MethodGet, "/api/v1/delivery/bundles/?project_id="+projectA.String(), nil, token)
	wrongResourceRecorder := httptest.NewRecorder()
	router.ServeHTTP(wrongResourceRecorder, wrongResource)
	if wrongResourceRecorder.Code != http.StatusForbidden || bundleStore.listCalls != 0 {
		t.Fatalf("cross-resource status=%d calls=%d body=%s", wrongResourceRecorder.Code, bundleStore.listCalls, wrongResourceRecorder.Body.String())
	}

	bundleRouter, bundleToken := newDeliveryRouteTestRouter(t, deliveryProjectBinding(projectA, rbac.ResourceDeliveryBundles, rbac.VerbList), sourceStore, bundleStore)
	bundleRequest := authenticatedDeliveryRequest(http.MethodGet, "/api/v1/delivery/bundles/", nil, bundleToken)
	bundleRequest.Header.Set("X-Project-ID", projectA.String())
	bundleRecorder := httptest.NewRecorder()
	bundleRouter.ServeHTTP(bundleRecorder, bundleRequest)
	if bundleRecorder.Code != http.StatusOK || bundleStore.listCalls != 1 {
		t.Fatalf("bundle status=%d calls=%d body=%s", bundleRecorder.Code, bundleStore.listCalls, bundleRecorder.Body.String())
	}
}

func TestDeliveryMutationScopeValidationPrecedesPersistence(t *testing.T) {
	projectA := uuid.New()
	projectB := uuid.New()
	store := &deliverySourceRouteStore{projectID: projectA, sourceID: uuid.New()}
	router, token := newDeliveryRouteTestRouter(t, deliveryProjectBinding(projectA, rbac.ResourceDeliverySources, rbac.VerbCreate), store, nil)

	validBody := validDeliverySourceRouteBody(projectA, false)
	valid := authenticatedDeliveryRequest(http.MethodPost, "/api/v1/delivery/sources/", strings.NewReader(validBody), token)
	validRecorder := httptest.NewRecorder()
	router.ServeHTTP(validRecorder, valid)
	if validRecorder.Code != http.StatusCreated || store.createCalls != 1 {
		t.Fatalf("valid status=%d calls=%d body=%s", validRecorder.Code, store.createCalls, validRecorder.Body.String())
	}

	mismatch := authenticatedDeliveryRequest(http.MethodPost, "/api/v1/delivery/sources/?project_id="+projectB.String(), strings.NewReader(validBody), token)
	mismatchRecorder := httptest.NewRecorder()
	router.ServeHTTP(mismatchRecorder, mismatch)
	if mismatchRecorder.Code != http.StatusBadRequest || store.createCalls != 1 {
		t.Fatalf("mismatch status=%d calls=%d body=%s", mismatchRecorder.Code, store.createCalls, mismatchRecorder.Body.String())
	}

	unknown := authenticatedDeliveryRequest(http.MethodPost, "/api/v1/delivery/sources/", strings.NewReader(validDeliverySourceRouteBody(projectA, true)), token)
	unknownRecorder := httptest.NewRecorder()
	router.ServeHTTP(unknownRecorder, unknown)
	if unknownRecorder.Code != http.StatusBadRequest || store.createCalls != 1 {
		t.Fatalf("unknown status=%d calls=%d body=%s", unknownRecorder.Code, store.createCalls, unknownRecorder.Body.String())
	}

	oversizedBody := `{"project_id":"` + projectA.String() + `","description":"` + strings.Repeat("x", deliveryRouteMaxBodyBytes) + `"}`
	oversized := authenticatedDeliveryRequest(http.MethodPost, "/api/v1/delivery/sources/", strings.NewReader(oversizedBody), token)
	oversizedRecorder := httptest.NewRecorder()
	router.ServeHTTP(oversizedRecorder, oversized)
	if oversizedRecorder.Code != http.StatusBadRequest || store.createCalls != 1 {
		t.Fatalf("oversized status=%d calls=%d body=%s", oversizedRecorder.Code, store.createCalls, oversizedRecorder.Body.String())
	}

	headerBody := validDeliverySourceRouteBody(uuid.Nil, false)
	headerScoped := authenticatedDeliveryRequest(http.MethodPost, "/api/v1/delivery/sources/", strings.NewReader(headerBody), token)
	headerScoped.Header.Set("X-Project-ID", projectA.String())
	headerRecorder := httptest.NewRecorder()
	router.ServeHTTP(headerRecorder, headerScoped)
	if headerRecorder.Code != http.StatusCreated || store.createCalls != 2 {
		t.Fatalf("header scope status=%d calls=%d body=%s", headerRecorder.Code, store.createCalls, headerRecorder.Body.String())
	}
}

func TestDeliveryMutationRequiresProjectsWriteTokenScope(t *testing.T) {
	projectID := uuid.New()
	userID := uuid.New()
	store := &deliverySourceRouteStore{projectID: projectID, sourceID: uuid.New()}
	body := validDeliverySourceRouteBody(projectID, false)
	bindings := deliveryProjectBinding(projectID, rbac.ResourceDeliverySources, rbac.VerbCreate)

	for _, test := range []struct {
		name       string
		scopes     json.RawMessage
		wantStatus int
	}{
		{"read only", json.RawMessage(`["read"]`), http.StatusForbidden},
		{"wrong write scope", json.RawMessage(`["clusters:write"]`), http.StatusForbidden},
		{"projects write", json.RawMessage(`["projects:write"]`), http.StatusCreated},
	} {
		t.Run(test.name, func(t *testing.T) {
			rawToken := "astro_delivery_scope_" + strings.ReplaceAll(test.name, " ", "_")
			jwtManager := auth.MustNewJWTManager("delivery-route-test-secret", 60)
			router := NewRouter(&config.Config{}, RouterDependencies{
				JWT: jwtManager, AuthQueries: routeSecurityAPITokenQuerier(rawToken, userID, test.scopes),
				RBACEngine: rbac.NewEngine(), RBACQueries: deliveryRouteRBACQuerier{bindings: bindings},
				DeliverySources: deliveryhandler.NewSourceHandler(store, nil, 1),
			})
			request := httptest.NewRequest(http.MethodPost, "/api/v1/delivery/sources/", strings.NewReader(body))
			request.Header.Set("Authorization", "Bearer "+rawToken)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
		})
	}
}

func TestDeliverySourceVerifyRequiresScopedUpdateAndQueuesDurably(t *testing.T) {
	projectA, projectB := uuid.New(), uuid.New()
	sourceID, resolutionID := uuid.New(), uuid.New()
	store := &deliverySourceRouteStore{
		projectID: projectA, sourceID: sourceID, resolutionID: resolutionID,
	}
	body := `{"project_id":"` + projectA.String() + `","requested_revision":"main"}`
	path := "/api/v1/delivery/sources/" + sourceID.String() + "/verify/"

	createOnlyRouter, createOnlyToken := newDeliveryRouteTestRouter(t,
		deliveryProjectBinding(projectA, rbac.ResourceDeliverySources, rbac.VerbCreate), store, nil)
	denied := authenticatedDeliveryRequest(http.MethodPost, path, strings.NewReader(body), createOnlyToken)
	denied.Header.Set("Idempotency-Key", "verify-create-only")
	deniedRecorder := httptest.NewRecorder()
	createOnlyRouter.ServeHTTP(deniedRecorder, denied)
	if deniedRecorder.Code != http.StatusForbidden || store.getCalls != 0 || store.resolutionCalls != 0 {
		t.Fatalf("create-only status=%d get=%d resolution=%d body=%s", deniedRecorder.Code, store.getCalls, store.resolutionCalls, deniedRecorder.Body.String())
	}

	updateRouter, updateToken := newDeliveryRouteTestRouter(t,
		deliveryProjectBinding(projectA, rbac.ResourceDeliverySources, rbac.VerbUpdate), store, nil)
	wrongProjectBody := `{"project_id":"` + projectB.String() + `","requested_revision":"main"}`
	wrongProject := authenticatedDeliveryRequest(http.MethodPost, path, strings.NewReader(wrongProjectBody), updateToken)
	wrongProject.Header.Set("Idempotency-Key", "verify-wrong-project")
	wrongProjectRecorder := httptest.NewRecorder()
	updateRouter.ServeHTTP(wrongProjectRecorder, wrongProject)
	if wrongProjectRecorder.Code != http.StatusForbidden || store.getCalls != 0 || store.resolutionCalls != 0 {
		t.Fatalf("wrong-project status=%d get=%d resolution=%d body=%s", wrongProjectRecorder.Code, store.getCalls, store.resolutionCalls, wrongProjectRecorder.Body.String())
	}

	allowed := authenticatedDeliveryRequest(http.MethodPost, path, strings.NewReader(body), updateToken)
	allowed.Header.Set("Idempotency-Key", "verify-main")
	allowedRecorder := httptest.NewRecorder()
	updateRouter.ServeHTTP(allowedRecorder, allowed)
	if allowedRecorder.Code != http.StatusAccepted {
		t.Fatalf("allowed status=%d body=%s", allowedRecorder.Code, allowedRecorder.Body.String())
	}
	if store.getCalls != 1 || store.lastGet.ID != sourceID || store.lastGet.ProjectID != projectA {
		t.Fatalf("source lookup was not project scoped: calls=%d arg=%#v", store.getCalls, store.lastGet)
	}
	if store.resolutionCalls != 1 || store.lastResolution.SourceID != sourceID ||
		store.lastResolution.BundleVersionID.Valid || store.lastResolution.RequestedRevision != "main" ||
		store.lastResolution.ChartName != "" {
		t.Fatalf("atomic resolution/outbox enqueue mismatch: calls=%d arg=%#v", store.resolutionCalls, store.lastResolution)
	}
	if !strings.Contains(allowedRecorder.Body.String(), resolutionID.String()) {
		t.Fatalf("response omitted durable resolution identity: %s", allowedRecorder.Body.String())
	}
}

func TestDeliveryCreateRouteIdempotencyRunsAfterAuthorization(t *testing.T) {
	projectID := uuid.New()
	store := &deliverySourceRouteStore{projectID: projectID, sourceID: uuid.New()}
	router, token := newDeliveryRouteTestRouter(t, deliveryProjectBinding(projectID, rbac.ResourceDeliverySources, rbac.VerbCreate), store, nil)
	body := validDeliverySourceRouteBody(projectID, false)
	for attempt := 0; attempt < 2; attempt++ {
		request := authenticatedDeliveryRequest(http.MethodPost, "/api/v1/delivery/sources/", strings.NewReader(body), token)
		request.Header.Set("Idempotency-Key", "delivery-source-create-1")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("attempt=%d status=%d body=%s", attempt, recorder.Code, recorder.Body.String())
		}
		if attempt == 1 && recorder.Header().Get("Idempotent-Replayed") != "true" {
			t.Fatalf("second request was not replayed; headers=%v", recorder.Header())
		}
	}
	if store.createCalls != 1 {
		t.Fatalf("create calls=%d, want 1", store.createCalls)
	}
}

func newDeliveryRouteTestRouter(t *testing.T, bindings []rbac.RoleBinding, sources *deliverySourceRouteStore, bundles *deliveryBundleRouteStore) (http.Handler, string) {
	t.Helper()
	jwtManager := auth.MustNewJWTManager("delivery-route-test-secret", 60)
	token, err := jwtManager.GenerateAccessToken(uuid.New())
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}
	deps := RouterDependencies{
		JWT: jwtManager, RBACEngine: rbac.NewEngine(), RBACQueries: deliveryRouteRBACQuerier{bindings: bindings},
	}
	if sources != nil {
		deps.DeliverySources = deliveryhandler.NewSourceHandler(sources, nil, 1)
	}
	if bundles != nil {
		deps.DeliveryBundles = deliveryhandler.NewBundleHandler(bundles)
	}
	return NewRouter(&config.Config{}, deps), token
}

func deliveryProjectBinding(projectID uuid.UUID, resource rbac.Resource, verbs ...rbac.Verb) []rbac.RoleBinding {
	values := make([]string, 0, len(verbs))
	for _, verb := range verbs {
		values = append(values, string(verb))
	}
	return []rbac.RoleBinding{{
		ProjectID: projectID.String(), Scope: "project",
		RoleRules: []rbac.Rule{{Resource: string(resource), Verbs: values}},
	}}
}

func authenticatedDeliveryRequest(method, target string, body *strings.Reader, token string) *http.Request {
	var reader io.Reader
	if body != nil {
		reader = body
	}
	request := httptest.NewRequest(method, target, reader)
	request.Header.Set("Authorization", "Bearer "+token)
	return request
}

func validDeliverySourceRouteBody(projectID uuid.UUID, unknown bool) string {
	project := ""
	if projectID != uuid.Nil {
		project = `"project_id":"` + projectID.String() + `",`
	}
	extra := ""
	if unknown {
		extra = `,"unknown":true`
	}
	return `{` + project + `"name":"platform-source","type":"git","url":"https://git.example.test/platform/config.git","auth_mode":"none","trust_policy":{"allow_unsigned":true}` + extra + `}`
}
