package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

type bundleQueryFake struct {
	countFn            func(context.Context, uuid.UUID) (int64, error)
	listFn             func(context.Context, sqlc.ListComponentBundlesParams) ([]sqlc.ComponentBundle, error)
	createFn           func(context.Context, sqlc.CreateComponentBundleParams) (sqlc.ComponentBundle, error)
	getFn              func(context.Context, sqlc.GetComponentBundleParams) (sqlc.ComponentBundle, error)
	updateFn           func(context.Context, sqlc.UpdateComponentBundleParams) (sqlc.ComponentBundle, error)
	deleteFn           func(context.Context, sqlc.DeleteComponentBundleParams) (int64, error)
	createVersionFn    func(context.Context, sqlc.CreateComponentBundleVersionParams) (sqlc.ComponentBundleVersion, error)
	listVersionsFn     func(context.Context, sqlc.ListComponentBundleVersionsParams) ([]sqlc.ComponentBundleVersion, error)
	getVersionFn       func(context.Context, sqlc.GetComponentBundleVersionParams) (sqlc.ComponentBundleVersion, error)
	getSourceFn        func(context.Context, sqlc.GetDeliverySourceParams) (sqlc.GetDeliverySourceRow, error)
	createResolutionFn func(context.Context, sqlc.CreateDeliverySourceResolutionAndOutboxParams) (sqlc.CreateDeliverySourceResolutionAndOutboxRow, error)
	failVersionFn      func(context.Context, sqlc.FailComponentBundleVersionParams) (sqlc.ComponentBundleVersion, error)
}

func (f *bundleQueryFake) CountComponentBundles(ctx context.Context, id uuid.UUID) (int64, error) {
	if f.countFn == nil {
		panic("unexpected CountComponentBundles")
	}
	return f.countFn(ctx, id)
}

func (f *bundleQueryFake) ListComponentBundles(ctx context.Context, arg sqlc.ListComponentBundlesParams) ([]sqlc.ComponentBundle, error) {
	if f.listFn == nil {
		panic("unexpected ListComponentBundles")
	}
	return f.listFn(ctx, arg)
}

func (f *bundleQueryFake) CreateComponentBundle(ctx context.Context, arg sqlc.CreateComponentBundleParams) (sqlc.ComponentBundle, error) {
	if f.createFn == nil {
		panic("unexpected CreateComponentBundle")
	}
	return f.createFn(ctx, arg)
}

func (f *bundleQueryFake) GetComponentBundle(ctx context.Context, arg sqlc.GetComponentBundleParams) (sqlc.ComponentBundle, error) {
	if f.getFn == nil {
		panic("unexpected GetComponentBundle")
	}
	return f.getFn(ctx, arg)
}

func (f *bundleQueryFake) UpdateComponentBundle(ctx context.Context, arg sqlc.UpdateComponentBundleParams) (sqlc.ComponentBundle, error) {
	if f.updateFn == nil {
		panic("unexpected UpdateComponentBundle")
	}
	return f.updateFn(ctx, arg)
}

func (f *bundleQueryFake) DeleteComponentBundle(ctx context.Context, arg sqlc.DeleteComponentBundleParams) (int64, error) {
	if f.deleteFn == nil {
		panic("unexpected DeleteComponentBundle")
	}
	return f.deleteFn(ctx, arg)
}

func (f *bundleQueryFake) CreateComponentBundleVersion(ctx context.Context, arg sqlc.CreateComponentBundleVersionParams) (sqlc.ComponentBundleVersion, error) {
	if f.createVersionFn == nil {
		panic("unexpected CreateComponentBundleVersion")
	}
	return f.createVersionFn(ctx, arg)
}

func (f *bundleQueryFake) ListComponentBundleVersions(ctx context.Context, arg sqlc.ListComponentBundleVersionsParams) ([]sqlc.ComponentBundleVersion, error) {
	if f.listVersionsFn == nil {
		panic("unexpected ListComponentBundleVersions")
	}
	return f.listVersionsFn(ctx, arg)
}

