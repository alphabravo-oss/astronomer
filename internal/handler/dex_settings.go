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
	"cmp"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func (h *DexHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	row, err := h.queries.GetDexSettings(r.Context(), dexSettingsSingletonID)
	if err != nil {
		RespondJSON(w, http.StatusOK, defaultSettingsResponse())
		return
	}
	clients, row, err := h.loadPublicClients(r.Context(), row)
	if err != nil {
		apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.EncryptUnavailable, "Dex static-client secrets are unavailable")
		return
	}
	response, err := settingsResponse(row, clients)
	if err != nil {
		apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.SettingsError, "Stored Dex settings are invalid and must be repaired")
		return
	}
	RespondJSON(w, http.StatusOK, response)
}

// UpdateSettings upserts the singleton settings row. PUT /api/v1/auth/dex/settings/
func (h *DexHandler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req settingsRequest
	if err := decodeDexRequest(r.Body, &req, false); err != nil {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidBody, "Invalid JSON body")
		return
	}
	if strings.TrimSpace(req.IssuerURL) == "" {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.MissingIssuer, "issuer_url is required")
		return
	}
	if err := validateCanonicalDexURL(req.IssuerURL, true); err != nil {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, "issuer_url "+err.Error())
		return
	}
	existing, existingErr := h.queries.GetDexSettings(r.Context(), dexSettingsSingletonID)
	if h.bundledIdentity != nil {
		if existingErr != nil {
			apiutil.WriteError(w, r, http.StatusConflict, apiutil.SettingsError, "Bundled Dex bootstrap settings are unavailable; restart the server after the chart is ready")
			return
		}
		if _, err := h.normalizeRuntimeIdentity(existing); err != nil {
			apiutil.WriteError(w, r, http.StatusConflict, apiutil.SettingsError, "Bundled Dex runtime identity does not match the installed chart")
			return
		}
		identity := *h.bundledIdentity
		for field, requested := range map[string]string{
			"namespace": req.Namespace, "release_name": req.ReleaseName, "chart_release_name": req.ChartReleaseName,
			"deployment_name": req.DeploymentName, "service_name": req.ServiceName, "runtime_secret_name": req.RuntimeSecretName,
		} {
			var expected string
			switch field {
			case "namespace":
				expected = identity.Namespace
			case "release_name", "deployment_name":
				expected = identity.DeploymentName
			case "chart_release_name":
				expected = identity.ChartReleaseName
			case "service_name":
				expected = identity.ServiceName
			default:
				expected = identity.RuntimeSecretName
			}
			if requested != "" && requested != expected {
				apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, field+" is immutable for bundled Dex")
				return
			}
		}
		req.Namespace, req.ReleaseName = identity.Namespace, identity.DeploymentName
		req.ChartReleaseName, req.DeploymentName, req.ServiceName = identity.ChartReleaseName, identity.DeploymentName, identity.ServiceName
		req.RuntimeSecretName = identity.RuntimeSecretName
	} else {
		if req.Namespace == "" {
			req.Namespace = "dex"
		}
		if req.ReleaseName == "" {
			req.ReleaseName = "dex"
		}
		if req.RuntimeSecretName == "" {
			req.RuntimeSecretName = "astronomer-dex-runtime"
		}
		if req.DeploymentName == "" {
			req.DeploymentName = req.ReleaseName
		}
		if req.ServiceName == "" {
			req.ServiceName = req.ReleaseName
		}
	}
	runtimePhase := "fresh"
	if existingErr == nil {
		runtimePhase = cmp.Or(existing.RuntimePhase, runtimePhase)
	}
	if h.bundledIdentity != nil {
		runtimePhase = h.bundledIdentity.MigrationPhase
	}
	if errs := k8svalidation.IsDNS1123Label(req.Namespace); len(errs) > 0 {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, "namespace must be a valid Kubernetes DNS label")
		return
	}
	if errs := k8svalidation.IsDNS1123Label(req.ReleaseName); len(errs) > 0 {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, "release_name must be a valid Kubernetes DNS label")
		return
	}
	for label, value := range map[string]string{"deployment_name": req.DeploymentName, "service_name": req.ServiceName} {
		if errs := k8svalidation.IsDNS1123Subdomain(value); len(errs) > 0 {
			apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, label+" must be a valid Kubernetes name")
			return
		}
	}
	if errs := k8svalidation.IsDNS1123Subdomain(req.RuntimeSecretName); len(errs) > 0 {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, "runtime_secret_name must be a valid Kubernetes Secret name")
		return
	}
	clusterUUID := pgtype.UUID{}
	if req.ClusterID != "" {
		id, err := uuid.Parse(req.ClusterID)
		if err != nil {
			apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidID, "Invalid cluster_id")
			return
		}
		clusterUUID = pgtype.UUID{Bytes: id, Valid: true}
	}
	existingClients := []map[string]any{}
	if existingErr == nil {
		var loadErr error
		existingClients, _, loadErr = h.loadPublicClients(r.Context(), existing)
		if loadErr != nil {
			apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.EncryptUnavailable, "Dex static-client secrets are unavailable")
			return
		}
	}
	clients := mergePublicClientSecrets(existingClients, req.PublicClients)
	if h.bundledIdentity != nil {
		clients = existingClients
	}
	if err := validatePublicClients(clients); err != nil {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, err.Error())
		return
	}
	encryptedClients, err := h.encryptPublicClients(clients)
	if err != nil {
		apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.EncryptUnavailable, "Encryptor is not configured; cannot store Dex static clients")
		return
	}
	if h.bundledIdentity != nil {
		_ = json.Unmarshal(existing.Expiry, &req.Expiry)
		_ = json.Unmarshal(existing.Extra, &req.Extra)
	}
	if err := validateDexExtra(req.Extra); err != nil {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, err.Error())
		return
	}
	if err := validateDexExpiry(req.Expiry); err != nil {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, err.Error())
		return
	}
	expiryBytes, _ := json.Marshal(req.Expiry)
	if len(expiryBytes) == 0 || string(expiryBytes) == "null" {
		expiryBytes = []byte("{}")
	}
	extraBytes, _ := json.Marshal(req.Extra)
	if len(extraBytes) == 0 || string(extraBytes) == "null" {
		extraBytes = []byte("{}")
	}
	params := sqlc.StageDexSettingsAndDisableSSOParams{
		ID:                 dexSettingsSingletonID,
		IssuerUrl:          strings.TrimRight(req.IssuerURL, "/"),
		ClusterID:          clusterUUID,
		Namespace:          req.Namespace,
		ReleaseName:        req.ReleaseName,
		ConfigmapName:      req.RuntimeSecretName,
		RuntimeSecretName:  req.RuntimeSecretName,
		PublicClientsKeyID: encryptedClients.KeyID, PublicClientsAlgorithm: encryptedClients.Algorithm, PublicClientsCiphertext: encryptedClients.Ciphertext, PublicClientsNonce: encryptedClients.Nonce,
		Expiry:           expiryBytes,
		Extra:            extraBytes,
		ChartReleaseName: req.ChartReleaseName,
		DeploymentName:   req.DeploymentName,
		ServiceName:      req.ServiceName,
		RuntimePhase:     runtimePhase,
	}
	row, err := executeMutation(r, h.runTx,
		func(q DexMutationTx) (sqlc.DexSetting, error) {
			generation, mutationErr := q.StageDexSettingsAndDisableSSO(r.Context(), params)
			if mutationErr != nil {
				return sqlc.DexSetting{}, mutationErr
			}
			return q.GetDexSettingsForGeneration(r.Context(), sqlc.GetDexSettingsForGenerationParams{ID: dexSettingsSingletonID, RuntimeGeneration: generation})
		},
		func(row sqlc.DexSetting) mutationAuditEvent {
			return mutationAuditEvent{
				action: "dex.settings.update", resourceType: "dex_settings", resourceID: row.ID.String(), resourceName: row.ReleaseName,
				status: http.StatusOK,
				detail: map[string]any{
					"issuer_url": row.IssuerUrl, "namespace": row.Namespace,
					"runtime_secret_name": row.RuntimeSecretName, "runtime_generation": row.RuntimeGeneration,
				},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apiutil.SaveError, "Failed to save Dex settings")
		return
	}
	response, err := settingsResponse(row, clients)
	if err != nil {
		apiutil.WriteError(w, r, http.StatusInternalServerError, apiutil.SettingsError, "Saved Dex settings failed validation")
		return
	}
	RespondJSON(w, http.StatusOK, response)
}
