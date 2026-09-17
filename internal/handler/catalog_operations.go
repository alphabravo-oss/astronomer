package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"sigs.k8s.io/yaml"
)

func (h *CatalogHandler) ListOperations(w http.ResponseWriter, r *http.Request) {
	limit := queryLimit(r, 50)
	offset := queryOffset(r)
	arg := sqlc.ListCatalogOperationsParams{Limit: int32(limit), Offset: int32(offset)}
	if v := strings.TrimSpace(r.URL.Query().Get("targetType")); v != "" {
		arg.TargetType = pgtype.Text{String: v, Valid: true}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("targetKey")); v != "" {
		arg.TargetKey = pgtype.Text{String: v, Valid: true}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("status")); v != "" {
		arg.Status = pgtype.Text{String: v, Valid: true}
	}
	all, clusterIDs, _, err := h.authz.authorizedScopeIDs(r.Context(), rbac.ResourceCatalog, rbac.VerbRead, rbac.NarrowedClustersWiden)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.Forbidden, "Failed to retrieve user permissions")
		return
	}
	var ops []sqlc.CatalogOperation
	var total int64
	pager, hasPager := h.queries.(catalogOperationPager)
	if all {
		ops, err = h.queries.ListCatalogOperations(r.Context(), arg)
		if err == nil && hasPager {
			total, err = pager.CountCatalogOperations(r.Context(), sqlc.CountCatalogOperationsParams{
				TargetType: arg.TargetType, TargetKey: arg.TargetKey, Status: arg.Status,
			})
		}
	} else {
		if !hasPager {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Scoped catalog-operation pagination is unavailable")
			return
		}
		ops, err = pager.ListCatalogOperationsForScopes(r.Context(), sqlc.ListCatalogOperationsForScopesParams{
			TargetType: arg.TargetType, TargetKey: arg.TargetKey, Status: arg.Status,
			ClusterIds: clusterIDs, QueryLimit: int32(limit), QueryOffset: int32(offset),
		})
		if err == nil {
			total, err = pager.CountCatalogOperationsForScopes(r.Context(), sqlc.CountCatalogOperationsForScopesParams{
				TargetType: arg.TargetType, TargetKey: arg.TargetKey, Status: arg.Status, ClusterIds: clusterIDs,
			})
		}
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list catalog operations")
		return
	}
	items := make([]map[string]any, 0, len(ops))
	for _, op := range ops {
		items = append(items, catalogOperationResponse(op))
	}
	if !hasPager {
		paging.Write(w, items, paging.FromPage(limit, offset, len(ops)))
		return
	}
	paging.Write(w, items, paging.Exact(total, limit, offset, len(ops)))
}

func (h *CatalogHandler) GetOperation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid operation ID")
		return
	}
	op, err := h.queries.GetCatalogOperation(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Catalog operation not found")
		return
	}
	clusterID, err := catalogOperationClusterID(op)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ResolveError, "Failed to resolve catalog operation target")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceCatalog, rbac.VerbRead) {
		return
	}
	resp := catalogOperationResponse(op)
	if events, err := h.queries.ListCatalogOperationEvents(r.Context(), op.ID); err == nil {
		resp["events"] = catalogOperationEventsResponse(events)
	}
	RespondJSON(w, http.StatusOK, resp)
}

func (h *CatalogHandler) RetryOperation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid operation ID")
		return
	}
	op, err := h.queries.GetCatalogOperation(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Catalog operation not found")
		return
	}
	if !requireRetryableOperation(w, r, op.Status) {
		return
	}
	clusterID, err := catalogOperationClusterID(op)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ResolveError, "Failed to resolve catalog operation target")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceCatalog, rbac.VerbUpdate) {
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	requeued, err := executeMutation(r, h.runTx,
		func(q CatalogMutationTx) (sqlc.CatalogOperation, error) {
			return q.RequeueCatalogOperation(r.Context(), id)
		},
		func(row sqlc.CatalogOperation) mutationAuditEvent {
			return mutationAuditEvent{action: "catalog.operation.retry", resourceType: "catalog_operation", resourceID: id.String(), resourceName: op.TargetKey, status: http.StatusAccepted, detail: map[string]any{
				"target_type": op.TargetType, "previous_status": op.Status,
			}}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.RetryError, "Failed to retry catalog operation")
		return
	}
	h.TriggerReconcile()
	RespondAcceptedOperation(w, "/api/v1/catalog/operations/"+requeued.ID.String()+"/", catalogOperationResponse(requeued))
}

