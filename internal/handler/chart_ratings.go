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

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
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
	GetHelmRepositoryByID(ctx context.Context, id uuid.UUID) (sqlc.HelmRepository, error)
	GetCatalogVisibilityForProject(ctx context.Context, projectID, catalogID uuid.UUID) (sqlc.CatalogVisibility, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
}

// ChartRatingMutationTx is the complete transaction-bound surface for a
// rating write. The locked reads keep authorization and upsert decisions true
// until commit; catalog.Querier keeps aggregate maintenance in that same
// transaction; OutboxQuerier makes audit evidence mandatory.
type ChartRatingMutationTx interface {
	ChartRatingsQuerier
	LockChartRatingMutationKey(context.Context, string) error
	GetChartRatingByIDForUpdate(context.Context, uuid.UUID) (sqlc.ChartRating, error)
	GetChartRatingByUserAndInstallationForUpdate(context.Context, sqlc.GetChartRatingByUserAndInstallationParams) (sqlc.ChartRating, error)
	GetChartRatingByUserAndChartNoInstallForUpdate(context.Context, sqlc.GetChartRatingByUserAndChartNoInstallParams) (sqlc.ChartRating, error)
	GetUserByIDForUpdate(context.Context, uuid.UUID) (sqlc.User, error)
	audit.OutboxQuerier
}

type chartRatingRunTxFunc func(context.Context, func(ChartRatingMutationTx) error) error

type chartRatingMutationResult struct {
	rating sqlc.ChartRating
	action string
	status int
}

var (
	errChartRatingChartNotFound = errors.New("chart rating chart not found")
	errChartRatingNotFound      = errors.New("chart rating not found")
	errChartRatingCallerMissing = errors.New("chart rating caller not found")
	errChartRatingForbidden     = errors.New("chart rating mutation forbidden")
	errChartRatingConflict      = errors.New("chart rating conflicts with chart")
)

// ChartRatingsHandler handles ratings + recommendation HTTP endpoints.
// It does not run any background work; rating writes call
// catalog.RecomputeAggregate inline so the hot-path browse never reads
// a stale aggregate, and the worker handles the nightly co-installation
// matrix rebuild.
type ChartRatingsHandler struct {
	queries ChartRatingsQuerier
	log     *slog.Logger
	authz   authorizationSupport
	runTx   chartRatingRunTxFunc
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

func (h *ChartRatingsHandler) SetAuthorization(engine *rbac.Engine, querier middleware.RBACQuerier) {
	h.authz.SetAuthorization(engine, querier)
}

func (h *ChartRatingsHandler) SetRunTx(runTx chartRatingRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *ChartRatingsHandler) TransactionalAuditWired() bool {
	return h != nil && h.runTx != nil
}

// executeChartRatingMutation is the only rating-write commit boundary. A
// missing runner fails closed: production must never fall back to independent
// domain, aggregate, and best-effort audit writes.
func executeChartRatingMutation(
	r *http.Request,
	h *ChartRatingsHandler,
	mutate func(ChartRatingMutationTx) (chartRatingMutationResult, error),
) (chartRatingMutationResult, error) {
	var result chartRatingMutationResult
	if h == nil || h.runTx == nil {
		return result, audit.ErrOutboxUnavailable
	}
	err := h.runTx(r.Context(), func(q ChartRatingMutationTx) error {
		var mutationErr error
		result, mutationErr = mutate(q)
		if mutationErr != nil {
			return mutationErr
		}
		if err := catalog.RecomputeAggregate(r.Context(), q, result.rating.ChartID); err != nil {
			return err
		}
		return recordAuditOutbox(r, q, result.action, "chart_rating", result.rating.ID.String(), "", result.status, map[string]any{
			"chart_id": result.rating.ChartID.String(),
			"stars":    result.rating.Stars,
		})
	})
	return result, err
}

func respondChartRatingMutationError(w http.ResponseWriter, r *http.Request, err error, forbiddenMessage, fallbackCode, fallbackMessage string) {
	switch {
	case errors.Is(err, errChartRatingChartNotFound):
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "chart not found")
	case errors.Is(err, errChartRatingNotFound):
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "rating not found")
	case errors.Is(err, errChartRatingCallerMissing):
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Caller not found")
	case errors.Is(err, errChartRatingForbidden):
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, forbiddenMessage)
	case errors.Is(err, errChartRatingConflict):
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "rating does not belong to this chart")
	default:
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, fallbackCode, fallbackMessage)
	}
}

// --- request / response payloads -------------------------------------

