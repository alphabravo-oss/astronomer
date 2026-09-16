package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// --- Pipeline endpoints ---

// ListPipelines handles GET /api/v1/logging/pipelines/ (fleet-wide) and the
// cluster-scoped ?cluster_id= form — same shape as ListOutputs above.
func (h *LoggingHandler) ListPipelines(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 20))
	offset := int32(queryOffset(r))

	clusterID, present, ok := parseOptionalClusterID(w, r)
	if !ok {
		return
	}
	if !present {
		h.listPipelinesFleetWide(w, r, limit, offset)
		return
	}

	pipelines, err := h.queries.ListPipelinesByCluster(r.Context(), sqlc.ListPipelinesByClusterParams{
		ClusterID: clusterID,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list logging pipelines")
		return
	}

	total, err := h.queries.CountPipelinesByCluster(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count logging pipelines")
		return
	}
	pipelineDTOs, err := h.loggingPipelineDTOs(r.Context(), pipelines)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to load logging pipeline outputs")
		return
	}

	paging.Write(w, pipelineDTOs, paging.Exact(total, queryLimit(r, 20), queryOffset(r), len(pipelineDTOs)))
}

// CreatePipeline handles POST /api/v1/clusters/{cluster_id}/logging/pipelines/.
func (h *LoggingHandler) CreatePipeline(w http.ResponseWriter, r *http.Request) {
	var req CreateLoggingPipelineRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	clusterID, present, ok := parseOptionalClusterID(w, r)
	if !ok {
		return
	}
	if !present {
		var err error
		clusterID, err = uuid.Parse(req.ClusterID)
		if err != nil || clusterID == uuid.Nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
			return
		}
	}
	if clusterID == uuid.Nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceLogging, rbac.VerbCreate) {
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	outputIDs, outputNames, err := h.validatePipelineOutputs(r.Context(), clusterID, req.OutputIDs)
	if err != nil {
		respondLoggingMutationError(w, r, err, http.StatusBadRequest, apierror.InvalidBody, "Invalid logging pipeline outputs")
		return
	}

	namespaces := req.Namespaces
	if namespaces == nil {
		namespaces = json.RawMessage(`[]`)
	}
	labels := req.Labels
	if labels == nil {
		labels = json.RawMessage(`{}`)
	}
	filters := req.Filters
	if filters == nil {
		filters = json.RawMessage(`{}`)
	}

	params := sqlc.CreateLoggingPipelineParams{
		Name:        req.Name,
		ClusterID:   clusterID,
		Namespaces:  namespaces,
		Labels:      labels,
		Filters:     filters,
		Enabled:     req.Enabled,
		CreatedByID: currentUserUUID(r),
	}
	mutationContext := withOperationIdempotency(r, "logging")
	result, err := executeMutation(r, h.runTx,
		func(q LoggingMutationTx) (loggingMutationResult[sqlc.LoggingPipeline], error) {
			pipeline, createErr := q.CreateLoggingPipeline(r.Context(), params)
			if createErr != nil {
				return loggingMutationResult[sqlc.LoggingPipeline]{}, createErr
			}
			if associationErr := replacePipelineOutputs(r.Context(), q, pipeline.ID, outputIDs); associationErr != nil {
				return loggingMutationResult[sqlc.LoggingPipeline]{}, associationErr
			}
			op, opErr := createLoggingPipelineApplyOperation(mutationContext, q, pipeline, currentUserUUID(r))
			return loggingMutationResult[sqlc.LoggingPipeline]{row: pipeline, op: op}, opErr
		},
		func(result loggingMutationResult[sqlc.LoggingPipeline]) mutationAuditEvent {
			return mutationAuditEvent{action: "logging.pipeline.create", resourceType: "logging_pipeline", resourceID: result.row.ID.String(), resourceName: result.row.Name, status: http.StatusAccepted, detail: map[string]any{
				"cluster_id": clusterID.String(), "enabled": result.row.Enabled, "output_count": len(outputIDs), "operation_id": operationIDOrEmpty(result.op),
			}}
		})
	if err != nil {
		respondLoggingMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create logging pipeline")
		return
	}
	h.afterLoggingOperationCommit(result.op)
	RespondAcceptedOperation(w, "/api/v1/logging/operations/"+result.op.ID.String()+"/", loggingPipelineMutationReceipt{
		Pipeline:  loggingPipelineDTO(result.row, outputIDs, outputNames),
		Operation: loggingOperationResponse(result.op),
	})
}

