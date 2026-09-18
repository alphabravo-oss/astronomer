package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// --- Helm Charts ---

func catalogProjectQuery(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if raw == "" {
		return uuid.Nil, false, true
	}
	projectID, err := uuid.Parse(raw)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid project_id query param")
		return uuid.Nil, false, false
	}
	return projectID, true, true
}

func catalogClusterQuery(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("cluster_id"))
	if raw == "" {
		return uuid.Nil, false, true
	}
	clusterID, err := uuid.Parse(raw)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster_id query param")
		return uuid.Nil, false, false
	}
	return clusterID, true, true
}

var errCatalogClusterAccessDenied = errors.New("catalog access denied for cluster")

func (h *CatalogHandler) visibleCatalogRepositoryIDs(ctx context.Context, clusterID uuid.UUID) ([]uuid.UUID, error) {
	resolver, ok := h.queries.(catalogClusterProjectResolver)
	if !ok {
		return nil, errors.New("cluster project resolver is unavailable")
	}
	projects, err := resolver.ListProjectsByCluster(ctx, sqlc.ListProjectsByClusterParams{ClusterID: clusterID, QueryLimit: 10_000, QueryOffset: 0})
	if err != nil {
		return nil, err
	}
	bindings, restricted, err := h.authz.bindingsForContext(ctx)
	if err != nil {
		return nil, err
	}
	allows := func(projectID uuid.UUID, namespace string) bool {
		if !restricted {
			return true
		}
		return h.authz.engine != nil && h.authz.engine.CheckPermission(bindings, rbac.ResourceCatalog, rbac.VerbRead, clusterID, projectID, namespace)
	}
	clusterWide := allows(uuid.Nil, "")
	allowedProjects := make([]uuid.UUID, 0, len(projects))
	for _, project := range projects {
		allowed := clusterWide || allows(project.ID, "")
		if !allowed {
			if lister, ok := h.queries.(catalogProjectNamespaceLister); ok {
				namespaces, listErr := lister.ListProjectNamespaces(ctx, project.ID)
				if listErr != nil {
					return nil, listErr
				}
				for _, item := range namespaces {
					if item.ClusterID == clusterID && allows(project.ID, item.Namespace) {
						allowed = true
						break
					}
				}
			}
		}
		if allowed {
			allowedProjects = append(allowedProjects, project.ID)
		}
	}
	if !clusterWide && len(allowedProjects) == 0 {
		return nil, errCatalogClusterAccessDenied
	}
	repositories := make(map[uuid.UUID]struct{})
	globals, err := h.queries.ListGlobalHelmRepositories(ctx, sqlc.ListGlobalHelmRepositoriesParams{Limit: 10_000, Offset: 0})
	if err != nil {
		return nil, err
	}
	for _, repository := range globals {
		repositories[repository.ID] = struct{}{}
	}
	for _, projectID := range allowedProjects {
		rows, listErr := h.queries.ListCatalogsForProject(ctx, projectID)
		if listErr != nil {
			return nil, listErr
		}
		for _, repository := range rows {
			repositories[repository.ID] = struct{}{}
		}
	}
	ids := make([]uuid.UUID, 0, len(repositories))
	for id := range repositories {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(left, right uuid.UUID) int { return strings.Compare(left.String(), right.String()) })
	return ids, nil
}

func catalogVisibilityAllowsRead(visibility sqlc.CatalogVisibility) bool {
	switch visibility {
	case sqlc.CatalogVisibilityOwn, sqlc.CatalogVisibilitySubscribedPublic, sqlc.CatalogVisibilityPublic:
		return true
	default:
		return false
	}
}

func (h *CatalogHandler) authorizeChartRead(w http.ResponseWriter, r *http.Request, chart sqlc.HelmChart) bool {
	projectID, projectScoped, ok := catalogProjectQuery(w, r)
	if !ok {
		return false
	}
	clusterID, clusterScoped, ok := catalogClusterQuery(w, r)
	if !ok {
		return false
	}
	repository, err := h.queries.GetHelmRepositoryByID(r.Context(), chart.RepositoryID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Chart repository not found")
		return false
	}
	if clusterScoped && !projectScoped {
		visible, visibilityErr := h.visibleCatalogRepositoryIDs(r.Context(), clusterID)
		if visibilityErr != nil {
			if errors.Is(visibilityErr, errCatalogClusterAccessDenied) {
				RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "You do not have catalog access on this cluster")
			} else {
				RespondRequestError(w, r, http.StatusInternalServerError, apierror.ReadError, "Failed to resolve cluster catalog visibility")
			}
			return false
		}
		if !slices.Contains(visible, repository.ID) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Chart not found")
			return false
		}
		return true
	}
	if !projectScoped {
		if repository.OwnerProjectID.Valid {
			// A private chart is intentionally indistinguishable from a missing
			// chart unless the caller selects and is authorized for its project.
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Chart not found")
			return false
		}
		return h.authz.authorizeGlobalAction(w, r, rbac.ResourceCatalog, rbac.VerbRead)
	}
	if !h.authz.authorizeProjectAction(w, r, projectID, rbac.ResourceCatalog, rbac.VerbRead) {
		return false
	}
	visibility, err := h.queries.GetCatalogVisibilityForProject(r.Context(), projectID, repository.ID)
	if err != nil || !catalogVisibilityAllowsRead(visibility) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Chart not found")
		return false
	}
	return true
}

