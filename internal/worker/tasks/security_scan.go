package tasks

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/scanner"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// SecurityIngestType is the asynq task type emitted by SecurityHandler.CreateScan
// after a ClusterScan CR has been created. The handler is package-public so
// the security HTTP handler can reference the same string without importing
// this package (avoiding a worker → handler import cycle).
const (
	SecurityIngestType         = "security:ingest_scan_results"
	SecurityIngestRecoveryType = "security:recover_scan_ingestion"
)

// ingestPollInterval and ingestMaxAttempts together cap report polling at
// 30 minutes (60 attempts × 30s), matching the design doc. We use a
// re-enqueue pattern instead of a single long-running task so a worker
// restart doesn't lose progress.
const (
	ingestPollInterval = 30 * time.Second
	ingestMaxAttempts  = 60
	ingestLease        = 2 * time.Minute
	maxIngestBodyBytes = 8 << 20
	ingestRecoveryRows = 100
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
	ScanID     string `json:"scan_id"`
	Generation int64  `json:"generation,omitempty"`
}

// SecurityIngestQuerier is the slice of the runtime querier the ingest task
// touches. Using its own interface lets unit tests stub a tiny in-memory
// implementation without dragging in the entire RuntimeQuerier surface.
type SecurityIngestQuerier interface {
	GetSecurityScanResultByID(ctx context.Context, id uuid.UUID) (sqlc.SecurityScanResult, error)
	ClaimSecurityScanPoll(ctx context.Context, arg sqlc.ClaimSecurityScanPollParams) (sqlc.SecurityScanResult, error)
	RescheduleSecurityScanPoll(ctx context.Context, arg sqlc.RescheduleSecurityScanPollParams) (int64, error)
	FinalizeSecurityScanReport(ctx context.Context, arg sqlc.FinalizeSecurityScanReportParams) (int64, error)
	FailSecurityScanPoll(ctx context.Context, arg sqlc.FailSecurityScanPollParams) (int64, error)
	ListRecoverableSecurityScans(ctx context.Context, arg sqlc.ListRecoverableSecurityScansParams) ([]sqlc.SecurityScanResult, error)
}

// SecurityIngestK8sFetcher mirrors handler.K8sRequester but lives in the
// tasks package to keep it import-cycle-free.
type SecurityIngestK8sFetcher interface {
	Do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (*protocol.K8sResponsePayload, error)
}

// SecurityIngestDeps carries the task dependencies. Production composition
// validation requires the database, tunnel fetcher, and durable task outbox;
// the handler also fails visibly if invoked before they are wired.
type SecurityIngestDeps struct {
	Queries SecurityIngestQuerier
	K8s     SecurityIngestK8sFetcher
	Outbox  TaskOutboxWriter
	Log     *slog.Logger
	Bus     *events.Bus
	Owner   string
	// Now is overridable for tests.
	Now func() time.Time
}

// NewSecurityScanTask rejects the retired synthetic scan path. Real scans are
// created through the security API and completed by security:ingest_scan_results.
func NewSecurityScanTask(payload SecurityScanPayload) (*asynq.Task, error) {
	_ = payload
	return nil, fmt.Errorf("security:scan is retired; create a ClusterScan through the security API")
}

// NewSecurityIngestTask schedules the next poll of a ClusterScanReport. This
// is exported so handler code can rebuild the same task shape without having
// to import the asynq package directly.
func NewSecurityIngestTask(payload SecurityScanIngestPayload) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal security ingest payload: %w", err)
	}
	return asynq.NewTask(SecurityIngestType, data, asynq.MaxRetry(3), asynq.Timeout(2*time.Minute)), nil
}

// HandleSecurityScan drains legacy queue entries without fabricating an empty
// completed scan result. SkipRetry keeps the invalid operation visible as a
// terminal queue failure while preventing repeated execution.
func HandleSecurityScan(_ context.Context, t *asynq.Task) error {
	var p SecurityScanPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal security scan payload: %w", err)
	}
	if p.ClusterID == "" {
		return fmt.Errorf("cluster_id is required")
	}
	if _, err := uuid.Parse(p.ClusterID); err != nil {
		return fmt.Errorf("invalid cluster_id: %w", err)
	}
	return fmt.Errorf("security:scan is retired; use security:ingest_scan_results: %w", asynq.SkipRetry)
}

