package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type CharlieAdminBackend interface {
	Status(context.Context) (charlie.AdminStatusView, error)
	Install(context.Context) (charlie.AdminAgentView, error)
	ReplacementAction(context.Context, string) (charlie.AdminAgentView, error)
	Uninstall(context.Context, uuid.UUID) error
	Disconnect(context.Context, uuid.UUID) error
	UpdateMode(context.Context, charlie.Mode, int64, bool, uuid.UUID) (charlie.AdminModeView, error)
	AcknowledgeDisclosure(context.Context, string) (charlie.AdminModeView, error)
	Automation(context.Context) (charlie.AdminAutomationView, error)
	AlertPolicy(context.Context) (charlie.AdminAlertPolicyView, error)
	AlertDeliveryProofs(context.Context, uuid.UUID) (charlie.AdminAlertDeliveryProofView, error)
	DiscoveryQualification(context.Context, string) (charlie.AdminDiscoveryQualificationView, error)
	UpdateAlertPolicy(context.Context, charlie.AdminAlertPolicyInput, uuid.UUID) (charlie.AdminAlertPolicyView, error)
	UpdateActionPolicy(context.Context, string, charlie.AdminActionPolicyInput) (charlie.AdminActionPolicy, error)
	CreateTrigger(context.Context, uuid.UUID, charlie.AdminTriggerRule) (charlie.AdminTriggerRule, error)
	UpdateTrigger(context.Context, uuid.UUID, charlie.AdminTriggerRule) (charlie.AdminTriggerRule, error)
	DeleteTrigger(context.Context, uuid.UUID) error
	ListTriggerEvents(context.Context, string, int32, int32) ([]charlie.AdminTriggerEventView, error)
	RetryTriggerEvent(context.Context, uuid.UUID, uuid.UUID) (charlie.AdminTriggerEventView, error)
	Access(context.Context) (charlie.AdminAccessView, error)
	SetAutomationIdentity(context.Context, bool) (charlie.AdminAccessView, error)
	Diagnostics(context.Context, string) (charlie.AdminDiagnosticsView, error)
}

type charlieAdminLocalBackend interface {
	LocalStatus(context.Context) (charlie.AdminStatusView, error)
	LocalDiagnostics(context.Context, string) (charlie.AdminDiagnosticsView, error)
}

type CharlieAdminHandler struct {
	backend  CharlieAdminBackend
	audit    any
	features *SettingsCache
}

func (h *CharlieAdminHandler) SetSettingsCache(cache *SettingsCache) { h.features = cache }

func NewCharlieAdminHandler(backend CharlieAdminBackend, audit any) *CharlieAdminHandler {
	return &CharlieAdminHandler{backend: backend, audit: audit}
}

func (h *CharlieAdminHandler) Status(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actor(w, r); !ok {
		return
	}
	if h.backend == nil {
		h.respondError(w, r, charlie.ErrAdminUnavailable)
		return
	}
	var view charlie.AdminStatusView
	var err error
	if h.features != nil && !h.features.BoolValue(r.Context(), "feature.charlie", false) {
		local, ok := h.backend.(charlieAdminLocalBackend)
		if !ok {
			h.respondError(w, r, charlie.ErrAdminUnavailable)
			return
		}
		view, err = local.LocalStatus(r.Context())
	} else {
		view, err = h.backend.Status(r.Context())
	}
	if err != nil {
		h.respondError(w, r, err)
		return
	}
	RespondJSON(w, http.StatusOK, view)
}

// openapi:request CharlieAdminActionRequest
type charlieAdminActionRequest struct {
	Confirmation string `json:"confirmation"`
}

func (h *CharlieAdminHandler) Install(w http.ResponseWriter, r *http.Request) {
	_, ok := h.actor(w, r)
	if !ok {
		return
	}
	if !h.requireAuthorityAudit(w, r, "admin.charlie.agent.install", "charlie_connection", "current", nil) {
		return
	}
	view, err := h.backend.Install(r.Context())
	if err != nil {
		h.respondError(w, r, err)
		return
	}
	recordCharlieAdminAudit(r, h.audit, "admin.charlie.agent.install", "charlie_connection", "current", nil)
	RespondJSON(w, http.StatusOK, view)
}

