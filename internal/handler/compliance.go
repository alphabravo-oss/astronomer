// Package handler — compliance report export endpoint.
//
// SOC 2 / ISO 27001 audit prep used to be a 4-hour SQL adventure:
// grab the audit log, hand-export RBAC bindings, ssh into a cluster
// to dump the policy snapshot, etc. This handler turns that into a
// single GET on /api/v1/admin/compliance/export/ which streams a ZIP
// bundle of CSVs + JSON keyed to the relevant control IDs.
//
// The handler streams the ZIP directly. The previous async branch
// returned an Asynq job ID without a registered worker or durable job
// state; leaving that path enabled made large exports look queued
// while they could never complete.
//
// Both paths gate on superuser inside the handler (same pattern as
// admin_drill.go / admin_queues.go / support_bundle.go) so the
// rejection mode is a clean 403 — easier to debug than a generic
// permission-middleware rejection.
package handler

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// ── metrics ────────────────────────────────────────────────────────────

var (
	complianceExportsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Subsystem: "compliance",
			Name:      "exports_total",
			Help:      "Total number of compliance export bundles requested, labelled by transport.",
		},
		observability.MetricLabels("outcome"),
	)

	complianceExportBytes = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "astronomer",
			Subsystem: "compliance",
			Name:      "export_bytes",
			Help:      "Size in bytes of the compliance export bundle (inline path).",
			Buckets:   prometheus.ExponentialBuckets(1024, 4, 9), // 1KB..16GB
		},
		observability.MetricLabels(),
	)
)

func init() {
	prometheus.MustRegister(complianceExportsTotal)
	prometheus.MustRegister(complianceExportBytes)
}

// ── querier seams ──────────────────────────────────────────────────────

// ComplianceQuerier is the union of the per-section querier interfaces
// the handler delegates to. In production it's satisfied by
// *sqlc.Queries; tests substitute a narrow fake that implements only
// the methods the test exercises.
type ComplianceQuerier interface {
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)

	AuditCounter
	AuditExporter
	RBACSnapshotQuerier
	ClusterInventoryQuerier
	AccessTokenQuerier
	BackupDrillHistoryQuerier
	ProjectPolicyQuerier
}

// ── handler ────────────────────────────────────────────────────────────

// ComplianceHandler wraps the /api/v1/admin/compliance/* endpoints.
type ComplianceHandler struct {
	queries ComplianceQuerier
	now     func() time.Time
}

// NewComplianceHandler returns a wired handler. The second parameter is kept
// for call-site compatibility while async compliance export remains disabled
// until it has durable status and storage.
func NewComplianceHandler(queries ComplianceQuerier, _ any) *ComplianceHandler {
	return &ComplianceHandler{
		queries: queries,
		now:     time.Now,
	}
}

// ── HTTP handlers ──────────────────────────────────────────────────────

// Export handles GET /api/v1/admin/compliance/export/?from=&to=.
//
// Streams a ZIP body directly. Async export is intentionally disabled until it
// has a registered worker and durable job/output state.
func (h *ComplianceHandler) Export(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.gate(w, r); !ok {
		return
	}

	from, to, err := parseComplianceRange(r)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_range", err.Error())
		return
	}

	// Audit the request itself (read-only superuser endpoint that
	// exposes platform internals — same convention as support-bundle).
	recordAudit(r, h.queries, "admin.compliance.export_requested",
		"platform", "", "compliance-export", map[string]any{
			"from": from.UTC().Format(time.RFC3339),
			"to":   to.UTC().Format(time.RFC3339),
		})

	h.streamInline(w, r, from, to)
}

// GetExportStatus handles GET /api/v1/admin/compliance/exports/{id}/.
// Async compliance exports are intentionally disabled until the system has a
// durable job table plus object storage writer. Keep the route so old clients
// receive a clear 404 instead of a missing-route response.
func (h *ComplianceHandler) GetExportStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.gate(w, r); !ok {
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	idx := strings.LastIndex(path, "/")
	if idx == -1 {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Missing export id")
		return
	}
	id := path[idx+1:]
	if id == "" {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Missing export id")
		return
	}
	RespondRequestError(w, r, http.StatusNotFound, "not_found", "Async compliance exports are not enabled; request a new export")
}

// ── inline streaming path ──────────────────────────────────────────────

