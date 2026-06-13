// Package tasks — migration 050 cluster-registry apply worker.
//
// Two task types:
//
//   - "cluster:apply_registry_secret"      — single (registry, cluster)
//     run. Enqueued by the registry handler on POST / PUT / DELETE. On
//     the apply path the worker:
//       1. Loads the registry_config row (or, on Op="unapply", uses the
//          inline snapshot stamped by the handler).
//       2. Builds the dockerconfigjson payload.
//       3. For each target namespace (the explicit list on the row, OR
//          every project_namespaces row for this cluster when the list is
//          empty) — applies the Secret and (optionally) patches the
//          namespace's default ServiceAccount to add the Secret to
//          imagePullSecrets.
//       4. Stamps last_applied_at / last_apply_error on the row.
//
//   - "cluster:registry_drift_reconcile"   — periodic sweep across every
//     row. Cheap: read-then-skip when already in desired state, idempotent
//     SSA when not. Handles missed apply enqueues (worker restart while a
//     task was in flight, fresh namespace created post-apply, etc).
//
// The Secret + SA-patch reuse the same machinery the project reconciler
// uses for its project-scoped registry secret (project_reconcile.go). The
// helpers there take a fixed secret_name; we want per-row names so the
// new path constructs its own helpers below — they share the JSON-merge-
// patch / strategic-merge-patch transport layer with the project path
// indirectly via ProjectK8sRequester.

package tasks

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// Task type identifiers. Exported so the worker mux + scheduler can
// register them, and so the handler can construct asynq.Task{Type}.
const (
	ClusterApplyRegistrySecretType    = "cluster:apply_registry_secret"
	ClusterRegistryDriftReconcileType = "cluster:registry_drift_reconcile"
)

// clusterRegistryFieldManager is the K8s server-side-apply identifier the
// worker stamps every Secret with. Distinct from
// projectFieldManager (project_reconcile.go) so the project reconciler's
// per-namespace astronomer-registry Secret and the per-row Secrets here
// own disjoint field sets — re-applying one never stomps the other.
const clusterRegistryFieldManager = "astronomer-go-cluster-registry"

// clusterRegistrySecretNamePrefix is prepended to the row id when the
// operator didn't pick an explicit secret_name. The full name is
// "astronomer-registry-<row-id>", deterministic across re-applies so SSA
// + cleanup find the same Secret.
const clusterRegistrySecretNamePrefix = "astronomer-registry-"

// driftReconcileLeaseTTL bounds how long one worker holds the sweep so a
// crashed worker doesn't strand the whole sweep until the next 30m tick.
const driftReconcileLeaseTTL = 5 * time.Minute

// ClusterApplyRegistrySecretPayload is the JSON body of an apply task.
// Op is "apply" (default — write Secret + patch SA), "unapply" (remove
// Secret + de-patch SA). The Snapshot* fields are only populated on
// unapply paths where the DB row has already been deleted by the time
// the worker runs.
type ClusterApplyRegistrySecretPayload struct {
	RegistryID        string   `json:"registry_id"`
	ClusterID         string   `json:"cluster_id"`
	Op                string   `json:"op,omitempty"`
	SnapshotSecret    string   `json:"snapshot_secret,omitempty"`
	SnapshotNamespace []string `json:"snapshot_namespace,omitempty"`
	SnapshotInjectSA  bool     `json:"snapshot_inject_sa,omitempty"`
}

// NewClusterApplyRegistrySecretTask builds the asynq.Task envelope.
func NewClusterApplyRegistrySecretTask(p ClusterApplyRegistrySecretPayload) (*asynq.Task, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshal cluster registry apply payload: %w", err)
	}
	return asynq.NewTask(ClusterApplyRegistrySecretType, data), nil
}

// NewClusterRegistryDriftReconcileTask is the empty-payload constructor
// used by the periodic scheduler.
func NewClusterRegistryDriftReconcileTask() (*asynq.Task, error) {
	return asynq.NewTask(ClusterRegistryDriftReconcileType, nil), nil
}

// ClusterRegistryApplyQuerier is the DB surface the apply task uses. The
// runtime wires *sqlc.Queries; tests pass a hand-rolled fake.
type ClusterRegistryApplyQuerier interface {
	GetClusterRegistryConfigByID(ctx context.Context, id uuid.UUID) (sqlc.ClusterRegistryConfig, error)
	ListClusterRegistryConfigs(ctx context.Context, clusterID uuid.UUID) ([]sqlc.ClusterRegistryConfig, error)
	ListAllClusterRegistryConfigs(ctx context.Context) ([]sqlc.ClusterRegistryConfig, error)
	ListProjectNamespaces(ctx context.Context, projectID uuid.UUID) ([]sqlc.ProjectNamespace, error)
	ListAllProjectNamespaces(ctx context.Context) ([]sqlc.ProjectNamespace, error)
	MarkClusterRegistryApplied(ctx context.Context, id uuid.UUID) error
	MarkClusterRegistryApplyError(ctx context.Context, arg sqlc.MarkClusterRegistryApplyErrorParams) error
}

