package scanner

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeQuerier is an in-memory Querier that records upsert calls and CVE
// row writes per report. It models the (cluster_id, report_name)
// uniqueness explicitly so the idempotency test can assert "second
// ingest does not produce a second report row".
type fakeQuerier struct {
	mu           sync.Mutex
	reportsByKey map[string]sqlc.ImageVulnerabilityReport // key = cluster_id|report_name
	cves         map[uuid.UUID][]sqlc.InsertImageVulnerabilityParams
	upsertCalls  int
	deleteCalls  int
	batchCalls   int
	batchSizes   []int
}

func newFakeQuerier() *fakeQuerier {
	return &fakeQuerier{
		reportsByKey: map[string]sqlc.ImageVulnerabilityReport{},
		cves:         map[uuid.UUID][]sqlc.InsertImageVulnerabilityParams{},
	}
}

func (f *fakeQuerier) UpsertImageVulnerabilityReport(_ context.Context, arg sqlc.UpsertImageVulnerabilityReportParams) (sqlc.ImageVulnerabilityReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upsertCalls++
	key := arg.ClusterID.String() + "|" + arg.ReportName
	existing, ok := f.reportsByKey[key]
	if !ok {
		existing = sqlc.ImageVulnerabilityReport{
			ID:        uuid.New(),
			ClusterID: arg.ClusterID,
		}
	}
	existing.ReportName = arg.ReportName
	existing.Namespace = arg.Namespace
	existing.WorkloadKind = arg.WorkloadKind
	existing.WorkloadName = arg.WorkloadName
	existing.ContainerName = arg.ContainerName
	existing.ImageRegistry = arg.ImageRegistry
	existing.ImageRepo = arg.ImageRepo
	existing.ImageTag = arg.ImageTag
	existing.ImageDigest = arg.ImageDigest
	existing.Scanner = arg.Scanner
	existing.ScannerVersion = arg.ScannerVersion
	existing.CriticalCount = arg.CriticalCount
	existing.HighCount = arg.HighCount
	existing.MediumCount = arg.MediumCount
	existing.LowCount = arg.LowCount
	existing.UnknownCount = arg.UnknownCount
	existing.ScannedAt = arg.ScannedAt
	if existing.CreatedAt.IsZero() {
		existing.CreatedAt = time.Now().UTC()
	}
	existing.UpdatedAt = time.Now().UTC()
	f.reportsByKey[key] = existing
	return existing, nil
}

func (f *fakeQuerier) DeleteImageVulnerabilitiesByReport(_ context.Context, reportID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls++
	delete(f.cves, reportID)
	return nil
}

func (f *fakeQuerier) BatchInsertImageVulnerabilities(_ context.Context, rows []sqlc.InsertImageVulnerabilityParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batchCalls++
	f.batchSizes = append(f.batchSizes, len(rows))
	for _, r := range rows {
		f.cves[r.ReportID] = append(f.cves[r.ReportID], r)
	}
	return nil
}

// Sprint 081 — snapshot append. Tests don't assert on snapshot rows
// (the history endpoints have their own unit coverage), but the
// Querier interface now requires this method.
func (f *fakeQuerier) InsertImageVulnerabilityReportSnapshot(_ context.Context, _ sqlc.InsertImageVulnerabilityReportSnapshotParams) error {
	return nil
}

func (f *fakeQuerier) reportCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reportsByKey)
}

func (f *fakeQuerier) onlyReport(t *testing.T) sqlc.ImageVulnerabilityReport {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reportsByKey) != 1 {
		t.Fatalf("expected exactly one report, got %d", len(f.reportsByKey))
	}
	for _, v := range f.reportsByKey {
		return v
	}
	return sqlc.ImageVulnerabilityReport{}
}

func (f *fakeQuerier) cveCount(reportID uuid.UUID) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.cves[reportID])
}

// recordingRecorder counts metric writes so tests can assert the gauge +
// counter sides fire on success.
type recordingRecorder struct {
	mu       sync.Mutex
	totals   map[string]float64
	outcomes map[string]int
}

func newRecorder() *recordingRecorder {
	return &recordingRecorder{
		totals:   map[string]float64{},
		outcomes: map[string]int{},
	}
}

func (r *recordingRecorder) SetClusterTotals(cluster, severity string, count float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.totals[cluster+"|"+severity] = count
}

func (r *recordingRecorder) IncIngestOutcome(outcome string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outcomes[outcome]++
}

