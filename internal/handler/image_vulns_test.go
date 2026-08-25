package handler

import (
	"context"
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
)

// Test-naming convention: every TestHandler_* MUST be prefixed
// TestImageVulnHandler_* to avoid collision with concurrent agents on
// the same package (per the sprint brief).

// stubVulnQuerier is the in-memory ImageVulnQuerier used by every test
// here. It models just enough of the schema to exercise the handler's
// branching paths.
type stubVulnQuerier struct {
	reports      map[uuid.UUID]sqlc.ImageVulnerabilityReport
	cves         map[uuid.UUID][]sqlc.ImageVulnerability
	getErr       error
	aggregateErr error
	listErr      error
	listNsErr    error
	listCVEErr   error
	countErr     error
	clusterAgg   sqlc.AggregateClusterVulnerabilitiesRow
	fleetAgg     sqlc.AggregateFleetVulnerabilitiesRow
	topByCluster []sqlc.ImageVulnerabilityReport
	topByNS      []sqlc.ImageVulnerabilityReport
	topClusters  []sqlc.TopClustersByVulnerabilityRow
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
	if int(arg.PageOffset) >= len(rows) {
		return []sqlc.ImageVulnerability{}, nil
	}
	end := int(arg.PageOffset) + int(arg.PageLimit)
	if end > len(rows) {
		end = len(rows)
	}
	return rows[arg.PageOffset:end], nil
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
		ReportCount:   7,
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

type imageVulnMutationFake struct {
	cluster    sqlc.Cluster
	operations []sqlc.WorkloadOperation
	tasks      []sqlc.UpsertTaskOutboxParams
	audits     []sqlc.UpsertAuditOutboxParams
	taskErr    error
	auditErr   error
}

func (f *imageVulnMutationFake) GetClusterByID(_ context.Context, id uuid.UUID) (sqlc.Cluster, error) {
	if f.cluster.ID != id {
		return sqlc.Cluster{}, errors.New("cluster missing")
	}
	return f.cluster, nil
}

func (f *imageVulnMutationFake) CreateWorkloadOperation(_ context.Context, arg sqlc.CreateWorkloadOperationParams) (sqlc.WorkloadOperation, error) {
	row := sqlc.WorkloadOperation{ID: uuid.New(), TargetType: arg.TargetType, TargetKey: arg.TargetKey,
		OperationType: arg.OperationType, Payload: arg.Payload, Status: arg.Status, CreatedByID: arg.CreatedByID,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	f.operations = append(f.operations, row)
	return row, nil
}

func (f *imageVulnMutationFake) CreateWorkloadOperationIdempotent(ctx context.Context, arg sqlc.CreateWorkloadOperationIdempotentParams) (sqlc.WorkloadOperation, error) {
	return f.CreateWorkloadOperation(ctx, sqlc.CreateWorkloadOperationParams{
		TargetType: arg.TargetType, TargetKey: arg.TargetKey, OperationType: arg.OperationType,
		Payload: arg.Payload, Status: arg.Status, CreatedByID: arg.CreatedByID,
	})
}

func (f *imageVulnMutationFake) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	if f.taskErr != nil {
		return sqlc.TaskOutbox{}, f.taskErr
	}
	f.tasks = append(f.tasks, arg)
	return sqlc.TaskOutbox{ID: uuid.New(), TaskType: arg.TaskType, Payload: arg.Payload}, nil
}

func (f *imageVulnMutationFake) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if f.auditErr != nil {
		return sqlc.AuditOutbox{}, f.auditErr
	}
	f.audits = append(f.audits, arg)
	return sqlc.AuditOutbox{ID: uuid.New()}, nil
}

func imageVulnTestRunTx(fake *imageVulnMutationFake) imageVulnRunTxFunc {
	return func(_ context.Context, fn func(ImageVulnMutationTx) error) error {
		operations := append([]sqlc.WorkloadOperation(nil), fake.operations...)
		taskRows := append([]sqlc.UpsertTaskOutboxParams(nil), fake.tasks...)
		auditRows := append([]sqlc.UpsertAuditOutboxParams(nil), fake.audits...)
		if err := fn(fake); err != nil {
			fake.operations, fake.tasks, fake.audits = operations, taskRows, auditRows
			return err
		}
		return nil
	}
}

