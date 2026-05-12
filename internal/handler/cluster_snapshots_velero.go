// Per-cluster Velero CRD driver (migration 052).
//
// The handler in cluster_snapshots.go translates HTTP requests into DB
// rows and then drives the Velero CRDs on the target member cluster
// through the existing tunnel K8sRequester. The CRD shapes are simple
// enough that we deliberately avoid pulling in the upstream Velero Go
// types (github.com/vmware-tanzu/velero) — that repo carries a heavy
// transitive dep tree (Velero plugins, csi snapshot APIs) we don't
// need. Instead we construct unstructured map[string]any values and
// marshal to JSON for the kube-apiserver, which mirrors how
// backups_velero.go already handles the management-plane CRs.
//
// CRDs touched:
//   - velero.io/v1/Backup                — create + GET poll + label list
//   - velero.io/v1/Restore               — create + GET poll
//   - velero.io/v1/DeleteBackupRequest   — Velero's indirect-deletion CR
//                                          (a DELETE on /backups/{name}
//                                          directly tells Velero nothing
//                                          about cleaning up the object
//                                          store backing data).
//   - velero.io/v1/BackupStorageLocation — read-only, for the
//                                          velero-status pre-flight.
//
// Each helper here is pure transport: marshal → POST/GET → ensureSuccess
// → parsed map. The business-logic mapping (Velero phases ↔ DB phase
// column) lives in the poller worker.

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// PerClusterSnapshotRender is the input bundle for a Velero Backup CRD
// targeting a member cluster. We keep this separate from the existing
// VeleroBackupRender so the two code paths can evolve independently —
// the management-plane Render targets the control plane's own Velero
// and only knows a tiny subset of BackupSpec fields, while this one
// surfaces everything the cluster-snapshot UX needs.
type PerClusterSnapshotRender struct {
	Name      string
	Namespace string

	// Spec fields — the operator supplied them as a JSON body; we
	// project them into a Velero BackupSpec map. Zero values are
	// omitted so the resulting CRD body stays minimal (Velero applies
	// reasonable defaults itself).
	IncludedNamespaces      []string
	ExcludedNamespaces      []string
	IncludedResources       []string
	ExcludedResources       []string
	LabelSelector           string
	SnapshotVolumes         *bool
	TTL                     string
	StorageLocation         string
	VolumeSnapshotLocations []string

	// Origin labels stamped on the CR for cross-referencing back to
	// the Astronomer DB row. Mirrors how the management-plane CRs are
	// tagged with app.kubernetes.io/managed-by.
	SnapshotID string
}

// renderPerClusterBackup returns the JSON body for a Velero Backup CR
// driven by the cluster_snapshots admin UX. Output is the unstructured
// map[string]any payload — the caller marshals it for the kube-apiserver
// (the K8sRequester wants []byte).
func renderPerClusterBackup(in PerClusterSnapshotRender) map[string]any {
	if in.Namespace == "" {
		in.Namespace = defaultVeleroNamespace
	}
	spec := map[string]any{}
	if len(in.IncludedNamespaces) > 0 {
		spec["includedNamespaces"] = in.IncludedNamespaces
	}
	if len(in.ExcludedNamespaces) > 0 {
		spec["excludedNamespaces"] = in.ExcludedNamespaces
	}
	if len(in.IncludedResources) > 0 {
		spec["includedResources"] = in.IncludedResources
	}
	if len(in.ExcludedResources) > 0 {
		spec["excludedResources"] = in.ExcludedResources
	}
	if strings.TrimSpace(in.LabelSelector) != "" {
		// Velero accepts both a stringified selector AND a structured
		// metav1.LabelSelector — we always emit the structured form
		// because the stringified path is deprecated in v1.13+. Parse
		// "k1=v1,k2=v2" into matchLabels{}. Unparseable tokens are
		// dropped on the floor (the handler's request validator
		// rejects them before we ever get here).
		labels := parseLabelSelector(in.LabelSelector)
		if len(labels) > 0 {
			spec["labelSelector"] = map[string]any{"matchLabels": labels}
		}
	}
	if in.SnapshotVolumes != nil {
		spec["snapshotVolumes"] = *in.SnapshotVolumes
	}
	if strings.TrimSpace(in.TTL) != "" {
		spec["ttl"] = in.TTL
	}
	if strings.TrimSpace(in.StorageLocation) != "" {
		spec["storageLocation"] = in.StorageLocation
	}
	if len(in.VolumeSnapshotLocations) > 0 {
		spec["volumeSnapshotLocations"] = in.VolumeSnapshotLocations
	}

	labels := map[string]string{
		"app.kubernetes.io/managed-by": "astronomer-go",
	}
	if in.SnapshotID != "" {
		labels["astronomer.io/snapshot-id"] = in.SnapshotID
	}

	return map[string]any{
		"apiVersion": veleroAPIVersion,
		"kind":       "Backup",
		"metadata": map[string]any{
			"name":      in.Name,
			"namespace": in.Namespace,
			"labels":    labels,
		},
		"spec": spec,
	}
}

