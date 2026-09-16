package handler

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (h *SupportBundleHandler) Create(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireSuperuser(w, r, h.queries, superuserGateConfig{
		StoreUnavailableMessage: "Support bundle store not configured",
		ForbiddenMessage:        "Support bundle generation requires superuser privileges",
	}); !ok {
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	if h.runTx == nil || h.operations == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "Support bundle operation store is not configured")
		return
	}
	caller := currentUserUUID(r)
	if !caller.Valid || caller.Bytes == uuid.Nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Invalid caller")
		return
	}
	r = r.WithContext(withOperationIdempotency(r, "support_bundle"))
	idem, ok := operationIdempotencyFromContext(r.Context())
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, "A valid Idempotency-Key header is required")
		return
	}
	digestBytes := sha256.Sum256([]byte("support-bundle:v1"))
	digest := fmt.Sprintf("%x", digestBytes[:])
	var row sqlc.CreateSupportBundleOperationRow
	err := h.runTx(r.Context(), func(q SupportBundleMutationTx) error {
		var createErr error
		row, createErr = q.CreateSupportBundleOperation(r.Context(), sqlc.CreateSupportBundleOperationParams{
			RequestedBy: caller.Bytes, IdempotencyScope: idem.scope,
			IdempotencyKey: idem.key, RequestDigest: digest,
		})
		if createErr != nil {
			return createErr
		}
		if !row.Created {
			return nil
		}
		return recordAuditOutbox(r, q, "admin.support_bundle.generation_accepted",
			"support_bundle_operation", row.ID.String(), "support-bundle", http.StatusAccepted,
			map[string]any{"operation_id": row.ID.String(), "expires_at": row.ExpiresAt.UTC().Format(time.RFC3339)})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key already identifies a different support bundle request")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DBError, "Failed to persist support bundle operation")
		return
	}
	RespondAcceptedOperation(w, supportBundleStatusURL(row.ID), supportBundleResponseFromCreate(row))
}

func (h *SupportBundleHandler) GetOperation(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireSuperuser(w, r, h.queries, superuserGateConfig{StoreUnavailableMessage: "Support bundle store not configured", ForbiddenMessage: "Support bundle status requires superuser privileges"}); !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid support bundle operation ID")
		return
	}
	if h.operations == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Support bundle operation store is not configured")
		return
	}
	row, err := h.operations.GetSupportBundleOperation(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Support bundle operation not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, "Failed to load support bundle operation")
		return
	}
	RespondJSON(w, http.StatusOK, supportBundleResponseFromRow(row))
}

func (h *SupportBundleHandler) Download(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireSuperuser(w, r, h.queries, superuserGateConfig{StoreUnavailableMessage: "Support bundle store not configured", ForbiddenMessage: "Support bundle download requires superuser privileges"}); !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid support bundle operation ID")
		return
	}
	if h.operations == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Support bundle operation store is not configured")
		return
	}
	artifact, err := h.operations.GetSupportBundleArtifact(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Support bundle is not ready for download")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, "Failed to load support bundle artifact")
		return
	}
	if !artifact.ExpiresAt.After(time.Now().UTC()) {
		RespondRequestError(w, r, http.StatusGone, apierror.NotFound, "Support bundle artifact has expired")
		return
	}
	recordAudit(r, h.queries, "admin.support_bundle.downloaded", "support_bundle_operation", id.String(), artifact.Filename, map[string]any{"sha256": artifact.ArtifactSha256.String, "size": artifact.ArtifactSize})
	w.Header().Set("Content-Type", artifact.ArtifactContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+artifact.Filename+`"`)
	w.Header().Set("ETag", `"sha256:`+artifact.ArtifactSha256.String+`"`)
	w.Header().Set("X-Checksum-SHA256", artifact.ArtifactSha256.String)
	w.Header().Set("Content-Length", strconv.FormatInt(artifact.ArtifactSize, 10))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(artifact.Artifact)
}

// Generate writes one redacted support ZIP. It is invoked by the durable
// tunnel worker and kept separate from HTTP so retries cannot partially commit
// a response body.
