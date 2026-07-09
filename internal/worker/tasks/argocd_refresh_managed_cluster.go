// Package tasks: ArgoCD managed-cluster label refresh.
//
// When an operator changes the `labels` JSONB on an Astronomer cluster row, the
// ApplicationSet `clusters` generators that target those labels would see
// stale data until the cluster is re-registered. This task re-stamps the
// astronomer.io/label-* keys on every upstream ArgoCD cluster Secret that
// maps to the cluster, without rotating the registered credentials.
//
// Single task type:
//
//   - "argocd:refresh_managed_cluster_labels" — refresh labels for a single
//     cluster_id. Enqueued from ClustersHandler.Update on every successful
//     mutation; idempotent so the periodic Argo reconciler can re-issue it
//     without harm.
package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/argolabels"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// Task type name. Exported so worker.go can register it against the asynq mux.
const ArgoCDRefreshManagedClusterLabelsType = "argocd:refresh_managed_cluster_labels"

// ArgoCDRefreshAllManagedClusterLabelsType is the periodic fleet sweep
// (DIR-10) that re-stamps labels for every managed-cluster mapping.
const ArgoCDRefreshAllManagedClusterLabelsType = "argocd:refresh_all_managed_cluster_labels"

// argoCDNamespace is the namespace where the upstream ArgoCD instance stores
// its cluster Secrets. Kept in lockstep with internal/handler.argocdNamespace —
// duplicated here so this package doesn't import internal/handler (which would
// create a cycle).
const argoCDNamespace = "astronomer"

// argoCDClusterSecretTypeLabel marks a Secret as an ArgoCD cluster registration.
// Mirrors the constants in internal/handler/argocd.go for the same reason as
// argoCDNamespace above.
const argoCDClusterSecretTypeLabel = argolabels.ArgoCDClusterSecretTypeLabel
const argoCDClusterSecretTypeValue = argolabels.ArgoCDClusterSecretTypeValue

// astronomerLabelPrefix is the prefix Astronomer stamps onto Argo cluster
// Secrets to mirror the cluster row's user-managed labels. Stripped + rewritten
// on every refresh.
const astronomerLabelPrefix = argolabels.LabelPrefix
const astronomerManagedByLabelKey = argolabels.ManagedByLabelKey
const astronomerManagedByLabelValue = argolabels.ManagedByLabelValue
const astronomerClusterIDLabelKey = argolabels.ClusterIDLabelKey
const astronomerClusterNameLabelKey = argolabels.ClusterNameLabelKey
const astronomerEnvironmentLabelKey = argolabels.EnvironmentLabelKey
const astronomerIsLocalLabelKey = argolabels.IsLocalLabelKey
const astronomerRegionLabelKey = argolabels.RegionLabelKey
const astronomerProviderLabelKey = argolabels.ProviderLabelKey
const astronomerDistributionLabelKey = argolabels.DistributionLabelKey
const astronomerAgentProfileLabelKey = argolabels.AgentProfileLabelKey
const astronomerAgentVersionLabelKey = argolabels.AgentVersionLabelKey
const astronomerKubernetesVersionLabelKey = argolabels.KubernetesVersionLabelKey
const astronomerProjectLabelKey = argolabels.ProjectLabelKey
const astronomerProjectIDLabelKey = argolabels.ProjectIDLabelKey
const astronomerProjectMembershipLabelPrefix = argolabels.ProjectMembershipPrefix
const astronomerProjectIDMembershipLabelPrefix = argolabels.ProjectIDMembershipPrefix

// ArgoCDRefreshQuerier is the slice of sqlc.Queries this task needs.
// Declared locally so tests can stand up a fake without importing the whole
// project. The runtime wires *sqlc.Queries via ConfigureArgoCDRefresh.
type ArgoCDRefreshQuerier interface {
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	ListArgoCDManagedClustersByCluster(ctx context.Context, clusterID uuid.UUID) ([]sqlc.ArgocdManagedCluster, error)
	UpdateArgoCDManagedClusterLabels(ctx context.Context, arg sqlc.UpdateArgoCDManagedClusterLabelsParams) (sqlc.ArgocdManagedCluster, error)
	// ListClusters powers the DIR-10 fleet-wide re-stamp sweep.
	ListClusters(ctx context.Context, arg sqlc.ListClustersParams) ([]sqlc.Cluster, error)
}