func catalogOperationClusterID(op sqlc.CatalogOperation) (uuid.UUID, error) {
	var env catalogOperationEnvelope
	if err := json.Unmarshal(op.Payload, &env); err != nil {
		return uuid.UUID{}, err
	}
	return uuid.Parse(env.ClusterID)
}

func (h *CatalogHandler) ControllerStatus(w http.ResponseWriter, r *http.Request) {
	summary, err := h.controllerSummary(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.StatusError, "Failed to load catalog operations")
		return
	}
	RespondJSON(w, http.StatusOK, summary)
}

func (h *CatalogHandler) controllerSummary(ctx context.Context) (map[string]any, error) {
	ops, err := h.queries.ListCatalogOperations(ctx, sqlc.ListCatalogOperationsParams{Limit: 1000, Offset: 0})
	if err != nil {
		return nil, err
	}
	bindings, restricted, err := h.authz.bindingsForContext(ctx)
	if err != nil {
		return nil, err
	}
	opSummary := summarizeOperations(ctx, ops, operationStatusSummaryConfig[sqlc.CatalogOperation]{
		Status:    func(op sqlc.CatalogOperation) string { return op.Status },
		CreatedAt: func(op sqlc.CatalogOperation) time.Time { return op.CreatedAt },
		IsStaleRunning: func(op sqlc.CatalogOperation, now time.Time) bool {
			return op.StartedAt.Valid && now.Sub(op.StartedAt.Time) > time.Minute
		},
		Include: func(_ context.Context, op sqlc.CatalogOperation) bool {
			if !restricted {
				return true
			}
			clusterID, err := catalogOperationClusterID(op)
			return err == nil && h.authz.allowsCluster(bindings, clusterID, rbac.ResourceCatalog, rbac.VerbRead)
		},
		Preview:               func(ctx context.Context, op sqlc.CatalogOperation) map[string]any { return h.operationPreview(ctx, op) },
		StaleThresholdSeconds: 60,
	})
	charts, _ := h.queries.CountHelmCharts(ctx)
	installed, _ := h.queries.CountInstalledCharts(ctx)
	return map[string]any{
		"reconciler": opSummary.reconcilerMap(),
		"catalog": map[string]any{
			"chartCount": charts,
			"installedCount": func() any {
				if restricted {
					return nil
				}
				return installed
			}(),
		},
		"operations":         opSummary.Counts,
		"recentFailureCount": opSummary.RecentFailures,
		"recentOperations":   opSummary.Recent,
		"latestFailure":      opSummary.LatestFailure,
	}, nil
}

type catalogOperationCreator interface {
	CreateCatalogOperation(context.Context, sqlc.CreateCatalogOperationParams) (sqlc.CatalogOperation, error)
}

type idempotentCatalogOperationCreator interface {
	CreateCatalogOperationIdempotent(context.Context, sqlc.CreateCatalogOperationIdempotentParams) (sqlc.CatalogOperation, error)
}

type dispositionCatalogOperationCreator interface {
	CreateCatalogOperationIdempotentWithDisposition(context.Context, sqlc.CreateCatalogOperationIdempotentWithDispositionParams) (sqlc.CreateCatalogOperationIdempotentWithDispositionRow, error)
}

var errCatalogOperationIdempotencyConflict = errors.New("catalog operation idempotency key already identifies a committed operation")

func respondCatalogMutationError(w http.ResponseWriter, r *http.Request, err error, fallbackCode, fallbackMessage string) {
	if errors.Is(err, errCatalogOperationIdempotencyConflict) {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict,
			"Idempotency-Key already identifies a catalog operation; retrieve the existing operation instead of restaging it")
		return
	}
	respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, fallbackCode, fallbackMessage)
}

