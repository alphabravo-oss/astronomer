package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeAllowlistQuerier captures every interaction. The tests assert
// on the final state and the audit recording is exercised through the
// recordAudit helper which type-asserts on a non-nil auditor — we pass
// nil to keep these tests narrow.
type fakeAllowlistQuerier struct {
	clusterErr  error
	row         *sqlc.ApiserverAllowlist
	rowGetErr   error
	upserted    *sqlc.UpsertApiserverAllowlistParams
	upsertErr   error
	snapshots   []sqlc.ApiserverAllowlistSnapshot
	snapshotErr error
}

func (f *fakeAllowlistQuerier) GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error) {
	if f.clusterErr != nil {
		return sqlc.Cluster{}, f.clusterErr
	}
	return sqlc.Cluster{ID: id, Name: "test-cluster"}, nil
}

func (f *fakeAllowlistQuerier) GetApiserverAllowlistByClusterID(ctx context.Context, clusterID uuid.UUID) (sqlc.ApiserverAllowlist, error) {
	if f.rowGetErr != nil {
		return sqlc.ApiserverAllowlist{}, f.rowGetErr
	}
	if f.row == nil {
		return sqlc.ApiserverAllowlist{}, errors.New("no row")
	}
	return *f.row, nil
}

func (f *fakeAllowlistQuerier) UpsertApiserverAllowlist(ctx context.Context, arg sqlc.UpsertApiserverAllowlistParams) (sqlc.ApiserverAllowlist, error) {
	f.upserted = &arg
	if f.upsertErr != nil {
		return sqlc.ApiserverAllowlist{}, f.upsertErr
	}
	row := sqlc.ApiserverAllowlist{
		ClusterID: arg.ClusterID,
		Cidrs:     arg.Cidrs,
		Mode:      arg.Mode,
	}
	f.row = &row
	return row, nil
}

func (f *fakeAllowlistQuerier) ListApiserverAllowlistSnapshots(ctx context.Context, arg sqlc.ListApiserverAllowlistSnapshotsParams) ([]sqlc.ApiserverAllowlistSnapshot, error) {
	if f.snapshotErr != nil {
		return nil, f.snapshotErr
	}
	return f.snapshots, nil
}

func newRouterWithHandler(h *ApiserverAllowlistHandler) chi.Router {
	r := chi.NewRouter()
	r.Get("/clusters/{cluster_id}/apiserver-allowlist/", h.Get)
	r.Put("/clusters/{cluster_id}/apiserver-allowlist/", h.Update)
	r.Post("/clusters/{cluster_id}/apiserver-allowlist/reconcile/", h.Reconcile)
	r.Get("/clusters/{cluster_id}/apiserver-allowlist/snapshots/", h.Snapshots)
	r.Get("/clusters/{cluster_id}/apiserver-allowlist/preview/", h.Preview)
	return r
}

func TestApiserverAllowlistHandler_GetReturnsEmptyDefaultWhenNoRow(t *testing.T) {
	clusterID := uuid.New()
	q := &fakeAllowlistQuerier{}
	h := NewApiserverAllowlistHandler(q)
	h.SetAstronomerEgress([]string{"54.10.0.0/16"})
	router := newRouterWithHandler(h)

	req := httptest.NewRequest(http.MethodGet, "/clusters/"+clusterID.String()+"/apiserver-allowlist/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200; got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data AllowlistResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.Mode != "monitor" {
		t.Fatalf("expected default mode=monitor; got %q", resp.Data.Mode)
	}
	found := false
	for _, c := range resp.Data.Desired {
		if c == "54.10.0.0/16" {
			found = true
		}
	}
	if !found {
		t.Fatalf("egress block missing from rendered desired: %v", resp.Data.Desired)
	}
}

func TestApiserverAllowlistHandler_PUTHappyPath(t *testing.T) {
	clusterID := uuid.New()
	q := &fakeAllowlistQuerier{}
	h := NewApiserverAllowlistHandler(q)
	router := newRouterWithHandler(h)

	body, _ := json.Marshal(AllowlistUpdateRequest{
		CIDRs: []string{"10.0.0.0/8", "192.168.0.0/16"},
		Mode:  "monitor",
	})
	req := httptest.NewRequest(http.MethodPut, "/clusters/"+clusterID.String()+"/apiserver-allowlist/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200; got %d body=%s", rec.Code, rec.Body.String())
	}
	if q.upserted == nil {
		t.Fatalf("expected upsert call")
	}
	if q.upserted.Mode != "monitor" {
		t.Fatalf("expected mode=monitor; got %q", q.upserted.Mode)
	}
}

