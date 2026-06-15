package tasks

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// argocdRefreshQuerierStub is the minimum surface
// HandleArgoCDRefreshManagedClusterLabels needs. Returns canned values
// directly off the struct so each test case can set up an explicit state.
type argocdRefreshQuerierStub struct {
	cluster  sqlc.Cluster
	projects []sqlc.Project
	managed  []sqlc.ArgocdManagedCluster
	updates  []sqlc.UpdateArgoCDManagedClusterLabelsParams
	getError error
}

func (q *argocdRefreshQuerierStub) GetClusterByID(_ context.Context, _ uuid.UUID) (sqlc.Cluster, error) {
	if q.getError != nil {
		return sqlc.Cluster{}, q.getError
	}
	return q.cluster, nil
}

func (q *argocdRefreshQuerierStub) ListArgoCDManagedClustersByCluster(_ context.Context, _ uuid.UUID) ([]sqlc.ArgocdManagedCluster, error) {
	return q.managed, nil
}

func (q *argocdRefreshQuerierStub) ListProjectsByCluster(context.Context, sqlc.ListProjectsByClusterParams) ([]sqlc.Project, error) {
	return q.projects, nil
}

func (q *argocdRefreshQuerierStub) UpdateArgoCDManagedClusterLabels(_ context.Context, arg sqlc.UpdateArgoCDManagedClusterLabelsParams) (sqlc.ArgocdManagedCluster, error) {
	q.updates = append(q.updates, arg)
	return sqlc.ArgocdManagedCluster{}, nil
}

// makeRefreshTask wraps the payload marshaling + asynq.Task boilerplate so
// each test case is a single declarative call.
func makeRefreshTask(t *testing.T, clusterID uuid.UUID) *asynq.Task {
	t.Helper()
	data, err := json.Marshal(ArgoCDRefreshManagedClusterPayload{ClusterID: clusterID.String()})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return asynq.NewTask(ArgoCDRefreshManagedClusterLabelsType, data)
}

