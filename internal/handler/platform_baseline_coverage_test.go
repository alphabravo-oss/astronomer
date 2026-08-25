package handler

// Tests for the catalog-defined platform-baseline coverage endpoint.
//
// Coverage matrix:
//   - TestPlatformBaselineCoverage_AllResolved — every default-enabled catalog slug resolves;
//     missing_slugs is empty; each entry has chart_id + repository.
//   - TestPlatformBaselineCoverage_SomeMissing — one catalog slug is absent;
//     missing_slugs and resolved[] preserve catalog order.
//   - TestPlatformBaselineCoverage_RequiresSuperuser — a non-superuser
//     caller receives 403 with code=forbidden; no DB resolve calls are
//     made (gate runs first).
//   - TestPlatformBaselineCoverage_LookupErrorTreatedAsMissing — a non-
//     pgx error from ResolveChartByName (e.g. DB outage on a single
//     row) surfaces as not-resolved, not a 500, so the operator banner
//     can render a partial coverage result instead of crashing.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeCoverageQuerier is the in-memory PlatformBaselineCoverageQuerier
// used by these tests. slugs maps chart name -> resolution; absent keys
// resolve to sqlc.ErrCoverageSlugNotFound. errSlugs forces an arbitrary
// DB error for a specific slug (covers the "DB outage on one row" path).
type fakeCoverageQuerier struct {
	user        sqlc.User
	slugs       map[string]sqlc.ChartResolution
	errSlugs    map[string]error
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
	catalog, err := builtinbundles.Load()
	if err != nil {
		t.Fatal(err)
	}
	wantSlugs := make([]string, 0, len(catalog.Components))
	resolutions := make(map[string]sqlc.ChartResolution)
	for _, component := range catalog.Components {
		if !component.DefaultEnabled {
			continue
		}
		wantSlugs = append(wantSlugs, component.Slug)
		resolutions[component.Slug] = sqlc.ChartResolution{ChartID: uuid.New(), Repository: "embedded-catalog"}
	}
	if len(wantSlugs) == 0 {
		t.Fatal("embedded catalog has no default-enabled components")
	}
	callerID := uuid.New()
	q := &fakeCoverageQuerier{
		user:  sqlc.User{ID: callerID, IsSuperuser: true},
		slugs: resolutions,
	}
	h := NewPlatformBaselineCoverageHandler(q)

	w := httptest.NewRecorder()
	req := authedRequest(http.MethodGet, "/api/v1/admin/platform-settings/default-cluster-template/coverage/", callerID, nil)
	h.Coverage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	resp := decodeCoverage(t, w.Body.Bytes())
	if !reflect.DeepEqual(resp.ExpectedSlugs, wantSlugs) {
		t.Fatalf("expected_slugs = %v, want catalog defaults %v", resp.ExpectedSlugs, wantSlugs)
	}
	if got, want := len(resp.Resolved), len(wantSlugs); got != want {
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
	if q.resolveHits != len(wantSlugs) {
		t.Errorf("resolveHits = %d, want %d", q.resolveHits, len(wantSlugs))
	}
}

func TestPlatformBaselineCoverage_SomeMissing(t *testing.T) {
	expectedSlugs, err := loadDefaultBaselineSlugs()
	if err != nil {
		t.Fatal(err)
	}
	if len(expectedSlugs) < 2 {
		t.Fatalf("need at least two default-enabled catalog components, got %v", expectedSlugs)
	}
	missingSlug := expectedSlugs[len(expectedSlugs)-1]
	callerID := uuid.New()
	resolutions := make(map[string]sqlc.ChartResolution, len(expectedSlugs)-1)
	for _, slug := range expectedSlugs[:len(expectedSlugs)-1] {
		resolutions[slug] = sqlc.ChartResolution{ChartID: uuid.New(), Repository: "embedded-catalog"}
	}
	q := &fakeCoverageQuerier{
		user:  sqlc.User{ID: callerID, IsSuperuser: true},
		slugs: resolutions,
	}
	h := NewPlatformBaselineCoverageHandler(q)

	w := httptest.NewRecorder()
	req := authedRequest(http.MethodGet, "/coverage/", callerID, nil)
	h.Coverage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	resp := decodeCoverage(t, w.Body.Bytes())

	if !reflect.DeepEqual(resp.MissingSlugs, []string{missingSlug}) {
		t.Fatalf("missing_slugs = %v, want [%s]", resp.MissingSlugs, missingSlug)
	}

	if len(resp.Resolved) != len(expectedSlugs) {
		t.Fatalf("resolved len = %d, want %d", len(resp.Resolved), len(expectedSlugs))
	}
	gotFound := map[string]bool{}
	for _, e := range resp.Resolved {
		gotFound[e.Slug] = e.Found
	}
	for _, s := range expectedSlugs[:len(expectedSlugs)-1] {
		if !gotFound[s] {
			t.Errorf("resolved[%s] found=false, want true", s)
		}
	}
	if gotFound[missingSlug] {
		t.Errorf("resolved[%s] found=true, want false", missingSlug)
	}
}

func TestPlatformBaselineCoverage_RequiresSuperuser(t *testing.T) {
	callerID := uuid.New()
	q := &fakeCoverageQuerier{
		user:  sqlc.User{ID: callerID, IsSuperuser: false}, // ← not superuser
		slugs: map[string]sqlc.ChartResolution{},
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
	expectedSlugs, err := loadDefaultBaselineSlugs()
	if err != nil {
		t.Fatal(err)
	}
	if len(expectedSlugs) == 0 {
		t.Fatal("embedded catalog has no default-enabled components")
	}
	failedSlug := expectedSlugs[0]
	resolutions := make(map[string]sqlc.ChartResolution, len(expectedSlugs)-1)
	for _, slug := range expectedSlugs[1:] {
		resolutions[slug] = sqlc.ChartResolution{ChartID: uuid.New(), Repository: "embedded-catalog"}
	}
	q := &fakeCoverageQuerier{
		user:  sqlc.User{ID: callerID, IsSuperuser: true},
		slugs: resolutions,
		errSlugs: map[string]error{
			failedSlug: errors.New("connection refused"),
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
	if len(resp.MissingSlugs) != 1 || resp.MissingSlugs[0] != failedSlug {
		t.Fatalf("missing_slugs = %v, want [%s]", resp.MissingSlugs, failedSlug)
	}
}

func TestPlatformBaselineCoverageSourceDoesNotOwnLegacyMembership(t *testing.T) {
	source, err := os.ReadFile("platform_baseline_coverage.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, forbidden := range []string{"defaultBaselineSlugs", "trivy-operator", "fluent-bit", "cert-manager", "ingress-nginx", "gatekeeper"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("coverage handler still hard-codes legacy baseline member %q", forbidden)
		}
	}
	if !strings.Contains(text, "builtinbundles.Load()") || !strings.Contains(text, "component.DefaultEnabled") {
		t.Fatal("coverage handler must derive membership from default-enabled embedded catalog components")
	}
}