// streamInline writes the ZIP bundle directly into the response.
// Every section is best-effort: a per-section failure populates the
// manifest README but doesn't abort the whole bundle.
func (h *ComplianceHandler) streamInline(w http.ResponseWriter, r *http.Request, from, to time.Time) {
	filename := fmt.Sprintf("astronomer-compliance-%s-%s.zip",
		from.UTC().Format("20060102"),
		to.UTC().Format("20060102"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)

	// Wrap the ResponseWriter so we can report bundle size on
	// completion. ResponseWriter is buffered by net/http anyway; this
	// is purely a metering wrapper.
	mw := &meteringWriter{Writer: w}

	zw := zip.NewWriter(mw)

	log := newSectionLog()
	h.writeAllSections(r.Context(), zw, log, from, to)
	h.writeBundleReadme(zw, log, from, to)
	if err := zw.Close(); err != nil {
		slog.Warn("failed to finish compliance export", "error", err)
	}

	complianceExportsTotal.WithLabelValues(observability.MetricValues("inline")...).Inc()
	complianceExportBytes.WithLabelValues(observability.MetricValues()...).Observe(float64(mw.n))
}

// writeAllSections runs each writer into the ZIP. Errors are
// collected into `log`; the README writer renders them at the end.
func (h *ComplianceHandler) writeAllSections(ctx context.Context, zw *zip.Writer, log *sectionLog, from, to time.Time) {
	if h.queries == nil {
		log.skipped("audit-log.csv", "queries not configured")
		log.skipped("auth-events.csv", "queries not configured")
		log.skipped("rbac-snapshot.csv", "queries not configured")
		log.skipped("cluster-inventory.csv", "queries not configured")
		log.skipped("access-tokens.csv", "queries not configured")
		log.skipped("backup-drill-history.csv", "queries not configured")
		log.skipped("policy-snapshot.json", "queries not configured")
		return
	}

	// Per-section helper. Captures (zw, log) so each writer call
	// reads at a single glance.
	emit := func(name string, fn func(w io.Writer) (int64, error)) {
		fw, err := zw.Create(name)
		if err != nil {
			log.section(name, err)
			return
		}
		count, err := fn(fw)
		if err != nil {
			log.section(name, fmt.Errorf("%w (wrote %d rows)", err, count))
			return
		}
		log.section(name, nil)
	}

	emit("audit-log.csv", func(w io.Writer) (int64, error) {
		return WriteAuditLogCSV(ctx, w, from, to, h.queries)
	})
	emit("auth-events.csv", func(w io.Writer) (int64, error) {
		return WriteAuthEventsCSV(ctx, w, from, to, h.queries)
	})
	emit("rbac-snapshot.csv", func(w io.Writer) (int64, error) {
		return WriteRBACSnapshotCSV(ctx, w, h.queries)
	})
	emit("cluster-inventory.csv", func(w io.Writer) (int64, error) {
		return WriteClusterInventoryCSV(ctx, w, h.queries)
	})
	emit("access-tokens.csv", func(w io.Writer) (int64, error) {
		return WriteAccessTokensCSV(ctx, w, h.queries)
	})
	emit("backup-drill-history.csv", func(w io.Writer) (int64, error) {
		return WriteBackupDrillHistoryCSV(ctx, w, h.queries)
	})

	// policy-snapshot.json has a distinct signature — wrap separately.
	if fw, err := zw.Create("policy-snapshot.json"); err == nil {
		log.section("policy-snapshot.json", WritePolicySnapshotJSON(ctx, fw, h.queries, h.queries))
	} else {
		log.section("policy-snapshot.json", err)
	}
}

// writeBundleReadme is the bundle's human-readable manifest +
// SOC 2 / ISO 27001 control mapping. The mapping is curated by hand
// — an auditor reads this file first to know which CSV maps to
// which control narrative.
func (h *ComplianceHandler) writeBundleReadme(zw *zip.Writer, log *sectionLog, from, to time.Time) {
	var b strings.Builder
	b.WriteString("Astronomer compliance export\n")
	b.WriteString("============================\n\n")
	fmt.Fprintf(&b, "Generated: %s\n", h.now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "Range:     %s — %s (UTC, half-open)\n\n",
		from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))

	b.WriteString("Contents (per-section outcome):\n")
	for _, line := range log.lines {
		b.WriteString("  - " + line + "\n")
	}

	b.WriteString(`
SOC 2 / ISO 27001 control mapping
---------------------------------

  audit-log.csv             SOC 2 CC7.2 (System Monitoring), CC7.3
                            (Anomaly Detection); ISO 27001 A.12.4.1
                            (Event logging), A.12.4.3 (Administrator
                            and operator logs).

  auth-events.csv           SOC 2 CC6.1 (Logical Access), CC6.2
                            (Authentication), CC6.3 (User
                            Provisioning); ISO 27001 A.9.2.1
                            (User registration), A.9.2.6 (Removal
                            or adjustment of access rights), A.9.4.2
                            (Secure log-on).

  rbac-snapshot.csv         SOC 2 CC6.1 (Logical Access), CC6.3
                            (User Provisioning); ISO 27001 A.9.1.1
                            (Access control policy), A.9.2.5
                            (Review of user access rights), A.9.4.1
                            (Information access restriction). Source
                            column ('manual' vs 'group_sync')
                            distinguishes admin-assigned rights from
                            IdP-driven assignments.

  cluster-inventory.csv     SOC 2 CC2.1 (Information & Comm.), CC8.1
                            (Change Management); ISO 27001 A.8.1.1
                            (Inventory of assets), A.8.1.2
                            (Ownership of assets).

  access-tokens.csv         SOC 2 CC6.1 (Logical Access), CC6.7
                            (Transmission of Information); ISO 27001
                            A.9.4.1 (Information access
                            restriction), A.9.4.3 (Password
                            management system). Token hashes are
                            INTENTIONALLY OMITTED — defence in depth.

  backup-drill-history.csv  SOC 2 A1.2 (System Recovery), CC9.1
                            (Risk Mitigation); ISO 27001 A.12.3.1
                            (Information backup), A.17.1.3 (Verify,
                            review and evaluate information security
                            continuity).

  policy-snapshot.json      SOC 2 CC6.6 (Network Security), CC6.8
                            (Malicious Software); ISO 27001 A.13.1.3
                            (Segregation in networks), A.14.2.5
                            (Secure system engineering principles).
                            Per-project pod_security_profile +
                            resource_quota_* + network_policy_mode
                            give the auditor the workload-isolation
                            baseline at the moment of export.

Verification notes
------------------

  - Timestamps are RFC3339, UTC.
  - The range is half-open: [from, to). A row at exactly ` + "`to`" + `
    is NOT included.
  - The 'detail' column in audit-log.csv is JSONB serialised as a
    compact JSON string in a single CSV cell; csv.Writer quoting
    handles embedded newlines and commas.
  - Per-section failures are recorded above; the affected file may
    be absent or truncated. A successful run shows every section as
    "OK".
`)
	fw, err := zw.Create("README.md")
	if err != nil {
		return
	}
	_, _ = io.Copy(fw, strings.NewReader(b.String()))
}