func (f *bundleQueryFake) GetComponentBundleVersion(ctx context.Context, arg sqlc.GetComponentBundleVersionParams) (sqlc.ComponentBundleVersion, error) {
	if f.getVersionFn == nil {
		panic("unexpected GetComponentBundleVersion")
	}
	return f.getVersionFn(ctx, arg)
}

func (f *bundleQueryFake) GetDeliverySource(ctx context.Context, arg sqlc.GetDeliverySourceParams) (sqlc.GetDeliverySourceRow, error) {
	if f.getSourceFn == nil {
		panic("unexpected GetDeliverySource")
	}
	return f.getSourceFn(ctx, arg)
}

func (f *bundleQueryFake) CreateDeliverySourceResolutionAndOutbox(ctx context.Context, arg sqlc.CreateDeliverySourceResolutionAndOutboxParams) (sqlc.CreateDeliverySourceResolutionAndOutboxRow, error) {
	if f.createResolutionFn == nil {
		panic("unexpected CreateDeliverySourceResolutionAndOutbox")
	}
	return f.createResolutionFn(ctx, arg)
}

func (f *bundleQueryFake) FailComponentBundleVersion(ctx context.Context, arg sqlc.FailComponentBundleVersionParams) (sqlc.ComponentBundleVersion, error) {
	if f.failVersionFn == nil {
		panic("unexpected FailComponentBundleVersion")
	}
	return f.failVersionFn(ctx, arg)
}

func TestBundleDeleteIsProjectScopedAndBlocksMissingRows(t *testing.T) {
	projectID, bundleID := uuid.New(), uuid.New()
	var deleted sqlc.DeleteComponentBundleParams
	fake := &bundleQueryFake{
		deleteFn: func(_ context.Context, arg sqlc.DeleteComponentBundleParams) (int64, error) {
			deleted = arg
			if arg.ID != bundleID || arg.ProjectID != projectID {
				return 0, nil
			}
			return 1, nil
		},
	}
	request := requestWithPathParams(http.MethodDelete, "/api/v1/delivery/bundles/"+bundleID.String()+"?project_id="+projectID.String(), nil, map[string]string{"id": bundleID.String()})
	request.Header.Set("Idempotency-Key", "bundle-delete-1")
	recorder := httptest.NewRecorder()
	NewBundleHandler(fake).Delete(recorder, request)
	if recorder.Code != http.StatusNoContent || deleted.ID != bundleID || deleted.ProjectID != projectID {
		t.Fatalf("status=%d deleted=%#v body=%s", recorder.Code, deleted, recorder.Body.String())
	}

	fake.deleteFn = func(context.Context, sqlc.DeleteComponentBundleParams) (int64, error) { return 0, nil }
	missing := requestWithPathParams(http.MethodDelete, "/api/v1/delivery/bundles/"+uuid.New().String()+"?project_id="+projectID.String(), nil, map[string]string{"id": uuid.New().String()})
	missing.Header.Set("Idempotency-Key", "bundle-delete-missing")
	missingRecorder := httptest.NewRecorder()
	NewBundleHandler(fake).Delete(missingRecorder, missing)
	if missingRecorder.Code != http.StatusNotFound {
		t.Fatalf("missing delete status=%d body=%s", missingRecorder.Code, missingRecorder.Body.String())
	}
}

