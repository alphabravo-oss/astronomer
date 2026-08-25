// Package handler — image vulnerability scan endpoints (sprint 062).

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

// trivyOperatorNamespace is the recommended install namespace from the
// Aqua chart. The handler PATCHes its operator service's annotations to
// nudge a re-scan.
const trivyOperatorNamespace = "astronomer-trivy-system"

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

type ImageVulnMutationTx interface {
	GetClusterByID(context.Context, uuid.UUID) (sqlc.Cluster, error)
	CreateWorkloadOperation(context.Context, sqlc.CreateWorkloadOperationParams) (sqlc.WorkloadOperation, error)
	CreateWorkloadOperationIdempotent(context.Context, sqlc.CreateWorkloadOperationIdempotentParams) (sqlc.WorkloadOperation, error)
	audit.OutboxQuerier
	tasks.TaskOutboxWriter
}

type imageVulnRunTxFunc func(context.Context, func(ImageVulnMutationTx) error) error

// ImageVulnHandler owns /api/v1/clusters/{cluster_id}/vulnerabilities/*
// and /api/v1/security/vulnerabilities/*. The K8sRequester is optional
// — when nil the rescan path short-circuits, every read path still
// works against the local DB.
type ImageVulnHandler struct {
	queries ImageVulnQuerier
	k8s     K8sRequester
	runTx   imageVulnRunTxFunc
}

// NewImageVulnHandler constructs the handler. log defaults to slog's
// process default if nil.
func NewImageVulnHandler(q ImageVulnQuerier) *ImageVulnHandler {
	return &ImageVulnHandler{queries: q}
}

// SetK8sRequester wires the tunnel-backed Kubernetes API client used
// by read-only scan-progress probes.
func (h *ImageVulnHandler) SetK8sRequester(req K8sRequester) {
	if h != nil {
		h.k8s = req
	}
}

