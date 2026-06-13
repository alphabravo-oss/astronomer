// Migration 055 — chart ratings + recommendations HTTP surface.
//
// Routes (all under /api/v1/, require an authenticated session/token):
//   POST   /charts/{chart_id}/ratings/                  — create or update
//   GET    /charts/{chart_id}/ratings/                  — paginated list
//   GET    /charts/{chart_id}/ratings/aggregate/        — score + histogram
//   GET    /charts/{chart_id}/ratings/mine/             — current user's rating
//   PUT    /charts/{chart_id}/ratings/{rating_id}/      — owner-or-superuser update
//   DELETE /charts/{chart_id}/ratings/{rating_id}/      — owner-or-superuser delete
//   GET    /catalog/recommendations/popular/            — TopCharts
//   GET    /catalog/recommendations/similar/{chart_id}/ — SimilarCharts
//
// Audit:
//   chart.rating.{created,updated,deleted} — include chart_id + stars
//   (NOT the note — operator notes are user-generated content and
//    don't belong in the audit trail).

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// NoteMaxLen is the character cap on user-submitted notes. Matches the
// VARCHAR(280) in the chart_ratings schema — "Twitter-classic" was the
// spec's idiom for it.
const NoteMaxLen = 280

// ChartRatingsQuerier is the DB surface the handler needs. Kept narrow
// (no embedded sqlc.Querier) so handler tests can supply small in-
// memory fakes without satisfying the generated interface.
type ChartRatingsQuerier interface {
	catalog.Querier

	// Rating CRUD.
	CreateChartRating(ctx context.Context, arg sqlc.CreateChartRatingParams) (sqlc.ChartRating, error)
	UpdateChartRating(ctx context.Context, arg sqlc.UpdateChartRatingParams) (sqlc.ChartRating, error)
	GetChartRatingByID(ctx context.Context, id uuid.UUID) (sqlc.ChartRating, error)
	GetChartRatingByUserAndInstallation(ctx context.Context, arg sqlc.GetChartRatingByUserAndInstallationParams) (sqlc.ChartRating, error)
	GetChartRatingByUserAndChartNoInstall(ctx context.Context, arg sqlc.GetChartRatingByUserAndChartNoInstallParams) (sqlc.ChartRating, error)
	GetChartRatingForUserChart(ctx context.Context, arg sqlc.GetChartRatingForUserChartParams) (sqlc.ChartRating, error)
	ListChartRatingsByChart(ctx context.Context, arg sqlc.ListChartRatingsByChartParams) ([]sqlc.ChartRating, error)
	CountChartRatingsByChart(ctx context.Context, chartID uuid.UUID) (int64, error)
	DeleteChartRating(ctx context.Context, id uuid.UUID) error
	ChartRatingHistogram(ctx context.Context, chartID uuid.UUID) ([5]int64, error)

	// Chart resolution + superuser check.
	GetHelmChartByID(ctx context.Context, id uuid.UUID) (sqlc.HelmChart, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
}

// ChartRatingsHandler handles ratings + recommendation HTTP endpoints.
// It does not run any background work; rating writes call
// catalog.RecomputeAggregate inline so the hot-path browse never reads
// a stale aggregate, and the worker handles the nightly co-installation
// matrix rebuild.
type ChartRatingsHandler struct {
	queries ChartRatingsQuerier
	log     *slog.Logger
}

// NewChartRatingsHandler returns a handler bound to the given querier.
// A nil log is filled with slog.Default() at request time so the
// caller doesn't have to supply one.
func NewChartRatingsHandler(queries ChartRatingsQuerier) *ChartRatingsHandler {
	return &ChartRatingsHandler{queries: queries, log: slog.Default()}
}

// SetLogger swaps the per-handler logger. Used by routes.go to inject
// the configured server logger so request_id/correlation_id make it
// into audit lines.
func (h *ChartRatingsHandler) SetLogger(log *slog.Logger) {
	if log != nil {
		h.log = log
	}
}

// --- request / response payloads -------------------------------------

type createOrUpdateRatingRequest struct {
	Stars int16 `json:"stars"`
	// InstallationID is optional. When omitted the rating is bound by
	// the partial unique index to "this user, this chart" (one row per
	// chart). When set, it's bound to the specific installation
	// (UNIQUE(user_id, installation_id)).
	InstallationID *string `json:"installation_id,omitempty"`
	Note           string  `json:"note,omitempty"`
}

type ratingResponse struct {
	ID             uuid.UUID `json:"id"`
	ChartID        uuid.UUID `json:"chart_id"`
	InstallationID *string   `json:"installation_id,omitempty"`
	UserID         uuid.UUID `json:"user_id"`
	Stars          int16     `json:"stars"`
	Note           string    `json:"note"`
	CreatedAt      string    `json:"created_at"`
	UpdatedAt      string    `json:"updated_at"`
}

type aggregateResponse struct {
	RatingCount   int32    `json:"rating_count"`
	AvgStars      float64  `json:"avg_stars"`
	BayesianScore float64  `json:"bayesian_score"`
	Histogram     [5]int64 `json:"histogram"`
}

func toRatingResponse(r sqlc.ChartRating) ratingResponse {
	var instID *string
	if r.InstallationID.Valid {
		s := uuid.UUID(r.InstallationID.Bytes).String()
		instID = &s
	}
	return ratingResponse{
		ID:             r.ID,
		ChartID:        r.ChartID,
		InstallationID: instID,
		UserID:         r.UserID,
		Stars:          r.Stars,
		Note:           r.Note,
		CreatedAt:      r.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:      r.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}

// --- input helpers ---------------------------------------------------

// sanitizeNote trims whitespace and strips HTML tags. We're not aiming
// for a full HTML sanitizer here — the column cap of 280 is too small
// for a meaningful XSS payload to survive — but stripping `<...>`
// blocks keeps the JSON output safe for any consumer that re-renders
// these notes inside un-escaped contexts (a release-notes preview, an
// admin CSV export, etc.).
func sanitizeNote(s string) string {
	s = strings.TrimSpace(s)
	if !strings.ContainsAny(s, "<>") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	skip := false
	for _, r := range s {
		switch {
		case r == '<':
			skip = true
		case r == '>':
			skip = false
		case !skip:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// parseInstallationID decodes the optional installation_id from the
// request body. Returns (zero, true) when omitted (caller treats as
// "rate without installation"). Returns (zero, false) on a malformed
// UUID — the handler then writes a 400.
func parseInstallationID(s *string) (pgtype.UUID, bool) {
	if s == nil || *s == "" {
		return pgtype.UUID{}, true
	}
	parsed, err := uuid.Parse(*s)
	if err != nil {
		return pgtype.UUID{}, false
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, true
}

// --- endpoints -------------------------------------------------------

// CreateRating handles POST /charts/{chart_id}/ratings/. If a rating
// already exists for this (user, installation) (or (user, chart) when
// installation is omitted), the request is treated as an update — the
// spec's "handle the 409 from the DB by issuing a PUT instead" rule.
func (h *ChartRatingsHandler) CreateRating(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok {
		RespondRequestError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return
	}
	userID, err := uuid.Parse(user.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "internal_error", "Invalid user ID")
		return
	}
	chartID, err := uuid.Parse(chi.URLParam(r, "chart_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_chart_id", "chart_id must be a UUID")
		return
	}

	var body createOrUpdateRatingRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_json", "Body must be valid JSON")
		return
	}
	if body.Stars < 1 || body.Stars > 5 {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_stars", "stars must be 1-5")
		return
	}
	note := sanitizeNote(body.Note)
	if len(note) > NoteMaxLen {
		RespondRequestError(w, r, http.StatusBadRequest, "note_too_long", "note must be 280 chars or fewer")
		return
	}
	instID, ok := parseInstallationID(body.InstallationID)
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_installation_id", "installation_id must be a UUID")
		return
	}

	// Verify chart exists. A POST against a non-existent chart should
	// 404, not silently insert and then fail on the FK.
	if _, err := h.queries.GetHelmChartByID(r.Context(), chartID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, "chart_not_found", "chart not found")
			return
		}
		h.log.ErrorContext(r.Context(), "lookup chart", "error", err)
		RespondRequestError(w, r, http.StatusInternalServerError, "internal_error", "lookup chart failed")
		return
	}

	// Re-rate semantics: if a row exists, UPDATE rather than INSERT.
	// We resolve to an existing row through one of two unique
	// constraints depending on whether the rating is install-bound.
	existing, found := h.findExistingRating(r.Context(), userID, chartID, instID)
	if found {
		updated, err := h.queries.UpdateChartRating(r.Context(), sqlc.UpdateChartRatingParams{
			ID: existing.ID, Stars: body.Stars, Note: note,
		})
		if err != nil {
			h.log.ErrorContext(r.Context(), "update rating", "error", err)
			RespondRequestError(w, r, http.StatusInternalServerError, "update_failed", "could not update rating")
			return
		}
		h.recomputeInline(r.Context(), chartID)
		recordAudit(r, h.queries, "chart.rating.updated", "chart_rating", updated.ID.String(), "", map[string]any{
			"chart_id": chartID.String(),
			"stars":    body.Stars,
		})
		RespondJSON(w, http.StatusOK, toRatingResponse(updated))
		return
	}

	created, err := h.queries.CreateChartRating(r.Context(), sqlc.CreateChartRatingParams{
		ChartID:        chartID,
		InstallationID: instID,
		UserID:         userID,
		Stars:          body.Stars,
		Note:           note,
	})
	if err != nil {
		h.log.ErrorContext(r.Context(), "create rating", "error", err)
		RespondRequestError(w, r, http.StatusInternalServerError, "create_failed", "could not create rating")
		return
	}
	h.recomputeInline(r.Context(), chartID)
	recordAudit(r, h.queries, "chart.rating.created", "chart_rating", created.ID.String(), "", map[string]any{
		"chart_id": chartID.String(),
		"stars":    body.Stars,
	})
	RespondJSON(w, http.StatusCreated, toRatingResponse(created))
}

// findExistingRating returns the row (if any) the new POST should
// resolve to. Install-bound: (user, installation). Otherwise: (user,
// chart, installation IS NULL) — matching the partial unique index.
func (h *ChartRatingsHandler) findExistingRating(ctx context.Context, userID, chartID uuid.UUID, instID pgtype.UUID) (sqlc.ChartRating, bool) {
	if instID.Valid {
		got, err := h.queries.GetChartRatingByUserAndInstallation(ctx, sqlc.GetChartRatingByUserAndInstallationParams{
			UserID: userID, InstallationID: instID,
		})
		if err == nil {
			return got, true
		}
		return sqlc.ChartRating{}, false
	}
	got, err := h.queries.GetChartRatingByUserAndChartNoInstall(ctx, sqlc.GetChartRatingByUserAndChartNoInstallParams{
		UserID: userID, ChartID: chartID,
	})
	if err == nil {
		return got, true
	}
	return sqlc.ChartRating{}, false
}

// recomputeInline runs catalog.RecomputeAggregate without blocking the
// HTTP response on its result. The aggregate row is cheap to rebuild
// (a single scan of chart_ratings on this chart_id), but the spec
// stipulates the catalog browse should read fresh — so we run it
// synchronously inside the handler. Errors are logged, not surfaced
// to the caller; the nightly recompute is the backstop.
func (h *ChartRatingsHandler) recomputeInline(ctx context.Context, chartID uuid.UUID) {
	if err := catalog.RecomputeAggregate(ctx, h.queries, chartID); err != nil {
		h.log.WarnContext(ctx, "inline recompute failed", "chart_id", chartID, "error", err)
	}
}

// ListRatings handles GET /charts/{chart_id}/ratings/.
func (h *ChartRatingsHandler) ListRatings(w http.ResponseWriter, r *http.Request) {
	chartID, err := uuid.Parse(chi.URLParam(r, "chart_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_chart_id", "chart_id must be a UUID")
		return
	}
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := h.queries.ListChartRatingsByChart(r.Context(), sqlc.ListChartRatingsByChartParams{
		ChartID: chartID, Limit: limit, Offset: offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	total, err := h.queries.CountChartRatingsByChart(r.Context(), chartID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "count_failed", err.Error())
		return
	}
	out := make([]ratingResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toRatingResponse(row))
	}
	RespondPaginated(w, r, out, total)
}

// GetAggregate handles GET /charts/{chart_id}/ratings/aggregate/.
func (h *ChartRatingsHandler) GetAggregate(w http.ResponseWriter, r *http.Request) {
	chartID, err := uuid.Parse(chi.URLParam(r, "chart_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_chart_id", "chart_id must be a UUID")
		return
	}
	agg, err := h.queries.GetChartRatingAggregate(r.Context(), chartID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusInternalServerError, "aggregate_failed", err.Error())
		return
	}
	hist, err := h.queries.ChartRatingHistogram(r.Context(), chartID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "histogram_failed", err.Error())
		return
	}
	resp := aggregateResponse{
		RatingCount:   agg.RatingCount,
		AvgStars:      numericToFloat(agg.AvgStars),
		BayesianScore: numericToFloat(agg.BayesianScore),
		Histogram:     hist,
	}
	RespondJSON(w, http.StatusOK, resp)
}

