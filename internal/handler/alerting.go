package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/strutil"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

// isSupportedChannelType is the create+update gate. Centralized here
// so the handler doesn't drift from the dispatcher's switch list.
func isSupportedChannelType(t string) bool {
	for _, s := range tasks.SupportedNotificationChannels {
		if t == s {
			return true
		}
	}
	return false
}

// AlertingQuerier abstracts the alerting-related database queries needed by AlertingHandler.
type AlertingQuerier interface {
	// Channels
	ListNotificationChannels(ctx context.Context, arg sqlc.ListNotificationChannelsParams) ([]sqlc.NotificationChannel, error)
	GetNotificationChannelByID(ctx context.Context, id uuid.UUID) (sqlc.NotificationChannel, error)
	CreateNotificationChannel(ctx context.Context, arg sqlc.CreateNotificationChannelParams) (sqlc.NotificationChannel, error)
	UpdateNotificationChannel(ctx context.Context, arg sqlc.UpdateNotificationChannelParams) (sqlc.NotificationChannel, error)
	DeleteNotificationChannel(ctx context.Context, id uuid.UUID) error
	CountNotificationChannels(ctx context.Context) (int64, error)
	// Rules
	ListAlertRules(ctx context.Context, arg sqlc.ListAlertRulesParams) ([]sqlc.AlertRule, error)
	GetAlertRuleByID(ctx context.Context, id uuid.UUID) (sqlc.AlertRule, error)
	ListAlertRulesByIDs(ctx context.Context, ids []uuid.UUID) ([]sqlc.AlertRule, error)
	CreateAlertRule(ctx context.Context, arg sqlc.CreateAlertRuleParams) (sqlc.AlertRule, error)
	UpdateAlertRule(ctx context.Context, arg sqlc.UpdateAlertRuleParams) (sqlc.AlertRule, error)
	DeleteAlertRule(ctx context.Context, id uuid.UUID) error
	AddAlertRuleChannel(ctx context.Context, arg sqlc.AddAlertRuleChannelParams) error
	RemoveAlertRuleChannel(ctx context.Context, arg sqlc.RemoveAlertRuleChannelParams) error
	ListChannelsForAlertRule(ctx context.Context, alertRuleID uuid.UUID) ([]sqlc.NotificationChannel, error)
	ListAlertRuleChannelsByRules(ctx context.Context, ruleIds []uuid.UUID) ([]sqlc.AlertRuleChannel, error)
	CountAlertRules(ctx context.Context) (int64, error)
	// Events
	ListAlertEvents(ctx context.Context, arg sqlc.ListAlertEventsParams) ([]sqlc.AlertEvent, error)
	ListAlertEventsByRule(ctx context.Context, arg sqlc.ListAlertEventsByRuleParams) ([]sqlc.AlertEvent, error)
	ListAlertEventsFiltered(ctx context.Context, arg sqlc.ListAlertEventsFilteredParams) ([]sqlc.AlertEvent, error)
	CountAlertEventsFiltered(ctx context.Context, arg sqlc.CountAlertEventsFilteredParams) (int64, error)
	CountActiveAlertsByRules(ctx context.Context, ruleIds []uuid.UUID) ([]sqlc.CountActiveAlertsByRulesRow, error)
	GetAlertEventByID(ctx context.Context, id uuid.UUID) (sqlc.AlertEvent, error)
	AcknowledgeAlertEvent(ctx context.Context, arg sqlc.AcknowledgeAlertEventParams) error
	UpdateAlertEventStatus(ctx context.Context, arg sqlc.UpdateAlertEventStatusParams) error
	CountAlertEvents(ctx context.Context) (int64, error)
	// Silences
	ListAlertSilences(ctx context.Context, arg sqlc.ListAlertSilencesParams) ([]sqlc.AlertSilence, error)
	GetAlertSilenceByID(ctx context.Context, id uuid.UUID) (sqlc.AlertSilence, error)
	CreateAlertSilence(ctx context.Context, arg sqlc.CreateAlertSilenceParams) (sqlc.AlertSilence, error)
	DeleteAlertSilence(ctx context.Context, id uuid.UUID) error
	CountAlertSilences(ctx context.Context) (int64, error)
	// Inhibitions (P-03) — Alertmanager-style inhibition rules.
	ListAlertInhibitions(ctx context.Context, arg sqlc.ListAlertInhibitionsParams) ([]sqlc.AlertInhibition, error)
	GetAlertInhibitionByID(ctx context.Context, id uuid.UUID) (sqlc.AlertInhibition, error)
	CreateAlertInhibition(ctx context.Context, arg sqlc.CreateAlertInhibitionParams) (sqlc.AlertInhibition, error)
	UpdateAlertInhibition(ctx context.Context, arg sqlc.UpdateAlertInhibitionParams) (sqlc.AlertInhibition, error)
	DeleteAlertInhibition(ctx context.Context, id uuid.UUID) error
	CountAlertInhibitions(ctx context.Context) (int64, error)
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	ListClustersByIDs(ctx context.Context, ids []uuid.UUID) ([]sqlc.Cluster, error)
	GetDefaultMonitoringBackend(ctx context.Context) (sqlc.MonitoringBackend, error)
	UpsertDefaultMonitoringBackend(ctx context.Context, arg sqlc.UpsertDefaultMonitoringBackendParams) (sqlc.MonitoringBackend, error)
}

// AlertingHandler handles alerting endpoints.
type AlertingHandler struct {
	queries   AlertingQuerier
	requester K8sRequester
	// enqueuer hands a notification:send task to the worker dispatcher for the
	// "Test Channel" button. Optional — when nil, the test endpoint reports
	// the dispatcher is unavailable rather than pretending success.
	enqueuer tasks.Enqueuer
}

// NewAlertingHandler creates a new alerting handler.
func NewAlertingHandler(queries AlertingQuerier) *AlertingHandler {
	return &AlertingHandler{queries: queries}
}

func NewAlertingHandlerWithDeps(queries AlertingQuerier, requester K8sRequester) *AlertingHandler {
	return &AlertingHandler{queries: queries, requester: requester}
}

// SetEnqueuer wires the asynq client used to dispatch test notifications.
func (h *AlertingHandler) SetEnqueuer(e tasks.Enqueuer) { h.enqueuer = e }

// --- Request types ---

// CreateChannelRequest represents the request body for creating a notification channel.
type CreateChannelRequest struct {
	Name          string          `json:"name" validate:"required"`
	ChannelType   string          `json:"channel_type"`
	Type          string          `json:"type"`
	Configuration json.RawMessage `json:"configuration"`
	Config        json.RawMessage `json:"config"`
	Enabled       bool            `json:"enabled"`
}

// CreateAlertRuleRequest represents the request body for creating an alert rule.
//
// Sprint 072 added the RuleKind / Anomaly* fields. Submitting a body
// with RuleKind="anomaly" requires the operator to supply Metric +
// AnomalyStddev + AnomalyWindowSeconds; the other anomaly fields
// default sensibly (stddev=3, direction=above, min_samples=50).
type CreateAlertRuleRequest struct {
	Name                   string            `json:"name" validate:"required"`
	Description            string            `json:"description"`
	ClusterID              *uuid.UUID        `json:"cluster_id"`
	RuleType               string            `json:"rule_type"`
	Type                   string            `json:"type"`
	Configuration          json.RawMessage   `json:"configuration"`
	Query                  string            `json:"query"`
	Threshold              *float64          `json:"threshold"`
	Duration               string            `json:"duration"`
	Labels                 map[string]string `json:"labels"`
	Annotations            map[string]string `json:"annotations"`
	NotificationChannelIDs []string          `json:"notificationChannelIds"`
	Severity               string            `json:"severity"`
	Enabled                bool              `json:"enabled"`
	CooldownMinutes        int32             `json:"cooldown_minutes"`
	// Sprint 072 anomaly-rule fields. RuleKind="anomaly" engages the
	// rolling-baseline evaluator path; everything else (including the
	// default RuleKind="threshold") uses the existing static-threshold
	// path unchanged.
	RuleKind             string   `json:"rule_kind"`
	Metric               string   `json:"metric"`
	AnomalyStddev        *float64 `json:"anomaly_stddev"`
	AnomalyWindowSeconds *int32   `json:"anomaly_window_seconds"`
	AnomalyMinSamples    *int32   `json:"anomaly_min_samples"`
	AnomalyDirection     string   `json:"anomaly_direction"`
}

