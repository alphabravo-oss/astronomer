// Package tasks — migration 053 cloud-credential materialization worker.
//
// Two task types:
//
//   - "cloud_credentials:materialize"  — single materialization run for
//     one (credential, cluster, namespace) tuple. Enqueued by the
//     handler on POST / PUT / DELETE and by /test/ flows that need an
//     immediate apply.
//   - "cloud_credentials:drift_reconcile" — periodic 30m sweep across
//     every row whose status != 'applied'. Idempotent: SSA on the
//     Secret is a no-op when already in desired state.
//
// The cleartext credential blob only lives inside this worker's
// process memory and (at apply time) inside the in-cluster Secret that
// k8s itself encrypts at rest. The DB stores only the Fernet ciphertext.
package tasks

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/cloudcreds"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// Task type identifiers. Exported for the worker mux + scheduler.
const (
	CloudCredentialMaterializeType     = "cloud_credentials:materialize"
	CloudCredentialDriftReconcileType  = "cloud_credentials:drift_reconcile"
)

// cloudCredentialFieldManager is the K8s server-side-apply identifier the
// worker stamps every materialized Secret with. Disjoint from
// projectFieldManager and clusterRegistryFieldManager so re-apply on one
// path never stomps the others.
const cloudCredentialFieldManager = "astronomer-go-cloud-credentials"

// CloudCredentialMaterializePayload is the JSON body of a single
// materialize task. Op is one of:
//   - ""        / "apply": render + SSA the Secret.
//   - "delete":            remove the Secret (used by the DELETE
//                          handler before the credential row is
//                          purged from the DB).
type CloudCredentialMaterializePayload struct {
	CredentialID string `json:"credential_id"`
	ClusterID    string `json:"cluster_id"`
	Namespace    string `json:"namespace"`
	SecretName   string `json:"secret_name"`
	Op           string `json:"op,omitempty"`
}

// NewCloudCredentialMaterializeTask builds the asynq envelope.
func NewCloudCredentialMaterializeTask(p CloudCredentialMaterializePayload) (*asynq.Task, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshal cloud credential materialize payload: %w", err)
	}
	return asynq.NewTask(CloudCredentialMaterializeType, data), nil
}

// NewCloudCredentialDriftReconcileTask is the empty-payload constructor
// used by the periodic scheduler.
func NewCloudCredentialDriftReconcileTask() (*asynq.Task, error) {
	return asynq.NewTask(CloudCredentialDriftReconcileType, nil), nil
}

// CloudCredentialQuerier is the DB surface the materialize task uses.
// Tests pass a hand-rolled fake; production wires *sqlc.Queries.
type CloudCredentialQuerier interface {
	GetCloudCredentialByID(ctx context.Context, id uuid.UUID) (sqlc.CloudCredential, error)
	ListCloudCredentialMaterializations(ctx context.Context, credentialID uuid.UUID) ([]sqlc.CloudCredentialMaterialization, error)
	ListAllPendingCloudCredentialMaterializations(ctx context.Context) ([]sqlc.CloudCredentialMaterialization, error)
	MarkCloudCredentialMaterializationApplied(ctx context.Context, id uuid.UUID) error
	MarkCloudCredentialMaterializationFailed(ctx context.Context, arg sqlc.MarkCloudCredentialMaterializationFailedParams) error
}

// CloudCredentialDecryptor unwraps the encrypted-at-rest blob. The
// production implementation is *auth.Encryptor; tests pass a stub that
// returns the input verbatim.
type CloudCredentialDecryptor interface {
	Decrypt(token string) (string, error)
}

// CloudCredentialMaterializeDeps is the wiring for this task.
type CloudCredentialMaterializeDeps struct {
	Queries   CloudCredentialQuerier
	Requester ProjectK8sRequester // reuse the same shape used by project_reconcile + cluster_registry_apply
	Decryptor CloudCredentialDecryptor
}

var cloudCredentialDeps CloudCredentialMaterializeDeps

// ConfigureCloudCredentialMaterialize stores the runtime deps. Called
// from server startup; tests may swap in fakes via direct assignment.
func ConfigureCloudCredentialMaterialize(deps CloudCredentialMaterializeDeps) {
	cloudCredentialDeps = deps
}

// ResetCloudCredentialMaterialize clears the runtime deps (test only).
func ResetCloudCredentialMaterialize() {
	cloudCredentialDeps = CloudCredentialMaterializeDeps{}
}

// cloudCredentialMaterializationsTotal counts apply / delete outcomes
// per provider. The provider label is set from the credential row;
// outcome is success / failure.
var cloudCredentialMaterializationsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "astronomer",
		Name:      "cloud_credentials_materializations_total",
		Help:      "Cloud credentials materialization outcomes by phase + provider.",
	},
	observability.MetricLabels("phase", "provider", "outcome"),
)

func init() {
	prometheus.MustRegister(cloudCredentialMaterializationsTotal)
}

