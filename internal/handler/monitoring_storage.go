package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/google/uuid"
	"sigs.k8s.io/yaml"
)

type storageConfigAuthorizer func(sqlc.BackupStorageConfig) error

// errStorageConfigForbidden / errStorageConfigAuthzFailed are the two outcomes
// a storageConfigAuthorizer can produce that must NOT be reported as a bad
// request body. respondStackPayloadError maps them to 403 / 500.
var (
	errStorageConfigForbidden   = errors.New("you do not have permission to use this object storage configuration")
	errStorageConfigAuthzFailed = errors.New("failed to retrieve user permissions")
)

// clusterStorageConfigAuthorizer is the guard for a PER-CLUSTER stack install
// on clusterID. A reference is allowed when any of the following holds:
//
//  1. the config belongs to the routed cluster (cluster_id == clusterID) — it
//     is that cluster's own object and the caller already holds a monitoring
//     write on it to be here at all;
//  2. the caller holds backups:read at the config's OWN scope — the same rule
//     authorizeBackup applies to every other read of that row;
//  3. the caller holds a fleet-wide monitoring grant at routeVerb — i.e. they
//     could have reached this exact handler back when its gate was a global
//     check. This clause is pre-fix parity and nothing more, which is why it
//     takes the ROUTE's verb rather than any monitoring verb: a Monitoring
//     Admin installing against the fleet's default (unscoped) storage config
//     must keep working, but a global monitoring:read holder must not gain the
//     install path they never had.
//
// Anything else — most importantly a single-cluster tenant naming a global or
// a neighbouring tenant's config — is refused.
func (h *MonitoringHandler) clusterStorageConfigAuthorizer(ctx context.Context, clusterID uuid.UUID, routeVerb rbac.Verb) storageConfigAuthorizer {
	return func(cfg sqlc.BackupStorageConfig) error {
		bindings, restricted, err := h.authz.bindingsForContext(ctx)
		if err != nil {
			return errStorageConfigAuthzFailed
		}
		if !restricted {
			return nil
		}
		if cfg.ClusterID.Valid {
			if uuid.UUID(cfg.ClusterID.Bytes) == clusterID {
				return nil
			}
			if h.authz.allowsCluster(bindings, uuid.UUID(cfg.ClusterID.Bytes), rbac.ResourceBackups, rbac.VerbRead) {
				return nil
			}
		} else if h.authz.allowsGlobal(bindings, rbac.ResourceBackups, rbac.VerbRead) {
			return nil
		}
		if h.authz.allowsGlobal(bindings, rbac.ResourceMonitoring, routeVerb) {
			return nil
		}
		return errStorageConfigForbidden
	}
}

// respondStackPayloadError maps a monitoringStackPayload failure to a status.
// Everything it returns is a complaint about the request body EXCEPT the two
// storage-config authorization outcomes: answering those 400 would tell the
// caller their body was malformed and invite them to retry it.
func respondStackPayloadError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errStorageConfigForbidden):
		// The canonical wording every other permission denial uses, not the
		// sentinel's text: the response must not say which of the authorizer's
		// clauses the caller failed, or whether the id they guessed resolved to
		// a row at all.
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "You do not have permission to perform this action")
	case errors.Is(err, errStorageConfigAuthzFailed):
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to retrieve user permissions")
	default:
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, err.Error())
	}
}

func (h *MonitoringHandler) objectStoreSecretSpec(ctx context.Context, storageConfigID, overrideName, defaultName string, authorize storageConfigAuthorizer) (objectStoreSecretSpec, error) {
	if h.queries == nil {
		return objectStoreSecretSpec{}, fmt.Errorf("monitoring store not configured")
	}
	storageID, err := uuid.Parse(storageConfigID)
	if err != nil {
		return objectStoreSecretSpec{}, fmt.Errorf("invalid storageConfigId")
	}
	storageCfg, err := h.queries.GetBackupStorageConfigByID(ctx, storageID)
	if err != nil {
		return objectStoreSecretSpec{}, fmt.Errorf("backup storage config not found")
	}
	// Before any field of the row is read, including into an error message.
	if authorize != nil {
		if err := authorize(storageCfg); err != nil {
			return objectStoreSecretSpec{}, err
		}
	}
	if storageCfg.Bucket == "" {
		return objectStoreSecretSpec{}, fmt.Errorf("storage config bucket is required")
	}
	content, err := h.buildObjstoreConfigYAML(storageCfg)
	if err != nil {
		return objectStoreSecretSpec{}, fmt.Errorf("failed to build object storage config: %w", err)
	}
	name := defaultString(overrideName, defaultName)
	return objectStoreSecretSpec{
		Name:            name,
		Key:             "objstore.yml",
		Content:         content,
		StorageConfigID: storageConfigID,
	}, nil
}