// CreateSilenceRequest represents the request body for creating an alert silence.
type CreateSilenceRequest struct {
	RuleID    *uuid.UUID        `json:"rule_id"`
	ClusterID *uuid.UUID        `json:"cluster_id"`
	Reason    string            `json:"reason" validate:"required"`
	StartsAt  time.Time         `json:"starts_at"`
	EndsAt    time.Time         `json:"ends_at"`
	Duration  string            `json:"duration"`
	Matchers  map[string]string `json:"matchers"`
}

// InhibitionMatcher is one label matcher in a source/target matcher set.
// is_regex selects full-string regex matching (anchored) instead of exact
// string equality. Mirrors the Alertmanager inhibit_rule matcher shape.
type InhibitionMatcher struct {
	Label   string `json:"label"`
	Value   string `json:"value"`
	IsRegex bool   `json:"is_regex"`
}

// InhibitionRequest is the create/update body for an inhibition rule (P-03).
type InhibitionRequest struct {
	Name           string              `json:"name" validate:"required"`
	SourceMatchers []InhibitionMatcher `json:"source_matchers"`
	TargetMatchers []InhibitionMatcher `json:"target_matchers"`
	EqualLabels    []string            `json:"equal_labels"`
	Enabled        *bool               `json:"enabled"`
}

// --- Channel Endpoints ---

// ListChannels handles GET /api/v1/alerting/channels/.
func (h *AlertingHandler) ListChannels(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 20))
	offset := int32(queryInt(r, "offset", 0))

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
	RespondList(w, items, NewPagination(int(total), int(limit), int(offset), len(items)))
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

	channel, err := h.queries.CreateNotificationChannel(r.Context(), sqlc.CreateNotificationChannelParams{
		Name:          req.Name,
		ChannelType:   channelType,
		Configuration: configuration,
		Enabled:       req.Enabled,
		CreatedByID:   currentUserUUID(r),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create notification channel")
		return
	}
	_ = h.syncSharedAlertingAssets(r.Context())

	recordAudit(r, h.queries, "alert.channel.create", "notification_channel", channel.ID.String(), channel.Name, map[string]any{
		"channel_type": channel.ChannelType,
		"enabled":      channel.Enabled,
	})

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

	channel, err := h.queries.UpdateNotificationChannel(r.Context(), sqlc.UpdateNotificationChannelParams{
		ID:            id,
		Name:          req.Name,
		ChannelType:   req.ChannelType,
		Configuration: req.Configuration,
		Enabled:       req.Enabled,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update notification channel")
		return
	}
	_ = h.syncSharedAlertingAssets(r.Context())

	recordAudit(r, h.queries, "alert.channel.update", "notification_channel", channel.ID.String(), channel.Name, map[string]any{
		"channel_type": channel.ChannelType,
		"enabled":      channel.Enabled,
	})

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
	if h.enqueuer == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.DispatcherUnavailable, "Notification dispatcher is not available")
		return
	}
	recipients := tasks.NotificationRecipients(channel)
	if len(recipients) == 0 && strings.ToLower(channel.ChannelType) != "email" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.NoDestination, "Channel has no configured destination to test")
		return
	}
	task, err := tasks.NewNotificationSendTask(tasks.NotificationSendPayload{
		Channel:    channel.ChannelType,
		Subject:    "Astronomer test notification",
		Body:       "This is a test notification from Astronomer for channel \"" + channel.Name + "\". If you can see this, delivery is working.",
		Recipients: recipients,
		Severity:   "info",
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.BuildError, "Failed to build test notification")
		return
	}
	if _, err := h.enqueuer.Enqueue(task); err != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.EnqueueError, "Failed to enqueue test notification")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Test notification sent to " + channel.Name})
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
	if err := h.queries.DeleteNotificationChannel(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Notification channel not found")
		return
	}
	_ = h.syncSharedAlertingAssets(r.Context())

	recordAudit(r, h.queries, "alert.channel.delete", "notification_channel", id.String(), channelName, nil)

	w.WriteHeader(http.StatusNoContent)
}

// --- Rule Endpoints ---

// ListRules handles GET /api/v1/alerting/rules/.
func (h *AlertingHandler) ListRules(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 20))
	offset := int32(queryInt(r, "offset", 0))

	rules, err := h.queries.ListAlertRules(r.Context(), sqlc.ListAlertRulesParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list alert rules")
		return
	}

	items := h.alertRuleResponses(r.Context(), rules)
	total, _ := h.queries.CountAlertRules(r.Context())
	RespondList(w, items, NewPagination(int(total), int(limit), int(offset), len(items)))
}

// CreateRule handles POST /api/v1/alerting/rules/.
func (h *AlertingHandler) CreateRule(w http.ResponseWriter, r *http.Request) {
	var req CreateAlertRuleRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if msg := validateAnomalyRuleRequest(req); msg != "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, msg)
		return
	}

	configuration := alertRuleConfiguration(req)

	var clusterID pgtype.UUID
	if req.ClusterID != nil {
		clusterID = pgtype.UUID{Bytes: *req.ClusterID, Valid: true}
	}
	ruleType := req.RuleType
	if ruleType == "" {
		ruleType = req.Type
	}

	rule, err := h.queries.CreateAlertRule(r.Context(), sqlc.CreateAlertRuleParams{
		Name:            req.Name,
		ClusterID:       clusterID,
		RuleType:        ruleType,
		Configuration:   configuration,
		Severity:        req.Severity,
		Enabled:         req.Enabled,
		CooldownMinutes: req.CooldownMinutes,
		CreatedByID:     currentUserUUID(r),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create alert rule")
		return
	}
	if err := h.syncRuleChannels(r.Context(), rule.ID, req.NotificationChannelIDs); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to associate notification channels")
		return
	}
	_ = h.syncSharedAlertingAssets(r.Context())

	recordAudit(r, h.queries, "alert.rule.create", "alert_rule", rule.ID.String(), rule.Name, map[string]any{
		"rule_type": rule.RuleType,
		"severity":  rule.Severity,
		"enabled":   rule.Enabled,
	})

	w.Header().Set("Location", "/api/v1/alerting/rules/"+rule.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, h.alertRuleResponse(r.Context(), rule))
}

// GetRule handles GET /api/v1/alerting/rules/{id}/.
func (h *AlertingHandler) GetRule(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid rule ID")
		return
	}

	rule, err := h.queries.GetAlertRuleByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Alert rule not found")
		return
	}

	RespondJSON(w, http.StatusOK, h.alertRuleResponse(r.Context(), rule))
}

// UpdateRule handles PUT /api/v1/alerting/rules/{id}/.
func (h *AlertingHandler) UpdateRule(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid rule ID")
		return
	}

	current, err := h.queries.GetAlertRuleByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Alert rule not found")
		return
	}

	var req CreateAlertRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if req.Name == "" {
		req.Name = current.Name
	}
	if req.RuleType == "" {
		req.RuleType = req.Type
	}
	if req.RuleType == "" {
		req.RuleType = current.RuleType
	}
	if req.Severity == "" {
		req.Severity = current.Severity
	}
	if req.CooldownMinutes == 0 {
		req.CooldownMinutes = current.CooldownMinutes
	}
	req.Configuration = alertRuleConfigurationWithFallback(req, current.Configuration)

	rule, err := h.queries.UpdateAlertRule(r.Context(), sqlc.UpdateAlertRuleParams{
		ID:              id,
		Name:            req.Name,
		RuleType:        req.RuleType,
		Configuration:   req.Configuration,
		Severity:        req.Severity,
		Enabled:         req.Enabled,
		CooldownMinutes: req.CooldownMinutes,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update alert rule")
		return
	}
	if len(req.NotificationChannelIDs) > 0 {
		if err := h.syncRuleChannels(r.Context(), rule.ID, req.NotificationChannelIDs); err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update notification channels")
			return
		}
	}
	_ = h.syncSharedAlertingAssets(r.Context())

	recordAudit(r, h.queries, "alert.rule.update", "alert_rule", rule.ID.String(), rule.Name, map[string]any{
		"severity": rule.Severity,
		"enabled":  rule.Enabled,
	})

	RespondJSON(w, http.StatusOK, h.alertRuleResponse(r.Context(), rule))
}

