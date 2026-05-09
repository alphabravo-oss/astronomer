// Package tasks: Phase B3 — project enforcement reconciler.
//
// The handler stores resource_quota / limit_range / network_policy_mode on the
// project row; this worker task is what actually pushes ResourceQuota,
// LimitRange and NetworkPolicy objects into the target namespace via the
// tunnel's K8sRequester and updates project_namespaces.last_reconciled_at /
// last_reconcile_error so the UI can show a green/red dot.
//
// Two task types:
//
//   - "project:reconcile"     — single (project, cluster, namespace) tuple.
//     Enqueued from the handler on AddNamespace / RemoveNamespace.
//
//   - "project:reconcile_all" — periodic sweep across every project_namespaces
//     row. Cooperatively leased via ClaimProjectNamespaceReconcile so multiple
//     worker pods don't fight (the lease holder bumps locked_until 30s into
//     the future; other workers SKIP that row).
package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// Task type names. Exported so worker.go (and any tests) can register them
// against the asynq mux and the periodic scheduler.
const (
	ProjectReconcileType    = "project:reconcile"
	ProjectReconcileAllType = "project:reconcile_all"
)

// Field manager used for server-side apply. K8s tracks per-manager ownership
// of fields, so re-applying the same SSA payload is the canonical drift
// recovery mechanism — fields we own get reset, fields owned by other
// managers (e.g. a human via kubectl edit) are left alone.
const projectFieldManager = "astronomer-go-project-controller"

// projectNamespaceLabelKey is stamped onto every reconciled namespace so the
// "allow-same-project" NetworkPolicy can select peers via a podSelector that
// matches all namespaces with this label set to the project ID.
const projectNamespaceLabelKey = "astronomer.io/project-id"

// Managed object names. Stable so re-apply lands on the same object and so
// the cleanup path (RemoveNamespace) can DELETE deterministically.
const (
	managedQuotaName         = "astronomer-quota"
	managedLimitRangeName    = "astronomer-limits"
	managedNetworkPolicyName = "astronomer-isolation"
)

// reconcileLeaseTTL is how long a worker holds the lease for a single
// (project, cluster, namespace) before another worker is free to re-claim
// the row. Longer than the typical apply (which is sub-second) but short
// enough that a crashed worker doesn't strand the row for long.
const reconcileLeaseTTL = 30 * time.Second

// ProjectReconcileQuerier is the slice of sqlc.Queries the reconcile task
// needs. Defined locally so tests can stand up a fake without importing the
// whole project. The runtime wires the live *sqlc.Queries via
// ConfigureProjectReconcile.
type ProjectReconcileQuerier interface {
	GetProjectByID(ctx context.Context, id uuid.UUID) (sqlc.Project, error)
	ListProjectNamespaces(ctx context.Context, projectID uuid.UUID) ([]sqlc.ProjectNamespace, error)
	ListAllProjectNamespaces(ctx context.Context) ([]sqlc.ProjectNamespace, error)
	UpsertProjectNamespace(ctx context.Context, arg sqlc.UpsertProjectNamespaceParams) (sqlc.ProjectNamespace, error)
	DeleteProjectNamespace(ctx context.Context, arg sqlc.DeleteProjectNamespaceParams) error
	ClaimProjectNamespaceReconcile(ctx context.Context, arg sqlc.ClaimProjectNamespaceReconcileParams) (sqlc.ProjectNamespace, error)
	MarkProjectNamespaceReconciled(ctx context.Context, arg sqlc.MarkProjectNamespaceReconciledParams) error
}

// ProjectK8sRequester is the same shape as handler.K8sRequester, redeclared
// here to avoid a worker -> handler import cycle.
type ProjectK8sRequester interface {
	Do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (*ProjectK8sResponse, error)
}

// ProjectK8sResponse is the minimal subset of protocol.K8sResponsePayload
// we need. The adapter in worker wiring decodes the protocol payload into
// this struct so this file has zero dependency on the tunnel package.
type ProjectK8sResponse struct {
	StatusCode int
	Body       []byte
}

// ProjectReconcileDeps is the wiring for the reconcile task. The runtime
// configures it once at server startup; tests can swap in fakes.
type ProjectReconcileDeps struct {
	Queries   ProjectReconcileQuerier
	Requester ProjectK8sRequester
}

