// Sprint 082 — enriched installed-charts list for the per-cluster
// Apps tab.
//
// The existing GET /catalog/installed/?cluster_id= returns raw
// installed_charts rows. That's fine for the older catalog/admin
// views, but the Apps tab needs to render chart name, icon, and
// version per row — and Tools-installed rows have
// chart_version_id = NULL with only tool_slug as their identifier.
//
// This handler exposes a single new endpoint that does the JOIN
// server-side so the UI can render a complete list with one fetch:
//
//   GET /api/v1/clusters/{cluster_id}/apps/
//
// The route lives under /clusters/{cluster_id}/apps/ rather than under
// /catalog/ so it can carry cluster-scoped semantics (RBAC, in-cluster
// nav, future cluster-only filters) without conflating with the
// admin-side catalog routes.

package handler

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// ListClusterApps handles GET /api/v1/clusters/{cluster_id}/apps/.
// Returns one row per installed_charts entry on this cluster with
// LEFT-joined chart metadata. Tool installs (chart_version_id NULL)
// come back with empty chart_* fields and a non-empty tool_slug.
func (h *CatalogHandler) ListClusterApps(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	limit := int32(queryInt(r, "limit", 50))
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	offset := int32(queryInt(r, "offset", 0))

	rows, err := h.queries.ListInstalledChartsWithMetadataByCluster(r.Context(), sqlc.ListInstalledChartsWithMetadataByClusterParams{
		ClusterID: clusterID,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "list_error", "Failed to list cluster apps")
		return
	}

	total, err := h.queries.CountInstalledChartsByCluster(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "count_error", "Failed to count cluster apps")
		return
	}

	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, enrichedInstalledRowJSON(row))
	}

	RespondPaginated(w, r, out, total)
}

// DeleteFailedClusterApps handles DELETE /api/v1/clusters/{cluster_id}/apps/failed/.
//
// Rancher-style "Force Delete" — hard-deletes installed_charts rows
// stuck in failed_install / failed_uninstall on this cluster. Used by
// the Apps tab to clear the trail of repeated failed install attempts
// (e.g. four `pgw-smoke*` rows where the chart resolution failed
// before the helm release was ever created). Returns the count of
// rows removed in the response body so the UI can render a "Deleted N
// failed install(s)" toast.
//
// Safety: we only remove rows in the two failure states. Healthy
// releases ('installed', 'pending_*', 'installing', etc.) are
// untouched. The helm release itself is not consulted — by definition
// the rows we delete here never reached the cluster (failed_install)
// or already failed to uninstall (failed_uninstall, where the release
// may already be gone). Operators who need to scrub a leftover helm
// release with `helm uninstall` can do so via the kubectl shell.
func (h *CatalogHandler) DeleteFailedClusterApps(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceCatalog, rbac.VerbDelete) {
		return
	}
	rows, err := h.queries.DeleteFailedInstallationsByCluster(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "delete_error", "Failed to delete failed installations")
		return
	}
	recordAudit(r, h.queries, "catalog.installations.delete_failed", "cluster", clusterID.String(), "", map[string]any{
		"deleted_count": rows,
	})
	RespondJSON(w, http.StatusOK, map[string]any{"deleted": rows})
}

// enrichedInstalledRowJSON projects an InstalledChartWithMetadata into
// the response shape the Apps tab consumes. Source kind makes the
// install-path provenance explicit so the UI can pivot tool_slug rows
// to the Tools tab and show "Managed by Tools" pills.
func enrichedInstalledRowJSON(row sqlc.InstalledChartWithMetadata) map[string]any {
	sourceKind := "app"
	if !row.ChartVersionID.Valid && row.ToolSlug.Valid && row.ToolSlug.String != "" {
		sourceKind = "tool"
	}

	displayName := pgTextString(row.ChartDisplayName)
	if displayName == "" {
		displayName = pgTextString(row.ChartName)
	}
	if displayName == "" && row.ToolSlug.Valid {
		// Fall back to tool_slug for Tools installs where chart_* are NULL.
		displayName = row.ToolSlug.String
	}
	if displayName == "" {
		// Last-resort fallback: the helm release name itself.
		displayName = row.ReleaseName
	}

	return map[string]any{
		"id":                row.ID.String(),
		"cluster_id":        row.ClusterID.String(),
		"chart_id":          pgUUIDString(row.ChartID),
		"chart_version_id":  pgUUIDString(row.ChartVersionID),
		"release_name":      row.ReleaseName,
		"namespace":         row.Namespace,
		"status":            row.Status,
		"revision":          row.Revision,
		"values_override":   row.ValuesOverride,
		"tool_slug":         pgTextString(row.ToolSlug),
		"preset_used":       pgTextString(row.PresetUsed),
		"source_kind":       sourceKind,
		"display_name":      displayName,
		"chart_name":        pgTextString(row.ChartName),
		"chart_version":     pgTextString(row.ChartVersion),
		"chart_app_version": pgTextString(row.ChartAppVersion),
		"chart_description": pgTextString(row.ChartDescription),
		"chart_icon_url":    pgTextString(row.ChartIconUrl),
		"chart_category":    pgTextString(row.ChartCategory),
		"repo_name":         pgTextString(row.RepoName),
		"repo_type":         pgTextString(row.RepoType),
		"created_at":        row.CreatedAt.Format(rfc3339Z),
		"updated_at":        row.UpdatedAt.Format(rfc3339Z),
	}
}

// pgTextString collapses pgtype.Text → string, returning "" for NULL.
func pgTextString(t pgtype.Text) string {
	if !t.Valid {
		return ""
	}
	return t.String
}

// pgUUIDString collapses pgtype.UUID → string, returning "" for NULL.
func pgUUIDString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	// pgtype.UUID stores 16 raw bytes; format as canonical 8-4-4-4-12.
	b := u.Bytes
	const hex = "0123456789abcdef"
	out := make([]byte, 36)
	j := 0
	for i, by := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out[j] = '-'
			j++
		}
		out[j] = hex[by>>4]
		out[j+1] = hex[by&0x0f]
		j += 2
	}
	return string(out)
}

const rfc3339Z = "2006-01-02T15:04:05Z07:00"