func (h *ImageVulnHandler) SetRunTx(runTx imageVulnRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *ImageVulnHandler) TransactionalRescanWired() bool {
	return h != nil && h.runTx != nil
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
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.AggregateError, "Failed to aggregate cluster vulnerabilities")
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
	limit := int32(queryLimit(r, 20))
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
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list vulnerable images")
		return
	}

	total, err := h.queries.CountVulnerableImagesForCluster(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count vulnerable images")
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid report ID")
		return
	}
	report, err := h.queries.GetImageVulnerabilityReportByID(r.Context(), reportID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Vulnerability report not found")
		return
	}
	// Cross-tenant guard. The URL carries cluster_id; the row's
	// cluster_id MUST match to avoid leaking a report from cluster A
	// to a session scoped on cluster B.
	if report.ClusterID != clusterID {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Vulnerability report not found")
		return
	}
	severity := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("severity")))
	limit := int32(queryLimit(r, 50))
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
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListCVEError, "Failed to list vulnerabilities")
		return
	}
	total, err := h.queries.CountVulnerabilitiesForReport(r.Context(), sqlc.CountVulnerabilitiesForReportParams{
		ReportID: reportID, SeverityFilter: severity,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountCVEError, "Failed to count vulnerabilities")
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

// ClusterRescan persists a durable rescan operation. It never contacts the
// member cluster in the request path: operation state, identifier-only task
// intent, and mandatory audit are committed in one transaction before a 202
// receipt is returned.
func (h *ImageVulnHandler) ClusterRescan(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	if values := r.Header.Values("Idempotency-Key"); len(values) > 1 || (len(values) == 1 && !validOperationIdempotencyKey(values[0])) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest,
			"Idempotency-Key must occur at most once and contain 1 through 128 printable UTF-8 bytes")
		return
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable,
			"Durable vulnerability rescan storage is unavailable")
		return
	}

	var operation sqlc.WorkloadOperation
	opContext := withOperationIdempotency(r, "image-vulnerability-rescans")
	err := h.runTx(r.Context(), func(q ImageVulnMutationTx) error {
		cluster, err := q.GetClusterByID(r.Context(), clusterID)
		if err != nil {
			return err
		}
		// Keep the generic operation receipt readable through the existing
		// /workload-operations/{id} API, whose authorization resolver consumes
		// the canonical clusterId envelope.
		payload, err := json.Marshal(map[string]string{"clusterId": clusterID.String()})
		if err != nil {
			return err
		}
		params := sqlc.CreateWorkloadOperationParams{
			TargetType: "cluster", TargetKey: clusterID.String(), OperationType: "vulnerability_rescan",
			Payload: payload, Status: "pending", CreatedByID: currentUserUUID(r),
		}
		if idem, present := operationIdempotencyFromContext(opContext); present {
			operation, err = q.CreateWorkloadOperationIdempotent(r.Context(), sqlc.CreateWorkloadOperationIdempotentParams{
				Scope: idem.scope, IdempotencyKey: idem.key, TargetType: params.TargetType,
				TargetKey: params.TargetKey, OperationType: params.OperationType, Payload: params.Payload,
				Status: params.Status, CreatedByID: params.CreatedByID,
			})
			if err == nil && operation.ID != uuid.Nil && (operation.TargetType != params.TargetType || operation.TargetKey != params.TargetKey || operation.OperationType != params.OperationType || !bytes.Equal(operation.Payload, params.Payload)) {
				return errWorkloadOperationIdempotencyConflict
			}
		} else {
			operation, err = q.CreateWorkloadOperation(r.Context(), params)
		}
		if err != nil {
			return err
		}
		task, err := tasks.NewImageVulnerabilityRescanTask(operation.ID)
		if err != nil {
			return err
		}
		taskPayload := observability.EnrichTaskPayload(r.Context(), task.Payload(), middleware.GetCorrelationID(r.Context()))
		task = asynq.NewTask(task.Type(), taskPayload, asynq.MaxRetry(5), asynq.Timeout(2*time.Minute))
		if _, err := tasks.EnqueueTaskOutbox(r.Context(), q, task, tasks.TaskOutboxOptions{
			DedupeKey: "vulnerability:rescan:" + operation.ID.String(), QueueName: tasks.ClusterTemplateApplyQueueName,
			MaxRetry: 5, Timeout: 2 * time.Minute, MaxDeliveryAttempts: 20,
		}); err != nil {
			return err
		}
		return recordAuditOutbox(r, q, "vulnerability.rescan.requested", "vulnerability_rescan", operation.ID.String(), cluster.Name,
			http.StatusAccepted, map[string]any{"cluster_id": clusterID.String(), "operation_id": operation.ID.String()})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	if errors.Is(err, errWorkloadOperationIdempotencyConflict) {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict,
			"Idempotency-Key already identifies a different vulnerability rescan")
		return
	}
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError,
			"Failed to create vulnerability rescan operation")
		return
	}
	operationURL := "/api/v1/workloads/operations/" + operation.ID.String() + "/"
	w.Header().Set("Location", operationURL)
	w.Header().Set("Retry-After", "2")
	RespondJSON(w, http.StatusAccepted, map[string]any{
		"operation_id": operation.ID.String(), "cluster_id": clusterID.String(), "status": operation.Status,
		"requested_at":  operation.CreatedAt.UTC().Format(time.RFC3339),
		"operation_url": operationURL,
	})
}

// --- Estate-wide endpoints --------------------------------------------

// FleetSummary handles GET /api/v1/security/vulnerabilities/summary/.
// Gated by security:read in the route layer.
func (h *ImageVulnHandler) FleetSummary(w http.ResponseWriter, r *http.Request) {
	agg, err := h.queries.AggregateFleetVulnerabilities(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.AggregateError, "Failed to aggregate fleet vulnerabilities")
		return
	}
	RespondJSON(w, http.StatusOK, renderFleetAggregate(agg))
}

// FleetTopClusters handles GET /api/v1/security/vulnerabilities/top-clusters/.
// Returns the N worst clusters by critical+high.
func (h *ImageVulnHandler) FleetTopClusters(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 10))
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	rows, err := h.queries.TopClustersByVulnerability(r.Context(), limit)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list top clusters")
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return uuid.Nil, false
	}
	return id, true
}
