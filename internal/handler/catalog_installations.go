package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/maintenance"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type installedChartScopedPager interface {
	ListInstalledChartsForScopes(ctx context.Context, arg sqlc.ListInstalledChartsForScopesParams) ([]sqlc.InstalledChart, error)
	CountInstalledChartsForScopes(ctx context.Context, clusterIDs []uuid.UUID) (int64, error)
}

// --- Installed Charts (Installations) ---

// ListInstallations handles GET /api/v1/clusters/{cluster_id}/installations/.
func (h *CatalogHandler) ListInstallations(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	// The per-cluster installation list (and ?cluster_id= on the fleet list,
	// which delegates here) carries values_override — gate it on the cluster's
	// own catalog:read so a caller without a grant there can't read it.
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceCatalog, rbac.VerbRead) {
		return
	}

	limit := int32(queryLimit(r, 20))
	offset := int32(queryOffset(r))

	installations, err := h.queries.ListInstalledChartsByCluster(r.Context(), sqlc.ListInstalledChartsByClusterParams{
		ClusterID: clusterID,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list installations")
		return
	}

	total, err := h.queries.CountInstalledChartsByCluster(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count installations")
		return
	}
	for index := range installations {
		installations[index] = h.refreshInstalledChartStatus(r.Context(), installations[index])
	}

	paging.Write(w, installations, paging.Exact(total, queryLimit(r, 20), queryOffset(r), len(installations)))
}

// CreateInstallationRequest represents the request body for creating an installation.
type CreateInstallationRequest struct {
	ChartVersionID string `json:"chart_version_id" validate:"required,uuid"`
	ProjectID      string `json:"project_id,omitempty"`
	ReleaseName    string `json:"release_name" validate:"required"`
	Namespace      string `json:"namespace" validate:"required"`
	ValuesOverride string `json:"values_override"`
	Notes          string `json:"notes"`
	ToolSlug       string `json:"tool_slug"`
	PresetUsed     string `json:"preset_used"`
}

