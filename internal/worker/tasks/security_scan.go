package tasks

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// SecurityIngestType is the asynq task type emitted by SecurityHandler.CreateScan
// after a ClusterScan CR has been created. The handler is package-public so
// the security HTTP handler can reference the same string without importing
// this package (avoiding a worker → handler import cycle).
const SecurityIngestType = "security:ingest_scan_results"

// cisOperatorNamespace is the well-known namespace cis-operator deploys into.
const cisOperatorNamespace = "cis-operator-system"

// ingestPollInterval and ingestMaxAttempts together cap report polling at
// 30 minutes (60 attempts × 30s), matching the design doc. We use a
// re-enqueue pattern instead of a single long-running task so a worker
// restart doesn't lose progress.
const (
	ingestPollInterval = 30 * time.Second
	ingestMaxAttempts  = 60
)

// SecurityScanPayload is the legacy payload still consumed by the
// `security:scan` task type. Kept to preserve backward compatibility with
// the public worker API and existing tests.
type SecurityScanPayload struct {
	ClusterID string `json:"cluster_id"`
	ScanType  string `json:"scan_type,omitempty"`
}

// SecurityScanIngestPayload drives the report-ingestion task. AttemptCount is
// incremented on every re-enqueue so we can fail the scan after the
// configured ceiling rather than retrying forever.
type SecurityScanIngestPayload struct {
	ScanID          string `json:"scan_id"`
	ClusterID       string `json:"cluster_id"`
	ClusterScanName string `json:"cluster_scan_name"`
	AttemptCount    int    `json:"attempt_count,omitempty"`
}

// SecurityIngestQuerier is the slice of the runtime querier the ingest task
// touches. Using its own interface lets unit tests stub a tiny in-memory
// implementation without dragging in the entire RuntimeQuerier surface.
type SecurityIngestQuerier interface {
	GetSecurityScanResultByID(ctx context.Context, id uuid.UUID) (sqlc.SecurityScanResult, error)
	UpdateSecurityScanReport(ctx context.Context, arg sqlc.UpdateSecurityScanReportParams) error
	UpdateSecurityScanFailedWithMessage(ctx context.Context, arg sqlc.UpdateSecurityScanFailedWithMessageParams) error
}

// SecurityIngestK8sFetcher mirrors handler.K8sRequester but lives in the
// tasks package to keep it import-cycle-free.
type SecurityIngestK8sFetcher interface {
	Do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (*protocol.K8sResponsePayload, error)
}

// SecurityIngestEnqueuer matches asynq.Client just enough to schedule a
// follow-up poll without forcing tests to spin up Redis.
type SecurityIngestEnqueuer interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

// SecurityIngestDeps carries the optional task dependencies. The handler is
// nil-safe — when these aren't wired the task no-ops, which is the right
// behavior in test environments.
type SecurityIngestDeps struct {
	Queries SecurityIngestQuerier
	K8s     SecurityIngestK8sFetcher
	Queue   SecurityIngestEnqueuer
	Log     *slog.Logger
	// Now is overridable for tests.
	Now func() time.Time
}

var securityIngestDeps SecurityIngestDeps

// ConfigureSecurityIngest wires the deps for the report-ingest task. Called
// from internal/server/server.go on startup once the tunnel hub and asynq
// client are available.
func ConfigureSecurityIngest(deps SecurityIngestDeps) {
	securityIngestDeps = deps
	if securityIngestDeps.Log == nil {
		securityIngestDeps.Log = slog.Default()
	}
	if securityIngestDeps.Now == nil {
		securityIngestDeps.Now = time.Now
	}
}

// NewSecurityScanTask creates a new security scan task. Kept for backward
// compatibility — current call sites are limited to the legacy code path.
func NewSecurityScanTask(payload SecurityScanPayload) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal security scan payload: %w", err)
	}
	return asynq.NewTask("security:scan", data, asynq.MaxRetry(1)), nil
}

// NewSecurityIngestTask schedules the next poll of a ClusterScanReport. This
// is exported so handler code can rebuild the same task shape without having
// to import the asynq package directly.
func NewSecurityIngestTask(payload SecurityScanIngestPayload) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal security ingest payload: %w", err)
	}
	return asynq.NewTask(SecurityIngestType, data,
		asynq.MaxRetry(3),
		asynq.ProcessIn(ingestPollInterval),
		asynq.Timeout(35*time.Minute),
	), nil
}

