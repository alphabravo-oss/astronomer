package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// authedLoggingReq builds a request whose context carries an authenticated
// principal, so the handler's authorizeClusterAction path treats the caller as
// RBAC-restricted (a real session) rather than an unconfigured/no-auth request.
func authedLoggingReq(method, target string, body []byte) *http.Request {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, target, bytes.NewReader(body))
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	ctx := middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: uuid.NewString()})
	return req.WithContext(ctx)
}

// TestLoggingMutatingRoutesDenyZeroGrantViewer proves a zero-grant viewer
// session is refused with 403 on the logging mutation surface. Before the fix
// these handlers never called authorizeClusterAction, so any authenticated
// principal could create/enable logging outputs and pipelines.
func TestLoggingMutatingRoutesDenyZeroGrantViewer(t *testing.T) {
	clusterID := uuid.New()

	newHandler := func() *LoggingHandler {
		h := NewLoggingHandler(newLoggingFakeQuerier())
		// Zero-grant viewer: authorization configured, but no logging bindings.
		h.SetAuthorization(rbac.NewEngine(), stubLoggingRBACQuerier{bindings: nil})
		return h
	}

	t.Run("create_output", func(t *testing.T) {
		h := newHandler()
		body, _ := json.Marshal(map[string]any{
			"name":          "viewer-output",
			"output_type":   "stdout",
			"configuration": map[string]any{},
			"cluster_id":    clusterID.String(),
			"enabled":       true,
		})
		rec := httptest.NewRecorder()
		h.CreateOutput(rec, authedLoggingReq(http.MethodPost, "/api/v1/logging/outputs/", body))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("CreateOutput status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})

	t.Run("create_pipeline", func(t *testing.T) {
		h := newHandler()
		body, _ := json.Marshal(map[string]any{
			"name":       "viewer-pipeline",
			"cluster_id": clusterID.String(),
			"enabled":    true,
		})
		target := "/api/v1/logging/pipelines/?cluster_id=" + clusterID.String()
		rec := httptest.NewRecorder()
		h.CreatePipeline(rec, authedLoggingReq(http.MethodPost, target, body))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("CreatePipeline status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})

	t.Run("test_output", func(t *testing.T) {
		// Seed an output so TestOutput reaches the authz check (not a 404).
		q := newLoggingFakeQuerier()
		out, err := q.CreateLoggingOutput(nil, sqlc.CreateLoggingOutputParams{
			Name:          "viewer-test",
			OutputType:    "stdout",
			Configuration: []byte(`{}`),
			ClusterID:     pgtype.UUID{Bytes: clusterID, Valid: true},
			Enabled:       true,
		})
		if err != nil {
			t.Fatalf("seed output: %v", err)
		}
		h := NewLoggingHandler(q)
		h.SetAuthorization(rbac.NewEngine(), stubLoggingRBACQuerier{bindings: nil})
		req := authedLoggingReq(http.MethodPost, "/api/v1/logging/outputs/"+out.ID.String()+"/test/", nil)
		rc := chi.NewRouteContext()
		rc.URLParams.Add("id", out.ID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
		rec := httptest.NewRecorder()
		h.TestOutput(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("TestOutput status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})
}

// TestLoggingMutatingRoutesAllowGrantedPrincipal is the positive control: a
// caller holding logging:create for the target cluster is NOT blocked by the
// new authorization gate (it proceeds into the handler body).
func TestLoggingMutatingRoutesAllowGrantedPrincipal(t *testing.T) {
	clusterID := uuid.New()
	h := NewLoggingHandler(newLoggingFakeQuerier())
	h.SetAuthorization(rbac.NewEngine(), stubLoggingRBACQuerier{bindings: []rbac.RoleBinding{{
		ClusterID: clusterID.String(),
		RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceLogging), Verbs: []string{string(rbac.VerbCreate)}}},
	}}})
	body, _ := json.Marshal(map[string]any{
		"name":          "granted-output",
		"output_type":   "stdout",
		"configuration": map[string]any{},
		"cluster_id":    clusterID.String(),
		"enabled":       true,
	})
	rec := httptest.NewRecorder()
	h.CreateOutput(rec, authedLoggingReq(http.MethodPost, "/api/v1/logging/outputs/", body))
	if rec.Code == http.StatusForbidden {
		t.Fatalf("granted principal was denied: status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("CreateOutput status = %d, want %d; body=%s", rec.Code, http.StatusCreated, rec.Body.String())
	}
}
