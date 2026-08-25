package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/lokiauth"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

type loggingAttachWriter interface {
	systemLoggingOutputWriter
	loggingOperationCreator
	UpsertLokiIngestToken(context.Context, sqlc.UpsertLokiIngestTokenParams) (sqlc.LokiIngestToken, error)
}

func persistLoggingAttach(ctx context.Context, q loggingAttachWriter, token *sqlc.UpsertLokiIngestTokenParams, spec systemLoggingOutputSpec, userID pgtype.UUID) (loggingMutationResult[sqlc.LoggingOutput], error) {
	if token != nil {
		if _, err := q.UpsertLokiIngestToken(ctx, *token); err != nil {
			return loggingMutationResult[sqlc.LoggingOutput]{}, err
		}
	}
	output, err := upsertSystemLoggingOutputWith(ctx, q, spec)
	if err != nil {
		return loggingMutationResult[sqlc.LoggingOutput]{}, err
	}
	op, err := createLoggingOutputApplyOperation(ctx, q, output, userID)
	return loggingMutationResult[sqlc.LoggingOutput]{row: output, op: op}, err
}

// GetAstronomerAttachStatus handles GET /api/v1/clusters/{id}/logging/outputs/attach-astronomer/.
// logging:read. Used by the cluster logging CTA; does not run the sizer.
func (h *LoggingHandler) GetAstronomerAttachStatus(w http.ResponseWriter, r *http.Request) {
	clusterID, err := clusterIDFromClusterRoute(r)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceLogging, rbac.VerbRead) {
		return
	}
	state := lokiAttachState{Port: "443"}
	if h.lokiAttach != nil {
		state = h.lokiAttach.LokiAttachState(r.Context())
	}
	if strings.TrimSpace(state.Port) == "" {
		state.Port = "443"
	}
	attached := false
	if out, lookupErr := h.queries.GetSystemLoggingOutputByCluster(r.Context(), pgtype.UUID{Bytes: clusterID, Valid: true}); lookupErr == nil {
		_, hasToken := h.lokiTokenForCluster(r.Context(), clusterID)
		attached = out.Enabled && hasToken
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"clusterId":    clusterID.String(),
		"ingestPublic": state.IngestPublic,
		"status":       state.Status,
		"host":         state.Host,
		"attached":     attached,
	})
}

