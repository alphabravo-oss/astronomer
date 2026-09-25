package handler

import (
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// GetInstalledChart resolves a durable release identity independently of list pages.
func (h *CatalogHandler) GetInstalledChart(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, 400, apierror.InvalidID, "Invalid release ID")
		return
	}
	installed, err := h.queries.GetInstalledChartByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, 404, apierror.NotFound, "Installed chart not found")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, installed.ClusterID, rbac.ResourceCatalog, rbac.VerbRead) {
		return
	}
	row := sqlc.InstalledChartWithMetadata{InstalledChart: h.refreshInstalledChartStatus(r.Context(), installed)}
	if installed.ChartVersionID.Valid {
		version, err := h.queries.GetHelmChartVersionByID(r.Context(), uuid.UUID(installed.ChartVersionID.Bytes))
		if err != nil {
			RespondRequestError(w, r, 500, apierror.InternalError, "Failed to load release chart version")
			return
		}
		chart, err := h.queries.GetHelmChartByID(r.Context(), version.ChartID)
		if err != nil {
			RespondRequestError(w, r, 500, apierror.InternalError, "Failed to load release chart")
			return
		}
		row.ChartID = pgtype.UUID{Bytes: chart.ID, Valid: true}
		row.ChartVersion = pgtype.Text{String: version.Version, Valid: true}
		row.ChartAppVersion = pgtype.Text{String: version.AppVersion, Valid: true}
		row.ChartName = pgtype.Text{String: chart.Name, Valid: true}
		row.ChartDisplayName = pgtype.Text{String: chart.DisplayName, Valid: true}
		row.ChartDescription = pgtype.Text{String: chart.Description, Valid: true}
		row.ChartIconUrl = pgtype.Text{String: chart.IconUrl, Valid: true}
		row.ChartCategory = pgtype.Text{String: chart.Category, Valid: true}
	}
	out := enrichedInstalledRowJSON(row)
	delete(out, "values_override")
	out["notes"] = installed.Notes
	RespondJSON(w, http.StatusOK, out)
}
