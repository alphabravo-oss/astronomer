package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	imonitoring "github.com/alphabravocompany/astronomer-go/internal/monitoring"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/jackc/pgx/v5"
)

func (h *MonitoringHandler) GetBackendConfig(w http.ResponseWriter, r *http.Request) {
	// The response embeds the backend's decoded authConfig (operator-supplied
	// backend auth material), so this read carries the same monitoring gate as
	// its mutating sibling — it was previously reachable unauthenticated.
	if !h.authz.authorizeGlobalAction(w, r, rbac.ResourceMonitoring, rbac.VerbRead) {
		return
	}
	if h.queries == nil {
		RespondJSON(w, http.StatusOK, map[string]any{})
		return
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(r.Context())
	if err != nil {
		if err == pgx.ErrNoRows {
			RespondJSON(w, http.StatusOK, map[string]any{})
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, "Failed to load monitoring backend")
		return
	}
	RespondJSON(w, http.StatusOK, monitoringBackendResponse(backend, h.readAuthConfig(backend)))
}

// readAuthConfig resolves a backend's auth_config for a RESPONSE.
//
// Unlike the write paths, a decrypt failure here is not fatal: the response
// carries no credential either way, and the operationPolicies block and the
// URLs are exactly what an operator needs while diagnosing a key problem. It
// falls back to the stored projection run through the at-rest split, so the
// answer is non-secret by construction even if a mid-rollout write by an old
// binary left something in the JSONB. The credential key names go missing,
// which is honest — we genuinely cannot tell what the envelope holds.
func (h *MonitoringHandler) readAuthConfig(backend sqlc.MonitoringBackend) map[string]any {
	authConfig, err := resolveMonitoringBackendAuthConfig(backend, h.monitoringDecryptor())
	if err != nil {
		h.log.Error("decrypt monitoring backend credential for read", "error", err)
		return imonitoring.StripAuthConfigSecrets(imonitoring.DecodeAuthConfig(backend.AuthConfig))
	}
	return authConfig
}

func (h *MonitoringHandler) UpdateBackendConfig(w http.ResponseWriter, r *http.Request) {
	if !h.authz.authorizeGlobalAction(w, r, rbac.ResourceMonitoring, rbac.VerbUpdate) {
		return
	}
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.MonitoringError, "monitoring store not configured")
		return
	}
	var req UpdateMonitoringBackendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	// RMW site (migration 146). This used to be a pure writer: whatever the
	// client sent as authConfig became the whole stored document. That stopped
	// being viable the moment reads started answering with the non-secret
	// projection — the UI's own GET → edit → PUT round-trip would post an
	// authConfig with no credential in it, and the credential would be gone.
	// The same replace-everything behaviour also silently discarded the
	// shared-Thanos / shared-Alertmanager deployment metadata that lives in
	// this column, on every backend edit.
	//
	// So: the stored document is the base. An ABSENT authConfig means "leave
	// the credential alone"; a PRESENT one is authoritative for the credential
	// and merges over the config-bag keys, which the client never sends.
	existing, err := h.queries.GetDefaultMonitoringBackend(r.Context())
	switch {
	case err == nil:
	case errors.Is(err, pgx.ErrNoRows):
		// First save. There is nothing to preserve.
		existing = sqlc.MonitoringBackend{}
	default:
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, "Failed to load monitoring backend")
		return
	}
	storedCfg, err := resolveMonitoringBackendAuthConfig(existing, h.monitoringDecryptor())
	if err != nil {
		// Fail the write rather than merge against a document we could not
		// read: an operator editing a timeout would otherwise have their
		// credential deleted as a side effect.
		h.log.Error("decrypt monitoring backend credential for update", "error", err)
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError,
			"Failed to read the existing monitoring credential; check the platform encryption key")
		return
	}
	authConfigMap := storedCfg
	if req.AuthConfig != nil {
		// The client is authoritative about the credential portion; the
		// config-bag keys it does not know about are carried forward.
		merged := imonitoring.StripAuthConfigSecrets(storedCfg)
		for key, value := range decodeJSONMap(req.AuthConfig) {
			merged[key] = value
		}
		authConfigMap = merged
	}
	policies := mapFromMapValue(authConfigMap["operationPolicies"])
	if req.DefaultAutoRollbackOnFailure != nil {
		policies["defaultAutoRollbackOnFailure"] = *req.DefaultAutoRollbackOnFailure
	}
	if req.MaxRetryAttempts > 0 {
		policies["maxRetryAttempts"] = req.MaxRetryAttempts
	} else if _, ok := policies["maxRetryAttempts"]; !ok {
		policies["maxRetryAttempts"] = int32(1)
	}
	authConfigMap["operationPolicies"] = policies
	if req.BackendType == "" {
		req.BackendType = "thanos"
	}
	if req.QueryURL == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "queryUrl is required")
		return
	}
	params := sqlc.UpsertDefaultMonitoringBackendParams{
		BackendType:        req.BackendType,
		QueryUrl:           req.QueryURL,
		AlertmanagerUrl:    req.AlertmanagerURL,
		TenantID:           req.TenantID,
		AuthType:           req.AuthType,
		DefaultStepSeconds: req.DefaultStepSeconds,
		TimeoutSeconds:     req.TimeoutSeconds,
		CreatedByID:        currentUserUUID(r),
	}
	if h.monitoringSealer() == nil && imonitoring.HasAuthConfigSecret(authConfigMap) {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.CryptoError, "Monitoring credential encryption is unavailable")
		return
	}
	if err := imonitoring.SealInto(&params, authConfigMap, h.monitoringSealer()); err != nil {
		h.log.Error("encrypt monitoring backend credential", "error", err)
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, "Failed to secure the monitoring credential")
		return
	}
	backend, err := executeMutation(r, h.runTx,
		func(q MonitoringMutationTx) (sqlc.MonitoringBackend, error) {
			return q.UpsertDefaultMonitoringBackend(r.Context(), params)
		},
		func(backend sqlc.MonitoringBackend) mutationAuditEvent {
			return mutationAuditEvent{action: "monitoring.endpoint.update", resourceType: "monitoring_backend", resourceID: backend.ID.String(), resourceName: backend.BackendType, status: http.StatusOK, detail: map[string]any{
				"tenant_id": backend.TenantID, "auth_type": backend.AuthType,
			}}
		})
	if err != nil {
		respondMonitoringMutationError(w, r, err, http.StatusInternalServerError, apierror.MonitoringError, "Failed to save monitoring backend")
		return
	}
	// UpdateBackendConfig is the upsert behind both CreateEndpoint and
	// UpdateEndpoint; we record the action as "update" — when the row didn't
	// exist before this is effectively a "create", but distinguishing the two
	// would require a pre-read and isn't worth the extra round-trip.
	// The response renders the document this request just produced rather than
	// a re-read of the row — the stored JSONB is now the stripped projection,
	// so a re-read would report `authConfigKeys: []` and make a successful save
	// look like a dropped credential — but it is REDACTED exactly like the
	// read. It has to be: since this handler became a read-modify-write, the
	// document it renders is the merge of the caller's input over the STORED
	// one, so echoing it unredacted would hand a caller who omitted authConfig
	// (or who supplied only some of its keys) the credential they never held.
	// monitoring:update must not become a way to read the credential.
	RespondJSON(w, http.StatusOK, monitoringBackendResponse(backend, authConfigMap))
}

