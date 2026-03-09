package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// AlertingQuerier abstracts the alerting-related database queries needed by AlertingHandler.
type AlertingQuerier interface {
	// Channels
	ListNotificationChannels(ctx context.Context, arg sqlc.ListNotificationChannelsParams) ([]sqlc.NotificationChannel, error)
	GetNotificationChannelByID(ctx context.Context, id uuid.UUID) (sqlc.NotificationChannel, error)
	CreateNotificationChannel(ctx context.Context, arg sqlc.CreateNotificationChannelParams) (sqlc.NotificationChannel, error)
	DeleteNotificationChannel(ctx context.Context, id uuid.UUID) error
	CountNotificationChannels(ctx context.Context) (int64, error)
	// Rules
	ListAlertRules(ctx context.Context, arg sqlc.ListAlertRulesParams) ([]sqlc.AlertRule, error)
	GetAlertRuleByID(ctx context.Context, id uuid.UUID) (sqlc.AlertRule, error)
	CreateAlertRule(ctx context.Context, arg sqlc.CreateAlertRuleParams) (sqlc.AlertRule, error)
	DeleteAlertRule(ctx context.Context, id uuid.UUID) error
	CountAlertRules(ctx context.Context) (int64, error)
	// Events
	ListAlertEvents(ctx context.Context, arg sqlc.ListAlertEventsParams) ([]sqlc.AlertEvent, error)
	GetAlertEventByID(ctx context.Context, id uuid.UUID) (sqlc.AlertEvent, error)
	CountAlertEvents(ctx context.Context) (int64, error)
	// Silences
	ListAlertSilences(ctx context.Context, arg sqlc.ListAlertSilencesParams) ([]sqlc.AlertSilence, error)
	CreateAlertSilence(ctx context.Context, arg sqlc.CreateAlertSilenceParams) (sqlc.AlertSilence, error)
	DeleteAlertSilence(ctx context.Context, id uuid.UUID) error
	CountAlertSilences(ctx context.Context) (int64, error)
}

// AlertingHandler handles alerting endpoints.
type AlertingHandler struct {
	queries AlertingQuerier
}

// NewAlertingHandler creates a new alerting handler.
func NewAlertingHandler(queries AlertingQuerier) *AlertingHandler {
	return &AlertingHandler{queries: queries}
}

// --- Request types ---

// CreateChannelRequest represents the request body for creating a notification channel.
type CreateChannelRequest struct {
	Name          string          `json:"name"`
	ChannelType   string          `json:"channel_type"`
	Configuration json.RawMessage `json:"configuration"`
	Enabled       bool            `json:"enabled"`
}

// CreateAlertRuleRequest represents the request body for creating an alert rule.
type CreateAlertRuleRequest struct {
	Name            string          `json:"name"`
	ClusterID       *uuid.UUID      `json:"cluster_id"`
	RuleType        string          `json:"rule_type"`
	Configuration   json.RawMessage `json:"configuration"`
	Severity        string          `json:"severity"`
	Enabled         bool            `json:"enabled"`
	CooldownMinutes int32           `json:"cooldown_minutes"`
}

// CreateSilenceRequest represents the request body for creating an alert silence.
type CreateSilenceRequest struct {
	RuleID    *uuid.UUID `json:"rule_id"`
	ClusterID *uuid.UUID `json:"cluster_id"`
	Reason    string     `json:"reason"`
	StartsAt  time.Time  `json:"starts_at"`
	EndsAt    time.Time  `json:"ends_at"`
}

// --- Channel Endpoints ---

// ListChannels handles GET /api/v1/alerting/channels/.
func (h *AlertingHandler) ListChannels(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	channels, err := h.queries.ListNotificationChannels(r.Context(), sqlc.ListNotificationChannelsParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list notification channels")
		return
	}

	total, err := h.queries.CountNotificationChannels(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count notification channels")
		return
	}

	RespondPaginated(w, r, channels, total)
}

// CreateChannel handles POST /api/v1/alerting/channels/.
func (h *AlertingHandler) CreateChannel(w http.ResponseWriter, r *http.Request) {
	var req CreateChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	if req.Name == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Channel name is required")
		return
	}

	configuration := req.Configuration
	if configuration == nil {
		configuration = json.RawMessage(`{}`)
	}

	channel, err := h.queries.CreateNotificationChannel(r.Context(), sqlc.CreateNotificationChannelParams{
		Name:          req.Name,
		ChannelType:   req.ChannelType,
		Configuration: configuration,
		Enabled:       req.Enabled,
		CreatedByID:   pgtype.UUID{}, // TODO: extract from auth context
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create notification channel")
		return
	}

	RespondJSON(w, http.StatusCreated, channel)
}

// GetChannel handles GET /api/v1/alerting/channels/{id}/.
func (h *AlertingHandler) GetChannel(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid channel ID")
		return
	}

	channel, err := h.queries.GetNotificationChannelByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Notification channel not found")
		return
	}

	RespondJSON(w, http.StatusOK, channel)
}

