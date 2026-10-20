package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
	"github.com/alphabravocompany/astronomer-go/internal/catalog/configuration"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
)

type configuredChartVersion struct {
	sqlc.HelmChartVersion
	CatalogVersion *configuration.Version `json:"catalog_version,omitempty"`
}

func (h *CatalogHandler) chartVersionProfile(ctx context.Context, version sqlc.HelmChartVersion) (*configuration.Version, error) {
	store, ok := h.queries.(applicationCatalogQuerier)
	if !ok {
		return nil, nil
	}
	entry, err := store.GetApplicationCatalogPresentationByChartVersion(ctx, version.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if entry.Revoked || (entry.VerificationStatus != "verified" && entry.VerificationStatus != "digest-verified") {
		return nil, errors.New("catalog metadata is not trusted")
	}
	artifact, err := configuration.Parse(entry.Artifact)
	if err != nil {
		return nil, err
	}
	profile := artifact.Find(version.Version)
	if profile != nil {
		if err := profile.Validate(version.Digest); err != nil {
			return nil, err
		}
	}
	return profile, nil
}

// Curated records are resolved by exact identity before pagination. A missing pin
// is an unavailable catalog, never an invitation to silently install latest.
func (h *CatalogHandler) listCuratedChartVersions(w http.ResponseWriter, r *http.Request, chart sqlc.HelmChart) bool {
	if !validateLegacyPage(w, r) {
		return true
	}
	if r.URL.Query().Get("view") != "curated" {
		return false
	}
	store, ok := h.queries.(applicationCatalogQuerier)
	if !ok {
		return false
	}
	repository, err := h.queries.GetHelmRepositoryByID(r.Context(), chart.RepositoryID)
	if err != nil {
		catalogConfigurationUnavailable(w, r)
		return true
	}
	entries, err := store.ListApplicationCatalogPresentations(r.Context())
	if err != nil {
		catalogConfigurationUnavailable(w, r)
		return true
	}
	var raw json.RawMessage
	for _, entry := range entries {
		if entry.ChartName == chart.Name && strings.TrimRight(entry.RepoUrl, "/") == strings.TrimRight(repository.Url, "/") {
			if raw != nil || entry.Revoked || (entry.VerificationStatus != "verified" && entry.VerificationStatus != "digest-verified") {
				catalogConfigurationUnavailable(w, r)
				return true
			}
			raw = entry.Artifact
		}
	}
	if raw == nil {
		return false
	}
	artifact, err := configuration.Parse(raw)
	if err != nil {
		catalogConfigurationUnavailable(w, r)
		return true
	}
	records := artifact.Versions
	if len(records) == 0 {
		records = []configuration.Version{{Version: artifact.Version, Available: true, Recommended: true}}
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].Recommended && !records[j].Recommended })
	var kubernetes string
	if clusterID := r.URL.Query().Get("cluster_id"); clusterID != "" {
		id, parseErr := uuid.Parse(clusterID)
		if parseErr != nil {
			catalogConfigurationUnavailable(w, r)
			return true
		}
		cluster, lookupErr := h.queries.GetClusterByID(r.Context(), id)
		if lookupErr != nil {
			catalogConfigurationUnavailable(w, r)
			return true
		}
		kubernetes = cluster.KubernetesVersion
	}
	rows := []configuredChartVersion{}
	for _, record := range records {
		version, lookupErr := h.queries.GetHelmChartVersion(r.Context(), sqlc.GetHelmChartVersionParams{ChartID: chart.ID, Version: record.Version})
		if lookupErr != nil {
			catalogConfigurationUnavailable(w, r)
			return true
		}
		row := configuredChartVersion{HelmChartVersion: version}
		if len(artifact.Versions) > 0 {
			if record.Available && !record.Revoked && record.Validate(version.Digest) != nil {
				catalogConfigurationUnavailable(w, r)
				return true
			}
			profile := record
			if r.URL.Query().Get("cluster_id") != "" {
				profile = profile.Eligible(kubernetes)
			}
			row.CatalogVersion = &profile
		}
		rows = append(rows, row)
	}
	if selected := r.URL.Query().Get("selected_version_id"); selected != "" {
		id, parseErr := uuid.Parse(selected)
		if parseErr != nil {
			apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidID, "Invalid selected version ID")
			return true
		}
		found := false
		for _, row := range rows {
			if row.ID == id {
				found = true
			}
		}
		if !found {
			current, lookupErr := h.queries.GetHelmChartVersionByID(r.Context(), id)
			if lookupErr != nil || current.ChartID != chart.ID {
				apiutil.WriteError(w, r, http.StatusNotFound, apiutil.NotFound, "Selected version is not part of this chart")
				return true
			}
			rows = append(rows, configuredChartVersion{HelmChartVersion: current, CatalogVersion: artifact.Find(current.Version)})
		}
	}
	limit, offset := queryLimit(r, 50), queryOffset(r)
	start := min(offset, len(rows))
	end := min(start+limit, len(rows))
	paging.Write(w, rows[start:end], paging.Exact(len(rows), limit, offset, end-start))
	return true
}
func catalogConfigurationUnavailable(w http.ResponseWriter, r *http.Request) {
	apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.Unavailable, "Catalog version configuration is unavailable or its immutable identity could not be verified")
}

