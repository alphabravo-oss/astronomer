package handler

import (
	"errors"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (h *ToolHandler) resolveAction(r *http.Request) (sqlc.ClusterTool, toolActionRequest, []toolRelease, string, error) {
	tool, err := h.queries.GetToolBySlug(r.Context(), chi.URLParam(r, "slug"))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = errToolNotFound
		}
		return sqlc.ClusterTool{}, toolActionRequest{}, nil, "", err
	}
	var req toolActionRequest
	if err := decodeStrictJSONBody(r, &req); err != nil {
		return sqlc.ClusterTool{}, toolActionRequest{}, nil, "", err
	}
	clusterID, err := uuid.Parse(req.ClusterID)
	if err != nil {
		return sqlc.ClusterTool{}, toolActionRequest{}, nil, "", err
	}
	charts, err := parseToolCharts(tool.Charts)
	if err != nil || len(charts) == 0 {
		if err == nil {
			err = errors.New("tool has no charts configured")
		}
		return sqlc.ClusterTool{}, toolActionRequest{}, nil, "", err
	}
	if err := validateToolFormValues(tool.Slug, req.ValuesOverride); err != nil {
		return sqlc.ClusterTool{}, toolActionRequest{}, nil, "", err
	}
	var distributionYAML string
	if cluster, err := h.queries.GetClusterByID(r.Context(), clusterID); err == nil {
		distributionYAML = distributionInstallValues(tool.Slug, cluster.Distribution)
	}
	// Deep merge preserves sibling settings across distribution, preset and
	// operator layers. The last explicit value wins.
	valuesYAML := mergeValueLayers(distributionYAML, presetValuesYAML(tool.Presets, req.Preset), req.ValuesOverride)
	if err := validateToolFormValues(tool.Slug, valuesYAML); err != nil {
		return sqlc.ClusterTool{}, toolActionRequest{}, nil, "", err
	}
	plan, err := buildToolReleasePlan(tool, req.ReleaseName, valuesYAML)
	return tool, req, plan, valuesYAML, err
}