// monitoringBackendResponse renders a backend row from its RESOLVED
// auth_config document. authConfig is operator-supplied backend auth material
// (bearer tokens, basic-auth passwords, custom headers) and monitoring:read is
// a low-privilege verb — the shipped troubleshooter/viewer templates all carry
// it — so responses get the non-secret projection only: the operationPolicies
// block, plus the key names so an operator can still see that credentials are
// configured.
//
// There is deliberately no write-response variant. There used to be one, on
// the argument that the monitoring:update caller had just supplied the
// authConfig in the same request and so was being told nothing it did not
// already hold. Migration 146 falsified that argument: UpdateBackendConfig is
// now a read-modify-write, so the document it renders is the caller's input
// MERGED OVER THE STORED ONE, and a request that omitted authConfig renders
// the stored credential. One redaction, applied to every response, is the only
// version of this that cannot drift back into a disclosure.
func monitoringBackendResponse(backend sqlc.MonitoringBackend, authConfig map[string]any) map[string]any {
	return monitoringBackendPayload(backend, authConfig)
}

// resolveMonitoringBackendAuthConfig returns the COMPLETE stored auth_config
// document for a backend row (migration 146).
//
// Every read-modify-write on this column starts here and every one of them
// must treat the error as fatal TO THE WRITE — and, in the unattended worker,
// to the write only: internal/worker/tasks/monitoring_reconcile.go logs and
// skips its status stamp rather than failing the tick, because per-cluster
// reconciliation needs no monitoring credential and must not be frozen by one.
// The column is a mixed credential/config bag: four separate paths mutate a
// non-secret key in it
// (shared-Thanos metadata, shared-Alertmanager metadata, shared-alerting asset
// hashes, and the reconcile status stamp in the worker), and any one of them
// that re-marshalled the stored JSONB projection instead of the resolved
// document would persist "this backend has no credential". The operator would
// see monitoring stop authenticating after an unrelated policy edit, with
// nothing in the audit trail pointing at the cause.
func resolveMonitoringBackendAuthConfig(backend sqlc.MonitoringBackend, dec imonitoring.Decryptor) (map[string]any, error) {
	full, err := imonitoring.ResolveAuthConfig(backend.AuthConfigEncrypted, backend.AuthConfig, dec)
	if err != nil {
		return nil, err
	}
	return imonitoring.DecodeAuthConfig(full), nil
}

