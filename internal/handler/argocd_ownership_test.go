package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestArgoCDClusterOwnershipShowsMigrationRequiredUntilRegistered(t *testing.T) {
	clusterID := uuid.New()
	h := NewArgoCDHandler(&argocdManagedClusterQueryStub{
		cluster: sqlc.Cluster{ID: clusterID, Name: "prod"},
	})

	rr := httptest.NewRecorder()
	h.ClusterOwnership(rr, argoOwnershipReq(http.MethodGet, clusterID, "", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var envelope struct {
		Data argoCDClusterOwnershipResponse `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := envelope.Data
	if got.Registered {
		t.Fatalf("registered = true, want false")
	}
	if len(got.Components) == 0 || got.Components[0].State != "migration_required" {
		t.Fatalf("components = %+v, want migration_required", got.Components)
	}
}

func TestArgoCDClusterOwnershipDecisionPersistsLeaveLocal(t *testing.T) {
	clusterID := uuid.New()
	q := &argocdManagedClusterQueryStub{
		cluster: sqlc.Cluster{ID: clusterID, Name: "prod"},
		managed: sqlc.ArgocdManagedCluster{
			ID:                uuid.New(),
			ArgocdInstanceID:  uuid.New(),
			ClusterID:         clusterID,
			ClusterSecretName: "cluster-prod",
			ServerUrl:         "https://prod.example",
			Labels:            json.RawMessage(`{"astronomer.io/cluster-name":"prod"}`),
			UpdatedAt:         time.Now(),
		},
	}
	h := NewArgoCDHandler(q)

	body := bytes.NewBufferString(`{"decision":"leave_local","reason":"regulated external owner"}`)
	rr := httptest.NewRecorder()
	h.SetClusterOwnershipDecision(rr, argoOwnershipReq(http.MethodPost, clusterID, "trivy-operator", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if q.decisions["trivy-operator"].Decision != "leave_local" {
		t.Fatalf("decision not persisted: %+v", q.decisions)
	}

	rr = httptest.NewRecorder()
	h.ClusterOwnership(rr, argoOwnershipReq(http.MethodGet, clusterID, "", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("summary status = %d body=%s", rr.Code, rr.Body.String())
	}
	var envelope struct {
		Data argoCDClusterOwnershipResponse `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var trivy argoCDBaselineComponentOwnership
	for _, component := range envelope.Data.Components {
		if component.Slug == "trivy-operator" {
			trivy = component
			break
		}
	}
	if trivy.State != "local_manual" || trivy.Decision == nil || trivy.Decision.Decision != "leave_local" {
		t.Fatalf("trivy component = %+v, want local_manual leave_local", trivy)
	}
}

func TestArgoCDClusterOwnershipLocalClusterBlocksReplaceOption(t *testing.T) {
	clusterID := uuid.New()
	h := NewArgoCDHandler(&argocdManagedClusterQueryStub{
		cluster: sqlc.Cluster{ID: clusterID, Name: "local", IsLocal: true},
	})

	rr := httptest.NewRecorder()
	h.ClusterOwnership(rr, argoOwnershipReq(http.MethodGet, clusterID, "", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var envelope struct {
		Data argoCDClusterOwnershipResponse `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(envelope.Data.Components) == 0 {
		t.Fatal("expected baseline components")
	}
	for _, component := range envelope.Data.Components {
		for _, option := range component.Options {
			if option == "replace" {
				t.Fatalf("local component %s exposed unsafe replace option: %+v", component.Slug, component.Options)
			}
		}
	}
}

func TestArgoCDClusterOwnershipReplaceRequiresReason(t *testing.T) {
	clusterID := uuid.New()
	q := &argocdManagedClusterQueryStub{
		cluster: sqlc.Cluster{ID: clusterID, Name: "prod"},
		managed: sqlc.ArgocdManagedCluster{
			ID:               uuid.New(),
			ArgocdInstanceID: uuid.New(),
			ClusterID:        clusterID,
		},
	}
	h := NewArgoCDHandler(q)

	rr := httptest.NewRecorder()
	h.SetClusterOwnershipDecision(rr, argoOwnershipReq(http.MethodPost, clusterID, "trivy-operator", bytes.NewBufferString(`{"decision":"replace"}`)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if len(q.decisions) != 0 {
		t.Fatalf("decision persisted despite missing reason: %+v", q.decisions)
	}
}

func TestArgoCDClusterOwnershipReplaceRequiresArgoRegistration(t *testing.T) {
	clusterID := uuid.New()
	q := &argocdManagedClusterQueryStub{
		cluster: sqlc.Cluster{ID: clusterID, Name: "prod"},
	}
	h := NewArgoCDHandler(q)

	rr := httptest.NewRecorder()
	h.SetClusterOwnershipDecision(rr, argoOwnershipReq(http.MethodPost, clusterID, "trivy-operator", bytes.NewBufferString(`{"decision":"replace","reason":"move ownership into argocd"}`)))
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if len(q.decisions) != 0 {
		t.Fatalf("decision persisted despite missing Argo registration: %+v", q.decisions)
	}
}

func argoOwnershipReq(method string, clusterID uuid.UUID, component string, body *bytes.Buffer) *http.Request {
	path := "/api/v1/argocd/clusters/" + clusterID.String() + "/ownership/"
	if component != "" {
		path += component + "/decision/"
	}
	var reqBody io.Reader
	if body != nil {
		reqBody = body
	}
	req := httptest.NewRequest(method, path, reqBody)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster_id", clusterID.String())
	if component != "" {
		rctx.URLParams.Add("component_slug", component)
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// The ownership panel must only claim the components Astronomer actually
// auto-provisions. It previously listed the entire baseline catalog with
// DesiredOwner=argocd, so five opt-in add-ons (trivy, fluent-bit, ingress-nginx,
// cert-manager, gatekeeper) rendered as ArgoCD-managed — naming ApplicationSets
// that were never created — on clusters where nothing was installed.
func TestArgoCDClusterOwnershipListsOnlyAutoProvisionedComponents(t *testing.T) {
	clusterID := uuid.New()
	h := NewArgoCDHandler(&argocdManagedClusterQueryStub{
		cluster: sqlc.Cluster{ID: clusterID, Name: "prod"},
	})

	rr := httptest.NewRecorder()
	h.ClusterOwnership(rr, argoOwnershipReq(http.MethodGet, clusterID, "", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var envelope struct {
		Data argoCDClusterOwnershipResponse `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}

	got := make(map[string]bool, len(envelope.Data.Components))
	for _, c := range envelope.Data.Components {
		got[c.Slug] = true
	}
	// Derived from the registry, not hand-listed: whatever is DefaultEnabled
	// must appear, and whatever is not must stay in the Tools add-on view until
	// an operator makes an explicit ownership decision for it.
	for _, item := range platformBaselineComponentCatalog {
		if item.DefaultEnabled && !got[item.Slug] {
			t.Errorf("auto-provisioned %q missing from ownership panel", item.Slug)
		}
		if !item.DefaultEnabled && got[item.Slug] {
			t.Errorf("opt-in add-on %q claimed as auto-provisioned (no decision recorded)", item.Slug)
		}
	}
	if len(envelope.Data.Components) == 0 {
		t.Fatal("no components returned")
	}
}

// ownershipAppStub adds the optional app-target query so leave_local can tell a
// component that is really running from one that was never installed.
type ownershipAppStub struct {
	*argocdManagedClusterQueryStub
	apps []sqlc.ArgocdApplication
}

func (s *ownershipAppStub) ListArgoCDApplicationsByManagedClusterTargets(
	_ context.Context, _ sqlc.ListArgoCDApplicationsByManagedClusterTargetsParams,
) ([]sqlc.ArgocdApplication, error) {
	return s.apps, nil
}

func ownershipStubWithApps(clusterID uuid.UUID, apps []sqlc.ArgocdApplication) *ownershipAppStub {
	return &ownershipAppStub{
		argocdManagedClusterQueryStub: &argocdManagedClusterQueryStub{
			cluster: sqlc.Cluster{ID: clusterID, Name: "prod"},
			managed: sqlc.ArgocdManagedCluster{
				ID:                uuid.New(),
				ArgocdInstanceID:  uuid.New(),
				ClusterID:         clusterID,
				ClusterSecretName: "cluster-prod",
				ServerUrl:         "https://prod.example",
				Labels:            json.RawMessage(`{"astronomer.io/cluster-name":"prod"}`),
				UpdatedAt:         time.Now(),
			},
		},
		apps: apps,
	}
}

func postLeaveLocal(t *testing.T, h *ArgoCDHandler, clusterID uuid.UUID, slug, body string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	h.SetClusterOwnershipDecision(rr, argoOwnershipReq(http.MethodPost, clusterID, slug, bytes.NewBufferString(body)))
	return rr
}

// "Leave local" does not uninstall: it drops the component from the desired
// state and the ApplicationSet. On a component ArgoCD is currently running that
// leaves the workload up with nothing reconciling it, so it must be refused —
// the operator has to uninstall, which takes the workload with it.
func TestArgoCDOwnershipLeaveLocalBlockedWhileComponentIsRunning(t *testing.T) {
	clusterID := uuid.New()
	q := ownershipStubWithApps(clusterID, []sqlc.ArgocdApplication{
		{Name: "astronomer-trivy-prod", DestinationCluster: "https://prod.example"},
	})
	rr := postLeaveLocal(t, NewArgoCDHandler(q), clusterID, "trivy-operator",
		`{"decision":"leave_local","reason":"we run our own"}`)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	if _, ok := q.decisions["trivy-operator"]; ok {
		t.Fatal("decision was persisted despite being refused — the component would be orphaned")
	}
}

// The same opt-out on a component that is NOT running is the legitimate case:
// "don't install yours here, I run my own". It must still work.
func TestArgoCDOwnershipLeaveLocalAllowedWhenComponentNotRunning(t *testing.T) {
	clusterID := uuid.New()
	// An app for a DIFFERENT component must not block this one.
	q := ownershipStubWithApps(clusterID, []sqlc.ArgocdApplication{
		{Name: "astronomer-ksm-prod", DestinationCluster: "https://prod.example"},
	})
	rr := postLeaveLocal(t, NewArgoCDHandler(q), clusterID, "trivy-operator",
		`{"decision":"leave_local","reason":"we run our own"}`)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if q.decisions["trivy-operator"].Decision != "leave_local" {
		t.Fatalf("decision not persisted: %+v", q.decisions)
	}
}

// leave_local is as consequential as replace, which has always required one.
func TestArgoCDOwnershipLeaveLocalRequiresReason(t *testing.T) {
	clusterID := uuid.New()
	q := ownershipStubWithApps(clusterID, nil)
	rr := postLeaveLocal(t, NewArgoCDHandler(q), clusterID, "trivy-operator", `{"decision":"leave_local"}`)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if _, ok := q.decisions["trivy-operator"]; ok {
		t.Fatal("reasonless decision was persisted")
	}
}
