package handler

// Phase B4 — Dex shim for enterprise auth.
//
// Astronomer-go itself only ever speaks generic OIDC (see internal/auth/oauth.go's
// RegisterOIDCProvider path that landed in Phase A1). Dex brokers the messy IdPs
// — Azure AD, LDAP, SAML, Okta, GitLab, etc. — and exposes a single OIDC issuer
// our SSO manager can register against.
//
// This file owns:
//   * CRUD for `dex_connectors` (one row per upstream IdP connector).
//   * Singleton settings for the running Dex deployment (issuer URL, namespace,
//     retained runtime Secret name, static clients, expiry).
//   * A connector-type registry mapping each connector kind to its required +
//     optional + secret config fields. Validation runs against this registry on
//     every write.
//   * Rendering settings + connectors into a Dex-shaped YAML document stored
//     only in a retained Kubernetes Secret mounted read-only by Dex.
//   * `register-as-sso` ergonomics: one-click row in `sso_configurations` so the
//     A1 OIDC discovery path can register Dex as a normal OIDC provider.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/dexconfig"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

func (h *DexHandler) ListConnectorTypes(w http.ResponseWriter, r *http.Request) {
	out := make([]map[string]any, 0, len(dexConnectorRegistry))
	for _, t := range dexConnectorTypes() {
		spec := dexConnectorRegistry[t]
		out = append(out, map[string]any{
			"type":         spec.Type,
			"display_hint": spec.DisplayHint,
			"required":     spec.Required,
			"optional":     spec.Optional,
			"secret":       spec.Secret,
			"nested":       nestedRequirementsToJSON(spec.Nested),
		})
	}
	RespondJSON(w, http.StatusOK, out)
}

func nestedRequirementsToJSON(in []nestedRequirement) []map[string]any {
	out := make([]map[string]any, 0, len(in))
	for _, n := range in {
		out = append(out, map[string]any{"parent": n.Parent, "keys": n.Keys})
	}
	return out
}

// ListConnectors returns every persisted connector. GET /api/v1/auth/dex/connectors/
func (h *DexHandler) ListConnectors(w http.ResponseWriter, r *http.Request) {
	rows, err := h.queries.ListDexConnectors(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list Dex connectors")
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		item, err := h.connectorResponse(row)
		if err != nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.SettingsError, "Stored Dex connector is invalid and must be repaired")
			return
		}
		items = append(items, item)
	}
	RespondJSON(w, http.StatusOK, items)
}

// GetConnector returns a single connector by ID. GET /api/v1/auth/dex/connectors/{id}/
func (h *DexHandler) GetConnector(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid connector ID")
		return
	}
	row, err := h.queries.GetDexConnectorByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Connector not found")
		return
	}
	response, err := h.connectorResponse(row)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.SettingsError, "Stored Dex connector is invalid and must be repaired")
		return
	}
	RespondJSON(w, http.StatusOK, response)
}

// CreateConnector validates + persists a new connector. POST /api/v1/auth/dex/connectors/
func (h *DexHandler) CreateConnector(w http.ResponseWriter, r *http.Request) {
	var req connectorRequest
	if err := decodeDexRequest(r.Body, &req, false); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	canonicalType, err := dexconfig.CanonicalConnectorType(req.Type)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidType, err.Error())
		return
	}
	req.Type = canonicalType
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidName, "Connector name is required")
		return
	}
	if _, ok := dexConnectorRegistry[req.Type]; !ok {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidType, fmt.Sprintf("Unknown connector type %q", req.Type))
		return
	}
	if req.Config == nil {
		req.Config = map[string]any{}
	}
	if err := validateConnectorConfig(req.Type, req.Config); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	if err := h.encryptSecretFields(req.Type, req.Config); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncryptError, "Failed to encrypt secret fields")
		return
	}
	cfgBytes, err := json.Marshal(req.Config)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MarshalError, "Failed to encode connector config")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	params := sqlc.StageCreateDexConnectorParams{
		Name:        req.Name,
		Type:        req.Type,
		DisplayName: req.DisplayName,
		Config:      cfgBytes,
		Enabled:     enabled,
	}
	staged, err := executeMutation(r, h.runTx,
		func(q DexMutationTx) (sqlc.StageCreateDexConnectorRow, error) {
			return q.StageCreateDexConnector(r.Context(), params)
		},
		func(row sqlc.StageCreateDexConnectorRow) mutationAuditEvent {
			return mutationAuditEvent{
				action: "dex.connector.create", resourceType: "dex_connector", resourceID: row.ID.String(), resourceName: row.Name,
				status: http.StatusCreated, detail: map[string]any{"type": row.Type, "enabled": row.Enabled, "runtime_generation": row.RuntimeGeneration},
			}
		})
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") || strings.Contains(strings.ToLower(err.Error()), "unique") {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "A connector with that name already exists")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create connector")
		return
	}
	row := sqlc.DexConnector{ID: staged.ID, Name: staged.Name, Type: staged.Type, DisplayName: staged.DisplayName, Config: staged.Config, Enabled: staged.Enabled, CreatedAt: staged.CreatedAt, UpdatedAt: staged.UpdatedAt}
	w.Header().Set("Location", "/api/v1/auth/dex/connectors/"+row.ID.String()+"/")
	response, err := h.connectorResponse(row)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.SettingsError, "Saved Dex connector failed validation")
		return
	}
	RespondJSON(w, http.StatusCreated, response)
}