// openapi:request ChartRatingWriteRequest
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
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}
	userID, err := uuid.Parse(user.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Invalid user ID")
		return
	}
	chartID, err := uuid.Parse(chi.URLParam(r, "chart_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "chart_id must be a UUID")
		return
	}

	var body createOrUpdateRatingRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Body must be valid JSON")
		return
	}
	if body.Stars < 1 || body.Stars > 5 {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidStars, "stars must be 1-5")
		return
	}
	note := sanitizeNote(body.Note)
	if len(note) > NoteMaxLen {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.NoteTooLong, "note must be 280 chars or fewer")
		return
	}
	instID, ok := parseInstallationID(body.InstallationID)
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "installation_id must be a UUID")
		return
	}

	result, err := executeChartRatingMutation(r, h, func(q ChartRatingMutationTx) (chartRatingMutationResult, error) {
		if err := q.LockChartRatingMutationKey(r.Context(), "chart-rating:chart:"+chartID.String()); err != nil {
			return chartRatingMutationResult{}, err
		}
		if err := q.LockChartRatingMutationKey(r.Context(), chartRatingNaturalLockKey(userID, chartID, instID)); err != nil {
			return chartRatingMutationResult{}, err
		}
		if _, err := q.GetHelmChartByID(r.Context(), chartID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return chartRatingMutationResult{}, errChartRatingChartNotFound
			}
			return chartRatingMutationResult{}, err
		}

		existing, found, err := findExistingRatingForUpdate(r.Context(), q, userID, chartID, instID)
		if err != nil {
			return chartRatingMutationResult{}, err
		}
		if found {
			// An installation-bound rating is globally unique for the user. Do
			// not let a mismatched chart URL silently update another chart.
			if existing.ChartID != chartID {
				return chartRatingMutationResult{}, errChartRatingConflict
			}
			updated, err := q.UpdateChartRating(r.Context(), sqlc.UpdateChartRatingParams{
				ID: existing.ID, Stars: body.Stars, Note: note,
			})
			return chartRatingMutationResult{rating: updated, action: "chart.rating.updated", status: http.StatusOK}, err
		}

		created, err := q.CreateChartRating(r.Context(), sqlc.CreateChartRatingParams{
			ChartID: chartID, InstallationID: instID, UserID: userID,
			Stars: body.Stars, Note: note,
		})
		return chartRatingMutationResult{rating: created, action: "chart.rating.created", status: http.StatusCreated}, err
	})
	if err != nil {
		h.log.ErrorContext(r.Context(), "commit rating upsert", "error", err)
		respondChartRatingMutationError(w, r, err, "only the rating's owner may modify it", apierror.CreateError, "could not create or update rating")
		return
	}
	if result.status == http.StatusCreated {
		w.Header().Set("Location", "/charts/"+chartID.String()+"/ratings/"+result.rating.ID.String()+"/")
	}
	RespondJSON(w, result.status, toRatingResponse(result.rating))
}

func chartRatingNaturalLockKey(userID, chartID uuid.UUID, instID pgtype.UUID) string {
	if instID.Valid {
		return "chart-rating:user-installation:" + userID.String() + ":" + uuid.UUID(instID.Bytes).String()
	}
	return "chart-rating:user-chart:" + userID.String() + ":" + chartID.String()
}

// findExistingRatingForUpdate repeats POST's state-dependent upsert lookup
// after the natural-key lock has been acquired inside the transaction.
func findExistingRatingForUpdate(ctx context.Context, q ChartRatingMutationTx, userID, chartID uuid.UUID, instID pgtype.UUID) (sqlc.ChartRating, bool, error) {
	var (
		got sqlc.ChartRating
		err error
	)
	if instID.Valid {
		got, err = q.GetChartRatingByUserAndInstallationForUpdate(ctx, sqlc.GetChartRatingByUserAndInstallationParams{UserID: userID, InstallationID: instID})
	} else {
		got, err = q.GetChartRatingByUserAndChartNoInstallForUpdate(ctx, sqlc.GetChartRatingByUserAndChartNoInstallParams{UserID: userID, ChartID: chartID})
	}
	if err == nil {
		return got, true, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.ChartRating{}, false, nil
	}
	return sqlc.ChartRating{}, false, err
}