// PerClusterRestoreRender is the input bundle for a Velero Restore CRD.
// Restore specs are simpler than Backup specs — most operators only
// override the includedNamespaces filter and the namespace remapping.
type PerClusterRestoreRender struct {
	Name             string
	Namespace        string
	BackupName       string
	IncludedNamespaces []string
	ExcludedNamespaces []string
	NamespaceMapping map[string]string
	LabelSelector    string
	RestorePVs       *bool
	// Origin labels stamped on the CR.
	RestoreID  string
	SnapshotID string
}

// renderPerClusterRestore returns the JSON body for a Velero Restore CR.
func renderPerClusterRestore(in PerClusterRestoreRender) map[string]any {
	if in.Namespace == "" {
		in.Namespace = defaultVeleroNamespace
	}
	spec := map[string]any{
		"backupName": in.BackupName,
	}
	if len(in.IncludedNamespaces) > 0 {
		spec["includedNamespaces"] = in.IncludedNamespaces
	}
	if len(in.ExcludedNamespaces) > 0 {
		spec["excludedNamespaces"] = in.ExcludedNamespaces
	}
	if len(in.NamespaceMapping) > 0 {
		spec["namespaceMapping"] = in.NamespaceMapping
	}
	if strings.TrimSpace(in.LabelSelector) != "" {
		labels := parseLabelSelector(in.LabelSelector)
		if len(labels) > 0 {
			spec["labelSelector"] = map[string]any{"matchLabels": labels}
		}
	}
	if in.RestorePVs != nil {
		spec["restorePVs"] = *in.RestorePVs
	}
	labels := map[string]string{
		"app.kubernetes.io/managed-by": "astronomer-go",
	}
	if in.RestoreID != "" {
		labels["astronomer.io/restore-id"] = in.RestoreID
	}
	if in.SnapshotID != "" {
		labels["astronomer.io/snapshot-id"] = in.SnapshotID
	}
	return map[string]any{
		"apiVersion": veleroAPIVersion,
		"kind":       "Restore",
		"metadata": map[string]any{
			"name":      in.Name,
			"namespace": in.Namespace,
			"labels":    labels,
		},
		"spec": spec,
	}
}

// renderDeleteBackupRequest builds the Velero DeleteBackupRequest CR
// body. Per Velero's protocol an HTTP DELETE on a /backups/{name} URL
// merely removes the Backup CR; the object-store-side data is only
// reclaimed when Velero observes a DeleteBackupRequest naming the same
// backup. We always go through the indirect path.
func renderDeleteBackupRequest(name, namespace, backupName string) map[string]any {
	if namespace == "" {
		namespace = defaultVeleroNamespace
	}
	return map[string]any{
		"apiVersion": veleroAPIVersion,
		"kind":       "DeleteBackupRequest",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels": map[string]string{
				"app.kubernetes.io/managed-by": "astronomer-go",
			},
		},
		"spec": map[string]any{
			"backupName": backupName,
		},
	}
}

// parseLabelSelector turns "k1=v1,k2=v2" into a matchLabels map. Empty
// or malformed tokens are dropped; this is intentionally permissive so
// a stray trailing comma doesn't 500 the create handler. The HTTP
// request validator enforces non-empty / well-formed shape up front.
func parseLabelSelector(s string) map[string]string {
	out := map[string]string{}
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		idx := strings.Index(tok, "=")
		if idx <= 0 || idx == len(tok)-1 {
			continue
		}
		k := strings.TrimSpace(tok[:idx])
		v := strings.TrimSpace(tok[idx+1:])
		if k == "" {
			continue
		}
		out[k] = v
	}
	return out
}