// UpdatePipeline handles PUT /api/v1/logging/pipelines/{id}/.
func (h *LoggingHandler) UpdatePipeline(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid pipeline ID")
		return
	}
	var req CreateLoggingPipelineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	// Authorize against the pipeline's owning cluster before mutating it.
	existing, err := h.queries.GetLoggingPipelineByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Logging pipeline not found")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, existing.ClusterID, rbac.ResourceLogging, rbac.VerbUpdate) {
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	outputIDs, outputNames, err := h.validatePipelineOutputs(r.Context(), existing.ClusterID, req.OutputIDs)
	if err != nil {
		respondLoggingMutationError(w, r, err, http.StatusBadRequest, apierror.InvalidBody, "Invalid logging pipeline outputs")
		return
	}
	params := sqlc.UpdateLoggingPipelineParams{
		ID:         id,
		Name:       req.Name,
		Namespaces: req.Namespaces,
		Labels:     req.Labels,
		Filters:    req.Filters,
		Enabled:    req.Enabled,
	}
	mutationContext := withOperationIdempotency(r, "logging")
	result, err := executeMutation(r, h.runTx,
		func(q LoggingMutationTx) (loggingMutationResult[sqlc.LoggingPipeline], error) {
			pipeline, updateErr := q.UpdateLoggingPipeline(r.Context(), params)
			if updateErr != nil {
				return loggingMutationResult[sqlc.LoggingPipeline]{}, updateErr
			}
			if associationErr := replacePipelineOutputs(r.Context(), q, pipeline.ID, outputIDs); associationErr != nil {
				return loggingMutationResult[sqlc.LoggingPipeline]{}, associationErr
			}
			op, opErr := createLoggingPipelineApplyOperation(mutationContext, q, pipeline, currentUserUUID(r))
			return loggingMutationResult[sqlc.LoggingPipeline]{row: pipeline, op: op}, opErr
		},
		func(result loggingMutationResult[sqlc.LoggingPipeline]) mutationAuditEvent {
			return mutationAuditEvent{action: "logging.pipeline.update", resourceType: "logging_pipeline", resourceID: result.row.ID.String(), resourceName: result.row.Name, status: http.StatusAccepted, detail: map[string]any{
				"enabled": result.row.Enabled, "output_count": len(outputIDs), "operation_id": operationIDOrEmpty(result.op),
			}}
		})
	if err != nil {
		respondLoggingMutationError(w, r, err, http.StatusInternalServerError, apierror.UpdateError, "Failed to update logging pipeline")
		return
	}
	h.afterLoggingOperationCommit(result.op)
	RespondAcceptedOperation(w, "/api/v1/logging/operations/"+result.op.ID.String()+"/", loggingPipelineMutationReceipt{
		Pipeline:  loggingPipelineDTO(result.row, outputIDs, outputNames),
		Operation: loggingOperationResponse(result.op),
	})
}

// DeletePipeline handles DELETE /api/v1/clusters/{cluster_id}/logging/pipelines/{id}/.
func (h *LoggingHandler) DeletePipeline(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid pipeline ID")
		return
	}

	existing, lookupErr := h.queries.GetLoggingPipelineByID(r.Context(), id)
	pipelineName := ""
	var pipelineClusterID uuid.UUID
	if lookupErr == nil {
		pipelineName = existing.Name
		pipelineClusterID = existing.ClusterID
	}
	if !h.authz.authorizeClusterAction(w, r, pipelineClusterID, rbac.ResourceLogging, rbac.VerbDelete) {
		return
	}
	if lookupErr != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Logging pipeline not found")
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	existingDTOs, err := h.loggingPipelineDTOs(r.Context(), []sqlc.LoggingPipeline{existing})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to load logging pipeline outputs")
		return
	}
	mutationContext := withOperationIdempotency(r, "logging")
	result, err := executeMutation(r, h.runTx,
		func(q LoggingMutationTx) (loggingMutationResult[sqlc.LoggingPipeline], error) {
			op, opErr := createLoggingPipelineDeleteOperation(mutationContext, q, existing, currentUserUUID(r))
			if opErr != nil {
				return loggingMutationResult[sqlc.LoggingPipeline]{}, opErr
			}
			if deleteErr := q.DeleteLoggingPipeline(r.Context(), id); deleteErr != nil {
				return loggingMutationResult[sqlc.LoggingPipeline]{}, deleteErr
			}
			return loggingMutationResult[sqlc.LoggingPipeline]{row: existing, op: op}, nil
		},
		func(result loggingMutationResult[sqlc.LoggingPipeline]) mutationAuditEvent {
			return mutationAuditEvent{action: "logging.pipeline.delete", resourceType: "logging_pipeline", resourceID: id.String(), resourceName: pipelineName, status: http.StatusAccepted, detail: map[string]any{
				"operation_id": operationIDOrEmpty(result.op),
			}}
		})
	if err != nil {
		respondLoggingMutationError(w, r, err, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete logging pipeline")
		return
	}
	h.afterLoggingOperationCommit(result.op)
	RespondAcceptedOperation(w, "/api/v1/logging/operations/"+result.op.ID.String()+"/", loggingPipelineMutationReceipt{
		Pipeline:  existingDTOs[0],
		Operation: loggingOperationResponse(result.op),
	})
}