// ClusterRegistryApplyDeps is the wiring for this task. The runtime
// configures it once at server startup; tests swap in fakes.
type ClusterRegistryApplyDeps struct {
	Queries   ClusterRegistryApplyQuerier
	Requester ProjectK8sRequester
	Encryptor *auth.Encryptor
}

var clusterRegistryApplyDeps ClusterRegistryApplyDeps

// ConfigureClusterRegistryApply stores the task's runtime dependencies.
// Called from server startup once the K8s tunnel hub and DB are wired.
func ConfigureClusterRegistryApply(deps ClusterRegistryApplyDeps) {
	clusterRegistryApplyDeps = deps
}

// ResetClusterRegistryApply clears runtime deps. Used by tests.
func ResetClusterRegistryApply() {
	clusterRegistryApplyDeps = ClusterRegistryApplyDeps{}
}

// clusterRegistryAppliesTotal counts every apply / unapply outcome.
// outcome ∈ {success, failure}; phase ∈ {apply, unapply}.
var clusterRegistryAppliesTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "astronomer",
		Name:      "cluster_registry_applies_total",
		Help:      "Cluster registry apply / unapply outcomes by phase.",
	},
	observability.MetricLabels("phase", "outcome"),
)

func init() {
	prometheus.MustRegister(clusterRegistryAppliesTotal)
}

// RegistryProbeURL turns the stored private_registry_url into the URL the
// /test/ endpoint should hit. Docker registries expose /v2/ as the
// canonical "I am alive" probe. We strip any path the operator stored
// (e.g. "registry.example.com/library") so we don't accidentally probe
// a missing path and conclude the registry is broken.
func RegistryProbeURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return ""
	}
	parsed.Path = "/v2/"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

// HandleClusterApplyRegistrySecret is the asynq mux handler.
func HandleClusterApplyRegistrySecret(ctx context.Context, t *asynq.Task) error {
	if clusterRegistryApplyDeps.Queries == nil || clusterRegistryApplyDeps.Requester == nil {
		runtimeLogger().InfoContext(ctx, "cluster registry apply runtime not configured, skipping")
		return nil
	}
	var p ClusterApplyRegistrySecretPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal cluster registry apply payload: %w", err)
	}
	registryID, err := uuid.Parse(p.RegistryID)
	if err != nil {
		return fmt.Errorf("invalid registry_id: %w", err)
	}
	clusterID, err := uuid.Parse(p.ClusterID)
	if err != nil {
		return fmt.Errorf("invalid cluster_id: %w", err)
	}
	op := p.Op
	if op == "" {
		op = "apply"
	}

	switch op {
	case "unapply":
		return runUnapply(ctx, registryID, clusterID, p.SnapshotSecret, p.SnapshotNamespace, p.SnapshotInjectSA)
	default:
		return runApply(ctx, registryID, clusterID)
	}
}

// HandleClusterRegistryDriftReconcile is the periodic-sweep handler. It
// walks every cluster_registry_configs row and re-applies — the apply
// helpers below are idempotent (SSA on the Secret, JSON-merge on the SA)
// so re-runs are cheap when the state already matches.
func HandleClusterRegistryDriftReconcile(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, ClusterRegistryDriftReconcileType, func() error {
		if clusterRegistryApplyDeps.Queries == nil || clusterRegistryApplyDeps.Requester == nil {
			runtimeLogger().InfoContext(ctx, "cluster registry apply runtime not configured, skipping drift sweep")
			return nil
		}
		rows, err := clusterRegistryApplyDeps.Queries.ListAllClusterRegistryConfigs(ctx)
		if err != nil {
			return fmt.Errorf("list cluster registry configs: %w", err)
		}
		// Idempotent re-apply. Errors per-row are logged but don't abort
		// the sweep — one broken row shouldn't stop the others from
		// converging.
		for _, row := range rows {
			if err := runApply(ctx, row.ID, row.ClusterID); err != nil {
				runtimeLogger().WarnContext(ctx, "cluster registry drift reconcile error", "registry_id", row.ID.String(), "cluster_id", row.ClusterID.String(), "error", err)
			}
		}
		return nil
	})
}

