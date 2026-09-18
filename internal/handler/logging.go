package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// LoggingNamespace is the namespace on the managed cluster where rendered
// Fluent Bit / output ConfigMaps live. We assume Fluent Bit is already
// installed and watching this namespace; installing it is out of scope for
// this controller (it's the agent's / platform-bootstrap's job).
const LoggingNamespace = "astronomer-logging"

// loggingReconcileInterval is how often the background loop sweeps the
// pending-operations queue. Matches the catalog/tools cadence so operators
// see consistent reconcile timing across subsystems.
const loggingReconcileInterval = 30 * time.Second

// LoggingQuerier abstracts the logging-related database queries needed by LoggingHandler.
type LoggingQuerier interface {
	// Outputs
	ListLoggingOutputs(ctx context.Context, arg sqlc.ListLoggingOutputsParams) ([]sqlc.LoggingOutput, error)
	ListOutputsByCluster(ctx context.Context, arg sqlc.ListOutputsByClusterParams) ([]sqlc.LoggingOutput, error)
	GetLoggingOutputByID(ctx context.Context, id uuid.UUID) (sqlc.LoggingOutput, error)
	CreateLoggingOutput(ctx context.Context, arg sqlc.CreateLoggingOutputParams) (sqlc.LoggingOutput, error)
	UpdateLoggingOutput(ctx context.Context, arg sqlc.UpdateLoggingOutputParams) (sqlc.LoggingOutput, error)
	DeleteLoggingOutput(ctx context.Context, id uuid.UUID) error
	CountLoggingOutputs(ctx context.Context) (int64, error)
	CountOutputsByCluster(ctx context.Context, clusterID pgtype.UUID) (int64, error)
	GetSystemLoggingOutputByCluster(ctx context.Context, clusterID pgtype.UUID) (sqlc.LoggingOutput, error)
	ListSystemLoggingOutputs(ctx context.Context) ([]sqlc.LoggingOutput, error)
	DisableSystemLoggingOutputs(ctx context.Context) ([]sqlc.LoggingOutput, error)
	// Pipelines
	ListLoggingPipelines(ctx context.Context, arg sqlc.ListLoggingPipelinesParams) ([]sqlc.LoggingPipeline, error)
	ListPipelinesByCluster(ctx context.Context, arg sqlc.ListPipelinesByClusterParams) ([]sqlc.LoggingPipeline, error)
	GetLoggingPipelineByID(ctx context.Context, id uuid.UUID) (sqlc.LoggingPipeline, error)
	ListLoggingPipelineOutputDetails(ctx context.Context, pipelineIDs []uuid.UUID) ([]sqlc.ListLoggingPipelineOutputDetailsRow, error)
	CreateLoggingPipeline(ctx context.Context, arg sqlc.CreateLoggingPipelineParams) (sqlc.LoggingPipeline, error)
	UpdateLoggingPipeline(ctx context.Context, arg sqlc.UpdateLoggingPipelineParams) (sqlc.LoggingPipeline, error)
	ReplaceLoggingPipelineOutputs(ctx context.Context, arg sqlc.ReplaceLoggingPipelineOutputsParams) (int64, error)
	DeleteLoggingPipeline(ctx context.Context, id uuid.UUID) error
	CountLoggingPipelines(ctx context.Context) (int64, error)
	CountPipelinesByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
	// Operations
	CreateLoggingOperation(ctx context.Context, arg sqlc.CreateLoggingOperationParams) (sqlc.LoggingOperation, error)
	GetLoggingOperation(ctx context.Context, id uuid.UUID) (sqlc.LoggingOperation, error)
	ListLoggingOperations(ctx context.Context, arg sqlc.ListLoggingOperationsParams) ([]sqlc.LoggingOperation, error)
	ListPendingLoggingOperations(ctx context.Context, limit int32) ([]sqlc.LoggingOperation, error)
	MarkLoggingOperationRunning(ctx context.Context, id uuid.UUID) (sqlc.LoggingOperation, error)
	MarkLoggingOperationCompleted(ctx context.Context, id uuid.UUID) (sqlc.LoggingOperation, error)
	MarkLoggingOperationFailed(ctx context.Context, arg sqlc.MarkLoggingOperationFailedParams) (sqlc.LoggingOperation, error)
	MarkLoggingOperationSuperseded(ctx context.Context, arg sqlc.MarkLoggingOperationSupersededParams) (sqlc.LoggingOperation, error)
	RequeueLoggingOperation(ctx context.Context, id uuid.UUID) (sqlc.LoggingOperation, error)
	CreateLoggingOperationEvent(ctx context.Context, arg sqlc.CreateLoggingOperationEventParams) (sqlc.LoggingOperationEvent, error)
	ListLoggingOperationEvents(ctx context.Context, operationID uuid.UUID) ([]sqlc.LoggingOperationEvent, error)
	GetLokiIngestTokenByCluster(ctx context.Context, clusterID uuid.UUID) (sqlc.LokiIngestToken, error)
	UpsertLokiIngestToken(ctx context.Context, arg sqlc.UpsertLokiIngestTokenParams) (sqlc.LokiIngestToken, error)
	ListLokiIngestTokenHashes(ctx context.Context) ([]sqlc.ListLokiIngestTokenHashesRow, error)
}

