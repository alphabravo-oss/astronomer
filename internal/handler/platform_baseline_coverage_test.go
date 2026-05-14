package handler

// Tests for the sprint 075 platform-baseline coverage endpoint.
//
// Coverage matrix:
//   - TestPlatformBaselineCoverage_AllResolved — all five slugs resolve;
//     missing_slugs is empty; each entry has chart_id + repository.
//   - TestPlatformBaselineCoverage_SomeMissing — two slugs are absent;
//     missing_slugs lists them in canonical order; resolved[] still has
//     all five entries with found=false for the missing ones.
//   - TestPlatformBaselineCoverage_RequiresSuperuser — a non-superuser
//     caller receives 403 with code=forbidden; no DB resolve calls are
//     made (gate runs first).
//   - TestPlatformBaselineCoverage_LookupErrorTreatedAsMissing — a non-
//     pgx error from ResolveChartByName (e.g. DB outage on a single
//     row) surfaces as not-resolved, not a 500, so the operator banner
//     can render "X/5 — catalog unreachable?" instead of crashing.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeCoverageQuerier is the in-memory PlatformBaselineCoverageQuerier
// used by these tests. slugs maps chart name -> resolution; absent keys
// resolve to sqlc.ErrCoverageSlugNotFound. errSlugs forces an arbitrary
// DB error for a specific slug (covers the "DB outage on one row" path).
type fakeCoverageQuerier struct {
	user      sqlc.User
	slugs     map[string]sqlc.ChartResolution
	errSlugs  map[string]error
	resolveHits int
}

func (f *fakeCoverageQuerier) GetUserByID(_ context.Context, id uuid.UUID) (sqlc.User, error) {
	if f.user.ID == id {
		return f.user, nil
	}
	return sqlc.User{}, errors.New("user not found")
}

func (f *fakeCoverageQuerier) ResolveChartByName(_ context.Context, name string) (sqlc.ChartResolution, error) {
	f.resolveHits++
	if err, ok := f.errSlugs[name]; ok {
		return sqlc.ChartResolution{}, err
	}
	if res, ok := f.slugs[name]; ok {
		return res, nil
	}
	return sqlc.ChartResolution{}, sqlc.ErrCoverageSlugNotFound
}

// compile-time: production *sqlc.Queries also satisfies the interface.
var _ PlatformBaselineCoverageQuerier = (*fakeCoverageQuerier)(nil)
var _ PlatformBaselineCoverageQuerier = (*sqlc.Queries)(nil)

func decodeCoverage(t *testing.T, body []byte) coverageResponse {
	t.Helper()
	var resp coverageResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode coverage response: %v (body=%s)", err, body)
	}
	return resp
}

func TestPlatformBaselineCoverage_AllResolved(t *testing.T) {
	callerID := uuid.New()
	trivyID, ksmID, neID, fbID, cmID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	q := &fakeCoverageQuerier{
		user: sqlc.User{ID: callerID, IsSuperuser: true},
		slugs: map[string]sqlc.ChartResolution{
			"trivy-operator":           {ChartID: trivyID, Repository: "aqua"},
			"kube-state-metrics":       {ChartID: ksmID, Repository: "prometheus-community"},
			"prometheus-node-exporter": {ChartID: neID, Repository: "prometheus-community"},
			"fluent-bit":               {ChartID: fbID, Repository: "fluent"},
			"cert-manager":             {ChartID: cmID, Repository: "jetstack"},
		},
	}
	h := NewPlatformBaselineCoverageHandler(q)

	w := httptest.NewRecorder()
	req := authedRequest(http.MethodGet, "/api/v1/admin/platform-settings/default-cluster-template/coverage/", callerID, nil)
	h.Coverage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	resp := decodeCoverage(t, w.Body.Bytes())
	if got, want := len(resp.ExpectedSlugs), 5; got != want {
		t.Fatalf("expected_slugs len = %d, want %d", got, want)
	}
	if got, want := len(resp.Resolved), 5; got != want {
		t.Fatalf("resolved len = %d, want %d", got, want)
	}
	if got, want := len(resp.MissingSlugs), 0; got != want {
		t.Fatalf("missing_slugs len = %d, want %d (missing=%v)", got, want, resp.MissingSlugs)
	}
	// Every entry must have found=true with both ChartID + Repository.
	for i, e := range resp.Resolved {
		if !e.Found {
			t.Errorf("resolved[%d] (%s) found=false, want true", i, e.Slug)
		}
		if e.ChartID == "" {
			t.Errorf("resolved[%d] (%s) chart_id empty", i, e.Slug)
		}
		if e.Repository == "" {
			t.Errorf("resolved[%d] (%s) repository empty", i, e.Slug)
		}
	}
	if q.resolveHits != 5 {
		t.Errorf("resolveHits = %d, want 5", q.resolveHits)
	}
}

