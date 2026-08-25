// Package handler — Per-tenant resource quotas admin surface.
//
// Migration 051 introduced the quota_plans + projects.quota_plan +
// users.quota_plan schema. This file is the admin API in front of it.
// Motivation mirrors Rancher's project / resource-quota tab: an
// operator running shared infrastructure needs a single place to
// reshape the catalog of caps (free vs team vs enterprise) AND see
// estate-wide consumption against those caps.
//
// Endpoints (all under /api/v1):
//
//	GET    /admin/quota-plans/           — list plans
//	POST   /admin/quota-plans/           — create plan
//	GET    /admin/quota-plans/{name}/    — get one plan
//	PUT    /admin/quota-plans/{name}/    — update (audit emitted)
//	DELETE /admin/quota-plans/{name}/    — delete; 409 if in-use
//
//	GET    /admin/quota-usage/           — estate-wide snapshot:
//	                                       totals + offenders at >=80%
//
//	GET    /projects/{id}/quota/         — project-scoped usage
//	GET    /auth/me/quota/               — caller's own usage
//
// Admin endpoints are superuser-gated inside the handler (same
// pattern as platform_settings + smtp).
//
// Delete-while-in-use protection: the FK on projects.quota_plan /
// users.quota_plan is ON DELETE SET DEFAULT, so a stray delete won't
// orphan rows — they'd fall back to 'free'. The handler's
// CountProjectsUsingQuotaPlan + CountUsersUsingQuotaPlan precheck
// turns that into a clean 409 with a "remove N tenants first" body
// so the operator notices instead of having a silent fallback.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/quota"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// QuotaQuerier is the narrow DB surface the quota handler uses.
// *sqlc.Queries satisfies it.
type QuotaQuerier interface {
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	ListQuotaPlans(ctx context.Context) ([]sqlc.QuotaPlan, error)
	GetQuotaPlan(ctx context.Context, name string) (sqlc.QuotaPlan, error)
	GetQuotaPlanForUpdate(ctx context.Context, name string) (sqlc.QuotaPlan, error)
	UpsertQuotaPlan(ctx context.Context, arg sqlc.UpsertQuotaPlanParams) (sqlc.QuotaPlan, error)
	DeleteQuotaPlan(ctx context.Context, name string) error
	CountProjectsUsingQuotaPlan(ctx context.Context, plan string) (int64, error)
	CountUsersUsingQuotaPlan(ctx context.Context, plan string) (int64, error)

	GetEffectiveQuotaForUser(ctx context.Context, id uuid.UUID) (sqlc.GetEffectiveQuotaForUserRow, error)
	GetEffectiveQuotaForProject(ctx context.Context, id uuid.UUID) (sqlc.GetEffectiveQuotaForProjectRow, error)

	CountClustersInProject(ctx context.Context, projectID uuid.UUID) (int64, error)
	CountNamespacesInProject(ctx context.Context, projectID uuid.UUID) (int32, error)
	CountMembersInProject(ctx context.Context, projectID uuid.UUID) (int64, error)
	CountProjectsForUser(ctx context.Context, userID uuid.UUID) (int64, error)
	CountActiveTokensForUser(ctx context.Context, userID uuid.UUID) (int64, error)
	CountTotalClusters(ctx context.Context) (int64, error)
	CountTotalActiveUsers(ctx context.Context) (int64, error)
	ListProjectQuotaSnapshots(ctx context.Context, arg sqlc.ListProjectQuotaSnapshotsParams) ([]sqlc.ProjectQuotaSnapshotRow, error)
	ListUserQuotaSnapshots(ctx context.Context, arg sqlc.ListUserQuotaSnapshotsParams) ([]sqlc.UserQuotaSnapshotRow, error)
}

type QuotaMutationTx interface {
	QuotaQuerier
	audit.OutboxQuerier
}

type quotaRunTxFunc func(context.Context, func(QuotaMutationTx) error) error

