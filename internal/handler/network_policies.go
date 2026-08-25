// Network policy templates handler (migration 068).
//
// Two mount points:
//
//   - /api/v1/admin/network-policy-templates/*  — superuser CRUD over the
//     library of NetworkPolicy bundles. Builtin rows are read-only
//     (PUT/DELETE on a kind='builtin' row returns 403).
//
//   - /api/v1/clusters/{cluster_id}/network-policies/applications/* —
//     per-cluster bind/list/delete/reapply. Gated on
//     ResourceClusters + VerbUpdate (the operator who can edit a cluster
//     can apply a NetworkPolicy template to one of its namespaces).
//
// Sister-feature of sprint 049 cluster_templates: those manage cluster-
// level config (env, labels, tool installs); this is namespace-level
// network security baselines. The reconciler is in
// internal/worker/tasks/network_policy_apply.go.

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/netpol"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// NetworkPolicyQuerier is the database surface the handler needs. The
// production *sqlc.Queries satisfies it; tests stand up a narrow fake.
type NetworkPolicyQuerier interface {
	// Template CRUD.
	ListNetworkPolicyTemplates(ctx context.Context, arg sqlc.ListNetworkPolicyTemplatesParams) ([]sqlc.NetworkPolicyTemplate, error)
	CountNetworkPolicyTemplates(ctx context.Context) (int64, error)
	GetNetworkPolicyTemplateByID(ctx context.Context, id uuid.UUID) (sqlc.NetworkPolicyTemplate, error)
	GetNetworkPolicyTemplateBySlug(ctx context.Context, slug string) (sqlc.NetworkPolicyTemplate, error)
	CreateNetworkPolicyTemplate(ctx context.Context, arg sqlc.CreateNetworkPolicyTemplateParams) (sqlc.NetworkPolicyTemplate, error)
	UpdateNetworkPolicyTemplate(ctx context.Context, arg sqlc.UpdateNetworkPolicyTemplateParams) (sqlc.NetworkPolicyTemplate, error)
	DeleteNetworkPolicyTemplate(ctx context.Context, id uuid.UUID) error

	// Application CRUD.
	ListApplicationsForCluster(ctx context.Context, clusterID uuid.UUID) ([]sqlc.NetworkPolicyApplication, error)
	ListApplicationsForTemplate(ctx context.Context, templateID uuid.UUID) ([]sqlc.NetworkPolicyApplication, error)
	GetNetworkPolicyApplicationByID(ctx context.Context, id uuid.UUID) (sqlc.NetworkPolicyApplication, error)
	GetNetworkPolicyApplicationByUnique(ctx context.Context, arg sqlc.GetNetworkPolicyApplicationByUniqueParams) (sqlc.NetworkPolicyApplication, error)
	CreateNetworkPolicyApplication(ctx context.Context, arg sqlc.CreateNetworkPolicyApplicationParams) (sqlc.NetworkPolicyApplication, error)
	DeleteNetworkPolicyApplication(ctx context.Context, id uuid.UUID) error
	MarkNetworkPolicyApplicationStatus(ctx context.Context, arg sqlc.MarkNetworkPolicyApplicationStatusParams) (sqlc.NetworkPolicyApplication, error)

	// Cluster + super-user lookups for body validation.
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
}

type NetworkPolicyMutationTx interface {
	NetworkPolicyQuerier
	UpsertNetworkPolicyApplication(context.Context, sqlc.UpsertNetworkPolicyApplicationParams) (sqlc.NetworkPolicyApplication, error)
	audit.OutboxQuerier
	tasks.TaskOutboxWriter
}

type networkPolicyRunTxFunc func(context.Context, func(NetworkPolicyMutationTx) error) error

func executeNetworkPolicyMutation[T any](r *http.Request, h *NetworkPolicyHandler, mutate func(NetworkPolicyMutationTx) (T, error), fallback func() (T, error), describe func(T) clusterAuditEvent) (T, error) {
	var zero T
	if h == nil {
		return zero, errors.New("network policy handler is nil")
	}
	if h.runTx != nil {
		var result T
		err := h.runTx(r.Context(), func(q NetworkPolicyMutationTx) error {
			var mutationErr error
			result, mutationErr = mutate(q)
			if mutationErr != nil {
				return mutationErr
			}
			event := describe(result)
			if event.action == "" {
				return nil
			}
			return recordAuditOutbox(r, q, event.action, event.resourceType, event.resourceID, event.resourceName, event.status, event.detail)
		})
		return result, err
	}
	result, err := fallback()
	if err != nil {
		return zero, err
	}
	event := describe(result)
	recordAudit(r, h.queries, event.action, event.resourceType, event.resourceID, event.resourceName, event.detail)
	return result, nil
}

