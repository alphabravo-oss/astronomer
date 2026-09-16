package handler

// RBAC role-template catalog and application endpoints.
//
// Endpoints:
//
//   GET /api/v1/rbac/templates/        — list all templates in stable order
//   GET /api/v1/rbac/templates/{name}/ — fetch one template by slug
//   POST /api/v1/projects/{id}/apply-rbac-template/ — apply a project template
//
// Reads require an authed user but no special RBAC permission — the
// data is non-sensitive metadata about what canned roles the platform
// knows how to apply. Application is separately gated by rbac:create at the
// target project and re-checks every effective template rule to prevent
// privilege escalation.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/quota"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// openapi:request RBACApplyProjectTemplateRequest
type applyProjectTemplateRequest struct {
	TemplateName string `json:"template_name" validate:"required"`
	UserID       string `json:"user_id" validate:"required"`
}

type appliedProjectTemplateLookup interface {
	GetAppliedProjectRoleTemplateBinding(context.Context, sqlc.GetAppliedProjectRoleTemplateBindingParams) (sqlc.ProjectRoleBinding, error)
}

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

// ApplyProjectTemplate materializes a project-scope catalog template as an
// immutable role and binds it to one existing local user. The template rule
// digest is part of the role identity: retries converge, while a future
// catalog rule change creates a new role rather than silently widening an
// existing grant. External IdP principal discovery is intentionally not
// implied here; SSO identities become searchable after their first login.
func (h *RBACHandler) ApplyProjectTemplate(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.templates == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.CatalogUnavailable, "RBAC template catalog not loaded")
		return
	}
	if h.runTx == nil || h.engine == nil || h.bindings == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RBACUnavailable, "RBAC authorization or transactional audit is not configured")
		return
	}
	projectID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid project ID")
		return
	}
	var req applyProjectTemplateRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid user ID")
		return
	}
	template, ok := h.templates.Get(req.TemplateName)
	if !ok {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "template not found")
		return
	}
	if template.Scope != rbac.ScopeProject {
		RespondRequestError(w, r, http.StatusConflict, apierror.InvalidScope, "template is not valid for project scope")
		return
	}
	rules, err := json.Marshal(template.EffectiveRules())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to encode template rules")
		return
	}
	if !h.enforceNoEscalation(w, r, rules, uuid.UUID{}, projectID, "") {
		return
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(rules))
	alreadyApplied := false
	if lookup, ok := h.queries.(appliedProjectTemplateLookup); ok {
		_, lookupErr := lookup.GetAppliedProjectRoleTemplateBinding(r.Context(), sqlc.GetAppliedProjectRoleTemplateBindingParams{
			UserID: pgtype.UUID{Bytes: userID, Valid: true}, ProjectID: projectID,
			TemplateName:   pgtype.Text{String: template.Name, Valid: true},
			TemplateDigest: pgtype.Text{String: digest, Valid: true},
		})
		switch {
		case lookupErr == nil:
			alreadyApplied = true
		case errors.Is(lookupErr, pgx.ErrNoRows):
			// A new binding follows; quota checks below apply.
		default:
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.RBACUnavailable, "Failed to check existing template binding")
			return
		}
	}

	// Preserve the same tenant quota semantics as a hand-authored project
	// binding. An idempotent retry bypasses quota checks because it adds no
	// member; the unique constraint remains the final arbiter under races.
	if h.enforcer != nil && !alreadyApplied {
		if err := h.enforcer.CheckProjectMemberAdd(r.Context(), projectID); err != nil {
			if qe, ok := quota.IsQuotaExceeded(err); ok {
				WriteQuotaExceeded(w, qe)
				return
			}
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.QuotaCheckError, "Failed to evaluate project member quota")
			return
		}
		if err := h.enforcer.CheckUserProjectAdd(r.Context(), userID); err != nil {
			if qe, ok := quota.IsQuotaExceeded(err); ok {
				WriteQuotaExceeded(w, qe)
				return
			}
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.QuotaCheckError, "Failed to evaluate user project quota")
			return
		}
	}

	roleName := fmt.Sprintf("template-%s-%s", template.Name, digest[:12])
	params := sqlc.ApplyProjectRoleTemplateParams{
		RoleName: roleName, DisplayName: template.DisplayName, Description: template.Description,
		Rules:          rules,
		TemplateName:   pgtype.Text{String: template.Name, Valid: true},
		TemplateDigest: pgtype.Text{String: digest, Valid: true},
		UserID:         pgtype.UUID{Bytes: userID, Valid: true}, ProjectID: projectID,
	}
	var binding sqlc.ApplyProjectRoleTemplateRow
	err = h.runTx(r.Context(), func(q RBACMutationTx) error {
		binding, err = q.ApplyProjectRoleTemplate(r.Context(), params)
		if err != nil {
			return err
		}
		return recordAuditOutbox(
			r, q, "template.apply", "project_role_binding", binding.ID.String(), template.DisplayName,
			http.StatusOK,
			map[string]any{
				"scope": "project", "template_name": template.Name, "template_digest": digest,
				"role_id": binding.RoleID.String(), "user_id": userID.String(), "project_id": projectID.String(),
			},
		)
	})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to apply project role template")
		return
	}
	h.invalidateUser(userID.String())
	w.Header().Set("Location", "/api/v1/rbac/project-role-bindings/"+binding.ID.String()+"/")
	RespondJSON(w, http.StatusOK, binding)
}