func executeQuotaMutation(r *http.Request, h *QuotaHandler, mutate func(QuotaQuerier) (sqlc.QuotaPlan, error), describe func(sqlc.QuotaPlan) clusterAuditEvent) (sqlc.QuotaPlan, error) {
	if h.runTx != nil {
		var plan sqlc.QuotaPlan
		err := h.runTx(r.Context(), func(q QuotaMutationTx) error {
			var mutationErr error
			plan, mutationErr = mutate(q)
			if mutationErr != nil {
				return mutationErr
			}
			event := describe(plan)
			return recordAuditOutbox(r, q, event.action, event.resourceType, event.resourceID, event.resourceName, event.status, event.detail)
		})
		return plan, err
	}
	plan, err := mutate(h.queries)
	if err != nil {
		return sqlc.QuotaPlan{}, err
	}
	event := describe(plan)
	recordAudit(r, h.queries, event.action, event.resourceType, event.resourceID, event.resourceName, event.detail)
	return plan, nil
}

var (
	errQuotaPlanInUse      = errors.New("quota plan is in use")
	errQuotaReferenceCount = errors.New("quota plan reference count failed")
)

// QuotaHandler owns /api/v1/admin/quota-plans/* and the two
// tenant-scoped /quota/ readers.
type QuotaHandler struct {
	queries QuotaQuerier
	runTx   quotaRunTxFunc
}

// NewQuotaHandler wires a new handler. queries may be nil for
// degenerate test installs; in that case every endpoint returns 503.
func NewQuotaHandler(queries QuotaQuerier) *QuotaHandler {
	return &QuotaHandler{queries: queries}
}

func (h *QuotaHandler) SetRunTx(runTx quotaRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *QuotaHandler) TransactionalAuditWired() bool { return h != nil && h.runTx != nil }

// gate is the superuser gate, mirroring platform_settings.gate.
func (h *QuotaHandler) gate(w http.ResponseWriter, r *http.Request) bool {
	_, ok := requireSuperuser(w, r, h.queries, superuserGateConfig{
		StoreUnavailableCode:    "not_configured",
		StoreUnavailableMessage: "Quota store not configured",
		ForbiddenMessage:        "Quota administration requires superuser privileges",
	})
	return ok
}

// ─── Plan CRUD ────────────────────────────────────────────────────────

// quotaPlanResponse is the wire shape returned by GET / POST / PUT
// endpoints.
type quotaPlanResponse struct {
	Name                    string `json:"name"`
	Enforcement             string `json:"enforcement"`
	Description             string `json:"description"`
	MaxClustersPerProject   int32  `json:"max_clusters_per_project"`
	MaxNamespacesPerProject int32  `json:"max_namespaces_per_project"`
	MaxMembersPerProject    int32  `json:"max_members_per_project"`
	MaxProjectsPerUser      int32  `json:"max_projects_per_user"`
	MaxTokensPerUser        int32  `json:"max_tokens_per_user"`
	MaxStreamsPerUser       int32  `json:"max_streams_per_user"`
	MaxTotalClusters        int32  `json:"max_total_clusters"`
	MaxTotalUsers           int32  `json:"max_total_users"`
}

func planToResponse(p sqlc.QuotaPlan) quotaPlanResponse {
	return quotaPlanResponse{
		Name:                    p.Name,
		Enforcement:             p.Enforcement,
		Description:             p.Description,
		MaxClustersPerProject:   p.MaxClustersPerProject,
		MaxNamespacesPerProject: p.MaxNamespacesPerProject,
		MaxMembersPerProject:    p.MaxMembersPerProject,
		MaxProjectsPerUser:      p.MaxProjectsPerUser,
		MaxTokensPerUser:        p.MaxTokensPerUser,
		MaxStreamsPerUser:       p.MaxStreamsPerUser,
		MaxTotalClusters:        p.MaxTotalClusters,
		MaxTotalUsers:           p.MaxTotalUsers,
	}
}

// quotaPlanRequest is the body shape accepted by POST + PUT.
// openapi:request QuotaPlanRequest
type quotaPlanRequest struct {
	Name                    string `json:"name"`
	Enforcement             string `json:"enforcement"`
	Description             string `json:"description"`
	MaxClustersPerProject   int32  `json:"max_clusters_per_project"`
	MaxNamespacesPerProject int32  `json:"max_namespaces_per_project"`
	MaxMembersPerProject    int32  `json:"max_members_per_project"`
	MaxProjectsPerUser      int32  `json:"max_projects_per_user"`
	MaxTokensPerUser        int32  `json:"max_tokens_per_user"`
	MaxStreamsPerUser       int32  `json:"max_streams_per_user"`
	MaxTotalClusters        int32  `json:"max_total_clusters"`
	MaxTotalUsers           int32  `json:"max_total_users"`
}