// createVeleroBackupCRD POSTs a Velero Backup CR to a member cluster's
// kube-apiserver through the tunnel. The handler treats a 409 (already
// exists) as success — the row already references this CR, so we don't
// need to fail the row on a retry.
func createVeleroBackupCRD(ctx context.Context, requester K8sRequester, clusterID string, body map[string]any) error {
	return postUnstructured(ctx, requester, clusterID, "backups", body)
}

// createVeleroRestoreCRD POSTs a Velero Restore CR to a member cluster.
// Same conflict-tolerant semantics as createVeleroBackupCRD.
func createVeleroRestoreCRD(ctx context.Context, requester K8sRequester, clusterID string, body map[string]any) error {
	return postUnstructured(ctx, requester, clusterID, "restores", body)
}

// createVeleroDeleteBackupRequest POSTs a DeleteBackupRequest CR. The
// handler creates one with a deterministic name (backup name suffix
// `-delete-<8 char random>`); a conflict on the suffix is unlikely but
// would be silently accepted as success.
func createVeleroDeleteBackupRequest(ctx context.Context, requester K8sRequester, clusterID string, body map[string]any) error {
	return postUnstructured(ctx, requester, clusterID, "deletebackuprequests", body)
}

// getVeleroBackupCRD reads a Velero Backup CR. Returns the parsed JSON
// (status block included) so the poller can inspect BackupStatus.Phase,
// progress counters, etc.
func getVeleroBackupCRD(ctx context.Context, requester K8sRequester, clusterID, namespace, name string) (map[string]any, error) {
	return getUnstructured(ctx, requester, clusterID, namespace, "backups", name)
}

// getVeleroRestoreCRD reads a Velero Restore CR; same shape as
// getVeleroBackupCRD but for the restore lifecycle.
func getVeleroRestoreCRD(ctx context.Context, requester K8sRequester, clusterID, namespace, name string) (map[string]any, error) {
	return getUnstructured(ctx, requester, clusterID, namespace, "restores", name)
}

// listVeleroBSLs reads the BackupStorageLocation CRD list under the
// velero namespace. Used by the velero-status endpoint to detect
// whether Velero is installed in a member cluster. Returns the
// `items` JSON array slice; nil + error when the CRD doesn't exist
// (404 / 503) — surfacing "not installed" to the caller is the entire
// point of the endpoint.
func listVeleroBSLs(ctx context.Context, requester K8sRequester, clusterID, namespace string) ([]map[string]any, error) {
	if requester == nil {
		return nil, fmt.Errorf("kubernetes requester not configured")
	}
	if namespace == "" {
		namespace = defaultVeleroNamespace
	}
	path := fmt.Sprintf("/apis/velero.io/v1/namespaces/%s/backupstoragelocations", namespace)
	resp, err := requester.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		// CRD not installed → Velero not present. Treat as a soft signal,
		// not an error: the velero-status endpoint surfaces this as
		// "installed: false".
		return nil, nil
	}
	if err := ensureSuccess(resp); err != nil {
		return nil, err
	}
	var parsed struct {
		Items []map[string]any `json:"items"`
	}
	if err := parseJSONResponse(resp, &parsed); err != nil {
		return nil, fmt.Errorf("decode bsl list: %w", err)
	}
	return parsed.Items, nil
}

// postUnstructured marshals and POSTs `body` to the Velero CRD endpoint
// for `kindPlural` in `body.metadata.namespace`. A 409 conflict is
// silently swallowed (re-POST after a transient failure shouldn't
// fail the operator's workflow); every other ≥400 status surfaces as
// the response error body.
func postUnstructured(ctx context.Context, requester K8sRequester, clusterID, kindPlural string, body map[string]any) error {
	if requester == nil {
		return fmt.Errorf("kubernetes requester not configured")
	}
	namespace := veleroNamespaceFromBody(body)
	createPath := fmt.Sprintf("/apis/velero.io/v1/namespaces/%s/%s", namespace, kindPlural)
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", kindPlural, err)
	}
	resp, err := requester.Do(ctx, clusterID, http.MethodPost, createPath, payload, requestHeaders("application/json"))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusConflict {
		// Already exists — fine, we don't try to PATCH the spec on a
		// re-post because Velero treats Backup specs as immutable once
		// the controller starts reconciling.
		return nil
	}
	return ensureSuccess(resp)
}

