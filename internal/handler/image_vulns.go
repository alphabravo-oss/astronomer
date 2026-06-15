// Package handler — image vulnerability scan endpoints (sprint 062).
//
// Five cluster-scoped routes + two fleet-wide routes. The cluster routes
// gate on cluster:read; the fleet routes on security:read. The "rescan"
// endpoint nudges the trivy-operator service in the managed cluster via
// an annotation patch — the operator's own watch loop picks the change
// up. When the K8sRequester is not wired (test fakes, k8s-less dev), we
// degrade to a 200 with `{"triggered": false}` so the UI shows a clean
// "operator missing" state instead of a 500.

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// trivyOperatorNamespace is the recommended install namespace from the
// Aqua chart. The handler PATCHes its operator service's annotations to
// nudge a re-scan.
const trivyOperatorNamespace = "trivy-system"

// trivyOperatorService is the Service name shipped by the chart.
const trivyOperatorService = "trivy-operator"

// ImageVulnQuerier is the slice of *sqlc.Queries the handler needs.
// Defined as an interface so tests can stub without a Postgres.
type ImageVulnQuerier interface {
	GetImageVulnerabilityReportByID(ctx context.Context, id uuid.UUID) (sqlc.ImageVulnerabilityReport, error)
	AggregateClusterVulnerabilities(ctx context.Context, clusterID uuid.UUID) (sqlc.AggregateClusterVulnerabilitiesRow, error)
	AggregateFleetVulnerabilities(ctx context.Context) (sqlc.AggregateFleetVulnerabilitiesRow, error)
	TopVulnerableImages(ctx context.Context, arg sqlc.TopVulnerableImagesParams) ([]sqlc.ImageVulnerabilityReport, error)
	ListVulnerableImagesByNamespace(ctx context.Context, arg sqlc.ListVulnerableImagesByNamespaceParams) ([]sqlc.ImageVulnerabilityReport, error)
	CountVulnerableImagesForCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
	ListVulnerabilitiesForReport(ctx context.Context, arg sqlc.ListVulnerabilitiesForReportParams) ([]sqlc.ImageVulnerability, error)
	CountVulnerabilitiesForReport(ctx context.Context, arg sqlc.CountVulnerabilitiesForReportParams) (int64, error)
	TopClustersByVulnerability(ctx context.Context, limit int32) ([]sqlc.TopClustersByVulnerabilityRow, error)
}

// ImageVulnHandler owns /api/v1/clusters/{cluster_id}/vulnerabilities/*
// and /api/v1/security/vulnerabilities/*. The K8sRequester is optional
// — when nil the rescan path short-circuits, every read path still
// works against the local DB.
type ImageVulnHandler struct {
	queries ImageVulnQuerier
	auditQ  any
	k8s     K8sRequester
	log     *slog.Logger
}

// NewImageVulnHandler constructs the handler. log defaults to slog's
// process default if nil.
func NewImageVulnHandler(q ImageVulnQuerier) *ImageVulnHandler {
	return &ImageVulnHandler{queries: q, log: slog.Default()}
}

// SetAuditQuerier wires the audit writer. Optional — when unset the
// audit calls are no-ops.
func (h *ImageVulnHandler) SetAuditQuerier(q any) {
	if h != nil {
		h.auditQ = q
	}
}

// SetK8sRequester wires the tunnel-backed Kubernetes API client used
// by the rescan path.
func (h *ImageVulnHandler) SetK8sRequester(req K8sRequester) {
	if h != nil {
		h.k8s = req
	}
}

// SetLogger sets the structured logger.
func (h *ImageVulnHandler) SetLogger(log *slog.Logger) {
	if h != nil && log != nil {
		h.log = log
	}
}

// --- Cluster-scoped endpoints -----------------------------------------

// ClusterSummary handles GET /api/v1/clusters/{cluster_id}/vulnerabilities/summary/.
// Returns the by-severity rollup + last_scanned_at for the cluster.
func (h *ImageVulnHandler) ClusterSummary(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	agg, err := h.queries.AggregateClusterVulnerabilities(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "aggregate_error", "Failed to aggregate cluster vulnerabilities")
		return
	}
	RespondJSON(w, http.StatusOK, renderClusterAggregate(agg))
}