type loggingOperationPager interface {
	CountLoggingOperations(ctx context.Context, arg sqlc.CountLoggingOperationsParams) (int64, error)
	ListLoggingOperationsForScopes(ctx context.Context, arg sqlc.ListLoggingOperationsForScopesParams) ([]sqlc.LoggingOperation, error)
	CountLoggingOperationsForScopes(ctx context.Context, arg sqlc.CountLoggingOperationsForScopesParams) (int64, error)
}

// LoggingMutationTx is the transaction-bound write surface for logging
// configuration. Production supplies sqlc.New(tx), so desired state, the
// durable reconciliation operation, and mandatory audit intent share one
// commit decision.
type LoggingMutationTx interface {
	audit.OutboxQuerier
	CreateLoggingOutput(context.Context, sqlc.CreateLoggingOutputParams) (sqlc.LoggingOutput, error)
	UpdateLoggingOutput(context.Context, sqlc.UpdateLoggingOutputParams) (sqlc.LoggingOutput, error)
	DeleteLoggingOutput(context.Context, uuid.UUID) error
	CreateLoggingPipeline(context.Context, sqlc.CreateLoggingPipelineParams) (sqlc.LoggingPipeline, error)
	UpdateLoggingPipeline(context.Context, sqlc.UpdateLoggingPipelineParams) (sqlc.LoggingPipeline, error)
	ReplaceLoggingPipelineOutputs(context.Context, sqlc.ReplaceLoggingPipelineOutputsParams) (int64, error)
	DeleteLoggingPipeline(context.Context, uuid.UUID) error
	CreateLoggingOperation(context.Context, sqlc.CreateLoggingOperationParams) (sqlc.LoggingOperation, error)
	CreateLoggingOperationIdempotent(context.Context, sqlc.CreateLoggingOperationIdempotentParams) (sqlc.LoggingOperation, error)
	CreateLoggingOperationIdempotentWithDisposition(context.Context, sqlc.CreateLoggingOperationIdempotentWithDispositionParams) (sqlc.CreateLoggingOperationIdempotentWithDispositionRow, error)
	RequeueLoggingOperation(context.Context, uuid.UUID) (sqlc.LoggingOperation, error)
	UpsertLokiIngestToken(context.Context, sqlc.UpsertLokiIngestTokenParams) (sqlc.LokiIngestToken, error)
	GetSystemLoggingOutputByCluster(context.Context, pgtype.UUID) (sqlc.LoggingOutput, error)
	GetLoggingSavedSearchForOwner(context.Context, sqlc.GetLoggingSavedSearchForOwnerParams) (sqlc.LoggingSavedSearch, error)
	CreateLoggingSavedSearch(context.Context, sqlc.CreateLoggingSavedSearchParams) (sqlc.LoggingSavedSearch, error)
	UpdateLoggingSavedSearch(context.Context, sqlc.UpdateLoggingSavedSearchParams) (sqlc.LoggingSavedSearch, error)
	DeleteLoggingSavedSearch(context.Context, sqlc.DeleteLoggingSavedSearchParams) (int64, error)
}

