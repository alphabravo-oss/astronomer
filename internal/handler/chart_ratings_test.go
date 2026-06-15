package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// fakeRatingsQuerier is the in-memory implementation of ChartRatingsQuerier
// used by these handler tests. It does NOT exercise SQL — every method is
// a map / slice operation — so the tests stay fast and avoid a Postgres
// dependency. The unique-index re-rating semantic is reproduced here via
// findExisting in the create path.
type fakeRatingsQuerier struct {
	ratings    map[uuid.UUID]sqlc.ChartRating
	aggregates map[uuid.UUID]sqlc.ChartRatingAggregate
	coEdges    map[[2]uuid.UUID]int32
	charts     map[uuid.UUID]sqlc.HelmChart
	users      map[uuid.UUID]sqlc.User
}

func newFakeRatingsQuerier() *fakeRatingsQuerier {
	return &fakeRatingsQuerier{
		ratings:    map[uuid.UUID]sqlc.ChartRating{},
		aggregates: map[uuid.UUID]sqlc.ChartRatingAggregate{},
		coEdges:    map[[2]uuid.UUID]int32{},
		charts:     map[uuid.UUID]sqlc.HelmChart{},
		users:      map[uuid.UUID]sqlc.User{},
	}
}

// catalog.Querier methods (delegating to the same maps).
func (f *fakeRatingsQuerier) ListChartRatingsByChart(_ context.Context, arg sqlc.ListChartRatingsByChartParams) ([]sqlc.ChartRating, error) {
	out := []sqlc.ChartRating{}
	for _, r := range f.ratings {
		if r.ChartID == arg.ChartID {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	from := int(arg.Offset)
	if from > len(out) {
		return []sqlc.ChartRating{}, nil
	}
	to := from + int(arg.Limit)
	if to > len(out) {
		to = len(out)
	}
	return out[from:to], nil
}

func (f *fakeRatingsQuerier) CountChartRatingsByChart(_ context.Context, chartID uuid.UUID) (int64, error) {
	var n int64
	for _, r := range f.ratings {
		if r.ChartID == chartID {
			n++
		}
	}
	return n, nil
}

func (f *fakeRatingsQuerier) UpsertChartRatingAggregate(_ context.Context, arg sqlc.UpsertChartRatingAggregateParams) (sqlc.ChartRatingAggregate, error) {
	a := sqlc.ChartRatingAggregate{
		ChartID: arg.ChartID, RatingCount: arg.RatingCount, RatingSum: arg.RatingSum,
		AvgStars: arg.AvgStars, BayesianScore: arg.BayesianScore, UpdatedAt: time.Now(),
	}
	f.aggregates[arg.ChartID] = a
	return a, nil
}

func (f *fakeRatingsQuerier) GetChartRatingAggregate(_ context.Context, chartID uuid.UUID) (sqlc.ChartRatingAggregate, error) {
	if a, ok := f.aggregates[chartID]; ok {
		return a, nil
	}
	return sqlc.ChartRatingAggregate{}, pgx.ErrNoRows
}

func (f *fakeRatingsQuerier) ListTopChartsByBayesian(_ context.Context, arg sqlc.ListTopChartsByBayesianParams) ([]sqlc.ChartRatingAggregate, error) {
	out := []sqlc.ChartRatingAggregate{}
	for _, a := range f.aggregates {
		if a.RatingCount >= arg.MinRatingCount {
			out = append(out, a)
		}
	}
	if int(arg.Limit) < len(out) {
		out = out[:arg.Limit]
	}
	return out, nil
}

func (f *fakeRatingsQuerier) ListChartCoInstallationsFor(_ context.Context, arg sqlc.ListChartCoInstallationsForParams) ([]sqlc.ChartCoInstallation, error) {
	out := []sqlc.ChartCoInstallation{}
	for key, w := range f.coEdges {
		if key[0] == arg.ChartID || key[1] == arg.ChartID {
			out = append(out, sqlc.ChartCoInstallation{ChartAID: key[0], ChartBID: key[1], Weight: w})
		}
	}
	return out, nil
}

func (f *fakeRatingsQuerier) TruncateChartCoInstallation(_ context.Context) error {
	f.coEdges = map[[2]uuid.UUID]int32{}
	return nil
}

func (f *fakeRatingsQuerier) UpsertChartCoInstallation(_ context.Context, arg sqlc.UpsertChartCoInstallationParams) error {
	f.coEdges[[2]uuid.UUID{arg.ChartAID, arg.ChartBID}] = arg.Weight
	return nil
}

func (f *fakeRatingsQuerier) ListInstalledChartChartPairs(_ context.Context) ([]sqlc.InstalledChartChartPair, error) {
	return nil, nil
}

func (f *fakeRatingsQuerier) ListDistinctRatedChartIDs(_ context.Context) ([]uuid.UUID, error) {
	seen := map[uuid.UUID]struct{}{}
	out := []uuid.UUID{}
	for _, r := range f.ratings {
		if _, ok := seen[r.ChartID]; !ok {
			seen[r.ChartID] = struct{}{}
			out = append(out, r.ChartID)
		}
	}
	return out, nil
}

// Rating CRUD methods.
func (f *fakeRatingsQuerier) CreateChartRating(_ context.Context, arg sqlc.CreateChartRatingParams) (sqlc.ChartRating, error) {
	// Enforce UNIQUE (user_id, installation_id) in fake.
	for _, r := range f.ratings {
		if r.UserID == arg.UserID && r.InstallationID == arg.InstallationID && arg.InstallationID.Valid {
			return sqlc.ChartRating{}, errors.New("duplicate rating")
		}
		// Partial-index path: same user + same chart + both NULL inst.
		if r.UserID == arg.UserID && r.ChartID == arg.ChartID &&
			!r.InstallationID.Valid && !arg.InstallationID.Valid {
			return sqlc.ChartRating{}, errors.New("duplicate rating (partial index)")
		}
	}
	row := sqlc.ChartRating{
		ID: uuid.New(), ChartID: arg.ChartID, InstallationID: arg.InstallationID,
		UserID: arg.UserID, Stars: arg.Stars, Note: arg.Note,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	f.ratings[row.ID] = row
	return row, nil
}

func (f *fakeRatingsQuerier) UpdateChartRating(_ context.Context, arg sqlc.UpdateChartRatingParams) (sqlc.ChartRating, error) {
	r, ok := f.ratings[arg.ID]
	if !ok {
		return sqlc.ChartRating{}, pgx.ErrNoRows
	}
	r.Stars = arg.Stars
	r.Note = arg.Note
	r.UpdatedAt = time.Now()
	f.ratings[arg.ID] = r
	return r, nil
}

func (f *fakeRatingsQuerier) GetChartRatingByID(_ context.Context, id uuid.UUID) (sqlc.ChartRating, error) {
	if r, ok := f.ratings[id]; ok {
		return r, nil
	}
	return sqlc.ChartRating{}, pgx.ErrNoRows
}

func (f *fakeRatingsQuerier) GetChartRatingByUserAndInstallation(_ context.Context, arg sqlc.GetChartRatingByUserAndInstallationParams) (sqlc.ChartRating, error) {
	for _, r := range f.ratings {
		if r.UserID == arg.UserID && r.InstallationID == arg.InstallationID {
			return r, nil
		}
	}
	return sqlc.ChartRating{}, pgx.ErrNoRows
}

func (f *fakeRatingsQuerier) GetChartRatingByUserAndChartNoInstall(_ context.Context, arg sqlc.GetChartRatingByUserAndChartNoInstallParams) (sqlc.ChartRating, error) {
	for _, r := range f.ratings {
		if r.UserID == arg.UserID && r.ChartID == arg.ChartID && !r.InstallationID.Valid {
			return r, nil
		}
	}
	return sqlc.ChartRating{}, pgx.ErrNoRows
}

func (f *fakeRatingsQuerier) GetChartRatingForUserChart(_ context.Context, arg sqlc.GetChartRatingForUserChartParams) (sqlc.ChartRating, error) {
	for _, r := range f.ratings {
		if r.UserID == arg.UserID && r.ChartID == arg.ChartID {
			return r, nil
		}
	}
	return sqlc.ChartRating{}, pgx.ErrNoRows
}

func (f *fakeRatingsQuerier) DeleteChartRating(_ context.Context, id uuid.UUID) error {
	delete(f.ratings, id)
	return nil
}

func (f *fakeRatingsQuerier) ChartRatingHistogram(_ context.Context, chartID uuid.UUID) ([5]int64, error) {
	var out [5]int64
	for _, r := range f.ratings {
		if r.ChartID == chartID && r.Stars >= 1 && r.Stars <= 5 {
			out[r.Stars-1]++
		}
	}
	return out, nil
}

func (f *fakeRatingsQuerier) GetHelmChartByID(_ context.Context, id uuid.UUID) (sqlc.HelmChart, error) {
	if c, ok := f.charts[id]; ok {
		return c, nil
	}
	return sqlc.HelmChart{}, pgx.ErrNoRows
}

func (f *fakeRatingsQuerier) GetUserByID(_ context.Context, id uuid.UUID) (sqlc.User, error) {
	if u, ok := f.users[id]; ok {
		return u, nil
	}
	return sqlc.User{}, pgx.ErrNoRows
}

// --- helpers ---------------------------------------------------------

func newRatingsHandler(t *testing.T) (*ChartRatingsHandler, *fakeRatingsQuerier, uuid.UUID, uuid.UUID) {
	t.Helper()
	q := newFakeRatingsQuerier()
	chartID := uuid.New()
	q.charts[chartID] = sqlc.HelmChart{ID: chartID, Name: "demo"}
	userID := uuid.New()
	q.users[userID] = sqlc.User{ID: userID, IsSuperuser: false}
	return NewChartRatingsHandler(q), q, chartID, userID
}

// doAuth wraps a request with an injected authenticated user. chi
// routing is bound up via NewRouter so chi.URLParam resolves correctly.
func doAuth(method, path string, body []byte, userID uuid.UUID) *http.Request {
	var rdr *bytes.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	var req *http.Request
	if rdr != nil {
		req = httptest.NewRequest(method, path, rdr)
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	ctx := middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID: userID.String(), AuthMethod: "jwt",
	})
	return req.WithContext(ctx)
}

func mountRatings(h *ChartRatingsHandler) http.Handler {
	r := chi.NewRouter()
	r.Route("/charts/{chart_id}/ratings", func(r chi.Router) {
		r.Post("/", h.CreateRating)
		r.Get("/", h.ListRatings)
		r.Get("/aggregate/", h.GetAggregate)
		r.Get("/mine/", h.GetMyRating)
		r.Put("/{rating_id}/", h.UpdateRating)
		r.Delete("/{rating_id}/", h.DeleteRating)
	})
	r.Route("/catalog/recommendations", func(r chi.Router) {
		r.Get("/popular/", h.PopularRecommendations)
		r.Get("/similar/{chart_id}/", h.SimilarRecommendations)
	})
	return r
}

// --- tests -----------------------------------------------------------

func TestChartRatingsHandler_CreateRating_InsertsRow(t *testing.T) {
	h, q, chartID, userID := newRatingsHandler(t)
	router := mountRatings(h)
	body, _ := json.Marshal(map[string]any{"stars": 5, "note": "Excellent"})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, doAuth(http.MethodPost, "/charts/"+chartID.String()+"/ratings/", body, userID))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if len(q.ratings) != 1 {
		t.Fatalf("expected 1 rating, got %d", len(q.ratings))
	}
	// Aggregate should have been recomputed inline.
	if a, ok := q.aggregates[chartID]; !ok || a.RatingCount != 1 {
		t.Fatalf("aggregate not refreshed inline: %+v", a)
	}
}

func TestChartRatingsHandler_CreateRating_UpdatesViaUniqueIndex(t *testing.T) {
	// Re-rating the same chart (no installation_id) must UPDATE the
	// existing row, not INSERT a new one. We verify both via the row
	// count AND by asserting the returned id matches the first row's.
	h, q, chartID, userID := newRatingsHandler(t)
	router := mountRatings(h)

	body1, _ := json.Marshal(map[string]any{"stars": 3, "note": "Meh"})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, doAuth(http.MethodPost, "/charts/"+chartID.String()+"/ratings/", body1, userID))
	if rr.Code != http.StatusCreated {
		t.Fatalf("first POST status = %d", rr.Code)
	}
	var first struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode first: %v", err)
	}

	body2, _ := json.Marshal(map[string]any{"stars": 5, "note": "Updated"})
	rr2 := httptest.NewRecorder()
	router.ServeHTTP(rr2, doAuth(http.MethodPost, "/charts/"+chartID.String()+"/ratings/", body2, userID))
	if rr2.Code != http.StatusOK {
		t.Fatalf("re-rate status = %d body=%s", rr2.Code, rr2.Body.String())
	}
	if len(q.ratings) != 1 {
		t.Fatalf("expected 1 row after re-rate, got %d", len(q.ratings))
	}
	var second struct {
		Data struct {
			ID    string `json:"id"`
			Stars int    `json:"stars"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr2.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if second.Data.ID != first.Data.ID {
		t.Fatalf("expected same id %s, got %s", first.Data.ID, second.Data.ID)
	}
	if second.Data.Stars != 5 {
		t.Fatalf("expected stars=5, got %d", second.Data.Stars)
	}
}

func TestChartRatingsHandler_RequiresAuth(t *testing.T) {
	h, _, chartID, _ := newRatingsHandler(t)
	router := mountRatings(h)
	body, _ := json.Marshal(map[string]any{"stars": 5})
	// No auth context attached — the request should be rejected.
	req := httptest.NewRequest(http.MethodPost, "/charts/"+chartID.String()+"/ratings/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestChartRatingsHandler_OnlyOwnerCanEditExceptSuperuser(t *testing.T) {
	h, q, chartID, ownerID := newRatingsHandler(t)
	router := mountRatings(h)

	// Create as owner.
	body, _ := json.Marshal(map[string]any{"stars": 3})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, doAuth(http.MethodPost, "/charts/"+chartID.String()+"/ratings/", body, ownerID))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d", rr.Code)
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ratingID := created.Data.ID

	// A non-owner non-superuser must be forbidden.
	strangerID := uuid.New()
	q.users[strangerID] = sqlc.User{ID: strangerID, IsSuperuser: false}
	put, _ := json.Marshal(map[string]any{"stars": 1})
	rr2 := httptest.NewRecorder()
	router.ServeHTTP(rr2, doAuth(http.MethodPut, "/charts/"+chartID.String()+"/ratings/"+ratingID+"/", put, strangerID))
	if rr2.Code != http.StatusForbidden {
		t.Fatalf("stranger PUT status = %d, want 403", rr2.Code)
	}

	// A superuser passes.
	suID := uuid.New()
	q.users[suID] = sqlc.User{ID: suID, IsSuperuser: true}
	rr3 := httptest.NewRecorder()
	router.ServeHTTP(rr3, doAuth(http.MethodPut, "/charts/"+chartID.String()+"/ratings/"+ratingID+"/", put, suID))
	if rr3.Code != http.StatusOK {
		t.Fatalf("superuser PUT status = %d body=%s", rr3.Code, rr3.Body.String())
	}
}

func TestChartRatingsHandler_NoteTooLong400(t *testing.T) {
	h, _, chartID, userID := newRatingsHandler(t)
	router := mountRatings(h)
	long := strings.Repeat("x", 281)
	body, _ := json.Marshal(map[string]any{"stars": 4, "note": long})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, doAuth(http.MethodPost, "/charts/"+chartID.String()+"/ratings/", body, userID))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestChartRatingsHandler_OneRatingPerUserPerInstallation(t *testing.T) {
	// Two POSTs from the same user with the same installation_id must
	// converge to a single row. This is the unique-index path
	// distinct from the no-installation partial-index case tested
	// earlier (which uses CreateChartRating's no-install branch).
	h, q, chartID, userID := newRatingsHandler(t)
	router := mountRatings(h)
	instID := uuid.New().String()

	body1, _ := json.Marshal(map[string]any{"stars": 2, "installation_id": instID})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, doAuth(http.MethodPost, "/charts/"+chartID.String()+"/ratings/", body1, userID))
	if rr.Code != http.StatusCreated {
		t.Fatalf("first status = %d", rr.Code)
	}

	body2, _ := json.Marshal(map[string]any{"stars": 4, "installation_id": instID})
	rr2 := httptest.NewRecorder()
	router.ServeHTTP(rr2, doAuth(http.MethodPost, "/charts/"+chartID.String()+"/ratings/", body2, userID))
	if rr2.Code != http.StatusOK {
		t.Fatalf("second status = %d body=%s", rr2.Code, rr2.Body.String())
	}
	if len(q.ratings) != 1 {
		t.Fatalf("expected 1 row, got %d", len(q.ratings))
	}
	for _, r := range q.ratings {
		if r.Stars != 4 {
			t.Fatalf("expected stars=4 after re-rate, got %d", r.Stars)
		}
	}
}

func TestChartRatingsHandler_InvalidStarsRejected(t *testing.T) {
	h, _, chartID, userID := newRatingsHandler(t)
	router := mountRatings(h)
	for _, s := range []int{0, 6, -1, 99} {
		body, _ := json.Marshal(map[string]any{"stars": s})
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, doAuth(http.MethodPost, "/charts/"+chartID.String()+"/ratings/", body, userID))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("stars=%d status = %d, want 400", s, rr.Code)
		}
	}
}

func TestChartRatingsHandler_AggregateIncludesHistogram(t *testing.T) {
	h, q, chartID, userID := newRatingsHandler(t)
	router := mountRatings(h)
	for _, s := range []int{5, 5, 4} {
		q.ratings[uuid.New()] = sqlc.ChartRating{
			ID: uuid.New(), ChartID: chartID, UserID: userID, Stars: int16(s),
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
	}
	// Pre-populate aggregate (would normally be set by inline recompute).
	q.aggregates[chartID] = sqlc.ChartRatingAggregate{
		ChartID: chartID, RatingCount: 3, RatingSum: 14,
		AvgStars: numericOf(4.67), BayesianScore: numericOf(4.4),
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, doAuth(http.MethodGet, "/charts/"+chartID.String()+"/ratings/aggregate/", nil, userID))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		Data struct {
			RatingCount int      `json:"rating_count"`
			Histogram   [5]int64 `json:"histogram"`
			AvgStars    float64  `json:"avg_stars"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Data.Histogram[4] != 2 || out.Data.Histogram[3] != 1 {
		t.Fatalf("histogram mismatch: %v", out.Data.Histogram)
	}
}

// numericOf is the test-side mirror of catalog.mustNumeric. The
// catalog helper is unexported so we re-derive here against the
// pgtype.Numeric struct directly.
func numericOf(v float64) pgtype.Numeric {
	var n pgtype.Numeric
	_ = n.Scan(formatTwo(v))
	return n
}

// formatTwo: format a float with two decimals — replicates the exact
// string contract the prod recompute writes to NUMERIC(*, 2).
func formatTwo(v float64) string {
	const dec = 100.0
	r := int64(v*dec + 0.5)
	whole := r / int64(dec)
	frac := r % int64(dec)
	if frac < 10 {
		return formatInt(whole) + ".0" + formatInt(frac)
	}
	return formatInt(whole) + "." + formatInt(frac)
}

func formatInt(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	out := string(buf[i:])
	if neg {
		return "-" + out
	}
	return out
}