// CreateInstallation handles POST /api/v1/clusters/{cluster_id}/installations/.
func (h *CatalogHandler) CreateInstallation(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	// Migration 057: maintenance window gate.
	if blocked := h.checkCatalogMaintenanceWindow(w, r, clusterID, "helm.install"); blocked {
		return
	}

	var req CreateInstallationRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}

	params := sqlc.CreateInstalledChartParams{
		ClusterID:      clusterID,
		ReleaseName:    req.ReleaseName,
		Namespace:      req.Namespace,
		ValuesOverride: req.ValuesOverride,
		Status:         "pending_install",
		Revision:       1,
		Notes:          req.Notes,
		InstalledByID:  currentUserUUID(r),
	}

	var version sqlc.HelmChartVersion
	var chart sqlc.HelmChart
	var repo sqlc.HelmRepository
	var targetProjectID uuid.UUID
	var targetHasProject bool
	var err error
	if req.ChartVersionID != "" {
		targetProjectID, targetHasProject, err = h.resolveCatalogTargetProject(r.Context(), clusterID, req.Namespace)
		resolverUnavailable := errors.Is(err, errCatalogProjectResolverUnavailable)
		if (resolverUnavailable || (!targetHasProject && err == nil)) && req.ProjectID != "" {
			legacyProjectID, parseErr := uuid.Parse(req.ProjectID)
			if parseErr == nil {
				project, projectErr := h.queries.GetProjectByID(r.Context(), legacyProjectID)
				if projectErr == nil && project.ClusterID == clusterID {
					namespaceOwned := resolverUnavailable
					if lister, ok := h.queries.(catalogProjectNamespaceLister); ok {
						rows, listErr := lister.ListProjectNamespaces(r.Context(), legacyProjectID)
						if listErr == nil {
							for _, row := range rows {
								if row.ClusterID == clusterID && row.Namespace == req.Namespace {
									namespaceOwned = true
									break
								}
							}
						}
					}
					if namespaceOwned {
						targetProjectID, targetHasProject, err = legacyProjectID, true, nil
					}
				}
			}
		}
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ReadError, "Failed to resolve namespace ownership")
			return
		}
		if req.ProjectID != "" {
			legacyProjectID, parseErr := uuid.Parse(req.ProjectID)
			if parseErr != nil || !targetHasProject || legacyProjectID != targetProjectID {
				RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "project_id does not match the target namespace")
				return
			}
		}
		allowedTarget, authErr := h.catalogCallerAllowsTarget(r.Context(), clusterID, targetProjectID, req.Namespace, rbac.VerbCreate)
		if authErr != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to retrieve user permissions")
			return
		}
		if !allowedTarget {
			RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "You do not have permission to install applications in this namespace")
			return
		}
		if targetHasProject {
			params.ProjectID = pgtype.UUID{Bytes: targetProjectID, Valid: true}
		}
		cvID, err := uuid.Parse(req.ChartVersionID)
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid chart version ID")
			return
		}
		params.ChartVersionID = pgtype.UUID{Bytes: cvID, Valid: true}
		version, err = h.queries.GetHelmChartVersionByID(r.Context(), cvID)
		if err != nil {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Chart version not found")
			return
		}
		chart, err = h.queries.GetHelmChartByID(r.Context(), version.ChartID)
		if err != nil {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Chart not found")
			return
		}
		repo, err = h.queries.GetHelmRepositoryByID(r.Context(), chart.RepositoryID)
		if err != nil {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Repository not found")
			return
		}
		if repo.OwnerProjectID.Valid && (!targetHasProject || !catalogVisibleToProject(r.Context(), h.queries, targetProjectID, repo.ID)) {
			RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Chart repository is not visible from the target namespace")
			return
		}
		if catalogStore, ok := h.queries.(applicationCatalogQuerier); ok {
			presentation, presentationErr := catalogStore.GetApplicationCatalogPresentationByChartVersion(r.Context(), cvID)
			if presentationErr == nil {
				cluster, clusterErr := h.queries.GetClusterByID(r.Context(), clusterID)
				if clusterErr != nil {
					RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
					return
				}
				checks, allowed := catalogInstallChecks(cluster, version, presentation, req.ValuesOverride)
				if !allowed {
					RespondJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{"code": apierror.Conflict, "message": "Catalog installation prerequisites failed", "checks": checks}})
					return
				}
			} else if !errors.Is(presentationErr, pgx.ErrNoRows) {
				RespondRequestError(w, r, http.StatusInternalServerError, apierror.ReadError, "Failed to verify catalog application trust")
				return
			}
		}
	}

	if req.ToolSlug != "" {
		params.ToolSlug = pgtype.Text{String: req.ToolSlug, Valid: true}
	}
	if req.PresetUsed != "" {
		params.PresetUsed = pgtype.Text{String: req.PresetUsed, Valid: true}
	}
	resolvedProjectID := ""
	if targetHasProject {
		resolvedProjectID = targetProjectID.String()
	}
	if resolvedProjectID == "" && h.delivery != nil {
		projectID, scopeErr := h.platformCatalogProject(r.Context(), clusterID)
		if scopeErr != nil {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, scopeErr.Error())
			return
		}
		resolvedProjectID = projectID.String()
	}

	// Migration 067 — the values blob keeps its ${vault://...} markers in
	// both the installed_charts row AND the enqueued operation payload.
	// Resolution happens at execution time inside the reconciler
	// (sendHelm), so the resolved plaintext secret is never persisted to
	// catalog_operations.payload — it only ever exists in-memory on the
	// wire to the cluster. Catalog installs are cluster-scoped and not
	// tied to a single project, so unqualified references (no <connection>
	// segment) require the operator to use the explicit
	// "${vault://<connection>/...}" form; the reconciler fails the
	// operation clearly when a reference is unresolvable.
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	opCtx := withOperationIdempotency(r, "catalog")
	result, err := executeMutation(r, h.runTx,
		func(q CatalogMutationTx) (catalogMutationResult[sqlc.InstalledChart], error) {
			installation, mutationErr := q.CreateInstalledChart(r.Context(), params)
			if mutationErr != nil {
				return catalogMutationResult[sqlc.InstalledChart]{}, mutationErr
			}
			op, mutationErr := createCatalogOperation(opCtx, q, "installed_chart", installation.ID.String(), "install", catalogOperationEnvelope{
				InstalledChartID: installation.ID.String(), ClusterID: clusterID.String(), ReleaseName: installation.ReleaseName,
				ProjectID: resolvedProjectID, Namespace: installation.Namespace, ChartVersionID: req.ChartVersionID, ChartName: chart.Name, RepoURL: repo.Url,
				Version: version.Version, ChartDigest: version.Digest, ValuesOverride: installation.ValuesOverride, Notes: installation.Notes,
			}, currentUserUUID(r))
			return catalogMutationResult[sqlc.InstalledChart]{row: installation, op: op}, mutationErr
		},
		func(m catalogMutationResult[sqlc.InstalledChart]) mutationAuditEvent {
			return mutationAuditEvent{action: "catalog.installation.create", resourceType: "installed_chart", resourceID: m.row.ID.String(), resourceName: m.row.ReleaseName, status: http.StatusAccepted, detail: map[string]any{
				"cluster_id": m.row.ClusterID.String(), "namespace": m.row.Namespace, "chart_version_id": req.ChartVersionID,
				"chart_name": chart.Name, "repository_id": repo.ID.String(), "version": version.Version, "operation_id": m.op.ID.String(),
			}}
		})
	if err != nil {
		respondCatalogMutationError(w, r, err, apierror.EnqueueError, "Failed to create and enqueue installation")
		return
	}
	installation, op := result.row, result.op
	h.TriggerReconcile()
	h.publishCatalogReleaseChanged(clusterID.String(), installation.ID.String())
	RespondAcceptedOperation(w, "/api/v1/catalog/operations/"+op.ID.String()+"/", map[string]any{
		"installation": installation,
		"operation":    catalogOperationResponse(op),
	})
}