// ArgoCDRefreshDeps wires the task's runtime dependencies. The control-plane
// k8s client is required because the cluster Secrets live in the argocd
// namespace of the *control-plane* cluster (where every Astronomer-managed
// ArgoCD instance is installed), not in the managed cluster being re-stamped.
type ArgoCDRefreshDeps struct {
	Queries ArgoCDRefreshQuerier
	K8s     kubernetes.Interface
}

var argoCDRefreshDeps ArgoCDRefreshDeps

// ConfigureArgoCDRefresh stores the task's runtime dependencies. Called from
// server startup once the DB and control-plane k8s client are wired.
func ConfigureArgoCDRefresh(deps ArgoCDRefreshDeps) {
	argoCDRefreshDeps = deps
}

// ResetArgoCDRefresh clears runtime deps. Used by tests.
func ResetArgoCDRefresh() {
	argoCDRefreshDeps = ArgoCDRefreshDeps{}
}

// ArgoCDRefreshManagedClusterPayload is the JSON body of a refresh task.
// The cluster_id is the Astronomer clusters.id whose labels just changed.
type ArgoCDRefreshManagedClusterPayload struct {
	ClusterID string `json:"cluster_id"`
}

// NewArgoCDRefreshManagedClusterLabelsTask builds an asynq task to re-stamp
// the upstream Secret labels for every ArgoCD instance the cluster is
// registered into.
func NewArgoCDRefreshManagedClusterLabelsTask(clusterID uuid.UUID) (*asynq.Task, error) {
	data, err := json.Marshal(ArgoCDRefreshManagedClusterPayload{ClusterID: clusterID.String()})
	if err != nil {
		return nil, fmt.Errorf("marshal argocd refresh payload: %w", err)
	}
	return asynq.NewTask(ArgoCDRefreshManagedClusterLabelsType, data, asynq.MaxRetry(3)), nil
}

// HandleArgoCDRefreshAllManagedClusterLabels walks every non-decommissioned
// cluster and re-stamps Argo labels (DIR-10 periodic sweep).
func HandleArgoCDRefreshAllManagedClusterLabels(ctx context.Context, t *asynq.Task) error {
	if argoCDRefreshDeps.Queries == nil {
		runtimeLogger().InfoContext(ctx, "argocd refresh-all runtime not configured, skipping")
		return nil
	}
	const pageSize int32 = 500
	var offset int32
	var firstErr error
	for {
		page, err := argoCDRefreshDeps.Queries.ListClusters(ctx, sqlc.ListClustersParams{
			Limit:  pageSize,
			Offset: offset,
		})
		if err != nil {
			return fmt.Errorf("list clusters for label re-stamp: %w", err)
		}
		if len(page) == 0 {
			break
		}
		for _, c := range page {
			if c.DecommissionedAt.Valid {
				continue
			}
			payload, err := json.Marshal(ArgoCDRefreshManagedClusterPayload{ClusterID: c.ID.String()})
			if err != nil {
				continue
			}
			if err := HandleArgoCDRefreshManagedClusterLabels(ctx, asynq.NewTask(ArgoCDRefreshManagedClusterLabelsType, payload)); err != nil {
				runtimeLogger().WarnContext(ctx, "argocd refresh-all: cluster re-stamp failed",
					"cluster_id", c.ID.String(), "error", err)
				if firstErr == nil {
					firstErr = err
				}
			}
		}
		if int32(len(page)) < pageSize {
			break
		}
		offset += pageSize
	}
	return firstErr
}

