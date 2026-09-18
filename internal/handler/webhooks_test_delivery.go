package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (h *WebhookHandler) Test(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, err.Error())
		return
	}
	id, ok := parseUUIDParam(r, "id")
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid subscription id")
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Durable webhook delivery storage is unavailable")
		return
	}
	sub, err := h.queries.GetWebhookSubscription(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Webhook subscription not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ReadError, "Failed to read webhook subscription")
		return
	}
	now := time.Now().UTC()
	payload, _ := json.Marshal(map[string]any{
		"event_name": "webhook.test_ping",
		"event_id":   uuid.New().String(),
		"timestamp":  now,
		"detail": map[string]any{
			"message":      "synthetic test ping from astronomer admin",
			"triggered_by": callerUsername(r),
		},
	})
	params := sqlc.InsertWebhookDeliveryParams{
		SubscriptionID: sub.ID,
		EventName:      "webhook.test_ping",
		EventID:        uuid.New().String(),
		Payload:        payload,
		PayloadSize:    int32(len(payload)),
		Status:         "queued",
		NextAttemptAt:  pgtype.Timestamptz{Time: now, Valid: true},
	}
	idemContext := withOperationIdempotency(r, "admin-webhook-test")
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
				row, getErr := deliveryQ.GetWebhookDelivery(r.Context(), existingID)
				if getErr != nil {
					return webhookDeliveryMutationResult{}, getErr
				}
				if row.SubscriptionID != sub.ID || row.EventName != "webhook.test_ping" {
					return webhookDeliveryMutationResult{}, errOperationIdempotencyConflict
				}
				return webhookDeliveryMutationResult{delivery: row, replay: true}, nil
			}
			row, insertErr := q.InsertWebhookDelivery(r.Context(), params)
			if insertErr != nil {
				return webhookDeliveryMutationResult{}, insertErr
			}
			if attachErr := attachResourceOperation(idemContext, idempotencyQ, "webhook_deliveries", row.ID, toDeliveryResponse(row)); attachErr != nil {
				return webhookDeliveryMutationResult{}, attachErr
			}
			return webhookDeliveryMutationResult{delivery: row}, nil
		},
		func(result webhookDeliveryMutationResult) mutationAuditEvent {
			if result.replay {
				return mutationAuditEvent{}
			}
			return mutationAuditEvent{
				action: "admin.webhook.test_queued", resourceType: "webhook_subscription", resourceID: sub.ID.String(), resourceName: sub.Name,
				status: http.StatusAccepted, detail: map[string]any{"delivery_id": result.delivery.ID.String()},
			}
		})
	if err != nil {
		if errors.Is(err, errOperationIdempotencyConflict) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key already identifies a different webhook delivery")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.WriteError, "Failed to enqueue test delivery")
		return
	}
	RespondAcceptedOperation(w, webhookDeliveryLocation(sub.ID, result.delivery.ID), toDeliveryResponse(result.delivery))
}

// deliveryResponse is one row in the deliveries audit view.
