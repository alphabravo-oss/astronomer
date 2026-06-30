package handler

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// legacyClusterWithMetrics is a snapshot of the pre-DTO wire shape: it embeds
// sqlc.Cluster anonymously so every column is marshaled at the top level, then
// tacks on the metric scalars. We keep it here (not in production code) purely
// to pin the wire contract for TestClusterResponse_WireCompat.
type legacyClusterWithMetrics struct {
	sqlc.Cluster
	CPUPercentage        float64 `json:"cpu_percentage"`
	MemoryPercentage     float64 `json:"memory_percentage"`
	PodCount             int     `json:"pod_count"`
	MetricsServerPresent bool    `json:"metrics_server_present"`
}

// fixtureCluster returns a sqlc.Cluster populated across every column type
// (string, int32, bool, json.RawMessage, time.Time, pgtype.Timestamptz,
// pgtype.UUID — both Valid=true and Valid=false variants where applicable).
// Used by TestClusterResponse_WireCompat to confirm clusterToResponse emits
// the exact same JSON bytes the legacy embed-sqlc.Cluster shape did.
func fixtureCluster(t *testing.T, withOptionals bool) sqlc.Cluster {
	t.Helper()
	clusterID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	creatorID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	createdAt := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 2, 20, 12, 45, 30, 123456789, time.UTC)
	hb := time.Date(2026, 5, 10, 8, 15, 0, 0, time.UTC)
	dec := time.Date(2026, 5, 11, 9, 0, 0, 0, time.UTC)

	c := sqlc.Cluster{
		ID:                clusterID,
		Name:              "prod-east",
		DisplayName:       "Prod East",
		Description:       "Production east region",
		Status:            "active",
		ApiServerUrl:      "https://k8s.example.com",
		CaCertificate:     "-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----",
		Environment:       "production",
		Region:            "us-east-1",
		Provider:          "aws",
		Labels:            json.RawMessage(`{"team":"platform"}`),
		Annotations:       json.RawMessage(`{"owner":"sre"}`),
		Distribution:      "eks",
		AgentVersion:      "v1.2.3",
		KubernetesVersion: "v1.30.2",
		NodeCount:         12,
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
		IsLocal:           false,
	}
	if withOptionals {
		c.LastHeartbeat = pgtype.Timestamptz{Time: hb, Valid: true}
		c.DecommissionedAt = pgtype.Timestamptz{Time: dec, Valid: true}
		c.CreatedByID = pgtype.UUID{Bytes: creatorID, Valid: true}
	}
	return c
}

// TestClusterResponse_WireCompat asserts that clusterToResponse + the metric
// scalars produce byte-for-byte identical JSON to the legacy
// clusterWithMetrics{Cluster: c, ...} shape. This is the load-bearing
// guarantee that the DTO migration didn't change the dashboard API contract.
func TestClusterResponse_WireCompat(t *testing.T) {
	cases := []struct {
		name    string
		cluster sqlc.Cluster
		cpu     float64
		mem     float64
		pods    int
	}{
		{
			name:    "all fields populated",
			cluster: fixtureCluster(t, true),
			cpu:     42.5,
			mem:     63.1,
			pods:    87,
		},
		{
			name:    "optionals invalid (null pgtype)",
			cluster: fixtureCluster(t, false),
			cpu:     0,
			mem:     0,
			pods:    0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			legacy := legacyClusterWithMetrics{
				Cluster:          tc.cluster,
				CPUPercentage:    tc.cpu,
				MemoryPercentage: tc.mem,
				PodCount:         tc.pods,
			}
			legacyJSON, err := json.Marshal(legacy)
			if err != nil {
				t.Fatalf("marshal legacy: %v", err)
			}
			var legacyMap map[string]any
			if err := json.Unmarshal(legacyJSON, &legacyMap); err != nil {
				t.Fatalf("unmarshal legacy: %v", err)
			}
			legacyJSON, err = json.Marshal(legacyMap)
			if err != nil {
				t.Fatalf("remarshal legacy: %v", err)
			}

			resp := clusterToResponse(tc.cluster)
			resp.CPUPercentage = tc.cpu
			resp.MemoryPercentage = tc.mem
			resp.PodCount = tc.pods
			dtoJSON, err := json.Marshal(resp)
			if err != nil {
				t.Fatalf("marshal dto: %v", err)
			}
			var dtoMap map[string]any
			if err := json.Unmarshal(dtoJSON, &dtoMap); err != nil {
				t.Fatalf("unmarshal dto: %v", err)
			}
			delete(dtoMap, "argocd")
			delete(dtoMap, "agent_privilege_profile")
			dtoJSON, err = json.Marshal(dtoMap)
			if err != nil {
				t.Fatalf("remarshal dto: %v", err)
			}

			if !bytes.Equal(legacyJSON, dtoJSON) {
				t.Fatalf("wire mismatch:\n legacy: %s\n dto:    %s", legacyJSON, dtoJSON)
			}
		})
	}
}