// HandleSecurityIngest polls the ClusterScanReport for the given scan and
// either ingests it (success), reschedules another poll (still running), or
// marks the scan failed (timeout / unrecoverable error).
func (runtime SecurityIngestRuntime) HandleSecurityIngest(ctx context.Context, t *asynq.Task) error {
	runtime = runtime.normalized()
	var p SecurityScanIngestPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal security ingest payload: %w", err)
	}
	if p.ScanID == "" {
		return fmt.Errorf("scan_id is required")
	}
	if runtime.Deps.Queries == nil || runtime.Deps.K8s == nil || runtime.Deps.Outbox == nil {
		return fmt.Errorf("security ingest runtime is not configured")
	}

	scanID, err := uuid.Parse(p.ScanID)
	if err != nil {
		return fmt.Errorf("invalid scan_id: %w", err)
	}

	stored, err := runtime.Deps.Queries.GetSecurityScanResultByID(ctx, scanID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load security scan: %w", err)
	}
	if !securityScanActive(stored) {
		return nil
	}
	generation := p.Generation
	if generation == 0 {
		generation = stored.PollGeneration
	}
	if generation != stored.PollGeneration {
		return nil
	}
	now := runtime.Deps.Now().UTC()
	claimed, err := runtime.Deps.Queries.ClaimSecurityScanPoll(ctx, sqlc.ClaimSecurityScanPollParams{
		Owner:          runtime.Deps.Owner,
		LeaseExpiresAt: timestamptz(now.Add(ingestLease)),
		ID:             scanID,
		Generation:     generation,
		NowAt:          timestamptz(now),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("claim security scan poll: %w", err)
	}
	if claimed.PollDeadline.Valid && !now.Before(claimed.PollDeadline.Time) {
		return runtime.finishSecurityScanFailed(ctx, claimed, "ClusterScanReport ingestion deadline exceeded")
	}
	if claimed.PollAttempt > ingestMaxAttempts {
		return runtime.finishSecurityScanFailed(ctx, claimed, fmt.Sprintf("ClusterScanReport not available after %d attempts", ingestMaxAttempts))
	}

	report, found, err := fetchClusterScanReport(ctx, runtime.Deps.K8s, claimed.ClusterID.String(), claimed.ClusterScanName)
	if err != nil {
		if isTerminalSecurityIngestError(err) {
			return runtime.finishSecurityScanFailed(ctx, claimed, err.Error())
		}
		return runtime.rescheduleSecurityScan(ctx, claimed, err.Error())
	}
	if !found {
		return runtime.rescheduleSecurityScan(ctx, claimed, "report not yet available")
	}

	counts, findings, summaryRaw, resultsRaw := flattenClusterScanReport(report)

	rows, err := runtime.Deps.Queries.FinalizeSecurityScanReport(ctx, sqlc.FinalizeSecurityScanReportParams{
		Summary: summaryRaw, Results: resultsRaw, Passed: counts.Pass, Failed: counts.Fail,
		Warned: counts.Warn, Skipped: counts.Skip, Findings: findings,
		UpstreamReportName: reportObjectName(report), ID: claimed.ID,
		Generation: claimed.PollGeneration, Owner: runtime.Deps.Owner,
	})
	if err != nil {
		runtime.Deps.Log.Error("update security scan report failed",
			"scan_id", p.ScanID, "error", err)
		return err
	}
	if rows == 0 {
		return nil
	}
	runtime.publishSecurityScanChanged(claimed)
	runtime.Deps.Log.Info("security scan ingested",
		"scan_id", p.ScanID,
		"pass", counts.Pass, "fail", counts.Fail, "warn", counts.Warn, "skip", counts.Skip)
	return nil
}

