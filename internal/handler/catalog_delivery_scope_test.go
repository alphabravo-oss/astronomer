package handler

import (
	"context"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type platformCatalogScopeQuerier struct {
	*installedCatalogAuditQuerier
	project sqlc.Project
	err     error
}

func (q *platformCatalogScopeQuerier) GetProjectByNameAndCluster(_ context.Context, arg sqlc.GetProjectByNameAndClusterParams) (sqlc.Project, error) {
	if arg.Name != "astronomer-system" {
		panic("unexpected platform project name")
	}
	return q.project, q.err
}

func TestPlatformCatalogProjectFailsClosed(t *testing.T) {
	base, clusterID, projectID, _ := newInstalledCatalogAuditQuerier()
	for _, tc := range []struct {
		name    string
		project sqlc.Project
		missing bool
		wantOK  bool
	}{
		{"canonical", sqlc.Project{ID: projectID, ClusterID: clusterID, ManagedBy: "system"}, false, true},
		{"user-controlled project", sqlc.Project{ID: projectID, ClusterID: clusterID, ManagedBy: "user"}, false, false},
		{"different cluster", sqlc.Project{ID: projectID, ClusterID: uuid.New(), ManagedBy: "system"}, false, false},
		{"missing identity", sqlc.Project{ClusterID: clusterID, ManagedBy: "system"}, false, false},
		{"missing project", sqlc.Project{}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := &platformCatalogScopeQuerier{installedCatalogAuditQuerier: base, project: tc.project}
			if tc.missing {
				q.err = pgx.ErrNoRows
			}
			h := NewCatalogHandler(q)
			got, err := h.platformCatalogProject(context.Background(), clusterID)
			if (err == nil) != tc.wantOK {
				t.Fatalf("id=%s err=%v", got, err)
			}
			if tc.wantOK && got != projectID {
				t.Fatalf("id=%s want %s", got, projectID)
			}
		})
	}
}
