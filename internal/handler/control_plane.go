package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/controlplane"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type ControlPlaneQuerier interface {
	GetDefaultControlPlanePolicy(ctx context.Context) (sqlc.ControlPlanePolicy, error)
	UpsertDefaultControlPlanePolicy(ctx context.Context, arg sqlc.UpsertDefaultControlPlanePolicyParams) (sqlc.ControlPlanePolicy, error)
	ListControlPlaneAlerts(ctx context.Context, arg sqlc.ListControlPlaneAlertsParams) ([]sqlc.ControlPlaneAlert, error)
	GetActiveControlPlaneAlert(ctx context.Context, arg sqlc.GetActiveControlPlaneAlertParams) (sqlc.ControlPlaneAlert, error)
	CreateControlPlaneAlert(ctx context.Context, arg sqlc.CreateControlPlaneAlertParams) (sqlc.ControlPlaneAlert, error)
	ResolveControlPlaneAlert(ctx context.Context, arg sqlc.ResolveControlPlaneAlertParams) (sqlc.ControlPlaneAlert, error)
	AcknowledgeControlPlaneAlert(ctx context.Context, arg sqlc.AcknowledgeControlPlaneAlertParams) (sqlc.ControlPlaneAlert, error)
	CreateControlPlaneSilence(ctx context.Context, arg sqlc.CreateControlPlaneSilenceParams) (sqlc.ControlPlaneSilence, error)
	ListControlPlaneSilences(ctx context.Context, arg sqlc.ListControlPlaneSilencesParams) ([]sqlc.ControlPlaneSilence, error)
	GetActiveControlPlaneSilences(ctx context.Context) ([]sqlc.ControlPlaneSilence, error)
	DeleteControlPlaneSilence(ctx context.Context, id uuid.UUID) (sqlc.ControlPlaneSilence, error)
	ListEnabledNotificationChannels(ctx context.Context) ([]sqlc.NotificationChannel, error)
}

type ControlPlaneMutationTx interface {
	ControlPlaneQuerier
	audit.OutboxQuerier
	tasks.TaskOutboxWriter
}

type controlPlaneRunTxFunc func(context.Context, func(ControlPlaneMutationTx) error) error

type ControlPlaneHandler struct {
	queries    ControlPlaneQuerier
	service    *controlplane.Service
	runTx      controlPlaneRunTxFunc
	mu         sync.Mutex
	evaluateCh chan struct{}
}

func (h *ControlPlaneHandler) SetRunTx(runTx controlPlaneRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *ControlPlaneHandler) TransactionalAuditWired() bool { return h != nil && h.runTx != nil }

// openapi:request ControlPlanePolicyRequest
type UpdateControlPlanePolicyRequest struct {
	MonitoringQueueDepthThreshold    int32 `json:"monitoringQueueDepthThreshold"`
	DeliveryQueueDepthThreshold      int32 `json:"deliveryQueueDepthThreshold"`
	ToolsQueueDepthThreshold         int32 `json:"toolsQueueDepthThreshold"`
	CatalogQueueDepthThreshold       int32 `json:"catalogQueueDepthThreshold"`
	MonitoringStaleRunningThreshold  int32 `json:"monitoringStaleRunningThreshold"`
	DeliveryStaleRunningThreshold    int32 `json:"deliveryStaleRunningThreshold"`
	ToolsStaleRunningThreshold       int32 `json:"toolsStaleRunningThreshold"`
	CatalogStaleRunningThreshold     int32 `json:"catalogStaleRunningThreshold"`
	MonitoringRecentFailureThreshold int32 `json:"monitoringRecentFailureThreshold"`
	DeliveryRecentFailureThreshold   int32 `json:"deliveryRecentFailureThreshold"`
	ToolsRecentFailureThreshold      int32 `json:"toolsRecentFailureThreshold"`
	CatalogRecentFailureThreshold    int32 `json:"catalogRecentFailureThreshold"`
	RecentFailureWindowMinutes       int32 `json:"recentFailureWindowMinutes"`
}

// openapi:request ControlPlaneSilenceRequest
type CreateControlPlaneSilenceRequest struct {
	Controller    string `json:"controller" validate:"required"`
	ConditionType string `json:"conditionType"`
	Reason        string `json:"reason" validate:"required"`
	Duration      string `json:"duration"`
}

