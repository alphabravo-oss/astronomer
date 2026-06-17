package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
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
	DeleteControlPlaneSilence(ctx context.Context, id uuid.UUID) error
	ListEnabledNotificationChannels(ctx context.Context) ([]sqlc.NotificationChannel, error)
}

type ControlPlaneHandler struct {
	queries    ControlPlaneQuerier
	Monitoring *MonitoringHandler
	ArgoCD     *ArgoCDHandler
	Tools      *ToolHandler
	Catalog    *CatalogHandler
	Backups    *BackupHandler
	Logging    *LoggingHandler
	Security   *SecurityHandler
	queue      *asynq.Client
	emails     EmailNotifier
	mu         sync.Mutex
}

// SetEmailNotifier attaches the SMTP email enqueuer used by the
// alert-fired dispatch path to render and persist email_messages
// rows for every email-class notification channel that fires. The
// existing webhook/slack dispatch (via asynq notification:send) is
// unaffected.
func (h *ControlPlaneHandler) SetEmailNotifier(n EmailNotifier) { h.emails = n }

type UpdateControlPlanePolicyRequest struct {
	MonitoringQueueDepthThreshold    int32 `json:"monitoringQueueDepthThreshold"`
	ArgoCDQueueDepthThreshold        int32 `json:"argocdQueueDepthThreshold"`
	ToolsQueueDepthThreshold         int32 `json:"toolsQueueDepthThreshold"`
	CatalogQueueDepthThreshold       int32 `json:"catalogQueueDepthThreshold"`
	MonitoringStaleRunningThreshold  int32 `json:"monitoringStaleRunningThreshold"`
	ArgoCDStaleRunningThreshold      int32 `json:"argocdStaleRunningThreshold"`
	ToolsStaleRunningThreshold       int32 `json:"toolsStaleRunningThreshold"`
	CatalogStaleRunningThreshold     int32 `json:"catalogStaleRunningThreshold"`
	MonitoringRecentFailureThreshold int32 `json:"monitoringRecentFailureThreshold"`
	ArgoCDRecentFailureThreshold     int32 `json:"argocdRecentFailureThreshold"`
	ToolsRecentFailureThreshold      int32 `json:"toolsRecentFailureThreshold"`
	CatalogRecentFailureThreshold    int32 `json:"catalogRecentFailureThreshold"`
	RecentFailureWindowMinutes       int32 `json:"recentFailureWindowMinutes"`
}

type CreateControlPlaneSilenceRequest struct {
	Controller    string `json:"controller"`
	ConditionType string `json:"conditionType"`
	Reason        string `json:"reason"`
	Duration      string `json:"duration"`
}

func NewControlPlaneHandler(queries ControlPlaneQuerier, monitoring *MonitoringHandler, argocd *ArgoCDHandler, tools *ToolHandler, catalog *CatalogHandler, backups *BackupHandler, logging *LoggingHandler, security *SecurityHandler, queue *asynq.Client) *ControlPlaneHandler {
	return &ControlPlaneHandler{
		queries:    queries,
		Monitoring: monitoring,
		ArgoCD:     argocd,
		Tools:      tools,
		Catalog:    catalog,
		Backups:    backups,
		Logging:    logging,
		Security:   security,
		queue:      queue,
	}
}

func (h *ControlPlaneHandler) StartEvaluator(ctx context.Context) {
	if h == nil || h.queries == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		h.evaluate(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.evaluate(ctx)
			}
		}
	}()
}