type loggingRunTxFunc func(context.Context, func(LoggingMutationTx) error) error

type loggingMutationResult[T any] struct {
	row T
	op  sqlc.LoggingOperation
}

// LoggingHandler handles logging output and pipeline endpoints.
//
// As of the logging-controller refactor (comparison.md §7/§10/§11) the
// handler no longer applies to the cluster inline: it writes intent rows to
// the `logging_operations` table and a background reconciler picks them up.
// The reconciler renders ConfigMaps for each output/pipeline, applies a
// member Secret for hosted Loki ingest tokens, and patches the baseline
// fluent-bit Helm extraVolumes/extraVolumeMounts so OUTPUT uses
// bearer_token_file. Fluent Bit itself is assumed already installed.
type LoggingHandler struct {
	queries   LoggingQuerier
	runTx     loggingRunTxFunc
	requester K8sRequester
	helm      HelmRequester
	log       *slog.Logger
	authz     authorizationSupport
	bus       *events.Bus
	mu        sync.Mutex
	trigger   chan struct{}
	// helmConcurrency caps the parallel dispatch fan-out for
	// executeOperation; zero falls back to the package default.
	helmConcurrency int
	encryptor       *auth.Encryptor
	lokiIngest      lokiIngestReconciler
	lokiAttach      lokiAttachGate
	// querySlots bounds expensive remote log queries across all providers.
	// A handler-local semaphore keeps one noisy tenant from exhausting the
	// server's outbound connection pool.
	querySlots chan struct{}
}

// SetRunTx wires the production transaction boundary used for logging state,
// operation intent, and mandatory audit intent.
func (h *LoggingHandler) SetRunTx(runTx loggingRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *LoggingHandler) TransactionalAuditWired() bool { return h != nil && h.runTx != nil }

var (
	errLoggingOperationIdempotencyConflict = errors.New("logging operation idempotency key already identifies a committed operation")
	errLoggingPipelineOutputsInvalid       = errors.New("logging pipeline outputs are invalid")
)

func respondLoggingMutationError(w http.ResponseWriter, r *http.Request, err error, fallbackStatus int, fallbackCode, fallbackMessage string) {
	if errors.Is(err, errLoggingOperationIdempotencyConflict) {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict,
			"Idempotency-Key already identifies a logging operation; retrieve the existing operation instead of restaging it")
		return
	}
	if errors.Is(err, errLoggingPipelineOutputsInvalid) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody,
			"Every logging pipeline must reference one or more outputs from the same cluster")
		return
	}
	respondTransactionalMutationError(w, r, err, fallbackStatus, fallbackCode, fallbackMessage)
}

type lokiIngestReconciler interface {
	ReconcileLokiIngest(ctx context.Context) error
}

// lokiAttachGate is the hosted-Loki precheck used by one-click attach.
// MonitoringHandler implements it. Nil is fail-closed (Loki not ready).
type lokiAttachGate interface {
	LokiAttachState(ctx context.Context) lokiAttachState
	CheckLokiAttachCapacity(ctx context.Context, clusterID uuid.UUID) (code, msg string, ok bool)
}

type lokiAttachState struct {
	Status              string
	IngestPublic        bool
	Host                string
	Port                string
	Mode                string
	ManagementClusterID string
	Namespace           string
	ReleaseName         string
}