func NewControlPlaneHandler(queries ControlPlaneQuerier, service *controlplane.Service) *ControlPlaneHandler {
	if service == nil {
		service = controlplane.NewService(nil)
	}
	return &ControlPlaneHandler{
		queries:    queries,
		service:    service,
		evaluateCh: make(chan struct{}, 1),
	}
}

func (h *ControlPlaneHandler) StartEvaluator(ctx context.Context) {
	if h == nil || h.queries == nil {
		return
	}
	go h.RunEvaluator(ctx)
}

func (h *ControlPlaneHandler) RunEvaluator(ctx context.Context) {
	if h == nil || h.queries == nil {
		return
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	h.evaluate(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.evaluate(ctx)
		case <-h.evaluateCh:
			h.evaluate(ctx)
		}
	}
}

func (h *ControlPlaneHandler) Status(w http.ResponseWriter, r *http.Request) {
	policy, _ := h.queries.GetDefaultControlPlanePolicy(r.Context())
	snapshot := h.service.Snapshot(r.Context(), controlPlaneDomainPolicy(policy))
	out := make(map[string]any, len(snapshot.Controllers)+2)
	for name, summary := range snapshot.Controllers {
		out[name] = summary
	}
	out["policy"] = controlPlanePolicyResponse(policy)
	out["summary"] = snapshot.Aggregate
	RespondJSON(w, http.StatusOK, out)
}

func (h *ControlPlaneHandler) GetPolicy(w http.ResponseWriter, r *http.Request) {
	policy, err := h.queries.GetDefaultControlPlanePolicy(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.PolicyError, "Failed to load control plane policy")
		return
	}
	RespondJSON(w, http.StatusOK, controlPlanePolicyResponse(policy))
}

func (h *ControlPlaneHandler) UpdatePolicy(w http.ResponseWriter, r *http.Request) {
	var req UpdateControlPlanePolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	params := sqlc.UpsertDefaultControlPlanePolicyParams{
		MonitoringQueueDepthThreshold:    atLeastOne(req.MonitoringQueueDepthThreshold),
		DeliveryQueueDepthThreshold:      atLeastOne(req.DeliveryQueueDepthThreshold),
		ToolsQueueDepthThreshold:         atLeastOne(req.ToolsQueueDepthThreshold),
		CatalogQueueDepthThreshold:       atLeastOne(req.CatalogQueueDepthThreshold),
		MonitoringStaleRunningThreshold:  atLeastOne(req.MonitoringStaleRunningThreshold),
		DeliveryStaleRunningThreshold:    atLeastOne(req.DeliveryStaleRunningThreshold),
		ToolsStaleRunningThreshold:       atLeastOne(req.ToolsStaleRunningThreshold),
		CatalogStaleRunningThreshold:     atLeastOne(req.CatalogStaleRunningThreshold),
		MonitoringRecentFailureThreshold: atLeastOne(req.MonitoringRecentFailureThreshold),
		DeliveryRecentFailureThreshold:   atLeastOne(req.DeliveryRecentFailureThreshold),
		ToolsRecentFailureThreshold:      atLeastOne(req.ToolsRecentFailureThreshold),
		CatalogRecentFailureThreshold:    atLeastOne(req.CatalogRecentFailureThreshold),
		RecentFailureWindowMinutes:       atLeastOne(req.RecentFailureWindowMinutes),
	}
	policy, err := executeMutation(r, h.runTx,
		func(q ControlPlaneMutationTx) (sqlc.ControlPlanePolicy, error) {
			return q.UpsertDefaultControlPlanePolicy(r.Context(), params)
		},
		func(policy sqlc.ControlPlanePolicy) mutationAuditEvent {
			return mutationAuditEvent{
				action: "controlplane.policy.update", resourceType: "control_plane_policy", resourceID: policy.ID.String(), status: http.StatusOK,
				detail: map[string]any{
					"monitoring_queue_depth_threshold": policy.MonitoringQueueDepthThreshold,
					"delivery_queue_depth_threshold":   policy.DeliveryQueueDepthThreshold,
					"tools_queue_depth_threshold":      policy.ToolsQueueDepthThreshold,
					"catalog_queue_depth_threshold":    policy.CatalogQueueDepthThreshold,
					"recent_failure_window_minutes":    policy.RecentFailureWindowMinutes,
				},
			}
		},
	)
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.PolicyError, "Failed to update control plane policy")
		return
	}
	select {
	case h.evaluateCh <- struct{}{}:
	default:
	}
	RespondJSON(w, http.StatusOK, controlPlanePolicyResponse(policy))
}

