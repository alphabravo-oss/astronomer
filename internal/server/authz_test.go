package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
)

func newTestAuthorizer(t *testing.T, table map[string]Permission, bindings []rbac.RoleBinding) (*chi.Mux, *routeAuthorizer, *bytes.Buffer) {
	t.Helper()
	root := chi.NewRouter()
	deps := RouterDependencies{CoreAuth: CoreAuthDependencies{RBACEngine: rbac.NewEngine(), RBACQueries: routeSecurityRBACQuerier{bindings: bindings}}}
	a := newRouteAuthorizer(root, deps, table)
	logs := &bytes.Buffer{}
	a.logger = slog.New(slog.NewJSONHandler(logs, nil))
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	root.With(a.authorize).Get("/x/{cluster_id}/", ok)
	root.With(a.authorize).Get("/unlisted/", ok)
	return root, a, logs
}

func authedRequest(path, user string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	return req.WithContext(reqctx.WithUser(req.Context(), &reqctx.User{ID: user, AuthMethod: "jwt"}))
}

func TestRouteAuthorizerDenialLogsSubjectRouteRequirementAndRoles(t *testing.T) {
	t.Parallel()
	table := map[string]Permission{"GET /x/{cluster_id}/": {Resource: rbac.ResourceClusters, Verb: rbac.VerbUpdate}}
	viewer := []rbac.RoleBinding{{RoleName: "cluster-viewer", Scope: "global", RoleRules: []rbac.Rule{{Resource: "clusters", Verbs: []string{"read"}}}}}
	root, _, logs := newTestAuthorizer(t, table, viewer)
	user := uuid.NewString()

	rec := httptest.NewRecorder()
	root.ServeHTTP(rec, authedRequest("/x/"+uuid.NewString()+"/", user))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	var line map[string]any
	if err := json.Unmarshal(logs.Bytes(), &line); err != nil {
		t.Fatalf("denial log line is not one JSON record: %v: %s", err, logs.String())
	}
	if line["msg"] != "authorization denied" || line["sub"] != user || line["route"] != "GET /x/{cluster_id}/" || line["required"] != "clusters:update" {
		t.Fatalf("denial log line = %v", line)
	}
	if roles, _ := line["roles"].([]any); len(roles) != 1 || roles[0] != "global:cluster-viewer" {
		t.Fatalf("denial log roles = %v", line["roles"])
	}
}

func TestRouteAuthorizerAdmitsAndDeniesByDefault(t *testing.T) {
	t.Parallel()
	table := map[string]Permission{"GET /x/{cluster_id}/": {Resource: rbac.ResourceClusters, Verb: rbac.VerbRead}}
	reader := []rbac.RoleBinding{{RoleName: "reader", RoleRules: []rbac.Rule{{Resource: "clusters", Verbs: []string{"read"}}}}}
	root, _, logs := newTestAuthorizer(t, table, reader)

	rec := httptest.NewRecorder()
	root.ServeHTTP(rec, authedRequest("/x/"+uuid.NewString()+"/", uuid.NewString()))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("listed + granted route: status = %d, want 204", rec.Code)
	}
	rec = httptest.NewRecorder()
	root.ServeHTTP(rec, authedRequest("/unlisted/", uuid.NewString()))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("route without a table row: status = %d, want 403", rec.Code)
	}
	var line map[string]any
	if err := json.Unmarshal(logs.Bytes(), &line); err != nil || line["required"] != "unlisted" {
		t.Fatalf("unlisted denial log = %s", logs.String())
	}
}