// ListRatings handles GET /charts/{chart_id}/ratings/.
func (h *ChartRatingsHandler) ListRatings(w http.ResponseWriter, r *http.Request) {
	chartID, err := uuid.Parse(chi.URLParam(r, "chart_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "chart_id must be a UUID")
		return
	}
	limit := int32(queryLimit(r, 20))
	offset := int32(queryInt(r, "offset", 0))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := h.queries.ListChartRatingsByChart(r.Context(), sqlc.ListChartRatingsByChartParams{
		ChartID: chartID, Limit: limit, Offset: offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, err.Error())
		return
	}
	total, err := h.queries.CountChartRatingsByChart(r.Context(), chartID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, err.Error())
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "chart_id must be a UUID")
		return
	}
	agg, err := h.queries.GetChartRatingAggregate(r.Context(), chartID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.AggregateError, err.Error())
		return
	}
	hist, err := h.queries.ChartRatingHistogram(r.Context(), chartID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.HistogramFailed, err.Error())
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
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}
	userID, err := uuid.Parse(user.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Invalid user ID")
		return
	}
	chartID, err := uuid.Parse(chi.URLParam(r, "chart_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "chart_id must be a UUID")
		return
	}
	got, err := h.queries.GetChartRatingForUserChart(r.Context(), sqlc.GetChartRatingForUserChartParams{
		UserID: userID, ChartID: chartID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "no rating")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.LookupError, err.Error())
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
	callerID, ok := requireChartRatingUserID(w, r)
	if !ok {
		return
	}
	chartID, err := uuid.Parse(chi.URLParam(r, "chart_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "chart_id must be a UUID")
		return
	}
	ratingID, err := uuid.Parse(chi.URLParam(r, "rating_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "rating_id must be a UUID")
		return
	}
	var body createOrUpdateRatingRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Body must be valid JSON")
		return
	}
	if body.Stars < 1 || body.Stars > 5 {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidStars, "stars must be 1-5")
		return
	}
	note := sanitizeNote(body.Note)
	if len(note) > NoteMaxLen {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.NoteTooLong, "note must be 280 chars or fewer")
		return
	}
	result, err := executeChartRatingMutation(r, h, func(q ChartRatingMutationTx) (chartRatingMutationResult, error) {
		if err := q.LockChartRatingMutationKey(r.Context(), "chart-rating:chart:"+chartID.String()); err != nil {
			return chartRatingMutationResult{}, err
		}
		user, err := q.GetUserByIDForUpdate(r.Context(), callerID)
		if errors.Is(err, pgx.ErrNoRows) {
			return chartRatingMutationResult{}, errChartRatingCallerMissing
		}
		if err != nil {
			return chartRatingMutationResult{}, err
		}
		existing, err := q.GetChartRatingByIDForUpdate(r.Context(), ratingID)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && existing.ChartID != chartID) {
			return chartRatingMutationResult{}, errChartRatingNotFound
		}
		if err != nil {
			return chartRatingMutationResult{}, err
		}
		if existing.UserID != callerID && !user.IsSuperuser {
			return chartRatingMutationResult{}, errChartRatingForbidden
		}
		updated, err := q.UpdateChartRating(r.Context(), sqlc.UpdateChartRatingParams{ID: ratingID, Stars: body.Stars, Note: note})
		return chartRatingMutationResult{rating: updated, action: "chart.rating.updated", status: http.StatusOK}, err
	})
	if err != nil {
		respondChartRatingMutationError(w, r, err, "only the rating's owner may modify it", apierror.UpdateError, "could not update rating")
		return
	}
	RespondJSON(w, http.StatusOK, toRatingResponse(result.rating))
}

