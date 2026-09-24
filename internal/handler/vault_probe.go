package handler

import (
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/redaction"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// Test handles POST /api/v1/admin/vault-connections/{id}/test/.
// Body: {"probe_path": "secret/data/_health"} — optional; defaults to a
// known-empty path under the connection's default_mount.
func (h *VaultHandler) Test(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.gateSuperuser(w, r); !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid connection ID")
		return
	}
	conn, err := h.queries.GetVaultConnectionByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Vault connection not found")
		return
	}
	if h.probe == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Vault probe not configured")
		return
	}
	// openapi:request-operation adminVaultConnectionTest
	var body struct {
		ProbePath string `json:"probe_path"`
	}
	if !decodeOptionalJSON(w, r, &body) {
		return
	}
	if body.ProbePath == "" {
		body.ProbePath = "_health"
	}

	authBlob, decErr := h.decryptAuth(conn)
	if decErr != nil {
		safeErr := redaction.String(decErr.Error())
		if persistErr := h.persistHealthResult(r, vaultHealthPersistence{
			connection: conn, ok: false, lastError: safeErr, action: "admin.vault_connection.tested",
			detail: map[string]any{"ok": false, "probe_path": body.ProbePath, "result": "decrypt_failed"},
		}); persistErr != nil {
			respondTransactionalMutationError(w, r, persistErr, http.StatusInternalServerError, apierror.WriteError, "Failed to persist Vault test result")
			return
		}
		RespondJSON(w, http.StatusOK, TestResult{OK: false, Message: safeErr})
		return
	}

	res, err := h.probe.Test(r.Context(), conn, authBlob, body.ProbePath)
	if err != nil {
		safeErr := redaction.String(err.Error())
		if persistErr := h.persistHealthResult(r, vaultHealthPersistence{
			connection: conn, ok: false, lastError: safeErr, action: "admin.vault_connection.tested",
			detail: map[string]any{"ok": false, "probe_path": body.ProbePath, "result": "probe_failed"},
		}); persistErr != nil {
			respondTransactionalMutationError(w, r, persistErr, http.StatusInternalServerError, apierror.WriteError, "Failed to persist Vault test result")
			return
		}
		observability.RecordVaultHealth(conn.Name, false)
		RespondJSON(w, http.StatusOK, TestResult{OK: false, Message: safeErr, ProbePath: body.ProbePath})
		return
	}
	if persistErr := h.persistHealthResult(r, vaultHealthPersistence{
		connection: conn, ok: res.OK, action: "admin.vault_connection.tested",
		detail: map[string]any{"ok": res.OK, "latency_ms": res.LatencyMS, "probe_path": body.ProbePath},
	}); persistErr != nil {
		respondTransactionalMutationError(w, r, persistErr, http.StatusInternalServerError, apierror.WriteError, "Failed to persist Vault test result")
		return
	}
	observability.RecordVaultHealth(conn.Name, res.OK)
	RespondJSON(w, http.StatusOK, res)
}