// EnableOutput handles POST /api/v1/logging/outputs/{id}/enable/.
func (h *LoggingHandler) EnableOutput(w http.ResponseWriter, r *http.Request) {
	h.setOutputEnabled(w, r, true)
}

// DisableOutput handles POST /api/v1/logging/outputs/{id}/disable/.
func (h *LoggingHandler) DisableOutput(w http.ResponseWriter, r *http.Request) {
	h.setOutputEnabled(w, r, false)
}

func (h *LoggingHandler) setOutputEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid output ID")
		return
	}
	current, err := h.queries.GetLoggingOutputByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Logging output not found")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, uuid.UUID(current.ClusterID.Bytes), rbac.ResourceLogging, rbac.VerbUpdate) {
		return
	}
	denyAction := "enabled"
	if !enabled {
		denyAction = "disabled"
	}
	if rejectSystemOutputMutation(w, r, current, denyAction) {
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	params := sqlc.UpdateLoggingOutputParams{
		ID:            id,
		Name:          current.Name,
		OutputType:    current.OutputType,
		Configuration: current.Configuration,
		Enabled:       enabled,
	}
	action := "logging.output.enable"
	if !enabled {
		action = "logging.output.disable"
	}
	mutationContext := withOperationIdempotency(r, "logging")
	result, err := executeMutation(r, h.runTx,
		func(q LoggingMutationTx) (loggingMutationResult[sqlc.LoggingOutput], error) {
			output, updateErr := q.UpdateLoggingOutput(r.Context(), params)
			if updateErr != nil {
				return loggingMutationResult[sqlc.LoggingOutput]{}, updateErr
			}
			op, opErr := createLoggingOutputApplyOperation(mutationContext, q, output, currentUserUUID(r))
			return loggingMutationResult[sqlc.LoggingOutput]{row: output, op: op}, opErr
		},
		func(result loggingMutationResult[sqlc.LoggingOutput]) mutationAuditEvent {
			return mutationAuditEvent{action: action, resourceType: "logging_output", resourceID: result.row.ID.String(), resourceName: result.row.Name, status: http.StatusAccepted, detail: map[string]any{
				"enabled": enabled, "operation_id": operationIDOrEmpty(result.op),
			}}
		})
	if err != nil {
		respondLoggingMutationError(w, r, err, http.StatusInternalServerError, apierror.UpdateError, "Failed to update logging output")
		return
	}
	// Re-render the ConfigMap on enable/disable so cluster state tracks
	// intent. When disabled we still apply — the rendered config will
	// reflect enabled=false so Fluent Bit can skip it.
	h.afterLoggingOperationCommit(result.op)
	RespondAcceptedOperation(w, "/api/v1/logging/operations/"+result.op.ID.String()+"/", loggingOutputMutationReceipt{
		Output:    loggingOutputDTO(result.row),
		Operation: loggingOperationResponse(result.op),
	})
}

// loggingQueryRequest is the body for POST .../logging/outputs/{id}/query/.
//
// openapi:request LoggingQueryRequest
type loggingQueryRequest struct {
	Query      string   `json:"query"`
	Limit      int      `json:"limit,omitempty"`
	Start      string   `json:"start,omitempty"` // RFC3339 or Loki ns epoch
	End        string   `json:"end,omitempty"`
	Direction  string   `json:"direction,omitempty"` // forward | backward
	Namespaces []string `json:"namespaces,omitempty"`
}