// HandleCloudCredentialMaterialize is the asynq mux handler for a single
// materialization request.
func HandleCloudCredentialMaterialize(ctx context.Context, t *asynq.Task) error {
	if cloudCredentialDeps.Queries == nil || cloudCredentialDeps.Requester == nil {
		runtimeLogger().InfoContext(ctx, "cloud credential materialize runtime not configured, skipping")
		return nil
	}
	var p CloudCredentialMaterializePayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal cloud credential materialize payload: %w", err)
	}
	credentialID, err := uuid.Parse(p.CredentialID)
	if err != nil {
		return fmt.Errorf("invalid credential_id: %w", err)
	}
	clusterID, err := uuid.Parse(p.ClusterID)
	if err != nil {
		return fmt.Errorf("invalid cluster_id: %w", err)
	}
	if p.Namespace == "" {
		return fmt.Errorf("namespace is required")
	}
	if p.SecretName == "" {
		return fmt.Errorf("secret_name is required")
	}
	op := p.Op
	if op == "" {
		op = "apply"
	}
	switch op {
	case "delete":
		return runCloudCredentialDelete(ctx, clusterID, p.Namespace, p.SecretName, "")
	default:
		return runCloudCredentialApply(ctx, credentialID, clusterID, p.Namespace, p.SecretName)
	}
}

// HandleCloudCredentialDriftReconcile is the periodic-sweep handler. It
// walks every materialization row not in the applied state and retries
// — apply is idempotent (SSA), so converged rows are a no-op fast-path.
func HandleCloudCredentialDriftReconcile(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, CloudCredentialDriftReconcileType, func() error {
		if cloudCredentialDeps.Queries == nil || cloudCredentialDeps.Requester == nil {
			runtimeLogger().InfoContext(ctx, "cloud credential materialize runtime not configured, skipping drift sweep")
			return nil
		}
		rows, err := cloudCredentialDeps.Queries.ListAllPendingCloudCredentialMaterializations(ctx)
		if err != nil {
			return fmt.Errorf("list pending materializations: %w", err)
		}
		for _, row := range rows {
			if err := runCloudCredentialApply(ctx, row.CredentialID, row.ClusterID, row.Namespace, row.SecretName); err != nil {
				runtimeLogger().WarnContext(ctx, "cloud credential drift reconcile error",
					"credential_id", row.CredentialID.String(),
					"cluster_id", row.ClusterID.String(),
					"namespace", row.Namespace,
					"error", err)
			}
		}
		return nil
	})
}

// runCloudCredentialApply is the apply-path body, shared by the per-task
// handler and the periodic sweep.
func runCloudCredentialApply(ctx context.Context, credentialID, clusterID uuid.UUID, namespace, secretName string) error {
	cred, err := cloudCredentialDeps.Queries.GetCloudCredentialByID(ctx, credentialID)
	if err != nil {
		cloudCredentialMaterializationsTotal.WithLabelValues(observability.MetricValues("apply", "", "failure")...).Inc()
		return fmt.Errorf("load credential: %w", err)
	}
	matRow := findMaterializationRow(ctx, credentialID, clusterID, namespace)
	provider := cred.Provider
	clearBlob, err := decryptAndDecode(cred.DataEncrypted)
	if err != nil {
		cloudCredentialMaterializationsTotal.WithLabelValues(observability.MetricValues("apply", provider, "failure")...).Inc()
		_ = markMaterializationFailed(ctx, matRow, fmt.Sprintf("decrypt: %v", err))
		return err
	}
	data := cloudcreds.RenderSecretData(provider, clearBlob)
	if err := applyCloudCredentialSecret(ctx, clusterID.String(), namespace, secretName, provider, cred.ID, data); err != nil {
		cloudCredentialMaterializationsTotal.WithLabelValues(observability.MetricValues("apply", provider, "failure")...).Inc()
		_ = markMaterializationFailed(ctx, matRow, err.Error())
		return err
	}
	cloudCredentialMaterializationsTotal.WithLabelValues(observability.MetricValues("apply", provider, "success")...).Inc()
	if matRow != nil {
		_ = cloudCredentialDeps.Queries.MarkCloudCredentialMaterializationApplied(ctx, matRow.ID)
	}
	return nil
}

// runCloudCredentialDelete removes the materialized Secret. The DB row
// for the materialization may already be gone by the time this runs
// (the handler DELETEs the row inside its synchronous response path),
// so we don't try to mark it applied/failed; the metric label captures
// the outcome instead.
func runCloudCredentialDelete(ctx context.Context, clusterID uuid.UUID, namespace, secretName, provider string) error {
	if err := deleteCloudCredentialSecret(ctx, clusterID.String(), namespace, secretName); err != nil {
		cloudCredentialMaterializationsTotal.WithLabelValues(observability.MetricValues("delete", provider, "failure")...).Inc()
		return err
	}
	cloudCredentialMaterializationsTotal.WithLabelValues(observability.MetricValues("delete", provider, "success")...).Inc()
	return nil
}

