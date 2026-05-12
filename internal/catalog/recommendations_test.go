package catalog

import (
	"context"
	"math"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeQuerier is the narrow in-memory Querier used by these unit tests.
// It only implements the subset of the catalog.Querier surface we need;
// each method short-circuits to a slice/map maintained by the test.
type fakeQuerier struct {
	ratings    []sqlc.ChartRating
	aggregates map[uuid.UUID]sqlc.ChartRatingAggregate
	pairs      []sqlc.InstalledChartChartPair
	coEdges    map[[2]uuid.UUID]int32 // canonicalized (a<b)
}

func newFakeQuerier() *fakeQuerier {
	return &fakeQuerier{
		aggregates: map[uuid.UUID]sqlc.ChartRatingAggregate{},
		coEdges:    map[[2]uuid.UUID]int32{},
	}
}

func (f *fakeQuerier) ListChartRatingsByChart(_ context.Context, arg sqlc.ListChartRatingsByChartParams) ([]sqlc.ChartRating, error) {
	out := []sqlc.ChartRating{}
	for _, r := range f.ratings {
		if r.ChartID == arg.ChartID {
			out = append(out, r)
		}
	}
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

func (f *fakeQuerier) CountChartRatingsByChart(_ context.Context, chartID uuid.UUID) (int64, error) {
	var n int64
	for _, r := range f.ratings {
		if r.ChartID == chartID {
			n++
		}
	}
	return n, nil
}

func (f *fakeQuerier) UpsertChartRatingAggregate(_ context.Context, arg sqlc.UpsertChartRatingAggregateParams) (sqlc.ChartRatingAggregate, error) {
	a := sqlc.ChartRatingAggregate{
		ChartID:       arg.ChartID,
		RatingCount:   arg.RatingCount,
		RatingSum:     arg.RatingSum,
		AvgStars:      arg.AvgStars,
		BayesianScore: arg.BayesianScore,
		UpdatedAt:     time.Now(),
	}
	f.aggregates[arg.ChartID] = a
	return a, nil
}

func (f *fakeQuerier) GetChartRatingAggregate(_ context.Context, chartID uuid.UUID) (sqlc.ChartRatingAggregate, error) {
	if a, ok := f.aggregates[chartID]; ok {
		return a, nil
	}
	return sqlc.ChartRatingAggregate{}, pgx.ErrNoRows
}

func (f *fakeQuerier) ListTopChartsByBayesian(_ context.Context, arg sqlc.ListTopChartsByBayesianParams) ([]sqlc.ChartRatingAggregate, error) {
	out := []sqlc.ChartRatingAggregate{}
	for _, a := range f.aggregates {
		if a.RatingCount >= arg.MinRatingCount {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return numericFloat(out[i].BayesianScore) > numericFloat(out[j].BayesianScore)
	})
	if int(arg.Limit) < len(out) {
		out = out[:arg.Limit]
	}
	return out, nil
}

func (f *fakeQuerier) ListChartCoInstallationsFor(_ context.Context, arg sqlc.ListChartCoInstallationsForParams) ([]sqlc.ChartCoInstallation, error) {
	out := []sqlc.ChartCoInstallation{}
	for key, w := range f.coEdges {
		if key[0] == arg.ChartID || key[1] == arg.ChartID {
			out = append(out, sqlc.ChartCoInstallation{
				ChartAID: key[0], ChartBID: key[1], Weight: w,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Weight > out[j].Weight })
	if int(arg.Limit) < len(out) {
		out = out[:arg.Limit]
	}
	return out, nil
}

func (f *fakeQuerier) TruncateChartCoInstallation(_ context.Context) error {
	f.coEdges = map[[2]uuid.UUID]int32{}
	return nil
}

func (f *fakeQuerier) UpsertChartCoInstallation(_ context.Context, arg sqlc.UpsertChartCoInstallationParams) error {
	key := [2]uuid.UUID{arg.ChartAID, arg.ChartBID}
	f.coEdges[key] = arg.Weight
	return nil
}

func (f *fakeQuerier) ListInstalledChartChartPairs(_ context.Context) ([]sqlc.InstalledChartChartPair, error) {
	return f.pairs, nil
}

func (f *fakeQuerier) ListDistinctRatedChartIDs(_ context.Context) ([]uuid.UUID, error) {
	seen := map[uuid.UUID]struct{}{}
	out := []uuid.UUID{}
	for _, r := range f.ratings {
		if _, ok := seen[r.ChartID]; ok {
			continue
		}
		seen[r.ChartID] = struct{}{}
		out = append(out, r.ChartID)
	}
	return out, nil
}

// --- tests -----------------------------------------------------------

func TestRecomputeAggregate_NoRatingsZeroes(t *testing.T) {
	q := newFakeQuerier()
	chartID := uuid.New()
	if err := RecomputeAggregate(context.Background(), q, chartID); err != nil {
		t.Fatalf("RecomputeAggregate: %v", err)
	}
	a, ok := q.aggregates[chartID]
	if !ok {
		t.Fatalf("aggregate row not written")
	}
	if a.RatingCount != 0 || a.RatingSum != 0 {
		t.Fatalf("expected zeroed aggregate, got count=%d sum=%d", a.RatingCount, a.RatingSum)
	}
	if got := numericFloat(a.AvgStars); got != 0 {
		t.Fatalf("expected avg 0 with no ratings, got %v", got)
	}
	if got := numericFloat(a.BayesianScore); got != 0 {
		t.Fatalf("expected bayesian 0 with no ratings, got %v", got)
	}
}

func TestRecomputeAggregate_BayesianPushesLowSampleTowardGlobal(t *testing.T) {
	// Two 5-stars and nothing else. Raw average = 5.0. With
	// weight=10, avgGlobal=4.0, minimum=1, the formula yields:
	//   ((4.0 * 10) + (10 + 1)) / (10 + 2) = 51/12 ≈ 4.25
	// — significantly below the raw 5.0, exactly the low-sample
	// pullback we want for the popular-list ordering.
	q := newFakeQuerier()
	chartID := uuid.New()
	for i := 0; i < 2; i++ {
		q.ratings = append(q.ratings, sqlc.ChartRating{
			ID:      uuid.New(),
			ChartID: chartID,
			UserID:  uuid.New(),
			Stars:   5,
		})
	}
	if err := RecomputeAggregate(context.Background(), q, chartID); err != nil {
		t.Fatalf("RecomputeAggregate: %v", err)
	}
	a := q.aggregates[chartID]
	avg := numericFloat(a.AvgStars)
	bayes := numericFloat(a.BayesianScore)
	if math.Abs(avg-5.0) > 0.01 {
		t.Fatalf("expected raw avg 5.0, got %v", avg)
	}
	// The Bayesian score MUST be strictly below the raw average and
	// noticeably above the global mean (it's not zero).
	if !(bayes < avg-0.5) {
		t.Fatalf("expected bayesian << avg, got bayes=%v avg=%v", bayes, avg)
	}
	if !(bayes > 4.0 && bayes < 4.5) {
		t.Fatalf("expected bayesian in (4.0, 4.5), got %v", bayes)
	}
}

func TestRecomputeAggregate_HighSampleDoesntDrift(t *testing.T) {
	// 100 4-star ratings. Raw avg = 4.0. The formula with weight=10
	// gives ((4*10) + (400 + 1))/(10 + 100) = 441/110 ≈ 4.009 —
	// essentially the raw mean, which is exactly what we want once
	// the sample size dwarfs the confidence weight.
	q := newFakeQuerier()
	chartID := uuid.New()
	for i := 0; i < 100; i++ {
		q.ratings = append(q.ratings, sqlc.ChartRating{
			ID:      uuid.New(),
			ChartID: chartID,
			UserID:  uuid.New(),
			Stars:   4,
		})
	}
	if err := RecomputeAggregate(context.Background(), q, chartID); err != nil {
		t.Fatalf("RecomputeAggregate: %v", err)
	}
	a := q.aggregates[chartID]
	avg := numericFloat(a.AvgStars)
	bayes := numericFloat(a.BayesianScore)
	if math.Abs(avg-4.0) > 0.01 {
		t.Fatalf("expected raw avg 4.0, got %v", avg)
	}
	if math.Abs(bayes-avg) > 0.05 {
		t.Fatalf("expected high-sample bayesian within 0.05 of raw, got bayes=%v avg=%v", bayes, avg)
	}
}

func TestRecomputeCoInstallation_BuildsEdgesForCoInstalled(t *testing.T) {
	q := newFakeQuerier()
	cluster := uuid.New()
	chartA := uuid.New()
	chartB := uuid.New()
	chartC := uuid.New()
	now := time.Now()
	// A and B installed 1 day apart — should pair.
	// A and C installed 60 days apart — should NOT pair.
	q.pairs = []sqlc.InstalledChartChartPair{
		{ClusterID: cluster, ChartID: chartA, CreatedAt: tsValid(now)},
		{ClusterID: cluster, ChartID: chartB, CreatedAt: tsValid(now.Add(24 * time.Hour))},
		{ClusterID: cluster, ChartID: chartC, CreatedAt: tsValid(now.Add(60 * 24 * time.Hour))},
	}
	if err := RecomputeCoInstallation(context.Background(), q); err != nil {
		t.Fatalf("RecomputeCoInstallation: %v", err)
	}
	a, b := chartA, chartB
	if compareUUID(a, b) > 0 {
		a, b = b, a
	}
	if w, ok := q.coEdges[[2]uuid.UUID{a, b}]; !ok || w != 1 {
		t.Fatalf("expected weight 1 for AB pair, got ok=%v w=%v", ok, w)
	}
	// A-C should not exist (outside window).
	ac, ca := chartA, chartC
	if compareUUID(ac, ca) > 0 {
		ac, ca = ca, ac
	}
	if _, ok := q.coEdges[[2]uuid.UUID{ac, ca}]; ok {
		t.Fatalf("A/C pair should not exist (60d apart)")
	}
}

func TestRecomputeCoInstallation_Idempotent(t *testing.T) {
	q := newFakeQuerier()
	cluster := uuid.New()
	a, b := uuid.New(), uuid.New()
	now := time.Now()
	q.pairs = []sqlc.InstalledChartChartPair{
		{ClusterID: cluster, ChartID: a, CreatedAt: tsValid(now)},
		{ClusterID: cluster, ChartID: b, CreatedAt: tsValid(now.Add(time.Hour))},
	}
	if err := RecomputeCoInstallation(context.Background(), q); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first := len(q.coEdges)
	if err := RecomputeCoInstallation(context.Background(), q); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(q.coEdges) != first {
		t.Fatalf("non-idempotent: edges went from %d to %d", first, len(q.coEdges))
	}
}

func TestTopCharts_FiltersByMinRatingCount(t *testing.T) {
	q := newFakeQuerier()
	popular := uuid.New()
	thin := uuid.New()
	q.aggregates[popular] = sqlc.ChartRatingAggregate{
		ChartID:       popular,
		RatingCount:   10,
		BayesianScore: mustNumeric(4.2),
		AvgStars:      mustNumeric(4.3),
	}
	q.aggregates[thin] = sqlc.ChartRatingAggregate{
		ChartID:       thin,
		RatingCount:   1,
		BayesianScore: mustNumeric(4.9),
		AvgStars:      mustNumeric(5.0),
	}
	got, err := TopCharts(context.Background(), q, 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ChartID != popular {
		t.Fatalf("expected single popular row, got %+v", got)
	}
}

func TestSimilarCharts_OrdersByWeightDesc(t *testing.T) {
	q := newFakeQuerier()
	target := uuid.New()
	a := uuid.New()
	b := uuid.New()
	// Canonicalize for storage.
	pair := func(x, y uuid.UUID) [2]uuid.UUID {
		if compareUUID(x, y) > 0 {
			x, y = y, x
		}
		return [2]uuid.UUID{x, y}
	}
	q.coEdges[pair(target, a)] = 2
	q.coEdges[pair(target, b)] = 7
	q.aggregates[a] = sqlc.ChartRatingAggregate{
		ChartID: a, RatingCount: 5,
		AvgStars: mustNumeric(4.0), BayesianScore: mustNumeric(4.0),
	}
	q.aggregates[b] = sqlc.ChartRatingAggregate{
		ChartID: b, RatingCount: 8,
		AvgStars: mustNumeric(4.5), BayesianScore: mustNumeric(4.4),
	}
	got, err := SimilarCharts(context.Background(), q, target, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	if got[0].ChartID != b {
		t.Fatalf("expected b first (weight 7), got %v", got[0].ChartID)
	}
	if got[0].Weight != 7 {
		t.Fatalf("expected weight 7, got %d", got[0].Weight)
	}
}

func TestSimilarCharts_GatesByMinRatingCount(t *testing.T) {
	q := newFakeQuerier()
	target := uuid.New()
	unrated := uuid.New()
	pair := [2]uuid.UUID{target, unrated}
	if compareUUID(pair[0], pair[1]) > 0 {
		pair[0], pair[1] = pair[1], pair[0]
	}
	q.coEdges[pair] = 5
	// Unrated chart has count below MinRatingsForSimilar.
	q.aggregates[unrated] = sqlc.ChartRatingAggregate{
		ChartID: unrated, RatingCount: 1,
		AvgStars: mustNumeric(5.0), BayesianScore: mustNumeric(4.9),
	}
	got, err := SimilarCharts(context.Background(), q, target, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected unrated chart filtered out, got %+v", got)
	}
}

// --- helpers ---------------------------------------------------------

func tsValid(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}