func validateCatalogConfigurationValues(upstream json.RawMessage, profile *configuration.Version, values map[string]any) error {
	if err := validateCatalogValuesSchema(upstream, values); err != nil {
		return err
	}
	if profile != nil && profile.Configuration != nil {
		return validateCatalogValuesSchema(profile.Configuration.Schema, values)
	}
	return nil
}

func (h *CatalogHandler) authorizeCatalogPreviewChart(w http.ResponseWriter, r *http.Request, version sqlc.HelmChartVersion, projectID uuid.UUID) bool {
	chart, err := h.queries.GetHelmChartByID(r.Context(), version.ChartID)
	if err != nil {
		apiutil.WriteError(w, r, http.StatusNotFound, apiutil.NotFound, "Chart not found")
		return false
	}
	repo, err := h.queries.GetHelmRepositoryByID(r.Context(), chart.RepositoryID)
	if err != nil || (repo.OwnerProjectID.Valid && (projectID == uuid.Nil || !catalogVisibleToProject(r.Context(), h.queries, projectID, repo.ID))) {
		apiutil.WriteError(w, r, http.StatusNotFound, apiutil.NotFound, "Chart not found")
		return false
	}
	return true
}

func (h *CatalogHandler) recommendedChartVersion(ctx context.Context, chart sqlc.HelmChart) (sqlc.HelmChartVersion, bool, error) {
	store, ok := h.queries.(applicationCatalogQuerier)
	if !ok {
		return sqlc.HelmChartVersion{}, false, nil
	}
	repo, err := h.queries.GetHelmRepositoryByID(ctx, chart.RepositoryID)
	if err != nil {
		return sqlc.HelmChartVersion{}, true, err
	}
	entries, err := store.ListApplicationCatalogPresentations(ctx)
	if err != nil {
		return sqlc.HelmChartVersion{}, true, err
	}
	for _, entry := range entries {
		if entry.ChartName != chart.Name || strings.TrimRight(entry.RepoUrl, "/") != strings.TrimRight(repo.Url, "/") {
			continue
		}
		if entry.Revoked || (entry.VerificationStatus != "verified" && entry.VerificationStatus != "digest-verified") {
			return sqlc.HelmChartVersion{}, true, errors.New("catalog is not trusted")
		}
		artifact, err := configuration.Parse(entry.Artifact)
		if err != nil {
			return sqlc.HelmChartVersion{}, true, err
		}
		if len(artifact.Versions) > 0 {
			pin := artifact.Find(artifact.Version)
			if pin == nil || !pin.Recommended || !pin.Available || pin.Revoked {
				return sqlc.HelmChartVersion{}, true, errors.New("no recommended version is available")
			}
		}
		version, err := h.queries.GetHelmChartVersion(ctx, sqlc.GetHelmChartVersionParams{ChartID: chart.ID, Version: artifact.Version})
		return version, true, err
	}
	return sqlc.HelmChartVersion{}, false, nil
}

func (h *CatalogHandler) catalogPreviewUpgrade(ctx context.Context, to sqlc.HelmChartVersion, fromID string) error {
	profile, err := h.chartVersionProfile(ctx, to)
	if err != nil || profile == nil || profile.UpgradeFrom == "" {
		return err
	}
	id, err := uuid.Parse(fromID)
	if err != nil {
		return errors.New("select the installed version to preview this upgrade path")
	}
	from, err := h.queries.GetHelmChartVersionByID(ctx, id)
	if err != nil || from.ChartID != to.ChartID {
		return errors.New("installed version must belong to this chart")
	}
	return profile.CheckUpgrade(from.Version)
}

func (h *CatalogHandler) presentChartVersions(ctx context.Context, versions []sqlc.HelmChartVersion) []configuredChartVersion {
	rows := make([]configuredChartVersion, 0, len(versions))
	for _, version := range versions {
		profile, err := h.chartVersionProfile(ctx, version)
		if err != nil {
			profile = &configuration.Version{Version: version.Version, Available: false, UnavailableReason: "Catalog metadata is unavailable or this version is revoked"}
		}
		rows = append(rows, configuredChartVersion{HelmChartVersion: version, CatalogVersion: profile})
	}
	return rows
}
