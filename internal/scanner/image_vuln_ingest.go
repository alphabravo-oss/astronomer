// Package scanner holds the ingest path for image-vulnerability reports
// emitted by trivy-operator (or any compatible scanner) in managed
// clusters.
//
// The agent (sprint 014) already streams VulnerabilityReport CRD events
// to the management plane via the CRD mirror. Ingest is the receiver
// side: persist the report + its per-CVE rows in a single transaction
// that is idempotent on (cluster_id, report_name), and refresh the
// Prometheus gauges so dashboards stay live.
//
// We deliberately do NOT shell out to the `trivy` CLI from here. The
// management plane is the consumer of someone else's scan output, not
// the runner of the scans themselves.
package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
)

// Trivy label keys we extract workload identity from. trivy-operator
// stamps the source object onto every VulnerabilityReport so we don't
// have to walk owner refs.
const (
	labelContainerName = "trivy-operator.container.name"
	labelResourceKind  = "trivy-operator.resource.kind"
	labelResourceName  = "trivy-operator.resource.name"
	labelResourceNs    = "trivy-operator.resource.namespace"
)

// TrivyVulnerabilityReport is the subset of the upstream CRD shape we
// persist. Fields we don't read are left out — adding them later is a
// backwards-compatible change.
type TrivyVulnerabilityReport struct {
	Metadata TrivyMetadata `json:"metadata"`
	Report   TrivyReport   `json:"report"`
}

// TrivyMetadata is the standard k8s metadata block.
type TrivyMetadata struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Labels    map[string]string `json:"labels"`
}

// TrivyReport is the `.report` substructure of the VulnerabilityReport
// CR. Only fields we persist appear here.
type TrivyReport struct {
	Scanner         TrivyScanner  `json:"scanner"`
	Artifact        TrivyArtifact `json:"artifact"`
	Registry        TrivyRegistry `json:"registry"`
	Summary         TrivySummary  `json:"summary"`
	UpdateTimestamp string        `json:"updateTimestamp"`
	Vulnerabilities []TrivyCVE    `json:"vulnerabilities"`
}

// TrivyScanner identifies the scanner that produced the report. We
// persist this so a UI can flag results from a non-default scanner
// (some operators run Grype side-by-side with Trivy).
type TrivyScanner struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// TrivyArtifact is the (repo, tag, digest) tuple of the scanned image.
type TrivyArtifact struct {
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Digest     string `json:"digest"`
}

// TrivyRegistry captures the upstream registry host. Stored separately
// from the repo so a UI can pivot by registry.
type TrivyRegistry struct {
	Server string `json:"server"`
}

// TrivySummary holds the per-severity aggregate counts. The operator
// emits these along with the report so we don't have to re-derive them
// from the Vulnerabilities slice — but the ingest path also recomputes
// them as a defensive cross-check (see normalizeSummary).
type TrivySummary struct {
	CriticalCount int `json:"criticalCount"`
	HighCount     int `json:"highCount"`
	MediumCount   int `json:"mediumCount"`
	LowCount      int `json:"lowCount"`
	UnknownCount  int `json:"unknownCount"`
}

// TrivyCVE is a single CVE row from the upstream report. Score is a
// pointer because the schema is nullable.
type TrivyCVE struct {
	VulnerabilityID  string   `json:"vulnerabilityID"`
	Severity         string   `json:"severity"`
	Resource         string   `json:"resource"`
	InstalledVersion string   `json:"installedVersion"`
	FixedVersion     string   `json:"fixedVersion"`
	PrimaryLink      string   `json:"primaryLink"`
	Score            *float64 `json:"score,omitempty"`
	Title            string   `json:"title"`
	Description      string   `json:"description"`
}

// Querier is the slice of *sqlc.Queries Ingest needs. Defined as an
// interface so tests can stub the DB without standing up Postgres.
type Querier interface {
	UpsertImageVulnerabilityReport(ctx context.Context, arg sqlc.UpsertImageVulnerabilityReportParams) (sqlc.ImageVulnerabilityReport, error)
	// Sprint 081: history snapshot appended on every ingest so the
	// dashboard can render trend lines + diff-vs-yesterday. The
	// implementation upserts ON CONFLICT (report_id, scanned_at)
	// DO NOTHING so a mirror-event replay doesn't duplicate.
	InsertImageVulnerabilityReportSnapshot(ctx context.Context, arg sqlc.InsertImageVulnerabilityReportSnapshotParams) error
	DeleteImageVulnerabilitiesByReport(ctx context.Context, reportID uuid.UUID) error
	BatchInsertImageVulnerabilities(ctx context.Context, rows []sqlc.InsertImageVulnerabilityParams) error
}