// TestSanitizeLabelKey is the worker-side mirror of the handler test. We keep
// both because the two implementations are physically separate (handler is
// internal; worker is exported as SanitizeLabelKey for cross-package reuse)
// and a drift between them would silently corrupt label mirroring.
func TestSanitizeLabelKey(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"simple lower", "team", "team"},
		{"uppercase folded", "Team", "team"},
		{"space becomes dash", "Team Name", "team-name"},
		{"multiple spaces collapse", "Team   Name", "team-name"},
		{"slash becomes dash", "team/name", "team-name"},
		{"underscores become dash", "team_name", "team-name"},
		{"leading non-alnum stripped", "_team", "team"},
		{"trailing dash stripped", "team-", "team"},
		{"dots preserved", "team.name", "team.name"},
		{"truncation at 63", strings.Repeat("a", 80), strings.Repeat("a", 63)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeLabelKey(tc.in)
			if got != tc.want {
				t.Fatalf("SanitizeLabelKey(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestRefreshManagedClusterLabels_UpdatesSecret asserts that running the task
// rewrites the astronomer.io/label-* keys on every Argo cluster Secret tied to
// the cluster.
func TestRefreshManagedClusterLabels_UpdatesSecret(t *testing.T) {
	t.Cleanup(ResetArgoCDRefresh)
	instanceID := uuid.New()
	clusterID := uuid.New()
	secretName := "cluster-prod-east"

	q := &argocdRefreshQuerierStub{
		cluster: sqlc.Cluster{
			ID:                clusterID,
			Name:              "prod-east",
			Environment:       "production",
			Region:            "us-east-1",
			Provider:          "aws",
			Distribution:      "eks",
			AgentVersion:      "v0.4.1",
			KubernetesVersion: "v1.29.3+k3s1",
			Annotations:       json.RawMessage(`{"astronomer.io/agent-privilege-profile":"operator"}`),
			Labels:            json.RawMessage(`{"tier":"prod","region":"us-east"}`),
		},
		projects: []sqlc.Project{{ID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Name: "platform"}},
		managed: []sqlc.ArgocdManagedCluster{{
			ArgocdInstanceID:  instanceID,
			ClusterID:         clusterID,
			ClusterSecretName: secretName,
			ServerUrl:         "https://k8s.example.test:6443",
		}},
	}

	// Pre-existing Secret: has Argo's marker + a stale astronomer.io/label-tier
	// that should be overwritten and a stale astronomer.io/label-zone that
	// should be stripped (because the new cluster.Labels doesn't include it).
	k8s := k8sfake.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: argoCDNamespace,
			Labels: map[string]string{
				argoCDClusterSecretTypeLabel:            argoCDClusterSecretTypeValue,
				"astronomer.io/managed-by":              "astronomer",
				"astronomer.io/cluster-id":              clusterID.String(),
				"astronomer.io/cluster-name":            "prod-east",
				"astronomer.io/environment":             "staging", // stale, should flip to "production"
				"astronomer.io/region":                  "us-west-2",
				"astronomer.io/provider":                "gcp",
				"astronomer.io/distribution":            "gke",
				"astronomer.io/agent-privilege-profile": "admin",
				"astronomer.io/agent-version":           "v0.1.0",
				"astronomer.io/kubernetes-version":      "v1.28.0",
				"astronomer.io/project":                 "old-project",
				"astronomer.io/project-id":              "22222222-2222-2222-2222-222222222222",
				"astronomer.io/project.old-project":     "true",
				"astronomer.io/label-tier":              "dev",     // stale, should flip to "prod"
				"astronomer.io/label-zone":              "us-west", // stale, should be REMOVED
				"unrelated.example.com/keep-me":         "yes",     // preserved
			},
		},
		Data: map[string][]byte{"server": []byte("https://k8s.example.test:6443")},
	})

	ConfigureArgoCDRefresh(ArgoCDRefreshDeps{Queries: q, K8s: k8s})

	if err := HandleArgoCDRefreshManagedClusterLabels(context.Background(),
		makeRefreshTask(t, clusterID)); err != nil {
		t.Fatalf("handle: %v", err)
	}

	got, err := k8s.CoreV1().Secrets(argoCDNamespace).Get(context.Background(), secretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	want := map[string]string{
		"argocd.argoproj.io/secret-type":                                "cluster",
		"astronomer.io/managed-by":                                      "astronomer",
		"astronomer.io/cluster-id":                                      clusterID.String(),
		"astronomer.io/cluster-name":                                    "prod-east",
		"astronomer.io/environment":                                     "production",
		"astronomer.io/is-local":                                        "false",
		"astronomer.io/region":                                          "us-east-1",
		"astronomer.io/provider":                                        "aws",
		"astronomer.io/distribution":                                    "eks",
		"astronomer.io/agent-privilege-profile":                         "operator",
		"astronomer.io/agent-version":                                   "v0.4.1",
		"astronomer.io/kubernetes-version":                              "v1.29.3-k3s1",
		"astronomer.io/project":                                         "platform",
		"astronomer.io/project-id":                                      "11111111-1111-1111-1111-111111111111",
		"astronomer.io/project.platform":                                "true",
		"astronomer.io/project-id.11111111-1111-1111-1111-111111111111": "true",
		"astronomer.io/label-tier":                                      "prod",
		"astronomer.io/label-region":                                    "us-east",
		"unrelated.example.com/keep-me":                                 "yes",
	}
	if len(got.Labels) != len(want) {
		t.Fatalf("label count = %d (%v), want %d (%v)", len(got.Labels), got.Labels, len(want), want)
	}
	for k, v := range want {
		if got.Labels[k] != v {
			t.Errorf("label %q = %q, want %q", k, got.Labels[k], v)
		}
	}
	// Stale zone must be gone.
	if _, ok := got.Labels["astronomer.io/label-zone"]; ok {
		t.Errorf("stale label astronomer.io/label-zone was not stripped")
	}
	if _, ok := got.Labels["astronomer.io/project.old-project"]; ok {
		t.Errorf("stale project membership label was not stripped")
	}
	// The DB index row should have been re-stamped too.
	if len(q.updates) != 1 {
		t.Fatalf("UpdateArgoCDManagedClusterLabels calls = %d, want 1", len(q.updates))
	}
	var rowLabels map[string]string
	if err := json.Unmarshal(q.updates[0].Labels, &rowLabels); err != nil {
		t.Fatalf("unmarshal updated row labels: %v", err)
	}
	if rowLabels["astronomer.io/project-id"] != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("row project-id label missing: %+v", rowLabels)
	}
}

