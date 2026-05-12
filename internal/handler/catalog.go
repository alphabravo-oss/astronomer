package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// CatalogQuerier abstracts the catalog-related database queries needed by CatalogHandler.
type CatalogQuerier interface {
	// Cluster lookup — used by the migration-057 maintenance gate to
	// resolve the target cluster's labels for selector matching.
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	// Repositories
	GetHelmRepositoryByID(ctx context.Context, id uuid.UUID) (sqlc.HelmRepository, error)
	ListHelmRepositories(ctx context.Context, arg sqlc.ListHelmRepositoriesParams) ([]sqlc.HelmRepository, error)
	CreateHelmRepository(ctx context.Context, arg sqlc.CreateHelmRepositoryParams) (sqlc.HelmRepository, error)
	UpdateHelmRepository(ctx context.Context, arg sqlc.UpdateHelmRepositoryParams) (sqlc.HelmRepository, error)
	DeleteHelmRepository(ctx context.Context, id uuid.UUID) error
	CountHelmRepositories(ctx context.Context) (int64, error)
	// Charts
	ListHelmCharts(ctx context.Context, arg sqlc.ListHelmChartsParams) ([]sqlc.HelmChart, error)
	ListChartVersions(ctx context.Context, arg sqlc.ListChartVersionsParams) ([]sqlc.HelmChartVersion, error)
	ListChartsByRepository(ctx context.Context, arg sqlc.ListChartsByRepositoryParams) ([]sqlc.HelmChart, error)
	GetHelmChartByID(ctx context.Context, id uuid.UUID) (sqlc.HelmChart, error)
	GetHelmChartByRepoAndName(ctx context.Context, arg sqlc.GetHelmChartByRepoAndNameParams) (sqlc.HelmChart, error)
	CreateHelmChart(ctx context.Context, arg sqlc.CreateHelmChartParams) (sqlc.HelmChart, error)
	CountHelmCharts(ctx context.Context) (int64, error)
	// Chart Versions
	GetHelmChartVersionByID(ctx context.Context, id uuid.UUID) (sqlc.HelmChartVersion, error)
	GetLatestChartVersion(ctx context.Context, chartID uuid.UUID) (sqlc.HelmChartVersion, error)
	GetHelmChartVersion(ctx context.Context, arg sqlc.GetHelmChartVersionParams) (sqlc.HelmChartVersion, error)
	CreateHelmChartVersion(ctx context.Context, arg sqlc.CreateHelmChartVersionParams) (sqlc.HelmChartVersion, error)
	// Installed Charts
	ListInstalledCharts(ctx context.Context, arg sqlc.ListInstalledChartsParams) ([]sqlc.InstalledChart, error)
	ListInstalledChartsByCluster(ctx context.Context, arg sqlc.ListInstalledChartsByClusterParams) ([]sqlc.InstalledChart, error)
	GetInstalledChartByID(ctx context.Context, id uuid.UUID) (sqlc.InstalledChart, error)
	CreateInstalledChart(ctx context.Context, arg sqlc.CreateInstalledChartParams) (sqlc.InstalledChart, error)
	UpdateHelmRepositoryLastSynced(ctx context.Context, id uuid.UUID) error
	UpdateInstalledChartStatus(ctx context.Context, arg sqlc.UpdateInstalledChartStatusParams) error
	UpdateInstalledChartValues(ctx context.Context, arg sqlc.UpdateInstalledChartValuesParams) (sqlc.InstalledChart, error)
	DeleteInstalledChart(ctx context.Context, id uuid.UUID) error
	CountInstalledCharts(ctx context.Context) (int64, error)
	CountInstalledChartsByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
	CreateCatalogOperation(ctx context.Context, arg sqlc.CreateCatalogOperationParams) (sqlc.CatalogOperation, error)
	GetCatalogOperation(ctx context.Context, id uuid.UUID) (sqlc.CatalogOperation, error)
	ListCatalogOperations(ctx context.Context, arg sqlc.ListCatalogOperationsParams) ([]sqlc.CatalogOperation, error)
	ListPendingCatalogOperations(ctx context.Context, limit int32) ([]sqlc.CatalogOperation, error)
	MarkCatalogOperationRunning(ctx context.Context, id uuid.UUID) (sqlc.CatalogOperation, error)
	MarkCatalogOperationCompleted(ctx context.Context, id uuid.UUID) (sqlc.CatalogOperation, error)
	MarkCatalogOperationFailed(ctx context.Context, arg sqlc.MarkCatalogOperationFailedParams) (sqlc.CatalogOperation, error)
	MarkCatalogOperationSuperseded(ctx context.Context, arg sqlc.MarkCatalogOperationSupersededParams) (sqlc.CatalogOperation, error)
	RequeueCatalogOperation(ctx context.Context, id uuid.UUID) (sqlc.CatalogOperation, error)
	CreateCatalogOperationEvent(ctx context.Context, arg sqlc.CreateCatalogOperationEventParams) (sqlc.CatalogOperationEvent, error)
	ListCatalogOperationEvents(ctx context.Context, operationID uuid.UUID) ([]sqlc.CatalogOperationEvent, error)
	// Migration 061 — project-scoped catalog browse + admin all-rows view.
	// Optional on the interface: callers that don't use the project_id
	// query param never reach these methods, so tests that omit them
	// still satisfy the interface as long as they embed *sqlc.Queries.
	ListCatalogsForProject(ctx context.Context, projectID uuid.UUID) ([]sqlc.HelmRepositoryWithOwner, error)
	ListAdminCatalogsIncludingProjectOwned(ctx context.Context, arg sqlc.ListAdminCatalogsIncludingProjectOwnedParams) ([]sqlc.HelmRepositoryWithOwner, error)
	GetHelmRepositoryWithOwner(ctx context.Context, id uuid.UUID) (sqlc.HelmRepositoryWithOwner, error)
}

// CatalogHandler handles catalog endpoints (helm repositories, charts, installations).
type CatalogHandler struct {
	queries CatalogQuerier
	helm    HelmRequester
	log     *slog.Logger
	authz   authorizationSupport
	mu      sync.Mutex
	trigger chan struct{}
	// helmConcurrency caps the number of executeOperation goroutines
	// dispatched per reconciler tick. Zero falls back to the package
	// default (see effectiveHelmConcurrency).
	helmConcurrency int
	// maintenanceGate is the migration-057 hook on helm.{install,
	// uninstall}. Optional + nil-safe.
	maintenanceGate *MaintenanceGate
}

// SetMaintenanceGate wires the migration-057 gate that refuses or
// defers helm install / uninstall during an active window.
func (h *CatalogHandler) SetMaintenanceGate(g *MaintenanceGate) {
	if h == nil {
		return
	}
	h.maintenanceGate = g
}

// NewCatalogHandler creates a new catalog handler.
func NewCatalogHandler(queries CatalogQuerier) *CatalogHandler {
	return &CatalogHandler{
		queries: queries,
		log:     slog.Default(),
		trigger: make(chan struct{}, 1),
	}
}

func NewCatalogHandlerWithHelm(queries CatalogQuerier, helm HelmRequester) *CatalogHandler {
	return &CatalogHandler{
		queries: queries,
		helm:    helm,
		log:     slog.Default(),
		trigger: make(chan struct{}, 1),
	}
}