// validEnforcement enforces the CHECK constraint client-side so the
// API returns a clean 400 instead of a generic DB error.
func validEnforcement(s string) bool { return s == "soft" || s == "hard" }

// ListPlans handles GET /api/v1/admin/quota-plans/.
func (h *QuotaHandler) ListPlans(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	rows, err := h.queries.ListQuotaPlans(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list quota plans")
		return
	}
	out := make([]quotaPlanResponse, 0, len(rows))
	for _, p := range rows {
		out = append(out, planToResponse(p))
	}
	page, pagination := pageWindow(r, out)
	RespondList(w, page, pagination)
}

// GetPlan handles GET /api/v1/admin/quota-plans/{name}/.
func (h *QuotaHandler) GetPlan(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	name := chi.URLParam(r, "name")
	if name == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidName, "Plan name required")
		return
	}
	p, err := h.queries.GetQuotaPlan(r.Context(), name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Quota plan not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.GetError, "Failed to fetch quota plan")
		return
	}
	RespondJSON(w, http.StatusOK, planToResponse(p))
}

// CreatePlan handles POST /api/v1/admin/quota-plans/.
func (h *QuotaHandler) CreatePlan(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	var req quotaPlanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if req.Name == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Plan name is required")
		return
	}
	if req.Enforcement == "" {
		req.Enforcement = "hard"
	}
	if !validEnforcement(req.Enforcement) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "enforcement must be 'soft' or 'hard'")
		return
	}
	p, err := executeQuotaMutation(r, h,
		func(q QuotaQuerier) (sqlc.QuotaPlan, error) {
			return q.UpsertQuotaPlan(r.Context(), upsertParamsFromRequest(req))
		},
		quotaPlanAuditEvent("quota.plan_create", http.StatusCreated),
	)
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create quota plan")
		return
	}
	w.Header().Set("Location", "/api/v1/admin/quota-plans/"+p.Name+"/")
	RespondJSON(w, http.StatusCreated, planToResponse(p))
}

// UpdatePlan handles PUT /api/v1/admin/quota-plans/{name}/.
func (h *QuotaHandler) UpdatePlan(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	name := chi.URLParam(r, "name")
	if name == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidName, "Plan name required")
		return
	}
	var req quotaPlanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	// Body's `name` is ignored — URL is authoritative.
	req.Name = name
	if req.Enforcement == "" {
		req.Enforcement = "hard"
	}
	if !validEnforcement(req.Enforcement) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "enforcement must be 'soft' or 'hard'")
		return
	}
	p, err := executeQuotaMutation(r, h,
		func(q QuotaQuerier) (sqlc.QuotaPlan, error) {
			// Keep the existence decision and upsert in the same transaction so a
			// concurrent delete cannot turn an update into an implicit create.
			if _, getErr := q.GetQuotaPlanForUpdate(r.Context(), name); getErr != nil {
				return sqlc.QuotaPlan{}, getErr
			}
			return q.UpsertQuotaPlan(r.Context(), upsertParamsFromRequest(req))
		},
		quotaPlanAuditEvent("quota.plan_update", http.StatusOK),
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Quota plan not found")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.UpdateError, "Failed to update quota plan")
		return
	}
	RespondJSON(w, http.StatusOK, planToResponse(p))
}