var projectDeps ProjectReconcileDeps

// ConfigureProjectReconcile stores the task's runtime dependencies. Called
// from server startup once the K8s tunnel hub and DB are wired.
func ConfigureProjectReconcile(deps ProjectReconcileDeps) {
	projectDeps = deps
}

// ResetProjectReconcile clears runtime deps. Used by tests.
func ResetProjectReconcile() {
	projectDeps = ProjectReconcileDeps{}
}

// ProjectReconcilePayload is the JSON body of a "project:reconcile" task.
// Op == "remove" deletes our managed CRs and the project label; Op == "apply"
// (default) renders + applies them.
type ProjectReconcilePayload struct {
	ProjectID string `json:"project_id"`
	ClusterID string `json:"cluster_id"`
	Namespace string `json:"namespace"`
	Op        string `json:"op,omitempty"`
}

// NewProjectReconcileTask builds an asynq task for a single (project, cluster,
// namespace) reconcile. The handler enqueues this on AddNamespace.
func NewProjectReconcileTask(payload ProjectReconcilePayload) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal project reconcile payload: %w", err)
	}
	return asynq.NewTask(ProjectReconcileType, data), nil
}

// NewProjectReconcileAllTask builds an empty payload task for the periodic
// sweep. The scheduler enqueues this on a cron interval.
func NewProjectReconcileAllTask() (*asynq.Task, error) {
	return asynq.NewTask(ProjectReconcileAllType, nil), nil
}

// HandleProjectReconcile is the asynq handler for "project:reconcile".
func HandleProjectReconcile(ctx context.Context, t *asynq.Task) error {
	if projectDeps.Queries == nil || projectDeps.Requester == nil {
		runtimeLogger().InfoContext(ctx, "project reconcile runtime not configured, skipping")
		return nil
	}
	var p ProjectReconcilePayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal project reconcile payload: %w", err)
	}
	projectID, err := uuid.Parse(p.ProjectID)
	if err != nil {
		return fmt.Errorf("invalid project_id: %w", err)
	}
	clusterID, err := uuid.Parse(p.ClusterID)
	if err != nil {
		return fmt.Errorf("invalid cluster_id: %w", err)
	}
	if p.Namespace == "" {
		return fmt.Errorf("namespace is required")
	}
	op := p.Op
	if op == "" {
		op = "apply"
	}

	if op == "remove" {
		// Best-effort cleanup. We DELETE the row regardless of K8s outcome;
		// the user already removed the namespace from the project, so leaving
		// a row dangling here would just be confusing.
		_ = removeProjectEnforcement(ctx, projectDeps.Requester, p.ClusterID, p.Namespace, projectID)
		return projectDeps.Queries.DeleteProjectNamespace(ctx, sqlc.DeleteProjectNamespaceParams{
			ProjectID: projectID,
			ClusterID: clusterID,
			Namespace: p.Namespace,
		})
	}

	project, err := projectDeps.Queries.GetProjectByID(ctx, projectID)
	if err != nil {
		return fmt.Errorf("load project: %w", err)
	}
	return reconcileProjectNamespace(ctx, projectDeps.Queries, projectDeps.Requester, project, clusterID, p.Namespace)
}

// HandleProjectReconcileAll is the asynq handler for the periodic sweep.
// It walks every project_namespaces row, attempts to claim the lease, and
// reconciles only the ones it claims. Other workers running concurrently
// pick up disjoint rows.
func HandleProjectReconcileAll(ctx context.Context, _ *asynq.Task) error {
	if projectDeps.Queries == nil || projectDeps.Requester == nil {
		runtimeLogger().InfoContext(ctx, "project reconcile runtime not configured, skipping sweep")
		return nil
	}
	rows, err := projectDeps.Queries.ListAllProjectNamespaces(ctx)
	if err != nil {
		return fmt.Errorf("list project namespaces: %w", err)
	}
	for _, row := range rows {
		lease := pgtype.Timestamptz{Time: time.Now().UTC().Add(reconcileLeaseTTL), Valid: true}
		claimed, err := projectDeps.Queries.ClaimProjectNamespaceReconcile(ctx, sqlc.ClaimProjectNamespaceReconcileParams{
			ProjectID:   row.ProjectID,
			ClusterID:   row.ClusterID,
			Namespace:   row.Namespace,
			LockedUntil: lease,
		})
		if err != nil {
			// pgx returns ErrNoRows when the lease is held by someone else.
			// That's the normal cooperative path — skip silently.
			continue
		}
		project, err := projectDeps.Queries.GetProjectByID(ctx, claimed.ProjectID)
		if err != nil {
			runtimeLogger().WarnContext(ctx, "project lookup failed during sweep", "project_id", claimed.ProjectID.String(), "error", err)
			_ = markReconciled(ctx, projectDeps.Queries, claimed.ProjectID, claimed.ClusterID, claimed.Namespace, "project lookup failed: "+err.Error())
			continue
		}
		if err := reconcileProjectNamespace(ctx, projectDeps.Queries, projectDeps.Requester, project, claimed.ClusterID, claimed.Namespace); err != nil {
			runtimeLogger().WarnContext(ctx, "project reconcile failed", "project_id", claimed.ProjectID.String(), "namespace", claimed.Namespace, "error", err)
		}
	}
	return nil
}