// HandleSecurityScan is the legacy entrypoint. Maintained so the existing
// task type continues to work.
func HandleSecurityScan(ctx context.Context, t *asynq.Task) error {
	var p SecurityScanPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal security scan payload: %w", err)
	}
	if p.ClusterID == "" {
		return fmt.Errorf("cluster_id is required")
	}
	scanType := p.ScanType
	if scanType == "" {
		scanType = "full"
	}
	slog.InfoContext(ctx, "running security scan",
		"cluster_id", p.ClusterID,
		"scan_type", scanType,
	)
	if runtimeDeps.Queries == nil {
		slog.InfoContext(ctx, "security scan runtime not configured, skipping DB result creation")
		return nil
	}
	clusterID, err := uuid.Parse(p.ClusterID)
	if err != nil {
		return fmt.Errorf("invalid cluster_id: %w", err)
	}
	summary, _ := json.Marshal(map[string]any{
		"critical": 0,
		"high":     0,
		"medium":   0,
		"low":      0,
	})
	results, _ := json.Marshal(map[string]any{
		"scan_type": scanType,
		"source":    "worker",
		"findings":  []any{},
	})
	if _, err := runtimeDeps.Queries.CreateSecurityScanResult(ctx, sqlc.CreateSecurityScanResultParams{
		ClusterID:     clusterID,
		ScanType:      scanType,
		Status:        "completed",
		Summary:       summary,
		Results:       results,
		InitiatedByID: emptyUUID(),
	}); err != nil {
		return err
	}
	slog.InfoContext(ctx, "security scan complete", "cluster_id", p.ClusterID, "scan_type", scanType)
	return nil
}

// HandleSecurityIngest polls the ClusterScanReport for the given scan and
// either ingests it (success), reschedules another poll (still running), or
// marks the scan failed (timeout / unrecoverable error).
func HandleSecurityIngest(ctx context.Context, t *asynq.Task) error {
	var p SecurityScanIngestPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal security ingest payload: %w", err)
	}
	if p.ScanID == "" || p.ClusterID == "" || p.ClusterScanName == "" {
		return fmt.Errorf("scan_id, cluster_id, and cluster_scan_name are required")
	}
	if securityIngestDeps.Queries == nil || securityIngestDeps.K8s == nil {
		// Runtime not wired — most likely a test env or scheduler started
		// before the server did. No-op rather than retrying forever.
		slog.InfoContext(ctx, "security ingest runtime not configured, skipping",
			"scan_id", p.ScanID)
		return nil
	}

	scanID, err := uuid.Parse(p.ScanID)
	if err != nil {
		return fmt.Errorf("invalid scan_id: %w", err)
	}

	// Fetch the report by name from the cis-operator API. cis-operator names
	// the report identically to the ClusterScan that produced it.
	report, found, err := fetchClusterScanReport(ctx, securityIngestDeps.K8s, p.ClusterID, p.ClusterScanName)
	if err != nil {
		return reschedule(ctx, p, err.Error())
	}
	if !found {
		return reschedule(ctx, p, "report not yet available")
	}

	counts, findings, summaryRaw, resultsRaw := flattenClusterScanReport(report)

	if err := securityIngestDeps.Queries.UpdateSecurityScanReport(ctx, sqlc.UpdateSecurityScanReportParams{
		ID:       scanID,
		Summary:  summaryRaw,
		Results:  resultsRaw,
		Passed:   counts.Pass,
		Failed:   counts.Fail,
		Warned:   counts.Warn,
		Skipped:  counts.Skip,
		Findings: findings,
	}); err != nil {
		securityIngestDeps.Log.Error("update security scan report failed",
			"scan_id", p.ScanID, "error", err)
		return err
	}
	securityIngestDeps.Log.Info("security scan ingested",
		"scan_id", p.ScanID,
		"pass", counts.Pass, "fail", counts.Fail, "warn", counts.Warn, "skip", counts.Skip)
	return nil
}

// reschedule re-enqueues the ingest task unless we've hit the attempt
// ceiling, in which case we mark the scan failed with a clear message.
func reschedule(ctx context.Context, p SecurityScanIngestPayload, reason string) error {
	p.AttemptCount++
	if p.AttemptCount >= ingestMaxAttempts {
		scanID, err := uuid.Parse(p.ScanID)
		if err != nil {
			return err
		}
		msg := fmt.Sprintf("ClusterScanReport not available after %d attempts: %s",
			ingestMaxAttempts, reason)
		if err := securityIngestDeps.Queries.UpdateSecurityScanFailedWithMessage(ctx, sqlc.UpdateSecurityScanFailedWithMessageParams{
			ID:           scanID,
			ErrorMessage: msg,
		}); err != nil {
			return err
		}
		securityIngestDeps.Log.Warn("security scan timed out", "scan_id", p.ScanID, "reason", reason)
		return nil
	}
	if securityIngestDeps.Queue == nil {
		// No queue wired — fall through quietly. asynq's own retry will
		// eventually take care of re-queueing.
		return errors.New(reason)
	}
	task, err := NewSecurityIngestTask(p)
	if err != nil {
		return err
	}
	if _, err := securityIngestDeps.Queue.Enqueue(task); err != nil {
		return err
	}
	return nil
}

