// Platform-baseline catalog coverage endpoint.
//
// The embedded Flux bundle catalog is the only source of baseline membership.
// This endpoint reports whether every default-enabled component can also be
// resolved through the public chart catalog; it does not consult the retired
// cluster-template baseline.
//
// Security: superuser only. Same gate as platform_settings.go.

package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

func loadDefaultBaselineSlugs() ([]string, error) {
	catalog, err := builtinbundles.Load()
	if err != nil {
		return nil, err
	}
	slugs := make([]string, 0, len(catalog.Components))
	for _, component := range catalog.Components {
		if component.DefaultEnabled {
			slugs = append(slugs, component.Slug)
		}
	}
	return slugs, nil
}

// PlatformBaselineCoverageQuerier is the narrow DB surface this handler
// needs. The production *sqlc.Queries satisfies it; tests stand up a
// fake that maps slug -> sqlc.ChartResolution.
type PlatformBaselineCoverageQuerier interface {
	// GetUserByID is used for the superuser gate.
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	// ResolveChartByName returns the first helm_charts row whose name
	// matches the given slug, along with the owning repository's name.
	// Returns sqlc.ErrCoverageSlugNotFound when no row exists.
	ResolveChartByName(ctx context.Context, name string) (sqlc.ChartResolution, error)
}

// PlatformBaselineCoverageHandler serves the read-only coverage endpoint.
type PlatformBaselineCoverageHandler struct {
	queries PlatformBaselineCoverageQuerier
}

// NewPlatformBaselineCoverageHandler constructs the handler.
func NewPlatformBaselineCoverageHandler(queries PlatformBaselineCoverageQuerier) *PlatformBaselineCoverageHandler {
	return &PlatformBaselineCoverageHandler{queries: queries}
}

// coverageEntry is one slug's resolution result in the response.
type coverageEntry struct {
	Slug       string `json:"slug"`
	Found      bool   `json:"found"`
	ChartID    string `json:"chart_id,omitempty"`
	Repository string `json:"repository,omitempty"`
}

// coverageResponse is the JSON returned by Coverage. template_id remains an
// empty string for response compatibility; the built-in baseline has no
// cluster-template identity.
type coverageResponse struct {
	TemplateID    string          `json:"template_id"`
	ExpectedSlugs []string        `json:"expected_slugs"`
	Resolved      []coverageEntry `json:"resolved"`
	MissingSlugs  []string        `json:"missing_slugs"`
}

// Coverage handles GET /api/v1/admin/platform-settings/default-cluster-template/coverage/.
// Superuser-only. Read-only. Walks the embedded default-enabled bundle list and
// resolves each one against helm_charts. Slugs not present in the catalog
// are returned in missing_slugs so the operator can either re-run
// catalog:sync or add a helm_repositories row that contains them.
func (h *PlatformBaselineCoverageHandler) Coverage(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	ctx := r.Context()
	expectedSlugs, err := loadDefaultBaselineSlugs()
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Built-in bundle catalog is invalid")
		return
	}

	resp := coverageResponse{
		// TemplateID remains for response compatibility. Built-in baseline
		// ownership now lives exclusively in the versioned Flux catalog.
		TemplateID:    "",
		ExpectedSlugs: expectedSlugs,
		Resolved:      make([]coverageEntry, 0, len(expectedSlugs)),
		MissingSlugs:  []string{},
	}

	for _, slug := range expectedSlugs {
		res, err := h.queries.ResolveChartByName(ctx, slug)
		if err != nil {
			// ErrCoverageSlugNotFound or any other lookup error — both
			// surface to the operator as "not resolved". A DB outage
			// would produce a wave of unresolved entries which is the
			// right signal (the dashboard banner reports catalog
			// resolution failure without hiding the remaining entries).
			resp.Resolved = append(resp.Resolved, coverageEntry{Slug: slug, Found: false})
			resp.MissingSlugs = append(resp.MissingSlugs, slug)
			continue
		}
		resp.Resolved = append(resp.Resolved, coverageEntry{
			Slug:       slug,
			Found:      true,
			ChartID:    res.ChartID.String(),
			Repository: res.Repository,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// gate enforces superuser-only access. Matches the
// platform_settings.go pattern so the failure mode is a clean 403.
func (h *PlatformBaselineCoverageHandler) gate(w http.ResponseWriter, r *http.Request) bool {
	_, ok := requireSuperuser(w, r, h.queries, superuserGateConfig{
		StoreUnavailableStatus:  http.StatusInternalServerError,
		StoreUnavailableCode:    "internal_error",
		StoreUnavailableMessage: "User store not configured",
		ForbiddenMessage:        "Platform-baseline coverage requires superuser privileges",
	})
	return ok
}
