package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeAnomalyQuerier implements handler.AnomalyBaselineQuerier with
// in-memory storage for the request-handler tests.
type fakeAnomalyQuerierForHandler struct {
	rows []sqlc.AnomalyBaseline
	err  error
}

func (f *fakeAnomalyQuerierForHandler) ListAnomalyBaselines(_ context.Context, arg sqlc.ListAnomalyBaselinesParams) ([]sqlc.AnomalyBaseline, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := f.rows
	if int(arg.Offset) > len(out) {
		return []sqlc.AnomalyBaseline{}, nil
	}
	out = out[arg.Offset:]
	if int(arg.Limit) < len(out) {
		out = out[:arg.Limit]
	}
	return out, nil
}

func (f *fakeAnomalyQuerierForHandler) GetAnomalyBaselineByID(_ context.Context, id uuid.UUID) (sqlc.AnomalyBaseline, error) {
	if f.err != nil {
		return sqlc.AnomalyBaseline{}, f.err
	}
	for _, r := range f.rows {
		if r.ID == id {
			return r, nil
		}
	}
	return sqlc.AnomalyBaseline{}, errors.New("not found")
}

func (f *fakeAnomalyQuerierForHandler) ListAnomalyBaselinesByCluster(_ context.Context, clusterID uuid.UUID) ([]sqlc.AnomalyBaseline, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := []sqlc.AnomalyBaseline{}
	for _, r := range f.rows {
		if r.ClusterID == clusterID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeAnomalyQuerierForHandler) CountAnomalyBaselines(_ context.Context) (int64, error) {
	return int64(len(f.rows)), nil
}

func sampleBaseline(id, clusterID uuid.UUID, metric string) sqlc.AnomalyBaseline {
	return sqlc.AnomalyBaseline{
		ID:            id,
		ClusterID:     clusterID,
		MetricName:    metric,
		WindowSeconds: 86400,
		SampleCount:   123,
		Mean:          50.5,
		Stddev:        4.25,
		MinValue:      12,
		MaxValue:      90,
		P50:           50,
		P95:           80,
		P99:           87,
		LastValue:     51.2,
		LastValueAt:   pgtype.Timestamptz{Time: time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC), Valid: true},
		RecentSamples: json.RawMessage("[]"),
		UpdatedAt:     time.Date(2026, 5, 12, 12, 5, 0, 0, time.UTC),
	}
}

