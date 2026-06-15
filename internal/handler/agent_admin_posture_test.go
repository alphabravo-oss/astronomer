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
// profile plus an empty/garbage annotation (which the canonical
// NormalizePrivilegeProfile fails closed to viewer). Only the two clusters
// carrying the explicit `admin` annotation must appear in the report.
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
		// No annotation at all → must fail closed to viewer, NOT report
		// as admin. This is the GATE-0 (C2) invariant under test.
		{ID: uuid.New(), Name: "no-annotation", Status: "active"},
		// Garbage profile → fails closed to viewer, must not be reported.
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
	// Negative invariant: clusters that fail closed to viewer (no
	// annotation / garbage) and the operator/viewer clusters must NOT
	// leak into the cluster-admin report.
	for _, mustNot := range []string{"operator-a", "viewer-a", "ns-operator", "no-annotation", "garbage"} {
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