func (h *CatalogHandler) SetLogger(log *slog.Logger) {
	if log != nil {
		h.log = log
	}
}

func (h *CatalogHandler) SetAuthorization(engine *rbac.Engine, querier middleware.RBACQuerier) {
	h.authz.SetAuthorization(engine, querier)
}

func (h *CatalogHandler) StartReconciler(ctx context.Context) {
	if h == nil || h.queries == nil {
		return
	}
	go h.runReconciler(ctx)
}

func (h *CatalogHandler) TriggerReconcile() {
	if h == nil || h.trigger == nil {
		return
	}
	select {
	case h.trigger <- struct{}{}:
	default:
	}
}

func (h *CatalogHandler) runReconciler(ctx context.Context) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	h.processPendingOperations(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.processPendingOperations(ctx)
		case <-h.trigger:
			h.processPendingOperations(ctx)
		}
	}
}

// --- Helm Repositories ---

// ListRepos handles GET /api/v1/catalog/repositories/.
//
// Default behavior (admin view, no query params): excludes project-owned
// (private) catalogs — operators expect /admin/ to show only the
// operator-curated global set. Migration 061 added two new query params:
//
//   - ?include_project_owned=true → admin sees every helm_repositories row
//     including private ones (used by the superuser "all catalogs"
//     screen).
//   - ?project_id=<uuid> → switches to project-scoped browse (globals +
//     own + subscribed for that project).
//
// The two params are mutually exclusive: project_id always wins. If
// neither is set, the legacy "global list" behaviour is preserved
// verbatim — no semantic change for existing callers.
func (h *CatalogHandler) ListRepos(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	if pidRaw := r.URL.Query().Get("project_id"); pidRaw != "" {
		pid, err := uuid.Parse(pidRaw)
		if err != nil {
			RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid project_id query param")
			return
		}
		rows, err := h.queries.ListCatalogsForProject(r.Context(), pid)
		if err != nil {
			RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list project catalogs")
			return
		}
		RespondJSON(w, http.StatusOK, rows)
		return
	}

	if r.URL.Query().Get("include_project_owned") == "true" {
		rows, err := h.queries.ListAdminCatalogsIncludingProjectOwned(r.Context(), sqlc.ListAdminCatalogsIncludingProjectOwnedParams{
			Limit:  limit,
			Offset: offset,
		})
		if err != nil {
			RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list catalogs")
			return
		}
		total, err := h.queries.CountHelmRepositories(r.Context())
		if err != nil {
			RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count repositories")
			return
		}
		RespondPaginated(w, r, rows, total)
		return
	}

	// Legacy admin path — explicit column list in catalog.sql.go means
	// the existing query never sees owner_project_id, but we still want
	// the admin default view to hide private catalogs. Filter in-Go:
	// the row count for a typical install is modest enough that this
	// doesn't warrant another sqlc query just for the admin default.
	repos, err := h.queries.ListHelmRepositories(r.Context(), sqlc.ListHelmRepositoriesParams{
		Limit:  limit + offset + 50, // small over-fetch slack for the filter
		Offset: 0,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list repositories")
		return
	}

	// Project-owned rows are filtered out by cross-checking
	// owner_project_id via GetHelmRepositoryWithOwner. We accept the
	// per-row lookup for the admin path because the default page size
	// is 20 and the index on (id) makes each lookup O(log n).
	visible := make([]sqlc.HelmRepository, 0, len(repos))
	for _, repo := range repos {
		row, err := h.queries.GetHelmRepositoryWithOwner(r.Context(), repo.ID)
		if err != nil {
			// Fall back to inclusive behaviour on lookup error so a
			// transient DB hiccup never masks operator-visible rows.
			visible = append(visible, repo)
			continue
		}
		if row.OwnerProjectID.Valid {
			continue
		}
		visible = append(visible, repo)
	}

	// Slice the in-memory page after filtering.
	if int(offset) > len(visible) {
		visible = nil
	} else {
		end := int(offset) + int(limit)
		if end > len(visible) {
			end = len(visible)
		}
		visible = visible[offset:end]
	}

	total, err := h.queries.CountHelmRepositories(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count repositories")
		return
	}

	RespondPaginated(w, r, visible, total)
}

// CreateRepoRequest represents the request body for creating a helm repository.
type CreateRepoRequest struct {
	Name        string          `json:"name"`
	URL         string          `json:"url"`
	RepoType    string          `json:"repo_type"`
	Description string          `json:"description"`
	IsDefault   bool            `json:"is_default"`
	AuthType    string          `json:"auth_type"`
	AuthConfig  json.RawMessage `json:"auth_config"`
	Enabled     bool            `json:"enabled"`
}

// CreateRepo handles POST /api/v1/catalog/repositories/.
func (h *CatalogHandler) CreateRepo(w http.ResponseWriter, r *http.Request) {
	var req CreateRepoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	if req.Name == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Repository name is required")
		return
	}
	if req.URL == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Repository URL is required")
		return
	}

	if req.AuthConfig == nil {
		req.AuthConfig = json.RawMessage(`{}`)
	}

	// Auto-detect OCI URLs so the UI can render the correct icon and the
	// reconciler can dispatch to the OCI ingest path even when the operator
	// forgets to pass repo_type explicitly.
	if req.RepoType == "" && IsOCIRepo(req.URL) {
		req.RepoType = "oci"
	}

	repo, err := h.queries.CreateHelmRepository(r.Context(), sqlc.CreateHelmRepositoryParams{
		Name:        req.Name,
		Url:         req.URL,
		RepoType:    req.RepoType,
		Description: req.Description,
		IsDefault:   req.IsDefault,
		AuthType:    req.AuthType,
		AuthConfig:  req.AuthConfig,
		Enabled:     req.Enabled,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create repository")
		return
	}

	recordAudit(r, h.queries, "catalog.repo.create", "helm_repository", repo.ID.String(), repo.Name, map[string]any{
		"url":       repo.Url,
		"repo_type": repo.RepoType,
		"auth_type": repo.AuthType,
	})

	RespondJSON(w, http.StatusCreated, repo)
}

// GetRepo handles GET /api/v1/catalog/repositories/{id}/.
func (h *CatalogHandler) GetRepo(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid repository ID")
		return
	}

	repo, err := h.queries.GetHelmRepositoryByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Repository not found")
		return
	}

	RespondJSON(w, http.StatusOK, repo)
}

// UpdateRepoRequest represents the request body for updating a helm repository.
type UpdateRepoRequest struct {
	Name        string          `json:"name"`
	URL         string          `json:"url"`
	RepoType    string          `json:"repo_type"`
	Description string          `json:"description"`
	IsDefault   bool            `json:"is_default"`
	AuthType    string          `json:"auth_type"`
	AuthConfig  json.RawMessage `json:"auth_config"`
	Enabled     bool            `json:"enabled"`
}

// UpdateRepo handles PUT /api/v1/catalog/repositories/{id}/.
func (h *CatalogHandler) UpdateRepo(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid repository ID")
		return
	}

	var req UpdateRepoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	if req.AuthConfig == nil {
		req.AuthConfig = json.RawMessage(`{}`)
	}

	repo, err := h.queries.UpdateHelmRepository(r.Context(), sqlc.UpdateHelmRepositoryParams{
		ID:          id,
		Name:        req.Name,
		Url:         req.URL,
		RepoType:    req.RepoType,
		Description: req.Description,
		IsDefault:   req.IsDefault,
		AuthType:    req.AuthType,
		AuthConfig:  req.AuthConfig,
		Enabled:     req.Enabled,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "update_error", "Failed to update repository")
		return
	}

	recordAudit(r, h.queries, "catalog.repo.update", "helm_repository", repo.ID.String(), repo.Name, map[string]any{
		"url":       repo.Url,
		"enabled":   repo.Enabled,
		"auth_type": repo.AuthType,
	})

	RespondJSON(w, http.StatusOK, repo)
}

