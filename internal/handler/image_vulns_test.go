package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// Test-naming convention: every TestHandler_* MUST be prefixed
// TestImageVulnHandler_* to avoid collision with concurrent agents on
// the same package (per the sprint brief).

// stubVulnQuerier is the in-memory ImageVulnQuerier used by every test
// here. It models just enough of the schema to exercise the handler's
// branching paths.
type stubVulnQuerier struct {
	reports        map[uuid.UUID]sqlc.ImageVulnerabilityReport
	cves           map[uuid.UUID][]sqlc.ImageVulnerability
	getErr         error
	aggregateErr   error
	listErr        error
	listNsErr      error
	listCVEErr     error
	countErr       error
	clusterAgg     sqlc.AggregateClusterVulnerabilitiesRow
	fleetAgg       sqlc.AggregateFleetVulnerabilitiesRow
	topByCluster   []sqlc.ImageVulnerabilityReport
	topByNS        []sqlc.ImageVulnerabilityReport
	topClusters    []sqlc.TopClustersByVulnerabilityRow
}

func newStubVulnQuerier() *stubVulnQuerier {
	return &stubVulnQuerier{
		reports: map[uuid.UUID]sqlc.ImageVulnerabilityReport{},
		cves:    map[uuid.UUID][]sqlc.ImageVulnerability{},
	}
}

func (s *stubVulnQuerier) GetImageVulnerabilityReportByID(_ context.Context, id uuid.UUID) (sqlc.ImageVulnerabilityReport, error) {
	if s.getErr != nil {
		return sqlc.ImageVulnerabilityReport{}, s.getErr
	}
	r, ok := s.reports[id]
	if !ok {
		return sqlc.ImageVulnerabilityReport{}, errors.New("not found")
	}
	return r, nil
}

func (s *stubVulnQuerier) AggregateClusterVulnerabilities(_ context.Context, _ uuid.UUID) (sqlc.AggregateClusterVulnerabilitiesRow, error) {
	if s.aggregateErr != nil {
		return sqlc.AggregateClusterVulnerabilitiesRow{}, s.aggregateErr
	}
	return s.clusterAgg, nil
}

func (s *stubVulnQuerier) AggregateFleetVulnerabilities(_ context.Context) (sqlc.AggregateFleetVulnerabilitiesRow, error) {
	if s.aggregateErr != nil {
		return sqlc.AggregateFleetVulnerabilitiesRow{}, s.aggregateErr
	}
	return s.fleetAgg, nil
}

func (s *stubVulnQuerier) TopVulnerableImages(_ context.Context, _ sqlc.TopVulnerableImagesParams) ([]sqlc.ImageVulnerabilityReport, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.topByCluster, nil
}

func (s *stubVulnQuerier) ListVulnerableImagesByNamespace(_ context.Context, _ sqlc.ListVulnerableImagesByNamespaceParams) ([]sqlc.ImageVulnerabilityReport, error) {
	if s.listNsErr != nil {
		return nil, s.listNsErr
	}
	return s.topByNS, nil
}

func (s *stubVulnQuerier) CountVulnerableImagesForCluster(_ context.Context, _ uuid.UUID) (int64, error) {
	if s.countErr != nil {
		return 0, s.countErr
	}
	return int64(len(s.topByCluster) + len(s.topByNS)), nil
}