// decryptAndDecode unwraps the Fernet token and parses the JSON blob
// into a map[string]string. Empty token means "no blob" (legacy rows
// migrated from an unencrypted state); we return an empty map so the
// apply produces an empty Secret rather than 500ing.
func decryptAndDecode(token string) (map[string]string, error) {
	if strings.TrimSpace(token) == "" {
		return map[string]string{}, nil
	}
	if cloudCredentialDeps.Decryptor == nil {
		return nil, fmt.Errorf("decryptor not configured")
	}
	clear, err := cloudCredentialDeps.Decryptor.Decrypt(token)
	if err != nil {
		return nil, fmt.Errorf("fernet decrypt: %w", err)
	}
	return cloudcreds.DecodeBlob([]byte(clear))
}

// findMaterializationRow returns the row matching the (credential,
// cluster, namespace) tuple if it exists in the DB, otherwise nil. The
// drift sweep path always has a row (we iterate over them); the
// handler-enqueue path may race with a fresh row not yet visible.
func findMaterializationRow(ctx context.Context, credentialID, clusterID uuid.UUID, namespace string) *sqlc.CloudCredentialMaterialization {
	rows, err := cloudCredentialDeps.Queries.ListCloudCredentialMaterializations(ctx, credentialID)
	if err != nil {
		return nil
	}
	for _, row := range rows {
		if row.ClusterID == clusterID && row.Namespace == namespace {
			r := row
			return &r
		}
	}
	return nil
}

// markMaterializationFailed stamps the row with the most-recent error
// so the UI can show "this credential failed to materialize in cluster
// X: <reason>". Tolerates a nil row (race with row creation) by
// returning nil — the metric counter already captured the failure.
func markMaterializationFailed(ctx context.Context, row *sqlc.CloudCredentialMaterialization, errMsg string) error {
	if row == nil {
		return nil
	}
	return cloudCredentialDeps.Queries.MarkCloudCredentialMaterializationFailed(ctx, sqlc.MarkCloudCredentialMaterializationFailedParams{
		ID:        row.ID,
		LastError: errMsg,
	})
}

// applyCloudCredentialSecret builds the Secret manifest and SSAs it
// into the namespace. Type is "Opaque" — k8s base64-encodes the data
// map automatically when we pass it as `stringData`, but we pre-base64
// + use `data` to keep the SSA payload deterministic.
func applyCloudCredentialSecret(ctx context.Context, clusterID, namespace, secretName, provider string, credentialID uuid.UUID, data map[string]string) error {
	encoded := make(map[string]string, len(data))
	for k, v := range data {
		encoded[k] = base64.StdEncoding.EncodeToString([]byte(v))
	}
	secret := map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      secretName,
			"namespace": namespace,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by":     cloudCredentialFieldManager,
				"astronomer.io/managed-by":         "astronomer",
				"astronomer.io/cloud-credential":   credentialID.String(),
				"astronomer.io/credential-provider": provider,
			},
		},
		"type": "Opaque",
		"data": encoded,
	}
	body, err := json.Marshal(secret)
	if err != nil {
		return fmt.Errorf("marshal secret manifest: %w", err)
	}
	path := fmt.Sprintf("/api/v1/namespaces/%s/secrets/%s?fieldManager=%s&force=true", namespace, secretName, cloudCredentialFieldManager)
	resp, err := cloudCredentialDeps.Requester.Do(ctx, clusterID, http.MethodPatch, path, body, map[string]string{
		"Content-Type": "application/apply-patch+yaml",
		"Accept":       "application/json",
	})
	if err != nil {
		return fmt.Errorf("apply secret: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("apply secret status=%d body=%s", resp.StatusCode, string(resp.Body))
	}
	return nil
}

// deleteCloudCredentialSecret removes the Secret from the namespace.
// 404 is success — the desired post-state is "the Secret is absent".
func deleteCloudCredentialSecret(ctx context.Context, clusterID, namespace, secretName string) error {
	resp, err := cloudCredentialDeps.Requester.Do(ctx, clusterID, http.MethodDelete, fmt.Sprintf("/api/v1/namespaces/%s/secrets/%s", namespace, secretName), nil, map[string]string{
		"Accept": "application/json",
	})
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("delete secret status=%d body=%s", resp.StatusCode, string(resp.Body))
	}
	return nil
}

// cloudCredentialMaterializationLeaseTTL bounds how long one worker
// holds the sweep so a crashed worker doesn't strand the queue until
// the next 30m tick. Mirrors driftReconcileLeaseTTL in the cluster
// registry path.
const cloudCredentialMaterializationLeaseTTL = 5 * time.Minute

var _ = cloudCredentialMaterializationLeaseTTL // reserved for future per-row lease use
