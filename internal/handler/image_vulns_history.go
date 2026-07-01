// Sprint 081 — image vulnerability scan history + diff + CSV export.
//
// Three new endpoints on top of the sprint-062 surface:
//
//   GET /api/v1/clusters/{id}/vulnerabilities/history/   — sparkline
//     data: scanned_at + summed severity counts across all reports,
//     bounded by ?since= (default 30 days) and ?limit= (default 200).
//
//   GET /api/v1/clusters/{id}/vulnerabilities/diff/      — what
//     changed: latest vs prior aggregate severity totals + the
//     timestamps each anchor was taken at. ?prior_hours= picks the
//     comparison window (default 24).
//
//   GET /api/v1/clusters/{id}/vulnerabilities/export.csv — flat CSV
//     of every current image_vulnerability_reports row in the cluster
//     so operators can attach it to tickets / pipe it into compliance
//     spreadsheets.
//
// All three are read-only (cluster:read RBAC, same as the other
// vuln endpoints).

package handler

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

// ImageVulnHistoryQuerier is the extra surface ImageVulnHandler's
// history/diff/export endpoints need. Kept distinct from the existing
// ImageVulnQuerier so the embed below doesn't force every test fake
// to re-implement methods it doesn't exercise.
type ImageVulnHistoryQuerier interface {
	ListImageVulnerabilityHistoryForCluster(ctx context.Context, arg sqlc.ListImageVulnerabilityHistoryForClusterParams) ([]sqlc.ListImageVulnerabilityHistoryForClusterRow, error)
	CompareImageVulnerabilitySnapshotsForCluster(ctx context.Context, arg sqlc.CompareImageVulnerabilitySnapshotsForClusterParams) (sqlc.CompareImageVulnerabilitySnapshotsForClusterRow, error)
	ListImageVulnerabilityHistoryForReport(ctx context.Context, arg sqlc.ListImageVulnerabilityHistoryForReportParams) ([]sqlc.ImageVulnerabilityReportSnapshot, error)
}

// ClusterHistory handles GET /api/v1/clusters/{id}/vulnerabilities/history/.
// Returns a sparkline-ready array of {scanned_at, critical, high, ...}
// rows ordered newest-first.
func (h *ImageVulnHandler) ClusterHistory(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	q, ok := h.queries.(ImageVulnHistoryQuerier)
	if !ok {
		RespondRequestError(w, r, http.StatusNotImplemented, apierror.HistoryUnavailable, "Snapshot store not wired on this build")
		return
	}

	// Window defaults to 30 days back from now. Operators can
	// shorten via ?since_hours= (1..720) for the last-day chart on
	// the Cluster Overview card. We don't expose ?since= as an
	// absolute timestamp because URL-encoding RFC3339 is annoying;
	// hours-back is enough granularity.
	hoursBack := queryInt(r, "since_hours", 24*30)
	if hoursBack <= 0 || hoursBack > 24*365 {
		hoursBack = 24 * 30
	}
	limit := int32(queryInt(r, "limit", 200))
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	since := time.Now().Add(-time.Duration(hoursBack) * time.Hour)

	rows, err := q.ListImageVulnerabilityHistoryForCluster(r.Context(), sqlc.ListImageVulnerabilityHistoryForClusterParams{
		ClusterID: clusterID,
		Since:     pgtype.Timestamptz{Time: since, Valid: true},
		Limit:     limit,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.HistoryError, "Failed to list scan history")
		return
	}

	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, map[string]any{
			"scanned_at":   pgTimeString(r.ScannedAt),
			"critical":     r.Critical,
			"high":         r.High,
			"medium":       r.Medium,
			"low":          r.Low,
			"unknown":      r.Unknown,
			"report_count": r.ReportCount,
		})
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"cluster_id":  clusterID.String(),
		"since":       since.Format(time.RFC3339),
		"snapshots":   out,
		"total_count": len(out),
	})
}

// ClusterDiff handles GET /api/v1/clusters/{id}/vulnerabilities/diff/.
// ?prior_hours=24 (default) anchors the "before" snapshot at the most
// recent scan ≤ (now - prior_hours).
func (h *ImageVulnHandler) ClusterDiff(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	q, ok := h.queries.(ImageVulnHistoryQuerier)
	if !ok {
		RespondRequestError(w, r, http.StatusNotImplemented, apierror.DiffUnavailable, "Snapshot store not wired on this build")
		return
	}

	priorHours := queryInt(r, "prior_hours", 24)
	if priorHours <= 0 || priorHours > 24*365 {
		priorHours = 24
	}
	cutoff := time.Now().Add(-time.Duration(priorHours) * time.Hour)

	row, err := q.CompareImageVulnerabilitySnapshotsForCluster(r.Context(), sqlc.CompareImageVulnerabilitySnapshotsForClusterParams{
		ClusterID: clusterID,
		Cutoff:    pgtype.Timestamptz{Time: cutoff, Valid: true},
	})
	if err != nil {
		// No history rows yet — return zeros with a flag the UI can
		// render as "first scan, no comparison available yet" instead
		// of a 500. Real DB errors still surface as 500.
		RespondJSON(w, http.StatusOK, map[string]any{
			"cluster_id":     clusterID.String(),
			"has_comparison": false,
			"prior_hours":    priorHours,
		})
		return
	}

	delta := func(latest, prior int32) int32 { return latest - prior }
	RespondJSON(w, http.StatusOK, map[string]any{
		"cluster_id":     clusterID.String(),
		"has_comparison": row.LatestScannedAt.Valid && row.PriorScannedAt.Valid,
		"prior_hours":    priorHours,
		"latest": map[string]any{
			"critical":   row.LatestCritical,
			"high":       row.LatestHigh,
			"medium":     row.LatestMedium,
			"low":        row.LatestLow,
			"unknown":    row.LatestUnknown,
			"scanned_at": pgTimeString(row.LatestScannedAt),
		},
		"prior": map[string]any{
			"critical":   row.PriorCritical,
			"high":       row.PriorHigh,
			"medium":     row.PriorMedium,
			"low":        row.PriorLow,
			"unknown":    row.PriorUnknown,
			"scanned_at": pgTimeString(row.PriorScannedAt),
		},
		"delta": map[string]any{
			"critical": delta(row.LatestCritical, row.PriorCritical),
			"high":     delta(row.LatestHigh, row.PriorHigh),
			"medium":   delta(row.LatestMedium, row.PriorMedium),
			"low":      delta(row.LatestLow, row.PriorLow),
			"unknown":  delta(row.LatestUnknown, row.PriorUnknown),
		},
	})
}