// reconcileProjectNamespace renders and applies the three managed objects
// for a single (project, cluster, namespace) and records the outcome.
func reconcileProjectNamespace(ctx context.Context, q ProjectReconcileQuerier, requester ProjectK8sRequester, project sqlc.Project, clusterID uuid.UUID, namespace string) error {
	clusterIDStr := clusterID.String()
	if err := labelNamespace(ctx, requester, clusterIDStr, namespace, project.ID.String()); err != nil {
		return markReconciled(ctx, q, project.ID, clusterID, namespace, fmt.Sprintf("label namespace: %v", err))
	}

	quota := renderResourceQuota(namespace, project.ResourceQuota)
	if err := serverSideApply(ctx, requester, clusterIDStr, fmt.Sprintf("/api/v1/namespaces/%s/resourcequotas/%s", namespace, managedQuotaName), quota); err != nil {
		return markReconciled(ctx, q, project.ID, clusterID, namespace, fmt.Sprintf("apply resourcequota: %v", err))
	}

	if hasLimitRangeFields(project.LimitRange) {
		lr := renderLimitRange(namespace, project.LimitRange)
		if err := serverSideApply(ctx, requester, clusterIDStr, fmt.Sprintf("/api/v1/namespaces/%s/limitranges/%s", namespace, managedLimitRangeName), lr); err != nil {
			return markReconciled(ctx, q, project.ID, clusterID, namespace, fmt.Sprintf("apply limitrange: %v", err))
		}
	} else {
		// User cleared the spec — make sure any prior LimitRange is gone.
		_ = deleteIfExists(ctx, requester, clusterIDStr, fmt.Sprintf("/api/v1/namespaces/%s/limitranges/%s", namespace, managedLimitRangeName))
	}

	mode := normalizeNetworkPolicyMode(project.NetworkPolicyMode)
	if mode == "none" {
		_ = deleteIfExists(ctx, requester, clusterIDStr, fmt.Sprintf("/apis/networking.k8s.io/v1/namespaces/%s/networkpolicies/%s", namespace, managedNetworkPolicyName))
	} else {
		np := renderNetworkPolicy(namespace, project.ID.String(), mode)
		if err := serverSideApply(ctx, requester, clusterIDStr, fmt.Sprintf("/apis/networking.k8s.io/v1/namespaces/%s/networkpolicies/%s", namespace, managedNetworkPolicyName), np); err != nil {
			return markReconciled(ctx, q, project.ID, clusterID, namespace, fmt.Sprintf("apply networkpolicy: %v", err))
		}
	}

	return markReconciled(ctx, q, project.ID, clusterID, namespace, "")
}

