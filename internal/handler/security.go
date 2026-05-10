package handler

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// cisOperatorNamespace is the namespace cis-operator installs into and reads
// its CRDs from. See migration 022 for the matching tool catalog entry.
const cisOperatorNamespace = "cis-operator-system"

// SecurityIngestEnqueuer is the slice of asynq.Client we actually need. Defined
// as an interface so tests can stub it without spinning up Redis. We keep
// the interface even though the in-process poller is the primary ingestion
// path — leaving it as an extension point if a future deployment topology
// runs ingestion in the worker process via a side-channel HTTP call.
type SecurityIngestEnqueuer interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

// SecurityIngestPersister is the slice of the DB querier the in-process
// poller needs. Decoupled from SecurityQuerier so the poller can run with
// only the methods it actually touches.
type SecurityIngestPersister interface {
	UpdateSecurityScanReport(ctx context.Context, arg sqlc.UpdateSecurityScanReportParams) error
	UpdateSecurityScanFailedWithMessage(ctx context.Context, arg sqlc.UpdateSecurityScanFailedWithMessageParams) error
}

// SecurityClusterQuerier exposes the single cluster lookup we need to default
// the CIS profile from cluster.distribution. Kept as its own interface so
// SecurityQuerier doesn't have to grow a Cluster dependency.
type SecurityClusterQuerier interface {
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
}

// SecurityQuerier abstracts the security-related database queries needed by SecurityHandler.
type SecurityQuerier interface {
	// Templates
	GetPodSecurityTemplateByID(ctx context.Context, id uuid.UUID) (sqlc.PodSecurityTemplate, error)
	ListPodSecurityTemplates(ctx context.Context, arg sqlc.ListPodSecurityTemplatesParams) ([]sqlc.PodSecurityTemplate, error)
	CreatePodSecurityTemplate(ctx context.Context, arg sqlc.CreatePodSecurityTemplateParams) (sqlc.PodSecurityTemplate, error)
	UpdatePodSecurityTemplate(ctx context.Context, arg sqlc.UpdatePodSecurityTemplateParams) (sqlc.PodSecurityTemplate, error)
	DeletePodSecurityTemplate(ctx context.Context, id uuid.UUID) error
	CountPodSecurityTemplates(ctx context.Context) (int64, error)
	// Policies
	GetClusterSecurityPolicyByID(ctx context.Context, id uuid.UUID) (sqlc.ClusterSecurityPolicy, error)
	GetPolicyByCluster(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterSecurityPolicy, error)
	ListClusterSecurityPolicies(ctx context.Context, arg sqlc.ListClusterSecurityPoliciesParams) ([]sqlc.ClusterSecurityPolicy, error)
	CreateClusterSecurityPolicy(ctx context.Context, arg sqlc.CreateClusterSecurityPolicyParams) (sqlc.ClusterSecurityPolicy, error)
	DeleteClusterSecurityPolicy(ctx context.Context, id uuid.UUID) error
	UpdateClusterSecurityPolicyApplied(ctx context.Context, id uuid.UUID) error
	CountClusterSecurityPolicies(ctx context.Context) (int64, error)
	// Scans
	ListSecurityScanResults(ctx context.Context, arg sqlc.ListSecurityScanResultsParams) ([]sqlc.SecurityScanResult, error)
	ListScansByCluster(ctx context.Context, arg sqlc.ListScansByClusterParams) ([]sqlc.SecurityScanResult, error)
	GetSecurityScanResultByID(ctx context.Context, id uuid.UUID) (sqlc.SecurityScanResult, error)
	CreateSecurityScanResult(ctx context.Context, arg sqlc.CreateSecurityScanResultParams) (sqlc.SecurityScanResult, error)
	CreateCISScan(ctx context.Context, arg sqlc.CreateCISScanParams) (sqlc.SecurityScanResult, error)
	CountSecurityScanResults(ctx context.Context) (int64, error)
}

// SecurityHandler handles security endpoints.
type SecurityHandler struct {
	queries   SecurityQuerier
	persister SecurityIngestPersister
	clusters  SecurityClusterQuerier
	k8s       K8sRequester
	queue     SecurityIngestEnqueuer
	log       *slog.Logger
}

// NewSecurityHandler creates a new security handler.
func NewSecurityHandler(queries SecurityQuerier) *SecurityHandler {
	return &SecurityHandler{queries: queries, log: slog.Default()}
}

// SetK8sRequester wires the tunnel-backed Kubernetes API client. Optional:
// when nil the CIS scan trigger short-circuits with a 503 and tests can run
// the rest of the handlers without standing up a tunnel hub.
func (h *SecurityHandler) SetK8sRequester(req K8sRequester) {
	if h != nil {
		h.k8s = req
	}
}

// SetClusterQuerier supplies the GetClusterByID dependency used to default
// the CIS profile from cluster.distribution.
func (h *SecurityHandler) SetClusterQuerier(q SecurityClusterQuerier) {
	if h != nil {
		h.clusters = q
	}
}

