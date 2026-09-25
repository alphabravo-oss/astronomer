package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/google/uuid"
)

type navigationProjectQuerier struct {
	*policyTestQuerier
	scopeErr error
	scopeIDs []uuid.UUID
	listArg  sqlc.ListClusterProjectsForScopesParams
	countArg sqlc.CountClusterProjectsForScopesParams
	rows     []sqlc.Project
}

func (q *navigationProjectQuerier) ListProjectNamespaceScopes(_ context.Context, ids []uuid.UUID) ([]sqlc.ProjectNamespace, error) {
	q.scopeIDs = ids
	return q.nsRows, q.scopeErr
}
func (q *navigationProjectQuerier) ListClusterProjectsForScopes(_ context.Context, arg sqlc.ListClusterProjectsForScopesParams) ([]sqlc.Project, error) {
	q.listArg = arg
	return q.rows, nil
}
func (q *navigationProjectQuerier) CountClusterProjectsForScopes(_ context.Context, arg sqlc.CountClusterProjectsForScopesParams) (int64, error) {
	q.countArg = arg
	return 17, nil
}

func TestProjectNavigationReadPreservesClusterNamespaceIdentity(t *testing.T) {
	primary, secondary, project, foreign := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	q := &navigationProjectQuerier{policyTestQuerier: newPolicyTestQuerier()}
	q.projects[project] = sqlc.Project{ID: project, ClusterID: primary, Namespaces: json.RawMessage(`["primary-only"]`)}
	q.nsRows = []sqlc.ProjectNamespace{{ProjectID: project, ClusterID: secondary, Namespace: "secondary-only"}, {ProjectID: project, ClusterID: secondary, Namespace: "shared"}, {ProjectID: foreign, ClusterID: secondary, Namespace: "foreign"}}
	h := NewProjectHandler(q)
	rec := httptest.NewRecorder()
	h.Get(rec, authedCatalogReq("GET", "/", map[string]string{"id": project.String()}))
	var out struct {
		Data ProjectResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || len(out.Data.ClusterIDs) != 2 || !reflect.DeepEqual(q.scopeIDs, []uuid.UUID{project}) {
		t.Fatalf("invalid scope response %d %s", rec.Code, rec.Body.String())
	}
	scopes := map[string][]string{}
	for _, s := range out.Data.NamespaceScopes {
		scopes[s.ClusterID] = s.Namespaces
	}
	if !reflect.DeepEqual(scopes[primary.String()], []string{"primary-only"}) || !reflect.DeepEqual(scopes[secondary.String()], []string{"secondary-only", "shared"}) {
		t.Fatalf("cross-cluster namespaces %+v", scopes)
	}
	q.scopeErr = errors.New("database unavailable")
	rec = httptest.NewRecorder()
	h.Get(rec, authedCatalogReq("GET", "/", map[string]string{"id": project.String()}))
	if rec.Code != 500 {
		t.Fatalf("silently returned incomplete scopes: %d", rec.Code)
	}
}
func TestProjectNavigationClusterListFiltersScopeBeforePageAndCount(t *testing.T) {
	cluster, project := uuid.New(), uuid.New()
	for _, narrowed := range []bool{false, true} {
		t.Run(map[bool]string{false: "project grant", true: "namespace-only grant"}[narrowed], func(t *testing.T) {
			q := &navigationProjectQuerier{policyTestQuerier: newPolicyTestQuerier(), rows: []sqlc.Project{{ID: project, ClusterID: cluster}}}
			binding := rbac.RoleBinding{ProjectID: project.String(), RoleRules: []rbac.Rule{{Resource: "projects", Verbs: []string{"list"}}}}
			if narrowed {
				binding.ProjectID = ""
				binding.ClusterID = cluster.String()
				binding.Namespace = "team-a"
			}
			h := NewProjectHandler(q)
			h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: []rbac.RoleBinding{binding}})
			rec := httptest.NewRecorder()
			h.ListByCluster(rec, authedCatalogReq("GET", "/?limit=2&offset=3&search=needle", map[string]string{"cluster_id": cluster.String()}))
			if rec.Code != 200 || q.listArg.AllScopes || len(q.listArg.ClusterIds) != 0 || q.listArg.QueryLimit != 2 || q.listArg.QueryOffset != 3 || q.listArg.SelectedClusterID != cluster || q.listArg.FilterSearch != "needle" {
				t.Fatalf("incorrect SQL scope %+v response %d %s", q.listArg, rec.Code, rec.Body.String())
			}
			if narrowed && len(q.listArg.ProjectIds) != 0 || !narrowed && !reflect.DeepEqual(q.listArg.ProjectIds, []uuid.UUID{project}) {
				t.Fatalf("namespace grant widened %+v", q.listArg)
			}
			if q.countArg.AllScopes != q.listArg.AllScopes || !reflect.DeepEqual(q.countArg.ProjectIds, q.listArg.ProjectIds) || !reflect.DeepEqual(q.countArg.ClusterIds, q.listArg.ClusterIds) || q.countArg.SelectedClusterID != cluster || q.countArg.FilterSearch != "needle" {
				t.Fatal("page/count authorization differ")
			}
		})
	}
}