// DeleteInstalledChart handles DELETE /api/v1/catalog/installed/{id}/.
func (h *CatalogHandler) DeleteInstalledChart(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid installation ID")
		return
	}
	installation, err := h.queries.GetInstalledChartByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Installation not found")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, installation.ClusterID, rbac.ResourceCatalog, rbac.VerbDelete) {
		return
	}
	// Migration 057: maintenance window gate.
	if blocked := h.checkCatalogMaintenanceWindow(w, r, installation.ClusterID, "helm.uninstall"); blocked {
		return
	}
	statusParams := sqlc.UpdateInstalledChartStatusParams{
		ID:       installation.ID,
		Status:   "pending_uninstall",
		Revision: installation.Revision,
	}
	envelope := catalogOperationEnvelope{InstalledChartID: installation.ID.String(), ClusterID: installation.ClusterID.String(), ReleaseName: installation.ReleaseName, Namespace: installation.Namespace}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	opCtx := withOperationIdempotency(r, "catalog")
	result, err := executeMutation(r, h.runTx,
		func(q CatalogMutationTx) (catalogMutationResult[sqlc.InstalledChart], error) {
			if mutationErr := q.UpdateInstalledChartStatus(r.Context(), statusParams); mutationErr != nil {
				return catalogMutationResult[sqlc.InstalledChart]{}, mutationErr
			}
			op, mutationErr := createCatalogOperation(opCtx, q, "installed_chart", installation.ID.String(), "uninstall", envelope, currentUserUUID(r))
			return catalogMutationResult[sqlc.InstalledChart]{row: installation, op: op}, mutationErr
		},
		func(m catalogMutationResult[sqlc.InstalledChart]) mutationAuditEvent {
			return mutationAuditEvent{action: "catalog.installation.delete", resourceType: "installed_chart", resourceID: m.row.ID.String(), resourceName: m.row.ReleaseName, status: http.StatusAccepted, detail: map[string]any{
				"cluster_id": m.row.ClusterID.String(), "namespace": m.row.Namespace, "operation_id": m.op.ID.String(),
			}}
		})
	if err != nil {
		respondCatalogMutationError(w, r, err, apierror.EnqueueError, "Failed to stage and enqueue uninstall")
		return
	}
	op := result.op
	h.TriggerReconcile()
	h.publishCatalogReleaseChanged(installation.ClusterID.String(), installation.ID.String())
	RespondAcceptedOperation(w, "/api/v1/catalog/operations/"+op.ID.String()+"/", catalogOperationResponse(op))
}