// DeletePlan handles DELETE /api/v1/admin/quota-plans/{name}/.
// Rejected with 409 if any project / user still references the plan.
func (h *QuotaHandler) DeletePlan(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	name := chi.URLParam(r, "name")
	if name == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidName, "Plan name required")
		return
	}
	// Block deleting any seeded singleton plan — 'free' is the default
	// fallback, so removing it would leave new tenants with no plan
	// to default to. 'global' is the fleet-cap singleton; if an
	// operator wants to disable it they should set its max_total_*
	// to 0 (unlimited) rather than dropping the row entirely.
	switch name {
	case "free", "global":
		RespondRequestError(w, r, http.StatusConflict, apierror.PlanIsReserved, "The 'free' and 'global' quota plans are reserved and cannot be deleted")
		return
	}
	_, err := executeQuotaMutation(r, h,
		func(q QuotaQuerier) (sqlc.QuotaPlan, error) {
			plan, getErr := q.GetQuotaPlanForUpdate(r.Context(), name)
			if getErr != nil {
				return sqlc.QuotaPlan{}, getErr
			}
			projCount, countErr := q.CountProjectsUsingQuotaPlan(r.Context(), name)
			if countErr != nil {
				return sqlc.QuotaPlan{}, fmt.Errorf("%w: %v", errQuotaReferenceCount, countErr)
			}
			userCount, countErr := q.CountUsersUsingQuotaPlan(r.Context(), name)
			if countErr != nil {
				return sqlc.QuotaPlan{}, fmt.Errorf("%w: %v", errQuotaReferenceCount, countErr)
			}
			if projCount > 0 || userCount > 0 {
				return sqlc.QuotaPlan{}, errQuotaPlanInUse
			}
			return plan, q.DeleteQuotaPlan(r.Context(), name)
		},
		quotaPlanAuditEvent("quota.plan_delete", http.StatusNoContent),
	)
	if errors.Is(err, errQuotaPlanInUse) {
		RespondRequestError(w, r, http.StatusConflict, apierror.PlanInUse,
			"Quota plan is still referenced by at least one project or user; reassign them first")
		return
	}
	if errors.Is(err, errQuotaReferenceCount) {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count plan references")
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Quota plan not found")
		return
	}
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete quota plan")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func quotaPlanAuditEvent(action string, status int) func(sqlc.QuotaPlan) clusterAuditEvent {
	return func(plan sqlc.QuotaPlan) clusterAuditEvent {
		return clusterAuditEvent{
			action: action, resourceType: "quota_plan", resourceID: plan.Name, resourceName: plan.Name, status: status,
			detail: map[string]any{"enforcement": plan.Enforcement, "max_clusters_per_project": plan.MaxClustersPerProject, "max_projects_per_user": plan.MaxProjectsPerUser, "max_tokens_per_user": plan.MaxTokensPerUser},
		}
	}
}

func upsertParamsFromRequest(req quotaPlanRequest) sqlc.UpsertQuotaPlanParams {
	return sqlc.UpsertQuotaPlanParams{
		Name:                    req.Name,
		Enforcement:             req.Enforcement,
		Description:             req.Description,
		MaxClustersPerProject:   req.MaxClustersPerProject,
		MaxNamespacesPerProject: req.MaxNamespacesPerProject,
		MaxMembersPerProject:    req.MaxMembersPerProject,
		MaxProjectsPerUser:      req.MaxProjectsPerUser,
		MaxTokensPerUser:        req.MaxTokensPerUser,
		MaxStreamsPerUser:       req.MaxStreamsPerUser,
		MaxTotalClusters:        req.MaxTotalClusters,
		MaxTotalUsers:           req.MaxTotalUsers,
	}
}

// ─── Estate-wide quota usage snapshot ──────────────────────────────────

// usageThresholdPct is the cutoff at which a tenant counts as a
// "top offender" worth surfacing on the admin dashboard. 80% mirrors
// the convention used elsewhere in the operator UI (cluster CPU /
// disk early-warning thresholds).
const usageThresholdPct = 80

// quotaUsageResponse is the wire shape for /admin/quota-usage/.
type quotaUsageResponse struct {
	Global struct {
		TotalClusters    int64 `json:"total_clusters"`
		MaxTotalClusters int32 `json:"max_total_clusters"`
		TotalUsers       int64 `json:"total_users"`
		MaxTotalUsers    int32 `json:"max_total_users"`
	} `json:"global"`
	ProjectOffenders []projectOffenderRow `json:"project_offenders"`
	UserOffenders    []userOffenderRow    `json:"user_offenders"`
}

type projectOffenderRow struct {
	ProjectID   string  `json:"project_id"`
	ProjectName string  `json:"project_name"`
	QuotaPlan   string  `json:"quota_plan"`
	Limit       string  `json:"limit"`
	Current     int64   `json:"current"`
	Maximum     int32   `json:"maximum"`
	UsagePct    float64 `json:"usage_pct"`
}

type userOffenderRow struct {
	UserID    string  `json:"user_id"`
	Username  string  `json:"username"`
	QuotaPlan string  `json:"quota_plan"`
	Limit     string  `json:"limit"`
	Current   int64   `json:"current"`
	Maximum   int32   `json:"maximum"`
	UsagePct  float64 `json:"usage_pct"`
}