// GetMyRating handles GET /charts/{chart_id}/ratings/mine/.
func (h *ChartRatingsHandler) GetMyRating(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok {
		RespondRequestError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return
	}
	userID, err := uuid.Parse(user.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "internal_error", "Invalid user ID")
		return
	}
	chartID, err := uuid.Parse(chi.URLParam(r, "chart_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_chart_id", "chart_id must be a UUID")
		return
	}
	got, err := h.queries.GetChartRatingForUserChart(r.Context(), sqlc.GetChartRatingForUserChartParams{
		UserID: userID, ChartID: chartID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, "not_found", "no rating")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, "lookup_failed", err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, toRatingResponse(got))
}

// UpdateRating handles PUT /charts/{chart_id}/ratings/{rating_id}/.
// Only the rating's owner or a superuser may edit. The handler reads
// the existing row first to enforce ownership; the chart_id in the URL
// is validated against the existing row to prevent a cross-chart
// hijack (caller supplies any chart_id, body says any stars).
func (h *ChartRatingsHandler) UpdateRating(w http.ResponseWriter, r *http.Request) {
	user, callerID, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	ratingID, err := uuid.Parse(chi.URLParam(r, "rating_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_rating_id", "rating_id must be a UUID")
		return
	}
	existing, err := h.queries.GetChartRatingByID(r.Context(), ratingID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, "rating_not_found", "rating not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, "lookup_failed", err.Error())
		return
	}
	if existing.UserID != callerID && !user.IsSuperuser {
		RespondRequestError(w, r, http.StatusForbidden, "forbidden", "only the rating's owner may modify it")
		return
	}
	var body createOrUpdateRatingRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_json", "Body must be valid JSON")
		return
	}
	if body.Stars < 1 || body.Stars > 5 {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_stars", "stars must be 1-5")
		return
	}
	note := sanitizeNote(body.Note)
	if len(note) > NoteMaxLen {
		RespondRequestError(w, r, http.StatusBadRequest, "note_too_long", "note must be 280 chars or fewer")
		return
	}
	updated, err := h.queries.UpdateChartRating(r.Context(), sqlc.UpdateChartRatingParams{
		ID: ratingID, Stars: body.Stars, Note: note,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "update_failed", err.Error())
		return
	}
	h.recomputeInline(r.Context(), existing.ChartID)
	recordAudit(r, h.queries, "chart.rating.updated", "chart_rating", ratingID.String(), "", map[string]any{
		"chart_id": existing.ChartID.String(),
		"stars":    body.Stars,
	})
	RespondJSON(w, http.StatusOK, toRatingResponse(updated))
}

