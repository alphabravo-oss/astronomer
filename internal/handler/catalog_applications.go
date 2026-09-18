package handler

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	semver "github.com/Masterminds/semver/v3"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

type applicationCatalogQuerier interface {
	ListApplicationCatalogPresentations(context.Context) ([]sqlc.ListApplicationCatalogPresentationsRow, error)
	GetApplicationCatalogPresentationByChartVersion(context.Context, uuid.UUID) (sqlc.CatalogBlessedChart, error)
}

type applicationCatalogSourceQuerier interface {
	ListApplicationCatalogSources(context.Context) ([]sqlc.DeliveryCatalog, error)
}

type catalogUserDiscoveryQuerier interface {
	ListCatalogUserDiscovery(context.Context, uuid.UUID) ([]sqlc.ListCatalogUserDiscoveryRow, error)
	RecordCatalogChartView(context.Context, sqlc.RecordCatalogChartViewParams) error
	SetCatalogChartFavorite(context.Context, sqlc.SetCatalogChartFavoriteParams) (sqlc.SetCatalogChartFavoriteRow, error)
}

type catalogUpgradeVersionQuerier interface {
	ListUpgradeVersionsForInstalledChart(context.Context, uuid.UUID) ([]sqlc.HelmChartVersion, error)
}

type catalogClusterProjectResolver interface {
	ListProjectsByCluster(context.Context, sqlc.ListProjectsByClusterParams) ([]sqlc.Project, error)
	GetProjectNamespaceByClusterAndNamespace(context.Context, sqlc.GetProjectNamespaceByClusterAndNamespaceParams) (sqlc.ProjectNamespace, error)
}

type catalogProjectNamespaceLister interface {
	ListProjectNamespaces(context.Context, uuid.UUID) ([]sqlc.ProjectNamespace, error)
}

var errCatalogProjectResolverUnavailable = errors.New("project namespace resolver is unavailable")

func (h *CatalogHandler) ListCatalogApplications(w http.ResponseWriter, r *http.Request) {
	store, ok := h.queries.(applicationCatalogQuerier)
	if !ok {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Application catalog persistence is unavailable")
		return
	}
	applications, err := store.ListApplicationCatalogPresentations(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list catalog applications")
		return
	}
	RespondJSONUnwrapped(w, http.StatusOK, map[string]any{"data": applications, "count": len(applications)})
}

func (h *CatalogHandler) ListApplicationCatalogSources(w http.ResponseWriter, r *http.Request) {
	store, ok := h.queries.(applicationCatalogSourceQuerier)
	if !ok {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Application catalog source persistence is unavailable")
		return
	}
	sources, err := store.ListApplicationCatalogSources(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list application catalog sources")
		return
	}
	RespondJSONUnwrapped(w, http.StatusOK, map[string]any{"data": sources, "count": len(sources)})
}

// openapi:request Operation_postCatalogApplicationsPreview
type catalogInstallationPreviewRequest struct {
	ClusterID      string `json:"cluster_id"`
	ChartVersionID string `json:"chart_version_id"`
	Namespace      string `json:"namespace"`
	ValuesOverride string `json:"values_override"`
}

// openapi:request Operation_putCatalogChartsByIdFavorite
type catalogFavoriteRequest struct {
	Favorite bool `json:"favorite"`
}