// runApply is the apply-path body, shared by the single-task handler and
// the periodic sweep.
func runApply(ctx context.Context, registryID, clusterID uuid.UUID) error {
	cfg, err := clusterRegistryApplyDeps.Queries.GetClusterRegistryConfigByID(ctx, registryID)
	if err != nil {
		clusterRegistryAppliesTotal.WithLabelValues(observability.MetricValues("apply", "failure")...).Inc()
		return fmt.Errorf("load registry config: %w", err)
	}
	if cfg.ClusterID != clusterID {
		// The caller passed mismatched ids; treat as a hard error so
		// asynq retries-then-dlqs rather than apply the wrong cluster.
		clusterRegistryAppliesTotal.WithLabelValues(observability.MetricValues("apply", "failure")...).Inc()
		return fmt.Errorf("registry %s does not belong to cluster %s", registryID, clusterID)
	}
	if err := materializeClusterRegistryPassword(&cfg); err != nil {
		clusterRegistryAppliesTotal.WithLabelValues(observability.MetricValues("apply", "failure")...).Inc()
		_ = markRegistryError(ctx, cfg.ID, fmt.Sprintf("decrypt registry password: %v", err))
		return err
	}
	secretName := strings.TrimSpace(cfg.SecretName)
	if secretName == "" {
		secretName = clusterRegistrySecretNamePrefix + cfg.ID.String()
	}
	namespaces, err := resolveTargetNamespaces(ctx, cfg)
	if err != nil {
		clusterRegistryAppliesTotal.WithLabelValues(observability.MetricValues("apply", "failure")...).Inc()
		_ = markRegistryError(ctx, cfg.ID, fmt.Sprintf("resolve namespaces: %v", err))
		return err
	}
	if len(namespaces) == 0 {
		// No project namespaces yet → nothing to apply, but record a
		// clean state so the UI doesn't surface a stale error.
		_ = clusterRegistryApplyDeps.Queries.MarkClusterRegistryApplied(ctx, cfg.ID)
		clusterRegistryAppliesTotal.WithLabelValues(observability.MetricValues("apply", "success")...).Inc()
		return nil
	}

	var firstErr error
	for _, ns := range namespaces {
		if err := applyRegistrySecretToNamespace(ctx, clusterID.String(), ns, secretName, cfg); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if cfg.InjectDefaultSa {
			if err := ensureDefaultSAImagePullSecret(ctx, clusterID.String(), ns, secretName); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
		}
	}

	if firstErr != nil {
		clusterRegistryAppliesTotal.WithLabelValues(observability.MetricValues("apply", "failure")...).Inc()
		_ = markRegistryError(ctx, cfg.ID, firstErr.Error())
		return firstErr
	}
	clusterRegistryAppliesTotal.WithLabelValues(observability.MetricValues("apply", "success")...).Inc()
	if err := clusterRegistryApplyDeps.Queries.MarkClusterRegistryApplied(ctx, cfg.ID); err != nil {
		return fmt.Errorf("mark registry applied: %w", err)
	}
	return nil
}

// runUnapply removes the Secret + de-patches the SA. Best-effort: a
// 404 on the Secret or a missing SA isn't a failure — the steady state
// we want is "the Secret isn't there". Errors here translate to a
// retry up to asynq's max-retry; after that the row is already gone
// (we DELETE in the handler before enqueueing) so the worst case is a
// dangling Secret an operator can clean up manually.
func runUnapply(ctx context.Context, registryID, clusterID uuid.UUID, snapshotSecret string, snapshotNamespaces []string, _ bool) error {
	if snapshotSecret == "" {
		snapshotSecret = clusterRegistrySecretNamePrefix + registryID.String()
	}
	namespaces := snapshotNamespaces
	if len(namespaces) == 0 {
		// No namespace snapshot — best-effort fan out across every
		// project_namespaces row for the cluster so we don't strand the
		// Secret in unknown namespaces.
		all, err := clusterRegistryApplyDeps.Queries.ListAllProjectNamespaces(ctx)
		if err == nil {
			for _, row := range all {
				if row.ClusterID == clusterID {
					namespaces = append(namespaces, row.Namespace)
				}
			}
		}
	}
	var firstErr error
	for _, ns := range namespaces {
		if err := removeDefaultSAImagePullSecret(ctx, clusterID.String(), ns, snapshotSecret); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := deleteRegistrySecret(ctx, clusterID.String(), ns, snapshotSecret); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		clusterRegistryAppliesTotal.WithLabelValues(observability.MetricValues("unapply", "failure")...).Inc()
		return firstErr
	}
	clusterRegistryAppliesTotal.WithLabelValues(observability.MetricValues("unapply", "success")...).Inc()
	return nil
}