// ListInstalledCharts handles GET /api/v1/catalog/installed/.
func (h *CatalogHandler) ListInstalledCharts(w http.ResponseWriter, r *http.Request) {
	clusterIDStr := r.URL.Query().Get("cluster_id")
	if clusterIDStr != "" {
		ctx := chi.NewRouteContext()
		ctx.URLParams.Add("cluster_id", clusterIDStr)
		h.ListInstallations(w, r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, ctx)))
		return
	}

	// Resolve the authorized cluster set before the database page boundary.
	// The list projection never exposes values_override; that secret-bearing
	// field remains available only through the separately gated detail route.
	all, clusterIDs, _, err := h.authz.authorizedScopeIDs(r.Context(), rbac.ResourceCatalog, rbac.VerbRead, rbac.NarrowedClustersWiden)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.Forbidden, "Failed to retrieve user permissions")
		return
	}
	limit := queryLimit(r, 20)
	offset := queryOffset(r)
	var rows []sqlc.InstalledChart
	var total int64
	if all {
		rows, err = h.queries.ListInstalledCharts(r.Context(), sqlc.ListInstalledChartsParams{Limit: int32(limit), Offset: int32(offset)})
		if err == nil {
			total, err = h.queries.CountInstalledCharts(r.Context())
		}
	} else {
		pager, ok := h.queries.(installedChartScopedPager)
		if !ok {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Scoped installed-chart pagination is unavailable")
			return
		}
		rows, err = pager.ListInstalledChartsForScopes(r.Context(), sqlc.ListInstalledChartsForScopesParams{
			ClusterIds: clusterIDs, QueryLimit: int32(limit), QueryOffset: int32(offset),
		})
		if err == nil {
			total, err = pager.CountInstalledChartsForScopes(r.Context(), clusterIDs)
		}
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list installed charts")
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, ic := range rows {
		ic = h.refreshInstalledChartStatus(r.Context(), ic)
		items = append(items, installedChartListItem(ic))
	}
	paging.Write(w, items, paging.Exact(total, limit, offset, len(rows)))
}

// refreshInstalledChartStatus projects the authoritative Flux deployment
// phase onto the compatibility record consumed by the Apps UI. The durable
// Delivery records remain the source of truth.
func (h *CatalogHandler) refreshInstalledChartStatus(ctx context.Context, installed sqlc.InstalledChart) sqlc.InstalledChart {
	if h == nil || h.delivery == nil || !installed.RequestID.Valid {
		return installed
	}
	status, err := h.delivery.Status(ctx, uuid.UUID(installed.RequestID.Bytes))
	if err != nil {
		return installed
	}
	next := installed.Status
	switch status.Phase {
	case "pending", "blocked", "applying":
		switch installed.Status {
		case "rolling_back", "pending_rollback":
			next = "rolling_back"
		case "upgrading", "pending_upgrade":
			next = "upgrading"
		default:
			next = "installing"
		}
	case "ready":
		next = "installed"
	case "degraded":
		next = "degraded"
	case "failed", "timed_out", "rollback_failed":
		next = "failed_install"
	case "suspended":
		next = "suspended"
	case "deleting":
		next = "uninstalling"
	case "removed":
		next = "uninstalled"
	}
	if next == installed.Status {
		return installed
	}
	if next == "uninstalled" {
		if err := h.queries.DeleteInstalledChart(ctx, installed.ID); err == nil {
			installed.Status = next
		}
		return installed
	}
	if err := h.queries.UpdateInstalledChartStatus(ctx, sqlc.UpdateInstalledChartStatusParams{
		ID: installed.ID, Status: next, Revision: installed.Revision,
	}); err == nil {
		installed.Status = next
	}
	return installed
}

// CreateInstalledChart handles POST /api/v1/catalog/installed/.
func (h *CatalogHandler) CreateInstalledChart(w http.ResponseWriter, r *http.Request) {
	// openapi:request-operation postCatalogInstalled
	var req struct {
		ClusterID string `json:"cluster_id"`
		CreateInstallationRequest
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	clusterID, err := uuid.Parse(req.ClusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	// Gate before adapting to the legacy path-based handler. The adapter body
	// intentionally omits cluster_id, which would make a deferred replay of the
	// public /catalog/installed/ endpoint incomplete.
	fullBody, err := json.Marshal(req)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(fullBody))
	if h.checkCatalogMaintenanceWindow(w, r, clusterID, maintenance.OpHelmInstall) {
		return
	}
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("cluster_id", req.ClusterID)
	body, _ := json.Marshal(req.CreateInstallationRequest)
	r.Body = io.NopCloser(bytes.NewReader(body))
	h.CreateInstallation(w, r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, ctx)))
}

