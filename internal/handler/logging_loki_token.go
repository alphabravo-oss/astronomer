package handler

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/lokiauth"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// RotateOutputToken handles POST /api/v1/logging/outputs/{id}/rotate-token/
// and POST /api/v1/clusters/{id}/logging/outputs/{output_id}/rotate-token/.
//
// Plaintext is returned once. Postgres stores SHA-256 hash + Fernet ciphertext.
// The management-cluster hash Secret is reconciled without plaintext.
func (h *LoggingHandler) RotateOutputToken(w http.ResponseWriter, r *http.Request) {
	outputID, err := parseOutputIDParam(r)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid output ID")
		return
	}
	output, err := h.queries.GetLoggingOutputByID(r.Context(), outputID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Logging output not found")
		return
	}
	if !output.ClusterID.Valid {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Logging output has no cluster_id")
		return
	}
	clusterID := uuid.UUID(output.ClusterID.Bytes)
	if raw := clusterIDParamForRotate(r); raw != "" {
		want, parseErr := uuid.Parse(raw)
		if parseErr != nil || want != clusterID {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Output does not belong to this cluster")
			return
		}
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceLogging, rbac.VerbUpdate) {
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	if !strings.EqualFold(output.OutputType, "loki") {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Ingest tokens are only issued for Loki outputs")
		return
	}
	if h.encryptor == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Encryption is not configured")
		return
	}

	plaintext, err := mintLokiIngestToken()
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to mint ingest token")
		return
	}
	sealed, err := h.encryptor.Encrypt(plaintext)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to encrypt ingest token")
		return
	}
	params := sqlc.UpsertLokiIngestTokenParams{
		ClusterID:      clusterID,
		TokenHash:      lokiauth.HashBearer(plaintext),
		TokenEncrypted: sealed,
		CreatedByID:    currentUserUUID(r),
	}
	mutationContext := withOperationIdempotency(r, "logging")
	result, err := executeLoggingMutation(r, h,
		func(q LoggingMutationTx) (loggingMutationResult[sqlc.LokiIngestToken], error) {
			row, storeErr := q.UpsertLokiIngestToken(r.Context(), params)
			if storeErr != nil {
				return loggingMutationResult[sqlc.LokiIngestToken]{}, storeErr
			}
			op, opErr := createLoggingOutputApplyOperation(mutationContext, q, output, currentUserUUID(r))
			return loggingMutationResult[sqlc.LokiIngestToken]{row: row, op: op}, opErr
		},
		func() (loggingMutationResult[sqlc.LokiIngestToken], error) {
			row, storeErr := h.queries.UpsertLokiIngestToken(r.Context(), params)
			if storeErr != nil {
				return loggingMutationResult[sqlc.LokiIngestToken]{}, storeErr
			}
			op, opErr := createLoggingOutputApplyOperation(mutationContext, h.queries, output, currentUserUUID(r))
			return loggingMutationResult[sqlc.LokiIngestToken]{row: row, op: op}, opErr
		},
		func(result loggingMutationResult[sqlc.LokiIngestToken]) clusterAuditEvent {
			return clusterAuditEvent{action: "logging.loki_token.rotate", resourceType: "loki_ingest_token", resourceID: result.row.ID.String(), resourceName: output.Name, status: http.StatusAccepted, detail: map[string]any{
				"cluster_id": clusterID.String(), "output_id": output.ID.String(), "operation_id": operationIDOrEmpty(result.op),
			}}
		})
	if err != nil {
		respondLoggingMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to rotate ingest token")
		return
	}
	row := result.row
	h.afterLoggingOperationCommit(result.op)
	if h.lokiIngest != nil {
		if recErr := h.lokiIngest.ReconcileLokiIngest(r.Context()); recErr != nil && h.log != nil {
			h.log.Warn("loki ingest reconcile after rotate failed", "error", recErr, "cluster_id", clusterID.String())
		}
	}
	RespondAcceptedOperation(w, "/api/v1/logging/operations/"+result.op.ID.String()+"/", loggingTokenRotationReceipt{
		ClusterID: clusterID.String(),
		Token:     plaintext,
		RotatedAt: row.RotatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		Operation: loggingOperationResponse(result.op),
	})
}

type loggingTokenRotationReceipt struct {
	ClusterID string         `json:"clusterId"`
	Token     string         `json:"token"`
	RotatedAt string         `json:"rotatedAt"`
	Operation map[string]any `json:"operation"`
}

func clusterIDParamForRotate(r *http.Request) string {
	if raw := chi.URLParam(r, "cluster_id"); raw != "" {
		return raw
	}
	if chi.URLParam(r, "output_id") != "" {
		return chi.URLParam(r, "id")
	}
	return ""
}

func parseOutputIDParam(r *http.Request) (uuid.UUID, error) {
	if raw := chi.URLParam(r, "output_id"); raw != "" {
		return uuid.Parse(raw)
	}
	return uuid.Parse(chi.URLParam(r, "id"))
}

func mintLokiIngestToken() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}
