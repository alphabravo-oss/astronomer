package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type monitoringClusterOperationWriter interface {
	monitoringOperationCreator
	GetDefaultMonitoringBackend(context.Context) (sqlc.MonitoringBackend, error)
}

func createClusterStackOperationWith(ctx context.Context, h *MonitoringHandler, q monitoringClusterOperationWriter, userID pgtype.UUID, opType, clusterID string, req MonitoringStackRequest, values map[string]any) (sqlc.MonitoringOperation, error) {
	rawReq, err := json.Marshal(req)
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}
	backend, err := q.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}
	payload, err := json.Marshal(monitoringOperationEnvelope{
		ClusterID:                clusterID,
		Request:                  rawReq,
		Values:                   values,
		ResolvedAutoRollback:     h.resolveAutoRollbackPolicy(backend, req.AutoRollbackOnFailure),
		ResolvedMaxRetryAttempts: h.resolveMaxRetryAttempts(backend),
	})
	if err != nil {
		return sqlc.MonitoringOperation{}, err
	}
	params := sqlc.CreateMonitoringOperationParams{
		TargetType:    "cluster_stack",
		TargetKey:     clusterID,
		OperationType: opType,
		Payload:       payload,
		Status:        OpStatusPending,
		CreatedByID:   userID,
	}
	return createMonitoringOperationWith(ctx, q, params)
}

type monitoringOperationCreator interface {
	CreateMonitoringOperation(context.Context, sqlc.CreateMonitoringOperationParams) (sqlc.MonitoringOperation, error)
}

type idempotentMonitoringOperationCreator interface {
	CreateMonitoringOperationIdempotent(context.Context, sqlc.CreateMonitoringOperationIdempotentParams) (sqlc.MonitoringOperation, error)
}

var errMonitoringOperationIdempotencyConflict = errors.New("monitoring operation idempotency key identifies a different operation")

func createMonitoringOperationWith(ctx context.Context, q monitoringOperationCreator, params sqlc.CreateMonitoringOperationParams) (sqlc.MonitoringOperation, error) {
	if idem, ok := operationIdempotencyFromContext(ctx); ok {
		if creator, ok := q.(idempotentMonitoringOperationCreator); ok {
			op, err := creator.CreateMonitoringOperationIdempotent(ctx, sqlc.CreateMonitoringOperationIdempotentParams{
				Scope:          idem.scope,
				IdempotencyKey: idem.key,
				TargetType:     params.TargetType,
				TargetKey:      params.TargetKey,
				OperationType:  params.OperationType,
				Payload:        params.Payload,
				Status:         params.Status,
				CreatedByID:    params.CreatedByID,
			})
			if err == nil && op.ID != uuid.Nil && (op.TargetType != params.TargetType || op.TargetKey != params.TargetKey || op.OperationType != params.OperationType || !jsonPayloadEqual(op.Payload, params.Payload)) {
				return sqlc.MonitoringOperation{}, errMonitoringOperationIdempotencyConflict
			}
			return op, err
		}
	}
	return q.CreateMonitoringOperation(ctx, params)
}