// DeleteRule handles DELETE /api/v1/alerting/rules/{id}/.
func (h *AlertingHandler) DeleteRule(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid rule ID")
		return
	}

	ruleName := ""
	if existing, lookupErr := h.queries.GetAlertRuleByID(r.Context(), id); lookupErr == nil {
		ruleName = existing.Name
	}
	if err := h.queries.DeleteAlertRule(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Alert rule not found")
		return
	}
	_ = h.syncSharedAlertingAssets(r.Context())

	recordAudit(r, h.queries, "alert.rule.delete", "alert_rule", id.String(), ruleName, nil)

	w.WriteHeader(http.StatusNoContent)
}

// --- Event Endpoints ---

// ListEvents handles GET /api/v1/alerting/events/.
func (h *AlertingHandler) ListEvents(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 20))
	offset := int32(queryInt(r, "offset", 0))

	// Filters are pushed into SQL so pagination totals are correct across
	// pages (previously status/severity/cluster were applied in-memory to a
	// single page, so status=firing returned 0 while firing events existed
	// on later pages).
	var status pgtype.Text
	if v := r.URL.Query().Get("status"); v != "" {
		status = pgtype.Text{String: v, Valid: true}
	}
	var severity pgtype.Text
	if v := r.URL.Query().Get("severity"); v != "" {
		severity = pgtype.Text{String: v, Valid: true}
	}
	var clusterID pgtype.UUID
	if v := r.URL.Query().Get("clusterId"); v != "" {
		parsed, parseErr := uuid.Parse(v)
		if parseErr != nil {
			// An unparseable cluster filter matches nothing, mirroring the
			// old in-memory string compare against a UUID column.
			RespondList(w, []map[string]any{}, NewPagination(0, int(limit), int(offset), 0))
			return
		}
		clusterID = pgtype.UUID{Bytes: parsed, Valid: true}
	}

	events, err := h.queries.ListAlertEventsFiltered(r.Context(), sqlc.ListAlertEventsFilteredParams{
		Status:    status,
		Severity:  severity,
		ClusterID: clusterID,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list alert events")
		return
	}

	items := alertEventResponsesBatched(r.Context(), h.queries, events)
	total, _ := h.queries.CountAlertEventsFiltered(r.Context(), sqlc.CountAlertEventsFilteredParams{
		Status:    status,
		Severity:  severity,
		ClusterID: clusterID,
	})
	RespondList(w, items, NewPagination(int(total), int(limit), int(offset), len(items)))
}

// GetEvent handles GET /api/v1/alerting/events/{id}/.
func (h *AlertingHandler) GetEvent(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid event ID")
		return
	}

	event, err := h.queries.GetAlertEventByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Alert event not found")
		return
	}

	RespondJSON(w, http.StatusOK, h.alertEventResponse(r.Context(), event))
}

// AcknowledgeEvent handles POST /api/v1/alerting/events/{id}/acknowledge/.
func (h *AlertingHandler) AcknowledgeEvent(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid event ID")
		return
	}
	if _, err := h.queries.GetAlertEventByID(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Alert event not found")
		return
	}
	if err := h.queries.AcknowledgeAlertEvent(r.Context(), sqlc.AcknowledgeAlertEventParams{
		ID:               id,
		AcknowledgedByID: currentUserUUID(r),
	}); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to acknowledge alert event")
		return
	}
	event, _ := h.queries.GetAlertEventByID(r.Context(), id)
	recordAudit(r, h.queries, "alert.event.acknowledge", "alert_event", id.String(), "", nil)
	RespondJSON(w, http.StatusOK, h.alertEventResponse(r.Context(), event))
}

// ResolveEvent handles POST /api/v1/alerting/events/{id}/resolve/.
func (h *AlertingHandler) ResolveEvent(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid event ID")
		return
	}
	if _, err := h.queries.GetAlertEventByID(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Alert event not found")
		return
	}
	if err := h.queries.UpdateAlertEventStatus(r.Context(), sqlc.UpdateAlertEventStatusParams{
		ID:     id,
		Status: "resolved",
	}); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to resolve alert event")
		return
	}
	event, _ := h.queries.GetAlertEventByID(r.Context(), id)
	recordAudit(r, h.queries, "alert.event.resolve", "alert_event", id.String(), "", nil)
	RespondJSON(w, http.StatusOK, h.alertEventResponse(r.Context(), event))
}

// --- Silence Endpoints ---

// ListSilences handles GET /api/v1/alerting/silences/.
func (h *AlertingHandler) ListSilences(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 20))
	offset := int32(queryInt(r, "offset", 0))

	silences, err := h.queries.ListAlertSilences(r.Context(), sqlc.ListAlertSilencesParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list alert silences")
		return
	}

	items := make([]map[string]any, 0, len(silences))
	for _, silence := range silences {
		items = append(items, alertSilenceResponse(silence))
	}
	total, _ := h.queries.CountAlertSilences(r.Context())
	RespondList(w, items, NewPagination(int(total), int(limit), int(offset), len(items)))
}

// CreateSilence handles POST /api/v1/alerting/silences/.
func (h *AlertingHandler) CreateSilence(w http.ResponseWriter, r *http.Request) {
	var req CreateSilenceRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}

	if req.EndsAt.IsZero() {
		if req.Duration == "" {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Silence end time is required")
			return
		}
	}

	var ruleID pgtype.UUID
	if req.RuleID == nil {
		req.RuleID = parseMatcherUUID(req.Matchers, "rule_id", "ruleId")
	}
	if req.RuleID != nil {
		ruleID = pgtype.UUID{Bytes: *req.RuleID, Valid: true}
	}

	var clusterID pgtype.UUID
	if req.ClusterID == nil {
		req.ClusterID = parseMatcherUUID(req.Matchers, "cluster_id", "clusterId")
	}
	if req.ClusterID != nil {
		clusterID = pgtype.UUID{Bytes: *req.ClusterID, Valid: true}
	}

	startsAt := req.StartsAt
	if startsAt.IsZero() {
		startsAt = time.Now()
	}
	endsAt := req.EndsAt
	if endsAt.IsZero() {
		duration, err := time.ParseDuration(req.Duration)
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid silence duration")
			return
		}
		endsAt = startsAt.Add(duration)
	}

	silence, err := h.queries.CreateAlertSilence(r.Context(), sqlc.CreateAlertSilenceParams{
		RuleID:      ruleID,
		ClusterID:   clusterID,
		Reason:      req.Reason,
		StartsAt:    startsAt,
		EndsAt:      endsAt,
		CreatedByID: currentUserUUID(r),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create alert silence")
		return
	}
	_ = h.syncSharedAlertingAssets(r.Context())

	recordAudit(r, h.queries, "alert.silence.create", "alert_silence", silence.ID.String(), req.Reason, map[string]any{
		"starts_at": startsAt.UTC().Format(time.RFC3339),
		"ends_at":   endsAt.UTC().Format(time.RFC3339),
	})

	w.Header().Set("Location", "/api/v1/alerting/silences/"+silence.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, alertSilenceResponse(silence))
}

// EnableRule handles POST /api/v1/alerts/rules/{id}/enable/.
func (h *AlertingHandler) EnableRule(w http.ResponseWriter, r *http.Request) {
	h.setRuleEnabled(w, r, true)
}

// DisableRule handles POST /api/v1/alerts/rules/{id}/disable/.
func (h *AlertingHandler) DisableRule(w http.ResponseWriter, r *http.Request) {
	h.setRuleEnabled(w, r, false)
}