// resolveTargetNamespaces returns the list of namespaces the registry
// config should be materialised into. Explicit list wins; otherwise we
// fan out across every project_namespaces row under this cluster.
func resolveTargetNamespaces(ctx context.Context, cfg sqlc.ClusterRegistryConfig) ([]string, error) {
	if len(cfg.Namespaces) > 0 {
		var explicit []string
		if err := json.Unmarshal(cfg.Namespaces, &explicit); err != nil {
			return nil, fmt.Errorf("decode namespaces column: %w", err)
		}
		out := make([]string, 0, len(explicit))
		seen := map[string]struct{}{}
		for _, n := range explicit {
			n = strings.TrimSpace(n)
			if n == "" {
				continue
			}
			if _, ok := seen[n]; ok {
				continue
			}
			seen[n] = struct{}{}
			out = append(out, n)
		}
		if len(out) > 0 {
			return out, nil
		}
	}
	rows, err := clusterRegistryApplyDeps.Queries.ListAllProjectNamespaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("list project namespaces: %w", err)
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.ClusterID != cfg.ClusterID {
			continue
		}
		if _, ok := seen[row.Namespace]; ok {
			continue
		}
		seen[row.Namespace] = struct{}{}
		out = append(out, row.Namespace)
	}
	return out, nil
}

// applyRegistrySecretToNamespace builds the dockerconfigjson body and
// SSAs the Secret into the namespace under the configured manager.
func applyRegistrySecretToNamespace(ctx context.Context, clusterID, namespace, secretName string, cfg sqlc.ClusterRegistryConfig) error {
	dockerCfg := buildDockerConfigJSON(cfg)
	rawDockerCfg, err := json.Marshal(dockerCfg)
	if err != nil {
		return fmt.Errorf("marshal dockerconfigjson: %w", err)
	}
	secret := map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      secretName,
			"namespace": namespace,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by":  clusterRegistryFieldManager,
				"astronomer.io/registry-config": cfg.ID.String(),
			},
		},
		"type": "kubernetes.io/dockerconfigjson",
		"data": map[string]any{
			".dockerconfigjson": base64.StdEncoding.EncodeToString(rawDockerCfg),
		},
	}
	body, err := json.Marshal(secret)
	if err != nil {
		return fmt.Errorf("marshal secret manifest: %w", err)
	}
	path := fmt.Sprintf("/api/v1/namespaces/%s/secrets/%s?fieldManager=%s&force=true", namespace, secretName, clusterRegistryFieldManager)
	resp, err := clusterRegistryApplyDeps.Requester.Do(ctx, clusterID, http.MethodPatch, path, body, map[string]string{
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

// buildDockerConfigJSON renders the .dockerconfigjson "auths" map.
func buildDockerConfigJSON(cfg sqlc.ClusterRegistryConfig) map[string]any {
	authKey := canonicalRegistryHost(cfg.PrivateRegistryUrl)
	return map[string]any{
		"auths": map[string]any{
			authKey: map[string]any{
				"username": cfg.RegistryUsername,
				"password": cfg.RegistryPassword,
				"auth":     base64.StdEncoding.EncodeToString([]byte(cfg.RegistryUsername + ":" + cfg.RegistryPassword)),
			},
		},
	}
}

func materializeClusterRegistryPassword(cfg *sqlc.ClusterRegistryConfig) error {
	if cfg == nil || strings.TrimSpace(cfg.RegistryPasswordEncrypted) == "" {
		return nil
	}
	if clusterRegistryApplyDeps.Encryptor == nil {
		return fmt.Errorf("encrypted registry password present but encryptor is not configured")
	}
	password, err := clusterRegistryApplyDeps.Encryptor.Decrypt(cfg.RegistryPasswordEncrypted)
	if err != nil {
		return err
	}
	cfg.RegistryPassword = password
	return nil
}

// canonicalRegistryHost strips any scheme + trailing slash so the
// dockerconfigjson "auths" key matches what `docker login` would
// produce. Matches normalizeRegistryAuthKey in project_reconcile.go but
// lives here to keep the apply task independent.
func canonicalRegistryHost(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return trimmed
	}
	if parsed, err := url.Parse(trimmed); err == nil && parsed.Host != "" {
		return strings.TrimSuffix(parsed.Host+parsed.Path, "/")
	}
	return strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(trimmed, "https://"), "http://"), "/")
}