// SetIngestQueue wires the asynq client used to schedule the report-ingest
// follow-up. Optional: nil means the row is created but no async polling fires.
func (h *SecurityHandler) SetIngestQueue(q SecurityIngestEnqueuer) {
	if h != nil {
		h.queue = q
	}
}

// SetIngestPersister wires the DB methods the in-process poller needs.
// Typically this is the same `*sqlc.Queries` already in use elsewhere.
func (h *SecurityHandler) SetIngestPersister(p SecurityIngestPersister) {
	if h != nil {
		h.persister = p
	}
}

// SetLogger sets the structured logger used for handler-side diagnostics.
func (h *SecurityHandler) SetLogger(log *slog.Logger) {
	if h != nil && log != nil {
		h.log = log
	}
}

// ControllerStatus summarizes security policy and scan state.
func (h *SecurityHandler) ControllerStatus(w http.ResponseWriter, r *http.Request) {
	summary, err := h.controllerSummary(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "status_error", "Failed to load security templates")
		return
	}
	RespondJSON(w, http.StatusOK, summary)
}

func (h *SecurityHandler) controllerSummary(ctx context.Context) (map[string]any, error) {
	templates, err := h.queries.ListPodSecurityTemplates(ctx, sqlc.ListPodSecurityTemplatesParams{Limit: 1000, Offset: 0})
	if err != nil {
		return nil, err
	}
	policies, err := h.queries.ListClusterSecurityPolicies(ctx, sqlc.ListClusterSecurityPoliciesParams{Limit: 1000, Offset: 0})
	if err != nil {
		return nil, err
	}
	scans, err := h.queries.ListSecurityScanResults(ctx, sqlc.ListSecurityScanResultsParams{Limit: 1000, Offset: 0})
	if err != nil {
		return nil, err
	}
	policyStatuses := map[string]int{}
	scanStatuses := map[string]int{}
	failedPolicies := 0
	runningScans := 0
	failedScans := 0
	for _, policy := range policies {
		policyStatuses[policy.SyncStatus]++
		if policy.SyncStatus == "failed" || policy.ErrorMessage != "" {
			failedPolicies++
		}
	}
	for _, scan := range scans {
		scanStatuses[scan.Status]++
		switch scan.Status {
		case "pending", "running", "in_progress":
			runningScans++
		case "failed", "error":
			failedScans++
		}
	}
	health := "healthy"
	reasons := make([]string, 0, 2)
	if failedPolicies > 0 {
		health = "degraded"
		reasons = append(reasons, "failed_policy_syncs_present")
	}
	if failedScans > 0 {
		health = "degraded"
		reasons = append(reasons, "failed_scans_present")
	}
	return map[string]any{
		"reconciler": map[string]any{
			"enabled": false,
		},
		"health":        health,
		"healthReasons": reasons,
		"templates": map[string]any{
			"total": len(templates),
		},
		"policies": map[string]any{
			"total":       len(policies),
			"failedCount": failedPolicies,
			"statuses":    policyStatuses,
		},
		"scans": map[string]any{
			"total":        len(scans),
			"runningCount": runningScans,
			"failedCount":  failedScans,
			"statuses":     scanStatuses,
		},
	}, nil
}

// --- Request types ---

// CreateTemplateRequest represents the request body for creating a pod security template.
type CreateTemplateRequest struct {
	Name                 string          `json:"name"`
	Description          string          `json:"description"`
	IsDefault            bool            `json:"is_default"`
	EnforceLevel         string          `json:"enforce_level"`
	EnforceVersion       string          `json:"enforce_version"`
	AuditLevel           string          `json:"audit_level"`
	AuditVersion         string          `json:"audit_version"`
	WarnLevel            string          `json:"warn_level"`
	WarnVersion          string          `json:"warn_version"`
	ExemptUsernames      json.RawMessage `json:"exempt_usernames"`
	ExemptRuntimeClasses json.RawMessage `json:"exempt_runtime_classes"`
	ExemptNamespaces     json.RawMessage `json:"exempt_namespaces"`
}

// --- Endpoints ---

// ListTemplates handles GET /api/v1/security/templates/.
func (h *SecurityHandler) ListTemplates(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	templates, err := h.queries.ListPodSecurityTemplates(r.Context(), sqlc.ListPodSecurityTemplatesParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list security templates")
		return
	}

	total, err := h.queries.CountPodSecurityTemplates(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count security templates")
		return
	}

	RespondPaginated(w, r, templates, total)
}