func catalogVisibleToProject(ctx context.Context, queries CatalogQuerier, projectID, repositoryID uuid.UUID) bool {
	visibility, err := queries.GetCatalogVisibilityForProject(ctx, projectID, repositoryID)
	return err == nil && catalogVisibilityAllowsRead(visibility)
}

// ListCharts handles GET /api/v1/catalog/charts/.
//
// Migration 061: when ?project_id=<uuid> is present, the visible catalog
// set is narrowed from "every helm_repositories row" to the project-scoped
// union (globals + own + subscribed). Without project_id the behaviour
// is unchanged for the admin view.
// Migration 071: also accepts ?tag= to filter on helm_chart_tags (used by
// the service-mesh tab "Install" deep-link).
func (h *CatalogHandler) ListCharts(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 20))
	offset := int32(queryOffset(r))
	tag := strings.TrimSpace(r.URL.Query().Get("tag"))
	pid, projectScoped, ok := catalogProjectQuery(w, r)
	if !ok {
		return
	}
	clusterID, clusterScoped, ok := catalogClusterQuery(w, r)
	if !ok {
		return
	}
	if projectScoped && !h.authz.authorizeProjectAction(w, r, pid, rbac.ResourceCatalog, rbac.VerbRead) {
		return
	}
	if !projectScoped && !clusterScoped && !h.authz.authorizeGlobalAction(w, r, rbac.ResourceCatalog, rbac.VerbRead) {
		return
	}

	if tag != "" {
		if projectScoped {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, "tag filtering is available only on the global catalog")
			return
		}
		charts, err := h.queries.ListHelmChartsByTag(r.Context(), sqlc.ListHelmChartsByTagParams{
			Tag:    tag,
			Limit:  limit,
			Offset: offset,
		})
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list charts by tag")
			return
		}
		total, err := h.queries.CountHelmChartsByTag(r.Context(), tag)
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count charts by tag")
			return
		}
		paging.Write(w, charts, paging.Exact(total, queryLimit(r, 20), queryOffset(r), len(charts)))
		return
	}

	if projectScoped {
		visibleCatalogs, err := h.queries.ListCatalogsForProject(r.Context(), pid)
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to resolve project catalogs")
			return
		}
		// Single IN-list query over the project's visible catalog set with
		// real LIMIT/OFFSET + COUNT. The old path fanned out a Limit:1000
		// query per catalog and sliced in Go, which silently truncated any
		// catalog holding more than 1000 charts.
		repoIDs := make([]uuid.UUID, 0, len(visibleCatalogs))
		for _, cat := range visibleCatalogs {
			repoIDs = append(repoIDs, cat.ID)
		}
		if len(repoIDs) == 0 {
			paging.Write(w, []sqlc.HelmChart{}, paging.Exact(0, queryLimit(r, 20), queryOffset(r), len([]sqlc.HelmChart{})))
			return
		}
		charts, err := h.queries.ListChartsByRepositoryIDs(r.Context(), sqlc.ListChartsByRepositoryIDsParams{
			RepositoryIds: repoIDs,
			QueryLimit:    limit,
			QueryOffset:   offset,
		})
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list project charts")
			return
		}
		total, err := h.queries.CountChartsByRepositoryIDs(r.Context(), repoIDs)
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count project charts")
			return
		}
		paging.Write(w, charts, paging.Exact(total, queryLimit(r, 20), queryOffset(r), len(charts)))
		return
	}

	if clusterScoped {
		repoIDs, visibilityErr := h.visibleCatalogRepositoryIDs(r.Context(), clusterID)
		if visibilityErr != nil {
			if errors.Is(visibilityErr, errCatalogClusterAccessDenied) {
				RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "You do not have catalog access on this cluster")
			} else {
				RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to resolve cluster catalogs")
			}
			return
		}
		if len(repoIDs) == 0 {
			paging.Write(w, []sqlc.HelmChart{}, paging.Exact(0, int(limit), int(offset), 0))
			return
		}
		charts, listErr := h.queries.ListChartsByRepositoryIDs(r.Context(), sqlc.ListChartsByRepositoryIDsParams{RepositoryIds: repoIDs, QueryLimit: limit, QueryOffset: offset})
		if listErr != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list cluster charts")
			return
		}
		total, countErr := h.queries.CountChartsByRepositoryIDs(r.Context(), repoIDs)
		if countErr != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count cluster charts")
			return
		}
		paging.Write(w, charts, paging.Exact(total, int(limit), int(offset), len(charts)))
		return
	}

	charts, err := h.queries.ListHelmCharts(r.Context(), sqlc.ListHelmChartsParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list charts")
		return
	}

	total, err := h.queries.CountHelmCharts(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count charts")
		return
	}

	paging.Write(w, charts, paging.Exact(total, queryLimit(r, 20), queryOffset(r), len(charts)))
}