// TestRefreshManagedClusterLabels_StripsRemovedKeys verifies the subset-shrink
// case: a label that's gone from cluster.Labels disappears from the Secret on
// the next refresh.
func TestRefreshManagedClusterLabels_StripsRemovedKeys(t *testing.T) {
	t.Cleanup(ResetArgoCDRefresh)
	instanceID := uuid.New()
	clusterID := uuid.New()
	secretName := "cluster-east"

	q := &argocdRefreshQuerierStub{
		cluster: sqlc.Cluster{
			ID:     clusterID,
			Name:   "east",
			Labels: json.RawMessage(`{"tier":"prod"}`), // "team" is GONE
		},
		managed: []sqlc.ArgocdManagedCluster{{
			ArgocdInstanceID:  instanceID,
			ClusterID:         clusterID,
			ClusterSecretName: secretName,
			ServerUrl:         "https://k8s",
		}},
	}

	k8s := k8sfake.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: argoCDNamespace,
			Labels: map[string]string{
				argoCDClusterSecretTypeLabel: argoCDClusterSecretTypeValue,
				"astronomer.io/label-tier":   "prod",
				"astronomer.io/label-team":   "platform", // should be stripped
			},
		},
	})
	ConfigureArgoCDRefresh(ArgoCDRefreshDeps{Queries: q, K8s: k8s})

	if err := HandleArgoCDRefreshManagedClusterLabels(context.Background(),
		makeRefreshTask(t, clusterID)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	got, err := k8s.CoreV1().Secrets(argoCDNamespace).Get(context.Background(), secretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, exists := got.Labels["astronomer.io/label-team"]; exists {
		t.Fatalf("astronomer.io/label-team should have been stripped; labels=%v", got.Labels)
	}
	if got.Labels["astronomer.io/label-tier"] != "prod" {
		t.Fatalf("astronomer.io/label-tier = %q, want prod", got.Labels["astronomer.io/label-tier"])
	}
}

// TestRefreshManagedClusterLabels_SanitizesKeys verifies that a cluster.Label
// of "Team Name" becomes astronomer.io/label-team-name on the Secret.
func TestRefreshManagedClusterLabels_SanitizesKeys(t *testing.T) {
	t.Cleanup(ResetArgoCDRefresh)
	instanceID := uuid.New()
	clusterID := uuid.New()
	secretName := "cluster-x"

	q := &argocdRefreshQuerierStub{
		cluster: sqlc.Cluster{
			ID:     clusterID,
			Name:   "x",
			Labels: json.RawMessage(`{"Team Name":"platform"}`),
		},
		managed: []sqlc.ArgocdManagedCluster{{
			ArgocdInstanceID:  instanceID,
			ClusterID:         clusterID,
			ClusterSecretName: secretName,
		}},
	}
	k8s := k8sfake.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: argoCDNamespace,
			Labels: map[string]string{
				argoCDClusterSecretTypeLabel: argoCDClusterSecretTypeValue,
			},
		},
	})
	ConfigureArgoCDRefresh(ArgoCDRefreshDeps{Queries: q, K8s: k8s})

	if err := HandleArgoCDRefreshManagedClusterLabels(context.Background(),
		makeRefreshTask(t, clusterID)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	got, _ := k8s.CoreV1().Secrets(argoCDNamespace).Get(context.Background(), secretName, metav1.GetOptions{})
	if got.Labels["astronomer.io/label-team-name"] != "platform" {
		t.Fatalf("label-team-name = %q, want platform; full=%v",
			got.Labels["astronomer.io/label-team-name"], got.Labels)
	}
}

// TestRefreshManagedClusterLabels_NoChangeNoPatch confirms the idempotent
// fast-path: when desired == current, the Secret is not re-PATCHed.
func TestRefreshManagedClusterLabels_NoChangeNoPatch(t *testing.T) {
	t.Cleanup(ResetArgoCDRefresh)
	instanceID := uuid.New()
	clusterID := uuid.New()
	secretName := "cluster-noop"

	q := &argocdRefreshQuerierStub{
		cluster: sqlc.Cluster{
			ID:          clusterID,
			Name:        "noop",
			Environment: "prod",
			Labels:      json.RawMessage(`{"tier":"prod"}`),
		},
		managed: []sqlc.ArgocdManagedCluster{{
			ArgocdInstanceID:  instanceID,
			ClusterID:         clusterID,
			ClusterSecretName: secretName,
		}},
	}
	k8s := k8sfake.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: argoCDNamespace,
			Labels: map[string]string{
				argoCDClusterSecretTypeLabel:            argoCDClusterSecretTypeValue,
				"astronomer.io/managed-by":              "astronomer",
				"astronomer.io/cluster-id":              clusterID.String(),
				"astronomer.io/cluster-name":            "noop",
				"astronomer.io/environment":             "prod",
				"astronomer.io/is-local":                "false",
				// No explicit profile annotation on the cluster -> desired
				// profile is the full-management admin default. Matching here
				// keeps this the idempotent no-op case.
				"astronomer.io/agent-privilege-profile": "admin",
				"astronomer.io/label-tier":              "prod",
			},
		},
	})

	// Track patch actions on the fake clientset.
	var patchCalls int
	k8s.PrependReactor("patch", "secrets", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		patchCalls++
		return false, nil, nil
	})

	ConfigureArgoCDRefresh(ArgoCDRefreshDeps{Queries: q, K8s: k8s})
	if err := HandleArgoCDRefreshManagedClusterLabels(context.Background(),
		makeRefreshTask(t, clusterID)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if patchCalls != 0 {
		t.Fatalf("expected 0 patch calls, got %d", patchCalls)
	}
	// Index-row update is still issued because the worker always re-stamps
	// the DB labels JSON to reflect the live state — that's cheap and keeps
	// the API responses honest.
	if len(q.updates) != 1 {
		t.Fatalf("Update calls = %d, want 1", len(q.updates))
	}
}

// TestRefreshManagedClusterLabels_NoManagedClusters is the no-op path: the
// cluster isn't registered into any ArgoCD instance, so the task succeeds
// without touching k8s.
func TestRefreshManagedClusterLabels_NoManagedClusters(t *testing.T) {
	t.Cleanup(ResetArgoCDRefresh)
	clusterID := uuid.New()
	q := &argocdRefreshQuerierStub{
		cluster: sqlc.Cluster{ID: clusterID, Name: "unregistered"},
		managed: nil,
	}
	k8s := k8sfake.NewClientset()
	ConfigureArgoCDRefresh(ArgoCDRefreshDeps{Queries: q, K8s: k8s})

	if err := HandleArgoCDRefreshManagedClusterLabels(context.Background(),
		makeRefreshTask(t, clusterID)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(q.updates) != 0 {
		t.Fatalf("Update calls = %d, want 0", len(q.updates))
	}
}
