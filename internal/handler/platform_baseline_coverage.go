// Sprint 075 — platform-baseline slug coverage endpoint.
//
// The cluster_template (sprint 074) references five chart slugs:
//
//   trivy-operator, kube-state-metrics, node-exporter,
//   fluent-bit, cert-manager
//
// Migration 075 seeds three helm_repositories (bitnami, aqua, jetstack)
// that should contain those slugs once the first-boot catalog:sync
// completes. This read-only endpoint is the operator's sanity check
// after install: hit GET /api/v1/admin/platform-settings/default-cluster-template/coverage/
// and see "5/5 slugs resolved" or "3/5 resolved, 2 missing".
//
// The check is hard-coded against the documented baseline slug list
// (rather than reading from a `default_cluster_template` row in the
// DB) because at the time this lands sprint 074 hasn't shipped yet;
// when the platform-baseline template is created, it'll reference
// exactly these slugs and this endpoint's expected_slugs[] will line
// up with template.spec.tools[].slug 1:1. If that list diverges in a
// future sprint, the const lives in one place — defaultBaselineSlugs.
//
// Security: superuser only. Same gate as platform_settings.go.

package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// defaultBaselineSlugs is the canonical list of chart names the
// platform-baseline cluster_template (sprint 074) references. Kept
// here as the single source of truth for the coverage endpoint.
var defaultBaselineSlugs = []string{
	"trivy-operator",
	"kube-state-metrics",
	"node-exporter",
	"fluent-bit",
	"cert-manager",
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

// coverageResponse is the JSON returned by Coverage. template_id is
// emitted as an empty string when no default template exists yet —
// sprint 074 will populate this from the persisted baseline row.
type coverageResponse struct {
	TemplateID    string          `json:"template_id"`
	ExpectedSlugs []string        `json:"expected_slugs"`
	Resolved      []coverageEntry `json:"resolved"`
	MissingSlugs  []string        `json:"missing_slugs"`
}

// Coverage handles GET /api/v1/admin/platform-settings/default-cluster-template/coverage/.
// Superuser-only. Read-only. Walks the hard-coded baseline slug list and
// resolves each one against helm_charts. Slugs not present in the catalog
// are returned in missing_slugs so the operator can either re-run
// catalog:sync or add a helm_repositories row that contains them.
func (h *PlatformBaselineCoverageHandler) Coverage(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	ctx := r.Context()

	resp := coverageResponse{
		// TemplateID stays empty until sprint 074 lands the persisted
		// default-template row. Frontend treats "" as "baseline lives in
		// code, no DB row yet" and renders the coverage banner regardless.
		TemplateID:    "",
		ExpectedSlugs: append([]string(nil), defaultBaselineSlugs...),
		Resolved:      make([]coverageEntry, 0, len(defaultBaselineSlugs)),
		MissingSlugs:  []string{},
	}

	for _, slug := range defaultBaselineSlugs {
		res, err := h.queries.ResolveChartByName(ctx, slug)
		if err != nil {
			// ErrCoverageSlugNotFound or any other lookup error — both
			// surface to the operator as "not resolved". A DB outage
			// would produce a wave of unresolved entries which is the
			// right signal (the dashboard banner says "0/5 — catalog
			// unreachable?").
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
	caller, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok {
		RespondError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return false
	}
	callerID, err := uuid.Parse(caller.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "internal_error", "Invalid user ID")
		return false
	}
	if h.queries == nil {
		RespondError(w, http.StatusInternalServerError, "internal_error", "User store not configured")
		return false
	}
	user, err := h.queries.GetUserByID(r.Context(), callerID)
	if err != nil {
		RespondError(w, http.StatusForbidden, "forbidden", "Caller not found")
		return false
	}
	if !user.IsSuperuser {
		RespondError(w, http.StatusForbidden, "forbidden", "Platform-baseline coverage requires superuser privileges")
		return false
	}
	return true
}
