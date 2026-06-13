package handler

// Handler tests for the RBAC role-templates catalog endpoints (T1.1).
//
// We don't drive these through chi here — the routing wiring is
// pinned in internal/server/routes.go and the integration tests under
// internal/server/. What we pin at the handler level: a missing
// catalog yields 503 (so a misconfigured deploy is loud), the list
// endpoint returns the shipped templates, and Get by-name produces
// a 404 on an unknown slug.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

func TestListTemplates_NoCatalog503(t *testing.T) {
	h := &RBACHandler{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/rbac/templates/", nil)
	h.ListTemplates(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

func TestListTemplates_ReturnsExpandedCatalogInStableOrder(t *testing.T) {
	cat, err := rbac.LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	h := &RBACHandler{}
	h.SetTemplateCatalog(cat)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/rbac/templates/", nil)
	h.ListTemplates(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
	var envelope struct {
		Data struct {
			Templates []rbac.Template `json:"templates"`
			Count     int             `json:"count"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	body := envelope.Data
	if body.Count != cat.Count() || len(body.Templates) != cat.Count() {
		t.Fatalf("count=%d len=%d, want %d/%d", body.Count, len(body.Templates), cat.Count(), cat.Count())
	}
	// Pin the order so frontend snapshot tests don't churn.
	want := []string{
		"audit-viewer",
		"backup-operator",
		"catalog-admin",
		"compliance-auditor",
		"compliance-manager",
		"gitops-admin",
		"gitops-viewer",
		"logging-viewer",
		"monitoring-admin",
		"monitoring-viewer",
		"platform-admin",
		"platform-operator",
		"restore-operator",
		"security-auditor",
		"support-bundle-operator",
		"support-engineer",
		"catalog-installer",
		"cluster-backup-operator",
		"cluster-member",
		"cluster-operator",
		"cluster-owner",
		"cluster-viewer",
		"node-operator",
		"service-mesh-operator",
		"storage-manager",
		"config-manager",
		"gitops-deployer",
		"namespace-operator",
		"project-member",
		"project-owner",
		"project-viewer",
		"secret-manager",
		"service-ingress-manager",
		"workload-deployer",
		"workload-viewer",
	}
	if len(want) != cat.Count() {
		t.Fatalf("test expected order len = %d, catalog count = %d", len(want), cat.Count())
	}
	for i, n := range want {
		if body.Templates[i].Name != n {
			t.Errorf("templates[%d] = %q, want %q", i, body.Templates[i].Name, n)
		}
	}
	if body.Templates[0].RiskLevel == "" || !body.Templates[0].SystemManaged {
		t.Fatalf("template metadata missing in response: %+v", body.Templates[0])
	}
}

func TestGetTemplate_NotFound(t *testing.T) {
	cat, _ := rbac.LoadCatalog()
	h := &RBACHandler{}
	h.SetTemplateCatalog(cat)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", "does-not-exist")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/rbac/templates/does-not-exist/", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.GetTemplate(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestGetTemplate_PlatformAdmin(t *testing.T) {
	cat, _ := rbac.LoadCatalog()
	h := &RBACHandler{}
	h.SetTemplateCatalog(cat)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", "platform-admin")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/rbac/templates/platform-admin/", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.GetTemplate(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "platform-admin") {
		t.Errorf("body missing template name; got %s", rr.Body.String())
	}
}
