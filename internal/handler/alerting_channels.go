package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// --- Channel Endpoints ---

// ListChannels handles GET /api/v1/alerting/channels/.
func (h *AlertingHandler) ListChannels(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 20))
	offset := int32(queryOffset(r))

	channels, err := h.queries.ListNotificationChannels(r.Context(), sqlc.ListNotificationChannelsParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list notification channels")
		return
	}

	items := make([]map[string]any, 0, len(channels))
	for _, channel := range channels {
		items = append(items, notificationChannelResponse(channel))
	}
	total, _ := h.queries.CountNotificationChannels(r.Context())
	paging.Write(w, items, paging.Exact(total, int(limit), int(offset), len(items)))
}

// CreateChannel handles POST /api/v1/alerting/channels/.
func (h *AlertingHandler) CreateChannel(w http.ResponseWriter, r *http.Request) {
	var req CreateChannelRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}

	configuration := req.Configuration
	if configuration == nil {
		configuration = req.Config
	}
	if configuration == nil {
		configuration = json.RawMessage(`{}`)
	}
	channelType := strings.ToLower(strings.TrimSpace(req.ChannelType))
	if channelType == "" {
		channelType = strings.ToLower(strings.TrimSpace(req.Type))
	}
	// Fail-fast on unknown channel types so operators can't store a
	// row the dispatcher will reject at fire time (which would show up
	// as silent "alert fires, nothing happens"). Supported list is the
	// canonical one the dispatcher actually formats for —
	// see internal/worker/tasks/notification_dispatch.go.
	if !isSupportedChannelType(channelType) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError,
			fmt.Sprintf("Unsupported channel type %q; supported: %s",
				channelType, strings.Join(tasks.SupportedNotificationChannels, ", ")))

		return
	}

	params := sqlc.CreateNotificationChannelParams{
		Name:          req.Name,
		ChannelType:   channelType,
		Configuration: configuration,
		Enabled:       req.Enabled,
		CreatedByID:   currentUserUUID(r),
	}
	channel, err := executeMutation(r, h.runTx,
		func(q AlertingMutationTx) (sqlc.NotificationChannel, error) {
			return q.CreateNotificationChannel(r.Context(), params)
		},
		func(row sqlc.NotificationChannel) mutationAuditEvent {
			return mutationAuditEvent{
				action: "alert.channel.create", resourceType: "notification_channel",
				resourceID: row.ID.String(), resourceName: row.Name, status: http.StatusCreated,
				detail: map[string]any{"channel_type": row.ChannelType, "enabled": row.Enabled},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create notification channel")
		return
	}
	_ = h.syncSharedAlertingAssets(r.Context())

	w.Header().Set("Location", "/api/v1/alerting/channels/"+channel.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, notificationChannelResponse(channel))
}

// GetChannel handles GET /api/v1/alerting/channels/{id}/.
func (h *AlertingHandler) GetChannel(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid channel ID")
		return
	}

	channel, err := h.queries.GetNotificationChannelByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Notification channel not found")
		return
	}

	RespondJSON(w, http.StatusOK, notificationChannelResponse(channel))
}