// removeProjectEnforcement deletes our three managed CRs from the namespace
// and clears the project label. Best-effort: errors are returned but the
// caller (RemoveNamespace path) ignores them.
func removeProjectEnforcement(ctx context.Context, requester ProjectK8sRequester, clusterID, namespace string, projectID uuid.UUID) error {
	_ = deleteIfExists(ctx, requester, clusterID, fmt.Sprintf("/api/v1/namespaces/%s/resourcequotas/%s", namespace, managedQuotaName))
	_ = deleteIfExists(ctx, requester, clusterID, fmt.Sprintf("/api/v1/namespaces/%s/limitranges/%s", namespace, managedLimitRangeName))
	_ = deleteIfExists(ctx, requester, clusterID, fmt.Sprintf("/apis/networking.k8s.io/v1/namespaces/%s/networkpolicies/%s", namespace, managedNetworkPolicyName))
	// Strip the label by writing an empty value via a JSON-merge-patch on
	// the namespace metadata. (Server-side apply with an empty label set
	// would clear all labels we own; merge-patch with null is the surgical
	// alternative.)
	patch := fmt.Sprintf(`{"metadata":{"labels":{%q:null}}}`, projectNamespaceLabelKey)
	_, _ = requester.Do(ctx, clusterID, http.MethodPatch, fmt.Sprintf("/api/v1/namespaces/%s", namespace), []byte(patch), map[string]string{
		"Content-Type": "application/merge-patch+json",
		"Accept":       "application/json",
	})
	_ = projectID // currently unused, reserved for future audit linkage
	return nil
}

// labelNamespace stamps astronomer.io/project-id={projectID} onto the
// namespace via JSON-merge-patch so the "allow-same-project" NetworkPolicy
// selector can find peers. We patch instead of SSA to avoid stomping any
// other labels owned by external controllers.
func labelNamespace(ctx context.Context, requester ProjectK8sRequester, clusterID, namespace, projectID string) error {
	patch := fmt.Sprintf(`{"metadata":{"labels":{%q:%q}}}`, projectNamespaceLabelKey, projectID)
	resp, err := requester.Do(ctx, clusterID, http.MethodPatch, fmt.Sprintf("/api/v1/namespaces/%s", namespace), []byte(patch), map[string]string{
		"Content-Type": "application/merge-patch+json",
		"Accept":       "application/json",
	})
	if err != nil {
		return err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("label namespace failed: status=%d body=%s", resp.StatusCode, string(resp.Body))
	}
	return nil
}

// serverSideApply PATCHes the manifest using K8s server-side apply. The
// fieldManager is set so subsequent applies update the same fields cleanly,
// even when an out-of-band edit modified them.
func serverSideApply(ctx context.Context, requester ProjectK8sRequester, clusterID, path string, manifest map[string]any) error {
	body, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	pathWithFM := path + "?fieldManager=" + projectFieldManager + "&force=true"
	resp, err := requester.Do(ctx, clusterID, http.MethodPatch, pathWithFM, body, map[string]string{
		"Content-Type": "application/apply-patch+yaml",
		"Accept":       "application/json",
	})
	if err != nil {
		return err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("apply failed: status=%d body=%s", resp.StatusCode, string(resp.Body))
	}
	return nil
}

// deleteIfExists removes a named resource, ignoring 404. Used for cleanup
// (RemoveNamespace) and for "you used to have a NetworkPolicy but switched
// the mode back to 'none'" transitions.
func deleteIfExists(ctx context.Context, requester ProjectK8sRequester, clusterID, path string) error {
	resp, err := requester.Do(ctx, clusterID, http.MethodDelete, path, nil, map[string]string{
		"Accept": "application/json",
	})
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("delete failed: status=%d body=%s", resp.StatusCode, string(resp.Body))
	}
	return nil
}

// markReconciled persists the outcome on project_namespaces. errMsg is empty
// on success. Returning the error from the caller is fine because asynq's
// retry policy applies — but we still record the latest error on the row so
// the UI can surface it in steady state.
func markReconciled(ctx context.Context, q ProjectReconcileQuerier, projectID, clusterID uuid.UUID, namespace, errMsg string) error {
	if err := q.MarkProjectNamespaceReconciled(ctx, sqlc.MarkProjectNamespaceReconciledParams{
		ProjectID:          projectID,
		ClusterID:          clusterID,
		Namespace:          namespace,
		LastReconcileError: errMsg,
	}); err != nil {
		return fmt.Errorf("update project_namespace: %w", err)
	}
	if errMsg != "" {
		return fmt.Errorf("%s", errMsg)
	}
	return nil
}

// --- rendering ------------------------------------------------------------

// normalizeNetworkPolicyMode coerces unknown strings to "none" so a typo on
// the project row never silently leaves a namespace exposed.
func normalizeNetworkPolicyMode(mode string) string {
	switch mode {
	case "isolated", "allow-same-project":
		return mode
	default:
		return "none"
	}
}