func (h *CharlieAdminHandler) ReplacementAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := h.actor(w, r); !ok {
			return
		}
		auditAction := "admin.charlie.agent." + action
		if !h.requireAuthorityAudit(w, r, auditAction, "charlie_connection", "current", nil) {
			return
		}
		view, err := h.backend.ReplacementAction(r.Context(), action)
		if err != nil {
			h.respondError(w, r, err)
			return
		}
		recordCharlieAdminAudit(r, h.audit, auditAction, "charlie_connection", "current", nil)
		RespondJSON(w, http.StatusOK, view)
	}
}

func (h *CharlieAdminHandler) Uninstall(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	var request charlieAdminActionRequest
	if !decodeCharlieJSON(w, r, &request) {
		return
	}
	if request.Confirmation != "UNINSTALL CHARLIE" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Exact uninstall confirmation is required")
		return
	}
	if !h.requireAuthorityAudit(w, r, "admin.charlie.agent.uninstall", "charlie_connection", "current", nil) {
		return
	}
	if err := h.backend.Uninstall(r.Context(), mustUserID(actor)); err != nil {
		h.respondError(w, r, err)
		return
	}
	recordCharlieAdminAudit(r, h.audit, "admin.charlie.agent.uninstall", "charlie_connection", "current", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (h *CharlieAdminHandler) Disconnect(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	var request charlieAdminActionRequest
	if !decodeCharlieJSON(w, r, &request) {
		return
	}
	if request.Confirmation != "DISCONNECT CHARLIE" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Exact disconnect confirmation is required")
		return
	}
	if !h.requireAuthorityAudit(w, r, "admin.charlie.disconnect", "charlie_connection", "current", nil) {
		return
	}
	if err := h.backend.Disconnect(r.Context(), mustUserID(actor)); err != nil {
		h.respondError(w, r, err)
		return
	}
	recordCharlieAdminAudit(r, h.audit, "admin.charlie.disconnect", "charlie_connection", "current", nil)
	w.WriteHeader(http.StatusNoContent)
}

// openapi:request CharlieModeRequest
type charlieModeRequest struct {
	Mode                        charlie.Mode `json:"mode"`
	Revision                    *int64       `json:"revision"`
	EmergencyDisable            bool         `json:"emergency_disable"`
	AcknowledgeDisclosureDigest string       `json:"acknowledge_disclosure_digest"`
}

func (h *CharlieAdminHandler) Mode(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	var request charlieModeRequest
	if !decodeCharlieJSON(w, r, &request) {
		return
	}
	if !request.EmergencyDisable && h.features != nil && !h.features.BoolValue(r.Context(), "feature.charlie", false) {
		RespondRequestError(w, r, http.StatusConflict, apierror.ValidationError, "Charlie must be enabled before changing mode or disclosure")
		return
	}
	if request.AcknowledgeDisclosureDigest != "" {
		if request.Revision != nil || request.Mode != "" || request.EmergencyDisable {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Disclosure acknowledgement must be a separate request")
			return
		}
		if !h.requireAuthorityAudit(w, r, "admin.charlie.disclosure.acknowledge", "charlie_connection", "current", nil) {
			return
		}
		view, err := h.backend.AcknowledgeDisclosure(r.Context(), request.AcknowledgeDisclosureDigest)
		if err != nil {
			h.respondError(w, r, err)
			return
		}
		recordCharlieAdminAudit(r, h.audit, "admin.charlie.disclosure.acknowledge", "charlie_connection", "current", nil)
		RespondJSON(w, http.StatusOK, view)
		return
	}
	if request.Revision == nil || request.Mode == "" || (request.EmergencyDisable && request.Mode != charlie.ModeDisabled) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Charlie mode request is invalid")
		return
	}
	fields := map[string]any{"mode": string(request.Mode), "revision": *request.Revision}
	if !request.EmergencyDisable && !h.requireAuthorityAudit(w, r, "admin.charlie.mode.update", "charlie_connection", "current", fields) {
		return
	}
	view, err := h.backend.UpdateMode(r.Context(), request.Mode, *request.Revision, request.EmergencyDisable, mustUserID(actor))
	if err != nil {
		h.respondError(w, r, err)
		return
	}
	action := "admin.charlie.mode.update"
	if request.EmergencyDisable {
		action = "admin.charlie.mode.emergency_disable"
	}
	recordCharlieAdminAudit(r, h.audit, action, "charlie_connection", "current", fields)
	RespondJSON(w, http.StatusOK, view)
}

