package handler

import (
	"encoding/json"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ListOutputs handles GET /api/v1/logging/outputs/ (fleet-wide, the dashboard
// Logging page) and the cluster-scoped ?cluster_id= form. No cluster_id means
// fleet-wide: list every cluster's outputs, then filter per-cluster by the
// caller's RBAC (mirrors ListOperations). A malformed cluster_id is still 400.
// --- Output endpoints ---

// ListOutputs handles GET /api/v1/logging/outputs/ (fleet-wide, the dashboard
// Logging page) and the cluster-scoped ?cluster_id= form. No cluster_id means
// fleet-wide: list every cluster's outputs, then filter per-cluster by the
// caller's RBAC (mirrors ListOperations). A malformed cluster_id is still 400.
func (h *LoggingHandler) ListOutputs(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 20))
	offset := int32(queryOffset(r))

	clusterID, present, ok := parseOptionalClusterID(w, r)
	if !ok {
		return
	}
	if !present {
		h.listOutputsFleetWide(w, r, limit, offset)
		return
	}

	outputs, err := h.queries.ListOutputsByCluster(r.Context(), sqlc.ListOutputsByClusterParams{
		ClusterID: pgtype.UUID{Bytes: clusterID, Valid: true},
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list logging outputs")
		return
	}

	total, err := h.queries.CountOutputsByCluster(r.Context(), pgtype.UUID{Bytes: clusterID, Valid: true})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count logging outputs")
		return
	}

	paging.Write(w, loggingOutputDTOs(outputs), paging.Exact(total, queryLimit(r, 20), queryOffset(r), len(outputs)))
}

// CreateOutput handles POST /api/v1/clusters/{cluster_id}/logging/outputs/.
func (h *LoggingHandler) CreateOutput(w http.ResponseWriter, r *http.Request) {
	var req CreateLoggingOutputRequest
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

	configuration := stripBearerFromLoggingConfiguration(req.Configuration)

	params := sqlc.CreateLoggingOutputParams{
		Name:          req.Name,
		OutputType:    req.OutputType,
		Configuration: configuration,
		ClusterID:     pgtype.UUID{Bytes: clusterID, Valid: true},
		Enabled:       req.Enabled,
		CreatedByID:   currentUserUUID(r),
		IsSystem:      false,
	}
	mutationContext := withOperationIdempotency(r, "logging")
	result, err := executeMutation(r, h.runTx,
		func(q LoggingMutationTx) (loggingMutationResult[sqlc.LoggingOutput], error) {
			output, createErr := q.CreateLoggingOutput(r.Context(), params)
			if createErr != nil {
				return loggingMutationResult[sqlc.LoggingOutput]{}, createErr
			}
			op, opErr := createLoggingOutputApplyOperation(mutationContext, q, output, currentUserUUID(r))
			return loggingMutationResult[sqlc.LoggingOutput]{row: output, op: op}, opErr
		},
		func(result loggingMutationResult[sqlc.LoggingOutput]) mutationAuditEvent {
			return mutationAuditEvent{action: "logging.output.create", resourceType: "logging_output", resourceID: result.row.ID.String(), resourceName: result.row.Name, status: http.StatusAccepted, detail: map[string]any{
				"cluster_id": clusterID.String(), "output_type": result.row.OutputType, "enabled": result.row.Enabled, "operation_id": operationIDOrEmpty(result.op),
			}}
		})
	if err != nil {
		respondLoggingMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create logging output")
		return
	}
	h.afterLoggingOperationCommit(result.op)
	RespondAcceptedOperation(w, "/api/v1/logging/operations/"+result.op.ID.String()+"/", loggingOutputMutationReceipt{
		Output:    loggingOutputDTO(result.row),
		Operation: loggingOperationResponse(result.op),
	})
}

// UpdateOutput handles PUT /api/v1/logging/outputs/{id}/.
func (h *LoggingHandler) UpdateOutput(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid output ID")
		return
	}
	var req CreateLoggingOutputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	// Authorize against the output's owning cluster before mutating it.
	existing, err := h.queries.GetLoggingOutputByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Logging output not found")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, uuid.UUID(existing.ClusterID.Bytes), rbac.ResourceLogging, rbac.VerbUpdate) {
		return
	}
	if rejectSystemOutputMutation(w, r, existing, "edited") {
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	params := sqlc.UpdateLoggingOutputParams{
		ID:            id,
		Name:          req.Name,
		OutputType:    req.OutputType,
		Configuration: stripBearerFromLoggingConfiguration(req.Configuration),
		Enabled:       req.Enabled,
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
			return mutationAuditEvent{action: "logging.output.update", resourceType: "logging_output", resourceID: result.row.ID.String(), resourceName: result.row.Name, status: http.StatusAccepted, detail: map[string]any{
				"output_type": result.row.OutputType, "enabled": result.row.Enabled, "operation_id": operationIDOrEmpty(result.op),
			}}
		})
	if err != nil {
		respondLoggingMutationError(w, r, err, http.StatusInternalServerError, apierror.UpdateError, "Failed to update logging output")
		return
	}
	h.afterLoggingOperationCommit(result.op)
	RespondAcceptedOperation(w, "/api/v1/logging/operations/"+result.op.ID.String()+"/", loggingOutputMutationReceipt{
		Output:    loggingOutputDTO(result.row),
		Operation: loggingOperationResponse(result.op),
	})
}

