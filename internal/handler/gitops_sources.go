package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (h *GitOpsHandler) gate(w http.ResponseWriter, r *http.Request) bool {
	_, ok := requireSuperuser(w, r, h.queries, superuserGateConfig{
		StoreUnavailableMessage: "GitOps store not configured",
		ForbiddenMessage:        "GitOps administration requires superuser privileges",
	})
	return ok
}

// List handles GET /api/v1/admin/gitops-sources/.
func (h *GitOpsHandler) List(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	rows, err := h.queries.ListGitOpsSources(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list gitops sources")
		return
	}
	out := make([]gitopsSourceResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toGitOpsSourceResponse(row))
	}
	page, pagination := pageWindow(r, out)
	paging.Write(w, page, pagination)
}

// Create handles POST /api/v1/admin/gitops-sources/.
func (h *GitOpsHandler) Create(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	var req gitopsSourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if err := validateGitOpsRequest(&req, true); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	// Auth material is Fernet-sealed before opening the mutation transaction.
	// Decrypt happens only inside the sync worker; an unwired encryptor fails
	// closed instead of writing a misleading plaintext auth_encrypted value.
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	authBlob := req.Auth
	if authBlob != "" {
		if h.encryptor == nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "GitOps credential encryption is not configured")
			return
		}
		ct, encErr := h.encryptor.Encrypt(authBlob)
		if encErr != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncryptError, "Failed to encrypt gitops auth blob")
			return
		}
		authBlob = ct
	}
	webhookProvider := ""
	if req.WebhookProvider != nil {
		webhookProvider = strings.TrimSpace(*req.WebhookProvider)
	}
	webhookSecret, err := h.encryptWebhookSecret(req.WebhookSecret)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, err.Error())
		return
	}
	params := sqlc.CreateGitOpsSourceParams{
		Name:                   req.Name,
		RepoUrl:                req.RepoURL,
		Branch:                 gitopsDefaultString(req.Branch, "main"),
		PathPrefix:             req.PathPrefix,
		AuthMode:               gitopsDefaultString(req.AuthMode, "none"),
		AuthEncrypted:          authBlob,
		SyncMode:               gitopsDefaultString(req.SyncMode, "interval"),
		SyncIntervalSeconds:    defaultIntervalSeconds(req.SyncIntervalSeconds),
		OnDelete:               gitopsDefaultString(req.OnDelete, "log"),
		Enabled:                enabled,
		CreatedBy:              currentUserUUID(r),
		WebhookProvider:        webhookProvider,
		WebhookSecretEncrypted: webhookSecret,
	}
	row, err := executeMutation(r, h.runTx,
		func(q GitOpsMutationTx) (sqlc.GitopsRegistrationSource, error) {
			return q.CreateGitOpsSource(r.Context(), params)
		},
		func(row sqlc.GitopsRegistrationSource) mutationAuditEvent {
			return mutationAuditEvent{
				action: "admin.gitops_source.created", resourceType: "gitops_source",
				resourceID: row.ID.String(), resourceName: row.Name, status: http.StatusCreated,
				detail: gitOpsSourceAuditDetail(row),
			}
		})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create gitops source")
		return
	}
	h.warnLargeBlastRadius(r.Context(), row)
	w.Header().Set("Location", "/api/v1/admin/gitops-sources/"+row.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, toGitOpsSourceResponse(row))
}

// Get handles GET /api/v1/admin/gitops-sources/{id}/.
func (h *GitOpsHandler) Get(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid source ID")
		return
	}
	row, err := h.queries.GetGitOpsSource(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "GitOps source not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.GetError, "Failed to load gitops source")
		return
	}
	RespondJSON(w, http.StatusOK, toGitOpsSourceResponse(row))
}