// UpdateConnector PATCHes an existing connector. PATCH /api/v1/auth/dex/connectors/{id}/
//
// We accept partial bodies: omitted fields keep their current value. The
// `config` map is treated as a full replacement when present (partial config
// merges would be ambiguous for things like `groups`).
func (h *DexHandler) UpdateConnector(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid connector ID")
		return
	}
	existing, err := h.queries.GetDexConnectorByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Connector not found")
		return
	}
	var req connectorRequest
	if err := decodeDexRequest(r.Body, &req, false); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	connectorType := existing.Type
	if req.Type != "" {
		t, canonicalErr := dexconfig.CanonicalConnectorType(req.Type)
		if canonicalErr != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidType, canonicalErr.Error())
			return
		}
		if t != existing.Type {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "connector type is immutable")
			return
		}
	}
	displayName := existing.DisplayName
	if req.DisplayName != "" {
		displayName = req.DisplayName
	}
	enabled := existing.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	cfgBytes := existing.Config
	if req.Config != nil {
		// Merge: any secret field left empty in the request is preserved from
		// the existing row (so the UI can PATCH without resending the secret).
		merged := mergeSecretFromExisting(connectorType, existing.Config, req.Config)
		if err := validateConnectorConfig(connectorType, merged); err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
			return
		}
		if err := h.encryptSecretFields(connectorType, merged); err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncryptError, "Failed to encrypt secret fields")
			return
		}
		raw, err := json.Marshal(merged)
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.MarshalError, "Failed to encode connector config")
			return
		}
		cfgBytes = raw
	}
	params := sqlc.StageUpdateDexConnectorParams{
		ConnectorID: id,
		Type:        connectorType,
		DisplayName: displayName,
		Config:      cfgBytes,
		Enabled:     enabled,
	}
	staged, err := executeMutation(r, h.runTx,
		func(q DexMutationTx) (sqlc.StageUpdateDexConnectorRow, error) {
			return q.StageUpdateDexConnector(r.Context(), params)
		},
		func(row sqlc.StageUpdateDexConnectorRow) mutationAuditEvent {
			return mutationAuditEvent{
				action: "dex.connector.update", resourceType: "dex_connector", resourceID: row.ID.String(), resourceName: row.Name,
				status: http.StatusOK, detail: map[string]any{"type": row.Type, "enabled": row.Enabled, "runtime_generation": row.RuntimeGeneration},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.UpdateError, "Failed to update connector")
		return
	}
	row := sqlc.DexConnector{ID: staged.ID, Name: staged.Name, Type: staged.Type, DisplayName: staged.DisplayName, Config: staged.Config, Enabled: staged.Enabled, CreatedAt: staged.CreatedAt, UpdatedAt: staged.UpdatedAt}
	response, err := h.connectorResponse(row)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.SettingsError, "Saved Dex connector failed validation")
		return
	}
	RespondJSON(w, http.StatusOK, response)
}

// DeleteConnector removes a connector. DELETE /api/v1/auth/dex/connectors/{id}/
func (h *DexHandler) DeleteConnector(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid connector ID")
		return
	}
	existing, err := h.queries.GetDexConnectorByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Connector not found")
		return
	}
	_, err = executeMutation(r, h.runTx,
		func(q DexMutationTx) (int64, error) { return q.StageDeleteDexConnector(r.Context(), id) },
		func(generation int64) mutationAuditEvent {
			return mutationAuditEvent{
				action: "dex.connector.delete", resourceType: "dex_connector", resourceID: id.String(), resourceName: existing.Name,
				status: http.StatusOK, detail: map[string]any{"type": existing.Type, "runtime_generation": generation},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete connector")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"deleted": id.String()})
}

// GetSettings returns the singleton Dex settings row. GET /api/v1/auth/dex/settings/
//
// When the row hasn't been created yet (fresh install) we return zero values
// so the UI's first-time setup wizard can pre-fill defaults.