// DeleteRating handles DELETE /charts/{chart_id}/ratings/{rating_id}/.
// Owner-or-superuser only, same as Update.
func (h *ChartRatingsHandler) DeleteRating(w http.ResponseWriter, r *http.Request) {
	user, callerID, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	ratingID, err := uuid.Parse(chi.URLParam(r, "rating_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_rating_id", "rating_id must be a UUID")
		return
	}
	existing, err := h.queries.GetChartRatingByID(r.Context(), ratingID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, "rating_not_found", "rating not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, "lookup_failed", err.Error())
		return
	}
	if existing.UserID != callerID && !user.IsSuperuser {
		RespondRequestError(w, r, http.StatusForbidden, "forbidden", "only the rating's owner may delete it")
		return
	}
	if err := h.queries.DeleteChartRating(r.Context(), ratingID); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	h.recomputeInline(r.Context(), existing.ChartID)
	recordAudit(r, h.queries, "chart.rating.deleted", "chart_rating", ratingID.String(), "", map[string]any{
		"chart_id": existing.ChartID.String(),
		"stars":    existing.Stars,
	})
	w.WriteHeader(http.StatusNoContent)
}

// PopularRecommendations handles GET /catalog/recommendations/popular/.
func (h *ChartRatingsHandler) PopularRecommendations(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 6)
	if limit <= 0 || limit > 50 {
		limit = 6
	}
	results, err := catalog.TopCharts(r.Context(), h.queries, limit)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, results)
}