func createCatalogOperation(ctx context.Context, q catalogOperationCreator, targetType, targetKey, operationType string, env catalogOperationEnvelope, userID pgtype.UUID) (sqlc.CatalogOperation, error) {
	payload, err := json.Marshal(env)
	if err != nil {
		return sqlc.CatalogOperation{}, err
	}
	params := sqlc.CreateCatalogOperationParams{
		TargetType:    targetType,
		TargetKey:     targetKey,
		OperationType: operationType,
		Payload:       payload,
		Status:        OpStatusPending,
		CreatedByID:   userID,
	}
	var op sqlc.CatalogOperation
	if idem, ok := operationIdempotencyFromContext(ctx); ok {
		if creator, ok := q.(dispositionCatalogOperationCreator); ok {
			result, createErr := creator.CreateCatalogOperationIdempotentWithDisposition(ctx, sqlc.CreateCatalogOperationIdempotentWithDispositionParams{
				Scope: idem.scope, IdempotencyKey: idem.key, TargetType: params.TargetType, TargetKey: params.TargetKey,
				OperationType: params.OperationType, Payload: params.Payload, Status: params.Status, CreatedByID: params.CreatedByID,
			})
			if createErr != nil {
				return sqlc.CatalogOperation{}, createErr
			}
			if !result.Inserted {
				return sqlc.CatalogOperation{}, errCatalogOperationIdempotencyConflict
			}
			op = result.CatalogOperation
		} else if creator, ok := q.(idempotentCatalogOperationCreator); ok {
			op, err = creator.CreateCatalogOperationIdempotent(ctx, sqlc.CreateCatalogOperationIdempotentParams{
				Scope:          idem.scope,
				IdempotencyKey: idem.key,
				TargetType:     params.TargetType,
				TargetKey:      params.TargetKey,
				OperationType:  params.OperationType,
				Payload:        params.Payload,
				Status:         params.Status,
				CreatedByID:    params.CreatedByID,
			})
			if err == nil && op.ID != uuid.Nil && (op.TargetType != params.TargetType || op.TargetKey != params.TargetKey || op.OperationType != params.OperationType || !bytes.Equal(op.Payload, params.Payload)) {
				return sqlc.CatalogOperation{}, errCatalogOperationIdempotencyConflict
			}
		}
	}
	if op.ID == uuid.Nil && err == nil {
		op, err = q.CreateCatalogOperation(ctx, params)
	}
	return op, err
}

