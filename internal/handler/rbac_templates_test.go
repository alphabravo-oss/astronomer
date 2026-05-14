package handler

// Handler tests for the RBAC role-templates catalog endpoints (T1.1).
//
// We don't drive these through chi here — the routing wiring is
// pinned in internal/server/routes.go and the integration tests under
// internal/server/. What we pin at the handler level: a missing
// catalog yields 503 (so a misconfigured deploy is loud), the list
// endpoint returns the 8 shipped templates, and Get by-name produces
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

func TestListTemplates_ReturnsEightInStableOrder(t *testing.T) {
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
	if body.Count != 8 || len(body.Templates) != 8 {
		t.Fatalf("count=%d len=%d, want 8/8", body.Count, len(body.Templates))
	}
	// Pin the order so frontend snapshot tests don't churn.
	want := []string{
		"compliance-auditor",
		"platform-admin",
		"support-engineer",
		"cluster-operator",
		"cluster-viewer",
		"project-member",
		"project-owner",
		"project-viewer",
	}
	for i, n := range want {
		if body.Templates[i].Name != n {
			t.Errorf("templates[%d] = %q, want %q", i, body.Templates[i].Name, n)
		}
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
