package handler

// RBAC role-templates catalog endpoints (T1.1). Reads the embedded
// templates registry at internal/rbac/templates.go; no DB hits.
//
// Two endpoints:
//
//   GET /api/v1/rbac/templates/        — list all templates in stable order
//   GET /api/v1/rbac/templates/{name}/ — fetch one template by slug
//
// Both require an authed user but no special RBAC permission — the
// data is non-sensitive metadata about what canned roles the platform
// knows how to apply. The eventual apply endpoint (POST
// /api/v1/projects/{id}/apply-rbac-template/) WILL gate on
// rbac.ResourceRBAC + VerbCreate; that handler ships in a follow-up.

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// SetTemplateCatalog wires the pre-loaded catalog into the handler.
// The server initialises the catalog once at startup via
// rbac.LoadCatalog and passes it in. nil-safe; when the catalog is
// not wired both handlers respond with a 503 so the operator notices.
func (h *RBACHandler) SetTemplateCatalog(c *rbac.Catalog) {
	if h == nil {
		return
	}
	h.templates = c
}

// ListTemplates returns every template in stable order. Frontend
// consumes this on the /dashboard/admin/templates page.
func (h *RBACHandler) ListTemplates(w http.ResponseWriter, r *http.Request) {
	if h.templates == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.CatalogUnavailable, "RBAC template catalog not loaded")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"templates": h.templates.All(),
		"count":     h.templates.Count(),
	})
}

// GetTemplate returns a single template by URL slug. Returns 404 when
// the name doesn't exist in the catalog.
func (h *RBACHandler) GetTemplate(w http.ResponseWriter, r *http.Request) {
	if h.templates == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.CatalogUnavailable, "RBAC template catalog not loaded")
		return
	}
	name := chi.URLParam(r, "name")
	if name == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidName, "template name is required")
		return
	}
	t, ok := h.templates.Get(name)
	if !ok {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "template not found")
		return
	}
	RespondJSON(w, http.StatusOK, t)
}