// AttachAstronomerLogs handles POST /api/v1/clusters/{id}/logging/outputs/attach-astronomer/.
// logging:create. Idempotent when already attached unless ?rotate=true.
func (h *LoggingHandler) AttachAstronomerLogs(w http.ResponseWriter, r *http.Request) {
	clusterID, err := clusterIDFromClusterRoute(r)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceLogging, rbac.VerbCreate) {
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}

	rotate := queryBool(r, "rotate")
	existing, existingErr := h.queries.GetSystemLoggingOutputByCluster(r.Context(), pgtype.UUID{Bytes: clusterID, Valid: true})
	_, hasToken := h.lokiTokenForCluster(r.Context(), clusterID)
	alreadyAttached := existingErr == nil && existing.Enabled && hasToken

	if alreadyAttached && !rotate {
		RespondJSON(w, http.StatusOK, attachAstronomerResponse(existing, ""))
		return
	}

	state := lokiAttachState{}
	if h.lokiAttach != nil {
		state = h.lokiAttach.LokiAttachState(r.Context())
	}
	if strings.EqualFold(state.Status, "degraded_capacity") {
		RespondRequestError(w, r, http.StatusConflict, apierror.DegradedCapacity,
			"Astronomer Loki is over capacity; new attaches are frozen. Existing pipelines keep their current cap.")
		return
	}
	if !strings.EqualFold(state.Status, "healthy") || !state.IngestPublic || strings.TrimSpace(state.Host) == "" {
		RespondRequestError(w, r, http.StatusConflict, apierror.LokiNotReady,
			"Astronomer Loki is not healthy with a public ingest hostname")
		return
	}
	if h.lokiAttach != nil {
		if code, msg, ok := h.lokiAttach.CheckLokiAttachCapacity(r.Context(), clusterID); !ok {
			if code == "" {
				code = apierror.IngestCapExceeded
			}
			if msg == "" {
				msg = "Attaching this cluster would exceed hosted Loki ingest capacity"
			}
			RespondRequestError(w, r, http.StatusConflict, code, msg)
			return
		}
	}
	if h.encryptor == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Encryption is not configured")
		return
	}

	plaintext := ""
	var tokenParams *sqlc.UpsertLokiIngestTokenParams
	if !hasToken || rotate {
		minted, mintErr := mintLokiIngestToken()
		if mintErr != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to mint ingest token")
			return
		}
		sealed, encErr := h.encryptor.Encrypt(minted)
		if encErr != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to encrypt ingest token")
			return
		}
		params := sqlc.UpsertLokiIngestTokenParams{
			ClusterID:      clusterID,
			TokenHash:      lokiauth.HashBearer(minted),
			TokenEncrypted: sealed,
			CreatedByID:    currentUserUUID(r),
		}
		tokenParams = &params
		plaintext = minted
	}

	port := state.Port
	if strings.TrimSpace(port) == "" {
		port = "443"
	}
	spec := systemLoggingOutputSpec{
		ClusterID: clusterID,
		Host:      state.Host,
		Port:      port,
		Enabled:   true,
		CreatedBy: currentUserUUID(r),
	}
	auditAction := "logging.output.attach_astronomer"
	if rotate {
		auditAction = "logging.loki_token.rotate"
	}
	mutationContext := withOperationIdempotency(r, "logging")
	result, err := executeLoggingMutation(r, h,
		func(q LoggingMutationTx) (loggingMutationResult[sqlc.LoggingOutput], error) {
			return persistLoggingAttach(mutationContext, q, tokenParams, spec, currentUserUUID(r))
		},
		func() (loggingMutationResult[sqlc.LoggingOutput], error) {
			return persistLoggingAttach(mutationContext, h.queries, tokenParams, spec, currentUserUUID(r))
		},
		func(result loggingMutationResult[sqlc.LoggingOutput]) clusterAuditEvent {
			return clusterAuditEvent{action: auditAction, resourceType: "logging_output", resourceID: result.row.ID.String(), resourceName: result.row.Name, status: http.StatusAccepted, detail: map[string]any{
				"cluster_id": clusterID.String(), "rotated": rotate, "minted": plaintext != "", "operation_id": operationIDOrEmpty(result.op),
			}}
		})
	if err != nil {
		respondLoggingMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to attach Astronomer logs")
		return
	}
	h.afterLoggingOperationCommit(result.op)
	if h.lokiIngest != nil {
		if recErr := h.lokiIngest.ReconcileLokiIngest(r.Context()); recErr != nil && h.log != nil {
			h.log.Warn("loki ingest reconcile after attach failed", "error", recErr, "cluster_id", clusterID.String())
		}
	}
	RespondAcceptedOperation(w, "/api/v1/logging/operations/"+result.op.ID.String()+"/", loggingAttachMutationReceipt{
		Output:    attachAstronomerResponse(result.row, plaintext),
		Operation: loggingOperationResponse(result.op),
	})
}

func (h *LoggingHandler) lokiTokenForCluster(ctx context.Context, clusterID uuid.UUID) (sqlc.LokiIngestToken, bool) {
	if h == nil || h.queries == nil {
		return sqlc.LokiIngestToken{}, false
	}
	tok, err := h.queries.GetLokiIngestTokenByCluster(ctx, clusterID)
	if err != nil || tok.TokenHash == "" {
		return sqlc.LokiIngestToken{}, false
	}
	return tok, true
}

func clusterIDFromClusterRoute(r *http.Request) (uuid.UUID, error) {
	if raw := chi.URLParam(r, "id"); raw != "" {
		return uuid.Parse(raw)
	}
	return clusterIDFromRequest(r)
}

type loggingAttachResult struct {
	loggingOutputResponse
	Token string `json:"token,omitempty"`
}

type loggingAttachMutationReceipt struct {
	Output    loggingAttachResult `json:"output"`
	Operation map[string]any      `json:"operation"`
}

func attachAstronomerResponse(output sqlc.LoggingOutput, token string) loggingAttachResult {
	return loggingAttachResult{loggingOutputResponse: loggingOutputDTO(output), Token: token}
}