func (h *ControlPlaneHandler) ListAlerts(w http.ResponseWriter, r *http.Request) {
	arg := sqlc.ListControlPlaneAlertsParams{
		Limit:  int32(queryLimit(r, 50)),
		Offset: int32(queryOffset(r)),
	}
	if status := r.URL.Query().Get("status"); status != "" {
		arg.Status = pgtype.Text{String: status, Valid: true}
	}
	if controller := r.URL.Query().Get("controller"); controller != "" {
		arg.Controller = pgtype.Text{String: controller, Valid: true}
	}
	alerts, err := h.queries.ListControlPlaneAlerts(r.Context(), arg)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.AlertError, "Failed to list control plane alerts")
		return
	}
	resp := make([]map[string]any, 0, len(alerts))
	for _, alert := range alerts {
		resp = append(resp, controlPlaneAlertResponse(alert))
	}
	paging.Write(w, resp, paging.FromPage(int(arg.Limit), int(arg.Offset), len(resp)))
}

func (h *ControlPlaneHandler) AcknowledgeAlert(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid alert ID")
		return
	}
	alert, err := executeMutation(r, h.runTx,
		func(q ControlPlaneMutationTx) (sqlc.ControlPlaneAlert, error) {
			return q.AcknowledgeControlPlaneAlert(r.Context(), sqlc.AcknowledgeControlPlaneAlertParams{ID: id, AcknowledgedByID: currentUserUUID(r)})
		},
		func(alert sqlc.ControlPlaneAlert) mutationAuditEvent {
			return mutationAuditEvent{
				action: "controlplane.alert.acknowledge", resourceType: "control_plane_alert", resourceID: id.String(), resourceName: alert.Controller, status: http.StatusOK,
				detail: map[string]any{"condition_type": alert.ConditionType, "status": alert.Status},
			}
		},
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Control plane alert not found")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.AlertError, "Failed to acknowledge control plane alert")
		return
	}
	RespondJSON(w, http.StatusOK, controlPlaneAlertResponse(alert))
}

func (h *ControlPlaneHandler) ListSilences(w http.ResponseWriter, r *http.Request) {
	items, err := h.queries.ListControlPlaneSilences(r.Context(), sqlc.ListControlPlaneSilencesParams{
		Limit:  int32(queryLimit(r, 50)),
		Offset: int32(queryOffset(r)),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.SilenceError, "Failed to list control plane silences")
		return
	}
	resp := make([]map[string]any, 0, len(items))
	for _, item := range items {
		resp = append(resp, controlPlaneSilenceResponse(item))
	}
	paging.Write(w, resp, paging.FromPage(queryLimit(r, 50), queryOffset(r), len(resp)))
}

func (h *ControlPlaneHandler) CreateSilence(w http.ResponseWriter, r *http.Request) {
	var req CreateControlPlaneSilenceRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	duration := time.Hour
	if req.Duration != "" {
		if parsed, err := time.ParseDuration(req.Duration); err == nil && parsed > 0 {
			duration = parsed
		}
	}
	params := sqlc.CreateControlPlaneSilenceParams{
		Controller:    req.Controller,
		ConditionType: req.ConditionType,
		Reason:        req.Reason,
		StartsAt:      time.Now().UTC(),
		EndsAt:        time.Now().UTC().Add(duration),
		CreatedByID:   currentUserUUID(r),
	}
	item, err := executeMutation(r, h.runTx,
		func(q ControlPlaneMutationTx) (sqlc.ControlPlaneSilence, error) {
			return q.CreateControlPlaneSilence(r.Context(), params)
		},
		func(item sqlc.ControlPlaneSilence) mutationAuditEvent {
			return mutationAuditEvent{
				action: "controlplane.silence.create", resourceType: "control_plane_silence", resourceID: item.ID.String(), resourceName: item.Controller, status: http.StatusCreated,
				detail: map[string]any{"controller": item.Controller, "condition_type": item.ConditionType, "duration": duration.String()},
			}
		},
	)
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.SilenceError, "Failed to create control plane silence")
		return
	}
	w.Header().Set("Location", "/api/v1/controllers/silences/"+item.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, controlPlaneSilenceResponse(item))
}

