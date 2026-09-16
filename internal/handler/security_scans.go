package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// securityScanScopeQuerier is the production query surface required by
// cluster-scoped scan reads. It is deliberately separate from SecurityQuerier
// so small unit fakes that never serve these endpoints do not need unrelated
// methods. The handler fails closed when the scoped query is unavailable.
type securityScanScopeQuerier interface {
	GetSecurityScanResultByClusterAndID(ctx context.Context, arg sqlc.GetSecurityScanResultByClusterAndIDParams) (sqlc.SecurityScanResult, error)
	CountSecurityScanResultsByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
}

// securityEstateScanScopeQuerier applies cluster visibility before the page
// boundary for fleet security lists and object reads. A global security grant
// admits the route; it does not silently grant visibility into every cluster.
type securityEstateScanScopeQuerier interface {
	GetActiveSecurityScanResultByID(context.Context, uuid.UUID) (sqlc.SecurityScanResult, error)
	GetActiveSecurityScanResultByIDForScopes(context.Context, sqlc.GetActiveSecurityScanResultByIDForScopesParams) (sqlc.SecurityScanResult, error)
	ListSecurityScanResultsForScopes(context.Context, sqlc.ListSecurityScanResultsForScopesParams) ([]sqlc.SecurityScanResult, error)
	CountSecurityScanResultsForScopes(context.Context, []uuid.UUID) (int64, error)
}

// securityScanLifecycleQuerier is intentionally mandatory for scan mutations.
// Its create query commits the product row and first task-outbox intent in one
// PostgreSQL statement; cancellation and failure use terminal-state CAS updates.
type securityScanLifecycleQuerier interface {
	CreateCISScanWithOutbox(ctx context.Context, arg sqlc.CreateCISScanWithOutboxParams) (sqlc.CreateCISScanWithOutboxRow, error)
	FailSecurityScanPoll(ctx context.Context, arg sqlc.FailSecurityScanPollParams) (int64, error)
}

// publishCISScanChanged emits the metadata-only cis_scan.changed event after
// a successful scan-row write. P4.9: the same security_scan_results row also
// feeds the generic scans list (GET /security/scans/), so every write emits
// security_scan.changed alongside the CIS-specific type.
func (h *SecurityHandler) publishCISScanChanged(clusterID, scanID uuid.UUID) {
	if h == nil {
		return
	}
	events.PublishChanged(h.bus, "cis_scan", clusterID.String(), scanID.String(), nil)
	events.PublishChanged(h.bus, "security_scan", clusterID.String(), scanID.String(), nil)
}

// ListScans handles GET /api/v1/clusters/{cluster_id}/security/scans/.
func (h *SecurityHandler) ListScans(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}

	limit := int32(queryLimit(r, 20))
	offset := int32(queryOffset(r))

	scans, err := h.queries.ListScansByCluster(r.Context(), sqlc.ListScansByClusterParams{
		ClusterID: clusterID,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list security scans")
		return
	}

	scoped, ok := h.queries.(securityScanScopeQuerier)
	if !ok {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Security scan authorization is not configured")
		return
	}
	total, err := scoped.CountSecurityScanResultsByCluster(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count security scans")
		return
	}

	paging.Write(w, scans, paging.Exact(total, queryLimit(r, 20), queryOffset(r), len(scans)))
}

// GetScan handles GET /api/v1/clusters/{cluster_id}/security/scans/{id}/.
func (h *SecurityHandler) GetScan(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid scan ID")
		return
	}

	scoped, ok := h.queries.(securityScanScopeQuerier)
	if !ok {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Security scan authorization is not configured")
		return
	}
	scan, err := scoped.GetSecurityScanResultByClusterAndID(r.Context(), sqlc.GetSecurityScanResultByClusterAndIDParams{
		ClusterID: clusterID,
		ID:        id,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Security scan not found")
		return
	}

	RespondJSON(w, http.StatusOK, scan)
}