// NewLoggingHandler creates a new logging handler.
func NewLoggingHandler(queries LoggingQuerier) *LoggingHandler {
	return &LoggingHandler{
		queries:    queries,
		log:        slog.Default(),
		trigger:    make(chan struct{}, 1),
		querySlots: make(chan struct{}, 8),
	}
}

// SetK8sRequester wires the tunnel-backed K8sRequester the reconciler uses
// to apply ConfigMaps into managed clusters. Without it the reconciler still
// runs but fails apply operations with a clear error.
func (h *LoggingHandler) SetK8sRequester(r K8sRequester) {
	if h == nil {
		return
	}
	h.requester = r
}

// SetHelmRequester wires the tunnel Helm client used to patch the baseline
// fluent-bit release (extraVolumes / extraVolumeMounts for ingest tokens).
func (h *LoggingHandler) SetHelmRequester(r HelmRequester) {
	if h == nil {
		return
	}
	h.helm = r
}

func (h *LoggingHandler) SetEncryptor(e *auth.Encryptor) {
	if h == nil {
		return
	}
	h.encryptor = e
}

func (h *LoggingHandler) SetLokiIngestReconciler(r lokiIngestReconciler) {
	if h == nil {
		return
	}
	h.lokiIngest = r
}

func (h *LoggingHandler) SetLokiAttachGate(g lokiAttachGate) {
	if h == nil {
		return
	}
	h.lokiAttach = g
}

// SetLogger wires a structured logger.
func (h *LoggingHandler) SetLogger(log *slog.Logger) {
	if h == nil || log == nil {
		return
	}
	h.log = log
}

// SetAuthorization wires per-cluster RBAC for the operations endpoints.
// Matches the catalog/tools pattern so the same engine + querier
// instances are shared across handlers.
func (h *LoggingHandler) SetAuthorization(engine *rbac.Engine, querier rbac.BindingQuerier) {
	if h == nil {
		return
	}
	h.authz.SetAuthorization(engine, querier)
}

// StartReconciler launches the background loop that processes pending logging
// operations. Safe to call before SetK8sRequester — the reconciler will
// surface a "tunnel not configured" error per attempted apply until wired.
func (h *LoggingHandler) StartReconciler(ctx context.Context) {
	if h == nil || h.queries == nil {
		return
	}
	go h.RunReconciler(ctx)
}

func (h *LoggingHandler) RunReconciler(ctx context.Context) {
	if h == nil || h.queries == nil {
		return
	}
	h.runReconciler(ctx)
}

// TriggerReconcile nudges the reconciler so newly-enqueued operations don't
// wait for the next tick. Non-blocking; if a wakeup is already pending it
// silently drops.
func (h *LoggingHandler) TriggerReconcile() {
	if h == nil || h.trigger == nil {
		return
	}
	select {
	case h.trigger <- struct{}{}:
	default:
	}
}

func (h *LoggingHandler) runReconciler(ctx context.Context) {
	ticker := time.NewTicker(loggingReconcileInterval)
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

// ControllerStatus summarizes logging subsystem configuration state.
func (h *LoggingHandler) ControllerStatus(w http.ResponseWriter, r *http.Request) {
	summary, err := h.controllerSummary(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.StatusError, "Failed to load logging outputs")
		return
	}
	RespondJSON(w, http.StatusOK, summary)
}