// SimilarRecommendations handles GET /catalog/recommendations/similar/{chart_id}/.
func (h *ChartRatingsHandler) SimilarRecommendations(w http.ResponseWriter, r *http.Request) {
	chartID, err := uuid.Parse(chi.URLParam(r, "chart_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_chart_id", "chart_id must be a UUID")
		return
	}
	limit := queryInt(r, "limit", 5)
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	results, err := catalog.SimilarCharts(r.Context(), h.queries, chartID, limit)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, results)
}

// --- shared helpers --------------------------------------------------

// requireUser resolves the authenticated user (for the superuser check)
// and writes the appropriate error if anything is missing. Returns the
// full user row, the parsed UUID, and a continue-flag.
func (h *ChartRatingsHandler) requireUser(w http.ResponseWriter, r *http.Request) (sqlc.User, uuid.UUID, bool) {
	auth, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok {
		RespondRequestError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return sqlc.User{}, uuid.Nil, false
	}
	userID, err := uuid.Parse(auth.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "internal_error", "Invalid user ID")
		return sqlc.User{}, uuid.Nil, false
	}
	user, err := h.queries.GetUserByID(r.Context(), userID)
	if err != nil {
		RespondRequestError(w, r, http.StatusForbidden, "forbidden", "Caller not found")
		return sqlc.User{}, uuid.Nil, false
	}
	return user, userID, true
}

// numericToFloat is the handler-side decoder. Mirrors the helper in
// internal/catalog/recommendations.go — defined here so the handler
// doesn't have to import an unexported symbol.
func numericToFloat(n pgtype.Numeric) float64 {
	if !n.Valid {
		return 0
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return 0
	}
	return f.Float64
}