// QueryOutput handles POST /api/v1/logging/outputs/{id}/query/.
// Every provider advertises query support in the output DTO. This endpoint
// therefore rejects shipping-only providers before making an outbound call,
// rather than surprising the UI with a generic provider-specific 501.
func (h *LoggingHandler) QueryOutput(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid output ID")
		return
	}
	output, err := h.queries.GetLoggingOutputByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Logging output not found")
		return
	}
	if output.ClusterID.Valid {
		if !h.authz.authorizeClusterAction(w, r, output.ClusterID.Bytes, rbac.ResourceLogging, rbac.VerbRead) {
			return
		}
	}
	var req loggingQueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	capabilities := loggingCapabilitiesFor(output)
	if !capabilities.Query {
		RespondRequestError(w, r, http.StatusUnprocessableEntity, apierror.NotImplemented,
			fmt.Sprintf("Logging output type %q is shipping-only; configure a queryable store or use its secure link-out", output.OutputType))
		return
	}
	if h.querySlots == nil {
		h.querySlots = make(chan struct{}, 8)
	}
	select {
	case h.querySlots <- struct{}{}:
		defer func() { <-h.querySlots }()
	case <-r.Context().Done():
		RespondRequestError(w, r, http.StatusRequestTimeout, apierror.ProxyError, "Logging query canceled while waiting for capacity")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	result, qerr := h.queryLoggingOutput(ctx, output, req)
	if qerr != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.ProxyError, qerr.Error())
		return
	}
	recordLoggingQueryAudit(r, h.queries, output, req)
	RespondJSON(w, http.StatusOK, result)
}

// EnablePipeline handles POST /api/v1/logging/pipelines/{id}/enable/.
func (h *LoggingHandler) EnablePipeline(w http.ResponseWriter, r *http.Request) {
	h.setPipelineEnabled(w, r, true)
}

// DisablePipeline handles POST /api/v1/logging/pipelines/{id}/disable/.
func (h *LoggingHandler) DisablePipeline(w http.ResponseWriter, r *http.Request) {
	h.setPipelineEnabled(w, r, false)
}

func (h *LoggingHandler) setPipelineEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid pipeline ID")
		return
	}
	current, err := h.queries.GetLoggingPipelineByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Logging pipeline not found")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, current.ClusterID, rbac.ResourceLogging, rbac.VerbUpdate) {
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	currentDTOs, err := h.loggingPipelineDTOs(r.Context(), []sqlc.LoggingPipeline{current})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to load logging pipeline outputs")
		return
	}
	params := sqlc.UpdateLoggingPipelineParams{
		ID:         id,
		Name:       current.Name,
		Namespaces: current.Namespaces,
		Labels:     current.Labels,
		Filters:    current.Filters,
		Enabled:    enabled,
	}
	action := "logging.pipeline.enable"
	if !enabled {
		action = "logging.pipeline.disable"
	}
	mutationContext := withOperationIdempotency(r, "logging")
	result, err := executeMutation(r, h.runTx,
		func(q LoggingMutationTx) (loggingMutationResult[sqlc.LoggingPipeline], error) {
			pipeline, updateErr := q.UpdateLoggingPipeline(r.Context(), params)
			if updateErr != nil {
				return loggingMutationResult[sqlc.LoggingPipeline]{}, updateErr
			}
			op, opErr := createLoggingPipelineApplyOperation(mutationContext, q, pipeline, currentUserUUID(r))
			return loggingMutationResult[sqlc.LoggingPipeline]{row: pipeline, op: op}, opErr
		},
		func(result loggingMutationResult[sqlc.LoggingPipeline]) mutationAuditEvent {
			return mutationAuditEvent{action: action, resourceType: "logging_pipeline", resourceID: result.row.ID.String(), resourceName: result.row.Name, status: http.StatusAccepted, detail: map[string]any{
				"enabled": enabled, "operation_id": operationIDOrEmpty(result.op),
			}}
		})
	if err != nil {
		respondLoggingMutationError(w, r, err, http.StatusInternalServerError, apierror.UpdateError, "Failed to update logging pipeline")
		return
	}
	h.afterLoggingOperationCommit(result.op)
	RespondAcceptedOperation(w, "/api/v1/logging/operations/"+result.op.ID.String()+"/", loggingPipelineMutationReceipt{
		Pipeline:  loggingPipelineDTO(result.row, currentDTOs[0].OutputIDs, currentDTOs[0].OutputNames),
		Operation: loggingOperationResponse(result.op),
	})
}