// DeleteRepo handles DELETE /api/v1/catalog/repositories/{id}/.
func (h *CatalogHandler) DeleteRepo(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid repository ID")
		return
	}

	repoName := ""
	if existing, lookupErr := h.queries.GetHelmRepositoryByID(r.Context(), id); lookupErr == nil {
		repoName = existing.Name
	}
	if err := h.queries.DeleteHelmRepository(r.Context(), id); err != nil {
		RespondError(w, http.StatusInternalServerError, "delete_error", "Failed to delete repository")
		return
	}

	recordAudit(r, h.queries, "catalog.repo.delete", "helm_repository", id.String(), repoName, nil)

	w.WriteHeader(http.StatusNoContent)
}

// SyncRepo handles POST /api/v1/catalog/repositories/{id}/sync/.
//
// Fetches the repository's index.yaml, parses the standard Helm schema, and
// upserts HelmChart + HelmChartVersion rows. The previous implementation only
// stamped last_synced_at, which left the chart catalog empty on a fresh
// install. Errors from the network or DB bubble up as a 502 — last_synced_at
// is only stamped on successful ingest.
func (h *CatalogHandler) SyncRepo(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid repository ID")
		return
	}
	repo, err := h.queries.GetHelmRepositoryByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Repository not found")
		return
	}
	var chartCount, versionCount int
	if isOCIRepoSpec(repo) {
		chartCount, versionCount, err = h.fetchAndIngestOCIRepo(r.Context(), repo)
	} else {
		chartCount, versionCount, err = h.fetchAndIngestRepoIndex(r.Context(), repo)
	}
	if err != nil {
		h.log.Warn("catalog sync failed", "repo", repo.Url, "error", err)
		RespondError(w, http.StatusBadGateway, "sync_error", fmt.Sprintf("Failed to sync repository: %v", err))
		return
	}
	if err := h.queries.UpdateHelmRepositoryLastSynced(r.Context(), id); err != nil {
		RespondError(w, http.StatusInternalServerError, "sync_error", "Failed to update repository sync timestamp")
		return
	}
	recordAudit(r, h.queries, "catalog.repo.sync", "helm_repository", repo.ID.String(), repo.Name, map[string]any{
		"charts":   chartCount,
		"versions": versionCount,
	})
	RespondJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"message":  "Repository sync completed",
		"charts":   chartCount,
		"versions": versionCount,
	})
}

// helmIndexFile mirrors the relevant fields of a Helm repo index.yaml. We use
// our own minimal struct rather than helm.sh/helm/v3/pkg/repo to keep this
// handler decoupled from the worker package.
type helmIndexFile struct {
	APIVersion string                          `json:"apiVersion"`
	Entries    map[string][]helmIndexChartVer `json:"entries"`
}

type helmIndexChartVer struct {
	Name        string                  `json:"name"`
	Version     string                  `json:"version"`
	AppVersion  string                  `json:"appVersion"`
	Description string                  `json:"description"`
	Icon        string                  `json:"icon"`
	Home        string                  `json:"home"`
	Digest      string                  `json:"digest"`
	URLs        []string                `json:"urls"`
	Keywords    []string                `json:"keywords"`
	Maintainers []helmIndexChartMaint   `json:"maintainers"`
	Created     time.Time               `json:"created"`
}

type helmIndexChartMaint struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	URL   string `json:"url"`
}

func (h *CatalogHandler) fetchAndIngestRepoIndex(ctx context.Context, repo sqlc.HelmRepository) (chartCount, versionCount int, err error) {
	indexURL := strings.TrimRight(repo.Url, "/") + "/index.yaml"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, indexURL, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("build index request: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("fetch index: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return 0, 0, fmt.Errorf("repository returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, fmt.Errorf("read index body: %w", err)
	}
	var index helmIndexFile
	if err := yaml.Unmarshal(body, &index); err != nil {
		return 0, 0, fmt.Errorf("parse index yaml: %w", err)
	}
	for chartName, versions := range index.Entries {
		if chartName == "" || len(versions) == 0 {
			continue
		}
		// Pick the first non-empty descriptive fields across all versions —
		// some repos only set icon/home on the latest version.
		first := versions[0]
		description, icon, home := first.Description, first.Icon, first.Home
		var keywords []string
		var maintainers []helmIndexChartMaint
		for _, v := range versions {
			if description == "" && v.Description != "" {
				description = v.Description
			}
			if icon == "" && v.Icon != "" {
				icon = v.Icon
			}
			if home == "" && v.Home != "" {
				home = v.Home
			}
			if len(keywords) == 0 && len(v.Keywords) > 0 {
				keywords = v.Keywords
			}
			if len(maintainers) == 0 && len(v.Maintainers) > 0 {
				maintainers = v.Maintainers
			}
		}
		chart, err := h.queries.GetHelmChartByRepoAndName(ctx, sqlc.GetHelmChartByRepoAndNameParams{
			RepositoryID: repo.ID,
			Name:         chartName,
		})
		if err != nil {
			keywordsJSON, _ := json.Marshal(keywords)
			if len(keywordsJSON) == 0 {
				keywordsJSON = []byte(`[]`)
			}
			maintList := make([]map[string]string, 0, len(maintainers))
			for _, m := range maintainers {
				maintList = append(maintList, map[string]string{"name": m.Name, "email": m.Email, "url": m.URL})
			}
			maintainersJSON, _ := json.Marshal(maintList)
			if len(maintainersJSON) == 0 {
				maintainersJSON = []byte(`[]`)
			}
			chart, err = h.queries.CreateHelmChart(ctx, sqlc.CreateHelmChartParams{
				RepositoryID: repo.ID,
				Name:         chartName,
				DisplayName:  chartName,
				Description:  description,
				IconUrl:      icon,
				HomeUrl:      home,
				Category:     "",
				Keywords:     keywordsJSON,
				Maintainers:  maintainersJSON,
				Deprecated:   false,
			})
			if err != nil {
				return chartCount, versionCount, fmt.Errorf("create chart %s: %w", chartName, err)
			}
		}
		chartCount++
		for _, v := range versions {
			if v.Version == "" {
				continue
			}
			if _, err := h.queries.GetHelmChartVersion(ctx, sqlc.GetHelmChartVersionParams{
				ChartID: chart.ID,
				Version: v.Version,
			}); err == nil {
				continue
			}
			urlsJSON, _ := json.Marshal(v.URLs)
			if len(urlsJSON) == 0 {
				urlsJSON = []byte(`[]`)
			}
			if _, err := h.queries.CreateHelmChartVersion(ctx, sqlc.CreateHelmChartVersionParams{
				ChartID:       chart.ID,
				Version:       v.Version,
				AppVersion:    v.AppVersion,
				Digest:        v.Digest,
				Urls:          urlsJSON,
				ValuesSchema:  json.RawMessage(`{}`),
				DefaultValues: "",
				Readme:        "",
				CreatedAtUpstream: pgtype.Timestamptz{
					Time:  v.Created,
					Valid: !v.Created.IsZero(),
				},
			}); err != nil {
				return chartCount, versionCount, fmt.Errorf("create chart version %s/%s: %w", chartName, v.Version, err)
			}
			versionCount++
		}
	}
	return chartCount, versionCount, nil
}

// --- Helm Charts ---

// ListCharts handles GET /api/v1/catalog/charts/.
//
// Migration 061: when ?project_id=<uuid> is present, the visible catalog
// set is narrowed from "every helm_repositories row" to the project-scoped
// union (globals + own + subscribed). Without project_id the behaviour
// is unchanged for the admin view.
func (h *CatalogHandler) ListCharts(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	if pidRaw := r.URL.Query().Get("project_id"); pidRaw != "" {
		pid, err := uuid.Parse(pidRaw)
		if err != nil {
			RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid project_id query param")
			return
		}
		visibleCatalogs, err := h.queries.ListCatalogsForProject(r.Context(), pid)
		if err != nil {
			RespondError(w, http.StatusInternalServerError, "list_error", "Failed to resolve project catalogs")
			return
		}
		// Fan out per-catalog. The repo count per project is small
		// enough (typically <20) that a per-repo ListChartsByRepository
		// query is the right shape — no need for an IN-list query.
		merged := []sqlc.HelmChart{}
		for _, cat := range visibleCatalogs {
			rowsForRepo, err := h.queries.ListChartsByRepository(r.Context(), sqlc.ListChartsByRepositoryParams{
				RepositoryID: cat.ID,
				Limit:        1000,
				Offset:       0,
			})
			if err != nil {
				continue
			}
			merged = append(merged, rowsForRepo...)
		}
		// Slice in-memory to honor the caller's limit/offset.
		total := int64(len(merged))
		if int(offset) > len(merged) {
			merged = nil
		} else {
			end := int(offset) + int(limit)
			if end > len(merged) {
				end = len(merged)
			}
			merged = merged[offset:end]
		}
		RespondPaginated(w, r, merged, total)
		return
	}

	charts, err := h.queries.ListHelmCharts(r.Context(), sqlc.ListHelmChartsParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list charts")
		return
	}

	total, err := h.queries.CountHelmCharts(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count charts")
		return
	}

	RespondPaginated(w, r, charts, total)
}

// GetChart handles GET /api/v1/catalog/charts/{id}/.
func (h *CatalogHandler) GetChart(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid chart ID")
		return
	}

	chart, err := h.queries.GetHelmChartByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Chart not found")
		return
	}

	RespondJSON(w, http.StatusOK, chart)
}

