package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// rbacProfileQuerier is a minimal ToolQuerier that only serves GetClusterByID.
// It embeds the ToolQuerier interface so it satisfies the full contract without
// stubbing every method; checkClusterRBACProfile only ever calls
// GetClusterByID, so the embedded nil methods are never invoked.
type rbacProfileQuerier struct {
	ToolQuerier
	cluster sqlc.Cluster
	err     error
}

func (q rbacProfileQuerier) GetClusterByID(context.Context, uuid.UUID) (sqlc.Cluster, error) {
	return q.cluster, q.err
}

// clusterWithProfile builds a cluster whose annotations carry the given agent
// privilege profile, mirroring what the registration path stamps on the row.
func clusterWithProfile(t *testing.T, profile string) sqlc.Cluster {
	t.Helper()
	raw, err := json.Marshal(map[string]string{
		agenttemplate.PrivilegeProfileAnnotation: profile,
	})
	if err != nil {
		t.Fatalf("marshal annotations: %v", err)
	}
	return sqlc.Cluster{Annotations: raw}
}

func TestCheckClusterRBACProfile(t *testing.T) {
	tests := []struct {
		name      string
		slug      string
		cluster   sqlc.Cluster
		wantOK    bool
		wantInMsg string
	}{
		{
			name:    "admin cluster + cluster-RBAC component allowed",
			slug:    "cert-manager",
			cluster: clusterWithProfile(t, agenttemplate.PrivilegeProfileAdmin),
			wantOK:  true,
		},
		{
			name:    "admin cluster + gatekeeper allowed",
			slug:    "gatekeeper",
			cluster: clusterWithProfile(t, agenttemplate.PrivilegeProfileAdmin),
			wantOK:  true,
		},
		{
			name:      "operator cluster + cluster-RBAC component rejected",
			slug:      "cert-manager",
			cluster:   clusterWithProfile(t, agenttemplate.PrivilegeProfileOperator),
			wantOK:    false,
			wantInMsg: "cluster-scoped RBAC",
		},
		{
			name:    "operator cluster + non-cluster-RBAC component allowed",
			slug:    "prometheus-node-exporter",
			cluster: clusterWithProfile(t, agenttemplate.PrivilegeProfileOperator),
			wantOK:  true,
		},
		{
			name:    "viewer cluster + unknown/non-baseline slug allowed",
			slug:    "some-catalog-tool",
			cluster: clusterWithProfile(t, agenttemplate.PrivilegeProfileViewer),
			wantOK:  true,
		},
		{
			name:      "unspecified profile defaults to viewer and is rejected for cluster-RBAC component",
			slug:      "ingress-nginx",
			cluster:   sqlc.Cluster{}, // no annotations -> normalizes to viewer
			wantOK:    false,
			wantInMsg: "admin-profile",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &ToolHandler{queries: rbacProfileQuerier{cluster: tt.cluster}}
			msg, ok := h.checkClusterRBACProfile(context.Background(), tt.slug, uuid.New())
			if ok != tt.wantOK {
				t.Fatalf("checkClusterRBACProfile(%q) ok = %v, want %v (msg=%q)", tt.slug, ok, tt.wantOK, msg)
			}
			if tt.wantOK && msg != "" {
				t.Fatalf("allowed result should have empty message, got %q", msg)
			}
			if !tt.wantOK {
				if msg == "" {
					t.Fatalf("rejected result should carry an explanatory message")
				}
				if tt.wantInMsg != "" && !strings.Contains(msg, tt.wantInMsg) {
					t.Fatalf("message %q does not contain %q", msg, tt.wantInMsg)
				}
			}
		})
	}
}

// A cluster row that can't be loaded must fail open (allow), mirroring
// checkToolScope, so a transient lookup miss never wrongly blocks an install.
func TestCheckClusterRBACProfileFailsOpenOnLookupError(t *testing.T) {
	h := &ToolHandler{queries: rbacProfileQuerier{err: context.DeadlineExceeded}}
	if msg, ok := h.checkClusterRBACProfile(context.Background(), "cert-manager", uuid.New()); !ok {
		t.Fatalf("expected fail-open allow on cluster lookup error, got reject: %q", msg)
	}
}