// FluentbitConfig handles GET /api/v1/logging/pipelines/{id}/fluentbit-config/.
// Returns a minimal Fluent Bit configuration stub for the pipeline's cluster.
func (h *LoggingHandler) FluentbitConfig(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid pipeline ID")
		return
	}
	pipeline, err := h.queries.GetLoggingPipelineByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Logging pipeline not found")
		return
	}
	config, err := h.renderFullFluentbitConfig(r.Context(), pipeline.ClusterID, false)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to render logging configuration")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"cluster_id": pipeline.ClusterID.String(),
		"config":     config,
	})
}

// FluentBitConfigMapName is the single aggregate config ConfigMap the controller
// writes and Fluent Bit consumes (mounted via the chart's existingConfigMap).
const FluentBitConfigMapName = "astronomer-fluent-bit-config"

// refreshAggregateFluentBitConfig re-renders the cluster's full Fluent Bit
// config from all enabled outputs/pipelines and writes it to the single
// aggregate ConfigMap that the Fluent Bit DaemonSet mounts. This is the link
// that makes configured outputs actually take effect: the installed Fluent Bit
// reads this ConfigMap (existingConfigMap) and hot-reloads on change.
func (h *LoggingHandler) refreshAggregateFluentBitConfig(ctx context.Context, clusterID string) error {
	cu, err := uuid.Parse(clusterID)
	if err != nil {
		return fmt.Errorf("parse cluster id: %w", err)
	}
	config, err := h.renderFullFluentbitConfig(ctx, cu, true)
	if err != nil {
		return fmt.Errorf("render aggregate config: %w", err)
	}
	if err := ensureNamespace(ctx, h.requester, clusterID, LoggingNamespace); err != nil {
		return fmt.Errorf("ensure namespace: %w", err)
	}
	return applyConfigMap(ctx, h.requester, clusterID, LoggingNamespace, FluentBitConfigMapName, map[string]string{
		"fluent-bit.conf": config,
		// Standard parsers referenced by [SERVICE] Parsers_File; existingConfigMap
		// mounts only the keys in this ConfigMap, so we ship parsers here too.
		"parsers.conf": fluentBitDefaultParsers,
	})
}

// fluentBitDefaultParsers is a minimal parsers.conf covering CRI/Docker
// container-runtime log formats so the tail input can decode k8s logs.
const fluentBitDefaultParsers = `[PARSER]
    Name cri
    Format regex
    Regex ^(?<time>[^ ]+) (?<stream>stdout|stderr) (?<logtag>[^ ]*) (?<message>.*)$
    Time_Key time
    Time_Format %Y-%m-%dT%H:%M:%S.%L%z

[PARSER]
    Name docker
    Format json
    Time_Key time
    Time_Format %Y-%m-%dT%H:%M:%S.%L
    Time_Keep On
`