func (h *CharlieAdminHandler) ListTriggers(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actor(w, r); !ok {
		return
	}
	view, err := h.backend.Automation(r.Context())
	if err != nil {
		h.respondError(w, r, err)
		return
	}
	RespondJSON(w, http.StatusOK, view)
}

func (h *CharlieAdminHandler) AlertPolicy(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actor(w, r); !ok {
		return
	}
	view, err := h.backend.AlertPolicy(r.Context())
	if err != nil {
		h.respondError(w, r, err)
		return
	}
	RespondJSON(w, http.StatusOK, view)
}

func (h *CharlieAdminHandler) AlertDeliveryProofs(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actor(w, r); !ok {
		return
	}
	findingID, err := uuid.Parse(strings.TrimSpace(r.URL.Query().Get("finding_id")))
	if err != nil || findingID == uuid.Nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Valid Charlie finding ID is required")
		return
	}
	view, err := h.backend.AlertDeliveryProofs(r.Context(), findingID)
	if err != nil {
		h.respondError(w, r, err)
		return
	}
	recordCharlieAdminReadAudit(r, h.audit, "admin.charlie.alert_delivery.read", "charlie_finding", findingID.String(), map[string]any{
		"delivery_count": view.DeliveryCount, "dedupe_valid": view.DedupeValid,
	})
	RespondJSON(w, http.StatusOK, view)
}

// openapi:request CharlieDiscoveryQualificationRequest
type charlieDiscoveryQualificationRequest struct {
	Scenario string `json:"scenario"`
}

func (h *CharlieAdminHandler) DiscoveryQualification(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actor(w, r); !ok {
		return
	}
	var request charlieDiscoveryQualificationRequest
	if !decodeCharlieJSON(w, r, &request) {
		return
	}
	if request.Scenario != charlie.DiscoveryQualificationMixed && request.Scenario != charlie.DiscoveryQualificationMalformed {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Unknown Charlie discovery qualification scenario")
		return
	}
	view, err := h.backend.DiscoveryQualification(r.Context(), request.Scenario)
	if err != nil {
		h.respondError(w, r, err)
		return
	}
	recordCharlieAdminReadAudit(r, h.audit, "admin.charlie.discovery_qualification.run", "charlie_connection", "current", map[string]any{
		"scenario": view.Scenario, "accepted_count": view.AcceptedCount, "rejected_count": view.RejectedCount,
		"candidate_enabled": view.CandidateEnabled, "catalog_bound": view.CatalogBound,
	})
	RespondJSON(w, http.StatusOK, view)
}

func (h *CharlieAdminHandler) UpdateAlertPolicy(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	var request charlie.AdminAlertPolicyInput
	if !decodeCharlieJSON(w, r, &request) {
		return
	}
	fields := map[string]any{
		"enabled": request.Enabled, "minimum_severity": request.MinimumSeverity,
		"dedupe_window_seconds":    request.DedupeWindowSeconds,
		"escalation_after_seconds": request.EscalationAfterSeconds,
		"quiet_hours_enabled":      request.QuietHoursEnabled, "channel_count": len(request.ChannelIDs), "revision": request.Revision,
	}
	view, err := h.backend.UpdateAlertPolicy(r.Context(), request, mustUserID(actor))
	if err != nil {
		h.respondError(w, r, err)
		return
	}
	fields["revision"] = view.Revision
	recordCharlieAdminAudit(r, h.audit, "admin.charlie.alert_policy.update", "charlie_alert_policy", "current", fields)
	RespondJSON(w, http.StatusOK, view)
}