// CreateTemplate handles POST /api/v1/security/templates/.
func (h *SecurityHandler) CreateTemplate(w http.ResponseWriter, r *http.Request) {
	var req CreateTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	if req.Name == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Template name is required")
		return
	}

	exemptUsernames := req.ExemptUsernames
	if exemptUsernames == nil {
		exemptUsernames = json.RawMessage(`[]`)
	}
	exemptRuntimeClasses := req.ExemptRuntimeClasses
	if exemptRuntimeClasses == nil {
		exemptRuntimeClasses = json.RawMessage(`[]`)
	}
	exemptNamespaces := req.ExemptNamespaces
	if exemptNamespaces == nil {
		exemptNamespaces = json.RawMessage(`[]`)
	}

	template, err := h.queries.CreatePodSecurityTemplate(r.Context(), sqlc.CreatePodSecurityTemplateParams{
		Name:                 req.Name,
		Description:          req.Description,
		IsDefault:            req.IsDefault,
		EnforceLevel:         req.EnforceLevel,
		EnforceVersion:       req.EnforceVersion,
		AuditLevel:           req.AuditLevel,
		AuditVersion:         req.AuditVersion,
		WarnLevel:            req.WarnLevel,
		WarnVersion:          req.WarnVersion,
		ExemptUsernames:      exemptUsernames,
		ExemptRuntimeClasses: exemptRuntimeClasses,
		ExemptNamespaces:     exemptNamespaces,
		CreatedByID:          currentUserUUID(r),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create security template")
		return
	}

	recordAudit(r, h.queries, "security.template.create", "pod_security_template", template.ID.String(), template.Name, map[string]any{
		"enforce_level": template.EnforceLevel,
		"audit_level":   template.AuditLevel,
		"warn_level":    template.WarnLevel,
		"is_default":    template.IsDefault,
	})

	RespondJSON(w, http.StatusCreated, template)
}

// GetTemplate handles GET /api/v1/security/templates/{id}/.
func (h *SecurityHandler) GetTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid template ID")
		return
	}

	template, err := h.queries.GetPodSecurityTemplateByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Security template not found")
		return
	}

	RespondJSON(w, http.StatusOK, template)
}

// DeleteTemplate handles DELETE /api/v1/security/templates/{id}/.
func (h *SecurityHandler) DeleteTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid template ID")
		return
	}

	templateName := ""
	if existing, lookupErr := h.queries.GetPodSecurityTemplateByID(r.Context(), id); lookupErr == nil {
		templateName = existing.Name
	}
	if err := h.queries.DeletePodSecurityTemplate(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Security template not found")
		return
	}
	recordAudit(r, h.queries, "security.template.delete", "pod_security_template", id.String(), templateName, nil)

	w.WriteHeader(http.StatusNoContent)
}

// UpdateTemplate handles PUT /api/v1/security/templates/{id}/.
func (h *SecurityHandler) UpdateTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid template ID")
		return
	}
	var req CreateTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	template, err := h.queries.UpdatePodSecurityTemplate(r.Context(), sqlc.UpdatePodSecurityTemplateParams{
		ID:                   id,
		Name:                 req.Name,
		Description:          req.Description,
		IsDefault:            req.IsDefault,
		EnforceLevel:         req.EnforceLevel,
		EnforceVersion:       req.EnforceVersion,
		AuditLevel:           req.AuditLevel,
		AuditVersion:         req.AuditVersion,
		WarnLevel:            req.WarnLevel,
		WarnVersion:          req.WarnVersion,
		ExemptUsernames:      req.ExemptUsernames,
		ExemptRuntimeClasses: req.ExemptRuntimeClasses,
		ExemptNamespaces:     req.ExemptNamespaces,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "update_error", "Failed to update security template")
		return
	}
	recordAudit(r, h.queries, "security.template.update", "pod_security_template", template.ID.String(), template.Name, map[string]any{
		"enforce_level": template.EnforceLevel,
		"is_default":    template.IsDefault,
	})
	RespondJSON(w, http.StatusOK, template)
}

// GetPolicy handles GET /api/v1/clusters/{cluster_id}/security/policy/.
func (h *SecurityHandler) GetPolicy(w http.ResponseWriter, r *http.Request) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}

	policy, err := h.queries.GetPolicyByCluster(r.Context(), clusterID)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Security policy not found for cluster")
		return
	}

	RespondJSON(w, http.StatusOK, policy)
}

// ListScans handles GET /api/v1/clusters/{cluster_id}/security/scans/.
func (h *SecurityHandler) ListScans(w http.ResponseWriter, r *http.Request) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}

	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	scans, err := h.queries.ListScansByCluster(r.Context(), sqlc.ListScansByClusterParams{
		ClusterID: clusterID,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list security scans")
		return
	}

	total, err := h.queries.CountSecurityScanResults(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count security scans")
		return
	}

	RespondPaginated(w, r, scans, total)
}

// GetScan handles GET /api/v1/clusters/{cluster_id}/security/scans/{id}/.
func (h *SecurityHandler) GetScan(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid scan ID")
		return
	}

	scan, err := h.queries.GetSecurityScanResultByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Security scan not found")
		return
	}

	RespondJSON(w, http.StatusOK, scan)
}