// FleetUsage handles GET /api/v1/admin/quota-usage/.
func (h *QuotaHandler) FleetUsage(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	var resp quotaUsageResponse
	resp.ProjectOffenders = []projectOffenderRow{}
	resp.UserOffenders = []userOffenderRow{}

	plan, err := h.queries.GetQuotaPlan(r.Context(), "global")
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.GetError, "Failed to load global quota plan")
		return
	}
	resp.Global.MaxTotalClusters = plan.MaxTotalClusters
	resp.Global.MaxTotalUsers = plan.MaxTotalUsers
	clusters, err := h.queries.CountTotalClusters(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count clusters")
		return
	}
	resp.Global.TotalClusters = clusters
	users, err := h.queries.CountTotalActiveUsers(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count active users")
		return
	}
	resp.Global.TotalUsers = users

	// Project offenders. We page through the snapshot table because
	// the offender filter is in Go (the SQL view can't cheaply express
	// "max == 0 means unlimited"). Read every page: silently ignoring tenants
	// after the first batch would make the fleet dashboard incorrect precisely
	// at enterprise scale.
	projRows, err := h.listAllProjectQuotaSnapshots(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list project quota usage")
		return
	}
	resp.ProjectOffenders = collectProjectOffenders(projRows)
	userRows, err := h.listAllUserQuotaSnapshots(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list user quota usage")
		return
	}
	resp.UserOffenders = collectUserOffenders(userRows)
	RespondJSON(w, http.StatusOK, resp)
}

const quotaSnapshotBatchSize int32 = 500

func (h *QuotaHandler) listAllProjectQuotaSnapshots(ctx context.Context) ([]sqlc.ProjectQuotaSnapshotRow, error) {
	all := []sqlc.ProjectQuotaSnapshotRow{}
	for offset := int32(0); ; offset += quotaSnapshotBatchSize {
		page, err := h.queries.ListProjectQuotaSnapshots(ctx, sqlc.ListProjectQuotaSnapshotsParams{
			Limit: quotaSnapshotBatchSize, Offset: offset,
		})
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < int(quotaSnapshotBatchSize) {
			return all, nil
		}
	}
}

func (h *QuotaHandler) listAllUserQuotaSnapshots(ctx context.Context) ([]sqlc.UserQuotaSnapshotRow, error) {
	all := []sqlc.UserQuotaSnapshotRow{}
	for offset := int32(0); ; offset += quotaSnapshotBatchSize {
		page, err := h.queries.ListUserQuotaSnapshots(ctx, sqlc.ListUserQuotaSnapshotsParams{
			Limit: quotaSnapshotBatchSize, Offset: offset,
		})
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < int(quotaSnapshotBatchSize) {
			return all, nil
		}
	}
}

func collectProjectOffenders(rows []sqlc.ProjectQuotaSnapshotRow) []projectOffenderRow {
	out := []projectOffenderRow{}
	check := func(r sqlc.ProjectQuotaSnapshotRow, limit string, current int64, max int32) {
		if max <= 0 {
			return
		}
		pct := float64(current) / float64(max) * 100
		if pct < usageThresholdPct {
			return
		}
		out = append(out, projectOffenderRow{
			ProjectID:   r.ProjectID.String(),
			ProjectName: r.ProjectName,
			QuotaPlan:   r.QuotaPlan,
			Limit:       limit,
			Current:     current,
			Maximum:     max,
			UsagePct:    pct,
		})
	}
	for _, r := range rows {
		check(r, "max_clusters_per_project", r.ClustersInProject, r.MaxClustersPerProject)
		check(r, "max_namespaces_per_project", r.NamespacesInProject, r.MaxNamespacesPerProject)
		check(r, "max_members_per_project", r.MembersInProject, r.MaxMembersPerProject)
	}
	return out
}

func collectUserOffenders(rows []sqlc.UserQuotaSnapshotRow) []userOffenderRow {
	out := []userOffenderRow{}
	check := func(r sqlc.UserQuotaSnapshotRow, limit string, current int64, max int32) {
		if max <= 0 {
			return
		}
		pct := float64(current) / float64(max) * 100
		if pct < usageThresholdPct {
			return
		}
		out = append(out, userOffenderRow{
			UserID:    r.UserID.String(),
			Username:  r.Username,
			QuotaPlan: r.QuotaPlan,
			Limit:     limit,
			Current:   current,
			Maximum:   max,
			UsagePct:  pct,
		})
	}
	for _, r := range rows {
		check(r, "max_projects_per_user", r.ProjectsForUser, r.MaxProjectsPerUser)
		check(r, "max_tokens_per_user", r.TokensForUser, r.MaxTokensPerUser)
	}
	return out
}