func (h *CharlieAdminHandler) UpdateActionPolicy(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actor(w, r); !ok {
		return
	}
	capability := strings.TrimSpace(chi.URLParam(r, "capability"))
	var request charlie.AdminActionPolicyInput
	if capability == "" || !decodeCharlieJSON(w, r, &request) {
		return
	}
	resourceID := charlieAuditOpaque(capability)
	if !h.requireAuthorityAudit(w, r, "admin.charlie.action_policy.update", "charlie_action_policy", resourceID, nil) {
		return
	}
	view, err := h.backend.UpdateActionPolicy(r.Context(), capability, request)
	if err != nil {
		h.respondError(w, r, err)
		return
	}
	recordCharlieAdminAudit(r, h.audit, "admin.charlie.action_policy.update", "charlie_action_policy", resourceID, nil)
	RespondJSON(w, http.StatusOK, view)
}

func (h *CharlieAdminHandler) CreateTrigger(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	var request charlie.AdminTriggerRule
	if !decodeCharlieJSON(w, r, &request) {
		return
	}
	intentID := charlieAuditOpaque(request.Name)
	if !h.requireAuthorityAudit(w, r, "admin.charlie.trigger.create", "charlie_trigger_rule", intentID, map[string]any{"enabled": request.Enabled, "suppressed": request.Suppressed}) {
		return
	}
	view, err := h.backend.CreateTrigger(r.Context(), mustUserID(actor), request)
	if err != nil {
		h.respondError(w, r, err)
		return
	}
	recordCharlieAdminAudit(r, h.audit, "admin.charlie.trigger.create", "charlie_trigger_rule", view.ID, map[string]any{"enabled": view.Enabled, "suppressed": view.Suppressed})
	RespondJSON(w, http.StatusCreated, view)
}

func (h *CharlieAdminHandler) UpdateTrigger(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actor(w, r); !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "rule_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid Charlie trigger rule ID")
		return
	}
	var request charlie.AdminTriggerRule
	if !decodeCharlieJSON(w, r, &request) {
		return
	}
	fields := map[string]any{"enabled": request.Enabled, "suppressed": request.Suppressed}
	if !h.requireAuthorityAudit(w, r, "admin.charlie.trigger.update", "charlie_trigger_rule", id.String(), fields) {
		return
	}
	view, err := h.backend.UpdateTrigger(r.Context(), id, request)
	if err != nil {
		h.respondError(w, r, err)
		return
	}
	recordCharlieAdminAudit(r, h.audit, "admin.charlie.trigger.update", "charlie_trigger_rule", id.String(), map[string]any{"enabled": view.Enabled, "suppressed": view.Suppressed})
	RespondJSON(w, http.StatusOK, view)
}

func (h *CharlieAdminHandler) DeleteTrigger(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actor(w, r); !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "rule_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid Charlie trigger rule ID")
		return
	}
	var request charlieAdminActionRequest
	if !decodeCharlieJSON(w, r, &request) {
		return
	}
	if request.Confirmation != "DELETE TRIGGER" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Exact trigger deletion confirmation is required")
		return
	}
	if !h.requireAuthorityAudit(w, r, "admin.charlie.trigger.delete", "charlie_trigger_rule", id.String(), nil) {
		return
	}
	if err := h.backend.DeleteTrigger(r.Context(), id); err != nil {
		h.respondError(w, r, err)
		return
	}
	recordCharlieAdminAudit(r, h.audit, "admin.charlie.trigger.delete", "charlie_trigger_rule", id.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (h *CharlieAdminHandler) ListTriggerEvents(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actor(w, r); !ok {
		return
	}
	offset, ok := boundedQueryInt(w, r, "offset", 0, 0, 100000)
	if !ok {
		return
	}
	limit, ok := boundedQueryInt(w, r, "limit", 20, 1, charlie.MaxAdminTriggerEvents)
	if !ok {
		return
	}
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if state == "" {
		state = "dead"
	}
	items, err := h.backend.ListTriggerEvents(r.Context(), state, int32(offset), int32(limit))
	if err != nil {
		h.respondError(w, r, err)
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"items": items})
}

// openapi:request CharlieTriggerRetryRequest
type charlieTriggerRetryRequest struct {
	RequestID string `json:"request_id"`
}