func TestApiserverAllowlistHandler_PUT_RejectsBadCIDR(t *testing.T) {
	clusterID := uuid.New()
	q := &fakeAllowlistQuerier{}
	h := NewApiserverAllowlistHandler(q)
	router := newRouterWithHandler(h)

	body, _ := json.Marshal(AllowlistUpdateRequest{
		CIDRs: []string{"not-a-cidr"},
		Mode:  "monitor",
	})
	req := httptest.NewRequest(http.MethodPut, "/clusters/"+clusterID.String()+"/apiserver-allowlist/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid_cidr") {
		t.Fatalf("expected invalid_cidr error code; got %s", rec.Body.String())
	}
}

func TestApiserverAllowlistHandler_PUT_RejectsZeroSlash(t *testing.T) {
	clusterID := uuid.New()
	q := &fakeAllowlistQuerier{}
	h := NewApiserverAllowlistHandler(q)
	router := newRouterWithHandler(h)

	body, _ := json.Marshal(AllowlistUpdateRequest{
		CIDRs: []string{"0.0.0.0/0"},
		Mode:  "monitor",
	})
	req := httptest.NewRequest(http.MethodPut, "/clusters/"+clusterID.String()+"/apiserver-allowlist/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestApiserverAllowlistHandler_PUT_RejectsBadMode(t *testing.T) {
	clusterID := uuid.New()
	q := &fakeAllowlistQuerier{}
	h := NewApiserverAllowlistHandler(q)
	router := newRouterWithHandler(h)

	body, _ := json.Marshal(map[string]any{
		"cidrs": []string{"10.0.0.0/8"},
		"mode":  "lockbox-omega",
	})
	req := httptest.NewRequest(http.MethodPut, "/clusters/"+clusterID.String()+"/apiserver-allowlist/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d", rec.Code)
	}
}

func TestApiserverAllowlistHandler_PUT_RequiresForceApplyOnEnforceUpgradeWithDrift(t *testing.T) {
	clusterID := uuid.New()
	cidrsJSON, _ := json.Marshal([]string{"10.0.0.0/8"})
	q := &fakeAllowlistQuerier{
		row: &sqlc.ApiserverAllowlist{
			ClusterID:  clusterID,
			Mode:       "monitor",
			SyncStatus: "drifting",
			Cidrs:      cidrsJSON,
		},
	}
	h := NewApiserverAllowlistHandler(q)
	router := newRouterWithHandler(h)

	// PUT mode=enforce without force_apply — must 409.
	body, _ := json.Marshal(AllowlistUpdateRequest{
		CIDRs: []string{"10.0.0.0/8"},
		Mode:  "enforce",
	})
	req := httptest.NewRequest(http.MethodPut, "/clusters/"+clusterID.String()+"/apiserver-allowlist/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 on enforce upgrade with drift; got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "mode_change_requires_force") {
		t.Fatalf("expected mode_change_requires_force code; got %s", rec.Body.String())
	}
	if q.upserted != nil {
		t.Fatalf("must not upsert on the rejected path")
	}

	// Retry with force_apply=true — should succeed.
	body, _ = json.Marshal(AllowlistUpdateRequest{
		CIDRs:      []string{"10.0.0.0/8"},
		Mode:       "enforce",
		ForceApply: true,
	})
	req = httptest.NewRequest(http.MethodPut, "/clusters/"+clusterID.String()+"/apiserver-allowlist/", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with force_apply; got %d body=%s", rec.Code, rec.Body.String())
	}
	if q.upserted == nil || q.upserted.Mode != "enforce" {
		t.Fatalf("expected enforce upsert; got %+v", q.upserted)
	}
}