func (runtime SecurityIngestRuntime) rescheduleSecurityScan(ctx context.Context, scan sqlc.SecurityScanResult, reason string) error {
	next := runtime.Deps.Now().UTC().Add(ingestPollInterval)
	rows, err := runtime.Deps.Queries.RescheduleSecurityScanPoll(ctx, sqlc.RescheduleSecurityScanPollParams{
		NextPollAt: timestamptz(next), Reason: reason, ID: scan.ID,
		Generation: scan.PollGeneration, Owner: runtime.Deps.Owner,
	})
	if err != nil || rows == 0 {
		return err
	}
	runtime.publishSecurityScanChanged(scan)
	return runtime.enqueueSecurityScanPoll(ctx, scan, next,
		fmt.Sprintf("security_scan_ingest:%s:%d:%d", scan.ID, scan.PollGeneration, scan.PollAttempt+1))
}

func (runtime SecurityIngestRuntime) finishSecurityScanFailed(ctx context.Context, scan sqlc.SecurityScanResult, reason string) error {
	rows, err := runtime.Deps.Queries.FailSecurityScanPoll(ctx, sqlc.FailSecurityScanPollParams{
		Reason: reason, ID: scan.ID, Generation: scan.PollGeneration, Owner: runtime.Deps.Owner,
	})
	if err != nil {
		return err
	}
	if rows > 0 {
		runtime.publishSecurityScanChanged(scan)
		runtime.Deps.Log.Warn("security scan ingestion failed", "scan_id", scan.ID, "reason", reason)
	}
	return nil
}

func (runtime SecurityIngestRuntime) enqueueSecurityScanPoll(ctx context.Context, scan sqlc.SecurityScanResult, due time.Time, dedupe string) error {
	task, err := NewSecurityIngestTask(SecurityScanIngestPayload{ScanID: scan.ID.String(), Generation: scan.PollGeneration})
	if err != nil {
		return err
	}
	_, err = EnqueueTaskOutbox(ctx, runtime.Deps.Outbox, task, TaskOutboxOptions{
		DedupeKey: dedupe, QueueName: ClusterTemplateApplyQueueName, MaxRetry: 3,
		Timeout: 2 * time.Minute, MaxDeliveryAttempts: 20, NextAttemptAt: due,
	})
	return err
}

// HandleSecurityIngestRecovery is the periodic repair path for the crash
// windows on both sides of Redis delivery. Row leases make duplicate recovery
// tasks harmless, while a time-bucketed outbox key can replace a delivery that
// Redis acknowledged and subsequently lost.
func (runtime SecurityIngestRuntime) HandleSecurityIngestRecovery(ctx context.Context, _ *asynq.Task) error {
	runtime = runtime.normalized()
	return runPeriodicTaskWithLeaderUsing(ctx, runtime.Leader, runtime.Deps.Log, SecurityIngestRecoveryType, func() error {
		if runtime.Deps.Queries == nil || runtime.Deps.Outbox == nil {
			return fmt.Errorf("security ingest recovery runtime is not configured")
		}
		now := runtime.Deps.Now().UTC()
		rows, err := runtime.Deps.Queries.ListRecoverableSecurityScans(ctx, sqlc.ListRecoverableSecurityScansParams{
			NowAt: timestamptz(now), RowLimit: ingestRecoveryRows,
		})
		if err != nil {
			return fmt.Errorf("list recoverable security scans: %w", err)
		}
		var firstErr error
		for _, scan := range rows {
			if scan.PollDeadline.Valid && !now.Before(scan.PollDeadline.Time) {
				changed, failErr := runtime.Deps.Queries.FailSecurityScanPoll(ctx, sqlc.FailSecurityScanPollParams{
					Reason: "ClusterScanReport ingestion deadline exceeded", ID: scan.ID,
					Generation: scan.PollGeneration, Owner: "",
				})
				if failErr == nil && changed > 0 {
					runtime.publishSecurityScanChanged(scan)
				}
				if failErr != nil && firstErr == nil {
					firstErr = failErr
				}
				continue
			}
			dedupe := fmt.Sprintf("security_scan_recovery:%s:%d:%d", scan.ID, scan.PollGeneration, now.Unix()/60)
			if enqueueErr := runtime.enqueueSecurityScanPoll(ctx, scan, now, dedupe); enqueueErr != nil && firstErr == nil {
				firstErr = enqueueErr
			}
		}
		return firstErr
	})
}

