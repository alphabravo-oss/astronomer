package handler

import (
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

func (h *ClusterHandler) encryptLegacyRegistryPassword(password string) (string, string, error) {
	if h == nil || h.encryptor == nil || password == "" {
		return password, "", nil
	}
	encrypted, err := h.encryptor.Encrypt(password)
	if err != nil {
		return "", "", err
	}
	return "", encrypted, nil
}

// UpdateRegistryConfigRequest represents the request body for upserting registry config.
// openapi:request UpdateRegistryConfigRequest
type UpdateRegistryConfigRequest struct {
	PrivateRegistryUrl string `json:"private_registry_url"`
	RegistryUsername   string `json:"registry_username"`
	RegistryPassword   string `json:"registry_password"`
	Insecure           bool   `json:"insecure"`
	CaBundle           string `json:"ca_bundle"`
}

// GetRegistryConfig handles GET /api/v1/clusters/{id}/registry/.
func (h *ClusterHandler) GetRegistryConfig(w http.ResponseWriter, r *http.Request) {
	id, ok := parseClusterIDParam(w, r, "id")
	if !ok {
		return
	}

	config, err := h.queries.GetClusterRegistryConfig(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Registry config not found for cluster")
		return
	}

	// Never return the raw registry password or its ciphertext. Map through
	// the shared DTO so the secret is redacted to RegistryPasswordSentinel
	// (mirrors the newer /registries handler) while url, username, insecure,
	// and CA bundle remain intact.
	RespondJSON(w, http.StatusOK, clusterRegistryConfigToResponse(config))
}

// UpdateRegistryConfig handles PUT /api/v1/clusters/{id}/registry/.
func (h *ClusterHandler) UpdateRegistryConfig(w http.ResponseWriter, r *http.Request) {
	id, ok := parseClusterIDParam(w, r, "id")
	if !ok {
		return
	}

	var req UpdateRegistryConfigRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	registryPassword, registryPasswordEncrypted, err := h.encryptLegacyRegistryPassword(req.RegistryPassword)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CryptoError, "Failed to encrypt registry password")
		return
	}

	params := sqlc.UpsertClusterRegistryConfigParams{
		ClusterID:                 id,
		PrivateRegistryUrl:        req.PrivateRegistryUrl,
		RegistryUsername:          req.RegistryUsername,
		RegistryPassword:          registryPassword,
		RegistryPasswordEncrypted: registryPasswordEncrypted,
		Insecure:                  req.Insecure,
		CaBundle:                  req.CaBundle,
	}
	config, err := executeMutation(r, h.runTx,
		func(q ClusterMutationTx) (sqlc.ClusterRegistryConfig, error) {
			return q.UpsertClusterRegistryConfig(r.Context(), params)
		},
		func(config sqlc.ClusterRegistryConfig) mutationAuditEvent {
			return mutationAuditEvent{
				action: "cluster.registry.updated", resourceType: "cluster", resourceID: id.String(),
				status: http.StatusOK,
				detail: map[string]any{
					"registry_id": config.ID.String(), "private_registry_url": req.PrivateRegistryUrl,
					"registry_username": req.RegistryUsername, "insecure": req.Insecure,
				},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.UpdateError, "Failed to update registry config")
		return
	}

	// Redact the secret before echoing back — the raw sqlc row carries both
	// registry_password and registry_password_encrypted, which must never be
	// serialized (same reason GET uses the DTO mapper).
	RespondJSON(w, http.StatusOK, clusterRegistryConfigToResponse(config))
}

// DeleteRegistryConfig handles DELETE /api/v1/clusters/{id}/registry/.
func (h *ClusterHandler) DeleteRegistryConfig(w http.ResponseWriter, r *http.Request) {
	id, ok := parseClusterIDParam(w, r, "id")
	if !ok {
		return
	}

	_, err := executeMutation(r, h.runTx,
		func(q ClusterMutationTx) (struct{}, error) {
			return struct{}{}, q.DeleteClusterRegistryConfig(r.Context(), id)
		},
		func(struct{}) mutationAuditEvent {
			return mutationAuditEvent{
				action: "cluster.registry.deleted", resourceType: "cluster", resourceID: id.String(),
				status: http.StatusNoContent,
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete registry config")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
