package tasks

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type securityLifecycleFake struct {
	scan           sqlc.SecurityScanResult
	claim          sqlc.SecurityScanResult
	finalized      *sqlc.FinalizeSecurityScanReportParams
	rescheduled    *sqlc.RescheduleSecurityScanPollParams
	failed         *sqlc.FailSecurityScanPollParams
	recoverable    []sqlc.SecurityScanResult
	finalizeRows   int64
	rescheduleRows int64
	finalizeErr    error
	finalizeCalls  int
}

func (f *securityLifecycleFake) GetSecurityScanResultByID(context.Context, uuid.UUID) (sqlc.SecurityScanResult, error) {
	return f.scan, nil
}
func (f *securityLifecycleFake) ClaimSecurityScanPoll(context.Context, sqlc.ClaimSecurityScanPollParams) (sqlc.SecurityScanResult, error) {
	return f.claim, nil
}
func (f *securityLifecycleFake) RescheduleSecurityScanPoll(_ context.Context, arg sqlc.RescheduleSecurityScanPollParams) (int64, error) {
	f.rescheduled = &arg
	return f.rescheduleRows, nil
}
func (f *securityLifecycleFake) FinalizeSecurityScanReport(_ context.Context, arg sqlc.FinalizeSecurityScanReportParams) (int64, error) {
	f.finalized = &arg
	f.finalizeCalls++
	if f.finalizeErr != nil {
		return 0, f.finalizeErr
	}
	return f.finalizeRows, nil
}
func (f *securityLifecycleFake) FailSecurityScanPoll(_ context.Context, arg sqlc.FailSecurityScanPollParams) (int64, error) {
	f.failed = &arg
	return 1, nil
}
func (f *securityLifecycleFake) ListRecoverableSecurityScans(context.Context, sqlc.ListRecoverableSecurityScansParams) ([]sqlc.SecurityScanResult, error) {
	return f.recoverable, nil
}

type securityOutboxFake struct{ writes []sqlc.UpsertTaskOutboxParams }

func (f *securityOutboxFake) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	f.writes = append(f.writes, arg)
	return sqlc.TaskOutbox{ID: uuid.New()}, nil
}

func activeSecurityScan(now time.Time) sqlc.SecurityScanResult {
	return sqlc.SecurityScanResult{
		ID: uuid.New(), ClusterID: uuid.New(), Status: "running", ClusterScanName: "scan-a",
		PollGeneration: 1, PollAttempt: 1,
		PollDeadline: pgtype.Timestamptz{Time: now.Add(10 * time.Minute), Valid: true},
	}
}

func testSecurityIngestRuntime(deps SecurityIngestDeps) SecurityIngestRuntime {
	return SecurityIngestRuntime{Deps: deps}
}