func TestBundleCreateAndListAreProjectScopedAndPaginated(t *testing.T) {
	projectID := uuid.New()
	bundleID := uuid.New()
	now := time.Now().UTC()
	fake := &bundleQueryFake{
		createFn: func(_ context.Context, arg sqlc.CreateComponentBundleParams) (sqlc.ComponentBundle, error) {
			if arg.ProjectID != projectID {
				t.Fatalf("create escaped project scope: %#v", arg)
			}
			return sqlc.ComponentBundle{ID: bundleID, ProjectID: projectID, Name: arg.Name, Description: arg.Description, CreatedAt: now, UpdatedAt: now}, nil
		},
	}
	body := `{"name":"platform-ingress","description":"Pinned ingress component"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/delivery/bundles?project_id="+projectID.String(), strings.NewReader(body))
	recorder := httptest.NewRecorder()
	NewBundleHandler(fake).Create(recorder, request)
	if recorder.Code != http.StatusCreated || !strings.Contains(recorder.Body.String(), bundleID.String()) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	fake = &bundleQueryFake{
		listFn: func(_ context.Context, arg sqlc.ListComponentBundlesParams) ([]sqlc.ComponentBundle, error) {
			if arg.ProjectID != projectID || arg.QueryLimit != 5 || arg.QueryOffset != 2 {
				t.Fatalf("unexpected list scope: %#v", arg)
			}
			return []sqlc.ComponentBundle{{ID: bundleID, ProjectID: projectID, Name: "platform-ingress", CreatedAt: now, UpdatedAt: now}}, nil
		},
		countFn: func(_ context.Context, id uuid.UUID) (int64, error) {
			if id != projectID {
				t.Fatalf("unscoped count: %s", id)
			}
			return 9, nil
		},
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/delivery/bundles?project_id="+projectID.String()+"&limit=5&offset=2", nil)
	recorder = httptest.NewRecorder()
	NewBundleHandler(fake).List(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"total_known":true`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestBundleCreateRejectsUnknownFieldsAndScopeMismatch(t *testing.T) {
	projectA := uuid.New()
	projectB := uuid.New()
	tests := []struct {
		name   string
		target string
		body   string
	}{
		{"unknown field", "/api/v1/delivery/bundles?project_id=" + projectA.String(), `{"name":"bundle","unknown":true}`},
		{"scope mismatch", "/api/v1/delivery/bundles?project_id=" + projectA.String(), `{"project_id":"` + projectB.String() + `","name":"bundle"}`},
		{"invalid name", "/api/v1/delivery/bundles?project_id=" + projectA.String(), `{"name":" bundle "}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.target, strings.NewReader(test.body))
			recorder := httptest.NewRecorder()
			NewBundleHandler(&bundleQueryFake{}).Create(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestCreateBundleVersionPersistsImmutableCredentialFreeSnapshot(t *testing.T) {
	projectID := uuid.New()
	bundleID := uuid.New()
	dependencyA := uuid.New()
	dependencyB := uuid.New()
	sourceID := uuid.New()
	versionID := uuid.New()
	now := time.Now().UTC()
	requestValue := validCreateBundleVersionRequest(projectID, sourceID)
	requestValue.DependencyBundleID = []uuid.UUID{dependencyB, dependencyA}
	body, err := json.Marshal(requestValue)
	if err != nil {
		t.Fatal(err)
	}
	var persisted sqlc.CreateComponentBundleVersionParams
	var resolution sqlc.CreateDeliverySourceResolutionAndOutboxParams
	fake := &bundleQueryFake{
		getFn: func(_ context.Context, arg sqlc.GetComponentBundleParams) (sqlc.ComponentBundle, error) {
			switch arg.ID {
			case bundleID, dependencyA, dependencyB:
				if arg.ProjectID != projectID {
					t.Fatalf("bundle lookup escaped project: %#v", arg)
				}
				return sqlc.ComponentBundle{ID: arg.ID, ProjectID: projectID, Name: "bundle", CreatedAt: now, UpdatedAt: now}, nil
			default:
				return sqlc.ComponentBundle{}, pgx.ErrNoRows
			}
		},
		getSourceFn: func(_ context.Context, arg sqlc.GetDeliverySourceParams) (sqlc.GetDeliverySourceRow, error) {
			if arg.ID != sourceID || arg.ProjectID != projectID {
				t.Fatalf("source lookup escaped project: %#v", arg)
			}
			return validPublicSourceRow(projectID, sourceID), nil
		},
		createVersionFn: func(_ context.Context, arg sqlc.CreateComponentBundleVersionParams) (sqlc.ComponentBundleVersion, error) {
			persisted = arg
			return componentVersionRow(versionID, arg, now), nil
		},
		createResolutionFn: func(_ context.Context, arg sqlc.CreateDeliverySourceResolutionAndOutboxParams) (sqlc.CreateDeliverySourceResolutionAndOutboxRow, error) {
			resolution = arg
			return sqlc.CreateDeliverySourceResolutionAndOutboxRow{ID: uuid.New(), SourceID: arg.SourceID, BundleVersionID: arg.BundleVersionID, RequestedRevision: arg.RequestedRevision, Status: "pending", CreatedAt: now}, nil
		},
	}
	request := requestWithPathParams(http.MethodPost, "/api/v1/delivery/bundles/"+bundleID.String()+"/versions", strings.NewReader(string(body)), map[string]string{"id": bundleID.String()})
	recorder := httptest.NewRecorder()
	NewBundleHandler(fake).CreateVersion(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if persisted.BundleID != bundleID || persisted.SourceID != sourceID || persisted.SpecDigest == "" {
		t.Fatalf("unexpected persisted version: %#v", persisted)
	}
	if !resolution.BundleVersionID.Valid || resolution.BundleVersionID.Bytes != versionID || resolution.SourceID != sourceID {
		t.Fatalf("resolution was not bound to version: %#v", resolution)
	}
	var dependencies []uuid.UUID
	if err := json.Unmarshal(persisted.DependencyBundleIds, &dependencies); err != nil {
		t.Fatal(err)
	}
	if len(dependencies) != 2 || dependencies[0].String() > dependencies[1].String() {
		t.Fatalf("dependencies were not canonicalized: %v", dependencies)
	}
	var sourceSpec map[string]any
	if err := json.Unmarshal(persisted.SourceSpec, &sourceSpec); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"credential", "password", "token", "private_key", "credential_encrypted", "ca_bundle"} {
		if strings.Contains(string(persisted.SourceSpec), forbidden) || strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("version snapshot/response exposed %q: source=%s response=%s", forbidden, persisted.SourceSpec, recorder.Body.String())
		}
	}
}

func TestCreateBundleVersionRejectsCrossProjectSourceBeforeWrite(t *testing.T) {
	projectID := uuid.New()
	bundleID := uuid.New()
	sourceID := uuid.New()
	now := time.Now().UTC()
	requestValue := validCreateBundleVersionRequest(projectID, sourceID)
	body, _ := json.Marshal(requestValue)
	createCalled := false
	fake := &bundleQueryFake{
		getFn: func(context.Context, sqlc.GetComponentBundleParams) (sqlc.ComponentBundle, error) {
			return sqlc.ComponentBundle{ID: bundleID, ProjectID: projectID, Name: "bundle", CreatedAt: now, UpdatedAt: now}, nil
		},
		getSourceFn: func(_ context.Context, arg sqlc.GetDeliverySourceParams) (sqlc.GetDeliverySourceRow, error) {
			if arg.ProjectID != projectID || arg.ID != sourceID {
				t.Fatalf("source query was not scoped: %#v", arg)
			}
			return sqlc.GetDeliverySourceRow{}, pgx.ErrNoRows
		},
		createVersionFn: func(context.Context, sqlc.CreateComponentBundleVersionParams) (sqlc.ComponentBundleVersion, error) {
			createCalled = true
			return sqlc.ComponentBundleVersion{}, nil
		},
	}
	request := requestWithPathParams(http.MethodPost, "/api/v1/delivery/bundles/"+bundleID.String()+"/versions", strings.NewReader(string(body)), map[string]string{"id": bundleID.String()})
	recorder := httptest.NewRecorder()
	NewBundleHandler(fake).CreateVersion(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if createCalled {
		t.Fatal("bundle version was written for a cross-project source")
	}
}

func TestCreateBundleVersionValidationAndUnknownFields(t *testing.T) {
	projectID := uuid.New()
	bundleID := uuid.New()
	sourceID := uuid.New()
	valid := validCreateBundleVersionRequest(projectID, sourceID)
	invalidSpec := valid
	invalidSpec.Spec.Renderer.Kustomize.Path = "../../escape"
	invalidJSON, _ := json.Marshal(invalidSpec)
	validJSON, _ := json.Marshal(valid)
	unknownJSON := strings.TrimSuffix(string(validJSON), "}") + `,"raw_manifest":"secret"}`
	for _, body := range []string{string(invalidJSON), unknownJSON} {
		request := requestWithPathParams(http.MethodPost, "/api/v1/delivery/bundles/"+bundleID.String()+"/versions", strings.NewReader(body), map[string]string{"id": bundleID.String()})
		recorder := httptest.NewRecorder()
		NewBundleHandler(&bundleQueryFake{}).CreateVersion(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
}

func TestGetBundleVersionRejectsNestedBundleConfusion(t *testing.T) {
	projectID := uuid.New()
	requestedBundleID := uuid.New()
	actualBundleID := uuid.New()
	versionID := uuid.New()
	fake := &bundleQueryFake{getVersionFn: func(_ context.Context, arg sqlc.GetComponentBundleVersionParams) (sqlc.ComponentBundleVersion, error) {
		if arg.ProjectID != projectID || arg.ID != versionID {
			t.Fatalf("version query was not project scoped: %#v", arg)
		}
		return sqlc.ComponentBundleVersion{ID: versionID, BundleID: actualBundleID}, nil
	}}
	target := "/api/v1/delivery/bundles/" + requestedBundleID.String() + "/versions/" + versionID.String() + "?project_id=" + projectID.String()
	request := requestWithPathParams(http.MethodGet, target, nil, map[string]string{"id": requestedBundleID.String(), "versionId": versionID.String()})
	recorder := httptest.NewRecorder()
	NewBundleHandler(fake).GetVersion(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestBundleVersionResolutionFailureIsCompensated(t *testing.T) {
	projectID := uuid.New()
	bundleID := uuid.New()
	sourceID := uuid.New()
	versionID := uuid.New()
	now := time.Now().UTC()
	requestValue := validCreateBundleVersionRequest(projectID, sourceID)
	body, _ := json.Marshal(requestValue)
	compensated := false
	fake := &bundleQueryFake{
		getFn: func(context.Context, sqlc.GetComponentBundleParams) (sqlc.ComponentBundle, error) {
			return sqlc.ComponentBundle{ID: bundleID, ProjectID: projectID, Name: "bundle", CreatedAt: now, UpdatedAt: now}, nil
		},
		getSourceFn: func(context.Context, sqlc.GetDeliverySourceParams) (sqlc.GetDeliverySourceRow, error) {
			return validPublicSourceRow(projectID, sourceID), nil
		},
		createVersionFn: func(_ context.Context, arg sqlc.CreateComponentBundleVersionParams) (sqlc.ComponentBundleVersion, error) {
			return componentVersionRow(versionID, arg, now), nil
		},
		createResolutionFn: func(context.Context, sqlc.CreateDeliverySourceResolutionAndOutboxParams) (sqlc.CreateDeliverySourceResolutionAndOutboxRow, error) {
			return sqlc.CreateDeliverySourceResolutionAndOutboxRow{}, errors.New("queue unavailable")
		},
		failVersionFn: func(_ context.Context, arg sqlc.FailComponentBundleVersionParams) (sqlc.ComponentBundleVersion, error) {
			compensated = arg.ID == versionID && arg.LastErrorCode == "resolution_enqueue_failed"
			return sqlc.ComponentBundleVersion{}, nil
		},
	}
	request := requestWithPathParams(http.MethodPost, "/api/v1/delivery/bundles/"+bundleID.String()+"/versions", strings.NewReader(string(body)), map[string]string{"id": bundleID.String()})
	recorder := httptest.NewRecorder()
	NewBundleHandler(fake).CreateVersion(recorder, request)
	if recorder.Code != http.StatusInternalServerError || !compensated {
		t.Fatalf("status=%d compensated=%v body=%s", recorder.Code, compensated, recorder.Body.String())
	}
}

func TestListBundleVersionsPreflightsProjectAndBoundsPage(t *testing.T) {
	projectID := uuid.New()
	bundleID := uuid.New()
	now := time.Now().UTC()
	preflighted := false
	fake := &bundleQueryFake{
		getFn: func(_ context.Context, arg sqlc.GetComponentBundleParams) (sqlc.ComponentBundle, error) {
			preflighted = arg.ID == bundleID && arg.ProjectID == projectID
			return sqlc.ComponentBundle{ID: bundleID, ProjectID: projectID, Name: "bundle", CreatedAt: now, UpdatedAt: now}, nil
		},
		listVersionsFn: func(_ context.Context, arg sqlc.ListComponentBundleVersionsParams) ([]sqlc.ComponentBundleVersion, error) {
			if !preflighted || arg.BundleID != bundleID || arg.QueryLimit != 3 {
				t.Fatalf("unbounded or unscoped version list: %#v", arg)
			}
			return nil, nil
		},
	}
	target := "/api/v1/delivery/bundles/" + bundleID.String() + "/versions?project_id=" + projectID.String() + "&limit=2"
	request := requestWithPathParams(http.MethodGet, target, nil, map[string]string{"id": bundleID.String()})
	recorder := httptest.NewRecorder()
	NewBundleHandler(fake).ListVersions(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"total_known":false`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func validCreateBundleVersionRequest(projectID, sourceID uuid.UUID) createBundleVersionRequest {
	return createBundleVersionRequest{
		ProjectID: projectID,
		Version:   "1.2.3",
		Spec: model.BundleVersionDraft{
			SourceID:          sourceID,
			RequestedRevision: "main",
			Renderer:          model.RendererSpec{Kind: model.RendererKustomize, Kustomize: &model.KustomizeSpec{Path: "./clusters/base", TargetNamespace: "apps"}},
			Scope:             model.ScopeNamespace,
			Reconciliation: model.ReconciliationPolicy{
				Interval: model.Duration(10 * time.Minute), RetryInterval: model.Duration(time.Minute),
				Timeout: model.Duration(5 * time.Minute), Prune: true, Wait: true, Drift: model.DriftRepair,
			},
			RequiredCapabilities: []model.CapabilityRequirement{{Name: "delivery.astronomer.io/kustomize"}},
		},
	}
}

func validPublicSourceRow(projectID, sourceID uuid.UUID) sqlc.GetDeliverySourceRow {
	now := time.Now().UTC()
	return sqlc.GetDeliverySourceRow{
		ID: sourceID, ProjectID: projectID, Name: "source", SourceType: "git",
		Url: "https://git.example.test/platform/config.git", AuthMode: "none",
		TrustPolicy: json.RawMessage(`{"allow_unsigned":true}`), Status: "ready", CreatedAt: now, UpdatedAt: now,
	}
}

func componentVersionRow(id uuid.UUID, arg sqlc.CreateComponentBundleVersionParams, now time.Time) sqlc.ComponentBundleVersion {
	return sqlc.ComponentBundleVersion{
		ID: id, BundleID: arg.BundleID, SourceID: arg.SourceID, Version: arg.Version,
		Renderer: arg.Renderer, Scope: arg.Scope, RequestedRevision: arg.RequestedRevision,
		SourceSpec: arg.SourceSpec, RendererSpec: arg.RendererSpec, ReconciliationPolicy: arg.ReconciliationPolicy,
		HealthPolicy: arg.HealthPolicy, Requirements: arg.Requirements, DependencyBundleIds: arg.DependencyBundleIds,
		SpecDigest: arg.SpecDigest, VerificationStatus: "pending", State: "resolving", CreatedAt: now,
	}
}