// UpgradeInstalledChart handles PUT /api/v1/catalog/installed/{id}/upgrade/.
func (h *CatalogHandler) UpgradeInstalledChart(w http.ResponseWriter, r *http.Request) {
	// Defensive no-op for an unwired store: production always injects queries,
	// but the route-security tests reach here with a nil querier once a caller
	// clears the scope/RBAC gate. Answer 500 rather than dereferencing nil.
	if h == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Catalog store not configured")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid installed chart ID")
		return
	}
	// openapi:request-operation putCatalogInstalledByIdUpgrade
	var req struct {
		ChartVersionID string  `json:"chart_version_id"`
		ValuesOverride *string `json:"values_override"`
	}
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	installed, err := h.queries.GetInstalledChartByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Installed chart not found")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, installed.ClusterID, rbac.ResourceCatalog, rbac.VerbUpdate) {
		return
	}
	version, chart, repo, err := h.resolveInstalledChartRelease(r.Context(), installed)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ResolveError, "Failed to resolve installed chart release")
		return
	}
	targetVersionID := installed.ChartVersionID
	valuesOverride := installed.ValuesOverride
	if req.ValuesOverride != nil {
		valuesOverride = *req.ValuesOverride
	}
	if req.ChartVersionID != "" {
		requestedID, parseErr := uuid.Parse(req.ChartVersionID)
		if parseErr != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "chart_version_id must be a UUID")
			return
		}
		requestedVersion, lookupErr := h.queries.GetHelmChartVersionByID(r.Context(), requestedID)
		if lookupErr != nil {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Chart version not found")
			return
		}
		if requestedVersion.ChartID != chart.ID {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "chart_version_id must belong to the installed chart")
			return
		}
		version = requestedVersion
		targetVersionID = pgtype.UUID{Bytes: requestedID, Valid: true}
	}
	updateParams := sqlc.UpdateInstalledChartValuesParams{
		ID:             id,
		ChartVersionID: targetVersionID,
		ValuesOverride: valuesOverride,
		Status:         "pending_upgrade",
		Revision:       installed.Revision,
	}
	// The values blob keeps its ${vault://...} markers here and in the
	// persisted installed_charts row; the reconciler (sendHelm) resolves
	// them in-memory at execution time. Without that, the upgrade path
	// previously shipped the literal placeholder straight to Helm.
	envelope := catalogOperationEnvelope{
		InstalledChartID: installed.ID.String(),
		ClusterID:        installed.ClusterID.String(),
		ReleaseName:      installed.ReleaseName,
		Namespace:        installed.Namespace,
		ChartVersionID:   uuidFromPg(targetVersionID),
		ChartName:        chart.Name,
		RepoURL:          repo.Url,
		Version:          version.Version,
		ChartDigest:      version.Digest,
		ValuesOverride:   valuesOverride,
		Notes:            installed.Notes,
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	opCtx := withOperationIdempotency(r, "catalog")
	result, err := executeMutation(r, h.runTx,
		func(q CatalogMutationTx) (catalogMutationResult[sqlc.InstalledChart], error) {
			updated, mutationErr := q.UpdateInstalledChartValues(r.Context(), updateParams)
			if mutationErr != nil {
				return catalogMutationResult[sqlc.InstalledChart]{}, mutationErr
			}
			op, mutationErr := createCatalogOperation(opCtx, q, "installed_chart", installed.ID.String(), "upgrade", envelope, currentUserUUID(r))
			return catalogMutationResult[sqlc.InstalledChart]{row: updated, op: op}, mutationErr
		},
		func(m catalogMutationResult[sqlc.InstalledChart]) mutationAuditEvent {
			return mutationAuditEvent{action: "catalog.installation.upgrade", resourceType: "installed_chart", resourceID: installed.ID.String(), resourceName: installed.ReleaseName, status: http.StatusAccepted, detail: map[string]any{
				"cluster_id": installed.ClusterID.String(), "namespace": installed.Namespace, "chart_name": chart.Name,
				"repository_id": repo.ID.String(), "version": version.Version, "operation_id": m.op.ID.String(),
			}}
		})
	if err != nil {
		respondCatalogMutationError(w, r, err, apierror.EnqueueError, "Failed to stage and enqueue installed chart upgrade")
		return
	}
	updated, op := result.row, result.op
	h.TriggerReconcile()
	h.publishCatalogReleaseChanged(installed.ClusterID.String(), installed.ID.String())
	RespondAcceptedOperation(w, "/api/v1/catalog/operations/"+op.ID.String()+"/", map[string]any{
		"installation": updated,
		"operation":    catalogOperationResponse(op),
	})
}