func TestClusterResponseIncludesAgentPrivilegeProfile(t *testing.T) {
	cluster := fixtureCluster(t, false)
	cluster.Annotations = json.RawMessage(`{"astronomer.io/agent-privilege-profile":"operator"}`)

	resp := clusterToResponse(cluster)
	if resp.AgentPrivilegeProfile != "operator" {
		t.Fatalf("agent privilege profile = %q, want operator", resp.AgentPrivilegeProfile)
	}

	// An invalid/unknown profile must fail closed to least privilege (C2),
	// never to the cluster-admin profile.
	cluster.Annotations = json.RawMessage(`{"astronomer.io/agent-privilege-profile":"invalid"}`)
	resp = clusterToResponse(cluster)
	if resp.AgentPrivilegeProfile != "viewer" {
		t.Fatalf("invalid profile = %q, want viewer fallback", resp.AgentPrivilegeProfile)
	}

	// An explicit admin profile must still resolve to admin.
	cluster.Annotations = json.RawMessage(`{"astronomer.io/agent-privilege-profile":"admin"}`)
	resp = clusterToResponse(cluster)
	if resp.AgentPrivilegeProfile != "admin" {
		t.Fatalf("explicit admin profile = %q, want admin", resp.AgentPrivilegeProfile)
	}
}

func TestClusterResponseIncludesBaselineComponentOwnership(t *testing.T) {
	// Default-on set is the two metrics exporters only; everything else
	// (trivy, fluent-bit, ingress-nginx, cert-manager, gatekeeper) is opt-in
	// and must NOT be reported as managed unless explicitly enabled.
	resp := clusterToResponse(fixtureCluster(t, false))
	if len(resp.ArgoCD.BaselineComponents) != 2 {
		t.Fatalf("baseline component count = %d, want 2 (metrics exporters only)", len(resp.ArgoCD.BaselineComponents))
	}
	for _, component := range resp.ArgoCD.BaselineComponents {
		if component.ManagedBy != "unknown" {
			t.Fatalf("component %s managed_by = %q, want unknown", component.Slug, component.ManagedBy)
		}
		if component.ApplicationSetName == "" {
			t.Fatalf("component %s missing application set name", component.Slug)
		}
		switch component.Slug {
		case "trivy-operator", "fluent-bit", "ingress-nginx", "cert-manager", "gatekeeper":
			t.Fatalf("opt-in component %s should not be listed by default", component.Slug)
		}
	}

	components := baselineComponentOwnership("argocd")
	if len(components) != 2 {
		t.Fatalf("argocd component count = %d, want 2", len(components))
	}
	for _, component := range components {
		if component.ManagedBy != "argocd" {
			t.Fatalf("component %s managed_by = %q, want argocd", component.Slug, component.ManagedBy)
		}
		switch component.Slug {
		case "kube-state-metrics", "prometheus-node-exporter":
		default:
			t.Fatalf("unexpected baseline-managed component %q (only metrics exporters expected)", component.Slug)
		}
	}
}

