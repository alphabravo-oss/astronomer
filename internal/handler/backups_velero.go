// Phase B2: Velero is the backup engine. This file owns the CRD shape +
// apply pipeline used by BackupHandler to project our DB rows into upstream
// Velero CRDs (BackupStorageLocation, Schedule, Backup, Restore) and to
// surface their status back. CR rendering is pure (input -> map[string]any)
// so it is unit-testable without a live Kubernetes; the apply helpers go
// through the existing tunnel K8sRequester.
package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// veleroAPIVersion is the Velero CRD group/version we target. v1 is GA across
// all supported Velero releases since 1.0; v2alpha1 (data-mover) is intentionally
// excluded — those CRs are managed implicitly by the v1 Backup spec.
const veleroAPIVersion = "velero.io/v1"

// defaultVeleroNamespace is the namespace Velero installs into and reads its
// CRDs from. Configurable per BackupStorageConfig but defaults here.
const defaultVeleroNamespace = "velero"

// VeleroBackupRender holds the inputs for rendering a Velero Backup CR. Pure
// data — no DB or k8s coupling. Input fields map 1:1 to a Velero spec.
type VeleroBackupRender struct {
	Name               string
	Namespace          string
	BackupStorageName  string
	IncludedNamespaces []string
	ExcludedNamespaces []string
	TTL                string
	Labels             map[string]string
}

// renderVeleroBackup returns the JSON body for a Velero Backup CR. The
// resulting map is emitted via json.Marshal for the K8s API; we never write
// YAML on the wire because the kube-apiserver accepts JSON over PATCH/POST.
func renderVeleroBackup(in VeleroBackupRender) map[string]any {
	if in.Namespace == "" {
		in.Namespace = defaultVeleroNamespace
	}
	spec := map[string]any{
		"storageLocation": in.BackupStorageName,
	}
	if len(in.IncludedNamespaces) > 0 {
		spec["includedNamespaces"] = in.IncludedNamespaces
	}
	if len(in.ExcludedNamespaces) > 0 {
		spec["excludedNamespaces"] = in.ExcludedNamespaces
	}
	if strings.TrimSpace(in.TTL) != "" {
		spec["ttl"] = in.TTL
	}
	meta := map[string]any{
		"name":      in.Name,
		"namespace": in.Namespace,
	}
	labels := map[string]string{
		"app.kubernetes.io/managed-by": "astronomer-go",
	}
	for k, v := range in.Labels {
		labels[k] = v
	}
	meta["labels"] = labels
	return map[string]any{
		"apiVersion": veleroAPIVersion,
		"kind":       "Backup",
		"metadata":   meta,
		"spec":       spec,
	}
}

// VeleroScheduleRender captures the inputs for a Velero Schedule CR. The
// schedule's `template` is a Velero BackupSpec — exactly the shape rendered
// by renderVeleroBackup minus the metadata.
type VeleroScheduleRender struct {
	Name               string
	Namespace          string
	BackupStorageName  string
	Cron               string
	IncludedNamespaces []string
	ExcludedNamespaces []string
	TTL                string
	Labels             map[string]string
}

func renderVeleroSchedule(in VeleroScheduleRender) map[string]any {
	if in.Namespace == "" {
		in.Namespace = defaultVeleroNamespace
	}
	template := map[string]any{
		"storageLocation": in.BackupStorageName,
	}
	if len(in.IncludedNamespaces) > 0 {
		template["includedNamespaces"] = in.IncludedNamespaces
	}
	if len(in.ExcludedNamespaces) > 0 {
		template["excludedNamespaces"] = in.ExcludedNamespaces
	}
	if strings.TrimSpace(in.TTL) != "" {
		template["ttl"] = in.TTL
	}
	meta := map[string]any{
		"name":      in.Name,
		"namespace": in.Namespace,
	}
	labels := map[string]string{
		"app.kubernetes.io/managed-by": "astronomer-go",
	}
	for k, v := range in.Labels {
		labels[k] = v
	}
	meta["labels"] = labels
	return map[string]any{
		"apiVersion": veleroAPIVersion,
		"kind":       "Schedule",
		"metadata":   meta,
		"spec": map[string]any{
			"schedule": in.Cron,
			"template": template,
		},
	}
}

// VeleroRestoreRender captures the inputs for a Velero Restore CR.
type VeleroRestoreRender struct {
	Name               string
	Namespace          string
	BackupName         string
	IncludedNamespaces []string
	NamespaceMapping   map[string]string
	Labels             map[string]string
}

func renderVeleroRestore(in VeleroRestoreRender) map[string]any {
	if in.Namespace == "" {
		in.Namespace = defaultVeleroNamespace
	}
	spec := map[string]any{
		"backupName": in.BackupName,
	}
	if len(in.IncludedNamespaces) > 0 {
		spec["includedNamespaces"] = in.IncludedNamespaces
	}
	if len(in.NamespaceMapping) > 0 {
		spec["namespaceMapping"] = in.NamespaceMapping
	}
	meta := map[string]any{
		"name":      in.Name,
		"namespace": in.Namespace,
	}
	labels := map[string]string{
		"app.kubernetes.io/managed-by": "astronomer-go",
	}
	for k, v := range in.Labels {
		labels[k] = v
	}
	meta["labels"] = labels
	return map[string]any{
		"apiVersion": veleroAPIVersion,
		"kind":       "Restore",
		"metadata":   meta,
		"spec":       spec,
	}
}

// VeleroBSLRender captures the inputs for a Velero BackupStorageLocation CR.
type VeleroBSLRender struct {
	Name             string
	Namespace        string
	Provider         string
	Bucket           string
	Prefix           string
	Region           string
	S3URL            string
	S3ForcePathStyle bool
	CredentialSecret string
	CredentialKey    string
	Default          bool
}