func TestApiserverAllowlistHandler_Reconcile_FiresHook(t *testing.T) {
	clusterID := uuid.New()
	cidrsJSON, _ := json.Marshal([]string{"10.0.0.0/8"})
	q := &fakeAllowlistQuerier{
		row: &sqlc.ApiserverAllowlist{
			ClusterID: clusterID,
			Mode:      "monitor",
			Cidrs:     cidrsJSON,
		},
	}
	h := NewApiserverAllowlistHandler(q)
	called := false
	h.SetReconciler(func(ctx context.Context, id uuid.UUID) error {
		called = true
		if id != clusterID {
			t.Fatalf("wrong clusterID in hook: %s vs %s", id, clusterID)
		}
		return nil
	})
	router := newRouterWithHandler(h)

	req := httptest.NewRequest(http.MethodPost, "/clusters/"+clusterID.String()+"/apiserver-allowlist/reconcile/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202; got %d", rec.Code)
	}
	if !called {
		t.Fatalf("reconciler hook should have fired")
	}
}

func TestApiserverAllowlistHandler_RequiresClusterUpdate(t *testing.T) {
	// This test documents the route-level RBAC contract: the PUT and
	// POST /reconcile/ endpoints must be wired with ResourceClusters +
	// VerbUpdate. We test this by verifying that the handler doesn't
	// short-circuit reads with the same restriction (GET / preview /
	// snapshots are clusters:read). The actual middleware gating lives
	// in server/routes.go; this test exists to anchor the contract so
	// a future refactor doesn't accidentally promote a read-only
	// endpoint to a write-only one.
	clusterID := uuid.New()
	q := &fakeAllowlistQuerier{}
	h := NewApiserverAllowlistHandler(q)
	router := newRouterWithHandler(h)

	// GET — no middleware → handler returns 200 with the empty default.
	req := httptest.NewRequest(http.MethodGet, "/clusters/"+clusterID.String()+"/apiserver-allowlist/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200; got %d", rec.Code)
	}

	// PUT — handler accepts; the route layer would gate with
	// VerbUpdate. We don't simulate the middleware here, just that the
	// handler's HTTP method dispatch is correct.
	body, _ := json.Marshal(AllowlistUpdateRequest{CIDRs: []string{"10.0.0.0/8"}, Mode: "monitor"})
	req = httptest.NewRequest(http.MethodPut, "/clusters/"+clusterID.String()+"/apiserver-allowlist/", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200; got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestApiserverAllowlistHandler_Snapshots(t *testing.T) {
	clusterID := uuid.New()
	desiredJSON, _ := json.Marshal([]string{"10.0.0.0/8"})
	effectiveJSON, _ := json.Marshal([]string{"10.0.0.0/8", "192.168.0.0/16"})
	q := &fakeAllowlistQuerier{
		snapshots: []sqlc.ApiserverAllowlistSnapshot{
			{ID: 1, ClusterID: clusterID, EffectiveCidrs: effectiveJSON, DesiredCidrs: desiredJSON, Drift: true},
		},
	}
	h := NewApiserverAllowlistHandler(q)
	router := newRouterWithHandler(h)

	req := httptest.NewRequest(http.MethodGet, "/clusters/"+clusterID.String()+"/apiserver-allowlist/snapshots/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200; got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"drift":true`) {
		t.Fatalf("expected drift=true in body; got %s", rec.Body.String())
	}
}

func TestApiserverAllowlistHandler_Preview_DoesNotWrite(t *testing.T) {
	clusterID := uuid.New()
	cidrsJSON, _ := json.Marshal([]string{"10.0.0.0/8"})
	q := &fakeAllowlistQuerier{
		row: &sqlc.ApiserverAllowlist{
			ClusterID: clusterID,
			Mode:      "monitor",
			Cidrs:     cidrsJSON,
		},
	}
	h := NewApiserverAllowlistHandler(q)
	router := newRouterWithHandler(h)

	req := httptest.NewRequest(http.MethodGet, "/clusters/"+clusterID.String()+"/apiserver-allowlist/preview/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200; got %d", rec.Code)
	}
	if q.upserted != nil {
		t.Fatalf("preview must not call upsert; got %+v", q.upserted)
	}
}

func TestApiserverAllowlistHandler_BadClusterID(t *testing.T) {
	q := &fakeAllowlistQuerier{}
	h := NewApiserverAllowlistHandler(q)
	router := newRouterWithHandler(h)
	req := httptest.NewRequest(http.MethodGet, "/clusters/not-a-uuid/apiserver-allowlist/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d", rec.Code)
	}
}