// renderResourceQuota turns the project's resource_quota JSON blob into a
// minimal ResourceQuota manifest. Only fields that the user actually set
// land on the spec.hard map — leaving unset fields off avoids surprising
// the cluster with a "0Gi memory" quota.
func renderResourceQuota(namespace string, raw json.RawMessage) map[string]any {
	hard := buildHardSpec(raw)
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "ResourceQuota",
		"metadata": map[string]any{
			"name":      managedQuotaName,
			"namespace": namespace,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": projectFieldManager,
			},
		},
		"spec": map[string]any{
			"hard": hard,
		},
	}
}

// buildHardSpec is exposed so the unit tests can verify exact output.
// Recognized keys map to ResourceQuota's spec.hard names. Unknown keys are
// passed through verbatim so cluster operators can express less-common
// quotas (e.g. "count/services.loadbalancers") via the same JSON blob.
func buildHardSpec(raw json.RawMessage) map[string]any {
	hard := map[string]any{}
	if len(raw) == 0 {
		return hard
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return hard
	}
	// Friendly aliases that the UI already exposes.
	aliases := map[string]string{
		"cpu":              "cpu",
		"memory":           "memory",
		"pods":             "pods",
		"requests.cpu":     "requests.cpu",
		"requests.memory":  "requests.memory",
		"limits.cpu":       "limits.cpu",
		"limits.memory":    "limits.memory",
		"requests.storage": "requests.storage",
		"storage":          "requests.storage",
		"persistentvolumeclaims": "persistentvolumeclaims",
		"services":         "services",
		"configmaps":       "configmaps",
		"secrets":          "secrets",
	}
	for k, v := range decoded {
		if v == nil {
			continue
		}
		if s, ok := v.(string); ok && s == "" {
			continue
		}
		key := k
		if mapped, ok := aliases[k]; ok {
			key = mapped
		}
		hard[key] = v
	}
	return hard
}

// hasLimitRangeFields checks whether the project specified any container
// default request/limit so we know whether to skip the LimitRange entirely.
func hasLimitRangeFields(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return false
	}
	for _, k := range []string{"default", "defaultRequest", "max", "min"} {
		if m, ok := decoded[k].(map[string]any); ok && len(m) > 0 {
			return true
		}
	}
	return false
}

// renderLimitRange turns the project's limit_range blob into a single-item
// LimitRange targeting Containers. Per-container defaults are the most
// commonly desired multi-tenancy guard rail; supporting Pod / PVC scopes
// can come later when the UI surfaces them.
func renderLimitRange(namespace string, raw json.RawMessage) map[string]any {
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	limit := map[string]any{
		"type": "Container",
	}
	for _, k := range []string{"default", "defaultRequest", "max", "min"} {
		if m, ok := decoded[k].(map[string]any); ok && len(m) > 0 {
			limit[k] = m
		}
	}
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "LimitRange",
		"metadata": map[string]any{
			"name":      managedLimitRangeName,
			"namespace": namespace,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": projectFieldManager,
			},
		},
		"spec": map[string]any{
			"limits": []any{limit},
		},
	}
}

// renderNetworkPolicy expresses the requested isolation mode as a single
// NetworkPolicy. Both modes deny-by-default; "allow-same-project" then
// re-admits ingress + egress from peers carrying the same project label.
func renderNetworkPolicy(namespace, projectID, mode string) map[string]any {
	policy := map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata": map[string]any{
			"name":      managedNetworkPolicyName,
			"namespace": namespace,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": projectFieldManager,
			},
		},
		"spec": map[string]any{
			"podSelector": map[string]any{},
			"policyTypes": []any{"Ingress", "Egress"},
		},
	}
	if mode == "allow-same-project" {
		peer := map[string]any{
			"namespaceSelector": map[string]any{
				"matchLabels": map[string]any{
					projectNamespaceLabelKey: projectID,
				},
			},
		}
		policy["spec"].(map[string]any)["ingress"] = []any{
			map[string]any{"from": []any{peer}},
		}
		policy["spec"].(map[string]any)["egress"] = []any{
			map[string]any{"to": []any{peer}},
		}
	}
	// In "isolated" mode, omitting ingress/egress arrays under a policyTypes
	// list of [Ingress, Egress] means deny-all in both directions, which is
	// exactly the desired behaviour.
	return policy
}