// TestOutput handles POST /api/v1/logging/outputs/{id}/test/.
//
// Previously returned 501. Now triggers an apply operation, which proxies
// the configuration through the agent — operators get a real round-trip
// signal instead of "not implemented".
func (h *LoggingHandler) TestOutput(w http.ResponseWriter, r *http.Request) {
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
	// A "test" pushes the output's config to the managed cluster through the
	// agent, so it is a cluster-affecting mutation and gates like its siblings.
	if !h.authz.authorizeClusterAction(w, r, uuid.UUID(output.ClusterID.Bytes), rbac.ResourceLogging, rbac.VerbUpdate) {
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	mutationContext := withOperationIdempotency(r, "logging")
	result, err := executeMutation(r, h.runTx,
		func(q LoggingMutationTx) (loggingMutationResult[sqlc.LoggingOutput], error) {
			op, opErr := createLoggingOutputApplyOperation(mutationContext, q, output, currentUserUUID(r))
			return loggingMutationResult[sqlc.LoggingOutput]{row: output, op: op}, opErr
		},
		func(result loggingMutationResult[sqlc.LoggingOutput]) mutationAuditEvent {
			return mutationAuditEvent{action: "logging.output.test", resourceType: "logging_output", resourceID: output.ID.String(), resourceName: output.Name, status: http.StatusAccepted, detail: map[string]any{
				"cluster_id": uuid.UUID(output.ClusterID.Bytes).String(), "operation_id": operationIDOrEmpty(result.op),
			}}
		})
	if err != nil {
		respondLoggingMutationError(w, r, err, http.StatusInternalServerError, apierror.EnqueueError, "Failed to enqueue apply test")
		return
	}
	h.afterLoggingOperationCommit(result.op)
	RespondAcceptedOperation(w, "/api/v1/logging/operations/"+result.op.ID.String()+"/", map[string]any{
		"success":   true,
		"message":   "Logging output apply enqueued",
		"operation": loggingOperationResponse(result.op),
	})
}

// DeleteOutput handles DELETE /api/v1/clusters/{cluster_id}/logging/outputs/{id}/.
func (h *LoggingHandler) DeleteOutput(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid output ID")
		return
	}

	existing, lookupErr := h.queries.GetLoggingOutputByID(r.Context(), id)
	outputName := ""
	var outputClusterID uuid.UUID
	if lookupErr == nil {
		outputName = existing.Name
		outputClusterID = uuid.UUID(existing.ClusterID.Bytes)
	}
	if !h.authz.authorizeClusterAction(w, r, outputClusterID, rbac.ResourceLogging, rbac.VerbDelete) {
		return
	}
	if lookupErr == nil && rejectSystemOutputMutation(w, r, existing, "deleted") {
		return
	}
	if lookupErr != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Logging output not found")
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	mutationContext := withOperationIdempotency(r, "logging")
	result, err := executeMutation(r, h.runTx,
		func(q LoggingMutationTx) (loggingMutationResult[sqlc.LoggingOutput], error) {
			op, opErr := createLoggingOutputDeleteOperation(mutationContext, q, existing, currentUserUUID(r))
			if opErr != nil {
				return loggingMutationResult[sqlc.LoggingOutput]{}, opErr
			}
			if deleteErr := q.DeleteLoggingOutput(r.Context(), id); deleteErr != nil {
				return loggingMutationResult[sqlc.LoggingOutput]{}, deleteErr
			}
			return loggingMutationResult[sqlc.LoggingOutput]{row: existing, op: op}, nil
		},
		func(result loggingMutationResult[sqlc.LoggingOutput]) mutationAuditEvent {
			return mutationAuditEvent{action: "logging.output.delete", resourceType: "logging_output", resourceID: id.String(), resourceName: outputName, status: http.StatusAccepted, detail: map[string]any{
				"operation_id": operationIDOrEmpty(result.op),
			}}
		})
	if err != nil {
		if isFKRestrictViolation(err) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Logging output is still selected by a pipeline; edit or delete the pipeline first")
			return
		}
		respondLoggingMutationError(w, r, err, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete logging output")
		return
	}
	h.afterLoggingOperationCommit(result.op)
	RespondAcceptedOperation(w, "/api/v1/logging/operations/"+result.op.ID.String()+"/", loggingOutputMutationReceipt{
		Output:    loggingOutputDTO(existing),
		Operation: loggingOperationResponse(result.op),
	})
}