type catalogPrerequisiteCheck struct {
	Code        string `json:"code"`
	Status      string `json:"status"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

func (h *CatalogHandler) PreviewCatalogInstallation(w http.ResponseWriter, r *http.Request) {
	var request catalogInstallationPreviewRequest
	if !decodeAndValidate(w, r, &request) {
		return
	}
	clusterID, err := uuid.Parse(request.ClusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "A valid cluster_id is required")
		return
	}
	versionID, err := uuid.Parse(request.ChartVersionID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "A valid chart_version_id is required")
		return
	}
	if strings.TrimSpace(request.Namespace) == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, "A namespace is required")
		return
	}
	projectID, _, err := h.resolveCatalogTargetProject(r.Context(), clusterID, request.Namespace)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ReadError, "Failed to resolve namespace ownership")
		return
	}
	allowedTarget, err := h.catalogCallerAllowsTarget(r.Context(), clusterID, projectID, request.Namespace, rbac.VerbCreate)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to retrieve user permissions")
		return
	}
	if !allowedTarget {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "You do not have permission to install applications in this namespace")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	version, err := h.queries.GetHelmChartVersionByID(r.Context(), versionID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Chart version not found")
		return
	}
	store, ok := h.queries.(applicationCatalogQuerier)
	if !ok {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Application catalog persistence is unavailable")
		return
	}
	presentation, err := store.GetApplicationCatalogPresentationByChartVersion(r.Context(), versionID)
	if err != nil {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Chart version is not present in the verified application catalog")
		return
	}
	checks, allowed := catalogInstallChecks(cluster, version, presentation, request.ValuesOverride)
	RespondJSON(w, http.StatusOK, map[string]any{
		"allowed": allowed, "checks": checks, "application": presentation.Slug,
		"artifact_digest": version.Digest, "values_digest": sha256Hex([]byte(request.ValuesOverride)),
		"catalog_digest": presentation.CatalogDigest,
	})
}

func catalogInstallChecks(cluster sqlc.Cluster, version sqlc.HelmChartVersion, presentation sqlc.CatalogBlessedChart, values string) ([]catalogPrerequisiteCheck, bool) {
	checks := make([]catalogPrerequisiteCheck, 0, 6)
	allowed := true
	add := func(code, status, title, description string) {
		checks = append(checks, catalogPrerequisiteCheck{Code: code, Status: status, Title: title, Description: description})
		if status == "blocking" {
			allowed = false
		}
	}
	if presentation.Revoked || (presentation.VerificationStatus != "verified" && presentation.VerificationStatus != "digest-verified") {
		add("catalog_trust", "blocking", "Catalog trust", "The catalog entry is revoked or its immutable source was not verified.")
	} else {
		add("catalog_trust", "ready", "Catalog trust", "Catalog index identity and digest are verified.")
	}
	var artifact struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(presentation.Artifact, &artifact)
	if artifact.Version == "" || artifact.Version != version.Version || !validSHA256Digest(version.Digest) {
		add("artifact_identity", "blocking", "Immutable artifact", "The selected chart version does not match the catalog pin or lacks a SHA-256 digest.")
	} else {
		add("artifact_identity", "ready", "Immutable artifact", "The selected chart version and SHA-256 digest match the catalog entry.")
	}
	var compatibility struct {
		Kubernetes string `json:"kubernetes"`
	}
	_ = json.Unmarshal(presentation.Compatibility, &compatibility)
	if compatibility.Kubernetes != "" {
		constraint, constraintErr := semver.NewConstraint(compatibility.Kubernetes)
		current, versionErr := semver.NewVersion(strings.TrimPrefix(cluster.KubernetesVersion, "v"))
		if constraintErr != nil || versionErr != nil || !constraint.Check(current) {
			add("kubernetes_version", "blocking", "Kubernetes compatibility", fmt.Sprintf("Cluster %s does not satisfy %s.", cluster.KubernetesVersion, compatibility.Kubernetes))
		} else {
			add("kubernetes_version", "ready", "Kubernetes compatibility", fmt.Sprintf("Cluster %s satisfies %s.", cluster.KubernetesVersion, compatibility.Kubernetes))
		}
	}
	if cluster.Status != "connected" && cluster.Status != "active" && cluster.Status != "ready" {
		add("cluster_connectivity", "blocking", "Cluster connectivity", "The target cluster is not currently connected.")
	} else {
		add("cluster_connectivity", "ready", "Cluster connectivity", "The target cluster is connected.")
	}
	if presentation.Privileged {
		add("cluster_access", "approval", "Cluster-scoped access", "This application creates cluster-scoped or privileged resources; review is required.")
	}
	var storage struct {
		Required    bool   `json:"required"`
		Recommended string `json:"recommended"`
	}
	_ = json.Unmarshal(presentation.Storage, &storage)
	if storage.Required {
		detail := "Persistent storage is required; Flux will confirm the target StorageClass during reconciliation."
		if storage.Recommended != "" {
			detail = "Persistent storage is required (recommended " + storage.Recommended + "); Flux will confirm the target StorageClass during reconciliation."
		}
		add("persistent_storage", "advisory", "Persistent storage", detail)
	}
	if _, err := valuesJSONForPreview(values); err != nil {
		add("values", "blocking", "Configuration", err.Error())
	} else {
		add("values", "ready", "Configuration", "Values are valid bounded YAML and contain no persisted vault placeholders.")
	}
	return checks, allowed
}

const vaultReferencePrefix = "${vault://"

func valuesJSONForPreview(raw string) (map[string]any, error) {
	if strings.Contains(raw, vaultReferencePrefix) {
		return nil, errors.New("use Kubernetes Secret references; vault placeholders cannot be persisted in a Flux bundle")
	}
	if len(raw) > 1<<20 {
		return nil, errors.New("values exceed the 1 MiB preview limit")
	}
	if strings.TrimSpace(raw) == "" {
		return map[string]any{}, nil
	}
	var values map[string]any
	if err := yaml.Unmarshal([]byte(raw), &values); err != nil {
		return nil, fmt.Errorf("values YAML is invalid: %w", err)
	}
	return values, nil
}

func validSHA256Digest(value string) bool {
	value = strings.TrimPrefix(value, "sha256:")
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return fmt.Sprintf("sha256:%x", digest)
}

func (h *CatalogHandler) ListCatalogUserDiscovery(w http.ResponseWriter, r *http.Request) {
	user := currentUserUUID(r)
	if !user.Valid {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}
	store, ok := h.queries.(catalogUserDiscoveryQuerier)
	if !ok {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Catalog discovery persistence is unavailable")
		return
	}
	rows, err := store.ListCatalogUserDiscovery(r.Context(), uuid.UUID(user.Bytes))
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list catalog discovery state")
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		item := map[string]any{"chart_id": row.ChartID.String(), "favorite": row.Favorite, "view_count": row.ViewCount, "favorite_at": nil, "last_viewed_at": nil}
		if row.FavoriteAt.Valid {
			item["favorite_at"] = row.FavoriteAt.Time.UTC().Format(time.RFC3339)
		}
		if row.LastViewedAt.Valid {
			item["last_viewed_at"] = row.LastViewedAt.Time.UTC().Format(time.RFC3339)
		}
		data = append(data, item)
	}
	RespondJSONUnwrapped(w, http.StatusOK, map[string]any{"data": data, "count": len(data)})
}

func (h *CatalogHandler) SetCatalogChartFavorite(w http.ResponseWriter, r *http.Request) {
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
	var request catalogFavoriteRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	user := currentUserUUID(r)
	if !user.Valid {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}
	store, ok := h.queries.(catalogUserDiscoveryQuerier)
	if !ok {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Catalog discovery persistence is unavailable")
		return
	}
	row, err := store.SetCatalogChartFavorite(r.Context(), sqlc.SetCatalogChartFavoriteParams{UserID: uuid.UUID(user.Bytes), ChartID: chartID, Favorite: request.Favorite})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update favorite")
		return
	}
	response := map[string]any{"chart_id": row.ChartID.String(), "favorite": row.Favorite, "favorite_at": nil, "last_viewed_at": nil, "view_count": row.ViewCount}
	if row.FavoriteAt.Valid {
		response["favorite_at"] = row.FavoriteAt.Time.UTC().Format(time.RFC3339)
	}
	if row.LastViewedAt.Valid {
		response["last_viewed_at"] = row.LastViewedAt.Time.UTC().Format(time.RFC3339)
	}
	RespondJSON(w, http.StatusOK, response)
}

func (h *CatalogHandler) ListInstalledChartUpgradeVersions(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid installed chart ID")
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
	store, ok := h.queries.(catalogUpgradeVersionQuerier)
	if !ok {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Catalog upgrade discovery is unavailable")
		return
	}
	versions, err := store.ListUpgradeVersionsForInstalledChart(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list upgrade versions")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"data": versions, "count": len(versions)})
}

func (h *CatalogHandler) catalogCallerAllowsTarget(ctx context.Context, clusterID, projectID uuid.UUID, namespace string, verb rbac.Verb) (bool, error) {
	bindings, restricted, err := h.authz.bindingsForContext(ctx)
	if err != nil {
		return false, err
	}
	if !restricted {
		return true, nil
	}
	if h.authz.engine == nil {
		return false, nil
	}
	return h.authz.engine.CheckPermission(bindings, rbac.ResourceCatalog, verb, clusterID, projectID, namespace), nil
}

func (h *CatalogHandler) resolveCatalogTargetProject(ctx context.Context, clusterID uuid.UUID, namespace string) (uuid.UUID, bool, error) {
	resolver, ok := h.queries.(catalogClusterProjectResolver)
	if !ok {
		return uuid.Nil, false, errCatalogProjectResolverUnavailable
	}
	row, err := resolver.GetProjectNamespaceByClusterAndNamespace(ctx, sqlc.GetProjectNamespaceByClusterAndNamespaceParams{ClusterID: clusterID, Namespace: namespace})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return row.ProjectID, true, nil
}