// ListPolicies handles GET /api/v1/security/policies/.
func (h *SecurityHandler) ListPolicies(w http.ResponseWriter, r *http.Request) {
	policies, err := h.queries.ListClusterSecurityPolicies(r.Context(), sqlc.ListClusterSecurityPoliciesParams{
		Limit:  int32(queryInt(r, "limit", 20)),
		Offset: int32(queryInt(r, "offset", 0)),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list security policies")
		return
	}
	total, err := h.queries.CountClusterSecurityPolicies(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count security policies")
		return
	}
	RespondPaginated(w, r, policies, total)
}

// CreatePolicy handles POST /api/v1/security/policies/.
func (h *SecurityHandler) CreatePolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClusterID  uuid.UUID `json:"cluster_id"`
		TemplateID uuid.UUID `json:"template_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	policy, err := h.queries.CreateClusterSecurityPolicy(r.Context(), sqlc.CreateClusterSecurityPolicyParams{
		ClusterID:  req.ClusterID,
		TemplateID: req.TemplateID,
		SyncStatus: "pending",
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create security policy")
		return
	}
	recordAudit(r, h.queries, "security.policy.create", "cluster_security_policy", policy.ID.String(), "", map[string]any{
		"cluster_id":  req.ClusterID.String(),
		"template_id": req.TemplateID.String(),
	})
	RespondJSON(w, http.StatusCreated, policy)
}

// ApplyPolicy handles POST /api/v1/security/policies/{id}/apply/.
func (h *SecurityHandler) ApplyPolicy(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid policy ID")
		return
	}
	if _, err := h.queries.GetClusterSecurityPolicyByID(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Security policy not found")
		return
	}
	if err := h.queries.UpdateClusterSecurityPolicyApplied(r.Context(), id); err != nil {
		RespondError(w, http.StatusInternalServerError, "apply_error", "Failed to apply security policy")
		return
	}
	policy, _ := h.queries.GetClusterSecurityPolicyByID(r.Context(), id)
	recordAudit(r, h.queries, "security.policy.update", "cluster_security_policy", id.String(), "", map[string]any{
		"action":     "apply",
		"cluster_id": policy.ClusterID.String(),
	})
	RespondJSON(w, http.StatusOK, policy)
}

// DeletePolicy handles DELETE /api/v1/security/policies/{id}/.
func (h *SecurityHandler) DeletePolicy(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid policy ID")
		return
	}
	clusterID := ""
	if existing, lookupErr := h.queries.GetClusterSecurityPolicyByID(r.Context(), id); lookupErr == nil {
		clusterID = existing.ClusterID.String()
	}
	if err := h.queries.DeleteClusterSecurityPolicy(r.Context(), id); err != nil {
		RespondError(w, http.StatusInternalServerError, "delete_error", "Failed to delete security policy")
		return
	}
	recordAudit(r, h.queries, "security.policy.delete", "cluster_security_policy", id.String(), "", map[string]any{
		"cluster_id": clusterID,
	})
	w.WriteHeader(http.StatusNoContent)
}

// ListAllScans handles GET /api/v1/security/scans/.
func (h *SecurityHandler) ListAllScans(w http.ResponseWriter, r *http.Request) {
	scans, err := h.queries.ListSecurityScanResults(r.Context(), sqlc.ListSecurityScanResultsParams{
		Limit:  int32(queryInt(r, "limit", 20)),
		Offset: int32(queryInt(r, "offset", 0)),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list security scans")
		return
	}
	total, err := h.queries.CountSecurityScanResults(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count security scans")
		return
	}
	RespondPaginated(w, r, scans, total)
}

// CreateScan handles POST /api/v1/security/scans/.
//
// Phase B5: instead of just inserting a row, this now:
//  1. resolves the cluster to default the CIS profile from cluster.distribution,
//  2. POSTs a ClusterScan CR into the agent's `cis-operator-system` namespace
//     via the tunnel-backed K8s requester,
//  3. records our row with the upstream CR name so the worker can ingest the
//     ClusterScanReport,
//  4. enqueues an `security:ingest_scan_results` task for periodic polling.
//
// If the K8s requester is unset we fall back to the legacy (DB-only) path so
// older callers and the test suite continue to function.
func (h *SecurityHandler) CreateScan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClusterID uuid.UUID `json:"cluster_id"`
		// `profile` is the new field; `scan_type` kept for backward
		// compatibility with the existing UI.
		Profile  string `json:"profile"`
		ScanType string `json:"scan_type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	if req.ClusterID == uuid.Nil {
		RespondError(w, http.StatusBadRequest, "validation_error", "cluster_id is required")
		return
	}

	profile := strings.TrimSpace(req.Profile)
	if profile == "" {
		profile = strings.TrimSpace(req.ScanType)
	}
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
	if h.k8s != nil {
		resolveInput := profile
		if !explicitProfile {
			resolveInput = ""
		}
		if resolved := h.resolveClusterScanProfileName(r.Context(), req.ClusterID, clusterDistribution, resolveInput); strings.TrimSpace(resolved) != "" {
			profile = resolved
		}
	}

	scanName := fmt.Sprintf("astronomer-cis-%d", time.Now().UTC().Unix())

	if h.k8s != nil {
		if err := h.createClusterScanCR(r.Context(), req.ClusterID, scanName, profile); err != nil {
			h.log.Warn("create ClusterScan CR failed", "cluster_id", req.ClusterID.String(), "error", err)
			RespondError(w, http.StatusBadGateway, "cr_create_error", err.Error())
			return
		}
	}

	scan, err := h.queries.CreateCISScan(r.Context(), sqlc.CreateCISScanParams{
		ClusterID:       req.ClusterID,
		ScanType:        profile,
		Status:          "running",
		Summary:         json.RawMessage(`{}`),
		Results:         json.RawMessage(`[]`),
		ClusterScanName: scanName,
		InitiatedByID:   currentUserUUID(r),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create security scan")
		return
	}

	// Start the in-process ingest poller. We don't use asynq here even
	// though the worker package registers a task type for it: the worker
	// runs in a separate process with no tunnel hub, so the only place
	// ingestion can happen today is alongside the server. Keeping this in
	// a goroutine on a long-running context is fine — the loop is bounded
	// (up to 30 min) and exits cleanly on shutdown.
	if h.k8s != nil && h.persister != nil {
		go h.pollScanReport(scan.ID, req.ClusterID, scanName)
	}

	recordAudit(r, h.queries, "security.scan.create", "security_scan", scan.ID.String(), scanName, map[string]any{
		"cluster_id": req.ClusterID.String(),
		"profile":    profile,
	})

	RespondJSON(w, http.StatusCreated, scanWithFindings(scan))
}

