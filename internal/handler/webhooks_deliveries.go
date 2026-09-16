package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type deliveryResponse struct {
	ID             string  `json:"id"`
	EventName      string  `json:"event_name"`
	EventID        string  `json:"event_id"`
	Status         string  `json:"status"`
	Attempts       int     `json:"attempts"`
	PayloadSize    int     `json:"payload_size"`
	ResponseStatus int     `json:"response_status"`
	ResponseBody   string  `json:"response_body"`
	LastError      string  `json:"last_error"`
	DeliveredAt    *string `json:"delivered_at"`
	NextAttemptAt  *string `json:"next_attempt_at"`
	CreatedAt      string  `json:"created_at"`
}

type webhookDeliveryMutationResult struct {
	delivery sqlc.WebhookDelivery
	replay   bool
}

// Deliveries handles GET /api/v1/admin/webhooks/{id}/deliveries/.
func (h *WebhookHandler) Deliveries(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, err.Error())
		return
	}
	id, ok := parseUUIDParam(r, "id")
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid subscription id")
		return
	}
	limit, offset := queryLimitOffset(r, 50)
	rows, err := h.queries.ListWebhookDeliveriesBySubscription(r.Context(), sqlc.ListWebhookDeliveriesBySubscriptionParams{
		SubscriptionID: id,
		Limit:          int32(limit),
		Offset:         int32(offset),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ReadError, "Failed to read deliveries")
		return
	}
	total, err := h.queries.CountWebhookDeliveriesBySubscription(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ReadError, "Failed to count webhook deliveries")
		return
	}
	items := make([]deliveryResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, toDeliveryResponse(row))
	}
	paging.Write(w, items, paging.Exact(total, limit, offset, len(items)))
}

func webhookDeliveryLocation(subscriptionID, deliveryID uuid.UUID) string {
	return "/api/v1/admin/webhooks/" + subscriptionID.String() + "/deliveries/" + deliveryID.String() + "/"
}

// GetDelivery returns the exact durable delivery receipt used by test and retry.
func (h *WebhookHandler) GetDelivery(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, err.Error())
		return
	}
	subID, subOK := parseUUIDParam(r, "id")
	deliveryID, deliveryOK := parseUUIDParam(r, "delivery_id")
	if !subOK || !deliveryOK {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid subscription or delivery id")
		return
	}
	row, err := h.queries.GetWebhookDelivery(r.Context(), deliveryID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && row.SubscriptionID != subID) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Delivery not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ReadError, "Failed to read delivery")
		return
	}
	RespondJSON(w, http.StatusOK, toDeliveryResponse(row))
}

// RetryDelivery handles POST /api/v1/admin/webhooks/{id}/deliveries/{delivery_id}/retry/.
func (h *WebhookHandler) RetryDelivery(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, err.Error())
		return
	}
	subID, ok := parseUUIDParam(r, "id")
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid subscription id")
		return
	}
	delID, ok := parseUUIDParam(r, "delivery_id")
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid delivery id")
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	row, err := h.queries.GetWebhookDelivery(r.Context(), delID)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Delivery not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ReadError, "Failed to read delivery")
		return
	}
	if row.SubscriptionID != subID {
		// Refuse cross-subscription retry — keeps the URL contract clean
		// (the {id} in the path is load-bearing for the audit + RBAC
		// view).
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Delivery does not belong to this subscription")
		return
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Durable webhook delivery storage is unavailable")
		return
	}
	params := sqlc.RetryWebhookDeliveryParams{
		ID:            delID,
		NextAttemptAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}
	idemContext := withOperationIdempotency(r, "admin-webhook-delivery-retry")
	result, err := executeMutation(r, h.runTx,
		func(q WebhookMutationTx) (webhookDeliveryMutationResult, error) {
			idempotencyQ, ok := q.(resourceOperationIdempotencyQuerier)
			if !ok {
				return webhookDeliveryMutationResult{}, errors.New("durable webhook idempotency storage is unavailable")
			}
			deliveryQ, ok := q.(interface {
				GetWebhookDelivery(context.Context, uuid.UUID) (sqlc.WebhookDelivery, error)
			})
			if !ok {
				return webhookDeliveryMutationResult{}, errors.New("durable webhook delivery storage is unavailable")
			}
			existingID, replay, claimErr := claimResourceOperation(idemContext, idempotencyQ, "webhook_deliveries")
			if claimErr != nil {
				return webhookDeliveryMutationResult{}, claimErr
			}
			if replay {
				existing, getErr := deliveryQ.GetWebhookDelivery(r.Context(), existingID)
				if getErr != nil {
					return webhookDeliveryMutationResult{}, getErr
				}
				if existing.ID != delID || existing.SubscriptionID != subID {
					return webhookDeliveryMutationResult{}, errOperationIdempotencyConflict
				}
				return webhookDeliveryMutationResult{delivery: existing, replay: true}, nil
			}
			if retryErr := q.RetryWebhookDelivery(r.Context(), params); retryErr != nil {
				return webhookDeliveryMutationResult{}, retryErr
			}
			updated, getErr := deliveryQ.GetWebhookDelivery(r.Context(), delID)
			if getErr != nil {
				return webhookDeliveryMutationResult{}, getErr
			}
			if attachErr := attachResourceOperation(idemContext, idempotencyQ, "webhook_deliveries", updated.ID, toDeliveryResponse(updated)); attachErr != nil {
				return webhookDeliveryMutationResult{}, attachErr
			}
			return webhookDeliveryMutationResult{delivery: updated}, nil
		},
		func(result webhookDeliveryMutationResult) mutationAuditEvent {
			if result.replay {
				return mutationAuditEvent{}
			}
			return mutationAuditEvent{
				action: "admin.webhook.delivery.retry", resourceType: "webhook_delivery", resourceID: result.delivery.ID.String(),
				status: http.StatusAccepted, detail: map[string]any{"subscription_id": result.delivery.SubscriptionID.String()},
			}
		})
	if err != nil {
		if errors.Is(err, errOperationIdempotencyConflict) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key already identifies a different webhook delivery")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.WriteError, "Failed to mark delivery for retry")
		return
	}
	RespondAcceptedOperation(w, webhookDeliveryLocation(subID, result.delivery.ID), toDeliveryResponse(result.delivery))
}

// mergedSettings is the validated, all-pointers-resolved view used by
// both the create + update paths.