// NetworkPolicyEnqueuer is the minimal asynq.Client surface used to fire
// network_policy:apply tasks from the handler.
type NetworkPolicyEnqueuer interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

// NetworkPolicyK8sRequester proxies in-cluster Kubernetes API calls
// through the tunnel — used inline by Delete to revoke the in-cluster
// NetworkPolicy alongside the DB row.
type NetworkPolicyK8sRequester interface {
	Do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (*protocol.K8sResponsePayload, error)
}

// NetworkPolicyHandler owns the admin + per-cluster routes.
type NetworkPolicyHandler struct {
	queries   NetworkPolicyQuerier
	runTx     networkPolicyRunTxFunc
	queue     NetworkPolicyEnqueuer
	requester NetworkPolicyK8sRequester
}

// NewNetworkPolicyHandler constructs the handler.
func NewNetworkPolicyHandler(q NetworkPolicyQuerier) *NetworkPolicyHandler {
	return &NetworkPolicyHandler{queries: q}
}

func (h *NetworkPolicyHandler) SetRunTx(runTx networkPolicyRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *NetworkPolicyHandler) TransactionalAuditWired() bool { return h != nil && h.runTx != nil }

// SetQueue wires the asynq client. Optional — when nil, the handler
// still writes the DB row but only the periodic sweep will pick it up.
func (h *NetworkPolicyHandler) SetQueue(q NetworkPolicyEnqueuer) {
	if h == nil {
		return
	}
	h.queue = q
}

// SetK8sRequester wires the tunnel requester used by Delete to revoke
// the in-cluster NetworkPolicy alongside the row. Optional — when nil,
// Delete leaves the in-cluster resource in place and the row goes away;
// the next reconciler tick would no-op (nothing to reconcile).
func (h *NetworkPolicyHandler) SetK8sRequester(r NetworkPolicyK8sRequester) {
	if h == nil {
		return
	}
	h.requester = r
}

// ────────────────────────────────────────────────────────────────────────
// Wire shapes
// ────────────────────────────────────────────────────────────────────────