// pollScanReport runs the in-process ingest loop. It checks the
// ClusterScanReport every 30 seconds for up to 30 minutes; on success it
// flattens the report into our row, on timeout it marks the row failed.
func (h *SecurityHandler) pollScanReport(scanID, clusterID uuid.UUID, scanName string) {
	const (
		interval    = 30 * time.Second
		maxAttempts = 60 // 30 minutes
	)
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
	defer cancel()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			h.markScanFailed(context.Background(), scanID, "ingest context cancelled")
			return
		case <-ticker.C:
		}
		report, found, err := h.fetchClusterScanReport(ctx, clusterID, scanName)
		if err != nil {
			h.log.Debug("fetch ClusterScanReport failed",
				"scan_id", scanID.String(), "attempt", attempt, "error", err)
			continue
		}
		if !found {
			continue
		}
		counts, findings, summary, results := flattenCISReport(report)
		if err := h.persister.UpdateSecurityScanReport(ctx, sqlc.UpdateSecurityScanReportParams{
			ID:       scanID,
			Summary:  summary,
			Results:  results,
			Passed:   counts.Pass,
			Failed:   counts.Fail,
			Warned:   counts.Warn,
			Skipped:  counts.Skip,
			Findings: findings,
		}); err != nil {
			h.log.Error("persist CIS scan report failed",
				"scan_id", scanID.String(), "error", err)
			h.markScanFailed(context.Background(), scanID, "persist failed: "+err.Error())
			return
		}
		h.log.Info("CIS scan ingested",
			"scan_id", scanID.String(),
			"pass", counts.Pass, "fail", counts.Fail,
			"warn", counts.Warn, "skip", counts.Skip)
		return
	}
	h.markScanFailed(context.Background(), scanID,
		fmt.Sprintf("ClusterScanReport not available after %d attempts", maxAttempts))
}

func (h *SecurityHandler) markScanFailed(ctx context.Context, scanID uuid.UUID, reason string) {
	if h.persister == nil {
		return
	}
	if err := h.persister.UpdateSecurityScanFailedWithMessage(ctx, sqlc.UpdateSecurityScanFailedWithMessageParams{
		ID:           scanID,
		ErrorMessage: reason,
	}); err != nil {
		h.log.Error("mark CIS scan failed failed",
			"scan_id", scanID.String(), "error", err)
	}
}

// fetchClusterScanReport returns (report, true, nil) when the report exists
// in the cluster, (nil, false, nil) when cis-operator hasn't generated it
// yet, and (nil, false, err) for unexpected errors.
func (h *SecurityHandler) fetchClusterScanReport(ctx context.Context, clusterID uuid.UUID, scanName string) (map[string]any, bool, error) {
	reportName, found, err := h.resolveClusterScanReportName(ctx, clusterID, scanName)
	if err != nil || !found {
		return nil, found, err
	}
	path := fmt.Sprintf("/apis/cis.cattle.io/v1/clusterscanreports/%s", reportName)
	resp, err := h.k8s.Do(ctx, clusterID.String(), http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return nil, false, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, false, responseError(resp)
	}
	var out map[string]any
	if err := parseJSONResponse(resp, &out); err != nil {
		return nil, false, err
	}
	return out, true, nil
}