func (runtime SecurityIngestRuntime) publishSecurityScanChanged(scan sqlc.SecurityScanResult) {
	events.PublishChanged(runtime.Deps.Bus, "cis_scan", scan.ClusterID.String(), scan.ID.String(), nil)
	events.PublishChanged(runtime.Deps.Bus, "security_scan", scan.ClusterID.String(), scan.ID.String(), nil)
}

func securityScanActive(scan sqlc.SecurityScanResult) bool {
	if scan.CancelRequestedAt.Valid {
		return false
	}
	switch scan.Status {
	case "pending", "running", "in_progress":
		return true
	default:
		return false
	}
}

func timestamptz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

type terminalSecurityIngestError struct{ err error }

func (e terminalSecurityIngestError) Error() string { return e.err.Error() }
func (e terminalSecurityIngestError) Unwrap() error { return e.err }

func isTerminalSecurityIngestError(err error) bool {
	var terminal terminalSecurityIngestError
	return errors.As(err, &terminal)
}

func securityIngestStatusError(status int) error {
	err := fmt.Errorf("unexpected Kubernetes API status %d", status)
	if status >= http.StatusBadRequest && status < http.StatusInternalServerError && status != http.StatusNotFound {
		return terminalSecurityIngestError{err: err}
	}
	return err
}

func reportObjectName(report map[string]any) string {
	metadata, _ := report["metadata"].(map[string]any)
	name, _ := metadata["name"].(string)
	return strings.TrimSpace(name)
}

// fetchClusterScanReport queries the per-cluster API for a ClusterScanReport
// matching the upstream ClusterScan name. Returns (report, true, nil) when
// the report exists, (nil, false, nil) when the operator hasn't produced
// one yet, and (nil, false, err) for unexpected errors.
func fetchClusterScanReport(ctx context.Context, fetcher SecurityIngestK8sFetcher, clusterID, scanName string) (map[string]any, bool, error) {
	reportName, found, err := resolveClusterScanReportName(ctx, fetcher, clusterID, scanName)
	if err != nil || !found {
		return nil, found, err
	}
	path := fmt.Sprintf("/apis/cis.cattle.io/v1/clusterscanreports/%s", reportName)
	resp, err := fetcher.Do(ctx, clusterID, http.MethodGet, path, nil, map[string]string{"Accept": "application/json"})
	if err != nil {
		return nil, false, err
	}
	if resp == nil {
		return nil, false, errors.New("empty Kubernetes API response")
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, false, securityIngestStatusError(resp.StatusCode)
	}
	body, err := decodeIngestBody(resp)
	if err != nil {
		return nil, false, err
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, false, terminalSecurityIngestError{err: fmt.Errorf("decode ClusterScanReport JSON: %w", err)}
	}
	return out, true, nil
}

func resolveClusterScanReportName(ctx context.Context, fetcher SecurityIngestK8sFetcher, clusterID, scanName string) (string, bool, error) {
	if name, found, err := fetchClusterScanReportNameFromScan(ctx, fetcher, clusterID, scanName); err != nil || found {
		return name, found, err
	}
	return findClusterScanReportNameByOwner(ctx, fetcher, clusterID, scanName)
}