func TestImageVulnHandler_RescanCommitsOperationTaskAndAudit(t *testing.T) {
	clusterID := uuid.New()
	fake := &imageVulnMutationFake{cluster: sqlc.Cluster{ID: clusterID, Name: "prod"}}
	h := NewImageVulnHandler(newStubVulnQuerier())
	h.SetRunTx(imageVulnTestRunTx(fake))
	req := requestWith(http.MethodPost, "/", map[string]string{"cluster_id": clusterID.String()})
	rec := httptest.NewRecorder()
	h.ClusterRescan(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("got %d, body=%s", rec.Code, rec.Body.String())
	}
	if len(fake.operations) != 1 || len(fake.tasks) != 1 || len(fake.audits) != 1 {
		t.Fatalf("operation/task/audit=%d/%d/%d", len(fake.operations), len(fake.tasks), len(fake.audits))
	}
	if fake.tasks[0].TaskType != "vulnerability:rescan" || strings.Contains(string(fake.tasks[0].Payload), clusterID.String()) {
		t.Fatalf("task must be identifier-only: type=%q payload=%s", fake.tasks[0].TaskType, fake.tasks[0].Payload)
	}
	data := decodeJSON(t, rec)["data"].(map[string]any)
	if data["operation_id"] != fake.operations[0].ID.String() || data["status"] != "pending" {
		t.Fatalf("unexpected receipt: %v", data)
	}
	wantLocation := "/api/v1/workloads/operations/" + fake.operations[0].ID.String() + "/"
	if rec.Header().Get("Location") != wantLocation || rec.Header().Get("Retry-After") != "2" {
		t.Fatalf("Location=%q Retry-After=%q", rec.Header().Get("Location"), rec.Header().Get("Retry-After"))
	}
}

func TestImageVulnHandler_RescanAuditFailureRollsBack(t *testing.T) {
	clusterID := uuid.New()
	fake := &imageVulnMutationFake{cluster: sqlc.Cluster{ID: clusterID, Name: "prod"}, auditErr: errors.New("audit unavailable")}
	h := NewImageVulnHandler(newStubVulnQuerier())
	h.SetRunTx(imageVulnTestRunTx(fake))
	req := requestWith(http.MethodPost, "/", map[string]string{"cluster_id": clusterID.String()})
	rec := httptest.NewRecorder()
	h.ClusterRescan(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, body=%s", rec.Code, rec.Body.String())
	}
	if len(fake.operations) != 0 || len(fake.tasks) != 0 || len(fake.audits) != 0 {
		t.Fatalf("rolled-back operation/task/audit=%d/%d/%d", len(fake.operations), len(fake.tasks), len(fake.audits))
	}
}

func TestImageVulnHandler_RescanTaskFailureRollsBackBeforeAudit(t *testing.T) {
	clusterID := uuid.New()
	fake := &imageVulnMutationFake{cluster: sqlc.Cluster{ID: clusterID, Name: "prod"}, taskErr: errors.New("task unavailable")}
	h := NewImageVulnHandler(newStubVulnQuerier())
	h.SetRunTx(imageVulnTestRunTx(fake))
	req := requestWith(http.MethodPost, "/", map[string]string{"cluster_id": clusterID.String()})
	rec := httptest.NewRecorder()
	h.ClusterRescan(rec, req)
	if rec.Code != http.StatusInternalServerError || len(fake.operations) != 0 || len(fake.tasks) != 0 || len(fake.audits) != 0 {
		t.Fatalf("status=%d operation/task/audit=%d/%d/%d body=%s", rec.Code, len(fake.operations), len(fake.tasks), len(fake.audits), rec.Body.String())
	}
}

func TestImageVulnOperationReceiptResolvesThroughGenericOperationAPI(t *testing.T) {
	clusterID := uuid.New()
	payload, _ := json.Marshal(map[string]string{"clusterId": clusterID.String()})
	resolved, err := workloadOperationClusterID(sqlc.WorkloadOperation{Payload: payload})
	if err != nil || resolved != clusterID {
		t.Fatalf("resolved=%s err=%v payload=%s", resolved, err, payload)
	}
}

func TestImageVulnHandler_RescanFailsClosedWithoutTransaction(t *testing.T) {
	h := NewImageVulnHandler(newStubVulnQuerier())
	req := requestWith(http.MethodPost, "/", map[string]string{"cluster_id": uuid.NewString()})
	rec := httptest.NewRecorder()
	h.ClusterRescan(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestImageVulnHandler_RescanRejectsInvalidIdempotencyKey(t *testing.T) {
	h := NewImageVulnHandler(newStubVulnQuerier())
	req := requestWith(http.MethodPost, "/", map[string]string{"cluster_id": uuid.NewString()})
	req.Header.Set("Idempotency-Key", strings.Repeat("x", 129))
	rec := httptest.NewRecorder()
	h.ClusterRescan(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, body=%s", rec.Code, rec.Body.String())
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