// monitoringBackendPayload renders a backend row from its RESOLVED auth_config
// document.
//
// It takes the resolved document rather than the row so the ciphertext column
// has no path into a response, and so `authConfigKeys` keeps meaning "which
// credential keys are configured" after migration 146 sealed them out of the
// JSONB. Deriving that list from the stored projection instead would report an
// empty list for every sealed backend — an operator with monitoring:read would
// be told no credential is configured when one is.
func monitoringBackendPayload(backend sqlc.MonitoringBackend, authConfig map[string]any) map[string]any {
	return map[string]any{
		"id":                 backend.ID.String(),
		"name":               backend.Name,
		"backendType":        backend.BackendType,
		"queryUrl":           backend.QueryUrl,
		"alertmanagerUrl":    backend.AlertmanagerUrl,
		"tenantId":           backend.TenantID,
		"authType":           backend.AuthType,
		"authConfig":         redactedMonitoringAuthConfig(authConfig),
		"authConfigKeys":     monitoringAuthConfigKeys(authConfig),
		"operationPolicies":  mapFromMapValue(authConfig["operationPolicies"]),
		"defaultStepSeconds": backend.DefaultStepSeconds,
		"timeoutSeconds":     backend.TimeoutSeconds,
		"isDefault":          backend.IsDefault,
	}
}

// redactedMonitoringAuthConfig keeps only the keys that are definitionally not
// credentials. operationPolicies is retry/rollback policy the UI reads back;
// everything else in authConfig is treated as secret, because the shape is
// operator-authored and an allow-list is the only safe direction.
//
// This list is deliberately NARROWER than imonitoring.NonSecretAuthConfigKeys
// (the at-rest split). Being narrower is always safe — a key that stays in the
// clear on disk but is withheld from a monitoring:read response leaks nothing
// — whereas being wider would render something the envelope had sealed. The
// shared-stack metadata is served by its own status endpoints rather than
// smuggled through here. TestRedactedMonitoringAuthConfigIsSubsetOfNonSecret
// pins the direction.
func redactedMonitoringAuthConfig(authConfig map[string]any) map[string]any {
	out := map[string]any{}
	if _, ok := authConfig["operationPolicies"]; ok {
		out["operationPolicies"] = mapFromMapValue(authConfig["operationPolicies"])
	}
	return out
}

// monitoringAuthConfigKeys lists the authConfig key names (never values) so a
// read-only operator can tell whether auth material is configured without
// receiving it — the same "configured, not disclosed" shape the SIEM forwarder
// surface uses.
//
// Since migration 146 it means exactly "the keys the envelope holds": the
// caller passes the RESOLVED document and the non-secret config-bag keys are
// filtered out by the same allow-list the at-rest split uses, so an operator
// is not told that `sharedThanos` is auth material.
func monitoringAuthConfigKeys(authConfig map[string]any) []string {
	return imonitoring.AuthConfigSecretKeyNames(authConfig)
}