// fetchClusterScanReport queries the per-cluster API for a ClusterScanReport
// matching the upstream ClusterScan name. Returns (report, true, nil) when
// the report exists, (nil, false, nil) when the operator hasn't produced
// one yet, and (nil, false, err) for unexpected errors.
func fetchClusterScanReport(ctx context.Context, fetcher SecurityIngestK8sFetcher, clusterID, scanName string) (map[string]any, bool, error) {
	path := fmt.Sprintf("/apis/cis.cattle.io/v1/namespaces/%s/clusterscanreports/%s",
		cisOperatorNamespace, scanName)
	resp, err := fetcher.Do(ctx, clusterID, http.MethodGet, path, nil, map[string]string{"Accept": "application/json"})
	if err != nil {
		return nil, false, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, false, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	body, err := decodeIngestBody(resp)
	if err != nil {
		return nil, false, err
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, false, err
	}
	return out, true, nil
}

// CISCounts is exported so tests can assert against it.
type CISCounts struct {
	Total int32
	Pass  int32
	Fail  int32
	Warn  int32
	Skip  int32
}

// FlattenClusterScanReport extracts the totals + findings from a raw
// cis-operator ClusterScanReport object. Exported for unit testing.
//
// The cis-operator report shape (as of v1.4.x) wraps everything under
// `spec.reportJSON` as a JSON-encoded string. Inside that string we get
// `total`, `pass`, `fail`, `warn`, `skip` plus a `tests[]` slice that holds
// per-section results. Each test inside a section has `test_number`,
// `test_desc`, `status`, `remediation`, and a severity field. We flatten
// all of those into our normalized CISFinding shape.
func FlattenClusterScanReport(report map[string]any) (CISCounts, []map[string]any, json.RawMessage, json.RawMessage) {
	counts, findingsRaw, summaryRaw, resultsRaw := flattenClusterScanReport(report)
	var findings []map[string]any
	_ = json.Unmarshal(findingsRaw, &findings)
	return counts, findings, summaryRaw, resultsRaw
}

func flattenClusterScanReport(report map[string]any) (CISCounts, json.RawMessage, json.RawMessage, json.RawMessage) {
	var counts CISCounts
	findings := []map[string]any{}

	spec, _ := report["spec"].(map[string]any)
	reportPayload := decodeReportJSON(spec)

	if v, ok := numericField(reportPayload, "total"); ok {
		counts.Total = v
	}
	if v, ok := numericField(reportPayload, "pass"); ok {
		counts.Pass = v
	}
	if v, ok := numericField(reportPayload, "fail"); ok {
		counts.Fail = v
	}
	if v, ok := numericField(reportPayload, "warn"); ok {
		counts.Warn = v
	}
	if v, ok := numericField(reportPayload, "skip"); ok {
		counts.Skip = v
	}

	results, _ := reportPayload["results"].([]any)
	if len(results) == 0 {
		results, _ = reportPayload["tests"].([]any)
	}
	for _, raw := range results {
		section, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		// Both "results" and "tests" can hold the inner test slice; check
		// both keys to be defensive against minor schema drift between
		// cis-operator versions.
		inner, _ := section["checks"].([]any)
		if len(inner) == 0 {
			inner, _ = section["results"].([]any)
		}
		if len(inner) == 0 {
			inner, _ = section["tests"].([]any)
		}
		for _, t := range inner {
			test, ok := t.(map[string]any)
			if !ok {
				continue
			}
			id := stringField(test, "id", "test_number", "number")
			status := stringField(test, "state", "status")
			finding := map[string]any{
				"test_id":     id,
				"severity":    stringField(test, "scored_severity", "severity"),
				"status":      status,
				"description": stringField(test, "test_desc", "description", "desc"),
				"remediation": stringField(test, "remediation"),
			}
			findings = append(findings, finding)
		}
	}

	// Fall back to the top-level test summary counts when the reportJSON
	// didn't carry them directly.
	if counts.Total == 0 && len(findings) > 0 {
		counts.Total = int32(len(findings))
	}

	summary := map[string]any{
		"total":   counts.Total,
		"pass":    counts.Pass,
		"fail":    counts.Fail,
		"warn":    counts.Warn,
		"skip":    counts.Skip,
		"updated": time.Now().UTC().Format(time.RFC3339),
	}
	summaryRaw, _ := json.Marshal(summary)
	resultsRaw, _ := json.Marshal(map[string]any{
		"source":  "cis-operator",
		"version": stringField(spec, "scanProfileName"),
	})
	findingsRaw, _ := json.Marshal(findings)
	return counts, findingsRaw, summaryRaw, resultsRaw
}

// decodeReportJSON pulls the actual report payload out of `spec.reportJSON`
// (string-encoded) or `spec.report` (already an object), tolerating both
// shapes.
func decodeReportJSON(spec map[string]any) map[string]any {
	if spec == nil {
		return map[string]any{}
	}
	if raw, ok := spec["reportJSON"].(string); ok && raw != "" {
		var out map[string]any
		if err := json.Unmarshal([]byte(raw), &out); err == nil {
			return out
		}
	}
	if obj, ok := spec["report"].(map[string]any); ok {
		return obj
	}
	if obj, ok := spec["reportJSON"].(map[string]any); ok {
		return obj
	}
	return map[string]any{}
}

func stringField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func numericField(m map[string]any, key string) (int32, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return int32(n), true
	case int:
		return int32(n), true
	case int64:
		return int32(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int32(i), true
	}
	return 0, false
}

func decodeIngestBody(resp *protocol.K8sResponsePayload) ([]byte, error) {
	if resp == nil || resp.Body == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(resp.Body)
}