// GetChart handles GET /api/v1/catalog/charts/{id}/.
func (h *CatalogHandler) GetChart(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid chart ID")
		return
	}

	chart, err := h.queries.GetHelmChartByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Chart not found")
		return
	}
	if !h.authorizeChartRead(w, r, chart) {
		return
	}
	if user := currentUserUUID(r); user.Valid {
		if store, ok := h.queries.(catalogUserDiscoveryQuerier); ok {
			_ = store.RecordCatalogChartView(r.Context(), sqlc.RecordCatalogChartViewParams{
				UserID: uuid.UUID(user.Bytes), ChartID: chart.ID,
			})
		}
	}

	RespondJSON(w, http.StatusOK, chart)
}

// ListChartVersions handles GET /api/v1/catalog/charts/{id}/versions/.
func (h *CatalogHandler) ListChartVersions(w http.ResponseWriter, r *http.Request) {
	chartID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid chart ID")
		return
	}
	chart, err := h.queries.GetHelmChartByID(r.Context(), chartID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Chart not found")
		return
	}
	if !h.authorizeChartRead(w, r, chart) {
		return
	}
	limit := queryLimit(r, 50)
	offset := queryOffset(r)
	versions, err := h.queries.ListChartVersions(r.Context(), sqlc.ListChartVersionsParams{
		ChartID: chartID,
		Limit:   int32(limit),
		Offset:  int32(offset),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list chart versions")
		return
	}
	// No exact total is available; infer has_more from a full SQL page.
	paging.Write(w, versions, paging.FromPage(limit, offset, len(versions)))
}

// GetChartReadme handles GET /api/v1/catalog/charts/{id}/readme/.
// Returns the README from the latest (or ?version=) chart version.
func (h *CatalogHandler) GetChartReadme(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid chart ID")
		return
	}
	chart, err := h.queries.GetHelmChartByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Chart not found")
		return
	}
	if !h.authorizeChartRead(w, r, chart) {
		return
	}
	version, err := h.resolveChartVersion(r, chart)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "No versions found for this chart.")
		return
	}
	// Lazy hydrate on cache miss. hydrateChartVersion self-guards on
	// content_hydrated_at, so this is cheap once hydrated.
	if hydrated, hErr := h.hydrateChartVersion(r.Context(), version); hErr == nil {
		version = hydrated
	} else if h.log != nil {
		h.log.Warn("chart readme hydration failed",
			"chart_id", chart.ID, "version_id", version.ID, "error", hErr)
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"chart":   chart.Name,
		"version": version.Version,
		"readme":  version.Readme,
	})
}

// GetChartValues handles GET /api/v1/catalog/charts/{id}/values/.
// Returns the default values + values_schema from the latest (or ?version=) chart version.
func (h *CatalogHandler) GetChartValues(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid chart ID")
		return
	}
	chart, err := h.queries.GetHelmChartByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Chart not found")
		return
	}
	if !h.authorizeChartRead(w, r, chart) {
		return
	}
	version, err := h.resolveChartVersion(r, chart)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "No versions found for this chart.")
		return
	}
	// Lazy hydrate default_values + README + values_schema on cache miss so the
	// install modal gets real defaults + form. hydrateChartVersion self-guards
	// on content_hydrated_at, so calling it unconditionally is cheap once a row
	// is hydrated and also backfills schema for rows hydrated before that column
	// existed (they have values but no schema).
	if hydrated, hErr := h.hydrateChartVersion(r.Context(), version); hErr == nil {
		version = hydrated
	} else if h.log != nil {
		h.log.Warn("chart values hydration failed",
			"chart_id", chart.ID, "version_id", version.ID, "error", hErr)
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"chart":          chart.Name,
		"version":        version.Version,
		"default_values": version.DefaultValues,
		"values_schema":  version.ValuesSchema,
	})
}

// resolveChartVersion picks a specific chart version (by ?version= query param) or the latest.
func (h *CatalogHandler) resolveChartVersion(r *http.Request, chart sqlc.HelmChart) (sqlc.HelmChartVersion, error) {
	if v := strings.TrimSpace(r.URL.Query().Get("version")); v != "" {
		versions, err := h.queries.ListChartVersions(r.Context(), sqlc.ListChartVersionsParams{
			ChartID: chart.ID,
			Limit:   200,
			Offset:  0,
		})
		if err != nil {
			return sqlc.HelmChartVersion{}, err
		}
		for _, ver := range versions {
			if ver.Version == v {
				return ver, nil
			}
		}
		return sqlc.HelmChartVersion{}, fmt.Errorf("version %q not found", v)
	}
	return h.queries.GetLatestChartVersion(r.Context(), chart.ID)
}