func (h *ControlPlaneHandler) DeleteSilence(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid silence ID")
		return
	}
	_, err = executeMutation(r, h.runTx,
		func(q ControlPlaneMutationTx) (sqlc.ControlPlaneSilence, error) {
			return q.DeleteControlPlaneSilence(r.Context(), id)
		},
		func(item sqlc.ControlPlaneSilence) mutationAuditEvent {
			return mutationAuditEvent{
				action: "controlplane.silence.delete", resourceType: "control_plane_silence", resourceID: item.ID.String(), resourceName: item.Controller, status: http.StatusNoContent,
				detail: map[string]any{"condition_type": item.ConditionType},
			}
		},
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Control plane silence not found")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.SilenceError, "Failed to delete control plane silence")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *ControlPlaneHandler) evaluate(ctx context.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	policy, err := h.queries.GetDefaultControlPlanePolicy(ctx)
	if err != nil {
		return
	}
	silences, _ := h.queries.GetActiveControlPlaneSilences(ctx)
	for _, evaluated := range h.service.Snapshot(ctx, controlPlaneDomainPolicy(policy)).Evaluations {
		if !evaluated.Managed {
			continue
		}
		h.reconcileAlert(ctx, evaluated.Controller, "queue_depth", evaluated.QueueDepthExceeded, evaluated.Summary, silences)
		h.reconcileAlert(ctx, evaluated.Controller, "stale_running", evaluated.StaleRunningExceeded, evaluated.Summary, silences)
		h.reconcileAlert(ctx, evaluated.Controller, "recent_failures", evaluated.RecentFailuresExceeded, evaluated.Summary, silences)
	}
}

func controlPlaneDomainPolicy(policy sqlc.ControlPlanePolicy) controlplane.Policy {
	return controlplane.Policy{
		RecentFailureWindowMinutes: policy.RecentFailureWindowMinutes,
		Controllers: map[string]controlplane.Thresholds{
			"monitoring": {QueueDepth: policy.MonitoringQueueDepthThreshold, StaleRunning: policy.MonitoringStaleRunningThreshold, RecentFailure: policy.MonitoringRecentFailureThreshold},
			"delivery":   {QueueDepth: policy.DeliveryQueueDepthThreshold, StaleRunning: policy.DeliveryStaleRunningThreshold, RecentFailure: policy.DeliveryRecentFailureThreshold},
			"tools":      {QueueDepth: policy.ToolsQueueDepthThreshold, StaleRunning: policy.ToolsStaleRunningThreshold, RecentFailure: policy.ToolsRecentFailureThreshold},
			"catalog":    {QueueDepth: policy.CatalogQueueDepthThreshold, StaleRunning: policy.CatalogStaleRunningThreshold, RecentFailure: policy.CatalogRecentFailureThreshold},
		},
	}
}

func (h *ControlPlaneHandler) reconcileAlert(ctx context.Context, controller, condition string, active bool, summary map[string]any, silences []sqlc.ControlPlaneSilence) {
	existing, err := h.queries.GetActiveControlPlaneAlert(ctx, sqlc.GetActiveControlPlaneAlertParams{
		Controller:    controller,
		ConditionType: condition,
	})
	if active {
		if err == nil {
			return
		}
		raw, _ := json.Marshal(summary)
		if h.runTx == nil {
			return
		}
		if txErr := h.runTx(ctx, func(q ControlPlaneMutationTx) error {
			alert, createErr := q.CreateControlPlaneAlert(ctx, sqlc.CreateControlPlaneAlertParams{
				Controller: controller, ConditionType: condition, Status: "active",
				Message: controller + " controller degraded: " + condition, Detail: raw,
			})
			if createErr != nil || isSilenced(controller, condition, silences) {
				return createErr
			}
			return enqueueControlPlaneNotifications(ctx, q, alert)
		}); txErr != nil {
			slog.ErrorContext(ctx, "control-plane alert transaction failed",
				"controller", controller, "condition", condition, "error", txErr)
		}
		return
	}
	if err == nil {
		raw, _ := json.Marshal(summary)
		_, _ = h.queries.ResolveControlPlaneAlert(ctx, sqlc.ResolveControlPlaneAlertParams{
			ID:     existing.ID,
			Detail: raw,
		})
	}
}