// ListChartVersions handles GET /api/v1/catalog/charts/{id}/versions/.
func (h *CatalogHandler) ListChartVersions(w http.ResponseWriter, r *http.Request) {
	chartID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid chart ID")
		return
	}
	versions, err := h.queries.ListChartVersions(r.Context(), sqlc.ListChartVersionsParams{
		ChartID: chartID,
		Limit:   int32(queryInt(r, "limit", 50)),
		Offset:  int32(queryInt(r, "offset", 0)),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list chart versions")
		return
	}
	RespondJSON(w, http.StatusOK, versions)
}

// --- Installed Charts (Installations) ---

// ListInstallations handles GET /api/v1/clusters/{cluster_id}/installations/.
func (h *CatalogHandler) ListInstallations(w http.ResponseWriter, r *http.Request) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}

	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	installations, err := h.queries.ListInstalledChartsByCluster(r.Context(), sqlc.ListInstalledChartsByClusterParams{
		ClusterID: clusterID,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list installations")
		return
	}

	total, err := h.queries.CountInstalledChartsByCluster(r.Context(), clusterID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count installations")
		return
	}

	RespondPaginated(w, r, installations, total)
}

// CreateInstallationRequest represents the request body for creating an installation.
type CreateInstallationRequest struct {
	ChartVersionID string `json:"chart_version_id"`
	ReleaseName    string `json:"release_name"`
	Namespace      string `json:"namespace"`
	ValuesOverride string `json:"values_override"`
	Notes          string `json:"notes"`
	ToolSlug       string `json:"tool_slug"`
	PresetUsed     string `json:"preset_used"`
}

type catalogOperationEnvelope struct {
	InstalledChartID string `json:"installedChartId"`
	ClusterID        string `json:"clusterId"`
	ReleaseName      string `json:"releaseName"`
	Namespace        string `json:"namespace"`
	ChartVersionID   string `json:"chartVersionId,omitempty"`
	ChartName        string `json:"chartName,omitempty"`
	RepoURL          string `json:"repoUrl,omitempty"`
	Version          string `json:"version,omitempty"`
	ValuesOverride   string `json:"valuesOverride,omitempty"`
	Notes            string `json:"notes,omitempty"`
	RollbackRevision int    `json:"rollbackRevision,omitempty"`
}

// CreateInstallation handles POST /api/v1/clusters/{cluster_id}/installations/.
func (h *CatalogHandler) CreateInstallation(w http.ResponseWriter, r *http.Request) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceCatalog, rbac.VerbCreate) {
		return
	}

	// Migration 057: maintenance window gate.
	if blocked := h.checkCatalogMaintenanceWindow(w, r, clusterID, "helm.install"); blocked {
		return
	}

	var req CreateInstallationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	if req.ReleaseName == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Release name is required")
		return
	}
	if req.Namespace == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Namespace is required")
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
	if req.ChartVersionID != "" {
		cvID, err := uuid.Parse(req.ChartVersionID)
		if err != nil {
			RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid chart version ID")
			return
		}
		params.ChartVersionID = pgtype.UUID{Bytes: cvID, Valid: true}
		version, err = h.queries.GetHelmChartVersionByID(r.Context(), cvID)
		if err != nil {
			RespondError(w, http.StatusNotFound, "not_found", "Chart version not found")
			return
		}
		chart, err = h.queries.GetHelmChartByID(r.Context(), version.ChartID)
		if err != nil {
			RespondError(w, http.StatusNotFound, "not_found", "Chart not found")
			return
		}
		repo, err = h.queries.GetHelmRepositoryByID(r.Context(), chart.RepositoryID)
		if err != nil {
			RespondError(w, http.StatusNotFound, "not_found", "Repository not found")
			return
		}
	}

	if req.ToolSlug != "" {
		params.ToolSlug = pgtype.Text{String: req.ToolSlug, Valid: true}
	}
	if req.PresetUsed != "" {
		params.PresetUsed = pgtype.Text{String: req.PresetUsed, Valid: true}
	}

	installation, err := h.queries.CreateInstalledChart(r.Context(), params)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create installation")
		return
	}
	op, err := h.enqueueOperation(r.Context(), "installed_chart", installation.ID.String(), "install", catalogOperationEnvelope{
		InstalledChartID: installation.ID.String(),
		ClusterID:        clusterID.String(),
		ReleaseName:      installation.ReleaseName,
		Namespace:        installation.Namespace,
		ChartVersionID:   req.ChartVersionID,
		ChartName:        chart.Name,
		RepoURL:          repo.Url,
		Version:          version.Version,
		ValuesOverride:   installation.ValuesOverride,
		Notes:            installation.Notes,
	}, currentUserUUID(r))
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "enqueue_error", "Failed to enqueue installation")
		return
	}
	recordAudit(r, h.queries, "catalog.installation.create", "installed_chart", installation.ID.String(), installation.ReleaseName, map[string]any{
		"cluster_id":       installation.ClusterID.String(),
		"namespace":        installation.Namespace,
		"chart_version_id": req.ChartVersionID,
		"chart_name":       chart.Name,
		"repo_url":         repo.Url,
		"version":          version.Version,
		"operation_id":     op.ID.String(),
	})
	RespondJSON(w, http.StatusAccepted, map[string]any{
		"installation": installation,
		"operation":    catalogOperationResponse(op),
	})
}