func (h *AlertingHandler) setRuleEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid rule ID")
		return
	}
	current, err := h.queries.GetAlertRuleByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Alert rule not found")
		return
	}
	rule, err := h.queries.UpdateAlertRule(r.Context(), sqlc.UpdateAlertRuleParams{
		ID:              id,
		Name:            current.Name,
		RuleType:        current.RuleType,
		Configuration:   current.Configuration,
		Severity:        current.Severity,
		Enabled:         enabled,
		CooldownMinutes: current.CooldownMinutes,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update alert rule")
		return
	}
	_ = h.syncSharedAlertingAssets(r.Context())
	action := "alert.rule.disable"
	if enabled {
		action = "alert.rule.enable"
	}
	recordAudit(r, h.queries, action, "alert_rule", rule.ID.String(), rule.Name, map[string]any{
		"enabled": enabled,
	})
	RespondJSON(w, http.StatusOK, h.alertRuleResponse(r.Context(), rule))
}

// ExpireSilence handles POST /api/v1/alerts/silences/{id}/expire/.
// Currently this deletes the silence (we lack an UpdateAlertSilence query).
// The response shape preserves the original record for the UI to refresh.
func (h *AlertingHandler) ExpireSilence(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid silence ID")
		return
	}
	match, err := h.queries.GetAlertSilenceByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Alert silence not found")
		return
	}
	if !match.EndsAt.After(time.Now()) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.AlreadyExpired, "This silence has already expired.")
		return
	}
	if err := h.queries.DeleteAlertSilence(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DeleteError, "Failed to expire silence")
		return
	}
	_ = h.syncSharedAlertingAssets(r.Context())
	expired := match
	expired.EndsAt = time.Now()
	recordAudit(r, h.queries, "alert.silence.expire", "alert_silence", id.String(), match.Reason, nil)
	RespondJSON(w, http.StatusOK, alertSilenceResponse(expired))
}

// DeleteSilence handles DELETE /api/v1/alerting/silences/{id}/.
func (h *AlertingHandler) DeleteSilence(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid silence ID")
		return
	}

	match, err := h.queries.GetAlertSilenceByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Alert silence not found")
		return
	}
	if err := h.queries.DeleteAlertSilence(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Alert silence not found")
		return
	}
	_ = h.syncSharedAlertingAssets(r.Context())

	recordAudit(r, h.queries, "alert.silence.delete", "alert_silence", id.String(), match.Reason, nil)
	w.WriteHeader(http.StatusNoContent)
}

// --- Inhibition Endpoints (P-03) ---

// ListInhibitions handles GET /api/v1/admin/alerting/inhibitions/.
func (h *AlertingHandler) ListInhibitions(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 50))
	offset := int32(queryInt(r, "offset", 0))

	inhibitions, err := h.queries.ListAlertInhibitions(r.Context(), sqlc.ListAlertInhibitionsParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list inhibitions")
		return
	}
	items := make([]map[string]any, 0, len(inhibitions))
	for _, inhibition := range inhibitions {
		items = append(items, alertInhibitionResponse(inhibition))
	}
	total, _ := h.queries.CountAlertInhibitions(r.Context())
	RespondList(w, items, NewPagination(int(total), int(limit), int(offset), len(items)))
}

// GetInhibition handles GET /api/v1/admin/alerting/inhibitions/{id}/.
func (h *AlertingHandler) GetInhibition(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid inhibition ID")
		return
	}
	inhibition, err := h.queries.GetAlertInhibitionByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Inhibition not found")
		return
	}
	RespondJSON(w, http.StatusOK, alertInhibitionResponse(inhibition))
}

// CreateInhibition handles POST /api/v1/admin/alerting/inhibitions/.
func (h *AlertingHandler) CreateInhibition(w http.ResponseWriter, r *http.Request) {
	var req InhibitionRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if msg := validateInhibition(req); msg != "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, msg)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	inhibition, err := h.queries.CreateAlertInhibition(r.Context(), sqlc.CreateAlertInhibitionParams{
		Name:           req.Name,
		SourceMatchers: marshalMatchers(req.SourceMatchers),
		TargetMatchers: marshalMatchers(req.TargetMatchers),
		EqualLabels:    marshalEqualLabels(req.EqualLabels),
		Enabled:        enabled,
		CreatedByID:    currentUserUUID(r),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create inhibition")
		return
	}
	recordAudit(r, h.queries, "alert.inhibition.create", "alert_inhibition", inhibition.ID.String(), inhibition.Name, map[string]any{
		"enabled": enabled,
	})
	w.Header().Set("Location", "/api/v1/admin/alerting/inhibitions/"+inhibition.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, alertInhibitionResponse(inhibition))
}

// UpdateInhibition handles PUT /api/v1/admin/alerting/inhibitions/{id}/.
func (h *AlertingHandler) UpdateInhibition(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid inhibition ID")
		return
	}
	if _, err := h.queries.GetAlertInhibitionByID(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Inhibition not found")
		return
	}
	var req InhibitionRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if msg := validateInhibition(req); msg != "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, msg)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	inhibition, err := h.queries.UpdateAlertInhibition(r.Context(), sqlc.UpdateAlertInhibitionParams{
		ID:             id,
		Name:           req.Name,
		SourceMatchers: marshalMatchers(req.SourceMatchers),
		TargetMatchers: marshalMatchers(req.TargetMatchers),
		EqualLabels:    marshalEqualLabels(req.EqualLabels),
		Enabled:        enabled,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update inhibition")
		return
	}
	recordAudit(r, h.queries, "alert.inhibition.update", "alert_inhibition", inhibition.ID.String(), inhibition.Name, map[string]any{
		"enabled": enabled,
	})
	RespondJSON(w, http.StatusOK, alertInhibitionResponse(inhibition))
}

// DeleteInhibition handles DELETE /api/v1/admin/alerting/inhibitions/{id}/.
func (h *AlertingHandler) DeleteInhibition(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid inhibition ID")
		return
	}
	match, err := h.queries.GetAlertInhibitionByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Inhibition not found")
		return
	}
	if err := h.queries.DeleteAlertInhibition(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete inhibition")
		return
	}
	recordAudit(r, h.queries, "alert.inhibition.delete", "alert_inhibition", id.String(), match.Name, nil)
	w.WriteHeader(http.StatusNoContent)
}

// validateInhibition rejects rules that can never match usefully: a rule with
// no source and no target matcher would suppress nothing (or everything), and
// a regex matcher whose pattern does not compile would fail closed at eval
// time. Return an empty string when the rule is acceptable.
func validateInhibition(req InhibitionRequest) string {
	if len(req.SourceMatchers) == 0 {
		return "At least one source matcher is required"
	}
	if len(req.TargetMatchers) == 0 {
		return "At least one target matcher is required"
	}
	for _, m := range append(append([]InhibitionMatcher{}, req.SourceMatchers...), req.TargetMatchers...) {
		if strings.TrimSpace(m.Label) == "" {
			return "Every matcher requires a label"
		}
		if m.IsRegex {
			if _, err := regexp.Compile(m.Value); err != nil {
				return fmt.Sprintf("Invalid regex for label %q: %v", m.Label, err)
			}
		}
	}
	return ""
}

func marshalMatchers(matchers []InhibitionMatcher) json.RawMessage {
	if matchers == nil {
		matchers = []InhibitionMatcher{}
	}
	raw, err := json.Marshal(matchers)
	if err != nil {
		return json.RawMessage("[]")
	}
	return raw
}

func marshalEqualLabels(labels []string) json.RawMessage {
	if labels == nil {
		labels = []string{}
	}
	raw, err := json.Marshal(labels)
	if err != nil {
		return json.RawMessage("[]")
	}
	return raw
}