func (h *LoggingHandler) controllerSummary(ctx context.Context) (map[string]any, error) {
	outputs, err := h.queries.ListLoggingOutputs(ctx, sqlc.ListLoggingOutputsParams{Limit: 1000, Offset: 0})
	if err != nil {
		return nil, err
	}
	pipelines, err := h.queries.ListLoggingPipelines(ctx, sqlc.ListLoggingPipelinesParams{Limit: 1000, Offset: 0})
	if err != nil {
		return nil, err
	}
	outputTypes := map[string]int{}
	clusterOutputs := map[string]int{}
	enabledOutputs := 0
	for _, output := range outputs {
		outputTypes[output.OutputType]++
		if output.Enabled {
			enabledOutputs++
		}
		if output.ClusterID.Valid {
			clusterOutputs[uuid.UUID(output.ClusterID.Bytes).String()]++
		}
	}
	clusterPipelines := map[string]int{}
	enabledPipelines := 0
	for _, pipeline := range pipelines {
		if pipeline.Enabled {
			enabledPipelines++
		}
		clusterPipelines[pipeline.ClusterID.String()]++
	}
	health := "healthy"
	reasons := make([]string, 0, 2)
	if enabledPipelines > 0 && enabledOutputs == 0 {
		health = "degraded"
		reasons = append(reasons, "enabled_pipelines_without_outputs")
	}

	ops, _ := h.queries.ListLoggingOperations(ctx, sqlc.ListLoggingOperationsParams{Limit: 500, Offset: 0})
	opSummary := summarizeOperations(ctx, ops, operationStatusSummaryConfig[sqlc.LoggingOperation]{
		Status:    func(op sqlc.LoggingOperation) string { return op.Status },
		CreatedAt: func(op sqlc.LoggingOperation) time.Time { return op.CreatedAt },
		IsStaleRunning: func(op sqlc.LoggingOperation, now time.Time) bool {
			return op.StartedAt.Valid && now.Sub(op.StartedAt.Time) > time.Minute
		},
		Preview: func(_ context.Context, op sqlc.LoggingOperation) map[string]any { return loggingOperationResponse(op) },
		IsFailure: func(op sqlc.LoggingOperation) bool {
			return op.Status == OpStatusFailed
		},
		StaleThresholdSeconds: 60,
	})
	return map[string]any{
		"reconciler":    opSummary.reconcilerMap(),
		"health":        health,
		"healthReasons": reasons,
		"outputs": map[string]any{
			"total":              len(outputs),
			"enabledCount":       enabledOutputs,
			"types":              outputTypes,
			"configuredClusters": len(clusterOutputs),
		},
		"pipelines": map[string]any{
			"total":              len(pipelines),
			"enabledCount":       enabledPipelines,
			"configuredClusters": len(clusterPipelines),
		},
		"operations":         opSummary.Counts,
		"recentFailureCount": opSummary.RecentFailures,
		"recentOperations":   opSummary.Recent,
		"latestFailure":      opSummary.LatestFailure,
	}, nil
}

// CreateLoggingOutputRequest represents the request body for creating a logging output.
//
// ClusterID is accepted at the top level of the body for parity with the
// Next.js frontend, which posts to /api/v1/logging/outputs/ with the cluster
// ID in the body rather than the URL. Query (?cluster_id=) is preferred when
// present; otherwise we fall back to this body field.
//
// openapi:request LoggingOutputWriteRequest
type CreateLoggingOutputRequest struct {
	Name          string          `json:"name" validate:"required"`
	OutputType    string          `json:"output_type" validate:"required"`
	Configuration json.RawMessage `json:"configuration"`
	ClusterID     string          `json:"cluster_id"`
	Enabled       bool            `json:"enabled"`
}

// CreateLoggingPipelineRequest represents the request body for creating a logging pipeline.
//
// openapi:request LoggingPipelineWriteRequest
type CreateLoggingPipelineRequest struct {
	Name       string          `json:"name" validate:"required"`
	ClusterID  string          `json:"cluster_id"`
	Namespaces json.RawMessage `json:"namespaces"`
	Labels     json.RawMessage `json:"labels"`
	Filters    json.RawMessage `json:"filters"`
	OutputIDs  []string        `json:"output_ids"`
	Enabled    bool            `json:"enabled"`
}

const maxLoggingPipelineOutputs = 200

