package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/cloudcreds"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func (h *CloudCredentialHandler) requireTestAudit(r *http.Request, row sqlc.CloudCredential, outcome string) error {
	if h == nil || h.auditor == nil {
		return audit.ErrOutboxUnavailable
	}
	return recordMandatoryAudit(r, h.auditor, "cloud_credentials.test", "cloud_credential", row.ID.String(), row.Name, map[string]any{
		"project_id": row.ProjectID.String(), "provider": row.Provider, "outcome": outcome,
	})
}

// --- Internal helpers --------------------------------------------------

// loadCredentialForRequest parses {project_id} + {id}, fetches the row,
// and verifies the credential belongs to the project. Returns the row +
// ok=true on success. On any failure it has already written a response.
func (h *CloudCredentialHandler) loadCredentialForRequest(w http.ResponseWriter, r *http.Request) (sqlc.CloudCredential, bool) {
	projectID, ok := parseProjectID(w, r)
	if !ok {
		return sqlc.CloudCredential{}, false
	}
	credentialID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid credential ID")
		return sqlc.CloudCredential{}, false
	}
	row, err := h.queries.GetCloudCredentialByID(r.Context(), credentialID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Credential not found")
		return sqlc.CloudCredential{}, false
	}
	if row.ProjectID != projectID {
		// Don't leak that the credential exists under a different project.
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Credential not found")
		return sqlc.CloudCredential{}, false
	}
	return row, true
}

// rowToResponse turns a DB row into the wire DTO, decrypting + redacting
// the data blob. includeMaterializations=true means "fetch + attach the
// per-(cluster, namespace) status rows" (only on single-row GET).
func (h *CloudCredentialHandler) rowToResponse(ctx context.Context, row sqlc.CloudCredential, includeMaterializations bool) (CloudCredentialResponse, error) {
	blob, err := h.decryptToMap(row.DataEncrypted)
	if err != nil {
		return CloudCredentialResponse{}, err
	}
	resp := CloudCredentialResponse{
		ID:          row.ID,
		ProjectID:   row.ProjectID,
		Name:        row.Name,
		Provider:    row.Provider,
		Description: row.Description,
		Data:        cloudcreds.RedactSecrets(row.Provider, blob),
		TargetRefs:  decodeStoredTargetRefs(row.TargetRefs),
		CreatedAt:   row.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:   row.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if includeMaterializations {
		mats, err := h.queries.ListCloudCredentialMaterializations(ctx, row.ID)
		if err == nil {
			out := make([]MaterializationStatus, 0, len(mats))
			for _, m := range mats {
				ms := MaterializationStatus{
					ClusterID:  m.ClusterID,
					Namespace:  m.Namespace,
					SecretName: m.SecretName,
					Status:     m.Status,
					LastError:  m.LastError,
				}
				if m.LastAppliedAt.Valid {
					ms.LastAppliedAt = m.LastAppliedAt.Time.Format("2006-01-02T15:04:05Z07:00")
				}
				out = append(out, ms)
			}
			resp.Materializations = out
		}
	}
	return resp, nil
}

// rowToErrorResponse is the fallback shape when a single row can't be
// decoded (e.g. an encryption-key rotation that left an old row under a
// dropped key). We surface a clearly-failed row instead of failing the
// whole list call.
func (h *CloudCredentialHandler) rowToErrorResponse(row sqlc.CloudCredential, err error) CloudCredentialResponse {
	return CloudCredentialResponse{
		ID:          row.ID,
		ProjectID:   row.ProjectID,
		Name:        row.Name,
		Provider:    row.Provider,
		Description: fmt.Sprintf("(decode error: %s)", err.Error()),
		Data:        map[string]string{},
		TargetRefs:  decodeStoredTargetRefs(row.TargetRefs),
	}
}

// decryptToMap is the inverse of (encode → encrypt). Returns an empty
// map for an empty ciphertext so legacy / migrated rows decode cleanly.
func (h *CloudCredentialHandler) decryptToMap(ciphertext string) (map[string]string, error) {
	if strings.TrimSpace(ciphertext) == "" {
		return map[string]string{}, nil
	}
	if h.encryptor == nil {
		return nil, errors.New("encryptor not configured")
	}
	plain, err := h.encryptor.Decrypt(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return cloudcreds.DecodeBlob([]byte(plain))
}

// canonicaliseTargetRefs validates the incoming target_refs slice and
// fills in a default secret_name for any entry that omitted one.
// Verifies each cluster_id exists; rejects bad UUIDs / empty namespaces.
//
// It also AUTHORIZES each (cluster, namespace) against the set of
// namespaces the request's project owns (project_namespaces). Without this
// a caller with Project-Update on projectID could point a target_ref at any
// (cluster, namespace) in an imported cluster owned by a different project
// and have the worker force-apply — or, on delete, remove — a Secret there.
// Ownership is loaded once up front and is mandatory for every non-empty
// target set; an unavailable ownership store rejects the write.