func TestHandleSecurityIngestFinalizesUnderLease(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	scan := activeSecurityScan(now)
	q := &securityLifecycleFake{scan: scan, claim: scan, finalizeRows: 1}
	outbox := &securityOutboxFake{}
	reportJSON := `{"total":1,"pass":1,"results":[]}`
	report := `{"metadata":{"name":"report-a"},"spec":{"reportJSON":` + strconv.Quote(reportJSON) + `}}`
	fetch := &securityFetchRequester{resps: map[string]*protocol.K8sResponsePayload{
		"GET /apis/cis.cattle.io/v1/clusterscans/scan-a":         {StatusCode: http.StatusOK, Body: base64.StdEncoding.EncodeToString([]byte(`{"status":{"reportName":"report-a"}}`))},
		"GET /apis/cis.cattle.io/v1/clusterscanreports/report-a": {StatusCode: http.StatusOK, Body: base64.StdEncoding.EncodeToString([]byte(report))},
	}}
	runtime := testSecurityIngestRuntime(SecurityIngestDeps{Queries: q, K8s: fetch, Outbox: outbox, Owner: "replica-a", Now: func() time.Time { return now }})
	task, err := NewSecurityIngestTask(SecurityScanIngestPayload{ScanID: scan.ID.String(), Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.HandleSecurityIngest(context.Background(), task); err != nil {
		t.Fatalf("HandleSecurityIngest: %v", err)
	}
	if q.finalized == nil || q.finalized.Owner != "replica-a" || q.finalized.UpstreamReportName != "report-a" {
		t.Fatalf("finalize params = %#v", q.finalized)
	}
	if q.rescheduled != nil || len(outbox.writes) != 0 {
		t.Fatalf("completed scan should not reschedule: rescheduled=%v outbox=%d", q.rescheduled, len(outbox.writes))
	}
}

func TestHandleSecurityIngestPersistsNextPollBeforeOutbox(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	scan := activeSecurityScan(now)
	q := &securityLifecycleFake{scan: scan, claim: scan, rescheduleRows: 1}
	outbox := &securityOutboxFake{}
	runtime := testSecurityIngestRuntime(SecurityIngestDeps{Queries: q, K8s: &securityFetchRequester{}, Outbox: outbox, Owner: "replica-a", Now: func() time.Time { return now }})
	task, _ := NewSecurityIngestTask(SecurityScanIngestPayload{ScanID: scan.ID.String(), Generation: 1})
	if err := runtime.HandleSecurityIngest(context.Background(), task); err != nil {
		t.Fatalf("HandleSecurityIngest: %v", err)
	}
	if q.rescheduled == nil || !q.rescheduled.NextPollAt.Valid || !q.rescheduled.NextPollAt.Time.Equal(now.Add(ingestPollInterval)) {
		t.Fatalf("reschedule params = %#v", q.rescheduled)
	}
	if len(outbox.writes) != 1 || outbox.writes[0].QueueName != ClusterTemplateApplyQueueName {
		t.Fatalf("outbox writes = %#v", outbox.writes)
	}
	if !strings.Contains(outbox.writes[0].DedupeKey.String, scan.ID.String()) {
		t.Fatalf("dedupe key = %q", outbox.writes[0].DedupeKey.String)
	}
}

func TestDecodeIngestBodyRejectsOversizedReport(t *testing.T) {
	encoded := strings.Repeat("A", base64.StdEncoding.EncodedLen(maxIngestBodyBytes)+1)
	_, err := decodeIngestBody(&protocol.K8sResponsePayload{Body: encoded})
	if err == nil || !isTerminalSecurityIngestError(err) {
		t.Fatalf("expected terminal oversized-body error, got %v", err)
	}
}

type securityFetchRequester struct {
	lastPath string
	paths    []string
	resps    map[string]*protocol.K8sResponsePayload
	err      error
}

func (r *securityFetchRequester) Do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (*protocol.K8sResponsePayload, error) {
	r.lastPath = method + " " + path
	r.paths = append(r.paths, r.lastPath)
	if r.err != nil {
		return nil, r.err
	}
	if r.resps != nil {
		if resp, ok := r.resps[r.lastPath]; ok {
			return resp, nil
		}
	}
	return nil, nil
}

func completedSecurityReportFetcher() *securityFetchRequester {
	reportJSON := `{"total":1,"pass":1,"results":[]}`
	report := `{"metadata":{"name":"report-a"},"spec":{"reportJSON":` + strconv.Quote(reportJSON) + `}}`
	return &securityFetchRequester{resps: map[string]*protocol.K8sResponsePayload{
		"GET /apis/cis.cattle.io/v1/clusterscans/scan-a": {
			StatusCode: http.StatusOK,
			Body:       base64.StdEncoding.EncodeToString([]byte(`{"status":{"reportName":"report-a"}}`)),
		},
		"GET /apis/cis.cattle.io/v1/clusterscanreports/report-a": {
			StatusCode: http.StatusOK,
			Body:       base64.StdEncoding.EncodeToString([]byte(report)),
		},
	}}
}

func TestSecurityIngestRestartBeforeFirstPollRecoveredByReplacement(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	scan := activeSecurityScan(now)
	q := &securityLifecycleFake{
		scan: scan, claim: scan, recoverable: []sqlc.SecurityScanResult{scan},
		finalizeRows: 1,
	}
	outbox := &securityOutboxFake{}
	// Replica A exits after the durable scan row commits but before its first
	// poll task runs. The periodic recovery sweep reconstructs that delivery.
	runtimeA := testSecurityIngestRuntime(SecurityIngestDeps{
		Queries: q, K8s: &securityFetchRequester{}, Outbox: outbox,
		Owner: "replica-a", Now: func() time.Time { return now },
	})
	if err := runtimeA.HandleSecurityIngestRecovery(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(outbox.writes) != 1 {
		t.Fatalf("recovery outbox writes = %d, want 1", len(outbox.writes))
	}

	runtimeB := testSecurityIngestRuntime(SecurityIngestDeps{
		Queries: q, K8s: completedSecurityReportFetcher(), Outbox: outbox,
		Owner: "replica-b", Now: func() time.Time { return now },
	})
	task, _ := NewSecurityIngestTask(SecurityScanIngestPayload{ScanID: scan.ID.String(), Generation: scan.PollGeneration})
	if err := runtimeB.HandleSecurityIngest(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if q.finalized == nil || q.finalized.Owner != "replica-b" {
		t.Fatalf("replacement finalization = %#v", q.finalized)
	}
}

func TestSecurityIngestRestartDuringPollResumesFromDurableNextPoll(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	scan := activeSecurityScan(now)
	q := &securityLifecycleFake{scan: scan, claim: scan, rescheduleRows: 1, finalizeRows: 1}
	outbox := &securityOutboxFake{}
	task, _ := NewSecurityIngestTask(SecurityScanIngestPayload{ScanID: scan.ID.String(), Generation: scan.PollGeneration})

	runtimeA := testSecurityIngestRuntime(SecurityIngestDeps{
		Queries: q, K8s: &securityFetchRequester{err: errors.New("tunnel owner disconnected")}, Outbox: outbox,
		Owner: "replica-a", Now: func() time.Time { return now },
	})
	if err := runtimeA.HandleSecurityIngest(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if q.rescheduled == nil || len(outbox.writes) != 1 {
		t.Fatalf("durable retry not staged: reschedule=%#v outbox=%d", q.rescheduled, len(outbox.writes))
	}

	runtimeB := testSecurityIngestRuntime(SecurityIngestDeps{
		Queries: q, K8s: completedSecurityReportFetcher(), Outbox: outbox,
		Owner: "replica-b", Now: func() time.Time { return now.Add(ingestPollInterval) },
	})
	if err := runtimeB.HandleSecurityIngest(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if q.finalized == nil || q.finalized.Owner != "replica-b" {
		t.Fatalf("resumed finalization = %#v", q.finalized)
	}
}

func TestSecurityIngestRestartAfterReportReadRetriesDatabaseCommit(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	scan := activeSecurityScan(now)
	q := &securityLifecycleFake{
		scan: scan, claim: scan, finalizeRows: 1,
		finalizeErr: errors.New("database connection closed before commit"),
	}
	outbox := &securityOutboxFake{}
	task, _ := NewSecurityIngestTask(SecurityScanIngestPayload{ScanID: scan.ID.String(), Generation: scan.PollGeneration})

	runtimeA := testSecurityIngestRuntime(SecurityIngestDeps{
		Queries: q, K8s: completedSecurityReportFetcher(), Outbox: outbox,
		Owner: "replica-a", Now: func() time.Time { return now },
	})
	if err := runtimeA.HandleSecurityIngest(context.Background(), task); err == nil {
		t.Fatal("lost database commit must remain retryable")
	}
	q.finalizeErr = nil
	runtimeB := testSecurityIngestRuntime(SecurityIngestDeps{
		Queries: q, K8s: completedSecurityReportFetcher(), Outbox: outbox,
		Owner: "replica-b", Now: func() time.Time { return now.Add(time.Second) },
	})
	if err := runtimeB.HandleSecurityIngest(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if q.finalizeCalls != 2 || q.finalized == nil || q.finalized.Owner != "replica-b" {
		t.Fatalf("commit retry calls=%d params=%#v", q.finalizeCalls, q.finalized)
	}
}

func TestSecurityIngestDisconnectedClusterResumesBeforeAndFailsAfterDeadline(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	scan := activeSecurityScan(now)
	q := &securityLifecycleFake{scan: scan, claim: scan, rescheduleRows: 1}
	outbox := &securityOutboxFake{}
	task, _ := NewSecurityIngestTask(SecurityScanIngestPayload{ScanID: scan.ID.String(), Generation: scan.PollGeneration})

	runtimeA := testSecurityIngestRuntime(SecurityIngestDeps{
		Queries: q, K8s: &securityFetchRequester{err: errors.New("cluster disconnected")}, Outbox: outbox,
		Owner: "replica-a", Now: func() time.Time { return now },
	})
	if err := runtimeA.HandleSecurityIngest(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if q.rescheduled == nil || q.failed != nil {
		t.Fatalf("pre-deadline disconnect should reschedule: reschedule=%#v failed=%#v", q.rescheduled, q.failed)
	}

	deadline := scan.PollDeadline.Time
	q.recoverable = []sqlc.SecurityScanResult{scan}
	runtimeB := testSecurityIngestRuntime(SecurityIngestDeps{
		Queries: q, K8s: &securityFetchRequester{}, Outbox: outbox,
		Owner: "replica-b", Now: func() time.Time { return deadline },
	})
	if err := runtimeB.HandleSecurityIngestRecovery(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if q.failed == nil || !strings.Contains(q.failed.Reason, "deadline exceeded") {
		t.Fatalf("post-deadline recovery failure = %#v", q.failed)
	}
}

func TestFetchClusterScanReportUsesResolvedReportName(t *testing.T) {
	t.Parallel()

	scan := map[string]any{
		"apiVersion": "cis.cattle.io/v1",
		"kind":       "ClusterScan",
		"metadata":   map[string]any{"name": "demo"},
		"status":     map[string]any{"reportName": "scan-report-demo-abc123"},
	}
	report := map[string]any{
		"apiVersion": "cis.cattle.io/v1",
		"kind":       "ClusterScanReport",
		"metadata":   map[string]any{"name": "scan-report-demo-abc123"},
	}
	scanRaw, err := json.Marshal(scan)
	if err != nil {
		t.Fatalf("marshal scan: %v", err)
	}
	reportRaw, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	req := &securityFetchRequester{
		resps: map[string]*protocol.K8sResponsePayload{
			"GET /apis/cis.cattle.io/v1/clusterscans/demo": {
				StatusCode: http.StatusOK,
				Body:       base64.StdEncoding.EncodeToString(scanRaw),
			},
			"GET /apis/cis.cattle.io/v1/clusterscanreports/scan-report-demo-abc123": {
				StatusCode: http.StatusOK,
				Body:       base64.StdEncoding.EncodeToString(reportRaw),
			},
		},
	}

	got, found, err := fetchClusterScanReport(context.Background(), req, "cluster-1", "demo")
	if err != nil {
		t.Fatalf("fetchClusterScanReport() error = %v", err)
	}
	if !found {
		t.Fatal("expected report to be found")
	}
	if got["kind"] != "ClusterScanReport" {
		t.Fatalf("kind = %v", got["kind"])
	}
	if req.lastPath != "GET /apis/cis.cattle.io/v1/clusterscanreports/scan-report-demo-abc123" {
		t.Fatalf("unexpected path %q", req.lastPath)
	}
}

func TestFetchClusterScanReportFallsBackToOwnerMatchedList(t *testing.T) {
	t.Parallel()

	list := map[string]any{
		"items": []map[string]any{
			{
				"metadata": map[string]any{
					"name": "scan-report-demo-xyz789",
					"ownerReferences": []map[string]any{
						{"name": "demo"},
					},
				},
			},
		},
	}
	report := map[string]any{
		"apiVersion": "cis.cattle.io/v1",
		"kind":       "ClusterScanReport",
		"metadata":   map[string]any{"name": "scan-report-demo-xyz789"},
	}
	listRaw, err := json.Marshal(list)
	if err != nil {
		t.Fatalf("marshal list: %v", err)
	}
	reportRaw, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	req := &securityFetchRequester{
		resps: map[string]*protocol.K8sResponsePayload{
			"GET /apis/cis.cattle.io/v1/clusterscans/demo": {
				StatusCode: http.StatusOK,
				Body:       base64.StdEncoding.EncodeToString([]byte(`{"status":{}}`)),
			},
			"GET /apis/cis.cattle.io/v1/clusterscanreports": {
				StatusCode: http.StatusOK,
				Body:       base64.StdEncoding.EncodeToString(listRaw),
			},
			"GET /apis/cis.cattle.io/v1/clusterscanreports/scan-report-demo-xyz789": {
				StatusCode: http.StatusOK,
				Body:       base64.StdEncoding.EncodeToString(reportRaw),
			},
		},
	}

	got, found, err := fetchClusterScanReport(context.Background(), req, "cluster-1", "demo")
	if err != nil {
		t.Fatalf("fetchClusterScanReport() error = %v", err)
	}
	if !found {
		t.Fatal("expected report to be found")
	}
	if got["kind"] != "ClusterScanReport" {
		t.Fatalf("kind = %v", got["kind"])
	}
	if req.lastPath != "GET /apis/cis.cattle.io/v1/clusterscanreports/scan-report-demo-xyz789" {
		t.Fatalf("unexpected path %q", req.lastPath)
	}
}