// storageCredentials returns the S3 access/secret pair for a backup storage
// config.
//
// It is not simply cfg.AccessKey/cfg.SecretKey: since the credential-at-rest
// change those columns are deliberately BLANKED whenever the encrypted column
// is populated (legacyBackupCredentialColumns in backups.go), so reading them
// directly yields "" on every deployment that has an encryptor — and the stack
// was then installed with an empty-credential objstore secret and no error
// anywhere. Fail loudly instead when the row is sealed and we cannot open it.
func (h *MonitoringHandler) storageCredentials(cfg sqlc.BackupStorageConfig) (string, string, error) {
	if cfg.EncryptedCredentials == "" {
		return cfg.AccessKey, cfg.SecretKey, nil
	}
	if h == nil || h.encryptor == nil {
		return "", "", fmt.Errorf("object storage credentials are encrypted but no encryption key is configured")
	}
	plaintext, err := h.encryptor.Decrypt(cfg.EncryptedCredentials)
	if err != nil {
		return "", "", fmt.Errorf("failed to decrypt object storage credentials")
	}
	var creds struct {
		AccessKey string `json:"access_key"`
		SecretKey string `json:"secret_key"`
	}
	if err := json.Unmarshal([]byte(plaintext), &creds); err != nil {
		return "", "", fmt.Errorf("object storage credentials are malformed")
	}
	return creds.AccessKey, creds.SecretKey, nil
}

func (h *MonitoringHandler) buildObjstoreConfigYAML(storageCfg sqlc.BackupStorageConfig) (string, error) {
	accessKey, secretKey, err := h.storageCredentials(storageCfg)
	if err != nil {
		return "", err
	}
	objstoreConfig := map[string]any{
		"type": "S3",
		"config": map[string]any{
			"bucket":     storageCfg.Bucket,
			"endpoint":   storageCfg.EndpointUrl,
			"region":     storageCfg.Region,
			"access_key": accessKey,
			"secret_key": secretKey,
		},
	}
	if storageCfg.Prefix != "" {
		objstoreConfig["prefix"] = storageCfg.Prefix
	}
	if storageCfg.EndpointUrl == "" {
		objstoreConfig["config"].(map[string]any)["endpoint"] = "s3.amazonaws.com"
	}
	raw, err := yaml.Marshal(objstoreConfig)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (h *MonitoringHandler) ensureObjectStoreSecret(ctx context.Context, clusterID, namespace string, spec objectStoreSecretSpec) error {
	if h.requester == nil {
		return fmt.Errorf("kubernetes requester not configured")
	}
	if err := h.ensureNamespace(ctx, clusterID, namespace); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      spec.Name,
			"namespace": namespace,
			"labels": map[string]string{
				"app.kubernetes.io/managed-by": "astronomer-go",
				"astronomer.io/component":      "monitoring",
			},
		},
		"type": "Opaque",
		"stringData": map[string]string{
			spec.Key: spec.Content,
		},
	})
	if err != nil {
		return err
	}
	patchPath := fmt.Sprintf("/api/v1/namespaces/%s/secrets/%s", namespace, spec.Name)
	resp, err := h.requester.Do(ctx, clusterID, http.MethodPatch, patchPath, body, requestHeaders("application/merge-patch+json"))
	if err == nil && resp != nil && resp.StatusCode != http.StatusNotFound {
		return ensureSuccess(resp)
	}
	createPath := fmt.Sprintf("/api/v1/namespaces/%s/secrets", namespace)
	resp, err = h.requester.Do(ctx, clusterID, http.MethodPost, createPath, body, requestHeaders("application/json"))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusConflict {
		return nil
	}
	return ensureSuccess(resp)
}

func (h *MonitoringHandler) ensureNamespace(ctx context.Context, clusterID, namespace string) error {
	if h.requester == nil {
		return fmt.Errorf("kubernetes requester not configured")
	}
	path := fmt.Sprintf("/api/v1/namespaces/%s", namespace)
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return err
	}
	if resp.StatusCode < http.StatusBadRequest {
		return nil
	}
	if resp.StatusCode != http.StatusNotFound {
		return ensureSuccess(resp)
	}
	body, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]any{
			"name": namespace,
			"labels": map[string]string{
				"app.kubernetes.io/managed-by": "astronomer-go",
			},
		},
	})
	if err != nil {
		return err
	}
	resp, err = h.requester.Do(ctx, clusterID, http.MethodPost, "/api/v1/namespaces", body, requestHeaders("application/json"))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusConflict {
		return nil
	}
	return ensureSuccess(resp)
}