// DeleteChannel handles DELETE /api/v1/alerting/channels/{id}/.
func (h *AlertingHandler) DeleteChannel(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid channel ID")
		return
	}

	if err := h.queries.DeleteNotificationChannel(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Notification channel not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// --- Rule Endpoints ---

// ListRules handles GET /api/v1/alerting/rules/.
func (h *AlertingHandler) ListRules(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	rules, err := h.queries.ListAlertRules(r.Context(), sqlc.ListAlertRulesParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list alert rules")
		return
	}

	total, err := h.queries.CountAlertRules(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count alert rules")
		return
	}

	RespondPaginated(w, r, rules, total)
}

// CreateRule handles POST /api/v1/alerting/rules/.
func (h *AlertingHandler) CreateRule(w http.ResponseWriter, r *http.Request) {
	var req CreateAlertRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	if req.Name == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Rule name is required")
		return
	}

	configuration := req.Configuration
	if configuration == nil {
		configuration = json.RawMessage(`{}`)
	}

	var clusterID pgtype.UUID
	if req.ClusterID != nil {
		clusterID = pgtype.UUID{Bytes: *req.ClusterID, Valid: true}
	}

	rule, err := h.queries.CreateAlertRule(r.Context(), sqlc.CreateAlertRuleParams{
		Name:            req.Name,
		ClusterID:       clusterID,
		RuleType:        req.RuleType,
		Configuration:   configuration,
		Severity:        req.Severity,
		Enabled:         req.Enabled,
		CooldownMinutes: req.CooldownMinutes,
		CreatedByID:     pgtype.UUID{}, // TODO: extract from auth context
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create alert rule")
		return
	}

	RespondJSON(w, http.StatusCreated, rule)
}

// GetRule handles GET /api/v1/alerting/rules/{id}/.
func (h *AlertingHandler) GetRule(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid rule ID")
		return
	}

	rule, err := h.queries.GetAlertRuleByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Alert rule not found")
		return
	}

	RespondJSON(w, http.StatusOK, rule)
}

// DeleteRule handles DELETE /api/v1/alerting/rules/{id}/.
func (h *AlertingHandler) DeleteRule(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid rule ID")
		return
	}

	if err := h.queries.DeleteAlertRule(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Alert rule not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// --- Event Endpoints ---

// ListEvents handles GET /api/v1/alerting/events/.
func (h *AlertingHandler) ListEvents(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	events, err := h.queries.ListAlertEvents(r.Context(), sqlc.ListAlertEventsParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list alert events")
		return
	}

	total, err := h.queries.CountAlertEvents(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count alert events")
		return
	}

	RespondPaginated(w, r, events, total)
}

// GetEvent handles GET /api/v1/alerting/events/{id}/.
func (h *AlertingHandler) GetEvent(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid event ID")
		return
	}

	event, err := h.queries.GetAlertEventByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Alert event not found")
		return
	}

	RespondJSON(w, http.StatusOK, event)
}

// --- Silence Endpoints ---

// ListSilences handles GET /api/v1/alerting/silences/.
func (h *AlertingHandler) ListSilences(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	silences, err := h.queries.ListAlertSilences(r.Context(), sqlc.ListAlertSilencesParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list alert silences")
		return
	}

	total, err := h.queries.CountAlertSilences(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count alert silences")
		return
	}

	RespondPaginated(w, r, silences, total)
}

// CreateSilence handles POST /api/v1/alerting/silences/.
func (h *AlertingHandler) CreateSilence(w http.ResponseWriter, r *http.Request) {
	var req CreateSilenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	if req.Reason == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Silence reason is required")
		return
	}

	if req.EndsAt.IsZero() {
		RespondError(w, http.StatusBadRequest, "validation_error", "Silence end time is required")
		return
	}

	var ruleID pgtype.UUID
	if req.RuleID != nil {
		ruleID = pgtype.UUID{Bytes: *req.RuleID, Valid: true}
	}

	var clusterID pgtype.UUID
	if req.ClusterID != nil {
		clusterID = pgtype.UUID{Bytes: *req.ClusterID, Valid: true}
	}

	startsAt := req.StartsAt
	if startsAt.IsZero() {
		startsAt = time.Now()
	}

	silence, err := h.queries.CreateAlertSilence(r.Context(), sqlc.CreateAlertSilenceParams{
		RuleID:      ruleID,
		ClusterID:   clusterID,
		Reason:      req.Reason,
		StartsAt:    startsAt,
		EndsAt:      req.EndsAt,
		CreatedByID: pgtype.UUID{}, // TODO: extract from auth context
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create alert silence")
		return
	}

	RespondJSON(w, http.StatusCreated, silence)
}

// DeleteSilence handles DELETE /api/v1/alerting/silences/{id}/.
func (h *AlertingHandler) DeleteSilence(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid silence ID")
		return
	}

	if err := h.queries.DeleteAlertSilence(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Alert silence not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