func enqueueControlPlaneNotifications(ctx context.Context, q ControlPlaneMutationTx, alert sqlc.ControlPlaneAlert) error {
	channels, err := q.ListEnabledNotificationChannels(ctx)
	if err != nil {
		return err
	}
	for _, channel := range channels {
		recipients := controlPlaneChannelRecipients(channel)
		if len(recipients) == 0 {
			continue
		}
		deliveryID := "control-plane-alert:" + alert.ID.String() + ":" + channel.ID.String()
		if err := tasks.EnqueueNotificationOutbox(ctx, q, tasks.NotificationSendPayload{
			Channel:    channel.ChannelType,
			Subject:    "Control plane alert: " + alert.Controller + " " + alert.ConditionType,
			Body:       alert.Message,
			Recipients: recipients,
			Severity:   alert.Status,
			ChannelID:  channel.ID.String(),
			EventID:    alert.ID.String(),
			DeliveryID: deliveryID,
			FiredAt:    alert.FiredAt.UTC().Format(time.RFC3339),
		}, deliveryID); err != nil {
			return err
		}
	}
	return nil
}

func controlPlaneChannelRecipients(channel sqlc.NotificationChannel) []string {
	cfg := decodeJSONMap(channel.Configuration)
	if value, ok := firstConfigString(cfg, "url", "webhook_url"); ok {
		return []string{value}
	}
	if value, ok := firstConfigString(cfg, "email", "address"); ok {
		return []string{value}
	}
	return nil
}

func isSilenced(controller, condition string, silences []sqlc.ControlPlaneSilence) bool {
	for _, silence := range silences {
		if silence.Controller != controller {
			continue
		}
		if silence.ConditionType == "" || silence.ConditionType == condition {
			return true
		}
	}
	return false
}

func controlPlanePolicyResponse(policy sqlc.ControlPlanePolicy) map[string]any {
	return map[string]any{
		"monitoringQueueDepthThreshold":    policy.MonitoringQueueDepthThreshold,
		"deliveryQueueDepthThreshold":      policy.DeliveryQueueDepthThreshold,
		"toolsQueueDepthThreshold":         policy.ToolsQueueDepthThreshold,
		"catalogQueueDepthThreshold":       policy.CatalogQueueDepthThreshold,
		"monitoringStaleRunningThreshold":  policy.MonitoringStaleRunningThreshold,
		"deliveryStaleRunningThreshold":    policy.DeliveryStaleRunningThreshold,
		"toolsStaleRunningThreshold":       policy.ToolsStaleRunningThreshold,
		"catalogStaleRunningThreshold":     policy.CatalogStaleRunningThreshold,
		"monitoringRecentFailureThreshold": policy.MonitoringRecentFailureThreshold,
		"deliveryRecentFailureThreshold":   policy.DeliveryRecentFailureThreshold,
		"toolsRecentFailureThreshold":      policy.ToolsRecentFailureThreshold,
		"catalogRecentFailureThreshold":    policy.CatalogRecentFailureThreshold,
		"recentFailureWindowMinutes":       policy.RecentFailureWindowMinutes,
	}
}

func controlPlaneAlertResponse(alert sqlc.ControlPlaneAlert) map[string]any {
	return map[string]any{
		"id":             alert.ID.String(),
		"controller":     alert.Controller,
		"conditionType":  alert.ConditionType,
		"status":         alert.Status,
		"message":        alert.Message,
		"detail":         decodeJSONMap(alert.Detail),
		"firedAt":        alert.FiredAt.UTC().Format(time.RFC3339),
		"resolvedAt":     nullablePgTime(alert.ResolvedAt),
		"acknowledgedAt": nullablePgTime(alert.AcknowledgedAt),
		"acknowledgedBy": nullableUUID(alert.AcknowledgedByID),
		"createdAt":      alert.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt":      alert.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func controlPlaneSilenceResponse(item sqlc.ControlPlaneSilence) map[string]any {
	return map[string]any{
		"id":            item.ID.String(),
		"controller":    item.Controller,
		"conditionType": item.ConditionType,
		"reason":        item.Reason,
		"startsAt":      item.StartsAt.UTC().Format(time.RFC3339),
		"endsAt":        item.EndsAt.UTC().Format(time.RFC3339),
		"createdBy":     nullableUUID(item.CreatedByID),
		"createdAt":     item.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func atLeastOne(v int32) int32 {
	if v < 1 {
		return 1
	}
	return v
}
