package handler

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

func TestEventAllowedForUser_FiltersByCluster(t *testing.T) {
	engine := rbac.NewEngine()
	allowedCluster := uuid.New()
	otherCluster := uuid.New()
	bindings := []rbac.RoleBinding{{
		ClusterID: allowedCluster.String(),
		RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceClusters), Verbs: []string{string(rbac.VerbRead)}}},
	}}
	a := authorizationSupport{engine: engine}

	okEv := events.Event{
		Type: events.TypeClusterConnected,
		Data: map[string]any{"cluster_id": allowedCluster.String()},
	}
	if !eventAllowedForUser(a, bindings, okEv) {
		t.Fatal("expected allow for authorized cluster")
	}

	denyEv := events.Event{
		Type: events.TypeClusterConnected,
		Data: map[string]any{"cluster_id": otherCluster.String()},
	}
	if eventAllowedForUser(a, bindings, denyEv) {
		t.Fatal("expected deny for unauthorized cluster")
	}

	raw, _ := json.Marshal(map[string]any{"cluster_id": allowedCluster.String()})
	rawEv := events.Event{Type: events.TypeClusterMetrics, Data: json.RawMessage(raw)}
	if !eventAllowedForUser(a, bindings, rawEv) {
		t.Fatal("expected allow for raw JSON payload")
	}

	noCluster := events.Event{Type: events.TypeClusterConnected, Data: map[string]any{"foo": "bar"}}
	if eventAllowedForUser(a, bindings, noCluster) {
		t.Fatal("restricted users must not receive unscoped events")
	}
}