// HandleArgoCDRefreshManagedClusterLabels is the asynq handler.
func HandleArgoCDRefreshManagedClusterLabels(ctx context.Context, t *asynq.Task) error {
	if argoCDRefreshDeps.Queries == nil {
		runtimeLogger().InfoContext(ctx, "argocd refresh runtime not configured, skipping")
		return nil
	}
	var p ArgoCDRefreshManagedClusterPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal argocd refresh payload: %w", err)
	}
	clusterID, err := uuid.Parse(p.ClusterID)
	if err != nil {
		return fmt.Errorf("invalid cluster_id: %w", err)
	}
	cluster, err := argoCDRefreshDeps.Queries.GetClusterByID(ctx, clusterID)
	if err != nil {
		// Cluster has been hard-deleted (rare — decommission tombstones rather
		// than deletes). Nothing left to refresh; treat as success so asynq
		// doesn't retry.
		runtimeLogger().InfoContext(ctx, "argocd refresh: cluster lookup failed; treating as no-op",
			"cluster_id", p.ClusterID, "error", err)
		return nil
	}
	rows, err := argoCDRefreshDeps.Queries.ListArgoCDManagedClustersByCluster(ctx, clusterID)
	if err != nil {
		return fmt.Errorf("list managed clusters: %w", err)
	}
	if len(rows) == 0 {
		// Cluster isn't registered into any ArgoCD; nothing to refresh.
		return nil
	}
	projects, err := argolabels.ProjectsForCluster(ctx, argoCDRefreshDeps.Queries, cluster.ID)
	if err != nil {
		return fmt.Errorf("list cluster projects: %w", err)
	}
	desired := managedClusterArgoLabelsForProjects(cluster, projects)
	desiredJSON, err := json.Marshal(desired)
	if err != nil {
		return fmt.Errorf("marshal desired labels: %w", err)
	}

	var firstErr error
	for _, row := range rows {
		if err := refreshSingleManagedClusterSecret(ctx, argoCDRefreshDeps.K8s, row, desired); err != nil {
			runtimeLogger().WarnContext(ctx, "argocd refresh: failed to update Secret labels",
				"cluster_id", clusterID.String(),
				"argocd_instance_id", row.ArgocdInstanceID.String(),
				"cluster_secret_name", row.ClusterSecretName,
				"error", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		// Mirror the new label set onto the index row so List endpoints
		// return the truth without a round-trip to the Secret.
		if _, err := argoCDRefreshDeps.Queries.UpdateArgoCDManagedClusterLabels(ctx, sqlc.UpdateArgoCDManagedClusterLabelsParams{
			ArgocdInstanceID: row.ArgocdInstanceID,
			ClusterID:        clusterID,
			Labels:           desiredJSON,
		}); err != nil {
			runtimeLogger().WarnContext(ctx, "argocd refresh: failed to update index row labels",
				"cluster_id", clusterID.String(),
				"argocd_instance_id", row.ArgocdInstanceID.String(),
				"error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// refreshSingleManagedClusterSecret patches the upstream ArgoCD cluster Secret
// for one registration. The Secret is identified by ClusterSecretName (the
// canonical handle) with a fallback to a server-URL scan for older rows that
// pre-date the secret-name plumbing. Only the astronomer.io/* labels are
// touched — everything else (including the Argo `argocd.argoproj.io/secret-type`
// marker) is preserved.
func refreshSingleManagedClusterSecret(ctx context.Context, k8s kubernetes.Interface, row sqlc.ArgocdManagedCluster, desired map[string]string) error {
	if k8s == nil {
		return fmt.Errorf("kubernetes client not configured")
	}
	secret, err := lookupClusterSecret(ctx, k8s, row.ClusterSecretName, row.ServerUrl)
	if err != nil {
		return err
	}
	if secret == nil {
		// The Secret was deleted out-of-band (e.g. operator ran kubectl delete).
		// Nothing to do — the DB index row is stale. Decommission/Unregister
		// is the correct path to drop the row; this task doesn't speculate.
		return nil
	}
	// Build the new label set:
	//   - Start from the Secret's existing labels (preserves Argo's own keys
	//     plus any human-added bookkeeping labels).
	//   - Strip every astronomer.io/* key we own.
	//   - Re-stamp our owned keys from `desired`.
	updated := make(map[string]string, len(secret.Labels)+len(desired))
	for k, v := range secret.Labels {
		if isAstronomerOwnedLabel(k) {
			continue
		}
		updated[k] = v
	}
	for k, v := range desired {
		updated[k] = v
	}
	if labelMapEqual(secret.Labels, updated) {
		// No change — skip the write so we don't generate noise audit/etag churn.
		return nil
	}

	// Build a JSON merge patch that explicitly nulls every astronomer.io/* key
	// we owned but are no longer setting — RFC 7396 merge-patch preserves
	// missing keys, so omitting them would NOT remove them. Updated keys go
	// in verbatim. Other Secret fields (server, config, type) are untouched.
	patchLabels := map[string]any{}
	for k := range secret.Labels {
		if isAstronomerOwnedLabel(k) {
			if _, stillSet := updated[k]; !stillSet {
				patchLabels[k] = nil
			}
		}
	}
	for k, v := range updated {
		if existing, ok := secret.Labels[k]; !ok || existing != v {
			patchLabels[k] = v
		}
	}
	if len(patchLabels) == 0 {
		return nil
	}
	patch, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"labels": patchLabels,
		},
	})
	if err != nil {
		return fmt.Errorf("build patch: %w", err)
	}
	_, err = k8s.CoreV1().Secrets(argoCDNamespace).Patch(ctx, secret.Name, types.MergePatchType, patch, metav1.PatchOptions{
		FieldManager: "astronomer-go-argocd-refresh",
	})
	if err != nil {
		return fmt.Errorf("patch secret %s: %w", secret.Name, err)
	}
	return nil
}

// isAstronomerOwnedLabel returns true if the label key is one this task owns
// and must overwrite on every refresh. Anything else is preserved as-is.
func isAstronomerOwnedLabel(k string) bool {
	return argolabels.IsOwnedLabel(k)
}

// labelMapEqual reports whether two label maps are equal as multisets of (k, v)
// pairs. Cheaper than reflect.DeepEqual at the hot-path call site.
func labelMapEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// lookupClusterSecret finds the Argo cluster Secret by name first (fast path)
// and falls back to a server-URL scan if the name is empty (older
// argocd_managed_clusters rows pre-date the secret-name plumbing).
func lookupClusterSecret(ctx context.Context, k8s kubernetes.Interface, name, server string) (*corev1.Secret, error) {
	if name != "" {
		secret, err := k8s.CoreV1().Secrets(argoCDNamespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			return secret, nil
		}
		if !errors.IsNotFound(err) {
			return nil, err
		}
	}
	if server == "" {
		return nil, nil
	}
	secrets, err := k8s.CoreV1().Secrets(argoCDNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: argoCDClusterSecretTypeLabel + "=" + argoCDClusterSecretTypeValue,
	})
	if err != nil {
		return nil, err
	}
	for i := range secrets.Items {
		if strings.TrimSpace(string(secrets.Items[i].Data["server"])) == server {
			return &secrets.Items[i], nil
		}
	}
	return nil, nil
}

func managedClusterArgoLabelsForProjects(cluster sqlc.Cluster, projects []sqlc.Project) map[string]string {
	return argolabels.ManagedClusterLabels(cluster, projects)
}

// SanitizeLabelKey converts an arbitrary user-supplied label key into a form
// the Kubernetes label-key rules accept: lowercase alphanumerics, '.', '-',
// max 63 chars, must start/end with an alphanumeric. Everything else is
// replaced with '-' and runs collapsed. This is a one-way mapping — the
// reverse is not computed (operators reading the Argo Secret label see the
// sanitized form, not the original).
//
// Exported so the handler package can call the same function (the two are
// otherwise structurally identical; we keep them in lockstep manually). See
// the TestSanitizeLabelKey suite for the contract.
func SanitizeLabelKey(in string) string {
	return argolabels.SanitizeLabelKey(in)
}