// DeleteInstallation handles DELETE /api/v1/clusters/{cluster_id}/installations/{id}/.
func (h *CatalogHandler) DeleteInstallation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid installation ID")
		return
	}
	installation, err := h.queries.GetInstalledChartByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Installation not found")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, installation.ClusterID, rbac.ResourceCatalog, rbac.VerbDelete) {
		return
	}
	// Migration 057: maintenance window gate.
	if blocked := h.checkCatalogMaintenanceWindow(w, r, installation.ClusterID, "helm.uninstall"); blocked {
		return
	}
	if err := h.queries.UpdateInstalledChartStatus(r.Context(), sqlc.UpdateInstalledChartStatusParams{
		ID:       installation.ID,
		Status:   "pending_uninstall",
		Revision: installation.Revision,
	}); err != nil {
		RespondError(w, http.StatusInternalServerError, "update_error", "Failed to mark installation for deletion")
		return
	}
	op, err := h.enqueueOperation(r.Context(), "installed_chart", installation.ID.String(), "uninstall", catalogOperationEnvelope{
		InstalledChartID: installation.ID.String(),
		ClusterID:        installation.ClusterID.String(),
		ReleaseName:      installation.ReleaseName,
		Namespace:        installation.Namespace,
	}, currentUserUUID(r))
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "enqueue_error", "Failed to enqueue uninstall")
		return
	}
	recordAudit(r, h.queries, "catalog.installation.delete", "installed_chart", installation.ID.String(), installation.ReleaseName, map[string]any{
		"cluster_id":   installation.ClusterID.String(),
		"namespace":    installation.Namespace,
		"operation_id": op.ID.String(),
	})
	RespondJSON(w, http.StatusAccepted, catalogOperationResponse(op))
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

	items, err := h.queries.ListInstalledCharts(r.Context(), sqlc.ListInstalledChartsParams{
		Limit:  int32(queryInt(r, "limit", 20)),
		Offset: int32(queryInt(r, "offset", 0)),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list installed charts")
		return
	}
	total, err := h.queries.CountInstalledCharts(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count installed charts")
		return
	}
	RespondPaginated(w, r, items, total)
}