// CancelScan handles POST /api/v1/clusters/{cluster_id}/security/scans/{id}/cancel/.
// The cluster-qualified lookup and update prevent cross-tenant object access;
// the terminal-state CAS makes cancellation win or lose exactly once against a
// concurrently completing ingestion worker.
func (h *SecurityHandler) CancelScan(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid scan ID")
		return
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "security transaction runner is not configured")
		return
	}
	var scan sqlc.SecurityScanResult
	err = h.runTx(r.Context(), func(q SecurityMutationTx) error {
		var cancelErr error
		scan, cancelErr = q.CancelSecurityScan(r.Context(), sqlc.CancelSecurityScanParams{ClusterID: clusterID, ID: id})
		if cancelErr != nil {
			return cancelErr
		}
		return recordSecurityAuditOutbox(r, q, "security.scan.cancel", "security_scan", scan.ID.String(), scan.ClusterScanName, http.StatusOK, map[string]any{
			"cluster_id": clusterID.String(),
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		scoped, scopedOK := h.queries.(securityScanScopeQuerier)
		if !scopedOK {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Security scan authorization is not configured")
			return
		}
		existing, getErr := scoped.GetSecurityScanResultByClusterAndID(r.Context(), sqlc.GetSecurityScanResultByClusterAndIDParams{ClusterID: clusterID, ID: id})
		if errors.Is(getErr, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Security scan not found")
			return
		}
		if getErr != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.StatusError, "Failed to load security scan")
			return
		}
		if existing.Status == "cancelled" {
			RespondJSON(w, http.StatusOK, scanWithFindings(existing))
			return
		}
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Security scan is already terminal")
		return
	}
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.StatusError, "Failed to cancel security scan")
		return
	}
	h.publishCISScanChanged(clusterID, scan.ID)
	RespondJSON(w, http.StatusOK, scanWithFindings(scan))
}

// ListAllScans handles GET /api/v1/security/scans/.
func (h *SecurityHandler) ListAllScans(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 20))
	offset := int32(queryOffset(r))
	all, clusterIDs, err := h.securityScanVisibility(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.Forbidden, "Failed to retrieve user permissions")
		return
	}
	var scans []sqlc.SecurityScanResult
	var total int64
	if all {
		scans, err = h.queries.ListSecurityScanResults(r.Context(), sqlc.ListSecurityScanResultsParams{Limit: limit, Offset: offset})
		if err == nil {
			total, err = h.queries.CountSecurityScanResults(r.Context())
		}
	} else {
		scoped, ok := h.queries.(securityEstateScanScopeQuerier)
		if !ok {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Scoped security scan pagination is unavailable")
			return
		}
		scans, err = scoped.ListSecurityScanResultsForScopes(r.Context(), sqlc.ListSecurityScanResultsForScopesParams{
			ClusterIds: clusterIDs, QueryLimit: limit, QueryOffset: offset,
		})
		if err == nil {
			total, err = scoped.CountSecurityScanResultsForScopes(r.Context(), clusterIDs)
		}
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list security scans")
		return
	}
	paging.Write(w, scans, paging.Exact(total, queryLimit(r, 20), queryOffset(r), len(scans)))
}

func (h *SecurityHandler) securityScanVisibility(ctx context.Context) (bool, []uuid.UUID, error) {
	all, clusterIDs, _, err := h.authz.authorizedScopeIDs(ctx, rbac.ResourceClusters, rbac.VerbRead, rbac.NarrowedClustersWiden)
	return all, clusterIDs, err
}

func (h *SecurityHandler) loadVisibleEstateScan(ctx context.Context, id uuid.UUID) (sqlc.SecurityScanResult, error) {
	all, clusterIDs, err := h.securityScanVisibility(ctx)
	if err != nil {
		return sqlc.SecurityScanResult{}, err
	}
	scoped, ok := h.queries.(securityEstateScanScopeQuerier)
	if all {
		if ok {
			return scoped.GetActiveSecurityScanResultByID(ctx, id)
		}
		// Small direct-handler fakes predate the active-cluster read surface.
		// Production's generated querier always takes the branch above.
		return h.queries.GetSecurityScanResultByID(ctx, id)
	}
	if !ok {
		return sqlc.SecurityScanResult{}, errAuthorizationNotConfigured
	}
	return scoped.GetActiveSecurityScanResultByIDForScopes(ctx, sqlc.GetActiveSecurityScanResultByIDForScopesParams{
		ID: id, ClusterIds: clusterIDs,
	})
}

// CISScanCreateRequest starts one adopted-cluster CIS assessment.
// openapi:request CISScanCreateRequest
type CISScanCreateRequest struct {
	ClusterID uuid.UUID `json:"cluster_id"`
	Profile   string    `json:"profile"`
}