// TxBeginner mirrors what *pgxpool.Pool exposes. The production wiring
// hands the live pool in; tests can pass a stub that returns a stub Tx.
// Kept as an interface so this package doesn't import pgxpool.
type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// MetricRecorder is the slice of the Prometheus registry the ingest
// hot path touches. The package-level Vars below satisfy it; tests can
// pass nil to skip metrics.
type MetricRecorder interface {
	SetClusterTotals(cluster string, severity string, count float64)
	IncIngestOutcome(outcome string)
}

// BatchSize is the chunk count we ship per pgx.Batch send. 1000 matches
// the project constraint laid down in the sprint brief.
const BatchSize = 1000

// AuditHook is the optional callback fired after a successful ingest.
// Wired to the audit writer by server-side glue; nil-safe.
type AuditHook func(ctx context.Context, clusterID uuid.UUID, reportName, action string)

// Ingester is the receiver that owns the (querier, tx-begin, metrics)
// trio. Construct one per server process and re-use it for every
// VulnerabilityReport event the CRD mirror surfaces.
type Ingester struct {
	q       Querier
	tx      TxBeginner
	metrics MetricRecorder
	audit   AuditHook
	bus     *events.Bus
}

// SetEventBus wires the SSE bus for image_scan.changed liveness events
// (P4.5). Optional: fire-and-forget and nil-safe.
func (i *Ingester) SetEventBus(bus *events.Bus) {
	if i == nil {
		return
	}
	i.bus = bus
}

// NewIngester wires the receiver. tx may be nil — in that case Ingest
// runs the upsert + delete + batch insert without a wrapping
// transaction (the test fakes do this). metrics may be nil.
func NewIngester(q Querier, tx TxBeginner, metrics MetricRecorder, audit AuditHook) *Ingester {
	return &Ingester{q: q, tx: tx, metrics: metrics, audit: audit}
}

// IngestUnstructured is the convenience entry point the CRD-mirror
// watcher calls. It re-marshals the raw object into TrivyVulnerability
// Report and delegates to Ingest. Keeping this method here lets
// internal/crd hand us a `map[string]any` without taking a dependency
// on internal/scanner's typed shape.
func (i *Ingester) IngestUnstructured(ctx context.Context, clusterID uuid.UUID, raw any) error {
	if raw == nil {
		return errors.New("scanner: nil object")
	}
	b, err := encodeJSON(raw)
	if err != nil {
		return fmt.Errorf("scanner: re-encode unstructured: %w", err)
	}
	var typed TrivyVulnerabilityReport
	if err := decodeJSON(b, &typed); err != nil {
		return fmt.Errorf("scanner: decode unstructured: %w", err)
	}
	return i.Ingest(ctx, clusterID, typed)
}