func (h *CharlieAdminHandler) RetryTriggerEvent(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actor(w, r); !ok {
		return
	}
	eventID, err := uuid.Parse(chi.URLParam(r, "event_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid Charlie trigger event ID")
		return
	}
	var request charlieTriggerRetryRequest
	if !decodeCharlieJSON(w, r, &request) {
		return
	}
	requestID, err := uuid.Parse(request.RequestID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid Charlie trigger retry request")
		return
	}
	if !h.requireAuthorityAudit(w, r, "admin.charlie.trigger.retry", "charlie_trigger_event", eventID.String(), nil) {
		return
	}
	view, err := h.backend.RetryTriggerEvent(r.Context(), eventID, requestID)
	if err != nil {
		h.respondError(w, r, err)
		return
	}
	recordCharlieAdminAudit(r, h.audit, "admin.charlie.trigger.retry", "charlie_trigger_event", view.ID, nil)
	RespondJSON(w, http.StatusAccepted, map[string]any{"event": view})
}

func (h *CharlieAdminHandler) Access(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actor(w, r); !ok {
		return
	}
	view, err := h.backend.Access(r.Context())
	if err != nil {
		h.respondError(w, r, err)
		return
	}
	RespondJSON(w, http.StatusOK, view)
}

func (h *CharlieAdminHandler) UpdateAccess(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actor(w, r); !ok {
		return
	}
	var request charlieAccessRequest
	if !decodeCharlieJSON(w, r, &request) {
		return
	}
	if request.AutomationServiceIdentityEnabled == nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Automation service identity state is required")
		return
	}
	fields := map[string]any{"enabled": *request.AutomationServiceIdentityEnabled}
	if !h.requireAuthorityAudit(w, r, "admin.charlie.access.update", "charlie_automation_identity", "automation_identity", fields) {
		return
	}
	view, err := h.backend.SetAutomationIdentity(r.Context(), *request.AutomationServiceIdentityEnabled)
	if err != nil {
		h.respondError(w, r, err)
		return
	}
	recordCharlieAdminAudit(r, h.audit, "admin.charlie.access.update", "charlie_automation_identity", "automation_identity", fields)
	RespondJSON(w, http.StatusOK, view)
}

// openapi:request CharlieAccessRequest
type charlieAccessRequest struct {
	AutomationServiceIdentityEnabled *bool `json:"automation_service_identity_enabled"`
}

func (h *CharlieAdminHandler) Diagnostics(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actor(w, r); !ok {
		return
	}
	correlationID := appmiddleware.GetCorrelationID(r.Context())
	var view charlie.AdminDiagnosticsView
	var err error
	if h.features != nil && !h.features.BoolValue(r.Context(), "feature.charlie", false) {
		local, ok := h.backend.(charlieAdminLocalBackend)
		if !ok {
			h.respondError(w, r, charlie.ErrAdminUnavailable)
			return
		}
		view, err = local.LocalDiagnostics(r.Context(), correlationID)
	} else {
		view, err = h.backend.Diagnostics(r.Context(), correlationID)
	}
	if err != nil {
		h.respondError(w, r, err)
		return
	}
	recordCharlieAdminAudit(r, h.audit, "admin.charlie.diagnostics.run", "charlie_connection", "current", map[string]any{"overall": view.Overall})
	RespondJSON(w, http.StatusOK, view)
}

func (h *CharlieAdminHandler) actor(w http.ResponseWriter, r *http.Request) (*appmiddleware.AuthenticatedUser, bool) {
	if h == nil || h.backend == nil {
		h.respondError(w, r, charlie.ErrAdminUnavailable)
		return nil, false
	}
	return browserCharlieActor(w, r)
}

func (h *CharlieAdminHandler) respondError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, charlie.ErrAdminNotConfigured):
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Charlie is not configured")
	case errors.Is(err, charlie.ErrAdminConflict), errors.Is(err, charlie.ErrReplacementPackageNeeded):
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Charlie administration prerequisites are incomplete or changed")
	default:
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.InternalError, "Charlie administration is unavailable")
	}
}

func (h *CharlieAdminHandler) requireAuthorityAudit(w http.ResponseWriter, r *http.Request, action, resourceType, resourceID string, fields map[string]any) bool {
	if err := requireCharlieAdminAudit(r, h.audit, action, resourceType, resourceID, fields); err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.InternalError, "Charlie authority-change audit is unavailable")
		return false
	}
	return true
}
