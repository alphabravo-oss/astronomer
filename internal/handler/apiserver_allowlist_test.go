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
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
)

// fakeAllowlistQuerier captures every interaction, including audit rows
// emitted through the handler's optional auditor surface.
type fakeAllowlistQuerier struct {
	fakeOperationIdempotencyStore
	clusterErr   error
	cluster      sqlc.Cluster
	row          *sqlc.ApiserverAllowlist
	rowGetErr    error
	upserted     *sqlc.UpsertApiserverAllowlistParams
	upsertErr    error
	snapshots    []sqlc.ApiserverAllowlistSnapshot
	snapshotErr  error
	snapshotPage sqlc.ListApiserverAllowlistSnapshotsParams
}

func (f *fakeAllowlistQuerier) GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error) {
	if f.clusterErr != nil {
		return sqlc.Cluster{}, f.clusterErr
	}
	cluster := f.cluster
	if cluster.ID == uuid.Nil {
		cluster.ID = id
	}
	if cluster.Name == "" {
		cluster.Name = "test-cluster"
	}
	return cluster, nil
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
	f.snapshotPage = arg
	if f.snapshotErr != nil {
		return nil, f.snapshotErr
	}
	start := min(int(arg.Offset), len(f.snapshots))
	end := min(start+int(arg.Limit), len(f.snapshots))
	return f.snapshots[start:end], nil
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
	q := &transactionalAllowlistQ{fakeAllowlistQuerier: &fakeAllowlistQuerier{}}
	h := transactionalAllowlistHandler(q)
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
	q := &transactionalAllowlistQ{fakeAllowlistQuerier: &fakeAllowlistQuerier{}}
	h := transactionalAllowlistHandler(q)
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
	if len(q.auditRows) != 1 {
		t.Fatalf("audit rows=%d want 1", len(q.auditRows))
	}
	row := q.auditRows[0]
	if row.Action != "cluster.apiserver_allowlist.updated" || row.ResourceType != "cluster" || row.ResourceID != clusterID.String() {
		t.Fatalf("audit row=%+v, want cluster.apiserver_allowlist.updated on cluster %s", row, clusterID)
	}
	assertAuditDetail(t, row.Detail, "mode", "monitor")
	if bytes.Contains(row.Detail, []byte("10.0.0.0/8")) || bytes.Contains(row.Detail, []byte("192.168.0.0/16")) {
		t.Fatalf("audit detail leaked cleartext CIDR list: %s", row.Detail)
	}
	var detail map[string]any
	if err := json.Unmarshal(row.Detail, &detail); err != nil {
		t.Fatalf("decode audit detail: %v", err)
	}
	if detail["cidrs_hash"] == "" {
		t.Fatalf("audit detail missing cidrs_hash: %v", detail)
	}
	if got := detail["cidrs_count"]; got != float64(2) {
		t.Fatalf("audit cidrs_count=%v want 2", got)
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
	q := &transactionalAllowlistQ{fakeAllowlistQuerier: &fakeAllowlistQuerier{
		cluster: sqlc.Cluster{ID: clusterID, Provider: "eks"},
		row: &sqlc.ApiserverAllowlist{
			ClusterID:  clusterID,
			Mode:       "monitor",
			SyncStatus: "drifting",
			Cidrs:      cidrsJSON,
		},
	}}
	h := transactionalAllowlistHandler(q)
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

func TestApiserverAllowlistHandler_PUTRejectsUnsupportedEnforcement(t *testing.T) {
	clusterID := uuid.New()
	q := &fakeAllowlistQuerier{cluster: sqlc.Cluster{ID: clusterID, Provider: "self_managed"}}
	h := NewApiserverAllowlistHandler(q)
	router := newRouterWithHandler(h)
	body, _ := json.Marshal(AllowlistUpdateRequest{CIDRs: []string{"10.0.0.0/8"}, Mode: "enforce"})
	req := httptest.NewRequest(http.MethodPut, "/clusters/"+clusterID.String()+"/apiserver-allowlist/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "unsupported_provider") {
		t.Fatalf("expected 422 unsupported_provider; got %d body=%s", rec.Code, rec.Body.String())
	}
	if q.upserted != nil {
		t.Fatalf("unsupported enforce mode must not be persisted")
	}
}

func TestApiserverAllowlistHandler_Reconcile_FailsClosedWithoutTransaction(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodPost, "/clusters/"+clusterID.String()+"/apiserver-allowlist/reconcile/", nil)
	req.Header.Set("Idempotency-Key", "allowlist-reconcile-fail-closed")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503; got %d", rec.Code)
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
	q := &transactionalAllowlistQ{fakeAllowlistQuerier: &fakeAllowlistQuerier{}}
	h := transactionalAllowlistHandler(q)
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

func TestAllowlistSnapshotPaginationContract(t *testing.T) {
	clusterID := uuid.New()
	for _, tc := range []struct {
		name, query   string
		limit, offset int
		wantIDs       []int64
		hasMore       bool
	}{
		{"first", "?limit=2", 2, 0, []int64{1, 2}, true},
		{"middle", "?limit=2&offset=2", 2, 2, []int64{3, 4}, true},
		{"last", "?limit=2&offset=4", 2, 4, []int64{5}, false},
		{"empty", "?limit=2&offset=5", 2, 5, []int64{}, false},
		{"clamped", "?limit=999999&offset=-1", 200, 0, []int64{1, 2, 3, 4, 5}, false},
		{"overflow", "?offset=9223372036854775807", 20, int(maxPaginationOffset), []int64{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := &fakeAllowlistQuerier{}
			for id := int64(1); id <= 5; id++ {
				q.snapshots = append(q.snapshots, sqlc.ApiserverAllowlistSnapshot{ID: id, ClusterID: clusterID})
			}
			rec := httptest.NewRecorder()
			newRouterWithHandler(NewApiserverAllowlistHandler(q)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/clusters/"+clusterID.String()+"/apiserver-allowlist/snapshots/"+tc.query, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if q.snapshotPage.ClusterID != clusterID || q.snapshotPage.Limit != int32(tc.limit+1) || q.snapshotPage.Offset != int32(tc.offset) {
				t.Fatalf("query=%+v", q.snapshotPage)
			}
			var page paging.Response[SnapshotResponseEntry]
			if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			if page.Data == nil || len(page.Data) != len(tc.wantIDs) {
				t.Fatalf("rows=%+v want IDs=%v", page.Data, tc.wantIDs)
			}
			for i, id := range tc.wantIDs {
				if page.Data[i].ID != id {
					t.Fatalf("row %d ID=%d want %d", i, page.Data[i].ID, id)
				}
			}
			if page.Pagination.Total != nil || page.Pagination.Limit != tc.limit || page.Pagination.Offset != tc.offset || page.Pagination.HasMore != tc.hasMore {
				t.Fatalf("metadata=%+v", page.Pagination)
			}
			if tc.hasMore {
				if page.Pagination.NextOffset == nil || *page.Pagination.NextOffset != tc.offset+len(tc.wantIDs) {
					t.Fatalf("continuation=%+v", page.Pagination)
				}
			} else if page.Pagination.NextOffset != nil {
				t.Fatalf("unexpected continuation=%+v", page.Pagination)
			}
			var shape map[string]json.RawMessage
			if err := json.Unmarshal(rec.Body.Bytes(), &shape); err != nil {
				t.Fatal(err)
			}
			if len(shape) != 2 || shape["data"] == nil || shape["pagination"] == nil {
				t.Fatalf("noncanonical response=%s", rec.Body.String())
			}
		})
	}
}

func TestAllowlistSnapshotQueryFailureDoesNotReturnEmptyPage(t *testing.T) {
	q := &fakeAllowlistQuerier{snapshotErr: errors.New("database unavailable")}
	rec := httptest.NewRecorder()
	newRouterWithHandler(NewApiserverAllowlistHandler(q)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/clusters/"+uuid.NewString()+"/apiserver-allowlist/snapshots/", nil))
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), `"pagination"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
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