func (h *LoggingHandler) validatePipelineOutputs(ctx context.Context, clusterID uuid.UUID, rawIDs []string) ([]uuid.UUID, []string, error) {
	if len(rawIDs) == 0 || len(rawIDs) > maxLoggingPipelineOutputs {
		return nil, nil, errLoggingPipelineOutputsInvalid
	}
	ids := make([]uuid.UUID, 0, len(rawIDs))
	names := make([]string, 0, len(rawIDs))
	seen := make(map[uuid.UUID]struct{}, len(rawIDs))
	for _, rawID := range rawIDs {
		id, err := uuid.Parse(rawID)
		if err != nil {
			return nil, nil, errLoggingPipelineOutputsInvalid
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, nil, errLoggingPipelineOutputsInvalid
		}
		output, err := h.queries.GetLoggingOutputByID(ctx, id)
		if err != nil || !output.ClusterID.Valid || uuid.UUID(output.ClusterID.Bytes) != clusterID {
			return nil, nil, errLoggingPipelineOutputsInvalid
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
		names = append(names, output.Name)
	}
	return ids, names, nil
}

func replacePipelineOutputs(ctx context.Context, q interface {
	ReplaceLoggingPipelineOutputs(context.Context, sqlc.ReplaceLoggingPipelineOutputsParams) (int64, error)
}, pipelineID uuid.UUID, outputIDs []uuid.UUID) error {
	count, err := q.ReplaceLoggingPipelineOutputs(ctx, sqlc.ReplaceLoggingPipelineOutputsParams{
		LoggingPipelineID: pipelineID,
		OutputIds:         outputIDs,
	})
	if err != nil {
		return err
	}
	if count != int64(len(outputIDs)) {
		return fmt.Errorf("%w: associated %d of %d requested outputs", errLoggingPipelineOutputsInvalid, count, len(outputIDs))
	}
	return nil
}

// --- Operations endpoints ---

// ListOperations handles GET /api/v1/logging/operations/.
func (h *LoggingHandler) ListOperations(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 50))
	offset := int32(queryOffset(r))
	arg := sqlc.ListLoggingOperationsParams{Limit: limit, Offset: offset}
	if v := strings.TrimSpace(r.URL.Query().Get("targetType")); v != "" {
		arg.TargetType = pgtype.Text{String: v, Valid: true}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("targetKey")); v != "" {
		arg.TargetKey = pgtype.Text{String: v, Valid: true}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("status")); v != "" {
		arg.Status = pgtype.Text{String: v, Valid: true}
	}
	all, clusterIDs, _, err := h.authz.authorizedScopeIDs(r.Context(), rbac.ResourceLogging, rbac.VerbRead, rbac.NarrowedClustersWiden)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.Forbidden, "Failed to retrieve user permissions")
		return
	}
	var ops []sqlc.LoggingOperation
	var total int64
	pager, hasPager := h.queries.(loggingOperationPager)
	if all {
		ops, err = h.queries.ListLoggingOperations(r.Context(), arg)
		if err == nil && hasPager {
			total, err = pager.CountLoggingOperations(r.Context(), sqlc.CountLoggingOperationsParams{
				TargetType: arg.TargetType, TargetKey: arg.TargetKey, Status: arg.Status,
			})
		}
	} else {
		if !hasPager {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Scoped logging-operation pagination is unavailable")
			return
		}
		scoped := sqlc.ListLoggingOperationsForScopesParams{
			TargetType: arg.TargetType, TargetKey: arg.TargetKey, Status: arg.Status,
			ClusterIds: clusterIDs, QueryLimit: limit, QueryOffset: offset,
		}
		ops, err = pager.ListLoggingOperationsForScopes(r.Context(), scoped)
		if err == nil {
			total, err = pager.CountLoggingOperationsForScopes(r.Context(), sqlc.CountLoggingOperationsForScopesParams{
				TargetType: arg.TargetType, TargetKey: arg.TargetKey, Status: arg.Status, ClusterIds: clusterIDs,
			})
		}
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list logging operations")
		return
	}
	items := make([]map[string]any, 0, len(ops))
	for _, op := range ops {
		items = append(items, loggingOperationResponse(op))
	}
	if !hasPager {
		paging.Write(w, items, paging.FromPage(int(limit), int(offset), len(ops)))
		return
	}
	paging.Write(w, items, paging.Exact(total, int(limit), int(offset), len(ops)))
}