func catalogOperationResponse(op sqlc.CatalogOperation) map[string]any {
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

func catalogOperationEventsResponse(events []sqlc.CatalogOperationEvent) []map[string]any {
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

func (h *CatalogHandler) operationPreview(ctx context.Context, op sqlc.CatalogOperation) map[string]any {
	resp := catalogOperationResponse(op)
	if events, err := h.queries.ListCatalogOperationEvents(ctx, op.ID); err == nil && len(events) > 0 {
		resp["eventsPreview"] = catalogOperationEventsResponse(lastCatalogEvents(events, 3))
	}
	return resp
}

func lastCatalogEvents(events []sqlc.CatalogOperationEvent, n int) []sqlc.CatalogOperationEvent {
	if len(events) <= n {
		return events
	}
	return events[len(events)-n:]
}

func (h *CatalogHandler) processPendingOperations(ctx context.Context) {
	// Claim under the lock, then release before
	// the (potentially 10-minute) helm dispatch so other clusters'
	// operations are not stalled behind one stuck install.
	dispatchClaimed(ctx, h.helmConcurrency, h.claimPendingCatalogOperations(ctx))
}

// claimPendingCatalogOperations holds h.mu just long enough to mark
// supersession + claim the batch ("running" state). Returns the rows
// it owns wrapped as claimedOps; dispatchClaimed runs them outside the
// lock via per-row Run/OnComplete/OnFailure closures.
func (h *CatalogHandler) claimPendingCatalogOperations(ctx context.Context) []claimedOp {
	h.mu.Lock()
	defer h.mu.Unlock()
	ops, err := h.queries.ListPendingCatalogOperations(ctx, 20)
	if err != nil {
		return nil
	}
	return claimLatestOperations(ctx, ops, operationRunnerConfig[sqlc.CatalogOperation]{
		ID:        func(op sqlc.CatalogOperation) uuid.UUID { return op.ID },
		TargetKey: func(op sqlc.CatalogOperation) string { return op.TargetType + ":" + op.TargetKey },
		Status:    func(op sqlc.CatalogOperation) string { return op.Status },
		IsFreshRunning: func(op sqlc.CatalogOperation, now time.Time) bool {
			return op.StartedAt.Valid && now.Sub(op.StartedAt.Time) < time.Minute
		},
		Supersede: func(ctx context.Context, op sqlc.CatalogOperation) {
			h.recordCatalogOperationEvent(ctx, op.ID, "info", "queue", "operation superseded by newer desired state", map[string]any{
				"targetType": op.TargetType,
				"targetKey":  op.TargetKey,
			})
			_, _ = h.queries.MarkCatalogOperationSuperseded(ctx, sqlc.MarkCatalogOperationSupersededParams{
				ID:           op.ID,
				ErrorMessage: operationSupersededMessage,
			})
		},
		MarkRunning: func(ctx context.Context, op sqlc.CatalogOperation) (sqlc.CatalogOperation, error) {
			running, err := h.queries.MarkCatalogOperationRunning(ctx, op.ID)
			if err != nil {
				return sqlc.CatalogOperation{}, err
			}
			h.recordCatalogOperationEvent(ctx, running.ID, "info", "queue", "operation execution started", map[string]any{
				"operationType": running.OperationType,
				"targetType":    running.TargetType,
				"targetKey":     running.TargetKey,
				"attemptCount":  running.AttemptCount,
			})
			return running, nil
		},
		Claimed: func(running sqlc.CatalogOperation) claimedOp {
			return claimedOp{
				ID: running.ID,
				Run: func(ctx context.Context) error {
					return h.executeOperation(ctx, running)
				},
				OnComplete: func(ctx context.Context) {
					h.recordCatalogOperationEvent(ctx, running.ID, "info", "complete", "operation completed", map[string]any{})
					_, _ = h.queries.MarkCatalogOperationCompleted(ctx, running.ID)
				},
				OnFailure: func(ctx context.Context, err error) {
					h.recordCatalogOperationEvent(ctx, running.ID, "error", "complete", "operation failed", map[string]any{"error": err.Error()})
					_, _ = h.queries.MarkCatalogOperationFailed(ctx, sqlc.MarkCatalogOperationFailedParams{
						ID:           running.ID,
						ErrorMessage: err.Error(),
					})
					if h.log != nil {
						h.log.Warn("catalog operation failed", "id", running.ID.String(), "error", err)
					}
				},
			}
		},
	})
}

func (h *CatalogHandler) executeOperation(ctx context.Context, op sqlc.CatalogOperation) error {
	if h.helm == nil {
		return errors.New("helm requester not configured")
	}
	var env catalogOperationEnvelope
	if err := json.Unmarshal(op.Payload, &env); err != nil {
		return err
	}
	installationID, err := uuid.Parse(env.InstalledChartID)
	if err != nil {
		return err
	}
	installation, err := h.queries.GetInstalledChartByID(ctx, installationID)
	if err != nil {
		return err
	}
	clusterID := installation.ClusterID.String()
	// Every path below writes a terminal installed-chart status (success or
	// failed_*), so one deferred publish covers them all (P4.9).
	defer h.publishCatalogReleaseChanged(clusterID, installation.ID.String())
	switch op.OperationType {
	case "install":
		h.recordCatalogOperationEvent(ctx, op.ID, "info", "install", "installing catalog release", map[string]any{
			"clusterId":   clusterID,
			"releaseName": installation.ReleaseName,
			"namespace":   installation.Namespace,
		})
		result, err := h.sendHelm(ctx, clusterID, protocol.MsgHelmInstall, env)
		if err != nil {
			_ = h.queries.UpdateInstalledChartStatus(ctx, sqlc.UpdateInstalledChartStatusParams{ID: installation.ID, Status: "failed_install", Revision: installation.Revision})
			return err
		}
		return h.queries.UpdateInstalledChartStatus(ctx, sqlc.UpdateInstalledChartStatusParams{
			ID:       installation.ID,
			Status:   normalizeToolStatus(result.Status),
			Revision: int32(result.Revision),
		})
	case "upgrade":
		h.recordCatalogOperationEvent(ctx, op.ID, "info", "upgrade", "upgrading catalog release", map[string]any{
			"clusterId":   clusterID,
			"releaseName": installation.ReleaseName,
			"namespace":   installation.Namespace,
		})
		result, err := h.sendHelm(ctx, clusterID, protocol.MsgHelmUpgrade, env)
		if err != nil {
			_ = h.queries.UpdateInstalledChartStatus(ctx, sqlc.UpdateInstalledChartStatusParams{ID: installation.ID, Status: "failed_upgrade", Revision: installation.Revision})
			return err
		}
		_, err = h.queries.UpdateInstalledChartValues(ctx, sqlc.UpdateInstalledChartValuesParams{
			ID:             installation.ID,
			ValuesOverride: env.ValuesOverride,
			Status:         normalizeToolStatus(result.Status),
			Revision:       int32(result.Revision),
		})
		return err
	case "rollback":
		h.recordCatalogOperationEvent(ctx, op.ID, "info", "rollback", "rolling back catalog release", map[string]any{
			"clusterId":        clusterID,
			"releaseName":      installation.ReleaseName,
			"namespace":        installation.Namespace,
			"rollbackRevision": env.RollbackRevision,
		})
		result, err := h.helm.Do(ctx, clusterID, protocol.MsgHelmRollback, protocol.HelmRequestPayload{
			ReleaseName: installation.ReleaseName,
			Namespace:   installation.Namespace,
			Revision:    env.RollbackRevision,
		})
		if err != nil {
			_ = h.queries.UpdateInstalledChartStatus(ctx, sqlc.UpdateInstalledChartStatusParams{ID: installation.ID, Status: "failed_rollback", Revision: installation.Revision})
			return err
		}
		return h.queries.UpdateInstalledChartStatus(ctx, sqlc.UpdateInstalledChartStatusParams{
			ID:       installation.ID,
			Status:   normalizeToolStatus(result.Status),
			Revision: int32(result.Revision),
		})
	case "uninstall":
		h.recordCatalogOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling catalog release", map[string]any{
			"clusterId":   clusterID,
			"releaseName": installation.ReleaseName,
			"namespace":   installation.Namespace,
		})
		_, err := h.helm.Do(ctx, clusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{
			ReleaseName: installation.ReleaseName,
			Namespace:   installation.Namespace,
		})
		if err != nil {
			_ = h.queries.UpdateInstalledChartStatus(ctx, sqlc.UpdateInstalledChartStatusParams{ID: installation.ID, Status: "failed_uninstall", Revision: installation.Revision})
			return err
		}
		return h.queries.DeleteInstalledChart(ctx, installation.ID)
	default:
		return fmt.Errorf("unsupported catalog operation type: %s", op.OperationType)
	}
}