func alertInhibitionResponse(inhibition sqlc.AlertInhibition) map[string]any {
	var source, target []InhibitionMatcher
	var equal []string
	_ = json.Unmarshal(inhibition.SourceMatchers, &source)
	_ = json.Unmarshal(inhibition.TargetMatchers, &target)
	_ = json.Unmarshal(inhibition.EqualLabels, &equal)
	if source == nil {
		source = []InhibitionMatcher{}
	}
	if target == nil {
		target = []InhibitionMatcher{}
	}
	if equal == nil {
		equal = []string{}
	}
	return map[string]any{
		"id":              inhibition.ID.String(),
		"name":            inhibition.Name,
		"source_matchers": source,
		"target_matchers": target,
		"equal_labels":    equal,
		"enabled":         inhibition.Enabled,
		"created_at":      inhibition.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":      inhibition.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func (h *AlertingHandler) syncSharedAlertingAssets(ctx context.Context) error {
	if h.requester == nil || h.queries == nil {
		return nil
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil
		}
		return err
	}
	meta := sharedThanosMetadata(backend)
	clusterID := stringFromMap(meta, "managementClusterId")
	namespace := defaultString(stringFromMap(meta, "namespace"), "monitoring")
	if clusterID == "" {
		return nil
	}

	rules, err := h.queries.ListAlertRules(ctx, sqlc.ListAlertRulesParams{Limit: 1000, Offset: 0})
	if err != nil {
		return err
	}
	channels, err := h.queries.ListNotificationChannels(ctx, sqlc.ListNotificationChannelsParams{Limit: 1000, Offset: 0})
	if err != nil {
		return err
	}
	silences, err := h.queries.ListAlertSilences(ctx, sqlc.ListAlertSilencesParams{Limit: 1000, Offset: 0})
	if err != nil {
		return err
	}

	ruleContent, err := h.renderRulerRules(ctx, rules)
	if err != nil {
		return err
	}
	alertmanagerRouting, err := h.renderAlertmanagerConfig(ctx, channels, rules)
	if err != nil {
		return err
	}
	silenceContent, err := h.renderSilenceInventory(silences)
	if err != nil {
		return err
	}
	alertmanagerEndpoints, ok, err := h.renderThanosAlertmanagerEndpoints(ctx)
	if err != nil {
		return err
	}

	if err := ensureNamespaceWithRequester(ctx, h.requester, clusterID, namespace); err != nil {
		return err
	}
	if err := applyConfigMap(ctx, h.requester, clusterID, namespace, "astronomer-ruler-rules", map[string]string{
		"rules.yaml": ruleContent,
	}); err != nil {
		return err
	}
	if err := applyConfigMap(ctx, h.requester, clusterID, namespace, "astronomer-alertmanager-routing", map[string]string{
		"alertmanager.yaml": alertmanagerRouting,
	}); err != nil {
		return err
	}
	if err := applyConfigMap(ctx, h.requester, clusterID, namespace, "astronomer-alert-silences", map[string]string{
		"silences.yaml": silenceContent,
	}); err != nil {
		return err
	}
	if ok {
		if err := applyAlertSecret(ctx, h.requester, clusterID, namespace, "astronomer-thanos-rule-alertmanagers", map[string]string{
			"config": alertmanagerEndpoints,
		}); err != nil {
			return err
		}
	}
	if err := h.persistSharedAlertingAssetHashes(ctx, backend, map[string]any{
		"rulerRules":              specHash(ruleContent),
		"alertmanagerRouting":     specHash(alertmanagerRouting),
		"silenceInventory":        specHash(silenceContent),
		"thanosAlertmanagerPeers": specHash(alertmanagerEndpoints),
	}); err != nil {
		return err
	}
	return nil
}

func (h *AlertingHandler) persistSharedAlertingAssetHashes(ctx context.Context, backend sqlc.MonitoringBackend, hashes map[string]any) error {
	authCfg := decodeJSONMap(backend.AuthConfig)
	authCfg["sharedAlertingAssets"] = map[string]any{
		"hashes":    hashes,
		"updatedAt": time.Now().UTC().Format(time.RFC3339),
	}
	raw, err := json.Marshal(authCfg)
	if err != nil {
		return err
	}
	_, err = h.queries.UpsertDefaultMonitoringBackend(ctx, sqlc.UpsertDefaultMonitoringBackendParams{
		BackendType:        backend.BackendType,
		QueryUrl:           backend.QueryUrl,
		AlertmanagerUrl:    backend.AlertmanagerUrl,
		TenantID:           backend.TenantID,
		AuthType:           backend.AuthType,
		AuthConfig:         raw,
		DefaultStepSeconds: backend.DefaultStepSeconds,
		TimeoutSeconds:     backend.TimeoutSeconds,
		CreatedByID:        backend.CreatedByID,
	})
	return err
}

func (h *AlertingHandler) renderRulerRules(ctx context.Context, rules []sqlc.AlertRule) (string, error) {
	groupRules := make([]map[string]any, 0, len(rules))
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		cfg := decodeJSONMap(rule.Configuration)
		expr := strings.TrimSpace(stringFromMap(cfg, "query"))
		if expr == "" {
			expr = fallbackPromExpr(rule, cfg)
		}
		if expr == "" {
			continue
		}
		alertName := sanitizePromRuleName(rule.Name)
		labels := mapStringMap(cfg["labels"])
		if labels == nil {
			labels = map[string]string{}
		}
		labels["severity"] = defaultString(rule.Severity, "warning")
		labels["astronomer_rule_id"] = rule.ID.String()
		if rule.ClusterID.Valid {
			labels["astronomer_cluster_id"] = uuid.UUID(rule.ClusterID.Bytes).String()
		}
		annotations := mapStringMap(cfg["annotations"])
		if annotations == nil {
			annotations = map[string]string{}
		}
		if annotations["summary"] == "" {
			annotations["summary"] = strutil.FirstNonBlank(stringFromMap(cfg, "description"), rule.Name)
		}
		groupRules = append(groupRules, map[string]any{
			"alert":       alertName,
			"expr":        expr,
			"for":         defaultString(stringFromMap(cfg, "duration"), "5m"),
			"labels":      labels,
			"annotations": annotations,
		})
	}
	payload := map[string]any{
		"groups": []map[string]any{{
			"name":  "astronomer.rules",
			"rules": groupRules,
		}},
	}
	raw, err := yaml.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (h *AlertingHandler) renderAlertmanagerConfig(ctx context.Context, channels []sqlc.NotificationChannel, rules []sqlc.AlertRule) (string, error) {
	// Load every rule<->channel link for the rule set in ONE query and
	// build a channel_id -> set(rule_id) map, instead of the old N+1 that
	// ran ListChannelsForAlertRule for every rule on every alerting
	// mutation (O(channels x rules) round-trips).
	channelRuleSet := map[uuid.UUID]map[uuid.UUID]bool{}
	if len(rules) > 0 {
		ruleIDs := make([]uuid.UUID, 0, len(rules))
		for _, rule := range rules {
			ruleIDs = append(ruleIDs, rule.ID)
		}
		links, err := h.queries.ListAlertRuleChannelsByRules(ctx, ruleIDs)
		if err != nil {
			return "", err
		}
		for _, link := range links {
			set := channelRuleSet[link.NotificationChannelID]
			if set == nil {
				set = map[uuid.UUID]bool{}
				channelRuleSet[link.NotificationChannelID] = set
			}
			set[link.AlertRuleID] = true
		}
	}

	receivers := []map[string]any{{"name": "null"}}
	routes := []map[string]any{}
	for _, channel := range channels {
		if !channel.Enabled {
			continue
		}
		receiverName := "channel-" + channel.ID.String()
		receiver := map[string]any{"name": receiverName}
		cfg := decodeJSONMap(channel.Configuration)
		switch strings.ToLower(channel.ChannelType) {
		case "slack":
			if webhook, ok := firstConfigString(cfg, "url", "webhook_url"); ok {
				receiver["webhook_configs"] = []map[string]any{{"url": webhook, "send_resolved": true}}
			}
		case "webhook":
			if webhook, ok := firstConfigString(cfg, "url", "webhook_url"); ok {
				receiver["webhook_configs"] = []map[string]any{{"url": webhook, "send_resolved": true}}
			}
		case "email":
			if email, ok := firstConfigString(cfg, "email", "address"); ok {
				receiver["email_configs"] = []map[string]any{{"to": email, "send_resolved": true}}
			}
		default:
			continue
		}
		receivers = append(receivers, receiver)
		for _, rule := range rulesForChannel(rules, channelRuleSet[channel.ID]) {
			routes = append(routes, map[string]any{
				"receiver": receiverName,
				"matchers": []string{fmt.Sprintf(`astronomer_rule_id="%s"`, rule.ID.String())},
				"continue": true,
			})
		}
	}
	payload := map[string]any{
		"global": map[string]any{
			"resolve_timeout": "5m",
		},
		"route": map[string]any{
			"receiver":        "null",
			"group_by":        []string{"alertname", "astronomer_rule_id", "cluster"},
			"group_wait":      "30s",
			"group_interval":  "5m",
			"repeat_interval": "3h",
			"routes":          routes,
		},
		"receivers": receivers,
	}
	raw, err := yaml.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (h *AlertingHandler) renderSilenceInventory(silences []sqlc.AlertSilence) (string, error) {
	items := make([]map[string]any, 0, len(silences))
	for _, silence := range silences {
		items = append(items, map[string]any{
			"id":        silence.ID.String(),
			"clusterId": nullableUUID(silence.ClusterID),
			"ruleId":    nullableUUID(silence.RuleID),
			"reason":    silence.Reason,
			"startsAt":  silence.StartsAt.UTC().Format(time.RFC3339),
			"endsAt":    silence.EndsAt.UTC().Format(time.RFC3339),
		})
	}
	raw, err := yaml.Marshal(map[string]any{"silences": items})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (h *AlertingHandler) renderThanosAlertmanagerEndpoints(ctx context.Context) (string, bool, error) {
	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", false, nil
		}
		return "", false, err
	}
	if strings.TrimSpace(backend.AlertmanagerUrl) == "" {
		return "", false, nil
	}
	payload := map[string]any{
		"alertmanagers": []map[string]any{{
			"static_configs": []string{backend.AlertmanagerUrl},
			"scheme":         alertmanagerScheme(backend.AlertmanagerUrl),
			"timeout":        "10s",
			"api_version":    "v2",
		}},
	}
	raw, err := yaml.Marshal(payload)
	if err != nil {
		return "", false, err
	}
	return string(raw), true, nil
}