func monitoringOperationResponse(op sqlc.MonitoringOperation) map[string]any {
	return map[string]any{
		"id":            op.ID.String(),
		"targetType":    op.TargetType,
		"targetKey":     op.TargetKey,
		"operationType": op.OperationType,
		"status":        op.Status,
		"attemptCount":  op.AttemptCount,
		"startedAt":     nullablePgTime(op.StartedAt),
		"completedAt":   nullablePgTime(op.CompletedAt),
		"errorMessage":  op.ErrorMessage,
		"createdAt":     op.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt":     op.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func monitoringOperationEventsResponse(events []sqlc.MonitoringOperationEvent) []map[string]any {
	out := make([]map[string]any, 0, len(events))
	for _, event := range events {
		out = append(out, map[string]any{
			"id":        event.ID.String(),
			"level":     event.Level,
			"stage":     event.Stage,
			"message":   event.Message,
			"detail":    decodeJSONMap(event.Detail),
			"createdAt": event.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

func (h *MonitoringHandler) latestMonitoringOperation(ctx context.Context, targetType, targetKey string) (map[string]any, bool) {
	if h.queries == nil {
		return nil, false
	}
	op, err := h.queries.GetLatestMonitoringOperationForTarget(ctx, sqlc.GetLatestMonitoringOperationForTargetParams{
		TargetType: targetType,
		TargetKey:  targetKey,
	})
	if err != nil {
		return nil, false
	}
	return monitoringOperationResponse(op), true
}

func (h *MonitoringHandler) controllerSummary(ctx context.Context) (map[string]any, error) {
	if h == nil || h.queries == nil {
		return map[string]any{
			"reconciler": map[string]any{"enabled": false, "queueDepth": 0},
			"operations": map[string]int{},
		}, nil
	}
	ops, err := h.queries.ListMonitoringOperations(ctx, sqlc.ListMonitoringOperationsParams{Limit: 1000, Offset: 0})
	if err != nil {
		return nil, err
	}
	bindings, restricted, err := h.authz.bindingsForContext(ctx)
	if err != nil {
		return nil, err
	}
	opSummary := summarizeOperations(ctx, ops, operationStatusSummaryConfig[sqlc.MonitoringOperation]{
		Status:    func(op sqlc.MonitoringOperation) string { return op.Status },
		CreatedAt: func(op sqlc.MonitoringOperation) time.Time { return op.CreatedAt },
		IsStaleRunning: func(op sqlc.MonitoringOperation, now time.Time) bool {
			return op.StartedAt.Valid && now.Sub(op.StartedAt.Time) > 2*time.Minute
		},
		Include: func(ctx context.Context, op sqlc.MonitoringOperation) bool {
			if !restricted {
				return true
			}
			allowed, err := h.canReadMonitoringOperation(ctx, bindings, op)
			return err == nil && allowed
		},
		Preview: func(ctx context.Context, op sqlc.MonitoringOperation) map[string]any {
			return h.monitoringOperationPreview(ctx, op)
		},
		StaleThresholdSeconds: 120,
	})
	summary := map[string]any{
		"reconciler":         opSummary.reconcilerMap(),
		"operations":         opSummary.Counts,
		"recentFailureCount": opSummary.RecentFailures,
		"recentOperations":   opSummary.Recent,
		"latestFailure":      opSummary.LatestFailure,
	}
	if backend, err := h.queries.GetDefaultMonitoringBackend(ctx); err == nil {
		metadata := decodeJSONMap(backend.AuthConfig)
		summary["backend"] = map[string]any{
			"type":     backend.BackendType,
			"queryUrl": backend.QueryUrl,
			"healthy":  strings.EqualFold(fmt.Sprint(metadata["status"]), "healthy"),
			"status":   firstNonEmptyString(fmt.Sprint(metadata["status"]), "unknown"),
		}
	}
	return summary, nil
}

func (h *MonitoringHandler) monitoringOperationPreview(ctx context.Context, op sqlc.MonitoringOperation) map[string]any {
	resp := monitoringOperationResponse(op)
	if events, err := h.queries.ListMonitoringOperationEvents(ctx, op.ID); err == nil && len(events) > 0 {
		resp["eventsPreview"] = monitoringOperationEventsResponse(lastMonitoringEvents(events, 3))
	}
	return resp
}

func (h *MonitoringHandler) authorizeMonitoringOperationRead(w http.ResponseWriter, r *http.Request, op sqlc.MonitoringOperation) bool {
	return h.authorizeMonitoringOperation(w, r, op, rbac.VerbRead)
}

func (h *MonitoringHandler) authorizeMonitoringOperationUpdate(w http.ResponseWriter, r *http.Request, op sqlc.MonitoringOperation) bool {
	return h.authorizeMonitoringOperation(w, r, op, rbac.VerbUpdate)
}

func (h *MonitoringHandler) authorizeMonitoringOperation(w http.ResponseWriter, r *http.Request, op sqlc.MonitoringOperation, verb rbac.Verb) bool {
	bindings, restricted, err := h.authz.bindingsForContext(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.Forbidden, "Failed to retrieve user permissions")
		return false
	}
	if !restricted {
		return true
	}
	allowed, err := h.canAccessMonitoringOperation(r.Context(), bindings, op, verb)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ResolveError, "Failed to resolve monitoring operation target")
		return false
	}
	if !allowed {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "You do not have permission to access this operation")
		return false
	}
	return true
}

func (h *MonitoringHandler) canReadMonitoringOperation(ctx context.Context, bindings []rbac.RoleBinding, op sqlc.MonitoringOperation) (bool, error) {
	return h.canAccessMonitoringOperation(ctx, bindings, op, rbac.VerbRead)
}

func (h *MonitoringHandler) canAccessMonitoringOperation(ctx context.Context, bindings []rbac.RoleBinding, op sqlc.MonitoringOperation, verb rbac.Verb) (bool, error) {
	switch op.TargetType {
	case "shared_thanos", "shared_alertmanager", "shared_grafana", "shared_loki":
		return h.authz.allowsGlobal(bindings, rbac.ResourceMonitoring, verb), nil
	case "cluster_stack":
		clusterID, err := uuid.Parse(op.TargetKey)
		if err != nil {
			return false, err
		}
		return h.authz.allowsCluster(bindings, clusterID, rbac.ResourceMonitoring, verb), nil
	default:
		return h.authz.allowsGlobal(bindings, rbac.ResourceMonitoring, verb), nil
	}
}

func lastMonitoringEvents(events []sqlc.MonitoringOperationEvent, n int) []sqlc.MonitoringOperationEvent {
	if len(events) <= n {
		return events
	}
	return events[len(events)-n:]
}