// checkCatalogMaintenanceWindow consults the migration-057 gate and
// writes the 409/202 response when the operation is blocked. Returns
// true when the caller should stop. Best-effort cluster lookup —
// failing to resolve labels leaves the selector check on an empty
// label set rather than erroring the user out at gate time.
func (h *CatalogHandler) checkCatalogMaintenanceWindow(w http.ResponseWriter, r *http.Request, clusterID uuid.UUID, opType string) bool {
	if h == nil || h.maintenanceGate == nil {
		return false
	}
	labels := map[string]string{}
	if cluster, err := h.queries.GetClusterByID(r.Context(), clusterID); err == nil {
		labels = MaintenanceGateClusterLabels(cluster)
	}
	return EnforceMaintenanceWindow(w, r, h.maintenanceGate, opType, labels,
		pgtype.UUID{Bytes: clusterID, Valid: true}, pgtype.UUID{})
}

func (h *CatalogHandler) sendHelm(ctx context.Context, clusterID string, msgType protocol.MessageType, env catalogOperationEnvelope) (*protocol.HelmResultPayload, error) {
	// Resolve ${vault://...} markers at execution time. The operation
	// payload (and the installed_charts row) persist the ORIGINAL
	// marker-bearing blob, so no cleartext secret ever lands in
	// catalog_operations.payload; substitution happens here, in-memory,
	// right before the values are shipped to the cluster. This is also
	// the path that resolves markers on the UPGRADE flow, which
	// previously shipped the literal placeholder through to Helm.
	blob, err := vaultResolveBlob(ctx, h.vaultResolver, uuid.Nil, env.ValuesOverride)
	if err != nil {
		return nil, err
	}
	var values map[string]any
	if blob != "" {
		if err := yaml.Unmarshal([]byte(blob), &values); err != nil {
			return nil, err
		}
	}
	return h.helm.Do(ctx, clusterID, msgType, protocol.HelmRequestPayload{
		ReleaseName: env.ReleaseName,
		Namespace:   env.Namespace,
		ChartName:   env.ChartName,
		RepoURL:     env.RepoURL,
		Version:     env.Version,
		Values:      values,
	})
}