func (h *SecurityHandler) resolveClusterScanReportName(ctx context.Context, clusterID uuid.UUID, scanName string) (string, bool, error) {
	if name, found, err := h.fetchClusterScanReportNameFromScan(ctx, clusterID, scanName); err != nil || found {
		return name, found, err
	}
	return h.findClusterScanReportNameByOwner(ctx, clusterID, scanName)
}

func (h *SecurityHandler) fetchClusterScanReportNameFromScan(ctx context.Context, clusterID uuid.UUID, scanName string) (string, bool, error) {
	path := fmt.Sprintf("/apis/cis.cattle.io/v1/clusterscans/%s", scanName)
	resp, err := h.k8s.Do(ctx, clusterID.String(), http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return "", false, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return "", false, nil
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return "", false, responseError(resp)
	}
	var scan struct {
		Status struct {
			ReportName string `json:"reportName"`
		} `json:"status"`
	}
	if err := parseJSONResponse(resp, &scan); err != nil {
		return "", false, err
	}
	if strings.TrimSpace(scan.Status.ReportName) == "" {
		return "", false, nil
	}
	return scan.Status.ReportName, true, nil
}

func (h *SecurityHandler) findClusterScanReportNameByOwner(ctx context.Context, clusterID uuid.UUID, scanName string) (string, bool, error) {
	resp, err := h.k8s.Do(ctx, clusterID.String(), http.MethodGet,
		"/apis/cis.cattle.io/v1/clusterscanreports", nil, requestHeaders(""))
	if err != nil {
		return "", false, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return "", false, nil
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return "", false, responseError(resp)
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
	if err := parseJSONResponse(resp, &list); err != nil {
		return "", false, err
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

// GetScanFull handles GET /api/v1/security/scans/{id}/ — returns the row
// with `findings` parsed out of JSONB so the UI doesn't have to re-decode
// it client-side.
func (h *SecurityHandler) GetScanFull(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid scan ID")
		return
	}
	scan, err := h.queries.GetSecurityScanResultByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Security scan not found")
		return
	}
	RespondJSON(w, http.StatusOK, scanWithFindings(scan))
}

// ExportScanCSV handles GET /api/v1/security/scans/{id}/report.csv — flattens
// the JSONB findings array into a CSV download for compliance evidence
// archives. Empty when the scan is still running.
func (h *SecurityHandler) ExportScanCSV(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid scan ID")
		return
	}
	scan, err := h.queries.GetSecurityScanResultByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Security scan not found")
		return
	}
	findings, _ := parseCISFindings(scan.Findings)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="cis-scan-%s.csv"`, id.String()))
	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write([]string{"test_id", "severity", "status", "description", "remediation"})
	for _, f := range findings {
		_ = cw.Write([]string{f.TestID, f.Severity, f.Status, f.Description, f.Remediation})
	}
}

// ListProfiles handles GET /api/v1/security/profiles/?cluster_id=X — returns
// the set of `ClusterScanProfile` CRs currently installed on the target
// cluster (cis-operator preinstalls a few). When the K8s requester is unset
// we return the static fallback set so the UI always renders something.
func (h *SecurityHandler) ListProfiles(w http.ResponseWriter, r *http.Request) {
	clusterIDStr := r.URL.Query().Get("cluster_id")
	if clusterIDStr == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "cluster_id query parameter is required")
		return
	}
	clusterID, err := uuid.Parse(clusterIDStr)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}
	if h.k8s == nil {
		RespondJSON(w, http.StatusOK, map[string]any{
			"items":  staticCISProfiles(),
			"source": "fallback",
		})
		return
	}
	resp, err := h.k8s.Do(r.Context(), clusterID.String(), http.MethodGet,
		"/apis/cis.cattle.io/v1/clusterscanprofiles", nil, requestHeaders(""))
	if err != nil {
		// Best-effort fallback so the UI keeps working when the operator
		// isn't installed yet.
		h.log.Warn("list ClusterScanProfile CRs failed", "cluster_id", clusterID.String(), "error", err)
		RespondJSON(w, http.StatusOK, map[string]any{
			"items":  staticCISProfiles(),
			"source": "fallback",
			"error":  err.Error(),
		})
		return
	}
	if err := ensureSuccess(resp); err != nil {
		RespondJSON(w, http.StatusOK, map[string]any{
			"items":  staticCISProfiles(),
			"source": "fallback",
			"error":  err.Error(),
		})
		return
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Benchmark string `json:"benchmarkVersion"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := parseJSONResponse(resp, &list); err != nil {
		RespondError(w, http.StatusBadGateway, "decode_error", "Failed to decode ClusterScanProfile list")
		return
	}
	items := make([]map[string]any, 0, len(list.Items))
	for _, item := range list.Items {
		items = append(items, map[string]any{
			"name":             item.Metadata.Name,
			"benchmarkVersion": item.Spec.Benchmark,
		})
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"items":  items,
		"source": "cluster",
	})
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