// sampleReport returns a deterministic VulnerabilityReport for ingest
// tests. Two CVEs by default — tests can mutate the returned struct.
func sampleReport(name string, cves int) TrivyVulnerabilityReport {
	r := TrivyVulnerabilityReport{
		Metadata: TrivyMetadata{
			Name:      name,
			Namespace: "default",
			Labels: map[string]string{
				labelResourceKind:  "Deployment",
				labelResourceName:  "api-server",
				labelContainerName: "app",
				labelResourceNs:    "default",
			},
		},
		Report: TrivyReport{
			Scanner:  TrivyScanner{Name: "Trivy", Version: "0.49.1"},
			Artifact: TrivyArtifact{Repository: "nginx", Tag: "1.25", Digest: "sha256:abcd"},
			Registry: TrivyRegistry{Server: "docker.io"},
			Summary: TrivySummary{
				CriticalCount: 1,
				HighCount:     2,
				MediumCount:   3,
				LowCount:      4,
				UnknownCount:  0,
			},
			UpdateTimestamp: time.Now().UTC().Format(time.RFC3339),
		},
	}
	for i := 0; i < cves; i++ {
		score := 7.5 + float64(i)
		r.Report.Vulnerabilities = append(r.Report.Vulnerabilities, TrivyCVE{
			VulnerabilityID:  "CVE-2025-" + uuidShort(i),
			Severity:         "HIGH",
			Resource:         "libssl",
			InstalledVersion: "3.0.0",
			FixedVersion:     "3.0.1",
			PrimaryLink:      "https://example/cve/" + uuidShort(i),
			Score:            &score,
			Title:            "Sample CVE",
			Description:      "Demo",
		})
	}
	return r
}

func uuidShort(i int) string {
	const tab = "0123456789ABCDEF"
	return string([]byte{tab[i%16], tab[(i/16)%16], tab[(i/256)%16], tab[(i/4096)%16]})
}

func TestIngest_UpsertIsIdempotent(t *testing.T) {
	q := newFakeQuerier()
	ing := NewIngester(q, nil, newRecorder(), nil)
	clusterID := uuid.New()

	rep := sampleReport("api-server-deployment-abc", 3)
	if err := ing.Ingest(context.Background(), clusterID, rep); err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	if err := ing.Ingest(context.Background(), clusterID, rep); err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if q.reportCount() != 1 {
		t.Fatalf("expected idempotent upsert to keep one row, got %d", q.reportCount())
	}
	if q.upsertCalls != 2 {
		t.Fatalf("expected two upsert calls, got %d", q.upsertCalls)
	}
}

func TestIngest_ReplacesCVERowsOnUpdate(t *testing.T) {
	q := newFakeQuerier()
	ing := NewIngester(q, nil, nil, nil)
	clusterID := uuid.New()

	first := sampleReport("rep-1", 5)
	if err := ing.Ingest(context.Background(), clusterID, first); err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	r := q.onlyReport(t)
	if got := q.cveCount(r.ID); got != 5 {
		t.Fatalf("after first ingest expected 5 cves, got %d", got)
	}

	second := sampleReport("rep-1", 2) // report shrunk
	if err := ing.Ingest(context.Background(), clusterID, second); err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	r = q.onlyReport(t)
	if got := q.cveCount(r.ID); got != 2 {
		t.Fatalf("after shrink expected 2 cves, got %d", got)
	}
	if q.deleteCalls != 2 {
		t.Fatalf("expected DeleteImageVulnerabilitiesByReport on each ingest, got %d calls", q.deleteCalls)
	}
}

func TestIngest_BatchesAtBatchSize(t *testing.T) {
	q := newFakeQuerier()
	ing := NewIngester(q, nil, nil, nil)
	clusterID := uuid.New()

	rep := sampleReport("rep-big", BatchSize*2+17)
	if err := ing.Ingest(context.Background(), clusterID, rep); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if q.batchCalls != 3 {
		t.Fatalf("expected 3 batches for %d rows (BatchSize=%d), got %d", BatchSize*2+17, BatchSize, q.batchCalls)
	}
	if q.batchSizes[0] != BatchSize || q.batchSizes[1] != BatchSize || q.batchSizes[2] != 17 {
		t.Fatalf("unexpected batch sizes: %v", q.batchSizes)
	}
}

func TestIngest_RejectsEmptyClusterID(t *testing.T) {
	q := newFakeQuerier()
	ing := NewIngester(q, nil, nil, nil)
	if err := ing.Ingest(context.Background(), uuid.Nil, sampleReport("r", 1)); err == nil {
		t.Fatalf("expected error on nil cluster id")
	}
}

func TestIngest_RejectsEmptyReportName(t *testing.T) {
	q := newFakeQuerier()
	ing := NewIngester(q, nil, nil, nil)
	rep := sampleReport("", 1)
	if err := ing.Ingest(context.Background(), uuid.New(), rep); err == nil {
		t.Fatalf("expected error on empty report name")
	}
}

func TestIngest_NilCVEScoreRoundTrips(t *testing.T) {
	q := newFakeQuerier()
	ing := NewIngester(q, nil, nil, nil)

	rep := sampleReport("rep-noscore", 1)
	rep.Report.Vulnerabilities[0].Score = nil
	if err := ing.Ingest(context.Background(), uuid.New(), rep); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	r := q.onlyReport(t)
	rows := q.cves[r.ID]
	if len(rows) != 1 {
		t.Fatalf("expected 1 cve")
	}
	if rows[0].CvssScore.Valid {
		t.Fatalf("expected CvssScore to be NULL when source score was nil")
	}
}