// CreateInstalledChart handles POST /api/v1/catalog/installed/.
func (h *CatalogHandler) CreateInstalledChart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClusterID string `json:"cluster_id"`
		CreateInstallationRequest
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
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
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid installed chart ID")
		return
	}
	var req struct {
		ValuesOverride string `json:"values_override"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	installed, err := h.queries.GetInstalledChartByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Installed chart not found")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, installed.ClusterID, rbac.ResourceCatalog, rbac.VerbUpdate) {
		return
	}
	version, chart, repo, err := h.resolveInstalledChartRelease(r.Context(), installed)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "resolve_error", "Failed to resolve installed chart release")
		return
	}
	updated, err := h.queries.UpdateInstalledChartValues(r.Context(), sqlc.UpdateInstalledChartValuesParams{
		ID:             id,
		ValuesOverride: req.ValuesOverride,
		Status:         "pending_upgrade",
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "update_error", "Failed to stage installed chart upgrade")
		return
	}
	op, err := h.enqueueOperation(r.Context(), "installed_chart", installed.ID.String(), "upgrade", catalogOperationEnvelope{
		InstalledChartID: installed.ID.String(),
		ClusterID:        installed.ClusterID.String(),
		ReleaseName:      installed.ReleaseName,
		Namespace:        installed.Namespace,
		ChartVersionID:   uuidFromPg(installed.ChartVersionID),
		ChartName:        chart.Name,
		RepoURL:          repo.Url,
		Version:          version.Version,
		ValuesOverride:   req.ValuesOverride,
		Notes:            installed.Notes,
	}, currentUserUUID(r))
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "enqueue_error", "Failed to enqueue installed chart upgrade")
		return
	}
	recordAudit(r, h.queries, "catalog.installation.upgrade", "installed_chart", installed.ID.String(), installed.ReleaseName, map[string]any{
		"cluster_id":   installed.ClusterID.String(),
		"namespace":    installed.Namespace,
		"chart_name":   chart.Name,
		"repo_url":     repo.Url,
		"version":      version.Version,
		"operation_id": op.ID.String(),
	})
	RespondJSON(w, http.StatusAccepted, map[string]any{
		"installation": updated,
		"operation":    catalogOperationResponse(op),
	})
}

// RollbackInstalledChart handles POST /api/v1/catalog/installed/{id}/rollback/.
func (h *CatalogHandler) RollbackInstalledChart(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid installed chart ID")
		return
	}
	current, err := h.queries.GetInstalledChartByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Installed chart not found")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, current.ClusterID, rbac.ResourceCatalog, rbac.VerbUpdate) {
		return
	}
	if err := h.queries.UpdateInstalledChartStatus(r.Context(), sqlc.UpdateInstalledChartStatusParams{
		ID:       id,
		Status:   "pending_rollback",
		Revision: current.Revision,
	}); err != nil {
		RespondError(w, http.StatusInternalServerError, "rollback_error", "Failed to rollback installed chart")
		return
	}
	op, err := h.enqueueOperation(r.Context(), "installed_chart", current.ID.String(), "rollback", catalogOperationEnvelope{
		InstalledChartID: current.ID.String(),
		ClusterID:        current.ClusterID.String(),
		ReleaseName:      current.ReleaseName,
		Namespace:        current.Namespace,
		RollbackRevision: int(max(current.Revision-1, 1)),
	}, currentUserUUID(r))
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "enqueue_error", "Failed to enqueue rollback")
		return
	}
	recordAudit(r, h.queries, "catalog.installation.rollback", "installed_chart", current.ID.String(), current.ReleaseName, map[string]any{
		"cluster_id":        current.ClusterID.String(),
		"namespace":         current.Namespace,
		"rollback_revision": int(max(current.Revision-1, 1)),
		"operation_id":      op.ID.String(),
	})
	RespondJSON(w, http.StatusAccepted, catalogOperationResponse(op))
}

// DeleteInstalledChart is a compatibility alias for DeleteInstallation.
func (h *CatalogHandler) DeleteInstalledChart(w http.ResponseWriter, r *http.Request) {
	h.DeleteInstallation(w, r)
}

// TestRepoConnection handles POST /api/v1/catalog/repositories/{id}/test-connection/.
// Probes the repository's index.yaml endpoint to verify reachability.
func (h *CatalogHandler) TestRepoConnection(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid repository ID")
		return
	}
	repo, err := h.queries.GetHelmRepositoryByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Repository not found")
		return
	}
	if isOCIRepoSpec(repo) {
		// For OCI we just hit the /v2/ ping endpoint, which all
		// distribution-spec registries implement and all return 200/401
		// (401 here still proves the host is a registry).
		host, _, err := splitOCIURL(repo.Url)
		if err != nil {
			RespondJSON(w, http.StatusBadGateway, map[string]any{"success": false, "message": err.Error()})
			return
		}
		pingURL := "https://" + host + "/v2/"
		client := &http.Client{Timeout: 10 * time.Second}
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, pingURL, nil)
		if err != nil {
			RespondError(w, http.StatusBadRequest, "invalid_url", err.Error())
			return
		}
		cfg := parseOCIAuthConfig(repo.AuthConfig)
		if cfg.Username != "" || cfg.Password != "" {
			req.SetBasicAuth(cfg.Username, cfg.Password)
		}
		resp, err := client.Do(req)
		if err != nil {
			RespondJSON(w, http.StatusBadGateway, map[string]any{"success": false, "message": err.Error()})
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 500 {
			RespondJSON(w, http.StatusOK, map[string]any{"success": true, "message": fmt.Sprintf("OCI registry reachable (status %d).", resp.StatusCode)})
			return
		}
		RespondJSON(w, http.StatusBadGateway, map[string]any{"success": false, "message": fmt.Sprintf("registry returned status %d", resp.StatusCode)})
		return
	}
	url := strings.TrimRight(repo.Url, "/") + "/index.yaml"
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_url", err.Error())
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		RespondJSON(w, http.StatusBadGateway, map[string]any{"success": false, "message": err.Error()})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		RespondJSON(w, http.StatusBadGateway, map[string]any{
			"success": false,
			"message": fmt.Sprintf("repository returned status %d", resp.StatusCode),
		})
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Connection successful."})
}

// isOCIRepoSpec reports whether the stored repository should be treated as
// an OCI artifact registry. We accept either an oci:// URL or an explicit
// repo_type='oci' marker so operators can override URL-based detection.
func isOCIRepoSpec(repo sqlc.HelmRepository) bool {
	if strings.EqualFold(strings.TrimSpace(repo.RepoType), "oci") {
		return true
	}
	return IsOCIRepo(repo.Url)
}

// GetChartReadme handles GET /api/v1/catalog/charts/{id}/readme/.
// Returns the README from the latest (or ?version=) chart version.
func (h *CatalogHandler) GetChartReadme(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid chart ID")
		return
	}
	chart, err := h.queries.GetHelmChartByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Chart not found")
		return
	}
	version, err := h.resolveChartVersion(r, chart)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "No versions found for this chart.")
		return
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
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid chart ID")
		return
	}
	chart, err := h.queries.GetHelmChartByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Chart not found")
		return
	}
	version, err := h.resolveChartVersion(r, chart)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "No versions found for this chart.")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"chart":          chart.Name,
		"version":        version.Version,
		"default_values": version.DefaultValues,
		"values_schema":  version.ValuesSchema,
	})
}

// GetInstalledChartValues handles GET /api/v1/catalog/installed/{id}/values/.
// Returns the values_override stored on the release.
func (h *CatalogHandler) GetInstalledChartValues(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid release ID")
		return
	}
	installed, err := h.queries.GetInstalledChartByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Installed chart not found")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"release_name":    installed.ReleaseName,
		"namespace":       installed.Namespace,
		"values_override": installed.ValuesOverride,
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

func (h *CatalogHandler) ListOperations(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 50))
	offset := int32(queryInt(r, "offset", 0))
	arg := sqlc.ListCatalogOperationsParams{Limit: limit, Offset: offset}
	if v := strings.TrimSpace(r.URL.Query().Get("targetType")); v != "" {
		arg.TargetType = pgtype.Text{String: v, Valid: true}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("targetKey")); v != "" {
		arg.TargetKey = pgtype.Text{String: v, Valid: true}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("status")); v != "" {
		arg.Status = pgtype.Text{String: v, Valid: true}
	}
	ops, err := h.queries.ListCatalogOperations(r.Context(), arg)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list catalog operations")
		return
	}
	bindings, restricted, err := h.authz.bindingsForContext(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "permission_error", "Failed to retrieve user permissions")
		return
	}
	items := make([]map[string]any, 0, len(ops))
	for _, op := range ops {
		if restricted {
			clusterID, err := catalogOperationClusterID(op)
			if err != nil || !h.authz.allowsCluster(bindings, clusterID, rbac.ResourceCatalog, rbac.VerbRead) {
				continue
			}
		}
		items = append(items, catalogOperationResponse(op))
	}
	RespondJSON(w, http.StatusOK, items)
}

func (h *CatalogHandler) GetOperation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid operation ID")
		return
	}
	op, err := h.queries.GetCatalogOperation(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Catalog operation not found")
		return
	}
	clusterID, err := catalogOperationClusterID(op)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "resolve_error", "Failed to resolve catalog operation target")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceCatalog, rbac.VerbRead) {
		return
	}
	resp := catalogOperationResponse(op)
	if events, err := h.queries.ListCatalogOperationEvents(r.Context(), op.ID); err == nil {
		resp["events"] = catalogOperationEventsResponse(events)
	}
	RespondJSON(w, http.StatusOK, resp)
}

func (h *CatalogHandler) RetryOperation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid operation ID")
		return
	}
	op, err := h.queries.GetCatalogOperation(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Catalog operation not found")
		return
	}
	if op.Status != OpStatusFailed && op.Status != OpStatusSuperseded {
		RespondError(w, http.StatusConflict, "invalid_state", "Only failed or superseded operations can be retried")
		return
	}
	clusterID, err := catalogOperationClusterID(op)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "resolve_error", "Failed to resolve catalog operation target")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceCatalog, rbac.VerbUpdate) {
		return
	}
	requeued, err := h.queries.RequeueCatalogOperation(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "retry_error", "Failed to retry catalog operation")
		return
	}
	h.TriggerReconcile()
	recordAudit(r, h.queries, "catalog.operation.retry", "catalog_operation", id.String(), op.TargetKey, map[string]any{
		"target_type":     op.TargetType,
		"previous_status": op.Status,
	})
	RespondJSON(w, http.StatusAccepted, catalogOperationResponse(requeued))
}

func catalogOperationClusterID(op sqlc.CatalogOperation) (uuid.UUID, error) {
	var env catalogOperationEnvelope
	if err := json.Unmarshal(op.Payload, &env); err != nil {
		return uuid.UUID{}, err
	}
	return uuid.Parse(env.ClusterID)
}

func (h *CatalogHandler) ControllerStatus(w http.ResponseWriter, r *http.Request) {
	summary, err := h.controllerSummary(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "status_error", "Failed to load catalog operations")
		return
	}
	RespondJSON(w, http.StatusOK, summary)
}

func (h *CatalogHandler) controllerSummary(ctx context.Context) (map[string]any, error) {
	ops, err := h.queries.ListCatalogOperations(ctx, sqlc.ListCatalogOperationsParams{Limit: 1000, Offset: 0})
	if err != nil {
		return nil, err
	}
	bindings, restricted, err := h.authz.bindingsForContext(ctx)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	staleRunning := 0
	recent := make([]map[string]any, 0, min(len(ops), 5))
	var latestFailure map[string]any
	recentFailureCount := 0
	for _, op := range ops {
		if restricted {
			clusterID, err := catalogOperationClusterID(op)
			if err != nil || !h.authz.allowsCluster(bindings, clusterID, rbac.ResourceCatalog, rbac.VerbRead) {
				continue
			}
		}
		counts[op.Status]++
		if op.Status == OpStatusRunning && op.StartedAt.Valid && time.Since(op.StartedAt.Time) > time.Minute {
			staleRunning++
		}
		if len(recent) < 5 {
			recent = append(recent, h.operationPreview(ctx, op))
		}
		if (op.Status == OpStatusFailed || op.Status == OpStatusSuperseded) && time.Since(op.CreatedAt) <= 30*time.Minute {
			recentFailureCount++
		}
		if latestFailure == nil && (op.Status == OpStatusFailed || op.Status == OpStatusSuperseded) {
			latestFailure = h.operationPreview(ctx, op)
		}
	}
	charts, _ := h.queries.CountHelmCharts(ctx)
	installed, _ := h.queries.CountInstalledCharts(ctx)
	return map[string]any{
		"reconciler": map[string]any{
			"enabled":              true,
			"queueDepth":           counts[OpStatusPending] + counts[OpStatusRunning],
			"staleRunningCount":    staleRunning,
			"staleThresholdSecond": 60,
		},
		"catalog": map[string]any{
			"chartCount": charts,
			"installedCount": func() any {
				if restricted {
					return nil
				}
				return installed
			}(),
		},
		"operations":         counts,
		"recentFailureCount": recentFailureCount,
		"recentOperations":   recent,
		"latestFailure":      latestFailure,
	}, nil
}

func (h *CatalogHandler) enqueueOperation(ctx context.Context, targetType, targetKey, operationType string, env catalogOperationEnvelope, userID pgtype.UUID) (sqlc.CatalogOperation, error) {
	payload, err := json.Marshal(env)
	if err != nil {
		return sqlc.CatalogOperation{}, err
	}
	op, err := h.queries.CreateCatalogOperation(ctx, sqlc.CreateCatalogOperationParams{
		TargetType:    targetType,
		TargetKey:     targetKey,
		OperationType: operationType,
		Payload:       payload,
		Status:        OpStatusPending,
		CreatedByID:   userID,
	})
	if err == nil {
		h.TriggerReconcile()
	}
	return op, err
}

func catalogOperationResponse(op sqlc.CatalogOperation) map[string]any {
	return map[string]any{
		"id":            op.ID.String(),
		"targetType":    op.TargetType,
		"targetKey":     op.TargetKey,
		"operationType": op.OperationType,
		"status":        op.Status,
		"attemptCount":  op.AttemptCount,
		"startedAt":     nullablePgTime(op.StartedAt),
		"completedAt":   nullablePgTime(op.CompletedAt),
		"errorMessage":  op.ErrorMessage,
		"createdAt":     op.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt":     op.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func catalogOperationEventsResponse(events []sqlc.CatalogOperationEvent) []map[string]any {
	out := make([]map[string]any, 0, len(events))
	for _, event := range events {
		out = append(out, map[string]any{
			"id":        event.ID.String(),
			"level":     event.Level,
			"stage":     event.Stage,
			"message":   event.Message,
			"detail":    decodeJSONMap(event.Detail),
			"createdAt": event.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

func (h *CatalogHandler) operationPreview(ctx context.Context, op sqlc.CatalogOperation) map[string]any {
	resp := catalogOperationResponse(op)
	if events, err := h.queries.ListCatalogOperationEvents(ctx, op.ID); err == nil && len(events) > 0 {
		resp["eventsPreview"] = catalogOperationEventsResponse(lastCatalogEvents(events, 3))
	}
	return resp
}

func lastCatalogEvents(events []sqlc.CatalogOperationEvent, n int) []sqlc.CatalogOperationEvent {
	if len(events) <= n {
		return events
	}
	return events[len(events)-n:]
}

func (h *CatalogHandler) processPendingOperations(ctx context.Context) {
	// Claim under the lock, then release before
	// the (potentially 10-minute) helm dispatch so other clusters'
	// operations are not stalled behind one stuck install.
	dispatchClaimed(ctx, h.helmConcurrency, h.claimPendingCatalogOperations(ctx))
}

// claimPendingCatalogOperations holds h.mu just long enough to mark
// supersession + claim the batch ("running" state). Returns the rows
// it owns wrapped as claimedOps; dispatchClaimed runs them outside the
// lock via per-row Run/OnComplete/OnFailure closures.
func (h *CatalogHandler) claimPendingCatalogOperations(ctx context.Context) []claimedOp {
	h.mu.Lock()
	defer h.mu.Unlock()
	ops, err := h.queries.ListPendingCatalogOperations(ctx, 20)
	if err != nil {
		return nil
	}
	latestByTarget := map[string]uuid.UUID{}
	for i := len(ops) - 1; i >= 0; i-- {
		key := ops[i].TargetType + ":" + ops[i].TargetKey
		if _, ok := latestByTarget[key]; !ok {
			latestByTarget[key] = ops[i].ID
		}
	}
	claimed := make([]claimedOp, 0, len(ops))
	for _, op := range ops {
		key := op.TargetType + ":" + op.TargetKey
		if latestID, ok := latestByTarget[key]; ok && latestID != op.ID {
			h.recordCatalogOperationEvent(ctx, op.ID, "info", "queue", "operation superseded by newer desired state", map[string]any{
				"targetType": op.TargetType,
				"targetKey":  op.TargetKey,
			})
			_, _ = h.queries.MarkCatalogOperationSuperseded(ctx, sqlc.MarkCatalogOperationSupersededParams{
				ID:           op.ID,
				ErrorMessage: "superseded by newer operation for target",
			})
			continue
		}
		if op.Status == OpStatusRunning && op.StartedAt.Valid && time.Since(op.StartedAt.Time) < time.Minute {
			continue
		}
		running, err := h.queries.MarkCatalogOperationRunning(ctx, op.ID)
		if err != nil {
			continue
		}
		h.recordCatalogOperationEvent(ctx, running.ID, "info", "queue", "operation execution started", map[string]any{
			"operationType": running.OperationType,
			"targetType":    running.TargetType,
			"targetKey":     running.TargetKey,
			"attemptCount":  running.AttemptCount,
		})
		claimed = append(claimed, claimedOp{
			ID: running.ID,
			Run: func(ctx context.Context) error {
				return h.executeOperation(ctx, running)
			},
			OnComplete: func(ctx context.Context) {
				h.recordCatalogOperationEvent(ctx, running.ID, "info", "complete", "operation completed", map[string]any{})
				_, _ = h.queries.MarkCatalogOperationCompleted(ctx, running.ID)
			},
			OnFailure: func(ctx context.Context, err error) {
				h.recordCatalogOperationEvent(ctx, running.ID, "error", "complete", "operation failed", map[string]any{"error": err.Error()})
				_, _ = h.queries.MarkCatalogOperationFailed(ctx, sqlc.MarkCatalogOperationFailedParams{
					ID:           running.ID,
					ErrorMessage: err.Error(),
				})
				if h.log != nil {
					h.log.Warn("catalog operation failed", "id", running.ID.String(), "error", err)
				}
			},
		})
	}
	return claimed
}

func (h *CatalogHandler) executeOperation(ctx context.Context, op sqlc.CatalogOperation) error {
	if h.helm == nil {
		return errors.New("helm requester not configured")
	}
	var env catalogOperationEnvelope
	if err := json.Unmarshal(op.Payload, &env); err != nil {
		return err
	}
	installationID, err := uuid.Parse(env.InstalledChartID)
	if err != nil {
		return err
	}
	installation, err := h.queries.GetInstalledChartByID(ctx, installationID)
	if err != nil {
		return err
	}
	clusterID := installation.ClusterID.String()
	switch op.OperationType {
	case "install":
		h.recordCatalogOperationEvent(ctx, op.ID, "info", "install", "installing catalog release", map[string]any{
			"clusterId":   clusterID,
			"releaseName": installation.ReleaseName,
			"namespace":   installation.Namespace,
		})
		result, err := h.sendHelm(ctx, clusterID, protocol.MsgHelmInstall, env)
		if err != nil {
			_ = h.queries.UpdateInstalledChartStatus(ctx, sqlc.UpdateInstalledChartStatusParams{ID: installation.ID, Status: "failed_install", Revision: installation.Revision})
			return err
		}
		return h.queries.UpdateInstalledChartStatus(ctx, sqlc.UpdateInstalledChartStatusParams{
			ID:       installation.ID,
			Status:   normalizeToolStatus(result.Status),
			Revision: int32(result.Revision),
		})
	case "upgrade":
		h.recordCatalogOperationEvent(ctx, op.ID, "info", "upgrade", "upgrading catalog release", map[string]any{
			"clusterId":   clusterID,
			"releaseName": installation.ReleaseName,
			"namespace":   installation.Namespace,
		})
		result, err := h.sendHelm(ctx, clusterID, protocol.MsgHelmUpgrade, env)
		if err != nil {
			_ = h.queries.UpdateInstalledChartStatus(ctx, sqlc.UpdateInstalledChartStatusParams{ID: installation.ID, Status: "failed_upgrade", Revision: installation.Revision})
			return err
		}
		_, err = h.queries.UpdateInstalledChartValues(ctx, sqlc.UpdateInstalledChartValuesParams{
			ID:             installation.ID,
			ValuesOverride: env.ValuesOverride,
			Status:         normalizeToolStatus(result.Status),
		})
		return err
	case "rollback":
		h.recordCatalogOperationEvent(ctx, op.ID, "info", "rollback", "rolling back catalog release", map[string]any{
			"clusterId":        clusterID,
			"releaseName":      installation.ReleaseName,
			"namespace":        installation.Namespace,
			"rollbackRevision": env.RollbackRevision,
		})
		result, err := h.helm.Do(ctx, clusterID, protocol.MsgHelmRollback, protocol.HelmRequestPayload{
			ReleaseName: installation.ReleaseName,
			Namespace:   installation.Namespace,
			Revision:    env.RollbackRevision,
		})
		if err != nil {
			_ = h.queries.UpdateInstalledChartStatus(ctx, sqlc.UpdateInstalledChartStatusParams{ID: installation.ID, Status: "failed_rollback", Revision: installation.Revision})
			return err
		}
		return h.queries.UpdateInstalledChartStatus(ctx, sqlc.UpdateInstalledChartStatusParams{
			ID:       installation.ID,
			Status:   normalizeToolStatus(result.Status),
			Revision: int32(result.Revision),
		})
	case "uninstall":
		h.recordCatalogOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling catalog release", map[string]any{
			"clusterId":   clusterID,
			"releaseName": installation.ReleaseName,
			"namespace":   installation.Namespace,
		})
		_, err := h.helm.Do(ctx, clusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{
			ReleaseName: installation.ReleaseName,
			Namespace:   installation.Namespace,
		})
		if err != nil {
			_ = h.queries.UpdateInstalledChartStatus(ctx, sqlc.UpdateInstalledChartStatusParams{ID: installation.ID, Status: "failed_uninstall", Revision: installation.Revision})
			return err
		}
		return h.queries.DeleteInstalledChart(ctx, installation.ID)
	default:
		return fmt.Errorf("unsupported catalog operation type: %s", op.OperationType)
	}
}

// checkCatalogMaintenanceWindow consults the migration-057 gate and
// writes the 409/202 response when the operation is blocked. Returns
// true when the caller should stop. Best-effort cluster lookup —
// failing to resolve labels leaves the selector check on an empty
// label set rather than erroring the user out at gate time.
func (h *CatalogHandler) checkCatalogMaintenanceWindow(w http.ResponseWriter, r *http.Request, clusterID uuid.UUID, opType string) bool {
	if h == nil || h.maintenanceGate == nil {
		return false
	}
	labels := map[string]string{}
	if cluster, err := h.queries.GetClusterByID(r.Context(), clusterID); err == nil {
		labels = MaintenanceGateClusterLabels(cluster)
	}
	return EnforceMaintenanceWindow(w, r, h.maintenanceGate, opType, labels,
		pgtype.UUID{Bytes: clusterID, Valid: true}, pgtype.UUID{})
}

func (h *CatalogHandler) sendHelm(ctx context.Context, clusterID string, msgType protocol.MessageType, env catalogOperationEnvelope) (*protocol.HelmResultPayload, error) {
	var values map[string]any
	if env.ValuesOverride != "" {
		if err := yaml.Unmarshal([]byte(env.ValuesOverride), &values); err != nil {
			return nil, err
		}
	}
	return h.helm.Do(ctx, clusterID, msgType, protocol.HelmRequestPayload{
		ReleaseName: env.ReleaseName,
		Namespace:   env.Namespace,
		ChartName:   env.ChartName,
		RepoURL:     env.RepoURL,
		Version:     env.Version,
		Values:      values,
	})
}

func (h *CatalogHandler) resolveInstalledChartRelease(ctx context.Context, installed sqlc.InstalledChart) (sqlc.HelmChartVersion, sqlc.HelmChart, sqlc.HelmRepository, error) {
	if !installed.ChartVersionID.Valid {
		return sqlc.HelmChartVersion{}, sqlc.HelmChart{}, sqlc.HelmRepository{}, errors.New("installed chart has no chart version")
	}
	versionID := uuid.UUID(installed.ChartVersionID.Bytes)
	version, err := h.queries.GetHelmChartVersionByID(ctx, versionID)
	if err != nil {
		return sqlc.HelmChartVersion{}, sqlc.HelmChart{}, sqlc.HelmRepository{}, err
	}
	chart, err := h.queries.GetHelmChartByID(ctx, version.ChartID)
	if err != nil {
		return sqlc.HelmChartVersion{}, sqlc.HelmChart{}, sqlc.HelmRepository{}, err
	}
	repo, err := h.queries.GetHelmRepositoryByID(ctx, chart.RepositoryID)
	if err != nil {
		return sqlc.HelmChartVersion{}, sqlc.HelmChart{}, sqlc.HelmRepository{}, err
	}
	return version, chart, repo, nil
}

func uuidFromPg(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}

func (h *CatalogHandler) recordCatalogOperationEvent(ctx context.Context, operationID uuid.UUID, level, stage, message string, detail map[string]any) {
	if h == nil || h.queries == nil {
		return
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		raw = json.RawMessage(`{}`)
	}
	_, _ = h.queries.CreateCatalogOperationEvent(ctx, sqlc.CreateCatalogOperationEventParams{
		OperationID: operationID,
		Level:       level,
		Stage:       stage,
		Message:     message,
		Detail:      raw,
	})
}