func TestAnomalyHandler_List(t *testing.T) {
	clusterA := uuid.New()
	clusterB := uuid.New()
	q := &fakeAnomalyQuerierForHandler{
		rows: []sqlc.AnomalyBaseline{
			sampleBaseline(uuid.New(), clusterA, "cluster_cpu_percent"),
			sampleBaseline(uuid.New(), clusterB, "cluster_memory_percent"),
		},
	}
	h := NewAnomalyHandler(q)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/anomaly-baselines/", nil)
	w := httptest.NewRecorder()
	h.List(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	got := decodeAnomalyListBody(t, w.Body.Bytes())
	if len(got) != 2 {
		t.Fatalf("rows: want 2, got %d", len(got))
	}
}

func TestAnomalyHandler_ListFiltersByCluster(t *testing.T) {
	clusterA := uuid.New()
	clusterB := uuid.New()
	q := &fakeAnomalyQuerierForHandler{
		rows: []sqlc.AnomalyBaseline{
			sampleBaseline(uuid.New(), clusterA, "cluster_cpu_percent"),
			sampleBaseline(uuid.New(), clusterB, "cluster_memory_percent"),
		},
	}
	h := NewAnomalyHandler(q)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/anomaly-baselines/?clusterId="+clusterA.String(), nil)
	w := httptest.NewRecorder()
	h.List(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", w.Code)
	}
	got := decodeAnomalyListBody(t, w.Body.Bytes())
	if len(got) != 1 {
		t.Fatalf("filtered rows: want 1, got %d", len(got))
	}
	if got[0]["clusterId"] != clusterA.String() {
		t.Fatalf("clusterId in response: want %s, got %v", clusterA.String(), got[0]["clusterId"])
	}
}

func TestAnomalyHandler_GetByID(t *testing.T) {
	clusterA := uuid.New()
	row := sampleBaseline(uuid.New(), clusterA, "cluster_cpu_percent")
	q := &fakeAnomalyQuerierForHandler{rows: []sqlc.AnomalyBaseline{row}}
	h := NewAnomalyHandler(q)

	r := chi.NewRouter()
	r.Get("/{id}", h.Get)

	req := httptest.NewRequest(http.MethodGet, "/"+row.ID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	got := decodeAnomalyBody(t, w.Body.Bytes())
	if got["metric"] != "cluster_cpu_percent" {
		t.Fatalf("metric: want cluster_cpu_percent, got %v", got["metric"])
	}
	// mean is JSON-encoded as a number; numeric comparison after cast.
	if mean, ok := got["mean"].(float64); !ok || mean != 50.5 {
		t.Fatalf("mean: want 50.5, got %v", got["mean"])
	}
}

func TestAnomalyHandler_GetReturns404OnMissing(t *testing.T) {
	q := &fakeAnomalyQuerierForHandler{rows: nil}
	h := NewAnomalyHandler(q)

	r := chi.NewRouter()
	r.Get("/{id}", h.Get)

	req := httptest.NewRequest(http.MethodGet, "/"+uuid.New().String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d", w.Code)
	}
}

func TestAnomalyHandler_RejectsInvalidClusterFilter(t *testing.T) {
	h := NewAnomalyHandler(&fakeAnomalyQuerierForHandler{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/anomaly-baselines/?clusterId=not-a-uuid", nil)
	w := httptest.NewRecorder()
	h.List(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", w.Code)
	}
}

// TestAnomalyHandler_ValidateCreateRequest covers the request-body
// validation hook added to the AlertingHandler's CreateRule path —
// the rule_kind="anomaly" path requires a metric. The validation
// runs entirely on the request value, so we can drive it with a
// stub-free direct call.
func TestAnomalyHandler_ValidateCreateRequest(t *testing.T) {
	cases := []struct {
		name    string
		req     CreateAlertRuleRequest
		wantErr bool
	}{
		{"non-anomaly rule passes through", CreateAlertRuleRequest{RuleKind: "threshold"}, false},
		{"anomaly without metric fails", CreateAlertRuleRequest{RuleKind: "anomaly"}, true},
		{"anomaly with metric passes", CreateAlertRuleRequest{RuleKind: "anomaly", Metric: "cluster_cpu_percent"}, false},
		{"anomaly with bad direction fails", CreateAlertRuleRequest{RuleKind: "anomaly", Metric: "cpu", AnomalyDirection: "sideways"}, true},
		{"anomaly with stddev<=0 fails", CreateAlertRuleRequest{RuleKind: "anomaly", Metric: "cpu", AnomalyStddev: floatPtr(0)}, true},
		{"anomaly with window<=0 fails", CreateAlertRuleRequest{RuleKind: "anomaly", Metric: "cpu", AnomalyWindowSeconds: int32Ptr(0)}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := validateAnomalyRuleRequest(tc.req)
			if (msg != "") != tc.wantErr {
				t.Fatalf("validateAnomalyRuleRequest msg=%q wantErr=%v", msg, tc.wantErr)
			}
		})
	}
}

func floatPtr(f float64) *float64 { return &f }
func int32Ptr(i int32) *int32     { return &i }

// decodeAnomalyListBody unwraps the {"data": [...]} envelope into a
// slice of maps. RespondJSON wraps everything in {"data": ...}; the
// tests need plain []map[string]any to assert against.
func decodeAnomalyListBody(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode list body: %v (raw=%s)", err, string(body))
	}
	return env.Data
}

func decodeAnomalyBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode body: %v (raw=%s)", err, string(body))
	}
	return env.Data
}