// rulesForChannel returns the rules whose IDs are in ruleSet, preserving
// the order of allRules. ruleSet is the precomputed set of rule IDs linked
// to a given channel (see renderAlertmanagerConfig's bulk link load).
func rulesForChannel(allRules []sqlc.AlertRule, ruleSet map[uuid.UUID]bool) []sqlc.AlertRule {
	if len(ruleSet) == 0 {
		return nil
	}
	matched := make([]sqlc.AlertRule, 0, len(ruleSet))
	for _, rule := range allRules {
		if ruleSet[rule.ID] {
			matched = append(matched, rule)
		}
	}
	return matched
}

func fallbackPromExpr(rule sqlc.AlertRule, cfg map[string]any) string {
	clusterMatcher := ""
	if rule.ClusterID.Valid {
		clusterMatcher = fmt.Sprintf(`{cluster_id="%s"}`, uuid.UUID(rule.ClusterID.Bytes).String())
	}
	switch strings.ToLower(rule.RuleType) {
	case "absence", "deadman":
		return fmt.Sprintf(`absent(up%s)`, clusterMatcher)
	default:
		threshold := float64(0)
		if v := numberOrNil(cfg["threshold"]); v != nil {
			threshold, _ = v.(float64)
		}
		query := strings.ToLower(stringFromMap(cfg, "query"))
		switch {
		case strings.Contains(query, "cpu"):
			return fmt.Sprintf(`sum(rate(node_cpu_seconds_total{mode!="idle"%s}[5m])) > %.2f`, promMatcherSuffix(clusterMatcher), threshold)
		case strings.Contains(query, "memory"):
			return fmt.Sprintf(`sum(node_memory_MemTotal_bytes%s - node_memory_MemAvailable_bytes%s) > %.2f`, clusterMatcher, clusterMatcher, threshold)
		default:
			return ""
		}
	}
}

func promMatcherSuffix(matcher string) string {
	if matcher == "" {
		return ""
	}
	return "," + strings.TrimPrefix(strings.TrimSuffix(matcher, "}"), "{")
}

func sanitizePromRuleName(name string) string {
	replacer := strings.NewReplacer(" ", "_", "-", "_", ".", "_", "/", "_")
	return replacer.Replace(name)
}

func alertmanagerScheme(rawURL string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(rawURL)), "https://") {
		return "https"
	}
	return "http"
}

func firstConfigString(cfg map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		if value, ok := cfg[key].(string); ok && strings.TrimSpace(value) != "" {
			return value, true
		}
	}
	return "", false
}

func firstNonEmptyString(values ...string) string {
	return strutil.FirstNonBlank(values...)
}

func ensureNamespaceWithRequester(ctx context.Context, requester K8sRequester, clusterID, namespace string) error {
	path := fmt.Sprintf("/api/v1/namespaces/%s", namespace)
	resp, err := requester.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return err
	}
	if resp.StatusCode < http.StatusBadRequest {
		return nil
	}
	if resp.StatusCode != http.StatusNotFound {
		return ensureSuccess(resp)
	}
	body, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]any{
			"name": namespace,
		},
	})
	if err != nil {
		return err
	}
	resp, err = requester.Do(ctx, clusterID, http.MethodPost, "/api/v1/namespaces", body, requestHeaders("application/json"))
	if err != nil {
		return err
	}
	return ensureSuccess(resp)
}

func applyConfigMap(ctx context.Context, requester K8sRequester, clusterID, namespace, name string, data map[string]string) error {
	body, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
		},
		"data": data,
	})
	if err != nil {
		return err
	}
	return applyNamedResource(ctx, requester, clusterID, namespace, "configmaps", name, body)
}

func applyAlertSecret(ctx context.Context, requester K8sRequester, clusterID, namespace, name string, stringData map[string]string) error {
	body, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
		},
		"type":       "Opaque",
		"stringData": stringData,
	})
	if err != nil {
		return err
	}
	return applyNamedResource(ctx, requester, clusterID, namespace, "secrets", name, body)
}

func applyNamedResource(ctx context.Context, requester K8sRequester, clusterID, namespace, plural, name string, body []byte) error {
	patchPath := fmt.Sprintf("/api/v1/namespaces/%s/%s/%s", namespace, plural, name)
	resp, err := requester.Do(ctx, clusterID, http.MethodPatch, patchPath, body, requestHeaders("application/merge-patch+json"))
	if err == nil && resp != nil && resp.StatusCode != http.StatusNotFound {
		return ensureSuccess(resp)
	}
	createPath := fmt.Sprintf("/api/v1/namespaces/%s/%s", namespace, plural)
	resp, err = requester.Do(ctx, clusterID, http.MethodPost, createPath, body, requestHeaders("application/json"))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusConflict {
		return nil
	}
	return ensureSuccess(resp)
}

func (h *AlertingHandler) alertRuleResponse(ctx context.Context, rule sqlc.AlertRule) map[string]any {
	items := h.alertRuleResponses(ctx, []sqlc.AlertRule{rule})
	if len(items) == 0 {
		return map[string]any{}
	}
	return items[0]
}

// alertRuleResponses builds the response payloads for a page of rules with
// batched lookups: one query aggregates active-alert counts by rule_id, one
// bulk-loads rule<->channel links, and one bulk-loads the referenced
// clusters — replacing the ~3-queries-per-rule (incl. a 200-event fetch just
// to COUNT active alerts) the single-rule path used to run per rule.
func (h *AlertingHandler) alertRuleResponses(ctx context.Context, rules []sqlc.AlertRule) []map[string]any {
	ruleIDs := make([]uuid.UUID, 0, len(rules))
	clusterIDSet := map[uuid.UUID]struct{}{}
	for _, rule := range rules {
		ruleIDs = append(ruleIDs, rule.ID)
		if rule.ClusterID.Valid {
			clusterIDSet[uuid.UUID(rule.ClusterID.Bytes)] = struct{}{}
		}
	}

	activeByRule := map[uuid.UUID]int{}
	channelsByRule := map[uuid.UUID][]string{}
	if len(ruleIDs) > 0 {
		if counts, err := h.queries.CountActiveAlertsByRules(ctx, ruleIDs); err == nil {
			for _, c := range counts {
				activeByRule[c.RuleID] = int(c.ActiveCount)
			}
		}
		if links, err := h.queries.ListAlertRuleChannelsByRules(ctx, ruleIDs); err == nil {
			for _, link := range links {
				channelsByRule[link.AlertRuleID] = append(channelsByRule[link.AlertRuleID], link.NotificationChannelID.String())
			}
		}
	}

	clusterNames := map[uuid.UUID]any{}
	if len(clusterIDSet) > 0 {
		ids := make([]uuid.UUID, 0, len(clusterIDSet))
		for id := range clusterIDSet {
			ids = append(ids, id)
		}
		if clusters, err := h.queries.ListClustersByIDs(ctx, ids); err == nil {
			for _, cluster := range clusters {
				var name any = cluster.DisplayName
				if cluster.DisplayName == "" {
					name = cluster.Name
				}
				clusterNames[cluster.ID] = name
			}
		}
	}

	items := make([]map[string]any, 0, len(rules))
	for _, rule := range rules {
		channelIDs := channelsByRule[rule.ID]
		if channelIDs == nil {
			channelIDs = []string{}
		}
		var clusterName any = nil
		if rule.ClusterID.Valid {
			if n, ok := clusterNames[uuid.UUID(rule.ClusterID.Bytes)]; ok {
				clusterName = n
			}
		}
		items = append(items, alertRuleResponseFields(rule, activeByRule[rule.ID], clusterName, channelIDs))
	}
	return items
}

