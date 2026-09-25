package handler

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/google/uuid"
)

type pagedCatalogVisibilityQuerier struct {
	*clusterCatalogQuerier
	projectsPage              []sqlc.Project
	globalsPage               []sqlc.HelmRepository
	projectCalls, globalCalls int
}

func (q *pagedCatalogVisibilityQuerier) ListCatalogProjectsByCluster(_ context.Context, arg sqlc.ListCatalogProjectsByClusterParams) ([]sqlc.Project, error) {
	q.projectCalls++
	start := min(int(arg.QueryOffset), len(q.projectsPage))
	end := min(start+int(arg.QueryLimit), len(q.projectsPage))
	return q.projectsPage[start:end], nil
}
func (q *pagedCatalogVisibilityQuerier) ListGlobalHelmRepositories(_ context.Context, arg sqlc.ListGlobalHelmRepositoriesParams) ([]sqlc.HelmRepository, error) {
	q.globalCalls++
	start := min(int(arg.Offset), len(q.globalsPage))
	end := min(start+int(arg.Limit), len(q.globalsPage))
	return q.globalsPage[start:end], nil
}

func TestCatalogVisibilityIncludesRepositoriesAndAuthorizedProjectAfterOldLimit(t *testing.T) {
	cluster, project := uuid.New(), uuid.New()
	base := newFakeProjectCatalogQuerier()
	repo := seedOwned(base, "late-project-catalog", project)
	q := &pagedCatalogVisibilityQuerier{clusterCatalogQuerier: &clusterCatalogQuerier{minimalCatalogQuerier: &minimalCatalogQuerier{fakeProjectCatalogQuerier: base}}}
	for i := 0; i < 10001; i++ {
		q.projectsPage = append(q.projectsPage, sqlc.Project{ID: uuid.New(), ClusterID: cluster})
		q.globalsPage = append(q.globalsPage, sqlc.HelmRepository{ID: uuid.New()})
	}
	q.projectsPage[10000].ID = project
	h := NewCatalogHandler(q)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: []rbac.RoleBinding{{ProjectID: project.String(), RoleRules: []rbac.Rule{{Resource: "catalog", Verbs: []string{"read"}}}}}})
	req := authedCatalogReq("GET", "/", nil)
	ids, err := h.visibleCatalogRepositoryIDs(req.Context(), cluster)
	if err != nil {
		t.Fatal(err)
	}
	found := map[uuid.UUID]bool{}
	for _, id := range ids {
		found[id] = true
	}
	if !found[repo] || !found[q.globalsPage[10000].ID] || len(ids) != 10002 || q.projectCalls != 21 || q.globalCalls != 21 {
		t.Fatalf("visibility truncated: count=%d projectcalls=%d repocalls=%d", len(ids), q.projectCalls, q.globalCalls)
	}
}

func TestCatalogVisibilityPaginationFailsOnBackendErrorNonadvanceAndCancellation(t *testing.T) {
	rows := make([]uuid.UUID, catalogVisibilityPageSize)
	for i := range rows {
		rows[i] = uuid.New()
	}
	for _, mode := range []string{"repeat", "error", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			_, err := collectCatalogVisibilityPages(ctx, func(_, offset int32) ([]uuid.UUID, error) {
				calls++
				if offset > 0 && mode == "error" {
					return nil, errors.New("read failed")
				}
				if mode == "cancel" {
					cancel()
				}
				return rows, nil
			}, func(id uuid.UUID) uuid.UUID { return id })
			if err == nil || calls > 2 {
				t.Fatalf("pagination silently succeeded or looped: %v %d", err, calls)
			}
		})
	}
}

func TestCatalogBrowseRejectsAmbiguousProjectAndCluster(t *testing.T) {
	q := &recordingChartSearchQuerier{clusterCatalogQuerier: &clusterCatalogQuerier{minimalCatalogQuerier: &minimalCatalogQuerier{fakeProjectCatalogQuerier: newFakeProjectCatalogQuerier()}}}
	h := NewCatalogHandler(q)
	w := httptest.NewRecorder()
	h.ListCharts(w, httptest.NewRequest("GET", "/?project_id="+uuid.NewString()+"&cluster_id="+uuid.NewString(), nil))
	if w.Code != 400 || q.calls != 0 {
		t.Fatalf("ambiguous scope accepted %d %s", w.Code, w.Body.String())
	}
}