// getUnstructured does the GET counterpart of postUnstructured. Returns
// nil + os.ErrNotExist–style sentinel error so the caller can distinguish
// "CR was already deleted out from under us" from a generic 4xx/5xx.
func getUnstructured(ctx context.Context, requester K8sRequester, clusterID, namespace, kindPlural, name string) (map[string]any, error) {
	if requester == nil {
		return nil, fmt.Errorf("kubernetes requester not configured")
	}
	if namespace == "" {
		namespace = defaultVeleroNamespace
	}
	path := fmt.Sprintf("/apis/velero.io/v1/namespaces/%s/%s/%s", namespace, kindPlural, name)
	resp, err := requester.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrVeleroCRDMissing
	}
	if err := ensureSuccess(resp); err != nil {
		return nil, err
	}
	var out map[string]any
	if err := parseJSONResponse(resp, &out); err != nil {
		return nil, fmt.Errorf("decode %s: %w", kindPlural, err)
	}
	return out, nil
}

// ErrVeleroCRDMissing is the sentinel the poller checks for when a
// Velero CR has been removed from under us (operator's `kubectl delete`,
// Velero TTL sweep, etc.). The poller treats this as a terminal "Deleted"
// phase rather than retrying forever.
var ErrVeleroCRDMissing = fmt.Errorf("velero CRD not found")

// veleroNamespaceFromBody pulls .metadata.namespace out of an
// unstructured CRD body. Defensive — if the body is missing or
// mis-shapen we fall back to the project default. Callers are
// expected to always pre-set the namespace; this is the safety net.
func veleroNamespaceFromBody(body map[string]any) string {
	meta, _ := body["metadata"].(map[string]any)
	if meta == nil {
		return defaultVeleroNamespace
	}
	ns, _ := meta["namespace"].(string)
	if ns == "" {
		return defaultVeleroNamespace
	}
	return ns
}

// VeleroBackupStatus is the decoded subset of BackupStatus we care about.
// Only the fields the poller maps into DB columns are listed here —
// keeping the struct small forces a deliberate change when we want to
// surface a new Velero status field via the API.
type VeleroBackupStatus struct {
	Phase           string `json:"phase"`
	StartTimestamp  string `json:"startTimestamp"`
	CompletionTimestamp string `json:"completionTimestamp"`
	Warnings        int    `json:"warnings"`
	Errors          int    `json:"errors"`
	Progress        struct {
		TotalItems     int `json:"totalItems"`
		ItemsBackedUp  int `json:"itemsBackedUp"`
	} `json:"progress"`
	ValidationErrors []string `json:"validationErrors"`
}

// decodeBackupStatus pulls the `.status` subfield out of an unstructured
// Backup CR and projects it into VeleroBackupStatus. Missing fields
// default to zero values — Velero only populates BackupStatus.Phase
// once the controller picks the CR up, so an empty Phase string maps
// to "New" in the DB column.
func decodeBackupStatus(cr map[string]any) VeleroBackupStatus {
	status, _ := cr["status"].(map[string]any)
	if status == nil {
		return VeleroBackupStatus{}
	}
	raw, err := json.Marshal(status)
	if err != nil {
		return VeleroBackupStatus{}
	}
	var out VeleroBackupStatus
	_ = json.Unmarshal(raw, &out)
	return out
}

// VeleroRestoreStatus is the decoded subset of RestoreStatus. Mirrors
// VeleroBackupStatus but Velero uses a different Phase set on Restore
// (New / InProgress / Completed / PartiallyFailed / Failed / FailedValidation).
type VeleroRestoreStatus struct {
	Phase           string `json:"phase"`
	StartTimestamp  string `json:"startTimestamp"`
	CompletionTimestamp string `json:"completionTimestamp"`
	Warnings        int    `json:"warnings"`
	Errors          int    `json:"errors"`
	ValidationErrors []string `json:"validationErrors"`
}

func decodeRestoreStatus(cr map[string]any) VeleroRestoreStatus {
	status, _ := cr["status"].(map[string]any)
	if status == nil {
		return VeleroRestoreStatus{}
	}
	raw, err := json.Marshal(status)
	if err != nil {
		return VeleroRestoreStatus{}
	}
	var out VeleroRestoreStatus
	_ = json.Unmarshal(raw, &out)
	return out
}