type cisClusterScanProfile struct {
	Name      string
	Benchmark string
}

func (h *SecurityHandler) resolveClusterScanProfileName(ctx context.Context, clusterID uuid.UUID, distribution, profile string) string {
	profile = strings.TrimSpace(profile)
	profiles, err := h.listClusterScanProfiles(ctx, clusterID)
	if err != nil || len(profiles) == 0 {
		return profile
	}
	if profile != "" {
		for _, item := range profiles {
			if item.Name == profile || item.Benchmark == profile {
				return item.Name
			}
		}
		return profile
	}
	if recommended, ok := recommendClusterScanProfileName(distribution, profiles); ok {
		return recommended
	}
	return profile
}

func (h *SecurityHandler) listClusterScanProfiles(ctx context.Context, clusterID uuid.UUID) ([]cisClusterScanProfile, error) {
	if h == nil || h.k8s == nil {
		return nil, nil
	}
	resp, err := h.k8s.Do(ctx, clusterID.String(), http.MethodGet,
		"/apis/cis.cattle.io/v1/clusterscanprofiles", nil, requestHeaders(""))
	if err != nil {
		return nil, err
	}
	if err := ensureSuccess(resp); err != nil {
		return nil, err
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Benchmark string `json:"benchmarkVersion"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := parseJSONResponse(resp, &list); err != nil {
		return nil, err
	}
	out := make([]cisClusterScanProfile, 0, len(list.Items))
	for _, item := range list.Items {
		out = append(out, cisClusterScanProfile{
			Name:      item.Metadata.Name,
			Benchmark: item.Spec.Benchmark,
		})
	}
	return out, nil
}

var cisBenchmarkVersionRE = regexp.MustCompile(`(\d+)\.(\d+)`)

func recommendClusterScanProfileName(distribution string, profiles []cisClusterScanProfile) (string, bool) {
	prefix := cisProfileBenchmarkPrefix(distribution)
	bestName := ""
	bestMajor := -1
	bestMinor := -1
	bestPermissive := false
	for _, profile := range profiles {
		if !strings.HasPrefix(profile.Benchmark, prefix) {
			continue
		}
		major, minor := parseCISBenchmarkVersion(profile.Benchmark)
		permissive := strings.Contains(profile.Name, "permissive") || strings.Contains(profile.Benchmark, "permissive")
		if bestName == "" ||
			major > bestMajor ||
			(major == bestMajor && minor > bestMinor) ||
			(major == bestMajor && minor == bestMinor && permissive && !bestPermissive) {
			bestName = profile.Name
			bestMajor = major
			bestMinor = minor
			bestPermissive = permissive
		}
	}
	if bestName != "" {
		return bestName, true
	}
	if prefix != "cis-" {
		return recommendClusterScanProfileName("", profiles)
	}
	return "", false
}

func cisProfileBenchmarkPrefix(distribution string) string {
	switch strings.ToLower(strings.TrimSpace(distribution)) {
	case "rke", "rke1":
		return "rke-"
	case "rke2":
		return "rke2-"
	case "k3s":
		return "k3s-"
	case "eks":
		return "eks-"
	case "aks":
		return "aks-"
	case "gke":
		return "gke-"
	default:
		return "cis-"
	}
}

func parseCISBenchmarkVersion(s string) (int, int) {
	matches := cisBenchmarkVersionRE.FindStringSubmatch(s)
	if len(matches) != 3 {
		return -1, -1
	}
	major, err := strconv.Atoi(matches[1])
	if err != nil {
		return -1, -1
	}
	minor, err := strconv.Atoi(matches[2])
	if err != nil {
		return -1, -1
	}
	return major, minor
}

// CISFinding is the normalized shape we store in `findings` and surface to
// the UI / CSV. It's intentionally minimal — the cis-operator report has
// dozens of fields, but most users only care about pass/fail + remediation.
type CISFinding struct {
	TestID      string `json:"test_id"`
	Severity    string `json:"severity"`
	Status      string `json:"status"`
	Description string `json:"description"`
	Remediation string `json:"remediation"`
}

func parseCISFindings(raw json.RawMessage) ([]CISFinding, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var out []CISFinding
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// scanWithFindings expands the JSONB findings column into a typed slice on
// the response so the UI can iterate without re-parsing.
func scanWithFindings(scan sqlc.SecurityScanResult) map[string]any {
	findings, _ := parseCISFindings(scan.Findings)
	out := map[string]any{
		"id":                scan.ID.String(),
		"cluster_id":        scan.ClusterID.String(),
		"scan_type":         scan.ScanType,
		"status":            scan.Status,
		"summary":           json.RawMessage(scan.Summary),
		"results":           json.RawMessage(scan.Results),
		"started_at":        scan.StartedAt.UTC().Format(time.RFC3339),
		"cluster_scan_name": scan.ClusterScanName,
		"passed":            scan.Passed,
		"failed":            scan.Failed,
		"warned":            scan.Warned,
		"skipped":           scan.Skipped,
		"findings":          findings,
		"created_at":        scan.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":        scan.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if scan.CompletedAt.Valid {
		out["completed_at"] = scan.CompletedAt.Time.UTC().Format(time.RFC3339)
	} else {
		out["completed_at"] = nil
	}
	return out
}

// defaultCISProfileForDistribution maps the distribution string the cluster
// row carries to the CIS profile that ships preinstalled with cis-operator.
// Falls back to cis-1.8 for unknown distributions.
func defaultCISProfileForDistribution(distribution string) string {
	switch strings.ToLower(strings.TrimSpace(distribution)) {
	case "rke", "rke1":
		return "rke-profile-permissive-1.8"
	case "rke2":
		return "rke2-cis-1.8-profile-permissive"
	case "k3s":
		return "k3s-cis-1.8-profile-permissive"
	case "eks":
		return "eks-profile-1.5.0"
	case "aks":
		return "aks-profile"
	case "gke":
		return "gke-profile-1.6.0"
	default:
		return "cis-1.8-profile"
	}
}

// CISCounts is the flattened pass/fail/warn/skip totals from a
// cis-operator ClusterScanReport. Exposed for unit testing of the report
// parser.
type CISCounts struct {
	Total int32
	Pass  int32
	Fail  int32
	Warn  int32
	Skip  int32
}

// FlattenCISReport is the public entry point exercised by the unit tests.
// Given a raw ClusterScanReport object (already JSON-decoded into a map),
// it returns the totals + a slice of normalized findings, plus the summary
// and results JSON we want to persist on the row.
func FlattenCISReport(report map[string]any) (CISCounts, []CISFinding, json.RawMessage, json.RawMessage) {
	counts, findingsRaw, summary, results := flattenCISReport(report)
	var findings []CISFinding
	_ = json.Unmarshal(findingsRaw, &findings)
	return counts, findings, summary, results
}

// flattenCISReport is the shared parser. cis-operator's ClusterScanReport
// stores the actual benchmark output under either:
//   - `spec.reportJSON` (a string-encoded JSON blob, current shape), or
//   - `spec.report` (an inlined object, on some forks).
//
// Inside that payload we expect top-level pass/fail/warn/skip counts plus
// a `results[]` (or `tests[]`) slice of sections, each with an inner
// `checks[]` (or `results[]` / `tests[]`) holding the individual test
// records. We flatten everything into our normalized CISFinding shape so
// the UI doesn't have to learn cis-operator's schema.
func flattenCISReport(report map[string]any) (CISCounts, json.RawMessage, json.RawMessage, json.RawMessage) {
	var counts CISCounts
	findings := make([]CISFinding, 0)

	spec, _ := report["spec"].(map[string]any)
	payload := decodeReportJSON(spec)

	if v, ok := numericField(payload, "total"); ok {
		counts.Total = v
	}
	if v, ok := numericField(payload, "pass"); ok {
		counts.Pass = v
	}
	if v, ok := numericField(payload, "fail"); ok {
		counts.Fail = v
	}
	if v, ok := numericField(payload, "warn"); ok {
		counts.Warn = v
	}
	if v, ok := numericField(payload, "skip"); ok {
		counts.Skip = v
	}

	sections, _ := payload["results"].([]any)
	if len(sections) == 0 {
		sections, _ = payload["tests"].([]any)
	}
	for _, raw := range sections {
		section, ok := raw.(map[string]any)
		if !ok {
			continue
		}
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
			findings = append(findings, CISFinding{
				TestID:      stringField(test, "id", "test_number", "number"),
				Severity:    stringField(test, "scored_severity", "severity"),
				Status:      stringField(test, "state", "status"),
				Description: stringField(test, "test_desc", "description", "desc"),
				Remediation: stringField(test, "remediation"),
			})
		}
	}

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
		"profile": stringField(spec, "scanProfileName"),
	})
	findingsRaw, _ := json.Marshal(findings)
	return counts, findingsRaw, summaryRaw, resultsRaw
}

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

func staticCISProfiles() []map[string]any {
	return []map[string]any{
		{"name": "cis-1.8", "benchmarkVersion": "cis-1.8"},
		{"name": "rke-cis-1.8-permissive", "benchmarkVersion": "rke-cis-1.8"},
		{"name": "rke2-cis-1.8-permissive", "benchmarkVersion": "rke2-cis-1.8"},
		{"name": "k3s-cis-1.8-permissive", "benchmarkVersion": "k3s-cis-1.8"},
		{"name": "eks-cis-1.5", "benchmarkVersion": "eks-cis-1.5"},
		{"name": "aks-cis-1.0", "benchmarkVersion": "aks-cis-1.0"},
		{"name": "gke-cis-1.5", "benchmarkVersion": "gke-cis-1.5"},
	}
}