func TestIngest_MetricsFireOnSuccess(t *testing.T) {
	q := newFakeQuerier()
	rec := newRecorder()
	ing := NewIngester(q, nil, rec, nil)
	clusterID := uuid.New()

	if err := ing.Ingest(context.Background(), clusterID, sampleReport("r", 1)); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if rec.outcomes["ingested"] != 1 {
		t.Fatalf("expected ingested counter to fire once, got %d", rec.outcomes["ingested"])
	}
	if rec.totals[clusterID.String()+"|critical"] != 1 {
		t.Fatalf("expected critical gauge to be 1, got %v", rec.totals[clusterID.String()+"|critical"])
	}
}

// failingQuerier returns an error from the upsert call so we can assert
// the outcome counter increments on the failure path too.
type failingQuerier struct{}

func (failingQuerier) UpsertImageVulnerabilityReport(_ context.Context, _ sqlc.UpsertImageVulnerabilityReportParams) (sqlc.ImageVulnerabilityReport, error) {
	return sqlc.ImageVulnerabilityReport{}, errors.New("boom")
}
func (failingQuerier) DeleteImageVulnerabilitiesByReport(_ context.Context, _ uuid.UUID) error {
	return nil
}
func (failingQuerier) BatchInsertImageVulnerabilities(_ context.Context, _ []sqlc.InsertImageVulnerabilityParams) error {
	return nil
}
func (failingQuerier) InsertImageVulnerabilityReportSnapshot(_ context.Context, _ sqlc.InsertImageVulnerabilityReportSnapshotParams) error {
	return nil
}

func TestIngest_RecordsErrorOutcomeOnUpsertFailure(t *testing.T) {
	rec := newRecorder()
	ing := NewIngester(failingQuerier{}, nil, rec, nil)
	if err := ing.Ingest(context.Background(), uuid.New(), sampleReport("r", 1)); err == nil {
		t.Fatalf("expected error to propagate")
	}
	if rec.outcomes["error"] != 1 {
		t.Fatalf("expected error counter, got %v", rec.outcomes)
	}
}

func TestIngest_AuditHookFires(t *testing.T) {
	q := newFakeQuerier()
	var calls []string
	hook := func(_ context.Context, _ uuid.UUID, name, action string) {
		calls = append(calls, name+"|"+action)
	}
	ing := NewIngester(q, nil, nil, hook)
	if err := ing.Ingest(context.Background(), uuid.New(), sampleReport("audited", 0)); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if len(calls) != 1 || calls[0] != "audited|ingested" {
		t.Fatalf("unexpected audit hook calls: %v", calls)
	}
}

func TestExtractWorkloadIdentity_FallsBackToMetadataNamespace(t *testing.T) {
	r := TrivyVulnerabilityReport{
		Metadata: TrivyMetadata{Name: "x", Namespace: "kube-system", Labels: map[string]string{
			labelResourceKind: "DaemonSet",
			labelResourceName: "node-exporter",
		}},
	}
	kind, name, container, ns := extractWorkloadIdentity(r)
	if kind != "DaemonSet" || name != "node-exporter" || container != "" || ns != "kube-system" {
		t.Fatalf("unexpected identity: kind=%q name=%q container=%q ns=%q", kind, name, container, ns)
	}
}

func TestIngestUnstructured_DecodesAndForwards(t *testing.T) {
	q := newFakeQuerier()
	ing := NewIngester(q, nil, nil, nil)

	raw := map[string]any{
		"metadata": map[string]any{
			"name":      "rep-unstruct",
			"namespace": "default",
			"labels": map[string]string{
				labelResourceKind:  "Deployment",
				labelResourceName:  "api",
				labelContainerName: "main",
				labelResourceNs:    "default",
			},
		},
		"report": map[string]any{
			"summary": map[string]any{
				"criticalCount": 2,
				"highCount":     1,
			},
			"updateTimestamp": "2026-05-01T00:00:00Z",
			"vulnerabilities": []any{
				map[string]any{
					"vulnerabilityID": "CVE-2025-1234",
					"severity":        "CRITICAL",
				},
			},
		},
	}
	if err := ing.IngestUnstructured(context.Background(), uuid.New(), raw); err != nil {
		t.Fatalf("IngestUnstructured: %v", err)
	}
	r := q.onlyReport(t)
	if r.CriticalCount != 2 {
		t.Fatalf("expected CriticalCount=2, got %d", r.CriticalCount)
	}
}

func TestIngestUnstructured_RejectsNil(t *testing.T) {
	ing := NewIngester(newFakeQuerier(), nil, nil, nil)
	if err := ing.IngestUnstructured(context.Background(), uuid.New(), nil); err == nil {
		t.Fatalf("expected error on nil object")
	}
}

func TestParseRFC3339OrNow_ReturnsNowOnInvalid(t *testing.T) {
	got := parseRFC3339OrNow("not-a-date")
	if time.Since(got) > time.Second {
		t.Fatalf("expected near-now timestamp, got %v", got)
	}
}