type NetworkPolicyTemplateResponse struct {
	ID           string `json:"id"`
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Kind         string `json:"kind"`
	SpecTemplate string `json:"spec_template"`
	Enabled      bool   `json:"enabled"`
	CreatedBy    string `json:"created_by,omitempty"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

func networkPolicyTemplateToResponse(t sqlc.NetworkPolicyTemplate) NetworkPolicyTemplateResponse {
	resp := NetworkPolicyTemplateResponse{
		ID:           t.ID.String(),
		Slug:         t.Slug,
		Name:         t.Name,
		Description:  t.Description,
		Kind:         t.Kind,
		SpecTemplate: t.SpecTemplate,
		Enabled:      t.Enabled,
		CreatedAt:    t.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:    t.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if t.CreatedBy.Valid {
		resp.CreatedBy = uuid.UUID(t.CreatedBy.Bytes).String()
	}
	return resp
}

type NetworkPolicyApplicationResponse struct {
	ID            string `json:"id"`
	TemplateID    string `json:"template_id"`
	TemplateSlug  string `json:"template_slug,omitempty"`
	ClusterID     string `json:"cluster_id"`
	Namespace     string `json:"namespace"`
	PolicyName    string `json:"policy_name"`
	Status        string `json:"status"`
	LastAppliedAt string `json:"last_applied_at,omitempty"`
	LastError     string `json:"last_error,omitempty"`
	AppliedBy     string `json:"applied_by,omitempty"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

func networkPolicyApplicationToResponse(a sqlc.NetworkPolicyApplication, slug string) NetworkPolicyApplicationResponse {
	resp := NetworkPolicyApplicationResponse{
		ID:           a.ID.String(),
		TemplateID:   a.TemplateID.String(),
		TemplateSlug: slug,
		ClusterID:    a.ClusterID.String(),
		Namespace:    a.Namespace,
		PolicyName:   a.PolicyName,
		Status:       a.Status,
		LastError:    a.LastError,
		CreatedAt:    a.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:    a.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if a.LastAppliedAt.Valid {
		resp.LastAppliedAt = a.LastAppliedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
	}
	if a.AppliedBy.Valid {
		resp.AppliedBy = uuid.UUID(a.AppliedBy.Bytes).String()
	}
	return resp
}

// CreateNetworkPolicyTemplateRequest is the POST/PUT body.
// openapi:request CreateNetworkPolicyTemplateRequest
type CreateNetworkPolicyTemplateRequest struct {
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	SpecTemplate string `json:"spec_template"`
	Enabled      *bool  `json:"enabled,omitempty"`
	// CloneFrom lets the operator clone a builtin row into a custom
	// row in a single POST. When set, slug/spec_template defaults
	// are pulled from the source template (the new row gets a
	// "-copy" slug suffix unless an explicit Slug is provided).
	CloneFrom string `json:"clone_from,omitempty"`
}

// ApplyNetworkPolicyRequest is the POST body for creating an
// application. Accepts EITHER a single namespace OR a list, so the UI
// can bulk-apply to a multi-namespace selection in one POST.
// openapi:request ApplyNetworkPolicyRequest
type ApplyNetworkPolicyRequest struct {
	TemplateID string   `json:"template_id"`
	Namespace  string   `json:"namespace,omitempty"`
	Namespaces []string `json:"namespaces,omitempty"`
}

type networkPolicyApplyResult struct {
	applications []sqlc.NetworkPolicyApplication
	namespaces   []string
}

// ────────────────────────────────────────────────────────────────────────
// Validation
// ────────────────────────────────────────────────────────────────────────

// slugPattern restricts user-defined slugs to a safe subset. The
// reconciler embeds the slug in a Kubernetes object name, so we
// disallow uppercase, slashes, and any char that wouldn't pass DNS-1123.
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{0,62}$`)

// namespacePattern matches a Kubernetes DNS-1123 label, the canonical
// shape for a namespace name. Stricter than VARCHAR(253) on the column;
// catches typos at the API rather than at SSA time.
var namespacePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

func validateSlug(s string) error {
	if !slugPattern.MatchString(s) {
		return fmt.Errorf("slug must match %s", slugPattern.String())
	}
	return nil
}

func validateNamespace(ns string) error {
	if !namespacePattern.MatchString(ns) {
		return fmt.Errorf("namespace %q must be a valid DNS-1123 label", ns)
	}
	return nil
}

// ────────────────────────────────────────────────────────────────────────
// Template CRUD
// ────────────────────────────────────────────────────────────────────────

// ListTemplates handles GET /api/v1/admin/network-policy-templates/.
func (h *NetworkPolicyHandler) ListTemplates(w http.ResponseWriter, r *http.Request) {
	items, err := h.queries.ListNetworkPolicyTemplates(r.Context(), sqlc.ListNetworkPolicyTemplatesParams{
		Limit:  int32(queryLimit(r, 50)),
		Offset: int32(queryInt(r, "offset", 0)),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list network policy templates")
		return
	}
	total, _ := h.queries.CountNetworkPolicyTemplates(r.Context())
	resp := make([]NetworkPolicyTemplateResponse, 0, len(items))
	for _, t := range items {
		resp = append(resp, networkPolicyTemplateToResponse(t))
	}
	RespondPaginated(w, r, resp, total)
}

// GetTemplate handles GET /api/v1/admin/network-policy-templates/{id}/.
func (h *NetworkPolicyHandler) GetTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid template ID")
		return
	}
	t, err := h.queries.GetNetworkPolicyTemplateByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Network policy template not found")
		return
	}
	RespondJSON(w, http.StatusOK, networkPolicyTemplateToResponse(t))
}

// CreateTemplate handles POST /api/v1/admin/network-policy-templates/.
// New rows are always kind='custom' regardless of body — operators can't
// forge a 'builtin' row through this path. CloneFrom lets the operator
// bootstrap from a builtin slug.
func (h *NetworkPolicyHandler) CreateTemplate(w http.ResponseWriter, r *http.Request) {
	var req CreateNetworkPolicyTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	req.Slug = strings.TrimSpace(req.Slug)
	req.Name = strings.TrimSpace(req.Name)

	if req.CloneFrom != "" {
		src, err := h.queries.GetNetworkPolicyTemplateBySlug(r.Context(), req.CloneFrom)
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, fmt.Sprintf("clone_from slug %q not found", req.CloneFrom))
			return
		}
		if req.Slug == "" {
			req.Slug = src.Slug + "_copy"
		}
		if req.Name == "" {
			req.Name = src.Name + " (copy)"
		}
		if req.Description == "" {
			req.Description = src.Description
		}
		if req.SpecTemplate == "" {
			req.SpecTemplate = src.SpecTemplate
		}
	}

	if req.Slug == "" || req.Name == "" || req.SpecTemplate == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "slug, name, and spec_template are required")
		return
	}
	if err := validateSlug(req.Slug); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	// Smoke-test the template: render with a placeholder context so a
	// syntactically broken template can't reach the worker queue.
	if _, err := netpol.Render(req.SpecTemplate, netpol.Context{
		Namespace: "preview", Project: "preview", PolicyName: netpol.PolicyName(req.Slug),
	}); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, fmt.Sprintf("spec_template: %v", err))
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	params := sqlc.CreateNetworkPolicyTemplateParams{
		Slug:         req.Slug,
		Name:         req.Name,
		Description:  req.Description,
		Kind:         "custom",
		SpecTemplate: req.SpecTemplate,
		Enabled:      enabled,
		CreatedBy:    currentUserUUID(r),
	}
	tmpl, err := executeNetworkPolicyMutation(r, h,
		func(q NetworkPolicyMutationTx) (sqlc.NetworkPolicyTemplate, error) {
			return q.CreateNetworkPolicyTemplate(r.Context(), params)
		},
		func() (sqlc.NetworkPolicyTemplate, error) {
			return h.queries.CreateNetworkPolicyTemplate(r.Context(), params)
		},
		func(row sqlc.NetworkPolicyTemplate) clusterAuditEvent {
			return clusterAuditEvent{
				action: "admin.network_policy_template.created", resourceType: "network_policy_template",
				resourceID: row.ID.String(), resourceName: row.Name, status: http.StatusCreated,
				detail: map[string]any{"slug": row.Slug},
			}
		})
	if err != nil {
		if isUniqueViolation(err) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "A template with this slug already exists")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create network policy template")
		return
	}
	w.Header().Set("Location", "/api/v1/admin/network-policy-templates/"+tmpl.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, networkPolicyTemplateToResponse(tmpl))
}

// UpdateTemplate handles PUT /api/v1/admin/network-policy-templates/{id}/.
// Builtin rows are read-only: PUT returns 403.
func (h *NetworkPolicyHandler) UpdateTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid template ID")
		return
	}
	existing, err := h.queries.GetNetworkPolicyTemplateByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Network policy template not found")
		return
	}
	if existing.Kind == "builtin" {
		RespondRequestError(w, r, http.StatusForbidden, apierror.BuiltinReadonly, "Builtin templates are read-only; clone via POST to create an editable copy.")
		return
	}
	var req CreateNetworkPolicyTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.SpecTemplate) == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "name and spec_template are required")
		return
	}
	if _, err := netpol.Render(req.SpecTemplate, netpol.Context{
		Namespace: "preview", Project: "preview", PolicyName: netpol.PolicyName(existing.Slug),
	}); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, fmt.Sprintf("spec_template: %v", err))
		return
	}
	enabled := existing.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	params := sqlc.UpdateNetworkPolicyTemplateParams{
		ID:           id,
		Name:         req.Name,
		Description:  req.Description,
		SpecTemplate: req.SpecTemplate,
		Enabled:      enabled,
	}
	tmpl, err := executeNetworkPolicyMutation(r, h,
		func(q NetworkPolicyMutationTx) (sqlc.NetworkPolicyTemplate, error) {
			return q.UpdateNetworkPolicyTemplate(r.Context(), params)
		},
		func() (sqlc.NetworkPolicyTemplate, error) {
			return h.queries.UpdateNetworkPolicyTemplate(r.Context(), params)
		},
		func(row sqlc.NetworkPolicyTemplate) clusterAuditEvent {
			return clusterAuditEvent{
				action: "admin.network_policy_template.updated", resourceType: "network_policy_template",
				resourceID: row.ID.String(), resourceName: row.Name, status: http.StatusOK,
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.UpdateError, "Failed to update network policy template")
		return
	}
	RespondJSON(w, http.StatusOK, networkPolicyTemplateToResponse(tmpl))
}

// DeleteTemplate handles DELETE /api/v1/admin/network-policy-templates/{id}/.
// Builtin rows are read-only: DELETE returns 403. Applications cascade
// via FK ON DELETE CASCADE — the operator should detach those bindings
// explicitly before deleting if they want the in-cluster NetworkPolicy
// also revoked. We document this in the response body.
func (h *NetworkPolicyHandler) DeleteTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid template ID")
		return
	}
	existing, err := h.queries.GetNetworkPolicyTemplateByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Network policy template not found")
		return
	}
	if existing.Kind == "builtin" {
		RespondRequestError(w, r, http.StatusForbidden, apierror.BuiltinReadonly, "Builtin templates are read-only and cannot be deleted.")
		return
	}
	_, err = executeNetworkPolicyMutation(r, h,
		func(q NetworkPolicyMutationTx) (struct{}, error) {
			return struct{}{}, q.DeleteNetworkPolicyTemplate(r.Context(), id)
		},
		func() (struct{}, error) {
			return struct{}{}, h.queries.DeleteNetworkPolicyTemplate(r.Context(), id)
		},
		func(struct{}) clusterAuditEvent {
			return clusterAuditEvent{
				action: "admin.network_policy_template.deleted", resourceType: "network_policy_template",
				resourceID: existing.ID.String(), resourceName: existing.Name, status: http.StatusNoContent,
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete network policy template")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ────────────────────────────────────────────────────────────────────────
// Per-cluster applications
// ────────────────────────────────────────────────────────────────────────

// ListApplications handles GET /api/v1/clusters/{cluster_id}/network-policies/applications/.
func (h *NetworkPolicyHandler) ListApplications(w http.ResponseWriter, r *http.Request) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	apps, err := h.queries.ListApplicationsForCluster(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list network policy applications")
		return
	}
	// Hydrate template slugs in one pass for the common case where many
	// rows share the same template — avoids N round-trips.
	slugByID := map[uuid.UUID]string{}
	resp := make([]NetworkPolicyApplicationResponse, 0, len(apps))
	for _, a := range apps {
		slug, ok := slugByID[a.TemplateID]
		if !ok {
			if t, err := h.queries.GetNetworkPolicyTemplateByID(r.Context(), a.TemplateID); err == nil {
				slug = t.Slug
				slugByID[a.TemplateID] = slug
			}
		}
		resp = append(resp, networkPolicyApplicationToResponse(a, slug))
	}
	// ListApplicationsForCluster returns the full per-cluster set in one
	// shot (no SQL limit/offset), so the page is the whole result and the
	// total is its exact length. Add pagination if
	// per-cluster application counts ever grow unbounded.
	RespondList(w, resp, NewPagination(len(resp), len(resp), 0, len(resp)))
}

// CreateApplications handles POST /api/v1/clusters/{cluster_id}/network-policies/applications/.
// Accepts a single namespace OR a list (bulk-apply). The complete batch, one
// durable network_policy:apply task per namespace, and the audit intent commit
// atomically. Existing rows are reset to pending, making retries idempotent.
func (h *NetworkPolicyHandler) CreateApplications(w http.ResponseWriter, r *http.Request) {
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "network policy transaction runner is not configured")
		return
	}
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	var req ApplyNetworkPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	templateID, err := uuid.Parse(req.TemplateID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid template_id")
		return
	}
	tmpl, err := h.queries.GetNetworkPolicyTemplateByID(r.Context(), templateID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Network policy template not found")
		return
	}
	if !tmpl.Enabled {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.TemplateDisabled, "Network policy template is disabled")
		return
	}

	namespaces := append([]string{}, req.Namespaces...)
	if req.Namespace != "" {
		namespaces = append(namespaces, req.Namespace)
	}
	if len(namespaces) == 0 {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "namespace or namespaces is required")
		return
	}
	for _, ns := range namespaces {
		if err := validateNamespace(ns); err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
			return
		}
	}
	r = r.WithContext(withOperationIdempotency(r, "network_policy_apply"))
	digest, err := canonicalOperationRequestDigest(struct {
		ClusterID  string   `json:"cluster_id"`
		TemplateID string   `json:"template_id"`
		Namespaces []string `json:"namespaces"`
	}{ClusterID: clusterID.String(), TemplateID: tmpl.ID.String(), Namespaces: namespaces})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncodeError, "Failed to encode network policy request")
		return
	}
	var receipt NetworkPolicyApplicationsReceipt
	replayed := false

	_, err = executeNetworkPolicyMutation(r, h,
		func(q NetworkPolicyMutationTx) (networkPolicyApplyResult, error) {
			idemQ, ok := q.(resourceOperationIdempotencyQuerier)
			if !ok {
				return networkPolicyApplyResult{}, errors.New("network policy idempotency store is not configured")
			}
			_, stored, replay, claimErr := claimOperationReceipt[NetworkPolicyApplicationsReceipt](r.Context(), idemQ, "network_policy_applications", digest)
			if claimErr != nil {
				return networkPolicyApplyResult{}, claimErr
			}
			if replay {
				receipt, replayed = stored, true
				return networkPolicyApplyResult{}, nil
			}
			result := networkPolicyApplyResult{
				applications: make([]sqlc.NetworkPolicyApplication, 0, len(namespaces)),
				namespaces:   make([]string, 0, len(namespaces)),
			}
			for _, ns := range namespaces {
				app, upsertErr := q.UpsertNetworkPolicyApplication(r.Context(), sqlc.UpsertNetworkPolicyApplicationParams{
					TemplateID: tmpl.ID, ClusterID: clusterID, Namespace: ns,
					PolicyName: netpol.PolicyName(tmpl.Slug), AppliedBy: currentUserUUID(r),
				})
				if upsertErr != nil {
					return networkPolicyApplyResult{}, upsertErr
				}
				if taskErr := enqueueNetworkPolicyApplyTaskOutbox(r, q, app.ID); taskErr != nil {
					return networkPolicyApplyResult{}, taskErr
				}
				result.applications = append(result.applications, app)
				result.namespaces = append(result.namespaces, ns)
			}
			created := make([]NetworkPolicyApplicationResponse, 0, len(result.applications))
			for _, application := range result.applications {
				created = append(created, networkPolicyApplicationToResponse(application, tmpl.Slug))
			}
			receipt = NetworkPolicyApplicationsReceipt{ClusterID: clusterID.String(), Status: "pending", Applications: created}
			if len(result.applications) == 0 {
				return networkPolicyApplyResult{}, errors.New("network policy application batch is empty")
			}
			if attachErr := attachOperationReceipt(r.Context(), idemQ, "network_policy_applications", result.applications[0].ID, digest, receipt); attachErr != nil {
				return networkPolicyApplyResult{}, attachErr
			}
			return result, nil
		},
		func() (networkPolicyApplyResult, error) {
			result := networkPolicyApplyResult{
				applications: make([]sqlc.NetworkPolicyApplication, 0, len(namespaces)),
				namespaces:   make([]string, 0, len(namespaces)),
			}
			for _, ns := range namespaces {
				app, createErr := h.queries.CreateNetworkPolicyApplication(r.Context(), sqlc.CreateNetworkPolicyApplicationParams{
					TemplateID: tmpl.ID, ClusterID: clusterID, Namespace: ns,
					PolicyName: netpol.PolicyName(tmpl.Slug), AppliedBy: currentUserUUID(r),
				})
				if createErr != nil {
					if !isUniqueViolation(createErr) {
						continue
					}
					app, createErr = h.queries.GetNetworkPolicyApplicationByUnique(r.Context(), sqlc.GetNetworkPolicyApplicationByUniqueParams{
						ClusterID: clusterID, Namespace: ns, TemplateID: tmpl.ID,
					})
					if createErr != nil {
						continue
					}
				}
				h.enqueueApply(r, app.ID)
				result.applications = append(result.applications, app)
				result.namespaces = append(result.namespaces, ns)
			}
			return result, nil
		},
		func(result networkPolicyApplyResult) clusterAuditEvent {
			if replayed {
				return clusterAuditEvent{}
			}
			return clusterAuditEvent{
				action: "cluster.network_policy.applied", resourceType: "cluster",
				resourceID: clusterID.String(), resourceName: cluster.Name, status: http.StatusAccepted,
				detail: map[string]any{
					"template_id": tmpl.ID.String(), "template_slug": tmpl.Slug,
					"namespaces": result.namespaces,
				},
			}
		})
	if err != nil {
		if errors.Is(err, errOperationIdempotencyConflict) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key already identifies a different network policy application request")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.ApplyError, "Failed to apply network policy template")
		return
	}
	RespondAcceptedOperation(w, "/api/v1/clusters/"+clusterID.String()+"/network-policies/applications/", receipt)
}

type NetworkPolicyApplicationsReceipt struct {
	ClusterID    string                             `json:"cluster_id"`
	Status       string                             `json:"status"`
	Applications []NetworkPolicyApplicationResponse `json:"applications"`
}

// DeleteApplication handles DELETE /api/v1/clusters/{cluster_id}/network-policies/applications/{id}/.
// Reverts the in-cluster NetworkPolicy and removes the DB row. If the
// in-cluster delete fails, the row is left in status='failed' so the
// operator sees the cause and can manually clean up.
func (h *NetworkPolicyHandler) DeleteApplication(w http.ResponseWriter, r *http.Request) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	appID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid application ID")
		return
	}
	app, err := h.queries.GetNetworkPolicyApplicationByID(r.Context(), appID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Network policy application not found")
		return
	}
	if app.ClusterID != clusterID {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Network policy application not found for this cluster")
		return
	}
	// Revoke the in-cluster NetworkPolicy first. On failure we KEEP the
	// row and mark it 'failed' so the operator can investigate without
	// losing track of which template was applied where.
	if h.requester != nil {
		if err := tasks.DeleteNetworkPolicyInCluster(r.Context(), h.requester, clusterID, app.Namespace, app.PolicyName); err != nil {
			params := sqlc.MarkNetworkPolicyApplicationStatusParams{
				ID:           app.ID,
				Status:       "failed",
				LastError:    fmt.Sprintf("revoke in-cluster: %v", err),
				TouchApplied: false,
			}
			_, persistErr := executeNetworkPolicyMutation(r, h,
				func(q NetworkPolicyMutationTx) (sqlc.NetworkPolicyApplication, error) {
					return q.MarkNetworkPolicyApplicationStatus(r.Context(), params)
				},
				func() (sqlc.NetworkPolicyApplication, error) {
					return h.queries.MarkNetworkPolicyApplicationStatus(r.Context(), params)
				},
				func(row sqlc.NetworkPolicyApplication) clusterAuditEvent {
					return clusterAuditEvent{
						action: "cluster.network_policy.revert_failed", resourceType: "cluster",
						resourceID: clusterID.String(), resourceName: cluster.Name, status: http.StatusInternalServerError,
						detail: map[string]any{"application_id": row.ID.String(), "namespace": row.Namespace, "policy_name": row.PolicyName},
					}
				})
			if persistErr != nil {
				respondTransactionalMutationError(w, r, persistErr, http.StatusInternalServerError, apierror.RevokeError, "Failed to persist network policy revocation failure")
				return
			}
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.RevokeError, fmt.Sprintf("Failed to revoke in-cluster NetworkPolicy: %v", err))
			return
		}
	}
	_, err = executeNetworkPolicyMutation(r, h,
		func(q NetworkPolicyMutationTx) (struct{}, error) {
			return struct{}{}, q.DeleteNetworkPolicyApplication(r.Context(), app.ID)
		},
		func() (struct{}, error) {
			return struct{}{}, h.queries.DeleteNetworkPolicyApplication(r.Context(), app.ID)
		},
		func(struct{}) clusterAuditEvent {
			return clusterAuditEvent{
				action: "cluster.network_policy.reverted", resourceType: "cluster",
				resourceID: clusterID.String(), resourceName: cluster.Name, status: http.StatusNoContent,
				detail: map[string]any{
					"application_id": app.ID.String(), "namespace": app.Namespace, "policy_name": app.PolicyName,
				},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete network policy application")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Reapply handles POST /api/v1/clusters/{cluster_id}/network-policies/applications/{id}/reapply/.
// Resets the row to status='pending' and re-fires the apply task.
func (h *NetworkPolicyHandler) Reapply(w http.ResponseWriter, r *http.Request) {
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "network policy transaction runner is not configured")
		return
	}
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	appID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid application ID")
		return
	}
	app, err := h.queries.GetNetworkPolicyApplicationByID(r.Context(), appID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Network policy application not found")
		return
	}
	if app.ClusterID != clusterID {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Network policy application not found for this cluster")
		return
	}
	params := sqlc.MarkNetworkPolicyApplicationStatusParams{
		ID:           app.ID,
		Status:       "pending",
		LastError:    "",
		TouchApplied: false,
	}
	r = r.WithContext(withOperationIdempotency(r, "network_policy_reapply"))
	digest, err := canonicalOperationRequestDigest(struct {
		ClusterID     string `json:"cluster_id"`
		ApplicationID string `json:"application_id"`
	}{ClusterID: clusterID.String(), ApplicationID: app.ID.String()})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncodeError, "Failed to encode network policy reapply request")
		return
	}
	var receipt NetworkPolicyApplicationResponse
	replayed := false
	app, err = executeNetworkPolicyMutation(r, h,
		func(q NetworkPolicyMutationTx) (sqlc.NetworkPolicyApplication, error) {
			idemQ, ok := q.(resourceOperationIdempotencyQuerier)
			if !ok {
				return sqlc.NetworkPolicyApplication{}, errors.New("network policy idempotency store is not configured")
			}
			_, stored, replay, claimErr := claimOperationReceipt[NetworkPolicyApplicationResponse](r.Context(), idemQ, "network_policy_reapplications", digest)
			if claimErr != nil {
				return sqlc.NetworkPolicyApplication{}, claimErr
			}
			if replay {
				receipt, replayed = stored, true
				return sqlc.NetworkPolicyApplication{}, nil
			}
			row, updateErr := q.MarkNetworkPolicyApplicationStatus(r.Context(), params)
			if updateErr != nil {
				return sqlc.NetworkPolicyApplication{}, updateErr
			}
			if taskErr := enqueueNetworkPolicyApplyTaskOutbox(r, q, row.ID); taskErr != nil {
				return sqlc.NetworkPolicyApplication{}, taskErr
			}
			slug := ""
			if template, lookupErr := h.queries.GetNetworkPolicyTemplateByID(r.Context(), row.TemplateID); lookupErr == nil {
				slug = template.Slug
			}
			receipt = networkPolicyApplicationToResponse(row, slug)
			if attachErr := attachOperationReceipt(r.Context(), idemQ, "network_policy_reapplications", row.ID, digest, receipt); attachErr != nil {
				return sqlc.NetworkPolicyApplication{}, attachErr
			}
			return row, nil
		},
		func() (sqlc.NetworkPolicyApplication, error) {
			row, updateErr := h.queries.MarkNetworkPolicyApplicationStatus(r.Context(), params)
			if updateErr == nil {
				h.enqueueApply(r, row.ID)
			}
			return row, updateErr
		},
		func(row sqlc.NetworkPolicyApplication) clusterAuditEvent {
			if replayed {
				return clusterAuditEvent{}
			}
			return clusterAuditEvent{
				action: "cluster.network_policy.reapplied", resourceType: "cluster",
				resourceID: clusterID.String(), status: http.StatusAccepted,
				detail: map[string]any{"application_id": row.ID.String(), "namespace": row.Namespace, "policy_name": row.PolicyName},
			}
		})
	if err != nil {
		if errors.Is(err, errOperationIdempotencyConflict) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key already identifies a different network policy reapply request")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.ReapplyError, "Failed to reset application status")
		return
	}
	RespondAcceptedOperation(w, "/api/v1/clusters/"+clusterID.String()+"/network-policies/applications/", receipt)
}

// ────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────

// enqueueApply fires a network_policy:apply task. Best-effort; the
// reconciler will eventually pick up pending rows via the periodic sweep
// even when no queue is wired.
func newNetworkPolicyApplyTask(r *http.Request, applicationID uuid.UUID) (*asynq.Task, error) {
	task, err := tasks.NewNetworkPolicyApplyTask(applicationID)
	if err != nil {
		return nil, err
	}
	payload := observability.EnrichTaskPayload(r.Context(), task.Payload(), middleware.GetCorrelationID(r.Context()))
	return asynq.NewTask(task.Type(), payload, asynq.MaxRetry(3)), nil
}

func enqueueNetworkPolicyApplyTaskOutbox(r *http.Request, q tasks.TaskOutboxWriter, applicationID uuid.UUID) error {
	task, err := newNetworkPolicyApplyTask(r, applicationID)
	if err != nil {
		return err
	}
	dedupeKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if dedupeKey == "" {
		dedupeKey = uuid.NewString()
	}
	_, err = tasks.EnqueueTaskOutbox(r.Context(), q, task, tasks.TaskOutboxOptions{
		DedupeKey:           "network_policy_apply:" + audit.MutationDedupeKey(dedupeKey, r.Method+":"+r.URL.Path, "network_policy_application", applicationID.String()),
		QueueName:           tasks.ClusterTemplateApplyQueueName,
		MaxRetry:            3,
		MaxDeliveryAttempts: 20,
	})
	return err
}

func (h *NetworkPolicyHandler) enqueueApply(r *http.Request, applicationID uuid.UUID) {
	if h == nil || h.queue == nil {
		return
	}
	task, err := newNetworkPolicyApplyTask(r, applicationID)
	if err != nil {
		return
	}
	_, _ = h.queue.Enqueue(task, asynq.Queue(tasks.ClusterTemplateApplyQueueName))
}