// GetOperation handles GET /api/v1/logging/operations/{id}/.
func (h *LoggingHandler) GetOperation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid operation ID")
		return
	}
	op, err := h.queries.GetLoggingOperation(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Logging operation not found")
		return
	}
	clusterID, err := h.loggingOperationClusterID(r.Context(), op)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ResolveError, "Failed to resolve logging operation target")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceLogging, rbac.VerbRead) {
		return
	}
	resp := loggingOperationResponse(op)
	if events, err := h.queries.ListLoggingOperationEvents(r.Context(), op.ID); err == nil {
		resp["events"] = loggingOperationEventsResponse(events)
	}
	RespondJSON(w, http.StatusOK, resp)
}

// RetryOperation handles POST /api/v1/logging/operations/{id}/retry/.
func (h *LoggingHandler) RetryOperation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid operation ID")
		return
	}
	op, err := h.queries.GetLoggingOperation(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Logging operation not found")
		return
	}
	if !requireRetryableOperation(w, r, op.Status) {
		return
	}
	clusterID, err := h.loggingOperationClusterID(r.Context(), op)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ResolveError, "Failed to resolve logging operation target")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceLogging, rbac.VerbUpdate) {
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	requeued, err := executeMutation(r, h.runTx,
		func(q LoggingMutationTx) (sqlc.LoggingOperation, error) {
			return q.RequeueLoggingOperation(r.Context(), id)
		},
		func(requeued sqlc.LoggingOperation) mutationAuditEvent {
			return mutationAuditEvent{action: "logging.operation.retry", resourceType: "logging_operation", resourceID: id.String(), resourceName: op.TargetKey, status: http.StatusAccepted, detail: map[string]any{
				"target_type": op.TargetType, "previous_status": op.Status,
			}}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.RetryError, "Failed to retry logging operation")
		return
	}
	h.afterLoggingOperationCommit(requeued)
	RespondAcceptedOperation(w, "/api/v1/logging/operations/"+requeued.ID.String()+"/", loggingOperationResponse(requeued))
}

// --- Response helpers ---

type loggingPipelineMutationReceipt struct {
	Pipeline  loggingPipelineResponse `json:"pipeline"`
	Operation map[string]any          `json:"operation"`
}

type loggingOutputMutationReceipt struct {
	Output    loggingOutputResponse `json:"output"`
	Operation map[string]any        `json:"operation"`
}

