package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (h *WebhookHandler) List(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, err.Error())
		return
	}
	rows, err := h.queries.ListWebhookSubscriptions(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ReadError, "Failed to list webhook subscriptions")
		return
	}
	items := make([]subscriptionResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, toSubscriptionResponse(row))
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"total": len(items),
	})
}

// Create handles POST /api/v1/admin/webhooks/.
func (h *WebhookHandler) Create(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, err.Error())
		return
	}
	var req subscriptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	// Apply defaults for required fields.
	if req.Name == nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "name is required")
		return
	}
	if req.URL == nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "url is required")
		return
	}
	if req.Secret == nil || strings.TrimSpace(*req.Secret) == "" || *req.Secret == SecretSentinel {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "secret is required on create")
		return
	}
	merged, vErr := h.mergeForCreate(req)
	if vErr != "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, vErr)
		return
	}
	// Uniqueness check (the unique index would catch this too, but a
	// pre-flight returns a friendlier error than the raw constraint
	// violation).
	if existing, err := h.queries.GetWebhookSubscriptionByName(r.Context(), merged.Name); err == nil && existing.ID != uuid.Nil {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "A webhook subscription with this name already exists")
		return
	}

	if h.encryptor == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Encryptor is not configured; cannot store webhook secret")
		return
	}
	secretEnc, err := h.encryptor.Encrypt(*req.Secret)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncryptError, "Failed to encrypt webhook secret")
		return
	}

	filtersJSON, _ := json.Marshal(merged.EventFilters)
	headersJSON, _ := json.Marshal(merged.ExtraHeaders)

	params := sqlc.CreateWebhookSubscriptionParams{
		Name:            merged.Name,
		Url:             merged.URL,
		SecretEncrypted: secretEnc,
		EventFilters:    filtersJSON,
		PayloadTemplate: merged.PayloadTemplate,
		ExtraHeaders:    headersJSON,
		Enabled:         merged.Enabled,
		MaxRetries:      int32(merged.MaxRetries),
		TimeoutSeconds:  int32(merged.TimeoutSeconds),
		CreatedBy:       currentUserUUID(r),
	}
	saved, err := executeMutation(r, h.runTx,
		func(q WebhookMutationTx) (sqlc.WebhookSubscription, error) {
			return q.CreateWebhookSubscription(r.Context(), params)
		},
		func(saved sqlc.WebhookSubscription) mutationAuditEvent {
			return mutationAuditEvent{
				action: "admin.webhook.created", resourceType: "webhook_subscription", resourceID: saved.ID.String(), resourceName: saved.Name,
				status: http.StatusCreated,
				detail: map[string]any{"url": saved.Url, "event_filters": merged.EventFilters, "enabled": saved.Enabled, "max_retries": saved.MaxRetries},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.WriteError, "Failed to create webhook subscription")
		return
	}
	if h.tap != nil {
		h.tap.Invalidate()
	}
	RespondJSON(w, http.StatusCreated, toSubscriptionResponse(saved))
}

// Get handles GET /api/v1/admin/webhooks/{id}/.
func (h *WebhookHandler) Get(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, err.Error())
		return
	}
	id, ok := parseUUIDParam(r, "id")
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid subscription id")
		return
	}
	row, err := h.queries.GetWebhookSubscription(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Webhook subscription not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ReadError, "Failed to read webhook subscription")
		return
	}
	RespondJSON(w, http.StatusOK, toSubscriptionResponse(row))
}