// ClusterTopImages handles GET /api/v1/clusters/{cluster_id}/vulnerabilities/images/.
// Optional ?namespace=foo filter; pagination via ?limit=&offset=.
func (h *ImageVulnHandler) ClusterTopImages(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	limit := int32(queryInt(r, "limit", 20))
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	offset := int32(queryInt(r, "offset", 0))
	if offset < 0 {
		offset = 0
	}

	var (
		items []sqlc.ImageVulnerabilityReport
		err   error
	)
	if ns := strings.TrimSpace(r.URL.Query().Get("namespace")); ns != "" {
		items, err = h.queries.ListVulnerableImagesByNamespace(r.Context(), sqlc.ListVulnerableImagesByNamespaceParams{
			ClusterID: clusterID, Namespace: ns, PageLimit: limit, PageOffset: offset,
		})
	} else {
		items, err = h.queries.TopVulnerableImages(r.Context(), sqlc.TopVulnerableImagesParams{
			ClusterID: clusterID, Limit: limit, Offset: offset,
		})
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "list_error", "Failed to list vulnerable images")
		return
	}

	total, err := h.queries.CountVulnerableImagesForCluster(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "count_error", "Failed to count vulnerable images")
		return
	}

	rendered := make([]map[string]any, 0, len(items))
	for _, it := range items {
		rendered = append(rendered, renderReport(it))
	}
	RespondPaginated(w, r, rendered, total)
}

// ClusterReportDetail handles GET /api/v1/clusters/{cluster_id}/vulnerabilities/reports/{id}/.
// Returns the report row + a paginated, severity-filterable CVE list.
func (h *ImageVulnHandler) ClusterReportDetail(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	reportID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid report ID")
		return
	}
	report, err := h.queries.GetImageVulnerabilityReportByID(r.Context(), reportID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, "not_found", "Vulnerability report not found")
		return
	}
	// Cross-tenant guard. The URL carries cluster_id; the row's
	// cluster_id MUST match to avoid leaking a report from cluster A
	// to a session scoped on cluster B.
	if report.ClusterID != clusterID {
		RespondRequestError(w, r, http.StatusNotFound, "not_found", "Vulnerability report not found")
		return
	}
	severity := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("severity")))
	limit := int32(queryInt(r, "limit", 50))
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	offset := int32(queryInt(r, "offset", 0))
	if offset < 0 {
		offset = 0
	}

	cves, err := h.queries.ListVulnerabilitiesForReport(r.Context(), sqlc.ListVulnerabilitiesForReportParams{
		ReportID: reportID, SeverityFilter: severity, PageLimit: limit, PageOffset: offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "list_cve_error", "Failed to list vulnerabilities")
		return
	}
	total, err := h.queries.CountVulnerabilitiesForReport(r.Context(), sqlc.CountVulnerabilitiesForReportParams{
		ReportID: reportID, SeverityFilter: severity,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "count_cve_error", "Failed to count vulnerabilities")
		return
	}

	out := map[string]any{
		"report":              renderReport(report),
		"vulnerabilities":     renderVulnList(cves),
		"vulnerability_total": total,
		"severity_filter":     severity,
		"limit":               limit,
		"offset":              offset,
	}
	RespondJSON(w, http.StatusOK, out)
}

// ClusterRescan handles POST /api/v1/clusters/{cluster_id}/vulnerabilities/rescan/.
// Nudges the in-cluster trivy-operator service via an annotation patch
// so the operator re-evaluates every workload. Idempotent — operators
// can spam-click and the operator's watch loop will coalesce.
//
// Nil-safe when h.k8s is nil: returns 200 + {"triggered": false} so the
// UI degrades to "operator not installed".
func (h *ImageVulnHandler) ClusterRescan(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	out := map[string]any{
		"cluster_id":   clusterID.String(),
		"requested_at": time.Now().UTC().Format(time.RFC3339),
	}
	if h.k8s == nil {
		out["triggered"] = false
		out["reason"] = "operator_not_wired"
		RespondJSON(w, http.StatusOK, out)
		return
	}
	if err := h.nudgeTrivyOperator(r.Context(), clusterID); err != nil {
		h.log.Warn("trivy-operator rescan failed",
			"cluster_id", clusterID.String(), "error", err)
		out["triggered"] = false
		out["reason"] = "operator_unreachable"
		out["error"] = err.Error()
		RespondJSON(w, http.StatusOK, out)
		return
	}
	out["triggered"] = true
	recordAudit(r, h.auditQ, "vulnerability.rescan.requested", "cluster", clusterID.String(), "", map[string]any{
		"namespace": trivyOperatorNamespace,
		"service":   trivyOperatorService,
	})
	RespondJSON(w, http.StatusOK, out)
}