func (h *CatalogHandler) resolveInstalledChartRelease(ctx context.Context, installed sqlc.InstalledChart) (sqlc.HelmChartVersion, sqlc.HelmChart, sqlc.HelmRepository, error) {
	if !installed.ChartVersionID.Valid {
		return sqlc.HelmChartVersion{}, sqlc.HelmChart{}, sqlc.HelmRepository{}, errors.New("installed chart has no chart version")
	}
	versionID := uuid.UUID(installed.ChartVersionID.Bytes)
	version, err := h.queries.GetHelmChartVersionByID(ctx, versionID)
	if err != nil {
		return sqlc.HelmChartVersion{}, sqlc.HelmChart{}, sqlc.HelmRepository{}, err
	}
	chart, err := h.queries.GetHelmChartByID(ctx, version.ChartID)
	if err != nil {
		return sqlc.HelmChartVersion{}, sqlc.HelmChart{}, sqlc.HelmRepository{}, err
	}
	repo, err := h.queries.GetHelmRepositoryByID(ctx, chart.RepositoryID)
	if err != nil {
		return sqlc.HelmChartVersion{}, sqlc.HelmChart{}, sqlc.HelmRepository{}, err
	}
	return version, chart, repo, nil
}

func uuidFromPg(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}

func (h *CatalogHandler) recordCatalogOperationEvent(ctx context.Context, operationID uuid.UUID, level, stage, message string, detail map[string]any) {
	if h == nil || h.queries == nil {
		return
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		raw = json.RawMessage(`{}`)
	}
	_, _ = h.queries.CreateCatalogOperationEvent(ctx, sqlc.CreateCatalogOperationEventParams{
		OperationID: operationID,
		Level:       level,
		Stage:       stage,
		Message:     message,
		Detail:      raw,
	})
}

type catalogOperationPager interface {
	CountCatalogOperations(ctx context.Context, arg sqlc.CountCatalogOperationsParams) (int64, error)
	ListCatalogOperationsForScopes(ctx context.Context, arg sqlc.ListCatalogOperationsForScopesParams) ([]sqlc.CatalogOperation, error)
	CountCatalogOperationsForScopes(ctx context.Context, arg sqlc.CountCatalogOperationsForScopesParams) (int64, error)
}

func (h *CatalogHandler) StartReconciler(ctx context.Context) {
	if h == nil || h.queries == nil {
		return
	}
	go h.RunReconciler(ctx)
}

func (h *CatalogHandler) RunReconciler(ctx context.Context) {
	if h == nil || h.queries == nil {
		return
	}
	h.runReconciler(ctx)
}

func (h *CatalogHandler) TriggerReconcile() {
	if h == nil || h.trigger == nil {
		return
	}
	select {
	case h.trigger <- struct{}{}:
	default:
	}
}

func (h *CatalogHandler) runReconciler(ctx context.Context) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	h.processPendingOperations(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.processPendingOperations(ctx)
		case <-h.trigger:
			h.processPendingOperations(ctx)
		}
	}
}

type catalogOperationEnvelope struct {
	InstalledChartID string `json:"installedChartId"`
	ClusterID        string `json:"clusterId"`
	ReleaseName      string `json:"releaseName"`
	Namespace        string `json:"namespace"`
	ChartVersionID   string `json:"chartVersionId,omitempty"`
	ChartName        string `json:"chartName,omitempty"`
	RepoURL          string `json:"repoUrl,omitempty"`
	Version          string `json:"version,omitempty"`
	ValuesOverride   string `json:"valuesOverride,omitempty"`
	Notes            string `json:"notes,omitempty"`
	RollbackRevision int    `json:"rollbackRevision,omitempty"`
}
