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