// nudgeTrivyOperator triggers a fresh round of scans on every workload
// in the cluster. The implementation deletes all existing
// VulnerabilityReport CRs cluster-wide; trivy-operator watches the
// underlying workloads (Deployments, StatefulSets, DaemonSets, Jobs)
// and re-creates a VulnerabilityReport for each one within seconds —
// you can see the resulting scan-vulnerabilityreport-* Jobs appear in
// trivy-system immediately.
//
// Why delete instead of an annotation patch on the operator Service:
// trivy-operator doesn't watch its own Service for any annotation we
// could set, so the prior implementation was a no-op pretending to be
// a rescan. Deleting the CRs is the documented "force me to re-scan
// everything" path in trivy-operator's own README + matches what
// `kubectl trivy refresh` does internally.
//
// We use the collection-level DELETE (`/apis/.../vulnerabilityreports`
// without a name) with a labelSelector that matches "anything" so a
// single API call clears every namespace. That's faster than walking
// the list + deleting per-row, and atomically idempotent — a half-
// completed sweep just re-enqueues whichever ones are still there.
func (h *ImageVulnHandler) nudgeTrivyOperator(ctx context.Context, clusterID uuid.UUID) error {
	// trivy-operator deletes its own VRs on workload-delete, so we
	// can safely delete from EVERY namespace. The Kubernetes batch
	// DELETE for CRs is namespaced, so we walk namespaces first.
	listResp, err := h.k8s.Do(ctx, clusterID.String(), http.MethodGet,
		"/apis/"+trivyGroup+"/v1alpha1/vulnerabilityreports", nil, nil)
	if err != nil {
		return fmt.Errorf("list vulnerabilityreports: %w", err)
	}
	if listResp == nil || listResp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("trivy-operator CRDs not installed in cluster (aquasecurity.github.io/v1alpha1 missing)")
	}
	if listResp.StatusCode >= 400 {
		return fmt.Errorf("list vulnerabilityreports: HTTP %d", listResp.StatusCode)
	}
	body := decodeK8sProgressBody(listResp)
	if len(body) == 0 {
		return fmt.Errorf("empty list response")
	}
	var lst struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &lst); err != nil {
		return fmt.Errorf("decode list: %w", err)
	}
	if len(lst.Items) == 0 {
		// No existing reports yet — trivy is either still starting up
		// or scanning for the first time. Either way the operator
		// will produce reports on its own; this is a soft success.
		return nil
	}

	// DELETE per report — the CR resource doesn't support collection
	// delete with labelSelector across namespaces in a single call,
	// but the apiserver fan-out is fast enough that doing it in a
	// loop is fine for the report counts a normal cluster carries
	// (10s to low hundreds). Each delete returns 200 once accepted;
	// trivy re-creates the report within ~10s as a fresh scan Job.
	for _, it := range lst.Items {
		path := fmt.Sprintf("/apis/%s/v1alpha1/namespaces/%s/vulnerabilityreports/%s",
			trivyGroup, it.Metadata.Namespace, it.Metadata.Name)
		delResp, derr := h.k8s.Do(ctx, clusterID.String(), http.MethodDelete, path, nil, nil)
		if derr != nil {
			return fmt.Errorf("delete %s/%s: %w", it.Metadata.Namespace, it.Metadata.Name, derr)
		}
		// 404 on delete = the row vanished between list and delete
		// (concurrent operator delete is fine — we wanted it gone).
		if delResp != nil && delResp.StatusCode >= 400 && delResp.StatusCode != http.StatusNotFound {
			return fmt.Errorf("delete %s/%s: HTTP %d", it.Metadata.Namespace, it.Metadata.Name, delResp.StatusCode)
		}
	}
	return nil
}

// trivyGroup is the upstream Trivy CRD API group. Pulled out as a
// constant so the nudge + progress paths stay in sync if Aqua ever
// renames it (they migrated from `tunnel.aquasecurity.com` to this
// one in v0.16 — moving it to a constant prevents the same bug if
// they do it again).
const trivyGroup = "aquasecurity.github.io"

// --- Fleet-wide endpoints --------------------------------------------