// UpdateChannel handles PUT /api/v1/alerting/channels/{id}/.
func (h *AlertingHandler) UpdateChannel(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid channel ID")
		return
	}

	current, err := h.queries.GetNotificationChannelByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Notification channel not found")
		return
	}

	var req CreateChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if req.Configuration == nil {
		req.Configuration = req.Config
	}
	if req.Configuration == nil {
		req.Configuration = current.Configuration
	}
	if req.Name == "" {
		req.Name = current.Name
	}
	req.ChannelType = strings.ToLower(strings.TrimSpace(req.ChannelType))
	if req.ChannelType == "" {
		req.ChannelType = strings.ToLower(strings.TrimSpace(req.Type))
	}
	if req.ChannelType == "" {
		req.ChannelType = current.ChannelType
	}
	if !isSupportedChannelType(req.ChannelType) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError,
			fmt.Sprintf("Unsupported channel type %q; supported: %s",
				req.ChannelType, strings.Join(tasks.SupportedNotificationChannels, ", ")))

		return
	}

	params := sqlc.UpdateNotificationChannelParams{
		ID:            id,
		Name:          req.Name,
		ChannelType:   req.ChannelType,
		Configuration: req.Configuration,
		Enabled:       req.Enabled,
	}
	channel, err := executeMutation(r, h.runTx,
		func(q AlertingMutationTx) (sqlc.NotificationChannel, error) {
			return q.UpdateNotificationChannel(r.Context(), params)
		},
		func(row sqlc.NotificationChannel) mutationAuditEvent {
			return mutationAuditEvent{
				action: "alert.channel.update", resourceType: "notification_channel",
				resourceID: row.ID.String(), resourceName: row.Name, status: http.StatusOK,
				detail: map[string]any{"channel_type": row.ChannelType, "enabled": row.Enabled},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.UpdateError, "Failed to update notification channel")
		return
	}
	_ = h.syncSharedAlertingAssets(r.Context())

	RespondJSON(w, http.StatusOK, notificationChannelResponse(channel))
}

// TestChannel handles POST /api/v1/alerting/channels/{id}/test/.
func (h *AlertingHandler) TestChannel(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid channel ID")
		return
	}
	channel, err := h.queries.GetNotificationChannelByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Notification channel not found")
		return
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "Alerting transaction runner is not configured")
		return
	}
	recipients := tasks.NotificationRecipients(channel)
	if len(recipients) == 0 {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.NoDestination, "Channel has no configured destination to test")
		return
	}
	deliveryID := "alert-channel-test:" + id.String() + ":" + uuid.NewString()
	payload := tasks.NotificationSendPayload{
		Channel:    channel.ChannelType,
		Subject:    "Astronomer test notification",
		Body:       "This is a test notification from Astronomer for channel \"" + channel.Name + "\". If you can see this, delivery is working.",
		Recipients: recipients,
		Severity:   "info",
		ChannelID:  channel.ID.String(),
		DeliveryID: deliveryID,
	}
	_, err = executeMutation(r, h.runTx,
		func(q AlertingMutationTx) (sqlc.NotificationChannel, error) {
			enqueueErr := tasks.EnqueueNotificationOutbox(r.Context(), q, payload, deliveryID)
			return channel, enqueueErr
		},
		func(row sqlc.NotificationChannel) mutationAuditEvent {
			return mutationAuditEvent{
				action: "alert.channel.test", resourceType: "notification_channel",
				resourceID: row.ID.String(), resourceName: row.Name, status: http.StatusOK,
				detail: map[string]any{"channel_type": row.ChannelType},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusBadGateway, apierror.EnqueueError, "Failed to enqueue test notification")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Test notification queued for " + channel.Name})
}

// DeleteChannel handles DELETE /api/v1/alerting/channels/{id}/.
func (h *AlertingHandler) DeleteChannel(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid channel ID")
		return
	}

	channelName := ""
	if existing, lookupErr := h.queries.GetNotificationChannelByID(r.Context(), id); lookupErr == nil {
		channelName = existing.Name
	}
	_, err = executeMutation(r, h.runTx,
		func(q AlertingMutationTx) (struct{}, error) {
			return struct{}{}, q.DeleteNotificationChannel(r.Context(), id)
		},
		func(struct{}) mutationAuditEvent {
			return mutationAuditEvent{
				action: "alert.channel.delete", resourceType: "notification_channel",
				resourceID: id.String(), resourceName: channelName, status: http.StatusNoContent,
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusNotFound, apierror.NotFound, "Notification channel not found")
		return
	}
	_ = h.syncSharedAlertingAssets(r.Context())

	w.WriteHeader(http.StatusNoContent)
}