// renderFullFluentbitConfig assembles the complete Fluent Bit configuration for
// a cluster from its enabled pipelines (filters) and outputs, reusing the same
// block renderers the controller applies to the cluster. Previously this view
// returned a hardcoded stub that ignored the actual pipelines/outputs.
func (h *LoggingHandler) renderFullFluentbitConfig(ctx context.Context, clusterID uuid.UUID, includeSecrets bool) (string, error) {
	// includeSecrets is retained for callers; system ingest tokens are never
	// rendered into this ConfigMap (bearer_token_file + member Secret).
	_ = includeSecrets
	var b strings.Builder
	b.WriteString("# rendered by astronomer-go logging controller\n")
	b.WriteString("[SERVICE]\n")
	writeKV(&b, "Daemon", "Off")
	writeKV(&b, "Log_Level", "info")
	writeKV(&b, "Parsers_File", "parsers.conf")
	// HTTP server + hot reload let the configmap-reload sidecar push new
	// outputs/pipelines without a pod restart when the controller rewrites
	// the aggregate config ConfigMap.
	writeKV(&b, "HTTP_Server", "On")
	writeKV(&b, "HTTP_Listen", "0.0.0.0")
	writeKV(&b, "HTTP_Port", "2020")
	writeKV(&b, "Hot_Reload", "On")
	b.WriteString("\n[INPUT]\n")
	writeKV(&b, "Name", "tail")
	writeKV(&b, "Path", "/var/log/containers/*.log")
	writeKV(&b, "Tag", "kube.*")
	writeKV(&b, "Mem_Buf_Limit", "5MB")
	writeKV(&b, "Skip_Long_Lines", "On")
	b.WriteString("\n[FILTER]\n")
	writeKV(&b, "Name", "kubernetes")
	writeKV(&b, "Match", "kube.*")
	writeKV(&b, "Merge_Log", "On")

	pipelines, err := h.queries.ListPipelinesByCluster(ctx, sqlc.ListPipelinesByClusterParams{ClusterID: clusterID, Limit: 500, Offset: 0})
	if err != nil {
		return "", fmt.Errorf("list pipelines: %w", err)
	}
	pipelineByID := make(map[uuid.UUID]sqlc.LoggingPipeline, len(pipelines))
	pipelineIDs := make([]uuid.UUID, 0, len(pipelines))
	for _, p := range pipelines {
		pipelineByID[p.ID] = p
		pipelineIDs = append(pipelineIDs, p.ID)
		if !p.Enabled {
			continue
		}
		b.WriteString("\n")
		b.WriteString(renderPipelineBlock(loggingOperationEnvelope{
			ClusterID: clusterID.String(), TargetID: p.ID.String(), TargetType: "pipeline",
			Name: p.Name, Enabled: p.Enabled, Namespaces: p.Namespaces, Labels: p.Labels, Filters: p.Filters,
		}))
	}
	details, err := h.queries.ListLoggingPipelineOutputDetails(ctx, pipelineIDs)
	if err != nil {
		return "", fmt.Errorf("list pipeline outputs: %w", err)
	}
	pipelinesByOutput := make(map[uuid.UUID][]sqlc.LoggingPipeline)
	for _, detail := range details {
		pipeline, ok := pipelineByID[detail.LoggingPipelineID]
		if ok {
			pipelinesByOutput[detail.LoggingOutputID] = append(pipelinesByOutput[detail.LoggingOutputID], pipeline)
		}
	}

	outputs, err := h.queries.ListOutputsByCluster(ctx, sqlc.ListOutputsByClusterParams{ClusterID: pgtype.UUID{Bytes: clusterID, Valid: true}, Limit: 500, Offset: 0})
	if err != nil {
		return "", fmt.Errorf("list outputs: %w", err)
	}
	enabledOutputs := 0
	for _, o := range outputs {
		if !o.Enabled {
			continue
		}
		linkedPipelines := pipelinesByOutput[o.ID]
		// A cluster with no pipelines keeps the historical direct-output mode.
		// Once any pipeline exists, only explicit links receive records. This
		// fails safe for pre-association rows and for damaged relationships:
		// missing metadata cannot silently copy every cluster log externally.
		if len(pipelines) == 0 {
			enabledOutputs++
			b.WriteString("\n")
			b.WriteString(renderOutputBlock(loggingOperationEnvelope{
				ClusterID: clusterID.String(), TargetID: o.ID.String(), TargetType: "output",
				Name: o.Name, OutputType: o.OutputType, Enabled: o.Enabled, Configuration: o.Configuration,
				IsSystem: o.IsSystem,
			}))
			continue
		}
		for _, pipeline := range linkedPipelines {
			if !pipeline.Enabled {
				continue
			}
			enabledOutputs++
			b.WriteString("\n")
			b.WriteString(renderOutputBlock(loggingOperationEnvelope{
				ClusterID: clusterID.String(), TargetID: o.ID.String(), TargetType: "output",
				Name: o.Name + " via " + pipeline.Name, OutputType: o.OutputType, Enabled: o.Enabled,
				Configuration: withLoggingOutputMatch(o.Configuration, pipelineRouteTag(pipeline.ID)),
				IsSystem:      o.IsSystem,
			}))
		}
	}
	if enabledOutputs == 0 {
		// No output configured yet — stdout so logs are at least visible in the
		// Fluent Bit pod and the config is valid.
		b.WriteString("\n[OUTPUT]\n")
		writeKV(&b, "Name", "stdout")
		writeKV(&b, "Match", "*")
	}
	return b.String(), nil
}