// RollbackInstalledChart handles POST /api/v1/catalog/installed/{id}/rollback/.
func (h *CatalogHandler) RollbackInstalledChart(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid installed chart ID")
		return
	}
	current, err := h.queries.GetInstalledChartByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Installed chart not found")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, current.ClusterID, rbac.ResourceCatalog, rbac.VerbUpdate) {
		return
	}
	// Optional body: an explicit target revision lets operators roll back to
	// ANY prior revision (parity with `helm rollback <name> <revision>`), not
	// just the immediately preceding one. An empty body — or a non-positive
	// revision — falls back to the previous revision.
	// openapi:request-operation postCatalogInstalledByIdRollback
	var req struct {
		Revision int `json:"revision,omitempty"`
	}
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	targetRevision := int(max(current.Revision-1, 1))
	if req.Revision > 0 {
		targetRevision = req.Revision
	}
	if targetRevision >= int(current.Revision) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "revision must be lower than the current revision")
		return
	}
	statusParams := sqlc.UpdateInstalledChartStatusParams{
		ID:       id,
		Status:   "pending_rollback",
		Revision: current.Revision,
	}
	envelope := catalogOperationEnvelope{
		InstalledChartID: current.ID.String(),
		ClusterID:        current.ClusterID.String(),
		ReleaseName:      current.ReleaseName,
		Namespace:        current.Namespace,
		RollbackRevision: targetRevision,
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	opCtx := withOperationIdempotency(r, "catalog")
	result, err := executeMutation(r, h.runTx,
		func(q CatalogMutationTx) (catalogMutationResult[sqlc.InstalledChart], error) {
			if mutationErr := q.UpdateInstalledChartStatus(r.Context(), statusParams); mutationErr != nil {
				return catalogMutationResult[sqlc.InstalledChart]{}, mutationErr
			}
			op, mutationErr := createCatalogOperation(opCtx, q, "installed_chart", current.ID.String(), "rollback", envelope, currentUserUUID(r))
			return catalogMutationResult[sqlc.InstalledChart]{row: current, op: op}, mutationErr
		},
		func(m catalogMutationResult[sqlc.InstalledChart]) mutationAuditEvent {
			return mutationAuditEvent{action: "catalog.installation.rollback", resourceType: "installed_chart", resourceID: current.ID.String(), resourceName: current.ReleaseName, status: http.StatusAccepted, detail: map[string]any{
				"cluster_id": current.ClusterID.String(), "namespace": current.Namespace,
				"rollback_revision": targetRevision, "operation_id": m.op.ID.String(),
			}}
		})
	if err != nil {
		respondCatalogMutationError(w, r, err, apierror.EnqueueError, "Failed to stage and enqueue rollback")
		return
	}
	op := result.op
	h.TriggerReconcile()
	h.publishCatalogReleaseChanged(current.ClusterID.String(), current.ID.String())
	RespondAcceptedOperation(w, "/api/v1/catalog/operations/"+op.ID.String()+"/", catalogOperationResponse(op))
}

// ListInstalledChartRevisions handles GET /api/v1/catalog/installed/{id}/revisions/.
// DIR-12: returns helm release history via the agent tunnel.
func (h *CatalogHandler) ListInstalledChartRevisions(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid release ID")
		return
	}
	installed, err := h.queries.GetInstalledChartByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Installed chart not found")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, installed.ClusterID, rbac.ResourceCatalog, rbac.VerbRead) {
		return
	}
	if h.helm == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Helm requester not configured")
		return
	}
	result, err := h.helm.History(r.Context(), installed.ClusterID.String(), installed.ReleaseName, installed.Namespace)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.ProxyError, err.Error())
		return
	}
	revs := result.Revisions
	if revs == nil {
		revs = []protocol.HelmRevision{}
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"release_name": installed.ReleaseName,
		"namespace":    installed.Namespace,
		"revisions":    revs,
	})
}

// GetInstalledChartValues handles GET /api/v1/catalog/installed/{id}/values/.
// Returns the values_override stored on the release.
func (h *CatalogHandler) GetInstalledChartValues(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid release ID")
		return
	}
	installed, err := h.queries.GetInstalledChartByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Installed chart not found")
		return
	}
	// values_override routinely carries secrets (DB passwords, API keys).
	// Gate on the release's own cluster like the sibling upgrade/delete
	// handlers so a caller without catalog:read on that cluster gets a 403
	// instead of a fleet-wide values leak.
	if !h.authz.authorizeClusterAction(w, r, installed.ClusterID, rbac.ResourceCatalog, rbac.VerbRead) {
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"release_name":    installed.ReleaseName,
		"namespace":       installed.Namespace,
		"values_override": installed.ValuesOverride,
	})
}
