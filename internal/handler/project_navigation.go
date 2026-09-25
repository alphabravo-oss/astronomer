package handler

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	projectdomain "github.com/alphabravocompany/astronomer-go/internal/projects"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/google/uuid"
)

type ProjectNamespaceScopeResponse struct {
	ClusterID  string   `json:"cluster_id"`
	Namespaces []string `json:"namespaces"`
}

type projectNamespaceScopeQuerier interface {
	ListProjectNamespaceScopes(context.Context, []uuid.UUID) ([]sqlc.ProjectNamespace, error)
}
type clusterProjectScopeQuerier interface {
	ListClusterProjectsForScopes(context.Context, sqlc.ListClusterProjectsForScopesParams) ([]sqlc.Project, error)
	CountClusterProjectsForScopes(context.Context, sqlc.CountClusterProjectsForScopesParams) (int64, error)
}

func (h *ProjectHandler) projectNavigationResponses(ctx context.Context, projects []sqlc.Project) ([]ProjectResponse, error) {
	items := make([]ProjectResponse, 0, len(projects))
	if len(projects) == 0 {
		return items, nil
	}
	q, ok := h.queries.(projectNamespaceScopeQuerier)
	if !ok {
		return nil, fmt.Errorf("project namespace scope query unavailable")
	}
	ids := make([]uuid.UUID, 0, len(projects))
	for _, p := range projects {
		ids = append(ids, p.ID)
	}
	rows, err := q.ListProjectNamespaceScopes(ctx, ids)
	if err != nil {
		return nil, err
	}
	byProject := make(map[uuid.UUID][]sqlc.ProjectNamespace)
	for _, row := range rows {
		byProject[row.ProjectID] = append(byProject[row.ProjectID], row)
	}
	for _, p := range projects {
		item := projectToResponse(p)
		scopes := map[string]map[string]bool{p.ClusterID.String(): {}}
		// Legacy projects predate the sidecar; their primary namespace array is
		// still authoritative for that cluster alone, never for a secondary one.
		for _, ns := range projectdomain.Namespaces(p.Namespaces) {
			scopes[p.ClusterID.String()][ns] = true
		}
		for _, row := range byProject[p.ID] {
			id := row.ClusterID.String()
			if scopes[id] == nil {
				scopes[id] = map[string]bool{}
			}
			scopes[id][row.Namespace] = true
		}
		for id := range scopes {
			item.ClusterIDs = append(item.ClusterIDs, id)
		}
		sort.Strings(item.ClusterIDs)
		for _, id := range item.ClusterIDs {
			scope := ProjectNamespaceScopeResponse{ClusterID: id, Namespaces: []string{}}
			for ns := range scopes[id] {
				scope.Namespaces = append(scope.Namespaces, ns)
			}
			sort.Strings(scope.Namespaces)
			item.NamespaceScopes = append(item.NamespaceScopes, scope)
		}
		items = append(items, item)
	}
	return items, nil
}

func (h *ProjectHandler) ListByCluster(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	all, clusterIDs, projectIDs, err := h.authz.authorizedScopeIDs(r.Context(), rbac.ResourceProjects, rbac.VerbList, rbac.NarrowedClustersExcluded)
	if err != nil {
		RespondRequestError(w, r, 500, apierror.InternalError, "Failed to retrieve user permissions")
		return
	}
	q, ok := h.queries.(clusterProjectScopeQuerier)
	if !ok {
		RespondRequestError(w, r, 500, apierror.InternalError, "Scoped cluster project listing is not available")
		return
	}
	limit, offset := int32(queryLimit(r, 20)), int32(queryOffset(r))
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	rows, err := q.ListClusterProjectsForScopes(r.Context(), sqlc.ListClusterProjectsForScopesParams{SelectedClusterID: clusterID, AllScopes: all, ClusterIds: clusterIDs, ProjectIds: projectIDs, FilterSearch: search, QueryLimit: limit, QueryOffset: offset})
	if err != nil {
		RespondRequestError(w, r, 500, apierror.ListError, "Failed to list projects")
		return
	}
	total, err := q.CountClusterProjectsForScopes(r.Context(), sqlc.CountClusterProjectsForScopesParams{SelectedClusterID: clusterID, AllScopes: all, ClusterIds: clusterIDs, ProjectIds: projectIDs, FilterSearch: search})
	if err != nil {
		RespondRequestError(w, r, 500, apierror.CountError, "Failed to count projects")
		return
	}
	items, err := h.projectNavigationResponses(r.Context(), rows)
	if err != nil {
		RespondRequestError(w, r, 500, apierror.ListError, "Failed to read project namespace scopes")
		return
	}
	paging.Write(w, items, paging.Exact(total, int(limit), int(offset), len(items)))
}