func TestPlatformBaselineCoverage_SomeMissing(t *testing.T) {
	callerID := uuid.New()
	// Only three of the five slugs are present — node-exporter and
	// fluent-bit are deliberately omitted to exercise missing_slugs.
	q := &fakeCoverageQuerier{
		user: sqlc.User{ID: callerID, IsSuperuser: true},
		slugs: map[string]sqlc.ChartResolution{
			"trivy-operator":     {ChartID: uuid.New(), Repository: "aqua"},
			"kube-state-metrics": {ChartID: uuid.New(), Repository: "prometheus-community"},
			"cert-manager":       {ChartID: uuid.New(), Repository: "jetstack"},
		},
	}
	h := NewPlatformBaselineCoverageHandler(q)

	w := httptest.NewRecorder()
	req := authedRequest(http.MethodGet, "/coverage/", callerID, nil)
	h.Coverage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	resp := decodeCoverage(t, w.Body.Bytes())

	// missing_slugs must contain exactly the two omitted names, in the
	// canonical order of defaultBaselineSlugs (node-exporter then
	// fluent-bit). The order matters for stable frontend rendering.
	wantMissing := []string{"prometheus-node-exporter", "fluent-bit"}
	if len(resp.MissingSlugs) != len(wantMissing) {
		t.Fatalf("missing_slugs = %v, want %v", resp.MissingSlugs, wantMissing)
	}
	for i, s := range wantMissing {
		if resp.MissingSlugs[i] != s {
			t.Errorf("missing_slugs[%d] = %q, want %q", i, resp.MissingSlugs[i], s)
		}
	}

	// resolved still has all five — the two missing are found=false.
	if len(resp.Resolved) != 5 {
		t.Fatalf("resolved len = %d, want 5", len(resp.Resolved))
	}
	gotFound := map[string]bool{}
	for _, e := range resp.Resolved {
		gotFound[e.Slug] = e.Found
	}
	for _, s := range []string{"trivy-operator", "kube-state-metrics", "cert-manager"} {
		if !gotFound[s] {
			t.Errorf("resolved[%s] found=false, want true", s)
		}
	}
	for _, s := range wantMissing {
		if gotFound[s] {
			t.Errorf("resolved[%s] found=true, want false", s)
		}
	}
}

func TestPlatformBaselineCoverage_RequiresSuperuser(t *testing.T) {
	callerID := uuid.New()
	q := &fakeCoverageQuerier{
		user: sqlc.User{ID: callerID, IsSuperuser: false}, // ← not superuser
		slugs: map[string]sqlc.ChartResolution{
			"trivy-operator": {ChartID: uuid.New(), Repository: "aqua"},
		},
	}
	h := NewPlatformBaselineCoverageHandler(q)

	w := httptest.NewRecorder()
	req := authedRequest(http.MethodGet, "/coverage/", callerID, nil)
	h.Coverage(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
	// The gate must run BEFORE any catalog lookup — no DB calls.
	if q.resolveHits != 0 {
		t.Errorf("resolveHits = %d, want 0 (gate should short-circuit)", q.resolveHits)
	}
}

func TestPlatformBaselineCoverage_LookupErrorTreatedAsMissing(t *testing.T) {
	// A DB error on a single resolve must surface as "not resolved"
	// rather than crashing the whole endpoint with a 500 — the operator
	// banner is meant to be a robust diagnostic.
	callerID := uuid.New()
	q := &fakeCoverageQuerier{
		user: sqlc.User{ID: callerID, IsSuperuser: true},
		slugs: map[string]sqlc.ChartResolution{
			"trivy-operator":           {ChartID: uuid.New(), Repository: "aqua"},
			"kube-state-metrics":       {ChartID: uuid.New(), Repository: "prometheus-community"},
			"prometheus-node-exporter": {ChartID: uuid.New(), Repository: "prometheus-community"},
			"cert-manager":             {ChartID: uuid.New(), Repository: "jetstack"},
		},
		errSlugs: map[string]error{
			"fluent-bit": errors.New("connection refused"),
		},
	}
	h := NewPlatformBaselineCoverageHandler(q)

	w := httptest.NewRecorder()
	req := authedRequest(http.MethodGet, "/coverage/", callerID, nil)
	h.Coverage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (best-effort); body=%s", w.Code, w.Body.String())
	}
	resp := decodeCoverage(t, w.Body.Bytes())
	if len(resp.MissingSlugs) != 1 || resp.MissingSlugs[0] != "fluent-bit" {
		t.Fatalf("missing_slugs = %v, want [fluent-bit]", resp.MissingSlugs)
	}
}