func (s *stubVulnQuerier) ListVulnerabilitiesForReport(_ context.Context, arg sqlc.ListVulnerabilitiesForReportParams) ([]sqlc.ImageVulnerability, error) {
	if s.listCVEErr != nil {
		return nil, s.listCVEErr
	}
	rows := s.cves[arg.ReportID]
	if arg.SeverityFilter != "" {
		filtered := make([]sqlc.ImageVulnerability, 0, len(rows))
		for _, r := range rows {
			if r.Severity == arg.SeverityFilter {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}
	if int(arg.Offset) >= len(rows) {
		return []sqlc.ImageVulnerability{}, nil
	}
	end := int(arg.Offset) + int(arg.Limit)
	if end > len(rows) {
		end = len(rows)
	}
	return rows[arg.Offset:end], nil
}

func (s *stubVulnQuerier) CountVulnerabilitiesForReport(_ context.Context, arg sqlc.CountVulnerabilitiesForReportParams) (int64, error) {
	rows := s.cves[arg.ReportID]
	if arg.SeverityFilter == "" {
		return int64(len(rows)), nil
	}
	n := int64(0)
	for _, r := range rows {
		if r.Severity == arg.SeverityFilter {
			n++
		}
	}
	return n, nil
}

func (s *stubVulnQuerier) TopClustersByVulnerability(_ context.Context, _ int32) ([]sqlc.TopClustersByVulnerabilityRow, error) {
	return s.topClusters, nil
}

// requestWith returns a request that already has cluster_id (and
// optionally id) URL params filled in — convenient for handler tests
// that don't go through a chi router.
func requestWith(method, target string, urlParams map[string]string) *http.Request {
	rc := chi.NewRouteContext()
	for k, v := range urlParams {
		rc.URLParams.Add(k, v)
	}
	req := httptest.NewRequest(method, target, nil)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
}

func decodeJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return out
}

func TestImageVulnHandler_Summary(t *testing.T) {
	q := newStubVulnQuerier()
	q.clusterAgg = sqlc.AggregateClusterVulnerabilitiesRow{
		Critical: 5, High: 12, Medium: 30, Low: 90, Unknown: 1,
		ReportCount: 7,
		LastScannedAt: pgtype.Timestamptz{Time: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Valid: true},
	}
	h := NewImageVulnHandler(q)
	clusterID := uuid.New()
	req := requestWith(http.MethodGet, "/", map[string]string{"cluster_id": clusterID.String()})
	rec := httptest.NewRecorder()

	h.ClusterSummary(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeJSON(t, rec)
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("missing data: %v", body)
	}
	if got := data["critical"].(float64); got != 5 {
		t.Fatalf("critical: %v", got)
	}
	if got := data["last_scanned_at"].(string); got != "2026-01-02T03:04:05Z" {
		t.Fatalf("last_scanned_at: %v", got)
	}
}

func TestImageVulnHandler_Summary_InvalidClusterID(t *testing.T) {
	h := NewImageVulnHandler(newStubVulnQuerier())
	req := requestWith(http.MethodGet, "/", map[string]string{"cluster_id": "not-a-uuid"})
	rec := httptest.NewRecorder()
	h.ClusterSummary(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestImageVulnHandler_PerReportCVEs_PaginatesAndFilters(t *testing.T) {
	q := newStubVulnQuerier()
	clusterID := uuid.New()
	reportID := uuid.New()
	q.reports[reportID] = sqlc.ImageVulnerabilityReport{
		ID: reportID, ClusterID: clusterID, ReportName: "rep-1",
		Namespace: "default", CriticalCount: 1, HighCount: 2,
	}
	severities := []string{"CRITICAL", "HIGH", "HIGH", "MEDIUM", "LOW", "LOW", "LOW"}
	for i, sev := range severities {
		q.cves[reportID] = append(q.cves[reportID], sqlc.ImageVulnerability{
			ID: uuid.New(), ReportID: reportID,
			VulnerabilityID: "CVE-X-" + string(rune('A'+i)),
			Severity:        sev,
		})
	}

	h := NewImageVulnHandler(q)
	// Filter by HIGH severity.
	req := requestWith(http.MethodGet, "/?severity=HIGH&limit=10", map[string]string{
		"cluster_id": clusterID.String(),
		"id":         reportID.String(),
	})
	rec := httptest.NewRecorder()
	h.ClusterReportDetail(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeJSON(t, rec)
	data := body["data"].(map[string]any)
	vulns := data["vulnerabilities"].([]any)
	if len(vulns) != 2 {
		t.Fatalf("expected 2 HIGH cves, got %d", len(vulns))
	}
	if data["vulnerability_total"].(float64) != 2 {
		t.Fatalf("expected vulnerability_total=2")
	}

	// Pagination: limit=1 offset=1 over the unfiltered 7 → 1 item, total 7.
	req = requestWith(http.MethodGet, "/?limit=1&offset=1", map[string]string{
		"cluster_id": clusterID.String(),
		"id":         reportID.String(),
	})
	rec = httptest.NewRecorder()
	h.ClusterReportDetail(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	body = decodeJSON(t, rec)
	data = body["data"].(map[string]any)
	if vulns := data["vulnerabilities"].([]any); len(vulns) != 1 {
		t.Fatalf("expected 1 row, got %d", len(vulns))
	}
	if data["vulnerability_total"].(float64) != 7 {
		t.Fatalf("expected total=7")
	}
}

func TestImageVulnHandler_PerReportCVEs_CrossTenantBlocked(t *testing.T) {
	q := newStubVulnQuerier()
	otherCluster := uuid.New()
	requestedCluster := uuid.New()
	reportID := uuid.New()
	q.reports[reportID] = sqlc.ImageVulnerabilityReport{
		ID: reportID, ClusterID: otherCluster,
	}
	h := NewImageVulnHandler(q)
	req := requestWith(http.MethodGet, "/", map[string]string{
		"cluster_id": requestedCluster.String(),
		"id":         reportID.String(),
	})
	rec := httptest.NewRecorder()
	h.ClusterReportDetail(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 cross-tenant guard, got %d", rec.Code)
	}
}

// recordingK8s captures every call so the rescan tests can assert the
// LIST + per-VR DELETE flow the rewritten nudgeTrivyOperator now uses.
// `responder` is the per-call routing hook: tests register it to return
// different payloads depending on method+path. The previous one-shot
// `resp` field stays as a fallback for tests that only need a uniform
// response.
type recordingK8s struct {
	calls []struct {
		Method, Path string
		Body         []byte
	}
	resp      *protocol.K8sResponsePayload
	err       error
	responder func(method, path string) (*protocol.K8sResponsePayload, error)
}

func (r *recordingK8s) Do(_ context.Context, _ string, method, path string, body []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	r.calls = append(r.calls, struct {
		Method, Path string
		Body         []byte
	}{Method: method, Path: path, Body: body})
	if r.responder != nil {
		return r.responder(method, path)
	}
	return r.resp, r.err
}

// vrListBody encodes a VulnerabilityReportList with the given items
// into the base64-wrapped shape the tunnel proxy delivers. Keeps the
// test free of inline base64.
func vrListBody(items []struct{ Namespace, Name string }) string {
	type meta struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	}
	type item struct {
		Metadata meta `json:"metadata"`
	}
	out := struct {
		Items []item `json:"items"`
	}{}
	for _, it := range items {
		out.Items = append(out.Items, item{Metadata: meta{Name: it.Name, Namespace: it.Namespace}})
	}
	raw, _ := json.Marshal(out)
	return base64.StdEncoding.EncodeToString(raw)
}

// TestImageVulnHandler_RescanNudgesOperator exercises the rewritten
// nudge: a LIST of VulnerabilityReports across the cluster followed
// by a DELETE per row. The earlier PATCH-on-Service approach was a
// no-op pretending to be a rescan; this asserts the real delete flow.
func TestImageVulnHandler_RescanNudgesOperator(t *testing.T) {
	h := NewImageVulnHandler(newStubVulnQuerier())
	vrs := []struct{ Namespace, Name string }{
		{"default", "vr-nginx"},
		{"kube-system", "vr-coredns"},
	}
	rk := &recordingK8s{
		responder: func(method, path string) (*protocol.K8sResponsePayload, error) {
			if method == http.MethodGet && strings.HasSuffix(path, "/vulnerabilityreports") {
				return &protocol.K8sResponsePayload{StatusCode: 200, Body: vrListBody(vrs)}, nil
			}
			// DELETE on each /namespaces/<ns>/vulnerabilityreports/<name>.
			return &protocol.K8sResponsePayload{StatusCode: 200}, nil
		},
	}
	h.SetK8sRequester(rk)

	clusterID := uuid.New()
	req := requestWith(http.MethodPost, "/", map[string]string{"cluster_id": clusterID.String()})
	rec := httptest.NewRecorder()
	h.ClusterRescan(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, body=%s", rec.Code, rec.Body.String())
	}
	body := decodeJSON(t, rec)
	data := body["data"].(map[string]any)
	if data["triggered"].(bool) != true {
		t.Fatalf("expected triggered=true, body=%s", rec.Body.String())
	}
	// 1 LIST + N DELETEs.
	if len(rk.calls) != 1+len(vrs) {
		t.Fatalf("expected %d k8s calls (1 list + %d deletes), got %d", 1+len(vrs), len(vrs), len(rk.calls))
	}
	if rk.calls[0].Method != http.MethodGet {
		t.Fatalf("first call must be GET (list), got %s", rk.calls[0].Method)
	}
	if !strings.HasSuffix(rk.calls[0].Path, "/vulnerabilityreports") {
		t.Fatalf("list path = %q, want suffix /vulnerabilityreports", rk.calls[0].Path)
	}
	// Each delete path must include the VR name + namespace from the list.
	seen := map[string]bool{}
	for _, c := range rk.calls[1:] {
		if c.Method != http.MethodDelete {
			t.Fatalf("non-list call should be DELETE, got %s on %s", c.Method, c.Path)
		}
		for _, vr := range vrs {
			needle := "/namespaces/" + vr.Namespace + "/vulnerabilityreports/" + vr.Name
			if strings.HasSuffix(c.Path, needle) {
				seen[vr.Name] = true
			}
		}
	}
	for _, vr := range vrs {
		if !seen[vr.Name] {
			t.Fatalf("DELETE for VR %s/%s was not issued; calls=%+v", vr.Namespace, vr.Name, rk.calls)
		}
	}
}

// TestImageVulnHandler_RescanSoftSucceedsOnEmptyList exercises the
// "no VRs yet" branch: trivy hasn't produced any reports (cold-boot
// cluster) so the LIST returns an empty items array. The handler
// must still report triggered=true and skip the DELETE loop.
func TestImageVulnHandler_RescanSoftSucceedsOnEmptyList(t *testing.T) {
	h := NewImageVulnHandler(newStubVulnQuerier())
	rk := &recordingK8s{
		responder: func(method, path string) (*protocol.K8sResponsePayload, error) {
			return &protocol.K8sResponsePayload{StatusCode: 200, Body: vrListBody(nil)}, nil
		},
	}
	h.SetK8sRequester(rk)

	clusterID := uuid.New()
	req := requestWith(http.MethodPost, "/", map[string]string{"cluster_id": clusterID.String()})
	rec := httptest.NewRecorder()
	h.ClusterRescan(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, body=%s", rec.Code, rec.Body.String())
	}
	data := decodeJSON(t, rec)["data"].(map[string]any)
	if data["triggered"].(bool) != true {
		t.Fatalf("expected triggered=true on empty list, body=%s", rec.Body.String())
	}
	if len(rk.calls) != 1 {
		t.Fatalf("expected 1 k8s call (LIST only, no deletes), got %d", len(rk.calls))
	}
}

func TestRescan_NilSafeWhenOperatorMissing(t *testing.T) {
	h := NewImageVulnHandler(newStubVulnQuerier())
	// k8s requester left nil intentionally.
	clusterID := uuid.New()
	req := requestWith(http.MethodPost, "/", map[string]string{"cluster_id": clusterID.String()})
	rec := httptest.NewRecorder()
	h.ClusterRescan(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected nil-safe 200, got %d", rec.Code)
	}
	body := decodeJSON(t, rec)
	data := body["data"].(map[string]any)
	if data["triggered"].(bool) != false {
		t.Fatalf("expected triggered=false when operator unwired")
	}
	if data["reason"].(string) != "operator_not_wired" {
		t.Fatalf("expected reason=operator_not_wired, got %v", data["reason"])
	}
}

func TestImageVulnHandler_Rescan_ServiceMissingReportsCleanly(t *testing.T) {
	h := NewImageVulnHandler(newStubVulnQuerier())
	rk := &recordingK8s{resp: &protocol.K8sResponsePayload{
		StatusCode: 404,
		Body:       base64.StdEncoding.EncodeToString([]byte(`{"kind":"Status","status":"Failure"}`)),
	}}
	h.SetK8sRequester(rk)
	clusterID := uuid.New()
	req := requestWith(http.MethodPost, "/", map[string]string{"cluster_id": clusterID.String()})
	rec := httptest.NewRecorder()
	h.ClusterRescan(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeJSON(t, rec)
	data := body["data"].(map[string]any)
	if data["triggered"].(bool) != false {
		t.Fatalf("expected triggered=false on 404")
	}
}

func TestImageVulnHandler_TopImages_NamespaceFilter(t *testing.T) {
	q := newStubVulnQuerier()
	clusterID := uuid.New()
	q.topByCluster = []sqlc.ImageVulnerabilityReport{
		{ID: uuid.New(), ClusterID: clusterID, Namespace: "ns-a", ReportName: "a"},
		{ID: uuid.New(), ClusterID: clusterID, Namespace: "ns-b", ReportName: "b"},
	}
	q.topByNS = []sqlc.ImageVulnerabilityReport{
		{ID: uuid.New(), ClusterID: clusterID, Namespace: "ns-a", ReportName: "a"},
	}
	h := NewImageVulnHandler(q)

	// Unfiltered.
	req := requestWith(http.MethodGet, "/", map[string]string{"cluster_id": clusterID.String()})
	rec := httptest.NewRecorder()
	h.ClusterTopImages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	body := decodeJSON(t, rec)
	data := body["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("expected 2 reports, got %d", len(data))
	}

	// Namespace filter.
	req = requestWith(http.MethodGet, "/?namespace=ns-a", map[string]string{"cluster_id": clusterID.String()})
	rec = httptest.NewRecorder()
	h.ClusterTopImages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	body = decodeJSON(t, rec)
	data = body["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("expected 1 report in namespace filter, got %d", len(data))
	}
}

func TestImageVulnHandler_FleetSummary(t *testing.T) {
	q := newStubVulnQuerier()
	q.fleetAgg = sqlc.AggregateFleetVulnerabilitiesRow{
		Critical: 100, High: 250, Medium: 800, Low: 2500, Unknown: 5,
		ReportCount: 421, ClusterCount: 12,
		LastScannedAt: pgtype.Timestamptz{Time: time.Date(2026, 2, 3, 0, 0, 0, 0, time.UTC), Valid: true},
	}
	h := NewImageVulnHandler(q)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.FleetSummary(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	body := decodeJSON(t, rec)
	data := body["data"].(map[string]any)
	if data["cluster_count"].(float64) != 12 {
		t.Fatalf("expected cluster_count=12, got %v", data["cluster_count"])
	}
}

func TestImageVulnHandler_FleetTopClusters(t *testing.T) {
	q := newStubVulnQuerier()
	q.topClusters = []sqlc.TopClustersByVulnerabilityRow{
		{ClusterID: uuid.New(), Critical: 50, High: 80},
		{ClusterID: uuid.New(), Critical: 40, High: 90},
	}
	h := NewImageVulnHandler(q)
	req := httptest.NewRequest(http.MethodGet, "/?limit=10", nil)
	rec := httptest.NewRecorder()
	h.FleetTopClusters(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	body := decodeJSON(t, rec)
	data := body["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("expected 2 clusters, got %d", len(data))
	}
}

// TestImageVulnHandler_RequiresClusterRead is a documentary test: the
// permission gate lives at the router layer (requirePermission(rbac
// .ResourceClusters, rbac.VerbRead)). Here we assert the handler itself
// doesn't accidentally bypass URL validation — a request without a
// cluster_id param yields 400, which combined with the router-level
// gate gives the property "no anonymous read".
func TestImageVulnHandler_RequiresClusterRead(t *testing.T) {
	h := NewImageVulnHandler(newStubVulnQuerier())
	req := httptest.NewRequest(http.MethodGet, "/", nil) // no chi route ctx
	rec := httptest.NewRecorder()
	h.ClusterSummary(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (missing cluster_id), got %d", rec.Code)
	}
}