// Update handles PUT /api/v1/admin/webhooks/{id}/.
func (h *WebhookHandler) Update(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, err.Error())
		return
	}
	id, ok := parseUUIDParam(r, "id")
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid subscription id")
		return
	}
	existing, err := h.queries.GetWebhookSubscription(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Webhook subscription not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ReadError, "Failed to read webhook subscription")
		return
	}
	var req subscriptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	merged, vErr := h.mergeForUpdate(existing, req)
	if vErr != "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, vErr)
		return
	}
	// Secret handling: SecretSentinel means "keep existing". Any other
	// non-empty value is re-encrypted.
	encryptedSecret := existing.SecretEncrypted
	if req.Secret != nil && *req.Secret != SecretSentinel {
		if strings.TrimSpace(*req.Secret) == "" {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "secret cannot be blanked; supply a new value or omit the field")
			return
		}
		if h.encryptor == nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Encryptor unavailable")
			return
		}
		enc, encErr := h.encryptor.Encrypt(*req.Secret)
		if encErr != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncryptError, "Failed to encrypt secret")
			return
		}
		encryptedSecret = enc
	}
	filtersJSON, _ := json.Marshal(merged.EventFilters)
	headersJSON, _ := json.Marshal(merged.ExtraHeaders)

	params := sqlc.UpdateWebhookSubscriptionParams{
		ID:              id,
		Name:            merged.Name,
		Url:             merged.URL,
		SecretEncrypted: encryptedSecret,
		EventFilters:    filtersJSON,
		PayloadTemplate: merged.PayloadTemplate,
		ExtraHeaders:    headersJSON,
		Enabled:         merged.Enabled,
		MaxRetries:      int32(merged.MaxRetries),
		TimeoutSeconds:  int32(merged.TimeoutSeconds),
	}
	saved, err := executeMutation(r, h.runTx,
		func(q WebhookMutationTx) (sqlc.WebhookSubscription, error) {
			return q.UpdateWebhookSubscription(r.Context(), params)
		},
		func(saved sqlc.WebhookSubscription) mutationAuditEvent {
			return mutationAuditEvent{
				action: "admin.webhook.updated", resourceType: "webhook_subscription", resourceID: saved.ID.String(), resourceName: saved.Name,
				status: http.StatusOK, detail: map[string]any{"url": saved.Url, "event_filters": merged.EventFilters, "enabled": saved.Enabled},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.WriteError, "Failed to update webhook subscription")
		return
	}
	if h.tap != nil {
		h.tap.Invalidate()
	}
	RespondJSON(w, http.StatusOK, toSubscriptionResponse(saved))
}

// Delete handles DELETE /api/v1/admin/webhooks/{id}/. CASCADE on
// webhook_deliveries means the delivery history is wiped automatically.
func (h *WebhookHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, err.Error())
		return
	}
	id, ok := parseUUIDParam(r, "id")
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid subscription id")
		return
	}
	existing, err := h.queries.GetWebhookSubscription(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Webhook subscription not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ReadError, "Failed to read webhook subscription")
		return
	}
	// T6.064 — refuse delete when the subscription is named in the
	// active compliance baseline's required_webhooks, UNLESS the caller
	// holds the RBAC override permission. Operators without the override
	// must either revert the baseline or detach the requirement first.
	if slug, required := activeBaselineRequiresWebhook(r.Context(), h.queries, existing.Name); required {
		if !baselineOverrideAllowed(h.override, r) {
			RespondRequestError(w, r, http.StatusConflict, apierror.BaselineRequired,
				fmt.Sprintf("Webhook %q is required by the active compliance baseline %q.", existing.Name, slug))
			return
		}
		h.log.Warn("compliance deletion guard overridden",
			slog.String("webhook", existing.Name), slog.String("baseline", slug))
	}
	_, err = executeMutation(r, h.runTx,
		func(q WebhookMutationTx) (sqlc.WebhookSubscription, error) {
			return existing, q.DeleteWebhookSubscription(r.Context(), id)
		},
		func(existing sqlc.WebhookSubscription) mutationAuditEvent {
			return mutationAuditEvent{
				action: "admin.webhook.deleted", resourceType: "webhook_subscription", resourceID: id.String(), resourceName: existing.Name,
				status: http.StatusNoContent,
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.WriteError, "Failed to delete webhook subscription")
		return
	}
	if h.tap != nil {
		h.tap.Invalidate()
	}
	w.WriteHeader(http.StatusNoContent)
}

// Test handles POST /api/v1/admin/webhooks/{id}/test/. Enqueues a
// synthetic event tied to this subscription so it travels the same
// pipeline a real event would — the dispatcher picks it up on its
// next tick.