func fetchClusterScanReportNameFromScan(ctx context.Context, fetcher SecurityIngestK8sFetcher, clusterID, scanName string) (string, bool, error) {
	path := fmt.Sprintf("/apis/cis.cattle.io/v1/clusterscans/%s", scanName)
	resp, err := fetcher.Do(ctx, clusterID, http.MethodGet, path, nil, map[string]string{"Accept": "application/json"})
	if err != nil {
		return "", false, err
	}
	if resp == nil {
		return "", false, errors.New("empty Kubernetes API response")
	}
	if resp.StatusCode == http.StatusNotFound {
		return "", false, nil
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return "", false, securityIngestStatusError(resp.StatusCode)
	}
	body, err := decodeIngestBody(resp)
	if err != nil {
		return "", false, err
	}
	var scan struct {
		Status struct {
			ReportName string `json:"reportName"`
		} `json:"status"`
	}
	if err := json.Unmarshal(body, &scan); err != nil {
		return "", false, terminalSecurityIngestError{err: fmt.Errorf("decode ClusterScan JSON: %w", err)}
	}
	if scan.Status.ReportName == "" {
		return "", false, nil
	}
	return scan.Status.ReportName, true, nil
}

func findClusterScanReportNameByOwner(ctx context.Context, fetcher SecurityIngestK8sFetcher, clusterID, scanName string) (string, bool, error) {
	resp, err := fetcher.Do(ctx, clusterID, http.MethodGet, "/apis/cis.cattle.io/v1/clusterscanreports", nil, map[string]string{"Accept": "application/json"})
	if err != nil {
		return "", false, err
	}
	if resp == nil {
		return "", false, errors.New("empty Kubernetes API response")
	}
	if resp.StatusCode == http.StatusNotFound {
		return "", false, nil
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return "", false, securityIngestStatusError(resp.StatusCode)
	}
	body, err := decodeIngestBody(resp)
	if err != nil {
		return "", false, err
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name            string `json:"name"`
				OwnerReferences []struct {
					Name string `json:"name"`
				} `json:"ownerReferences"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return "", false, terminalSecurityIngestError{err: fmt.Errorf("decode ClusterScanReport list JSON: %w", err)}
	}
	for _, item := range list.Items {
		if item.Metadata.Name == scanName {
			return item.Metadata.Name, true, nil
		}
		for _, owner := range item.Metadata.OwnerReferences {
			if owner.Name == scanName {
				return item.Metadata.Name, true, nil
			}
		}
	}
	return "", false, nil
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
	reportPayload := scanner.DecodeCISReportJSON(spec)

	if v, ok := scanner.NumericField(reportPayload, "total"); ok {
		counts.Total = v
	}
	if v, ok := scanner.NumericField(reportPayload, "pass"); ok {
		counts.Pass = v
	}
	if v, ok := scanner.NumericField(reportPayload, "fail"); ok {
		counts.Fail = v
	}
	if v, ok := scanner.NumericField(reportPayload, "warn"); ok {
		counts.Warn = v
	}
	if v, ok := scanner.NumericField(reportPayload, "skip"); ok {
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
			id := scanner.StringField(test, "id", "test_number", "number")
			status := scanner.StringField(test, "state", "status")
			finding := map[string]any{
				"test_id":     id,
				"severity":    scanner.StringField(test, "scored_severity", "severity"),
				"status":      status,
				"description": scanner.StringField(test, "test_desc", "description", "desc"),
				"remediation": scanner.StringField(test, "remediation"),
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
		"version": scanner.StringField(spec, "scanProfileName"),
	})
	findingsRaw, _ := json.Marshal(findings)
	return counts, findingsRaw, summaryRaw, resultsRaw
}

func decodeIngestBody(resp *protocol.K8sResponsePayload) ([]byte, error) {
	if resp == nil || resp.Body == "" {
		return nil, nil
	}
	if len(resp.Body) > base64.StdEncoding.EncodedLen(maxIngestBodyBytes) {
		return nil, terminalSecurityIngestError{err: fmt.Errorf("Kubernetes API response exceeds %d bytes", maxIngestBodyBytes)}
	}
	body, err := base64.StdEncoding.DecodeString(resp.Body)
	if err != nil {
		return nil, terminalSecurityIngestError{err: fmt.Errorf("decode Kubernetes API response body: %w", err)}
	}
	if len(body) > maxIngestBodyBytes {
		return nil, terminalSecurityIngestError{err: fmt.Errorf("Kubernetes API response exceeds %d bytes", maxIngestBodyBytes)}
	}
	return body, nil
}