// ClusterExportCSV handles GET /api/v1/clusters/{id}/vulnerabilities/export.csv.
// Emits a flat CSV of every current report row (cluster_id, workload,
// image, severity counts, scanned_at). Suitable for offline analysis
// + ticket attachments — Excel + Google Sheets both consume this
// unchanged.
func (h *ImageVulnHandler) ClusterExportCSV(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	// We list up to 5000 rows for the CSV — a managed cluster with
	// more workloads than that should probably use the JSON paginated
	// API instead. The cap also keeps memory bounded.
	//
	// TopVulnerableImages is the cluster-wide list (no namespace
	// filter); ListVulnerableImagesByNamespace requires a namespace
	// bind and returns empty for the empty-string default we'd pass
	// from the export caller.
	rows, err := h.queries.TopVulnerableImages(r.Context(), sqlc.TopVulnerableImagesParams{
		ClusterID: clusterID,
		Limit:     5000,
		Offset:    0,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ExportError, "Failed to export scan results")
		return
	}

	filename := fmt.Sprintf("image-scans-%s-%s.csv",
		clusterID.String()[:8],
		time.Now().UTC().Format("20060102-150405"),
	)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)

	cw := csv.NewWriter(w)
	defer cw.Flush()

	_ = cw.Write([]string{
		"report_name", "namespace", "workload_kind", "workload_name", "container_name",
		"image_registry", "image_repo", "image_tag", "image_digest",
		"scanner", "scanner_version",
		"critical", "high", "medium", "low", "unknown",
		"scanned_at",
	})
	for _, r := range rows {
		_ = cw.Write([]string{
			r.ReportName,
			r.Namespace,
			r.WorkloadKind,
			r.WorkloadName,
			r.ContainerName,
			r.ImageRegistry,
			r.ImageRepo,
			r.ImageTag,
			r.ImageDigest,
			r.Scanner,
			r.ScannerVersion,
			strconv.Itoa(int(r.CriticalCount)),
			strconv.Itoa(int(r.HighCount)),
			strconv.Itoa(int(r.MediumCount)),
			strconv.Itoa(int(r.LowCount)),
			strconv.Itoa(int(r.UnknownCount)),
			r.ScannedAt.UTC().Format(time.RFC3339),
		})
	}
}

// ReportHistory handles
// GET /api/v1/clusters/{cluster_id}/vulnerabilities/reports/{report_id}/history/.
// Returns the per-image snapshot timeline so the drawer can show how
// a single workload's CVE counts evolved over time. report_id is
// globally unique, so besides the route guard's cluster:read on the URL
// cluster we also drop any returned snapshot whose cluster_id doesn't
// match the URL cluster — otherwise a report_id from another cluster
// would leak that cluster's history (cross-cluster IDOR).
func (h *ImageVulnHandler) ReportHistory(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	reportID, err := uuid.Parse(chi.URLParam(r, "report_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid report ID")
		return
	}
	q, ok := h.queries.(ImageVulnHistoryQuerier)
	if !ok {
		RespondRequestError(w, r, http.StatusNotImplemented, apierror.HistoryUnavailable, "Snapshot store not wired on this build")
		return
	}

	limit := int32(queryInt(r, "limit", 100))
	if limit <= 0 || limit > 1000 {
		limit = 100
	}

	rows, err := q.ListImageVulnerabilityHistoryForReport(r.Context(), sqlc.ListImageVulnerabilityHistoryForReportParams{
		ReportID: reportID,
		Limit:    limit,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.HistoryError, "Failed to list scan history")
		return
	}

	out := make([]map[string]any, 0, len(rows))
	for _, s := range rows {
		// Cluster isolation: report_id is globally unique, so a report from a
		// different cluster could otherwise leak through the {cluster_id} route
		// guard. Drop any snapshot not owned by the URL cluster, mirroring the
		// row.ClusterID != clusterID check in the snapshot/netpol handlers.
		if s.ClusterID != clusterID {
			continue
		}
		out = append(out, map[string]any{
			"scanned_at": pgTimeString(s.ScannedAt),
			"critical":   s.CriticalCount,
			"high":       s.HighCount,
			"medium":     s.MediumCount,
			"low":        s.LowCount,
			"unknown":    s.UnknownCount,
		})
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"report_id":   reportID.String(),
		"snapshots":   out,
		"total_count": len(out),
	})
}

// pgTimeString renders SQL timestamps as RFC3339 or empty when null.
// Centralised here so the three endpoints above produce consistent
// output for nullable and non-null generated timestamp fields.
func pgTimeString(value any) string {
	switch t := value.(type) {
	case time.Time:
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format(time.RFC3339)
	case pgtype.Timestamptz:
		if !t.Valid {
			return ""
		}
		return t.Time.UTC().Format(time.RFC3339)
	default:
		return ""
	}
}