func loggingOperationResponse(op sqlc.LoggingOperation) map[string]any {
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

func loggingOperationEventsResponse(events []sqlc.LoggingOperationEvent) []map[string]any {
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

// loggingOperationClusterID resolves the target cluster of a logging
// operation row by decoding its payload envelope (the canonical source —
// every enqueue path sets ClusterID). For older rows that may have an
// empty ClusterID (e.g. a delete enqueued before the row carried it), we
// fall back to looking up the underlying output/pipeline by target_key.
func (h *LoggingHandler) loggingOperationClusterID(ctx context.Context, op sqlc.LoggingOperation) (uuid.UUID, error) {
	var env loggingOperationEnvelope
	if err := json.Unmarshal(op.Payload, &env); err == nil && env.ClusterID != "" {
		return uuid.Parse(env.ClusterID)
	}
	// Fallback: hydrate from the underlying row. The reconciler may have
	// persisted a payload without cluster_id on legacy operations; we keep
	// authz correct by resolving through the target table.
	targetID, parseErr := uuid.Parse(op.TargetKey)
	if parseErr != nil {
		return uuid.UUID{}, parseErr
	}
	switch op.TargetType {
	case "output":
		out, err := h.queries.GetLoggingOutputByID(ctx, targetID)
		if err != nil {
			return uuid.UUID{}, err
		}
		if !out.ClusterID.Valid {
			return uuid.UUID{}, errors.New("logging output has no cluster_id")
		}
		return uuid.UUID(out.ClusterID.Bytes), nil
	case "pipeline":
		pipe, err := h.queries.GetLoggingPipelineByID(ctx, targetID)
		if err != nil {
			return uuid.UUID{}, err
		}
		return pipe.ClusterID, nil
	default:
		return uuid.UUID{}, fmt.Errorf("unknown logging operation target type: %s", op.TargetType)
	}
}

func operationIDOrEmpty(op sqlc.LoggingOperation) string {
	if op.ID == uuid.Nil {
		return ""
	}
	return op.ID.String()
}

// listOutputsFleetWide lists every cluster's outputs, then drops the ones the
// caller can't read (per-cluster logging RBAC). Total mirrors ListOperations:
// exact count when unrestricted, page length when RBAC-filtered.
func (h *LoggingHandler) listOutputsFleetWide(w http.ResponseWriter, r *http.Request, limit, offset int32) {
	outputs, err := h.queries.ListLoggingOutputs(r.Context(), sqlc.ListLoggingOutputsParams{Limit: limit, Offset: offset})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list logging outputs")
		return
	}
	bindings, restricted, err := h.authz.bindingsForContext(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.Forbidden, "Failed to retrieve user permissions")
		return
	}
	if !restricted {
		total, err := h.queries.CountLoggingOutputs(r.Context())
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count logging outputs")
			return
		}
		paging.Write(w, loggingOutputDTOs(outputs), paging.Exact(total, int(limit), int(offset), len(outputs)))
		return
	}
	filtered := outputs[:0]
	for _, o := range outputs {
		if o.ClusterID.Valid && h.authz.allowsCluster(bindings, uuid.UUID(o.ClusterID.Bytes), rbac.ResourceLogging, rbac.VerbRead) {
			filtered = append(filtered, o)
		}
	}
	paging.Write(w, loggingOutputDTOs(filtered), paging.Exact(int64(len(filtered)), int(limit), int(offset), len(filtered)))
}

// listPipelinesFleetWide is listOutputsFleetWide for pipelines (ClusterID is
// non-null here, so no .Valid guard).
func (h *LoggingHandler) listPipelinesFleetWide(w http.ResponseWriter, r *http.Request, limit, offset int32) {
	pipelines, err := h.queries.ListLoggingPipelines(r.Context(), sqlc.ListLoggingPipelinesParams{Limit: limit, Offset: offset})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list logging pipelines")
		return
	}
	bindings, restricted, err := h.authz.bindingsForContext(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.Forbidden, "Failed to retrieve user permissions")
		return
	}
	if !restricted {
		total, err := h.queries.CountLoggingPipelines(r.Context())
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count logging pipelines")
			return
		}
		pipelineDTOs, err := h.loggingPipelineDTOs(r.Context(), pipelines)
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to load logging pipeline outputs")
			return
		}
		paging.Write(w, pipelineDTOs, paging.Exact(total, int(limit), int(offset), len(pipelineDTOs)))
		return
	}
	filtered := pipelines[:0]
	for _, p := range pipelines {
		if h.authz.allowsCluster(bindings, p.ClusterID, rbac.ResourceLogging, rbac.VerbRead) {
			filtered = append(filtered, p)
		}
	}
	pipelineDTOs, err := h.loggingPipelineDTOs(r.Context(), filtered)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to load logging pipeline outputs")
		return
	}
	paging.Write(w, pipelineDTOs, paging.Exact(int64(len(filtered)), int(limit), int(offset), len(pipelineDTOs)))
}
