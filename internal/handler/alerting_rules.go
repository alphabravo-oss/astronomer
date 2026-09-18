package handler

import (
	"encoding/json"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// --- Rule Endpoints ---

// ListRules handles GET /api/v1/alerting/rules/.
func (h *AlertingHandler) ListRules(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 20))
	offset := int32(queryOffset(r))

	var clusterID pgtype.UUID
	if v := r.URL.Query().Get("clusterId"); v != "" {
		parsed, parseErr := uuid.Parse(v)
		if parseErr != nil {
			paging.Write(w, []map[string]any{}, paging.Exact(0, int(limit), int(offset), 0))
			return
		}
		clusterID = pgtype.UUID{Bytes: parsed, Valid: true}
	}

	var (
		rules []sqlc.AlertRule
		err   error
		total int64
	)
	if clusterID.Valid {
		rules, err = h.queries.ListAlertRulesByCluster(r.Context(), sqlc.ListAlertRulesByClusterParams{
			ClusterID: clusterID,
			Limit:     limit,
			Offset:    offset,
		})
	} else {
		rules, err = h.queries.ListAlertRules(r.Context(), sqlc.ListAlertRulesParams{
			Limit:  limit,
			Offset: offset,
		})
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list alert rules")
		return
	}
	if clusterID.Valid {
		total, _ = h.queries.CountAlertRulesByCluster(r.Context(), clusterID)
	} else {
		total, _ = h.queries.CountAlertRules(r.Context())
	}

	items := h.alertRuleResponses(r.Context(), rules)
	paging.Write(w, items, paging.Exact(total, int(limit), int(offset), len(items)))
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

	params := sqlc.CreateAlertRuleParams{
		Name:            req.Name,
		ClusterID:       clusterID,
		RuleType:        ruleType,
		Configuration:   configuration,
		Severity:        req.Severity,
		Enabled:         req.Enabled,
		CooldownMinutes: req.CooldownMinutes,
		CreatedByID:     currentUserUUID(r),
	}
	rule, err := executeMutation(r, h.runTx,
		func(q AlertingMutationTx) (sqlc.AlertRule, error) {
			rule, createErr := q.CreateAlertRule(r.Context(), params)
			if createErr != nil {
				return sqlc.AlertRule{}, createErr
			}
			if syncErr := syncRuleChannelsWith(r.Context(), q, rule.ID, req.NotificationChannelIDs); syncErr != nil {
				return sqlc.AlertRule{}, syncErr
			}
			return rule, nil
		},
		func(row sqlc.AlertRule) mutationAuditEvent {
			return mutationAuditEvent{
				action: "alert.rule.create", resourceType: "alert_rule",
				resourceID: row.ID.String(), resourceName: row.Name, status: http.StatusCreated,
				detail: map[string]any{"rule_type": row.RuleType, "severity": row.Severity, "enabled": row.Enabled},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create alert rule or associate notification channels")
		return
	}
	_ = h.syncSharedAlertingAssets(r.Context())

	h.publishAlertingChanged("rule", nullableUUIDString(rule.ClusterID), rule.ID)

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

	params := sqlc.UpdateAlertRuleParams{
		ID:              id,
		Name:            req.Name,
		RuleType:        req.RuleType,
		Configuration:   req.Configuration,
		Severity:        req.Severity,
		Enabled:         req.Enabled,
		CooldownMinutes: req.CooldownMinutes,
	}
	rule, err := executeMutation(r, h.runTx,
		func(q AlertingMutationTx) (sqlc.AlertRule, error) {
			rule, updateErr := q.UpdateAlertRule(r.Context(), params)
			if updateErr != nil {
				return sqlc.AlertRule{}, updateErr
			}
			if len(req.NotificationChannelIDs) > 0 {
				if syncErr := syncRuleChannelsWith(r.Context(), q, rule.ID, req.NotificationChannelIDs); syncErr != nil {
					return sqlc.AlertRule{}, syncErr
				}
			}
			return rule, nil
		},
		func(row sqlc.AlertRule) mutationAuditEvent {
			return mutationAuditEvent{
				action: "alert.rule.update", resourceType: "alert_rule",
				resourceID: row.ID.String(), resourceName: row.Name, status: http.StatusOK,
				detail: map[string]any{"severity": row.Severity, "enabled": row.Enabled},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.UpdateError, "Failed to update alert rule or notification channels")
		return
	}
	_ = h.syncSharedAlertingAssets(r.Context())

	h.publishAlertingChanged("rule", nullableUUIDString(rule.ClusterID), rule.ID)

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
	ruleCluster := ""
	if existing, lookupErr := h.queries.GetAlertRuleByID(r.Context(), id); lookupErr == nil {
		ruleName = existing.Name
		ruleCluster = nullableUUIDString(existing.ClusterID)
	}
	_, err = executeMutation(r, h.runTx,
		func(q AlertingMutationTx) (struct{}, error) {
			return struct{}{}, q.DeleteAlertRule(r.Context(), id)
		},
		func(struct{}) mutationAuditEvent {
			return mutationAuditEvent{
				action: "alert.rule.delete", resourceType: "alert_rule",
				resourceID: id.String(), resourceName: ruleName, status: http.StatusNoContent,
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusNotFound, apierror.NotFound, "Alert rule not found")
		return
	}
	_ = h.syncSharedAlertingAssets(r.Context())

	h.publishAlertingChanged("rule", ruleCluster, id)

	w.WriteHeader(http.StatusNoContent)
}

// EnableRule handles POST /api/v1/alerting/rules/{id}/enable/.
func (h *AlertingHandler) EnableRule(w http.ResponseWriter, r *http.Request) {
	h.setRuleEnabled(w, r, true)
}

// DisableRule handles POST /api/v1/alerting/rules/{id}/disable/.
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
	params := sqlc.UpdateAlertRuleParams{
		ID:              id,
		Name:            current.Name,
		RuleType:        current.RuleType,
		Configuration:   current.Configuration,
		Severity:        current.Severity,
		Enabled:         enabled,
		CooldownMinutes: current.CooldownMinutes,
	}
	action := "alert.rule.disable"
	if enabled {
		action = "alert.rule.enable"
	}
	rule, err := executeMutation(r, h.runTx,
		func(q AlertingMutationTx) (sqlc.AlertRule, error) {
			return q.UpdateAlertRule(r.Context(), params)
		},
		func(row sqlc.AlertRule) mutationAuditEvent {
			return mutationAuditEvent{
				action: action, resourceType: "alert_rule", resourceID: row.ID.String(),
				resourceName: row.Name, status: http.StatusOK, detail: map[string]any{"enabled": enabled},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.UpdateError, "Failed to update alert rule")
		return
	}
	_ = h.syncSharedAlertingAssets(r.Context())
	h.publishAlertingChanged("rule", nullableUUIDString(rule.ClusterID), rule.ID)
	RespondJSON(w, http.StatusOK, h.alertRuleResponse(r.Context(), rule))
}
