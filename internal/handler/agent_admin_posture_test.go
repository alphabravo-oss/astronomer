package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// mixedProfileFleet builds a fleet of clusters across every privilege
// profile plus an empty (unspecified) and a garbage annotation. Unspecified
// defaults to least-privilege viewer, and a garbage/typo value also fails
// closed to viewer — so only the explicit admin-a and admin-b appear in the
// cluster-admin report.
func mixedProfileFleet() []sqlc.Cluster {
	return []sqlc.Cluster{
		{ID: uuid.New(), Name: "admin-a", Status: "active", IsLocal: true,
			Annotations: profileAnnotation(agenttemplate.PrivilegeProfileAdmin)},
		{ID: uuid.New(), Name: "operator-a", Status: "active",
			Annotations: profileAnnotation(agenttemplate.PrivilegeProfileOperator)},
		{ID: uuid.New(), Name: "viewer-a", Status: "active",
			Annotations: profileAnnotation(agenttemplate.PrivilegeProfileViewer)},
		{ID: uuid.New(), Name: "admin-b", Status: "disconnected",
			Annotations: profileAnnotation(agenttemplate.PrivilegeProfileAdmin)},
		{ID: uuid.New(), Name: "ns-operator", Status: "active",
			Annotations: profileAnnotation(agenttemplate.PrivilegeProfileNamespaceOperator)},
		// No annotation at all → unspecified → defaults to least-privilege
		// viewer now, so it must NOT appear in the cluster-admin report.
		{ID: uuid.New(), Name: "no-annotation", Status: "active"},
		// Garbage/typo profile → fails closed to viewer, must not be reported.
		{ID: uuid.New(), Name: "garbage", Status: "active",
			Annotations: profileAnnotation("super-duper-admin")},
	}
}

// TestClusterAdminPosture_OnlyAdminProfileClustersReported is the E3
// report test: over a fixture of mixed profiles, the report returns ONLY
// the clusters whose agent resolves to the cluster-admin `admin` profile.
func TestClusterAdminPosture_OnlyAdminProfileClustersReported(t *testing.T) {
	callerID := uuid.New()
	q := &fakeAgentFleetQuerier{
		clusters: mixedProfileFleet(),
		users:    map[uuid.UUID]sqlc.User{callerID: {ID: callerID, IsSuperuser: true}},
	}
	h := NewAgentFleetHandler(q)

	w := httptest.NewRecorder()
	req := makeRequest("/api/v1/admin/agents/cluster-admin-posture/", callerID)
	h.ClusterAdminPosture(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var env struct {
		Data clusterAdminPostureResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v; body=%s", err, w.Body.String())
	}
	got := env.Data

	if got.TotalClusters != 7 {
		t.Fatalf("total_clusters = %d, want 7", got.TotalClusters)
	}
	// admin-a, admin-b (explicit) only. no-annotation now defaults to viewer
	// (not admin), and the garbage/typo cluster still fails closed to viewer.
	if got.AdminProfileClusters != 2 {
		t.Fatalf("admin_profile_clusters = %d, want 2", got.AdminProfileClusters)
	}
	if len(got.Items) != 2 {
		t.Fatalf("len(items) = %d, want 2; items=%+v", len(got.Items), got.Items)
	}
	names := map[string]bool{}
	for _, it := range got.Items {
		names[it.ClusterName] = true
		if it.PrivilegeProfile != agenttemplate.PrivilegeProfileAdmin {
			t.Fatalf("item %q profile = %q, want admin", it.ClusterName, it.PrivilegeProfile)
		}
	}
	for _, want := range []string{"admin-a", "admin-b"} {
		if !names[want] {
			t.Fatalf("expected admin cluster %q in report; got %+v", want, got.Items)
		}
	}
	// Negative invariant: operator/viewer/ns clusters, the garbage/typo cluster
	// (fails closed to viewer), and the no-annotation cluster (now defaults to
	// viewer) must NOT leak into the cluster-admin report.
	for _, mustNot := range []string{"operator-a", "viewer-a", "ns-operator", "garbage", "no-annotation"} {
		if names[mustNot] {
			t.Fatalf("cluster %q must NOT be reported as cluster-admin", mustNot)
		}
	}
}

// TestClusterAdminPosture_RequiresSuperuser is the negative auth test:
// a non-superuser gets 403 and an anonymous caller gets 401 — the report
// (which enumerates the highest-privilege agents in the fleet) must not be
// reachable without superuser.
func TestClusterAdminPosture_RequiresSuperuser(t *testing.T) {
	callerID := uuid.New()
	q := &fakeAgentFleetQuerier{
		clusters: mixedProfileFleet(),
		users:    map[uuid.UUID]sqlc.User{callerID: {ID: callerID, IsSuperuser: false}},
	}
	h := NewAgentFleetHandler(q)

	t.Run("NonSuperuser", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := makeRequest("/api/v1/admin/agents/cluster-admin-posture/", callerID)
		h.ClusterAdminPosture(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("Anonymous", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/agents/cluster-admin-posture/", nil)
		h.ClusterAdminPosture(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", w.Code, w.Body.String())
		}
	})
}