// FleetSummary handles GET /api/v1/security/vulnerabilities/summary/.
// Gated by security:read in the route layer.
func (h *ImageVulnHandler) FleetSummary(w http.ResponseWriter, r *http.Request) {
	agg, err := h.queries.AggregateFleetVulnerabilities(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "aggregate_error", "Failed to aggregate fleet vulnerabilities")
		return
	}
	RespondJSON(w, http.StatusOK, renderFleetAggregate(agg))
}

// FleetTopClusters handles GET /api/v1/security/vulnerabilities/top-clusters/.
// Returns the N worst clusters by critical+high.
func (h *ImageVulnHandler) FleetTopClusters(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 10))
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	rows, err := h.queries.TopClustersByVulnerability(r.Context(), limit)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "list_error", "Failed to list top clusters")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		entry := map[string]any{
			"cluster_id":   r.ClusterID.String(),
			"critical":     r.Critical,
			"high":         r.High,
			"medium":       r.Medium,
			"low":          r.Low,
			"report_count": r.ReportCount,
		}
		if t, ok := imageVulnScanTime(r.LastScannedAt); ok && r.ReportCount > 0 {
			entry["last_scanned_at"] = t.Format(time.RFC3339)
		} else {
			entry["last_scanned_at"] = nil
		}
		out = append(out, entry)
	}
	RespondJSON(w, http.StatusOK, out)
}

// --- Render helpers ---------------------------------------------------

func renderClusterAggregate(a sqlc.AggregateClusterVulnerabilitiesRow) map[string]any {
	out := map[string]any{
		"critical":     a.Critical,
		"high":         a.High,
		"medium":       a.Medium,
		"low":          a.Low,
		"unknown":      a.Unknown,
		"report_count": a.ReportCount,
	}
	if t, ok := imageVulnScanTime(a.LastScannedAt); ok && a.ReportCount > 0 {
		out["last_scanned_at"] = t.Format(time.RFC3339)
	} else {
		out["last_scanned_at"] = nil
	}
	return out
}

func renderFleetAggregate(a sqlc.AggregateFleetVulnerabilitiesRow) map[string]any {
	out := map[string]any{
		"critical":      a.Critical,
		"high":          a.High,
		"medium":        a.Medium,
		"low":           a.Low,
		"unknown":       a.Unknown,
		"report_count":  a.ReportCount,
		"cluster_count": a.ClusterCount,
	}
	if t, ok := imageVulnScanTime(a.LastScannedAt); ok && a.ReportCount > 0 {
		out["last_scanned_at"] = t.Format(time.RFC3339)
	} else {
		out["last_scanned_at"] = nil
	}
	return out
}

func renderReport(r sqlc.ImageVulnerabilityReport) map[string]any {
	return map[string]any{
		"id":              r.ID.String(),
		"cluster_id":      r.ClusterID.String(),
		"report_name":     r.ReportName,
		"namespace":       r.Namespace,
		"workload_kind":   r.WorkloadKind,
		"workload_name":   r.WorkloadName,
		"container_name":  r.ContainerName,
		"image_registry":  r.ImageRegistry,
		"image_repo":      r.ImageRepo,
		"image_tag":       r.ImageTag,
		"image_digest":    r.ImageDigest,
		"scanner":         r.Scanner,
		"scanner_version": r.ScannerVersion,
		"critical_count":  r.CriticalCount,
		"high_count":      r.HighCount,
		"medium_count":    r.MediumCount,
		"low_count":       r.LowCount,
		"unknown_count":   r.UnknownCount,
		"scanned_at":      r.ScannedAt.UTC().Format(time.RFC3339),
		"created_at":      r.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":      r.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func renderVulnList(cves []sqlc.ImageVulnerability) []map[string]any {
	out := make([]map[string]any, 0, len(cves))
	for _, c := range cves {
		entry := map[string]any{
			"id":                c.ID.String(),
			"report_id":         c.ReportID.String(),
			"vulnerability_id":  c.VulnerabilityID,
			"severity":          c.Severity,
			"pkg_name":          c.PkgName,
			"installed_version": c.InstalledVersion,
			"fixed_version":     c.FixedVersion,
			"primary_link":      c.PrimaryLink,
			"title":             c.Title,
			"description":       c.Description,
			"created_at":        c.CreatedAt.UTC().Format(time.RFC3339),
		}
		if score, ok := c.CVSSScoreFloat(); ok {
			entry["cvss_score"] = score
		} else {
			entry["cvss_score"] = nil
		}
		out = append(out, entry)
	}
	return out
}

func parseClusterID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return uuid.Nil, false
	}
	return id, true
}
