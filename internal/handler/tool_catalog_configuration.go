package handler

import (
	"context"
	"errors"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/catalog/configuration"
)

// Resolve only an exact repository/chart/version. Tool release ownership and
// preset composition remain with the tool; catalog field constraints are shared.
func (h *ToolHandler) toolCatalogProfile(ctx context.Context, release toolRelease) (*configuration.Version, error) {
	store, ok := h.queries.(applicationCatalogQuerier)
	if !ok {
		return nil, nil
	}
	entries, err := store.ListApplicationCatalogPresentations(ctx)
	if err != nil {
		return nil, err
	}
	var profile *configuration.Version
	for _, entry := range entries {
		if strings.TrimRight(entry.RepoUrl, "/") != strings.TrimRight(release.RepoURL, "/") || entry.ChartName != release.ChartName {
			continue
		}
		if entry.Revoked || (entry.VerificationStatus != "verified" && entry.VerificationStatus != "digest-verified") {
			return nil, errors.New("tool chart catalog metadata is not trusted")
		}
		artifact, parseErr := configuration.Parse(entry.Artifact)
		if parseErr != nil {
			return nil, parseErr
		}
		selected := artifact.Find(release.Version)
		if selected == nil {
			continue
		}
		if profile != nil {
			return nil, errors.New("ambiguous tool chart configuration")
		}
		if err := selected.Validate(selected.Artifact.Digest); err != nil {
			return nil, err
		}
		profile = selected
	}
	return profile, nil
}
func (h *ToolHandler) toolCatalogChecks(ctx context.Context, plan []toolRelease) []toolPreflightCheck {
	checks := []toolPreflightCheck{}
	for _, release := range plan {
		profile, err := h.toolCatalogProfile(ctx, release)
		if err == nil && profile != nil && profile.Configuration != nil {
			values, valuesErr := valuesJSONForPreview(release.ValuesYAML)
			err = valuesErr
			if err == nil {
				err = validateCatalogValuesSchema(profile.Configuration.Schema, values)
			}
		}
		if err != nil {
			checks = append(checks, toolPreflightCheck{Code: "catalog-configuration", Status: "block", Message: release.ChartName + ": " + err.Error()})
		}
	}
	return checks
}
func (h *ToolHandler) toolPreviewCharts(ctx context.Context, plan []toolRelease) ([]map[string]any, map[string]*configuration.Version) {
	charts := make([]map[string]any, 0, len(plan))
	profiles := make(map[string]*configuration.Version)
	for _, release := range plan {
		row := map[string]any{"chart_name": release.ChartName, "chart_version": release.Version, "namespace": release.Namespace, "release_name": release.ReleaseName, "values_yaml": release.ValuesYAML}
		if profile, err := h.toolCatalogProfile(ctx, release); err == nil && profile != nil {
			profiles[release.Namespace+"/"+release.ReleaseName] = profile
		}
		charts = append(charts, row)
	}
	return charts, profiles
}