func alertRuleResponseFields(rule sqlc.AlertRule, activeAlerts int, clusterName any, channelIDs []string) map[string]any {
	cfg := decodeJSONMap(rule.Configuration)
	return map[string]any{
		"id":                     rule.ID.String(),
		"name":                   rule.Name,
		"description":            stringFromMap(cfg, "description"),
		"type":                   defaultString(stringFromMap(cfg, "type"), rule.RuleType),
		"severity":               rule.Severity,
		"clusterId":              nullableUUID(rule.ClusterID),
		"clusterName":            clusterName,
		"namespace":              stringFromMap(cfg, "namespace"),
		"enabled":                rule.Enabled,
		"query":                  stringFromMap(cfg, "query"),
		"threshold":              numberOrNil(cfg["threshold"]),
		"duration":               defaultString(stringFromMap(cfg, "duration"), "5m"),
		"activeAlerts":           activeAlerts,
		"labels":                 mapStringMap(cfg["labels"]),
		"annotations":            mapStringMap(cfg["annotations"]),
		"notificationChannelIds": channelIDs,
		// Sprint 072 anomaly-rule surface. ruleKind defaults to
		// "threshold" so the frontend can branch its rule-edit
		// form without a follow-up GET.
		"ruleKind":             defaultString(stringFromMap(cfg, "rule_kind"), "threshold"),
		"metric":               stringFromMap(cfg, "metric"),
		"anomalyStddev":        numberOrNil(cfg["anomaly_stddev"]),
		"anomalyWindowSeconds": numberOrNil(cfg["anomaly_window_seconds"]),
		"anomalyMinSamples":    numberOrNil(cfg["anomaly_min_samples"]),
		"anomalyDirection":     defaultString(stringFromMap(cfg, "anomaly_direction"), ""),
		"createdAt":            rule.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt":            rule.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func notificationChannelResponse(channel sqlc.NotificationChannel) map[string]any {
	return map[string]any{
		"id":        channel.ID.String(),
		"name":      channel.Name,
		"type":      channel.ChannelType,
		"enabled":   channel.Enabled,
		"config":    redactChannelConfig(decodeJSONMap(channel.Configuration)),
		"createdAt": channel.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt": channel.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// redactChannelConfig masks delivery secrets before a notification-channel
// config is returned on any read path. A channel config *is* the credential
// (Slack webhook URL, PagerDuty routing key, generic webhook token), so any
// value under a secret-shaped key is replaced with a marker while the key is
// preserved so the UI can still tell the channel is configured. Callers who
// need the real value must re-enter it on update (write-only secret pattern).
func redactChannelConfig(cfg map[string]any) map[string]any {
	if cfg == nil {
		return cfg
	}
	out := make(map[string]any, len(cfg))
	for k, v := range cfg {
		if channelSecretKey(k) {
			if v == nil || v == "" {
				out[k] = v
			} else {
				out[k] = "[redacted]"
			}
			continue
		}
		if nested, ok := v.(map[string]any); ok {
			out[k] = redactChannelConfig(nested)
			continue
		}
		out[k] = v
	}
	return out
}

func channelSecretKey(key string) bool {
	n := strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.ToLower(key))
	for _, s := range []string{"url", "token", "key", "secret", "password", "webhook", "credential"} {
		if strings.Contains(n, s) {
			return true
		}
	}
	return false
}

func (h *AlertingHandler) alertEventResponse(ctx context.Context, event sqlc.AlertEvent) map[string]any {
	ruleName := ""
	severity := "warning"
	clusterName := any(nil)
	if rule, err := h.queries.GetAlertRuleByID(ctx, event.RuleID); err == nil {
		ruleName = rule.Name
		severity = rule.Severity
	}
	if event.ClusterID.Valid {
		if cluster, err := h.queries.GetClusterByID(ctx, uuid.UUID(event.ClusterID.Bytes)); err == nil {
			clusterName = cluster.DisplayName
			if clusterName == "" {
				clusterName = cluster.Name
			}
		}
	}
	return alertEventResponseFields(event, ruleName, severity, clusterName)
}

// alertEventRefLoader is the narrow batch surface alertEventResponsesBatched
// needs. AlertingQuerier satisfies it; a test can supply a counting fake.
type alertEventRefLoader interface {
	ListAlertRulesByIDs(ctx context.Context, ids []uuid.UUID) ([]sqlc.AlertRule, error)
	ListClustersByIDs(ctx context.Context, ids []uuid.UUID) ([]sqlc.Cluster, error)
}

// alertEventResponsesBatched builds the response payloads for a page of events
// with batched lookups: one ListAlertRulesByIDs + one ListClustersByIDs for the
// whole page, mirroring alertRuleResponses. Replaces the per-row
// GetAlertRuleByID + GetClusterByID N+1 (~2 queries per event) the event-list
// path used to run.
func alertEventResponsesBatched(ctx context.Context, q alertEventRefLoader, events []sqlc.AlertEvent) []map[string]any {
	ruleIDSet := map[uuid.UUID]struct{}{}
	clusterIDSet := map[uuid.UUID]struct{}{}
	for _, event := range events {
		ruleIDSet[event.RuleID] = struct{}{}
		if event.ClusterID.Valid {
			clusterIDSet[uuid.UUID(event.ClusterID.Bytes)] = struct{}{}
		}
	}

	type ruleInfo struct {
		name     string
		severity string
	}
	rulesByID := map[uuid.UUID]ruleInfo{}
	if len(ruleIDSet) > 0 {
		ids := make([]uuid.UUID, 0, len(ruleIDSet))
		for id := range ruleIDSet {
			ids = append(ids, id)
		}
		if rules, err := q.ListAlertRulesByIDs(ctx, ids); err == nil {
			for _, rule := range rules {
				rulesByID[rule.ID] = ruleInfo{name: rule.Name, severity: rule.Severity}
			}
		}
	}

	clusterNames := map[uuid.UUID]any{}
	if len(clusterIDSet) > 0 {
		ids := make([]uuid.UUID, 0, len(clusterIDSet))
		for id := range clusterIDSet {
			ids = append(ids, id)
		}
		if clusters, err := q.ListClustersByIDs(ctx, ids); err == nil {
			for _, cluster := range clusters {
				var name any = cluster.DisplayName
				if cluster.DisplayName == "" {
					name = cluster.Name
				}
				clusterNames[cluster.ID] = name
			}
		}
	}

	items := make([]map[string]any, 0, len(events))
	for _, event := range events {
		ruleName := ""
		severity := "warning"
		if info, ok := rulesByID[event.RuleID]; ok {
			ruleName = info.name
			severity = info.severity
		}
		var clusterName any = nil
		if event.ClusterID.Valid {
			if n, ok := clusterNames[uuid.UUID(event.ClusterID.Bytes)]; ok {
				clusterName = n
			}
		}
		items = append(items, alertEventResponseFields(event, ruleName, severity, clusterName))
	}
	return items
}

// alertEventResponseFields renders one event into its wire shape from already
// resolved rule/cluster metadata. Shared by the single-event path and the
// batched list path so both stay byte-identical.
func alertEventResponseFields(event sqlc.AlertEvent, ruleName, severity string, clusterName any) map[string]any {
	details := decodeJSONMap(event.Details)
	resp := map[string]any{
		"id":             event.ID.String(),
		"ruleId":         event.RuleID.String(),
		"ruleName":       ruleName,
		"severity":       severity,
		"status":         event.Status,
		"message":        event.Message,
		"clusterId":      nullableUUID(event.ClusterID),
		"clusterName":    clusterName,
		"namespace":      stringFromMap(details, "namespace"),
		"resource":       stringFromMap(details, "resource"),
		"labels":         mapStringMap(details["labels"]),
		"firedAt":        event.FiredAt.UTC().Format(time.RFC3339),
		"acknowledgedAt": nullableTime(event.AcknowledgedAt),
		"acknowledgedBy": nullableUUID(event.AcknowledgedByID),
		"resolvedAt":     nullableTime(event.ResolvedAt),
		"resolvedBy":     nil,
	}
	return resp
}

func alertSilenceResponse(silence sqlc.AlertSilence) map[string]any {
	return map[string]any{
		"id":        silence.ID.String(),
		"reason":    silence.Reason,
		"matchers":  map[string]string{"cluster_id": nullableUUIDString(silence.ClusterID), "rule_id": nullableUUIDString(silence.RuleID)},
		"startsAt":  silence.StartsAt.UTC().Format(time.RFC3339),
		"endsAt":    silence.EndsAt.UTC().Format(time.RFC3339),
		"duration":  silence.EndsAt.Sub(silence.StartsAt).String(),
		"createdBy": nullableUUID(silence.CreatedByID),
		"createdAt": silence.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func parseMatcherUUID(matchers map[string]string, keys ...string) *uuid.UUID {
	for _, key := range keys {
		if matchers == nil {
			return nil
		}
		value := strings.TrimSpace(matchers[key])
		if value == "" {
			continue
		}
		if id, err := uuid.Parse(value); err == nil {
			return &id
		}
	}
	return nil
}

func alertRuleConfiguration(req CreateAlertRuleRequest) json.RawMessage {
	cfg := map[string]any{
		"description": req.Description,
		"type":        defaultString(req.Type, req.RuleType),
		"query":       req.Query,
		"duration":    defaultString(req.Duration, "5m"),
		"labels":      req.Labels,
		"annotations": req.Annotations,
	}
	if req.Threshold != nil {
		cfg["threshold"] = *req.Threshold
	}
	applyAnomalyFieldsToConfig(cfg, req)
	data, _ := json.Marshal(cfg)
	return data
}

func alertRuleConfigurationWithFallback(req CreateAlertRuleRequest, current json.RawMessage) json.RawMessage {
	cfg := decodeJSONMap(current)
	if req.Description != "" {
		cfg["description"] = req.Description
	}
	if req.Type != "" || req.RuleType != "" {
		cfg["type"] = defaultString(req.Type, req.RuleType)
	}
	if req.Query != "" {
		cfg["query"] = req.Query
	}
	if req.Duration != "" {
		cfg["duration"] = req.Duration
	}
	if req.Threshold != nil {
		cfg["threshold"] = *req.Threshold
	}
	if req.Labels != nil {
		cfg["labels"] = req.Labels
	}
	if req.Annotations != nil {
		cfg["annotations"] = req.Annotations
	}
	applyAnomalyFieldsToConfig(cfg, req)
	data, _ := json.Marshal(cfg)
	return data
}

// applyAnomalyFieldsToConfig stamps the sprint 072 anomaly-rule
// fields into the rule's configuration JSONB.
//
// We store these in the configuration blob (rather than only in the
// dedicated alert_rules columns) so the alert evaluator can read them
// without an additional query — the existing AlertRule sqlc struct
// is unmodified and the evaluator already decodes the configuration
// on every tick.
//
// Defaults: anomaly_stddev=3, anomaly_direction=above,
// anomaly_min_samples=50, anomaly_window_seconds=86400 (24h). These
// match the migration column defaults so the two stay in sync.
func applyAnomalyFieldsToConfig(cfg map[string]any, req CreateAlertRuleRequest) {
	if req.RuleKind != "" {
		cfg["rule_kind"] = req.RuleKind
	}
	if req.Metric != "" {
		cfg["metric"] = req.Metric
	}
	if req.AnomalyStddev != nil {
		cfg["anomaly_stddev"] = *req.AnomalyStddev
	}
	if req.AnomalyWindowSeconds != nil {
		cfg["anomaly_window_seconds"] = *req.AnomalyWindowSeconds
	}
	if req.AnomalyMinSamples != nil {
		cfg["anomaly_min_samples"] = *req.AnomalyMinSamples
	}
	if req.AnomalyDirection != "" {
		cfg["anomaly_direction"] = req.AnomalyDirection
	}
	// On a rule-kind switch from anomaly→threshold via UpdateRule,
	// the operator clears the anomaly metadata explicitly via an
	// empty kind. We DON'T do that automatically — leaving the old
	// anomaly fields in place is harmless because the threshold
	// evaluator never reads them.
}

// validateAnomalyRuleRequest reports a validation error string if
// req declares an anomaly rule but is missing required anomaly
// fields. Returns "" when the request is valid (anomaly or not).
// Called by CreateRule before persistence; UpdateRule does not
// re-validate so a rule that loses its metric mid-update silently
// short-circuits to no-fire (preferred over a hard 400 — the rule
// stays editable).
func validateAnomalyRuleRequest(req CreateAlertRuleRequest) string {
	if req.RuleKind != "anomaly" {
		return ""
	}
	if req.Metric == "" {
		return "anomaly rule requires a metric name"
	}
	if req.AnomalyStddev != nil && *req.AnomalyStddev <= 0 {
		return "anomaly_stddev must be > 0"
	}
	if req.AnomalyWindowSeconds != nil && *req.AnomalyWindowSeconds <= 0 {
		return "anomaly_window_seconds must be > 0"
	}
	if req.AnomalyMinSamples != nil && *req.AnomalyMinSamples < 0 {
		return "anomaly_min_samples must be >= 0"
	}
	if req.AnomalyDirection != "" {
		switch req.AnomalyDirection {
		case "above", "below", "either":
		default:
			return "anomaly_direction must be one of above|below|either"
		}
	}
	return ""
}

func (h *AlertingHandler) syncRuleChannels(ctx context.Context, ruleID uuid.UUID, channelIDs []string) error {
	existing, err := h.queries.ListChannelsForAlertRule(ctx, ruleID)
	if err != nil {
		return err
	}
	existingSet := map[string]sqlc.NotificationChannel{}
	for _, channel := range existing {
		existingSet[channel.ID.String()] = channel
	}
	targetSet := map[string]struct{}{}
	for _, id := range channelIDs {
		targetSet[id] = struct{}{}
		if _, ok := existingSet[id]; ok {
			continue
		}
		parsed, err := uuid.Parse(id)
		if err != nil {
			return err
		}
		if err := h.queries.AddAlertRuleChannel(ctx, sqlc.AddAlertRuleChannelParams{
			AlertRuleID:           ruleID,
			NotificationChannelID: parsed,
		}); err != nil {
			return err
		}
	}
	for id, channel := range existingSet {
		if _, ok := targetSet[id]; ok {
			continue
		}
		if err := h.queries.RemoveAlertRuleChannel(ctx, sqlc.RemoveAlertRuleChannelParams{
			AlertRuleID:           ruleID,
			NotificationChannelID: channel.ID,
		}); err != nil {
			return err
		}
	}
	return nil
}

func decodeJSONMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}

func mapStringMap(v any) map[string]string {
	out := map[string]string{}
	raw, ok := v.(map[string]any)
	if !ok {
		return out
	}
	for k, value := range raw {
		if s, ok := value.(string); ok {
			out[k] = s
		}
	}
	return out
}

func stringFromMap(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func numberOrNil(v any) any {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return n
	default:
		return nil
	}
}

func nullableUUID(id pgtype.UUID) any {
	if id.Valid {
		return uuid.UUID(id.Bytes).String()
	}
	return nil
}

func nullableUUIDString(id pgtype.UUID) string {
	if id.Valid {
		return uuid.UUID(id.Bytes).String()
	}
	return ""
}

func nullableTime(ts pgtype.Timestamptz) any {
	if ts.Valid {
		return ts.Time.UTC().Format(time.RFC3339)
	}
	return nil
}