// CreateScan handles POST /api/v1/security/scans/.
//
// Phase B5: instead of just inserting a row, this now:
//  1. resolves the cluster to default the CIS profile from cluster.distribution,
//  2. atomically records the scan row and durable tunnel-queue intent,
//  3. POSTs a ClusterScan CR through the tunnel-backed K8s requester, and
//  4. leaves all report polling to the lease-owned worker lifecycle.
func (h *SecurityHandler) CreateScan(w http.ResponseWriter, r *http.Request) {
	var req CISScanCreateRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if req.ClusterID == uuid.Nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "cluster_id is required")
		return
	}
	if h.k8s == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StatusError, "CIS scan tunnel runtime is unavailable")
		return
	}
	lifecycle, ok := h.queries.(securityScanLifecycleQuerier)
	if !ok {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StatusError, "CIS scan lifecycle is not configured")
		return
	}

	profile := strings.TrimSpace(req.Profile)
	explicitProfile := profile != ""

	clusterDistribution := ""
	// Resolve the cluster so we can default the profile when the caller didn't
	// supply one. Distribution → profile map matches the cis-operator preset
	// names so we don't have to install custom ClusterScanProfiles.
	if profile == "" && h.clusters != nil {
		cluster, err := h.clusters.GetClusterByID(r.Context(), req.ClusterID)
		if err == nil {
			clusterDistribution = cluster.Distribution
			profile = defaultCISProfileForDistribution(cluster.Distribution)
		}
	}
	if profile == "" {
		profile = "cis-1.8"
	}
	resolveInput := profile
	if !explicitProfile {
		resolveInput = ""
	}
	if resolved := h.resolveClusterScanProfileName(r.Context(), req.ClusterID, clusterDistribution, resolveInput); strings.TrimSpace(resolved) != "" {
		profile = resolved
	}

	scanName := fmt.Sprintf("astronomer-cis-%d-%s", time.Now().UTC().Unix(), uuid.NewString()[:8])
	auditRequestID := reqctx.RequestID(r.Context())
	if auditRequestID == "" {
		auditRequestID = uuid.NewString()
	}
	auditDetail, _ := json.Marshal(audit.SanitizeDetail(map[string]any{
		"cluster_id": req.ClusterID.String(), "profile": profile,
		"phase": "scan_row_and_task_committed",
	}))
	created, err := lifecycle.CreateCISScanWithOutbox(r.Context(), sqlc.CreateCISScanWithOutboxParams{
		ClusterID:            req.ClusterID,
		ScanType:             profile,
		ClusterScanName:      scanName,
		InitiatedByID:        currentUserUUID(r),
		AuditID:              uuid.New(),
		AuditDedupeKey:       audit.MutationDedupeKey(auditRequestID, "security.scan.create", "cluster", req.ClusterID.String()),
		AuditActorAuthMethod: authMethodFromRequest(r),
		AuditHttpMethod:      r.Method,
		AuditPath:            r.URL.Path,
		AuditRequestID:       auditRequestID,
		AuditIpAddress:       reqctx.ClientIP(r),
		AuditUserAgent:       r.UserAgent(),
		AuditDetail:          auditDetail,
		AuditCorrelationID:   reqctx.CorrelationID(r.Context()),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create security scan")
		return
	}
	scan := securityScanResultFromCreate(created)

	if err := h.createClusterScanCR(r.Context(), req.ClusterID, scanName, profile); err != nil {
		h.log.Warn("create ClusterScan CR failed", "cluster_id", req.ClusterID.String(), "scan_id", scan.ID.String(), "error", err)
		_, failErr := lifecycle.FailSecurityScanPoll(r.Context(), sqlc.FailSecurityScanPollParams{
			Reason: "ClusterScan creation failed: " + err.Error(), ID: scan.ID,
			Generation: scan.PollGeneration, Owner: "",
		})
		if failErr != nil {
			h.log.Error("record ClusterScan creation failure", "scan_id", scan.ID.String(), "error", failErr)
		}
		h.publishCISScanChanged(req.ClusterID, scan.ID)
		RespondRequestError(w, r, http.StatusBadGateway, apierror.CRCreateError, err.Error())
		return
	}

	h.publishCISScanChanged(req.ClusterID, scan.ID)
	w.Header().Set("Location", "/api/v1/security/scans/"+scan.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, scanWithFindings(scan))
}

// createClusterScanCR posts a `ClusterScan` CR into the agent's
// `cis-operator-system` namespace. Unstructured JSON is used so we don't
// have to ship the cis-operator CRD types.
func (h *SecurityHandler) createClusterScanCR(ctx context.Context, clusterID uuid.UUID, scanName, profile string) error {
	body, err := json.Marshal(map[string]any{
		"apiVersion": "cis.cattle.io/v1",
		"kind":       "ClusterScan",
		"metadata": map[string]any{
			"name": scanName,
			"labels": map[string]string{
				"app.kubernetes.io/managed-by": "astronomer-go",
			},
		},
		"spec": map[string]any{
			"scanProfileName": profile,
		},
	})
	if err != nil {
		return err
	}
	resp, err := h.k8s.Do(ctx, clusterID.String(), http.MethodPost,
		"/apis/cis.cattle.io/v1/clusterscans",
		body, requestHeaders("application/json"))
	if err != nil {
		return err
	}
	return ensureSuccess(resp)
}

func securityScanResultFromCreate(row sqlc.CreateCISScanWithOutboxRow) sqlc.SecurityScanResult {
	return sqlc.SecurityScanResult(row)
}
