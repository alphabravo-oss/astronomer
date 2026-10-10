package handler

import (
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/redaction"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// Health handles POST /api/v1/admin/vault-connections/{id}/health/.
// Auth-only — no kv GET. Used by the UI's "ping" button.
func (h *VaultHandler) Health(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.gateSuperuser(w, r); !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid connection ID")
		return
	}
	lookup := observability.BeginVaultMetricLookup()
	conn, err := h.queries.GetVaultConnectionByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Vault connection not found")
		return
	}
	metrics := lookup.Bind(conn.ID, conn.Name)
	if h.probe == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Vault probe not configured")
		return
	}
	authBlob, decErr := h.decryptAuth(conn)
	if decErr != nil {
		safeErr := redaction.String(decErr.Error())
		if persistErr := h.persistHealthResult(r, vaultHealthPersistence{
			connection: conn, ok: false, lastError: safeErr, action: "admin.vault_connection.healthchecked",
			detail: map[string]any{"ok": false, "result": "decrypt_failed"},
		}); persistErr != nil {
			respondTransactionalMutationError(w, r, persistErr, http.StatusInternalServerError, apierror.WriteError, "Failed to persist Vault health result")
			return
		}
		metrics.RecordHealth(false)
		RespondJSON(w, http.StatusOK, map[string]any{"ok": false, "message": safeErr})
		return
	}
	start := time.Now()
	herr := h.probe.Health(r.Context(), conn, authBlob)
	latency := time.Since(start)
	ok := herr == nil
	msg := "ok"
	errStr := ""
	if !ok {
		msg = redaction.String(herr.Error())
		errStr = msg
	}
	if persistErr := h.persistHealthResult(r, vaultHealthPersistence{
		connection: conn, ok: ok, lastError: errStr, action: "admin.vault_connection.healthchecked",
		detail: map[string]any{"ok": ok, "latency_ms": latency.Milliseconds()},
	}); persistErr != nil {
		respondTransactionalMutationError(w, r, persistErr, http.StatusInternalServerError, apierror.WriteError, "Failed to persist Vault health result")
		return
	}
	metrics.RecordHealth(ok)
	RespondJSON(w, http.StatusOK, map[string]any{
		"ok":         ok,
		"latency_ms": latency.Milliseconds(),
		"message":    msg,
	})
}
