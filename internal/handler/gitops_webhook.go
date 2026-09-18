package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Webhook handles POST /api/v1/gitops/sources/{id}/webhook/. It accepts only
// GitHub's native HMAC-SHA256 delivery format. Each source owns a distinct
// Fernet-encrypted secret. Authenticated body digests are consumed once per
// source before queuing a durable sync. GitHub does not sign its delivery ID
// or a freshness timestamp, so receipts remain until the source is deleted.
func (h *GitOpsHandler) Webhook(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid source ID")
		return
	}
	if h == nil || h.runTx == nil || h.encryptor == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "GitOps task outbox not configured")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, gitOpsWebhookBodyLimit+1))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Failed to read webhook payload")
		return
	}
	if len(body) > gitOpsWebhookBodyLimit {
		RespondRequestError(w, r, http.StatusRequestEntityTooLarge, apierror.InvalidBody, "Webhook payload exceeds 1 MiB")
		return
	}
	deliveryID := strings.TrimSpace(r.Header.Get("X-GitHub-Delivery"))
	if !validWebhookDeliveryID(deliveryID) {
		h.recordGitOpsWebhookRejected(r, id, "invalid_delivery_id", "", deliveryID, http.StatusUnauthorized)
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.InvalidToken, "Invalid webhook authentication")
		return
	}

	var source sqlc.GitopsRegistrationSource
	result, err := executeMutation(r, h.runTx, func(q GitOpsMutationTx) (gitOpsSyncMutationResult, error) {
		var getErr error
		source, getErr = q.GetGitOpsSource(r.Context(), id)
		if getErr != nil {
			return gitOpsSyncMutationResult{}, getErr
		}
		if verifyErr := h.verifyGitHubWebhook(source, r.Header, body); verifyErr != nil {
			return gitOpsSyncMutationResult{}, verifyErr
		}
		contentDigest := sha256.Sum256(body)
		if _, receiptErr := q.CreateGitOpsWebhookReceipt(r.Context(), sqlc.CreateGitOpsWebhookReceiptParams{
			SourceID: source.ID, ContentDigest: hex.EncodeToString(contentDigest[:]),
		}); receiptErr != nil {
			if errors.Is(receiptErr, pgx.ErrNoRows) {
				return gitOpsSyncMutationResult{}, errGitOpsWebhookReplay
			}
			return gitOpsSyncMutationResult{}, fmt.Errorf("store GitOps webhook receipt: %w", receiptErr)
		}
		result, getErr := enqueueGitOpsSourceSync(r, q, q, id)
		if getErr != nil {
			return gitOpsSyncMutationResult{}, getErr
		}
		return result, nil
	}, func(result gitOpsSyncMutationResult) mutationAuditEvent {
		return mutationAuditEvent{
			action: "admin.gitops_source.sync_requested", resourceType: "gitops_source",
			resourceID: source.ID.String(), resourceName: source.Name, status: http.StatusAccepted,
			detail: map[string]any{"trigger": "github_webhook", "task_id": result.task.ID.String(), "delivery_id": deliveryID},
		}
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "GitOps source not found")
			return
		}
		if errors.Is(err, errGitOpsWebhookInvalid) {
			h.recordGitOpsWebhookRejected(r, id, "invalid_signature", source.WebhookProvider, deliveryID, http.StatusUnauthorized)
			RespondRequestError(w, r, http.StatusUnauthorized, apierror.InvalidToken, "Invalid webhook authentication")
			return
		}
		if errors.Is(err, errGitOpsWebhookReplay) {
			h.recordGitOpsWebhookRejected(r, id, "replay", source.WebhookProvider, deliveryID, http.StatusConflict)
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Webhook delivery already processed")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.SyncError, "Failed to queue GitOps source sync")
		return
	}
	RespondJSON(w, http.StatusAccepted, map[string]any{"status": "queued", "task_id": result.task.ID.String()})
}

func (h *GitOpsHandler) verifyGitHubWebhook(source sqlc.GitopsRegistrationSource, headers http.Header, body []byte) error {
	if source.WebhookProvider != "github" || source.WebhookSecretEncrypted == "" ||
		!strings.EqualFold(strings.TrimSpace(headers.Get("X-GitHub-Event")), "push") {
		return errGitOpsWebhookInvalid
	}
	secret, err := h.encryptor.DecryptBytes(source.WebhookSecretEncrypted)
	if err != nil {
		return fmt.Errorf("decrypt GitOps webhook secret: %w", err)
	}
	defer clear(secret)
	provided := strings.TrimSpace(headers.Get("X-Hub-Signature-256"))
	if !strings.HasPrefix(provided, "sha256=") {
		return errGitOpsWebhookInvalid
	}
	providedMAC, err := hex.DecodeString(strings.TrimPrefix(provided, "sha256="))
	if err != nil || len(providedMAC) != sha256.Size {
		return errGitOpsWebhookInvalid
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(body)
	if !hmac.Equal(providedMAC, mac.Sum(nil)) {
		return errGitOpsWebhookInvalid
	}
	return nil
}

func validWebhookDeliveryID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' || r == ':' {
			continue
		}
		return false
	}
	return true
}

func (h *GitOpsHandler) recordGitOpsWebhookRejected(r *http.Request, sourceID uuid.UUID, reason, provider, deliveryID string, status int) {
	if h == nil {
		return
	}
	auditor := any(h.audit)
	if h.audit == nil {
		auditor = h.queries
	}
	recordAudit(r, auditor, "admin.gitops_source.webhook_rejected", "gitops_source", sourceID.String(), "", map[string]any{
		"reason": reason, "provider": provider, "delivery_id": deliveryID, "status": status,
	})
}