// ensureDefaultSAImagePullSecret reads the namespace's `default` SA, adds
// the secret to its imagePullSecrets array, and PATCHes via strategic-
// merge so we don't clobber pull secrets owned by other controllers.
// Idempotent: returns nil when the secret is already in the list.
func ensureDefaultSAImagePullSecret(ctx context.Context, clusterID, namespace, secretName string) error {
	current, err := readSAImagePullSecrets(ctx, clusterID, namespace)
	if err != nil {
		return err
	}
	for _, name := range current {
		if name == secretName {
			return nil
		}
	}
	merged := append(current, secretName)
	return writeSAImagePullSecrets(ctx, clusterID, namespace, merged)
}

// removeDefaultSAImagePullSecret is the de-patch path: drops secretName
// from the SA's imagePullSecrets, leaving everything else intact.
func removeDefaultSAImagePullSecret(ctx context.Context, clusterID, namespace, secretName string) error {
	current, err := readSAImagePullSecrets(ctx, clusterID, namespace)
	if err != nil {
		return err
	}
	filtered := make([]string, 0, len(current))
	found := false
	for _, name := range current {
		if name == secretName {
			found = true
			continue
		}
		filtered = append(filtered, name)
	}
	if !found {
		return nil
	}
	return writeSAImagePullSecrets(ctx, clusterID, namespace, filtered)
}

// readSAImagePullSecrets fetches the namespace's default ServiceAccount
// and returns its current imagePullSecrets[].name list. 404 → empty list
// (no SA in the namespace yet, e.g. brand-new namespace; the apply task
// will be re-tried by the periodic sweep when k8s gets around to
// creating the default SA).
func readSAImagePullSecrets(ctx context.Context, clusterID, namespace string) ([]string, error) {
	resp, err := clusterRegistryApplyDeps.Requester.Do(ctx, clusterID, http.MethodGet, fmt.Sprintf("/api/v1/namespaces/%s/serviceaccounts/default", namespace), nil, map[string]string{
		"Accept": "application/json",
	})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("get default SA failed: status=%d body=%s", resp.StatusCode, string(resp.Body))
	}
	var doc struct {
		ImagePullSecrets []struct {
			Name string `json:"name"`
		} `json:"imagePullSecrets"`
	}
	if len(resp.Body) > 0 {
		if err := json.Unmarshal(resp.Body, &doc); err != nil {
			return nil, fmt.Errorf("decode SA body: %w", err)
		}
	}
	out := make([]string, 0, len(doc.ImagePullSecrets))
	for _, item := range doc.ImagePullSecrets {
		name := strings.TrimSpace(item.Name)
		if name != "" {
			out = append(out, name)
		}
	}
	return out, nil
}

// writeSAImagePullSecrets PATCHes the SA's imagePullSecrets array. We use
// strategic-merge so the patch only touches the imagePullSecrets field —
// other fields on the SA (secrets[], annotations) stay untouched.
func writeSAImagePullSecrets(ctx context.Context, clusterID, namespace string, names []string) error {
	items := make([]map[string]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		items = append(items, map[string]string{"name": name})
	}
	patch := map[string]any{
		"imagePullSecrets": items,
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	resp, err := clusterRegistryApplyDeps.Requester.Do(ctx, clusterID, http.MethodPatch, fmt.Sprintf("/api/v1/namespaces/%s/serviceaccounts/default", namespace), raw, map[string]string{
		"Content-Type": "application/strategic-merge-patch+json",
		"Accept":       "application/json",
	})
	if err != nil {
		return err
	}
	if resp.StatusCode >= http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("patch default SA failed: status=%d body=%s", resp.StatusCode, string(resp.Body))
	}
	return nil
}

// deleteRegistrySecret removes the Secret from the namespace. 404 is
// success — the steady state was already "the Secret isn't there".
func deleteRegistrySecret(ctx context.Context, clusterID, namespace, secretName string) error {
	resp, err := clusterRegistryApplyDeps.Requester.Do(ctx, clusterID, http.MethodDelete, fmt.Sprintf("/api/v1/namespaces/%s/secrets/%s", namespace, secretName), nil, map[string]string{
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

// markRegistryError stamps the row with the most recent failure so the
// UI can show "last apply failed: …". Empty `errMsg` resets the column;
// the handler picks the success path through MarkClusterRegistryApplied
// instead.
func markRegistryError(ctx context.Context, id uuid.UUID, errMsg string) error {
	return clusterRegistryApplyDeps.Queries.MarkClusterRegistryApplyError(ctx, sqlc.MarkClusterRegistryApplyErrorParams{
		ID:             id,
		LastApplyError: errMsg,
	})
}