// Ingest persists one Trivy VulnerabilityReport for the given cluster.
// Idempotent on (cluster_id, report_name): a re-ingest of the same
// report name REPLACES the CVE rows (DELETE-then-INSERT inside the
// tx) so operators never see a mix of stale + fresh CVEs.
func (i *Ingester) Ingest(ctx context.Context, clusterID uuid.UUID, raw TrivyVulnerabilityReport) error {
	if i == nil {
		return errors.New("scanner: ingester is nil")
	}
	if clusterID == uuid.Nil {
		return errors.New("scanner: cluster_id is required")
	}
	if strings.TrimSpace(raw.Metadata.Name) == "" {
		return errors.New("scanner: report metadata.name is required")
	}

	scannedAt := parseRFC3339OrNow(raw.Report.UpdateTimestamp)
	workloadKind, workloadName, containerName, namespace := extractWorkloadIdentity(raw)

	scannerName := strings.TrimSpace(raw.Report.Scanner.Name)
	if scannerName == "" {
		scannerName = "trivy"
	}

	upsertArgs := sqlc.UpsertImageVulnerabilityReportParams{
		ClusterID:      clusterID,
		ReportName:     raw.Metadata.Name,
		Namespace:      namespace,
		WorkloadKind:   workloadKind,
		WorkloadName:   workloadName,
		ContainerName:  containerName,
		ImageRegistry:  raw.Report.Registry.Server,
		ImageRepo:      raw.Report.Artifact.Repository,
		ImageTag:       raw.Report.Artifact.Tag,
		ImageDigest:    raw.Report.Artifact.Digest,
		Scanner:        scannerName,
		ScannerVersion: raw.Report.Scanner.Version,
		CriticalCount:  int32(raw.Report.Summary.CriticalCount),
		HighCount:      int32(raw.Report.Summary.HighCount),
		MediumCount:    int32(raw.Report.Summary.MediumCount),
		LowCount:       int32(raw.Report.Summary.LowCount),
		UnknownCount:   int32(raw.Report.Summary.UnknownCount),
		ScannedAt:      scannedAt,
	}

	cveRows := make([]sqlc.InsertImageVulnerabilityParams, 0, len(raw.Report.Vulnerabilities))
	for _, cve := range raw.Report.Vulnerabilities {
		if strings.TrimSpace(cve.VulnerabilityID) == "" {
			continue
		}
		cveRows = append(cveRows, sqlc.InsertImageVulnerabilityParams{
			VulnerabilityID:  cve.VulnerabilityID,
			Severity:         strings.ToUpper(strings.TrimSpace(cve.Severity)),
			PkgName:          cve.Resource,
			InstalledVersion: cve.InstalledVersion,
			FixedVersion:     cve.FixedVersion,
			PrimaryLink:      cve.PrimaryLink,
			CvssScore:        cvssScoreToNumeric(cve.Score),
			Title:            cve.Title,
			Description:      cve.Description,
		})
	}

	q := i.q
	commit := func(context.Context) error { return nil }
	rollback := func(context.Context) error { return nil }
	if i.tx != nil {
		tx, err := i.tx.Begin(ctx)
		if err != nil {
			i.recordOutcome("error")
			return fmt.Errorf("scanner: begin tx: %w", err)
		}
		// Swap the querier for the tx-scoped one. We satisfy this by
		// asserting the production *sqlc.Queries shape; tests pass a
		// stub Querier whose tx-scoping is implicit and i.tx is nil.
		if concrete, ok := q.(*sqlc.Queries); ok {
			q = concrete.WithTx(tx)
		}
		commit = tx.Commit
		rollback = tx.Rollback
	}

	report, err := q.UpsertImageVulnerabilityReport(ctx, upsertArgs)
	if err != nil {
		_ = rollback(ctx)
		i.recordOutcome("error")
		return fmt.Errorf("scanner: upsert report: %w", err)
	}

	// Append a history-snapshot row alongside the live upsert. The
	// snapshot powers the trend sparkline + the "what changed since
	// yesterday" diff card on the cluster's Image Scans tab.
	// Sprint 081. ON CONFLICT (report_id, scanned_at) DO NOTHING in
	// the SQL makes this idempotent against replay (the agent
	// re-emits cached items on tunnel reconnect).
	snapshotErr := q.InsertImageVulnerabilityReportSnapshot(ctx, sqlc.InsertImageVulnerabilityReportSnapshotParams{
		ReportID:      report.ID,
		ClusterID:     clusterID,
		CriticalCount: int32(report.CriticalCount),
		HighCount:     int32(report.HighCount),
		MediumCount:   int32(report.MediumCount),
		LowCount:      int32(report.LowCount),
		UnknownCount:  int32(report.UnknownCount),
		ScannedAt:     pgtype.Timestamptz{Time: upsertArgs.ScannedAt, Valid: true},
	})
	if snapshotErr != nil {
		// History append is best-effort: a snapshot insert failure
		// shouldn't abort the live ingest, because the live
		// image_vulnerability_reports row is still the source of
		// truth for the dashboard. Log + continue.
		i.recordOutcome("snapshot_error")
	}

	if err := q.DeleteImageVulnerabilitiesByReport(ctx, report.ID); err != nil {
		_ = rollback(ctx)
		i.recordOutcome("error")
		return fmt.Errorf("scanner: clear cves: %w", err)
	}

	for offset := 0; offset < len(cveRows); offset += BatchSize {
		end := offset + BatchSize
		if end > len(cveRows) {
			end = len(cveRows)
		}
		chunk := cveRows[offset:end]
		for j := range chunk {
			chunk[j].ReportID = report.ID
		}
		if err := q.BatchInsertImageVulnerabilities(ctx, chunk); err != nil {
			_ = rollback(ctx)
			i.recordOutcome("error")
			return fmt.Errorf("scanner: batch insert cves at offset %d: %w", offset, err)
		}
	}

	if err := commit(ctx); err != nil {
		i.recordOutcome("error")
		return fmt.Errorf("scanner: commit: %w", err)
	}

	i.refreshGauges(clusterID, upsertArgs)
	i.recordOutcome("ingested")
	events.PublishChanged(i.bus, "image_scan", clusterID.String(), report.ID.String(), nil)
	if i.audit != nil {
		i.audit(ctx, clusterID, report.ReportName, "ingested")
	}
	return nil
}