func (h *ControlPlaneHandler) Status(w http.ResponseWriter, r *http.Request) {
	policy, _ := h.queries.GetDefaultControlPlanePolicy(r.Context())
	out, summary := h.statusPayload(r.Context(), policy)
	out["policy"] = controlPlanePolicyResponse(policy)
	out["summary"] = summary
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
	policy, err := h.queries.UpsertDefaultControlPlanePolicy(r.Context(), sqlc.UpsertDefaultControlPlanePolicyParams{
		MonitoringQueueDepthThreshold:    atLeastOne(req.MonitoringQueueDepthThreshold),
		ArgocdQueueDepthThreshold:        atLeastOne(req.ArgoCDQueueDepthThreshold),
		ToolsQueueDepthThreshold:         atLeastOne(req.ToolsQueueDepthThreshold),
		CatalogQueueDepthThreshold:       atLeastOne(req.CatalogQueueDepthThreshold),
		MonitoringStaleRunningThreshold:  atLeastOne(req.MonitoringStaleRunningThreshold),
		ArgocdStaleRunningThreshold:      atLeastOne(req.ArgoCDStaleRunningThreshold),
		ToolsStaleRunningThreshold:       atLeastOne(req.ToolsStaleRunningThreshold),
		CatalogStaleRunningThreshold:     atLeastOne(req.CatalogStaleRunningThreshold),
		MonitoringRecentFailureThreshold: atLeastOne(req.MonitoringRecentFailureThreshold),
		ArgocdRecentFailureThreshold:     atLeastOne(req.ArgoCDRecentFailureThreshold),
		ToolsRecentFailureThreshold:      atLeastOne(req.ToolsRecentFailureThreshold),
		CatalogRecentFailureThreshold:    atLeastOne(req.CatalogRecentFailureThreshold),
		RecentFailureWindowMinutes:       atLeastOne(req.RecentFailureWindowMinutes),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.PolicyError, "Failed to update control plane policy")
		return
	}
	go h.evaluate(context.Background())
	recordAudit(r, h.queries, "controlplane.policy.update", "control_plane_policy", policy.ID.String(), "", map[string]any{
		"monitoring_queue_depth_threshold": policy.MonitoringQueueDepthThreshold,
		"argocd_queue_depth_threshold":     policy.ArgocdQueueDepthThreshold,
		"tools_queue_depth_threshold":      policy.ToolsQueueDepthThreshold,
		"catalog_queue_depth_threshold":    policy.CatalogQueueDepthThreshold,
		"recent_failure_window_minutes":    policy.RecentFailureWindowMinutes,
	})
	RespondJSON(w, http.StatusOK, controlPlanePolicyResponse(policy))
}

func (h *ControlPlaneHandler) ListAlerts(w http.ResponseWriter, r *http.Request) {
	arg := sqlc.ListControlPlaneAlertsParams{
		Limit:  int32(queryInt(r, "limit", 50)),
		Offset: int32(queryInt(r, "offset", 0)),
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
	RespondJSON(w, http.StatusOK, map[string]any{"data": resp, "limit": arg.Limit, "offset": arg.Offset})
}

func (h *ControlPlaneHandler) AcknowledgeAlert(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid alert ID")
		return
	}
	alert, err := h.queries.AcknowledgeControlPlaneAlert(r.Context(), sqlc.AcknowledgeControlPlaneAlertParams{
		ID:               id,
		AcknowledgedByID: currentUserUUID(r),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Control plane alert not found")
		return
	}
	recordAudit(r, h.queries, "controlplane.alert.acknowledge", "control_plane_alert", id.String(), alert.Controller, map[string]any{
		"condition_type": alert.ConditionType,
		"status":         alert.Status,
	})
	RespondJSON(w, http.StatusOK, controlPlaneAlertResponse(alert))
}

func (h *ControlPlaneHandler) ListSilences(w http.ResponseWriter, r *http.Request) {
	items, err := h.queries.ListControlPlaneSilences(r.Context(), sqlc.ListControlPlaneSilencesParams{
		Limit:  int32(queryInt(r, "limit", 50)),
		Offset: int32(queryInt(r, "offset", 0)),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.SilenceError, "Failed to list control plane silences")
		return
	}
	resp := make([]map[string]any, 0, len(items))
	for _, item := range items {
		resp = append(resp, controlPlaneSilenceResponse(item))
	}
	RespondJSON(w, http.StatusOK, resp)
}

func (h *ControlPlaneHandler) CreateSilence(w http.ResponseWriter, r *http.Request) {
	var req CreateControlPlaneSilenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if req.Controller == "" || req.Reason == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Controller and reason are required")
		return
	}
	duration := time.Hour
	if req.Duration != "" {
		if parsed, err := time.ParseDuration(req.Duration); err == nil && parsed > 0 {
			duration = parsed
		}
	}
	item, err := h.queries.CreateControlPlaneSilence(r.Context(), sqlc.CreateControlPlaneSilenceParams{
		Controller:    req.Controller,
		ConditionType: req.ConditionType,
		Reason:        req.Reason,
		StartsAt:      time.Now().UTC(),
		EndsAt:        time.Now().UTC().Add(duration),
		CreatedByID:   currentUserUUID(r),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.SilenceError, "Failed to create control plane silence")
		return
	}
	recordAudit(r, h.queries, "controlplane.silence.create", "control_plane_silence", item.ID.String(), req.Reason, map[string]any{
		"controller":     req.Controller,
		"condition_type": req.ConditionType,
		"duration":       duration.String(),
	})
	RespondJSON(w, http.StatusCreated, controlPlaneSilenceResponse(item))
}

