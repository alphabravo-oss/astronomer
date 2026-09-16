package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// --- Request types ---

// CreateTemplateRequest represents the request body for creating a pod security template.
// openapi:request PodSecurityTemplateWriteRequest
type CreateTemplateRequest struct {
	Name                 string          `json:"name" validate:"required"`
	Description          string          `json:"description"`
	IsDefault            bool            `json:"is_default"`
	EnforceLevel         string          `json:"enforce_level"`
	EnforceVersion       string          `json:"enforce_version"`
	AuditLevel           string          `json:"audit_level"`
	AuditVersion         string          `json:"audit_version"`
	WarnLevel            string          `json:"warn_level"`
	WarnVersion          string          `json:"warn_version"`
	ExemptUsernames      json.RawMessage `json:"exempt_usernames"`
	ExemptRuntimeClasses json.RawMessage `json:"exempt_runtime_classes"`
	ExemptNamespaces     json.RawMessage `json:"exempt_namespaces"`
}

// --- Endpoints ---

// ListTemplates handles GET /api/v1/security/templates/.
func (h *SecurityHandler) ListTemplates(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 20))
	offset := int32(queryOffset(r))

	templates, err := h.queries.ListPodSecurityTemplates(r.Context(), sqlc.ListPodSecurityTemplatesParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list security templates")
		return
	}

	total, err := h.queries.CountPodSecurityTemplates(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count security templates")
		return
	}

	paging.Write(w, templates, paging.Exact(total, queryLimit(r, 20), queryOffset(r), len(templates)))
}

// CreateTemplate handles POST /api/v1/security/templates/.
func (h *SecurityHandler) CreateTemplate(w http.ResponseWriter, r *http.Request) {
	var req CreateTemplateRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}

	exemptUsernames := req.ExemptUsernames
	if exemptUsernames == nil {
		exemptUsernames = json.RawMessage(`[]`)
	}
	exemptRuntimeClasses := req.ExemptRuntimeClasses
	if exemptRuntimeClasses == nil {
		exemptRuntimeClasses = json.RawMessage(`[]`)
	}
	exemptNamespaces := req.ExemptNamespaces
	if exemptNamespaces == nil {
		exemptNamespaces = json.RawMessage(`[]`)
	}

	params := sqlc.CreatePodSecurityTemplateParams{
		Name:                 req.Name,
		Description:          req.Description,
		IsDefault:            req.IsDefault,
		EnforceLevel:         req.EnforceLevel,
		EnforceVersion:       req.EnforceVersion,
		AuditLevel:           req.AuditLevel,
		AuditVersion:         req.AuditVersion,
		WarnLevel:            req.WarnLevel,
		WarnVersion:          req.WarnVersion,
		ExemptUsernames:      exemptUsernames,
		ExemptRuntimeClasses: exemptRuntimeClasses,
		ExemptNamespaces:     exemptNamespaces,
		CreatedByID:          currentUserUUID(r),
	}
	var template sqlc.PodSecurityTemplate
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "security transaction runner is not configured")
		return
	}
	err := h.runTx(r.Context(), func(q SecurityMutationTx) error {
		var createErr error
		template, createErr = q.CreatePodSecurityTemplate(r.Context(), params)
		if createErr != nil {
			return createErr
		}
		return recordSecurityAuditOutbox(r, q, "security.template.create", "pod_security_template", template.ID.String(), template.Name, http.StatusCreated, map[string]any{
			"enforce_level": template.EnforceLevel, "audit_level": template.AuditLevel,
			"warn_level": template.WarnLevel, "is_default": template.IsDefault,
		})
	})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create security template")
		return
	}

	w.Header().Set("Location", "/api/v1/security/templates/"+template.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, template)
}

// GetTemplate handles GET /api/v1/security/templates/{id}/.
func (h *SecurityHandler) GetTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid template ID")
		return
	}

	template, err := h.queries.GetPodSecurityTemplateByID(r.Context(), id)
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusNotFound, apierror.NotFound, "Security template not found")
		return
	}

	RespondJSON(w, http.StatusOK, template)
}

// DeleteTemplate handles DELETE /api/v1/security/templates/{id}/.
func (h *SecurityHandler) DeleteTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid template ID")
		return
	}

	templateName := ""
	if existing, lookupErr := h.queries.GetPodSecurityTemplateByID(r.Context(), id); lookupErr == nil {
		// Built-in PSA starter templates are platform-owned: operators may
		// inspect them and assign them to clusters, but Update/Delete are
		// refused so an upgrade doesn't have to handle a half-edited or
		// missing default. Mirrors the platform-baseline guard on
		// cluster_templates.
		if existing.IsBuiltin {
			RespondRequestError(w, r, http.StatusForbidden, apierror.BuiltinTemplate,
				fmt.Sprintf("%q is a built-in PSA template and cannot be deleted.", existing.Name))
			return
		}
		templateName = existing.Name
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "security transaction runner is not configured")
		return
	}
	err = h.runTx(r.Context(), func(q SecurityMutationTx) error {
		if deleteErr := q.DeletePodSecurityTemplate(r.Context(), id); deleteErr != nil {
			return deleteErr
		}
		return recordSecurityAuditOutbox(r, q, "security.template.delete", "pod_security_template", id.String(), templateName, http.StatusNoContent, nil)
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Security template not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UpdateTemplate handles PUT /api/v1/security/templates/{id}/.
func (h *SecurityHandler) UpdateTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid template ID")
		return
	}
	// Refuse mutations to built-in PSA starter templates (mirrors the
	// platform-baseline guard on cluster_templates). Loaded here so we can
	// pre-empt the SQL UPDATE and return a clean 403.
	if existing, gerr := h.queries.GetPodSecurityTemplateByID(r.Context(), id); gerr == nil && existing.IsBuiltin {
		RespondRequestError(w, r, http.StatusForbidden, apierror.BuiltinTemplate,
			fmt.Sprintf("%q is a built-in PSA template and cannot be edited.", existing.Name))
		return
	}
	var req CreateTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	params := sqlc.UpdatePodSecurityTemplateParams{
		ID:                   id,
		Name:                 req.Name,
		Description:          req.Description,
		IsDefault:            req.IsDefault,
		EnforceLevel:         req.EnforceLevel,
		EnforceVersion:       req.EnforceVersion,
		AuditLevel:           req.AuditLevel,
		AuditVersion:         req.AuditVersion,
		WarnLevel:            req.WarnLevel,
		WarnVersion:          req.WarnVersion,
		ExemptUsernames:      req.ExemptUsernames,
		ExemptRuntimeClasses: req.ExemptRuntimeClasses,
		ExemptNamespaces:     req.ExemptNamespaces,
	}
	var template sqlc.PodSecurityTemplate
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "security transaction runner is not configured")
		return
	}
	err = h.runTx(r.Context(), func(q SecurityMutationTx) error {
		var updateErr error
		template, updateErr = q.UpdatePodSecurityTemplate(r.Context(), params)
		if updateErr != nil {
			return updateErr
		}
		return recordSecurityAuditOutbox(r, q, "security.template.update", "pod_security_template", template.ID.String(), template.Name, http.StatusOK, map[string]any{
			"enforce_level": template.EnforceLevel, "is_default": template.IsDefault,
		})
	})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.UpdateError, "Failed to update security template")
		return
	}
	RespondJSON(w, http.StatusOK, template)
}