// DeleteRating handles DELETE /charts/{chart_id}/ratings/{rating_id}/.
// Owner-or-superuser only, same as Update.
func (h *ChartRatingsHandler) DeleteRating(w http.ResponseWriter, r *http.Request) {
	callerID, ok := requireChartRatingUserID(w, r)
	if !ok {
		return
	}
	chartID, err := uuid.Parse(chi.URLParam(r, "chart_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "chart_id must be a UUID")
		return
	}
	ratingID, err := uuid.Parse(chi.URLParam(r, "rating_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "rating_id must be a UUID")
		return
	}
	_, err = executeChartRatingMutation(r, h, func(q ChartRatingMutationTx) (chartRatingMutationResult, error) {
		if err := q.LockChartRatingMutationKey(r.Context(), "chart-rating:chart:"+chartID.String()); err != nil {
			return chartRatingMutationResult{}, err
		}
		user, err := q.GetUserByIDForUpdate(r.Context(), callerID)
		if errors.Is(err, pgx.ErrNoRows) {
			return chartRatingMutationResult{}, errChartRatingCallerMissing
		}
		if err != nil {
			return chartRatingMutationResult{}, err
		}
		existing, err := q.GetChartRatingByIDForUpdate(r.Context(), ratingID)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && existing.ChartID != chartID) {
			return chartRatingMutationResult{}, errChartRatingNotFound
		}
		if err != nil {
			return chartRatingMutationResult{}, err
		}
		if existing.UserID != callerID && !user.IsSuperuser {
			return chartRatingMutationResult{}, errChartRatingForbidden
		}
		if err := q.DeleteChartRating(r.Context(), ratingID); err != nil {
			return chartRatingMutationResult{}, err
		}
		return chartRatingMutationResult{rating: existing, action: "chart.rating.deleted", status: http.StatusNoContent}, nil
	})
	if err != nil {
		respondChartRatingMutationError(w, r, err, "only the rating's owner may delete it", apierror.DeleteError, "could not delete rating")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PopularRecommendations handles GET /catalog/recommendations/popular/.
func (h *ChartRatingsHandler) PopularRecommendations(w http.ResponseWriter, r *http.Request) {
	limit := queryLimit(r, 6)
	if limit <= 0 || limit > 50 {
		limit = 6
	}
	projectID, projectScoped, ok := catalogProjectQuery(w, r)
	if !ok {
		return
	}
	if projectScoped {
		if !h.authz.authorizeProjectAction(w, r, projectID, rbac.ResourceCatalog, rbac.VerbRead) {
			return
		}
	} else if !h.authz.authorizeGlobalAction(w, r, rbac.ResourceCatalog, rbac.VerbRead) {
		return
	}
	results, err := catalog.TopCharts(r.Context(), h.queries, 50)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, err.Error())
		return
	}
	// TopCharts is limit-capped; omit an inexact total.
	results = h.filterVisibleRecommendations(r.Context(), results, projectID, projectScoped, limit)
	RespondList(w, results, NewPaginationFromPage(limit, 0, len(results)))
}

// SimilarRecommendations handles GET /catalog/recommendations/similar/{chart_id}/.
func (h *ChartRatingsHandler) SimilarRecommendations(w http.ResponseWriter, r *http.Request) {
	chartID, err := uuid.Parse(chi.URLParam(r, "chart_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "chart_id must be a UUID")
		return
	}
	projectID, projectScoped, ok := catalogProjectQuery(w, r)
	if !ok || !h.authorizeRecommendationChart(w, r, chartID, projectID, projectScoped) {
		return
	}
	limit := queryLimit(r, 5)
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	results, err := catalog.SimilarCharts(r.Context(), h.queries, chartID, 20)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, err.Error())
		return
	}
	// SimilarCharts is limit-capped; omit an inexact total.
	results = h.filterVisibleRecommendations(r.Context(), results, projectID, projectScoped, limit)
	RespondList(w, results, NewPaginationFromPage(limit, 0, len(results)))
}

func (h *ChartRatingsHandler) authorizeRecommendationChart(w http.ResponseWriter, r *http.Request, chartID, projectID uuid.UUID, projectScoped bool) bool {
	if projectScoped {
		if !h.authz.authorizeProjectAction(w, r, projectID, rbac.ResourceCatalog, rbac.VerbRead) {
			return false
		}
	} else if !h.authz.authorizeGlobalAction(w, r, rbac.ResourceCatalog, rbac.VerbRead) {
		return false
	}
	chart, err := h.queries.GetHelmChartByID(r.Context(), chartID)
	if err != nil || !h.recommendationChartVisible(r.Context(), chart, projectID, projectScoped) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "chart not found")
		return false
	}
	return true
}

func (h *ChartRatingsHandler) filterVisibleRecommendations(ctx context.Context, scores []catalog.ChartScore, projectID uuid.UUID, projectScoped bool, limit int) []catalog.ChartScore {
	visible := make([]catalog.ChartScore, 0, min(limit, len(scores)))
	for _, score := range scores {
		chart, err := h.queries.GetHelmChartByID(ctx, score.ChartID)
		if err != nil || !h.recommendationChartVisible(ctx, chart, projectID, projectScoped) {
			continue
		}
		visible = append(visible, score)
		if len(visible) == limit {
			break
		}
	}
	return visible
}

func (h *ChartRatingsHandler) recommendationChartVisible(ctx context.Context, chart sqlc.HelmChart, projectID uuid.UUID, projectScoped bool) bool {
	repository, err := h.queries.GetHelmRepositoryByID(ctx, chart.RepositoryID)
	if err != nil {
		return false
	}
	if !projectScoped {
		return !repository.OwnerProjectID.Valid
	}
	visibility, err := h.queries.GetCatalogVisibilityForProject(ctx, projectID, repository.ID)
	return err == nil && catalogVisibilityAllowsRead(visibility)
}

// --- shared helpers --------------------------------------------------

// requireUser resolves the authenticated user (for the superuser check)
// and writes the appropriate error if anything is missing. Returns the
// full user row, the parsed UUID, and a continue-flag.
func requireChartRatingUserID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	auth, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return uuid.Nil, false
	}
	userID, err := uuid.Parse(auth.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Invalid user ID")
		return uuid.Nil, false
	}
	return userID, true
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