// Update handles PUT /api/v1/admin/gitops-sources/{id}/.
func (h *GitOpsHandler) Update(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid source ID")
		return
	}
	var req gitopsSourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if err := validateGitOpsRequest(&req, false); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	// auth blob: a sentinel or empty value means "keep the stored blob".
	// A fresh, non-sentinel value must be encrypted at rest exactly like
	// Create — otherwise a PUT that rotates the git PAT / SSH key would
	// silently persist the new credential in PLAINTEXT in auth_encrypted
	// (the sync worker's decryptGitAuth() falls back to the raw value on a
	// Fernet-decrypt miss, so the leak is invisible at runtime).
	replaceAuth := req.Auth != GitOpsAuthSentinel && req.Auth != ""
	var replacementAuth string
	if replaceAuth {
		if h.encryptor == nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "GitOps credential encryption is not configured")
			return
		}
		ct, encErr := h.encryptor.Encrypt(req.Auth)
		if encErr != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncryptError, "Failed to encrypt gitops auth blob")
			return
		}
		replacementAuth = ct
	}
	var replacementWebhookSecret string
	if req.WebhookSecret != "" {
		var encErr error
		replacementWebhookSecret, encErr = h.encryptWebhookSecret(req.WebhookSecret)
		if encErr != nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, encErr.Error())
			return
		}
	}
	updateSource := func(q GitOpsQuerier) (gitOpsUpdateMutationResult, error) {
		existing, getErr := q.GetGitOpsSource(r.Context(), id)
		if getErr != nil {
			return gitOpsUpdateMutationResult{}, getErr
		}
		authBlob := existing.AuthEncrypted
		if replaceAuth {
			authBlob = replacementAuth
		}
		enabled := existing.Enabled
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		allowMass := existing.AllowMassDecommission
		if req.AllowMassDecommission != nil {
			allowMass = *req.AllowMassDecommission
		}
		webhookProvider := existing.WebhookProvider
		webhookSecret := existing.WebhookSecretEncrypted
		if req.WebhookProvider != nil {
			webhookProvider = strings.TrimSpace(*req.WebhookProvider)
			if webhookProvider == "" {
				webhookSecret = ""
			}
		}
		if replacementWebhookSecret != "" {
			webhookSecret = replacementWebhookSecret
		}
		if webhookProvider != "" && webhookSecret == "" {
			return gitOpsUpdateMutationResult{}, errGitOpsWebhookSecretRequired
		}
		row, updateErr := q.UpdateGitOpsSource(r.Context(), sqlc.UpdateGitOpsSourceParams{
			ID:                     id,
			Name:                   gitopsDefaultString(req.Name, existing.Name),
			RepoUrl:                gitopsDefaultString(req.RepoURL, existing.RepoUrl),
			Branch:                 gitopsDefaultString(req.Branch, existing.Branch),
			PathPrefix:             req.PathPrefix,
			AuthMode:               gitopsDefaultString(req.AuthMode, existing.AuthMode),
			AuthEncrypted:          authBlob,
			SyncMode:               gitopsDefaultString(req.SyncMode, existing.SyncMode),
			SyncIntervalSeconds:    defaultIntervalSecondsOr(req.SyncIntervalSeconds, existing.SyncIntervalSeconds),
			OnDelete:               gitopsDefaultString(req.OnDelete, existing.OnDelete),
			Enabled:                enabled,
			AllowMassDecommission:  allowMass,
			WebhookProvider:        webhookProvider,
			WebhookSecretEncrypted: webhookSecret,
		})
		return gitOpsUpdateMutationResult{
			row: row, overrideArmed: req.AllowMassDecommission != nil &&
				*req.AllowMassDecommission && !existing.AllowMassDecommission,
		}, updateErr
	}
	result, err := executeMutation(r, h.runTx,
		func(q GitOpsMutationTx) (gitOpsUpdateMutationResult, error) { return updateSource(q) },
		func(result gitOpsUpdateMutationResult) mutationAuditEvent {
			detail := gitOpsSourceAuditDetail(result.row)
			detail["mass_decommission_override_armed"] = result.overrideArmed
			return mutationAuditEvent{
				action: "admin.gitops_source.updated", resourceType: "gitops_source",
				resourceID: result.row.ID.String(), resourceName: result.row.Name,
				status: http.StatusOK, detail: detail,
			}
		})
	if err != nil {
		if errors.Is(err, errGitOpsWebhookSecretRequired) {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
			return
		}
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "GitOps source not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update gitops source")
		return
	}
	row := result.row
	h.warnLargeBlastRadius(r.Context(), row)
	RespondJSON(w, http.StatusOK, toGitOpsSourceResponse(row))
}

// Delete handles DELETE /api/v1/admin/gitops-sources/{id}/.
func (h *GitOpsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid source ID")
		return
	}
	deleteSource := func(q GitOpsQuerier) (sqlc.GitopsRegistrationSource, error) {
		existing, getErr := q.GetGitOpsSource(r.Context(), id)
		if getErr != nil {
			return sqlc.GitopsRegistrationSource{}, getErr
		}
		if deleteErr := q.DeleteGitOpsSource(r.Context(), id); deleteErr != nil {
			return sqlc.GitopsRegistrationSource{}, deleteErr
		}
		return existing, nil
	}
	_, err = executeMutation(r, h.runTx,
		func(q GitOpsMutationTx) (sqlc.GitopsRegistrationSource, error) { return deleteSource(q) },
		func(existing sqlc.GitopsRegistrationSource) mutationAuditEvent {
			return mutationAuditEvent{
				action: "admin.gitops_source.deleted", resourceType: "gitops_source",
				resourceID: existing.ID.String(), resourceName: existing.Name,
				status: http.StatusNoContent, detail: gitOpsSourceAuditDetail(existing),
			}
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "GitOps source not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete gitops source")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
