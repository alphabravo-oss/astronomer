package handler

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func (h *CatalogHandler) catalogUpgradeConfiguration(w http.ResponseWriter, r *http.Request, installed sqlc.InstalledChart, version sqlc.HelmChartVersion, valuesOverride string, hasOverride bool, fromVersion string) (sqlc.HelmChartVersion, string, bool) {
	if catalogStore, ok := h.queries.(applicationCatalogQuerier); ok {
		presentation, presentationErr := catalogStore.GetApplicationCatalogPresentationByChartVersion(r.Context(), version.ID)
		if presentationErr == nil {
			var err error
			version, err = h.hydrateChartVersion(r.Context(), version)
			if err != nil {
				apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.Unavailable, "Chart configuration metadata is temporarily unavailable")
				return version, valuesOverride, false
			}
			cluster, clusterErr := h.queries.GetClusterByID(r.Context(), installed.ClusterID)
			if clusterErr != nil {
				apiutil.WriteError(w, r, http.StatusNotFound, apiutil.NotFound, "Cluster not found")
				return version, valuesOverride, false
			}
			if hasOverride {
				valuesOverride = mergeValueLayers(distributionInstallValues(presentation.Slug, cluster.Distribution), valuesOverride)
			}
			profile, profileErr := h.chartVersionProfile(r.Context(), version)
			if profileErr == nil && profile != nil {
				profileErr = profile.CheckUpgrade(fromVersion)
			}
			if profileErr != nil {
				apiutil.WriteError(w, r, http.StatusConflict, apiutil.Conflict, profileErr.Error())
				return version, valuesOverride, false
			}
			checks, allowed := catalogInstallChecks(cluster, version, presentation, valuesOverride)
			if !allowed {
				RespondJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{"code": apiutil.Conflict, "message": "Catalog upgrade prerequisites failed", "checks": checks}})
				return version, valuesOverride, false
			}
		} else if !errors.Is(presentationErr, pgx.ErrNoRows) {
			apiutil.WriteError(w, r, http.StatusInternalServerError, apiutil.ReadError, "Failed to verify catalog application trust")
			return version, valuesOverride, false
		}
	}

	return version, valuesOverride, true
}