// ─── Per-tenant /quota/ readers ───────────────────────────────────────

// ProjectQuota handles GET /api/v1/projects/{id}/quota/. RBAC for the
// outer /projects/ route already gates the caller's read access; we
// don't re-check here.
func (h *QuotaHandler) ProjectQuota(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Quota store not configured")
		return
	}
	idStr := chi.URLParam(r, "id")
	projectID, err := uuid.Parse(idStr)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid project ID")
		return
	}
	plan, err := h.queries.GetEffectiveQuotaForProject(r.Context(), projectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Project not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.LookupError, "Failed to load project quota")
		return
	}
	clusters, err := h.queries.CountClustersInProject(r.Context(), projectID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count project clusters")
		return
	}
	namespaces, err := h.queries.CountNamespacesInProject(r.Context(), projectID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count project namespaces")
		return
	}
	members, err := h.queries.CountMembersInProject(r.Context(), projectID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count project members")
		return
	}

	limits := map[string]int32{
		"max_clusters_per_project":   plan.MaxClustersPerProject,
		"max_namespaces_per_project": plan.MaxNamespacesPerProject,
		"max_members_per_project":    plan.MaxMembersPerProject,
	}
	usage := map[string]int64{
		"max_clusters_per_project":   clusters,
		"max_namespaces_per_project": int64(namespaces),
		"max_members_per_project":    members,
	}
	resp := map[string]any{
		"project_id":  plan.ProjectID.String(),
		"quota_plan":  plan.PlanName,
		"enforcement": plan.Enforcement,
		"limits":      limits,
		"usage":       usage,
		"usage_pct":   computeUsagePct(limits, usage),
		"overrides":   decodeOverrides(plan.Overrides),
	}
	RespondJSON(w, http.StatusOK, resp)
}

// MyQuota handles GET /api/v1/auth/me/quota/.
func (h *QuotaHandler) MyQuota(w http.ResponseWriter, r *http.Request) {
	caller, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok || caller == nil {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Quota store not configured")
		return
	}
	userID, err := uuid.Parse(caller.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Invalid caller ID")
		return
	}
	plan, err := h.queries.GetEffectiveQuotaForUser(r.Context(), userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "User not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.LookupError, "Failed to load user quota")
		return
	}
	projects, err := h.queries.CountProjectsForUser(r.Context(), userID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count user projects")
		return
	}
	tokens, err := h.queries.CountActiveTokensForUser(r.Context(), userID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count active API tokens")
		return
	}
	limits := map[string]int32{
		"max_projects_per_user": plan.MaxProjectsPerUser,
		"max_tokens_per_user":   plan.MaxTokensPerUser,
		"max_streams_per_user":  plan.MaxStreamsPerUser,
	}
	usage := map[string]int64{
		"max_projects_per_user": projects,
		"max_tokens_per_user":   tokens,
		// max_streams_per_user is enforced by the SSE handler itself;
		// the count isn't materialised in the DB, so we publish 0 here.
		"max_streams_per_user": 0,
	}
	resp := map[string]any{
		"user_id":     plan.UserID.String(),
		"quota_plan":  plan.PlanName,
		"enforcement": plan.Enforcement,
		"limits":      limits,
		"usage":       usage,
		"usage_pct":   computeUsagePct(limits, usage),
		"overrides":   decodeOverrides(plan.Overrides),
	}
	RespondJSON(w, http.StatusOK, resp)
}

func computeUsagePct(limits map[string]int32, usage map[string]int64) map[string]float64 {
	out := make(map[string]float64, len(limits))
	for k, max := range limits {
		if max <= 0 {
			out[k] = 0
			continue
		}
		out[k] = float64(usage[k]) / float64(max) * 100
	}
	return out
}

func decodeOverrides(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	if out == nil {
		return map[string]any{}
	}
	return out
}

// WriteQuotaExceeded is the canonical translator for the
// quota.QuotaExceededError type. Hooked from the gated handlers
// (clusters.Create, auth.CreateToken, ...) so the wire response is
// consistent across every quota site.
func WriteQuotaExceeded(w http.ResponseWriter, e *quota.QuotaExceededError) {
	body := map[string]any{
		"error": map[string]any{
			"code":    "quota_exceeded",
			"message": "Quota exceeded for this tenant.",
			"limit":   e.Limit,
			"current": e.Current,
			"maximum": e.Maximum,
			"subject": e.Subject,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(body)
}
