package handler

import (
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"sigs.k8s.io/yaml"
)

// Configuration returns the durable effective values for an installed Tool.
// Values may contain credentials, so this deliberately uses catalog:read on
// the exact cluster, matching the installed-chart values endpoint.
func (h *ToolHandler) Configuration(w http.ResponseWriter, r *http.Request) {
	tool, err := h.queries.GetToolBySlug(r.Context(), chi.URLParam(r, "slug"))
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Tool not found")
		return
	}
	clusterID, err := uuid.Parse(r.URL.Query().Get("cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceCatalog, rbac.VerbRead) {
		return
	}
	rows, err := h.installedToolReleases(r, clusterID, tool.Slug)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to load installed tool configuration")
		return
	}
	valuesYAML, preset, releases, err := combineInstalledToolValues(tool, rows)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Installed tool not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"preset":      preset,
		"values_yaml": valuesYAML,
		"releases":    releases,
	})
}

func (h *ToolHandler) installedToolReleases(r *http.Request, clusterID uuid.UUID, slug string) ([]sqlc.InstalledChart, error) {
	var matches []sqlc.InstalledChart
	for offset := int32(0); ; offset += 200 {
		rows, err := h.queries.ListInstalledChartsByCluster(r.Context(), sqlc.ListInstalledChartsByClusterParams{
			ClusterID: clusterID,
			Limit:     200,
			Offset:    offset,
		})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row.ToolSlug.Valid && row.ToolSlug.String == slug {
				matches = append(matches, row)
			}
		}
		if len(rows) < 200 {
			return matches, nil
		}
	}
}

func combineInstalledToolValues(tool sqlc.ClusterTool, rows []sqlc.InstalledChart) (string, string, []map[string]any, error) {
	if len(rows) == 0 {
		return "", "", nil, pgx.ErrNoRows
	}
	charts, err := parseToolCharts(tool.Charts)
	if err != nil {
		return "", "", nil, err
	}
	slices.SortStableFunc(charts, func(a, b toolChart) int { return a.Order - b.Order })
	byRef := make(map[string]sqlc.InstalledChart, len(rows))
	for _, row := range rows {
		byRef[row.Namespace+"/"+row.ReleaseName] = row
	}
	var preset string
	releases := make([]map[string]any, 0, len(charts))
	combined := map[string]any{}
	plainValues := ""
	for _, chart := range charts {
		name := chart.ReleaseName
		if name == "" {
			name = tool.Slug
		}
		namespace := chartNamespace(tool, chart)
		row, ok := byRef[namespace+"/"+name]
		if !ok {
			continue
		}
		if preset == "" && row.PresetUsed.Valid {
			preset = row.PresetUsed.String
		}
		releases = append(releases, map[string]any{
			"id": row.ID.String(), "release_name": name, "namespace": namespace,
			"revision": row.Revision,
		})
		if chart.ValuesKey == "" {
			// Rancher-style CRD + application plans historically persisted the
			// same effective document on each release. Prefer the later/main
			// release while walking the declared order.
			plainValues = row.ValuesOverride
			continue
		}
		value := map[string]any{}
		if strings.TrimSpace(row.ValuesOverride) != "" {
			if err := yaml.Unmarshal([]byte(row.ValuesOverride), &value); err != nil {
				return "", "", nil, err
			}
		}
		combined[chart.ValuesKey] = value
	}
	if len(releases) == 0 {
		return "", "", nil, pgx.ErrNoRows
	}
	if len(combined) == 0 {
		return plainValues, preset, releases, nil
	}
	raw, err := yaml.Marshal(combined)
	return string(raw), preset, releases, err
}