// ── gating ─────────────────────────────────────────────────────────────

// gate enforces superuser-only access. Mirrors the pattern in
// admin_drill.go / admin_queues.go.
func (h *ComplianceHandler) gate(w http.ResponseWriter, r *http.Request) (sqlc.User, bool) {
	return requireSuperuser(w, r, h.queries, superuserGateConfig{
		StoreUnavailableMessage: "Compliance store not configured",
		ForbiddenMessage:        "Compliance export requires superuser privileges",
	})
}

// ── parsing ────────────────────────────────────────────────────────────

// parseComplianceRange parses the ?from=&to= query parameters. The
// range is half-open: rows with created_at in [from, to) are
// included. Both parameters are required and must parse as RFC3339
// or YYYY-MM-DD.
func parseComplianceRange(r *http.Request) (time.Time, time.Time, error) {
	q := r.URL.Query()
	fromRaw := q.Get("from")
	toRaw := q.Get("to")
	if fromRaw == "" || toRaw == "" {
		return time.Time{}, time.Time{}, errors.New("from and to are required (RFC3339 or YYYY-MM-DD)")
	}
	from, err := parseDateFlexible(fromRaw)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid from: %w", err)
	}
	to, err := parseDateFlexible(toRaw)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid to: %w", err)
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, errors.New("to must be after from")
	}
	return from, to, nil
}

func parseDateFlexible(raw string) (time.Time, error) {
	// Accept the date-only form (the most common audit-prep input)
	// AND full RFC3339 so power users can scope to a sub-day window.
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("expected RFC3339 or YYYY-MM-DD, got %q", raw)
}

// ── meta helpers ───────────────────────────────────────────────────────

// meteringWriter wraps an io.Writer and counts bytes passed through
// so the inline path can emit a size histogram on completion.
type meteringWriter struct {
	io.Writer
	n int64
}

func (m *meteringWriter) Write(p []byte) (int, error) {
	n, err := m.Writer.Write(p)
	m.n += int64(n)
	return n, err
}