func (h *ControlPlaneHandler) DeleteSilence(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid silence ID")
		return
	}
	if err := h.queries.DeleteControlPlaneSilence(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Control plane silence not found")
		return
	}
	recordAudit(r, h.queries, "controlplane.silence.delete", "control_plane_silence", id.String(), "", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (h *ControlPlaneHandler) statusPayload(ctx context.Context, policy sqlc.ControlPlanePolicy) (map[string]any, map[string]any) {
	out := map[string]any{}
	totalQueue := 0
	totalStale := 0
	controllersWithFailures := 0
	degraded := 0

	for name, summary := range h.collectSummaries(ctx) {
		evaluated := h.applyPolicy(name, summary, policy)
		out[name] = evaluated
		totalQueue += extractQueueDepth(evaluated)
		totalStale += extractStaleRunning(evaluated)
		if hasLatestFailure(evaluated) {
			controllersWithFailures++
		}
		if controllerHealth(evaluated) != "healthy" {
			degraded++
		}
	}

	return out, map[string]any{
		"controllers":             len(out),
		"queueDepth":              totalQueue,
		"staleRunningCount":       totalStale,
		"controllersWithFailures": controllersWithFailures,
		"degradedControllers":     degraded,
		"health":                  ternaryHealth(degraded == 0),
	}
}

func (h *ControlPlaneHandler) collectSummaries(ctx context.Context) map[string]map[string]any {
	out := map[string]map[string]any{}
	if h.Monitoring != nil {
		if summary, err := h.Monitoring.controllerSummary(ctx); err == nil {
			out["monitoring"] = summary
		}
	}
	if h.ArgoCD != nil {
		if summary, err := h.ArgoCD.controllerSummary(ctx); err == nil {
			out["argocd"] = summary
		}
	}
	if h.Tools != nil {
		if summary, err := h.Tools.controllerSummary(ctx); err == nil {
			out["tools"] = summary
		}
	}
	if h.Catalog != nil {
		if summary, err := h.Catalog.controllerSummary(ctx); err == nil {
			out["catalog"] = summary
		}
	}
	if h.Backups != nil {
		if summary, err := h.Backups.controllerSummary(ctx); err == nil {
			out["backups"] = summary
		}
	}
	if h.Logging != nil {
		if summary, err := h.Logging.controllerSummary(ctx); err == nil {
			out["logging"] = summary
		}
	}
	if h.Security != nil {
		if summary, err := h.Security.controllerSummary(ctx); err == nil {
			out["security"] = summary
		}
	}
	return out
}

func (h *ControlPlaneHandler) applyPolicy(name string, summary map[string]any, policy sqlc.ControlPlanePolicy) map[string]any {
	if !policyManagedController(name) {
		if _, ok := summary["health"]; !ok {
			summary["health"] = "unknown"
		}
		if _, ok := summary["healthReasons"]; !ok {
			summary["healthReasons"] = []string{}
		}
		summary["policy"] = map[string]any{"managed": false}
		return summary
	}
	queueThreshold, staleThreshold, failureThreshold := thresholdsFor(name, policy)
	queueDepth := extractQueueDepth(summary)
	staleRunning := extractStaleRunning(summary)
	recentFailures := extractRecentFailureCount(summary)
	health := "healthy"
	reasons := []string{}
	if queueDepth >= int(queueThreshold) {
		health = "degraded"
		reasons = append(reasons, "queue_depth")
	}
	if staleRunning >= int(staleThreshold) {
		health = "degraded"
		reasons = append(reasons, "stale_running")
	}
	if recentFailures >= int(failureThreshold) {
		health = "degraded"
		reasons = append(reasons, "recent_failures")
	}
	summary["health"] = health
	summary["healthReasons"] = reasons
	summary["policy"] = map[string]any{
		"queueDepthThreshold":        queueThreshold,
		"staleRunningThreshold":      staleThreshold,
		"recentFailureThreshold":     failureThreshold,
		"recentFailureWindowMinutes": policy.RecentFailureWindowMinutes,
	}
	return summary
}

func (h *ControlPlaneHandler) evaluate(ctx context.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	policy, err := h.queries.GetDefaultControlPlanePolicy(ctx)
	if err != nil {
		return
	}
	silences, _ := h.queries.GetActiveControlPlaneSilences(ctx)
	for name, summary := range h.collectSummaries(ctx) {
		evaluated := h.applyPolicy(name, summary, policy)
		if !policyManagedController(name) {
			continue
		}
		h.reconcileAlert(ctx, name, "queue_depth", extractQueueDepth(evaluated) >= int(thresholdQueue(name, policy)), evaluated, silences)
		h.reconcileAlert(ctx, name, "stale_running", extractStaleRunning(evaluated) >= int(thresholdStale(name, policy)), evaluated, silences)
		h.reconcileAlert(ctx, name, "recent_failures", extractRecentFailureCount(evaluated) >= int(thresholdFailures(name, policy)), evaluated, silences)
	}
}

func policyManagedController(name string) bool {
	switch name {
	case "monitoring", "argocd", "tools", "catalog":
		return true
	default:
		return false
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
		alert, err := h.queries.CreateControlPlaneAlert(ctx, sqlc.CreateControlPlaneAlertParams{
			Controller:    controller,
			ConditionType: condition,
			Status:        "active",
			Message:       controller + " controller degraded: " + condition,
			Detail:        raw,
		})
		if err == nil && !isSilenced(controller, condition, silences) {
			h.enqueueNotifications(ctx, alert)
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

func (h *ControlPlaneHandler) enqueueNotifications(ctx context.Context, alert sqlc.ControlPlaneAlert) {
	if h == nil || h.queue == nil || h.queries == nil {
		return
	}
	channels, err := h.queries.ListEnabledNotificationChannels(ctx)
	if err != nil {
		return
	}
	for _, channel := range channels {
		recipients := controlPlaneChannelRecipients(channel)
		if len(recipients) == 0 {
			continue
		}
		// Email channels: enqueue through the SMTP path so the
		// admin-email audit view sees them and the operator gets a
		// real templated message. Falls back to the legacy
		// notification:send asynq task when the email enqueuer
		// isn't wired (test scaffolding, pre-encryption-key boot).
		if strings.EqualFold(channel.ChannelType, "email") && h.emails != nil {
			for _, recipient := range recipients {
				h.emails.EnqueueAndLog(ctx, EmailNotifierRequest{
					To:       recipient,
					Template: "alert_fired",
					Subject:  alert.Controller + " " + alert.ConditionType,
					Data: map[string]any{
						"AlertName":    alert.Controller + ":" + alert.ConditionType,
						"Severity":     alert.Status,
						"FiredAt":      alert.FiredAt.UTC().Format(time.RFC3339),
						"Resource":     alert.Controller,
						"Message":      alert.Message,
						"DashboardURL": "",
					},
				})
			}
			continue
		}
		task, err := tasks.NewNotificationSendTask(tasks.NotificationSendPayload{
			Channel:    channel.ChannelType,
			Subject:    "Control plane alert: " + alert.Controller + " " + alert.ConditionType,
			Body:       alert.Message,
			Recipients: recipients,
		})
		if err != nil {
			continue
		}
		payload := observability.EnrichTaskPayload(ctx, task.Payload(), middleware.GetCorrelationID(ctx))
		task = asynq.NewTask(task.Type(), payload)
		_, _ = h.queue.Enqueue(task)
	}
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
		"argocdQueueDepthThreshold":        policy.ArgocdQueueDepthThreshold,
		"toolsQueueDepthThreshold":         policy.ToolsQueueDepthThreshold,
		"catalogQueueDepthThreshold":       policy.CatalogQueueDepthThreshold,
		"monitoringStaleRunningThreshold":  policy.MonitoringStaleRunningThreshold,
		"argocdStaleRunningThreshold":      policy.ArgocdStaleRunningThreshold,
		"toolsStaleRunningThreshold":       policy.ToolsStaleRunningThreshold,
		"catalogStaleRunningThreshold":     policy.CatalogStaleRunningThreshold,
		"monitoringRecentFailureThreshold": policy.MonitoringRecentFailureThreshold,
		"argocdRecentFailureThreshold":     policy.ArgocdRecentFailureThreshold,
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

func thresholdsFor(name string, policy sqlc.ControlPlanePolicy) (int32, int32, int32) {
	return thresholdQueue(name, policy), thresholdStale(name, policy), thresholdFailures(name, policy)
}

func thresholdQueue(name string, policy sqlc.ControlPlanePolicy) int32 {
	switch name {
	case "monitoring":
		return policy.MonitoringQueueDepthThreshold
	case "argocd":
		return policy.ArgocdQueueDepthThreshold
	case "tools":
		return policy.ToolsQueueDepthThreshold
	default:
		return policy.CatalogQueueDepthThreshold
	}
}

func thresholdStale(name string, policy sqlc.ControlPlanePolicy) int32 {
	switch name {
	case "monitoring":
		return policy.MonitoringStaleRunningThreshold
	case "argocd":
		return policy.ArgocdStaleRunningThreshold
	case "tools":
		return policy.ToolsStaleRunningThreshold
	default:
		return policy.CatalogStaleRunningThreshold
	}
}

func thresholdFailures(name string, policy sqlc.ControlPlanePolicy) int32 {
	switch name {
	case "monitoring":
		return policy.MonitoringRecentFailureThreshold
	case "argocd":
		return policy.ArgocdRecentFailureThreshold
	case "tools":
		return policy.ToolsRecentFailureThreshold
	default:
		return policy.CatalogRecentFailureThreshold
	}
}

func extractQueueDepth(summary map[string]any) int {
	reconciler, ok := summary["reconciler"].(map[string]any)
	if !ok {
		return 0
	}
	return controlPlaneIntValue(reconciler["queueDepth"])
}

func extractStaleRunning(summary map[string]any) int {
	reconciler, ok := summary["reconciler"].(map[string]any)
	if !ok {
		return 0
	}
	return controlPlaneIntValue(reconciler["staleRunningCount"])
}

func extractRecentFailureCount(summary map[string]any) int {
	return controlPlaneIntValue(summary["recentFailureCount"])
}

func controllerHealth(summary map[string]any) string {
	if value, ok := summary["health"].(string); ok {
		return value
	}
	return "unknown"
}

func hasLatestFailure(summary map[string]any) bool {
	value, ok := summary["latestFailure"]
	return ok && value != nil
}

func controlPlaneIntValue(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func atLeastOne(v int32) int32 {
	if v < 1 {
		return 1
	}
	return v
}

func ternaryHealth(healthy bool) string {
	if healthy {
		return "healthy"
	}
	return "degraded"
}