func TestSummarizeArgoCDDrift(t *testing.T) {
	first := time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC)
	last := time.Date(2026, 6, 13, 10, 30, 0, 0, time.UTC)

	summary := summarizeArgoCDDrift([]sqlc.ArgocdApplication{
		{
			Name:         "cert-manager",
			SyncStatus:   "Synced",
			HealthStatus: "Healthy",
			LastSynced:   pgtype.Timestamptz{Time: first, Valid: true},
		},
		{
			Name:                 "ingress-nginx",
			SyncStatus:           "OutOfSync",
			HealthStatus:         "Degraded",
			LastSynced:           pgtype.Timestamptz{Time: last, Valid: true},
			ResourceCreatedCount: 1,
			ResourceChangedCount: 2,
		},
		{
			Name:                "external-secrets",
			SyncStatus:          "out_of_sync",
			HealthStatus:        "Progressing",
			ResourcePrunedCount: 1,
		},
		{
			Name:         "monitoring",
			SyncStatus:   "",
			HealthStatus: "",
		},
	})

	if summary.AppCount != 4 {
		t.Fatalf("app count = %d, want 4", summary.AppCount)
	}
	if summary.SyncedCount != 1 || summary.OutOfSyncCount != 2 || summary.UnknownSyncCount != 1 {
		t.Fatalf("sync counts = synced:%d out:%d unknown:%d", summary.SyncedCount, summary.OutOfSyncCount, summary.UnknownSyncCount)
	}
	if summary.HealthyCount != 1 || summary.ProgressingCount != 1 || summary.DegradedCount != 1 || summary.UnknownHealthCount != 1 {
		t.Fatalf("health counts = healthy:%d progressing:%d degraded:%d unknown:%d", summary.HealthyCount, summary.ProgressingCount, summary.DegradedCount, summary.UnknownHealthCount)
	}
	if summary.LastSynced == nil || *summary.LastSynced != last.Format(time.RFC3339Nano) {
		t.Fatalf("last_synced = %v, want %s", summary.LastSynced, last.Format(time.RFC3339Nano))
	}
	if summary.LastError != "1 degraded ArgoCD application" {
		t.Fatalf("last_error = %q", summary.LastError)
	}
	if summary.ResourceCreatedCount != 1 || summary.ResourceChangedCount != 2 || summary.ResourcePrunedCount != 1 {
		t.Fatalf("resource counts = created:%d changed:%d pruned:%d", summary.ResourceCreatedCount, summary.ResourceChangedCount, summary.ResourcePrunedCount)
	}
}

func TestArgoCDManagedClusterApplicationTargets(t *testing.T) {
	instanceA := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	instanceB := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	cluster := fixtureCluster(t, false)
	cluster.Name = "prod-east"

	instanceIDs, targets := argoCDManagedClusterApplicationTargets(cluster, []sqlc.ArgocdManagedCluster{
		{
			ArgocdInstanceID:  instanceA,
			ServerUrl:         "https://proxy.example.com/clusters/prod-east",
			ClusterSecretName: "astronomer-prod-east",
		},
		{
			ArgocdInstanceID:  instanceA,
			ServerUrl:         "https://proxy.example.com/clusters/prod-east",
			ClusterSecretName: "astronomer-prod-east",
		},
		{
			ArgocdInstanceID:  instanceB,
			ServerUrl:         "https://proxy.example.com/clusters/prod-east-2",
			ClusterSecretName: "",
		},
	})

	if len(instanceIDs) != 2 || instanceIDs[0] != instanceA || instanceIDs[1] != instanceB {
		t.Fatalf("instance IDs = %v, want [%s %s]", instanceIDs, instanceA, instanceB)
	}
	gotTargets := map[string]bool{}
	for _, target := range targets {
		if gotTargets[target] {
			t.Fatalf("duplicate target %q in %v", target, targets)
		}
		gotTargets[target] = true
	}
	for _, want := range []string{
		"https://proxy.example.com/clusters/prod-east",
		"astronomer-prod-east",
		"https://proxy.example.com/clusters/prod-east-2",
		"prod-east",
	} {
		if !gotTargets[want] {
			t.Fatalf("missing target %q in %v", want, targets)
		}
	}
}