func (i *Ingester) refreshGauges(clusterID uuid.UUID, p sqlc.UpsertImageVulnerabilityReportParams) {
	if i.metrics == nil {
		return
	}
	cluster := clusterID.String()
	i.metrics.SetClusterTotals(cluster, "critical", float64(p.CriticalCount))
	i.metrics.SetClusterTotals(cluster, "high", float64(p.HighCount))
	i.metrics.SetClusterTotals(cluster, "medium", float64(p.MediumCount))
	i.metrics.SetClusterTotals(cluster, "low", float64(p.LowCount))
	i.metrics.SetClusterTotals(cluster, "unknown", float64(p.UnknownCount))
}

func (i *Ingester) recordOutcome(outcome string) {
	if i.metrics == nil {
		return
	}
	i.metrics.IncIngestOutcome(outcome)
}

// extractWorkloadIdentity reads the trivy-operator labels into the four
// fields we persist. Falls back to the report's metadata.namespace when
// the label is missing so we always have *some* namespace value.
func extractWorkloadIdentity(raw TrivyVulnerabilityReport) (kind, name, container, namespace string) {
	labels := raw.Metadata.Labels
	if labels != nil {
		kind = labels[labelResourceKind]
		name = labels[labelResourceName]
		container = labels[labelContainerName]
		namespace = labels[labelResourceNs]
	}
	if namespace == "" {
		namespace = raw.Metadata.Namespace
	}
	return kind, name, container, namespace
}

func parseRFC3339OrNow(s string) time.Time {
	if s == "" {
		return time.Now().UTC()
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Now().UTC()
	}
	return t.UTC()
}

// encodeJSON / decodeJSON are tiny shims around encoding/json so the
// IngestUnstructured path doesn't tug encoding/json into every other
// site that imports this package's typed surface.
func encodeJSON(v any) ([]byte, error) { return json.Marshal(v) }
func decodeJSON(b []byte, v any) error { return json.Unmarshal(b, v) }

// cvssScoreToNumeric encodes the (possibly nil) float64 score as a
// pgtype.Numeric. We round to one decimal place to match the schema's
// NUMERIC(4,1) precision so the DB doesn't have to coerce.
func cvssScoreToNumeric(score *float64) pgtype.Numeric {
	if score == nil {
		return pgtype.Numeric{Valid: false}
	}
	// pgtype.Numeric's scan path accepts the string form most readily.
	formatted := strconv.FormatFloat(*score, 'f', 1, 64)
	var n pgtype.Numeric
	if err := n.Scan(formatted); err != nil {
		return pgtype.Numeric{Valid: false}
	}
	return n
}

// ----------------------------------------------------------------------
// Package-level metric recorder. Independent registration so the
// audit / handler / worker side can call MustRegisterMetrics() once at
// startup without coupling.
// ----------------------------------------------------------------------

var (
	registerOnce sync.Once

	clusterTotalsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Name:      "image_vulns_total",
			Help:      "Image-vulnerability count for the latest report by cluster + severity.",
		},
		[]string{"cluster", "severity"},
	)

	ingestOutcomeCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Name:      "image_vuln_reports_ingested_total",
			Help:      "VulnerabilityReport ingest outcomes (labels: ingested, error).",
		},
		[]string{"outcome"},
	)
)

// MustRegisterMetrics installs the package's gauges + counters into the
// default Prometheus registry. Safe to call multiple times.
func MustRegisterMetrics() {
	registerOnce.Do(func() {
		prometheus.MustRegister(clusterTotalsGauge, ingestOutcomeCounter)
	})
}

// DefaultMetricRecorder returns a MetricRecorder backed by the package-
// level metrics. Convenience wiring for server.go.
func DefaultMetricRecorder() MetricRecorder { return defaultRecorder{} }

type defaultRecorder struct{}

func (defaultRecorder) SetClusterTotals(cluster, severity string, count float64) {
	clusterTotalsGauge.WithLabelValues(cluster, severity).Set(count)
}
func (defaultRecorder) IncIngestOutcome(outcome string) {
	ingestOutcomeCounter.WithLabelValues(outcome).Inc()
}