func renderVeleroBSL(in VeleroBSLRender) map[string]any {
	if in.Namespace == "" {
		in.Namespace = defaultVeleroNamespace
	}
	cfg := map[string]string{}
	if in.Region != "" {
		cfg["region"] = in.Region
	}
	if in.S3URL != "" {
		cfg["s3Url"] = in.S3URL
	}
	if in.S3ForcePathStyle {
		cfg["s3ForcePathStyle"] = "true"
	}
	objectStorage := map[string]any{
		"bucket": in.Bucket,
	}
	if in.Prefix != "" {
		objectStorage["prefix"] = in.Prefix
	}
	spec := map[string]any{
		"provider":      in.Provider,
		"objectStorage": objectStorage,
		"default":       in.Default,
	}
	if len(cfg) > 0 {
		spec["config"] = cfg
	}
	if in.CredentialSecret != "" {
		key := in.CredentialKey
		if key == "" {
			key = "cloud"
		}
		spec["credential"] = map[string]any{
			"name": in.CredentialSecret,
			"key":  key,
		}
	}
	return map[string]any{
		"apiVersion": veleroAPIVersion,
		"kind":       "BackupStorageLocation",
		"metadata": map[string]any{
			"name":      in.Name,
			"namespace": in.Namespace,
			"labels": map[string]string{
				"app.kubernetes.io/managed-by": "astronomer-go",
			},
		},
		"spec": spec,
	}
}

// renderVeleroCredentialsSecret produces the Secret holding aws-style
// credentials referenced by a BackupStorageLocation. The key matches what
// `velero install --provider aws` uses by default ("cloud") and contains an
// INI document with [default] access_key + secret_key. This format is read
// by Velero's plugins for AWS, GCP (with hmac creds), and any S3-compatible
// store including MinIO.
func renderVeleroCredentialsSecret(name, namespace, accessKey, secretKey string) map[string]any {
	if namespace == "" {
		namespace = defaultVeleroNamespace
	}
	body := fmt.Sprintf("[default]\naws_access_key_id=%s\naws_secret_access_key=%s\n", accessKey, secretKey)
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels": map[string]string{
				"app.kubernetes.io/managed-by": "astronomer-go",
			},
		},
		"type": "Opaque",
		"data": map[string]string{
			"cloud": base64.StdEncoding.EncodeToString([]byte(body)),
		},
	}
}

// veleroProviderForStorageType converts our DB storage_type column into the
// Velero provider plugin name. Velero's BSL spec.provider values are stable.
func veleroProviderForStorageType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "gcs", "gcp":
		return "gcp"
	case "azure", "azureblob":
		return "azure"
	case "s3", "minio", "":
		return "aws"
	default:
		return strings.ToLower(t)
	}
}

// veleroNamespacesFromJSON decodes a JSONB array column into []string. Empty
// or invalid inputs return nil so callers can pass directly to renderers.
func veleroNamespacesFromJSON(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

// veleroBSLNameFor returns the Velero BSL name we use for a storage config row.
// Stable across upserts so the server can re-PATCH safely.
func veleroBSLNameFor(cfg sqlc.BackupStorageConfig) string {
	if cfg.BslName != "" {
		return cfg.BslName
	}
	// Slugify name; fall back to the row's UUID prefix.
	name := strings.ToLower(strings.TrimSpace(cfg.Name))
	name = strings.NewReplacer(" ", "-", "_", "-", "/", "-").Replace(name)
	if name == "" {
		name = "bsl-" + cfg.ID.String()[:8]
	}
	return name
}

// veleroSecretNameFor pairs a BSL with its credentials Secret name.
func veleroSecretNameFor(cfg sqlc.BackupStorageConfig) string {
	return veleroBSLNameFor(cfg) + "-credentials"
}

// applyJSONBody PATCH-then-POSTs a JSON object through the K8s requester.
// PATCH is attempted first (to update existing CRs). 404 falls through to a
// POST; conflicts on POST are treated as success (already-exists race).
func applyJSONBody(ctx context.Context, requester K8sRequester, clusterID, patchPath, createPath string, body []byte) error {
	if requester == nil {
		return fmt.Errorf("kubernetes requester not configured")
	}
	resp, err := requester.Do(ctx, clusterID, http.MethodPatch, patchPath, body, requestHeaders("application/merge-patch+json"))
	if err == nil && resp != nil && resp.StatusCode != http.StatusNotFound {
		return ensureSuccess(resp)
	}
	resp, err = requester.Do(ctx, clusterID, http.MethodPost, createPath, body, requestHeaders("application/json"))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusConflict {
		return nil
	}
	return ensureSuccess(resp)
}

// veleroCRDPath builds the API path for a Velero CRD (cluster-scoped namespace
// list under the velero.io API group). namespace + name segments are appended
// per the verb the caller wants. This mirrors the standard k8s API URL shape.
func veleroCRDPath(namespace, kindPlural, name string) (createPath, patchPath string) {
	if namespace == "" {
		namespace = defaultVeleroNamespace
	}
	createPath = fmt.Sprintf("/apis/velero.io/v1/namespaces/%s/%s", namespace, kindPlural)
	patchPath = fmt.Sprintf("/apis/velero.io/v1/namespaces/%s/%s/%s", namespace, kindPlural, name)
	return
}

// veleroSecretPath builds the API path for a Secret in the velero namespace.
func veleroSecretPath(namespace, name string) (createPath, patchPath string) {
	if namespace == "" {
		namespace = defaultVeleroNamespace
	}
	createPath = fmt.Sprintf("/api/v1/namespaces/%s/secrets", namespace)
	patchPath = fmt.Sprintf("/api/v1/namespaces/%s/secrets/%s", namespace, name)
	return
}
