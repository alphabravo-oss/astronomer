// Cluster templates (migration 049).
//
// Templates package the manual cluster onboarding flow (environment +
// labels + tool installs + a default project + a token rotation policy)
// into operator-defined "Production Web App" style presets. Applying a
// template to a cluster is async + idempotent: the handler upserts a
// cluster_template_applications row with status='pending' and enqueues
// a cluster_template:apply task; the worker walks the spec and converges
// the cluster to match.
//
// The handler validates the spec JSONB at create/update time:
//   - top-level keys are restricted to a known set (unknown keys -> 400)
//   - environment is one of "production"|"staging"|"development"
//   - default_project.pod_security_profile is one of
//     "privileged"|"baseline"|"restricted"
//   - registration_policy.token_rotation_days is non-negative
//
// Everything else (label k/v shapes, tools[].slug existence, project
// quota strings) is validated at apply time by the worker — the spec
// stays expressive enough that future fields don't need a handler
// change to flow through.

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	avault "github.com/alphabravocompany/astronomer-go/internal/vault"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

// ClusterTemplateQuerier is the database surface the handler needs. The
// production *sqlc.Queries satisfies it; tests stand up a narrow fake.
type ClusterTemplateQuerier interface {
	// Template CRUD.
	ListClusterTemplates(ctx context.Context, arg sqlc.ListClusterTemplatesParams) ([]sqlc.ClusterTemplate, error)
	CountClusterTemplates(ctx context.Context) (int64, error)
	GetClusterTemplateByID(ctx context.Context, id uuid.UUID) (sqlc.ClusterTemplate, error)
	GetClusterTemplateByName(ctx context.Context, name string) (sqlc.ClusterTemplate, error)
	CreateClusterTemplate(ctx context.Context, arg sqlc.CreateClusterTemplateParams) (sqlc.ClusterTemplate, error)
	UpdateClusterTemplate(ctx context.Context, arg sqlc.UpdateClusterTemplateParams) (sqlc.ClusterTemplate, error)
	DeleteClusterTemplate(ctx context.Context, id uuid.UUID) error
	CountClusterTemplateApplicationsByTemplate(ctx context.Context, templateID uuid.UUID) (int64, error)

	// Application + status surface.
	GetClusterTemplateApplication(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterTemplateApplication, error)
	UpsertClusterTemplateApplication(ctx context.Context, arg sqlc.UpsertClusterTemplateApplicationParams) (sqlc.ClusterTemplateApplication, error)
	MarkClusterTemplateApplicationStatus(ctx context.Context, arg sqlc.MarkClusterTemplateApplicationStatusParams) (sqlc.ClusterTemplateApplication, error)
	DeleteClusterTemplateApplication(ctx context.Context, clusterID uuid.UUID) error

	// Cluster existence check for the bind endpoints.
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)

	// Registration policy detach when the operator unbinds a template
	// that stamped one.
	DeleteClusterRegistrationPolicy(ctx context.Context, clusterID uuid.UUID) error
}

// ClusterTemplateEnqueuer is the minimal asynq.Client surface used to
// schedule cluster_template:apply tasks. Mirrors the pattern from
// ClusterDecommissionEnqueuer in clusters.go.
type ClusterTemplateEnqueuer interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

// ClusterTemplateHandler owns /api/v1/cluster-templates/* and the per-
// cluster /api/v1/clusters/{cluster_id}/template/* endpoints.
type ClusterTemplateHandler struct {
	queries ClusterTemplateQuerier
	bus     *events.Bus
	// queue is the asynq client used to schedule apply tasks. Optional —
	// nil-safe so tests can drive the handler without a Redis-backed
	// asynq.Client. When nil, the handler still upserts the application
	// row but the worker only picks it up via the periodic sweep.
	queue      ClusterTemplateEnqueuer
	taskOutbox tasks.TaskOutboxWriter
	// maintenanceGate is the migration-057 hook on cluster_template.apply.
	// Optional + nil-safe.
	maintenanceGate *MaintenanceGate
	// vaultResolver pre-flights ${vault://...} references in the
	// template spec at Apply time. The spec snapshot is NEVER mutated
	// with resolved values — that would persist cleartext secrets in
	// the DB; the worker re-resolves at install time. Migration 067.
	vaultResolver *avault.Resolver
}

// SetVaultResolver wires the Vault resolver used to pre-flight
// ${vault://...} references in template specs at Apply time.
func (h *ClusterTemplateHandler) SetVaultResolver(r *avault.Resolver) {
	if h == nil {
		return
	}
	h.vaultResolver = r
}

// NewClusterTemplateHandler constructs the handler.
func NewClusterTemplateHandler(queries ClusterTemplateQuerier) *ClusterTemplateHandler {
	return &ClusterTemplateHandler{queries: queries}
}

// SetQueue wires the asynq client used to enqueue apply tasks. Optional;
// when not wired, applies still write the pending row but rely on the
// periodic worker sweep to converge.
// SetEventBus wires the SSE bus for template_binding.changed liveness
// events (P4.5). Optional: fire-and-forget and nil-safe.
func (h *ClusterTemplateHandler) SetEventBus(bus *events.Bus) {
	if h == nil {
		return
	}
	h.bus = bus
}

// publishTemplateBindingChanged emits the metadata-only
// template_binding.changed event after a successful application-row write.
func (h *ClusterTemplateHandler) publishTemplateBindingChanged(clusterID uuid.UUID, status string) {
	if h == nil {
		return
	}
	extra := map[string]any{}
	if status != "" {
		extra["status"] = status
	}
	events.PublishChanged(h.bus, "template_binding", clusterID.String(), clusterID.String(), extra)
}

func (h *ClusterTemplateHandler) SetQueue(q ClusterTemplateEnqueuer) {
	if h == nil {
		return
	}
	h.queue = q
}

// SetTaskOutbox wires the durable task outbox used before direct Redis enqueue.
// Optional and nil-safe.
func (h *ClusterTemplateHandler) SetTaskOutbox(q tasks.TaskOutboxWriter) {
	if h == nil {
		return
	}
	h.taskOutbox = q
}

// SetMaintenanceGate wires the migration-057 gate that refuses or
// defers cluster_template.apply during an active maintenance window.
func (h *ClusterTemplateHandler) SetMaintenanceGate(g *MaintenanceGate) {
	if h == nil {
		return
	}
	h.maintenanceGate = g
}

// Status constants for cluster_template_applications.status. Kept in
// lockstep with the worker's transitions.
const (
	ClusterTemplateStatusPending  = "pending"
	ClusterTemplateStatusApplying = "applying"
	ClusterTemplateStatusApplied  = "applied"
	ClusterTemplateStatusFailed   = "failed"
)

// ClusterTemplateResponse is the wire shape returned by the list/get/
// create/update endpoints.
type ClusterTemplateResponse struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Spec        json.RawMessage `json:"spec"`
	CreatedBy   string          `json:"created_by,omitempty"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

func templateToResponse(t sqlc.ClusterTemplate) ClusterTemplateResponse {
	resp := ClusterTemplateResponse{
		ID:          t.ID.String(),
		Name:        t.Name,
		Description: t.Description,
		Spec:        t.Spec,
		CreatedAt:   t.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:   t.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if t.CreatedBy.Valid {
		resp.CreatedBy = uuid.UUID(t.CreatedBy.Bytes).String()
	}
	return resp
}

// ClusterTemplateApplicationResponse is the wire shape for the
// /clusters/{id}/template/ GET status endpoint.
type ClusterTemplateApplicationResponse struct {
	ClusterID    string          `json:"cluster_id"`
	TemplateID   string          `json:"template_id"`
	TemplateName string          `json:"template_name,omitempty"`
	Status       string          `json:"status"`
	SpecSnapshot json.RawMessage `json:"spec_snapshot"`
	LastError    string          `json:"last_error,omitempty"`
	AppliedAt    string          `json:"applied_at,omitempty"`
	CreatedAt    string          `json:"created_at"`
	UpdatedAt    string          `json:"updated_at"`
	// Drift is filled in only by the drift-check task; the GET endpoint
	// reports the cached value. Empty string means "not yet evaluated".
	// Possible values: "synced" | "drift" | "".
	Drift string `json:"drift,omitempty"`
}

func applicationToResponse(a sqlc.ClusterTemplateApplication, templateName string) ClusterTemplateApplicationResponse {
	resp := ClusterTemplateApplicationResponse{
		ClusterID:    a.ClusterID.String(),
		TemplateID:   a.TemplateID.String(),
		TemplateName: templateName,
		Status:       a.Status,
		SpecSnapshot: a.SpecSnapshot,
		LastError:    a.LastError,
		CreatedAt:    a.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:    a.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if a.AppliedAt.Valid {
		resp.AppliedAt = a.AppliedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
	}
	return resp
}

// CreateClusterTemplateRequest is the POST/PUT body shape.
type CreateClusterTemplateRequest struct {
	Name        string          `json:"name" validate:"required"`
	Description string          `json:"description"`
	Spec        json.RawMessage `json:"spec"`
}

// ApplyClusterTemplateRequest is the POST /clusters/{id}/template/ body.
type ApplyClusterTemplateRequest struct {
	TemplateID string `json:"template_id" validate:"required"`
}

// ────────────────────────────────────────────────────────────────────────
// Spec validation
// ────────────────────────────────────────────────────────────────────────

// validTemplateTopKeys are the only top-level keys the spec is allowed
// to contain. Anything else is rejected at write time so a typo
// (e.g. "registration_polciy") doesn't silently fail to apply.
var validTemplateTopKeys = map[string]struct{}{
	"environment":         {},
	"labels":              {},
	"tools":               {},
	"default_project":     {},
	"registration_policy": {},
}

var validTemplateEnvironments = map[string]struct{}{
	"production":  {},
	"staging":     {},
	"development": {},
}

// validPodSecurityProfiles is defined in projects.go — reuse the same
// closed enum so a template's spec validation stays in lockstep with
// the per-project policy validation.

// validateTemplateSpec returns nil when the spec is a syntactically and
// enum-wise valid template body. It deliberately does NOT enforce that
// referenced tool slugs or chart presets actually exist — that's done at
// apply time by the worker so an operator can stage a template
// pre-catalog-sync without an order-of-operations footgun.
func validateTemplateSpec(raw json.RawMessage) error {
	if len(raw) == 0 {
		// Empty spec is a valid no-op template.
		return nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return fmt.Errorf("spec must be a JSON object: %w", err)
	}
	for k := range top {
		if _, ok := validTemplateTopKeys[k]; !ok {
			return fmt.Errorf("unknown spec key %q (allowed: environment, labels, tools, default_project, registration_policy)", k)
		}
	}
	if envRaw, ok := top["environment"]; ok {
		var env string
		if err := json.Unmarshal(envRaw, &env); err != nil {
			return fmt.Errorf("environment must be a string")
		}
		if _, ok := validTemplateEnvironments[env]; !ok {
			return fmt.Errorf("environment must be production|staging|development, got %q", env)
		}
	}
	if labelsRaw, ok := top["labels"]; ok {
		var labels map[string]string
		if err := json.Unmarshal(labelsRaw, &labels); err != nil {
			return fmt.Errorf("labels must be an object of string->string")
		}
	}
	if toolsRaw, ok := top["tools"]; ok {
		var tools []map[string]any
		if err := json.Unmarshal(toolsRaw, &tools); err != nil {
			return fmt.Errorf("tools must be an array of {slug, preset, values}")
		}
		for i, t := range tools {
			slug, _ := t["slug"].(string)
			if strings.TrimSpace(slug) == "" {
				return fmt.Errorf("tools[%d].slug is required", i)
			}
		}
	}
	if dpRaw, ok := top["default_project"]; ok {
		var dp map[string]any
		if err := json.Unmarshal(dpRaw, &dp); err != nil {
			return fmt.Errorf("default_project must be an object")
		}
		if name, _ := dp["name"].(string); strings.TrimSpace(name) == "" {
			return fmt.Errorf("default_project.name is required")
		}
		if pss, ok := dp["pod_security_profile"].(string); ok && pss != "" {
			if _, ok := validPodSecurityProfiles[pss]; !ok {
				return fmt.Errorf("default_project.pod_security_profile must be privileged|baseline|restricted")
			}
		}
	}
	if rpRaw, ok := top["registration_policy"]; ok {
		var rp map[string]any
		if err := json.Unmarshal(rpRaw, &rp); err != nil {
			return fmt.Errorf("registration_policy must be an object")
		}
		if days, ok := rp["token_rotation_days"]; ok {
			f, ok := days.(float64)
			if !ok || f < 0 {
				return fmt.Errorf("registration_policy.token_rotation_days must be a non-negative integer")
			}
		}
	}
	return nil
}

// ────────────────────────────────────────────────────────────────────────
// Template CRUD
// ────────────────────────────────────────────────────────────────────────

// List handles GET /api/v1/cluster-templates/.
func (h *ClusterTemplateHandler) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.queries.ListClusterTemplates(r.Context(), sqlc.ListClusterTemplatesParams{
		Limit:  int32(queryLimit(r, 20)),
		Offset: int32(queryInt(r, "offset", 0)),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list cluster templates")
		return
	}
	total, _ := h.queries.CountClusterTemplates(r.Context())
	resp := make([]ClusterTemplateResponse, 0, len(items))
	for _, t := range items {
		resp = append(resp, templateToResponse(t))
	}
	RespondPaginated(w, r, resp, total)
}

// Get handles GET /api/v1/cluster-templates/{id}/.
func (h *ClusterTemplateHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid template ID")
		return
	}
	tmpl, err := h.queries.GetClusterTemplateByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster template not found")
		return
	}
	RespondJSON(w, http.StatusOK, templateToResponse(tmpl))
}

// Create handles POST /api/v1/cluster-templates/.
func (h *ClusterTemplateHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateClusterTemplateRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "name is required")
		return
	}
	// T6.074 — operators may not create a template with one of the
	// reserved platform-baseline names; the platform owns those.
	if isBuiltinTemplate(req.Name) {
		RespondRequestError(w, r, http.StatusForbidden, apierror.BuiltinTemplate,
			fmt.Sprintf("%q is a reserved platform-baseline template name.", req.Name))

		return
	}
	spec := req.Spec
	if len(spec) == 0 {
		spec = json.RawMessage(`{}`)
	}
	if err := validateTemplateSpec(spec); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}

	tmpl, err := h.queries.CreateClusterTemplate(r.Context(), sqlc.CreateClusterTemplateParams{
		Name:        req.Name,
		Description: req.Description,
		Spec:        spec,
		CreatedBy:   currentUserUUID(r),
	})
	if err != nil {
		// Unique-name conflict on cluster_templates_name_key bubbles up as
		// a 23505. Translate so the UI sees a clean 409 rather than 500.
		if isUniqueViolation(err) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "A template with this name already exists")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create cluster template")
		return
	}
	recordAudit(r, h.queries, "admin.cluster_template.created", "cluster_template", tmpl.ID.String(), tmpl.Name, map[string]any{
		"description": tmpl.Description,
	})
	w.Header().Set("Location", "/api/v1/cluster-templates/"+tmpl.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, templateToResponse(tmpl))
}

// Update handles PUT /api/v1/cluster-templates/{id}/.
func (h *ClusterTemplateHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid template ID")
		return
	}
	// T6.074 — refuse mutations to platform-baseline templates so an
	// upgrade doesn't have to handle a half-renamed builtin. The row
	// is loaded here (not inside UpdateClusterTemplate) so we can
	// pre-empt the SQL UPDATE and return a clean 403.
	if existing, gerr := h.queries.GetClusterTemplateByID(r.Context(), id); gerr == nil {
		if isBuiltinTemplate(existing.Name) {
			RespondRequestError(w, r, http.StatusForbidden, apierror.BuiltinTemplate,
				fmt.Sprintf("%q is a platform-baseline template and cannot be edited.", existing.Name))

			return
		}
	}
	var req CreateClusterTemplateRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "name is required")
		return
	}
	// Also refuse renaming AWAY from a builtin (defence in depth — the
	// existing.Name check above catches renaming a builtin; this catches
	// renaming a non-builtin TO a reserved name).
	if isBuiltinTemplate(req.Name) {
		RespondRequestError(w, r, http.StatusForbidden, apierror.BuiltinTemplate,
			fmt.Sprintf("%q is a reserved platform-baseline template name.", req.Name))

		return
	}
	spec := req.Spec
	if len(spec) == 0 {
		spec = json.RawMessage(`{}`)
	}
	if err := validateTemplateSpec(spec); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	tmpl, err := h.queries.UpdateClusterTemplate(r.Context(), sqlc.UpdateClusterTemplateParams{
		ID:          id,
		Name:        req.Name,
		Description: req.Description,
		Spec:        spec,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster template not found")
			return
		}
		if isUniqueViolation(err) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "A template with this name already exists")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update cluster template")
		return
	}
	recordAudit(r, h.queries, "admin.cluster_template.updated", "cluster_template", tmpl.ID.String(), tmpl.Name, nil)
	RespondJSON(w, http.StatusOK, templateToResponse(tmpl))
}

// isBuiltinTemplate returns true for the well-known templates the
// platform ships and reconciles on its own. Operators are allowed to
// reapply them, inspect them, and bind them to clusters — but Update
// and Delete are refused so an upgrade doesn't have to deal with a
// half-renamed or missing baseline template. The set is small and
// closed; extending it requires a code change. (T6.074)
func isBuiltinTemplate(name string) bool {
	switch name {
	case "platform-baseline", "platform-default":
		return true
	}
	return false
}

// Delete handles DELETE /api/v1/cluster-templates/{id}/. Refuses to
// remove a template that's still applied to at least one cluster — the
// operator must detach those bindings first. We do the count-first check
// (instead of relying on the FK violation) so the 409 body can include
// the exact reason without parsing pgconn error codes.
func (h *ClusterTemplateHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid template ID")
		return
	}
	tmpl, err := h.queries.GetClusterTemplateByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster template not found")
		return
	}
	if isBuiltinTemplate(tmpl.Name) {
		RespondRequestError(w, r, http.StatusForbidden, apierror.BuiltinTemplate,
			fmt.Sprintf("%q is a platform-baseline template and cannot be deleted.", tmpl.Name))

		return
	}
	count, err := h.queries.CountClusterTemplateApplicationsByTemplate(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.LookupError, "Failed to count template applications")
		return
	}
	if count > 0 {
		RespondRequestError(w, r, http.StatusConflict, apierror.TemplateInUse,
			fmt.Sprintf("Template is applied to %d cluster(s); detach it from those clusters before deleting.", count))

		return
	}
	if err := h.queries.DeleteClusterTemplate(r.Context(), id); err != nil {
		// Belt-and-suspenders: the count check above closes the race for
		// normal traffic, but a concurrent POST to /clusters/{id}/template/
		// could insert a binding between count and delete. Treat the FK
		// violation as the same 409.
		if isFKRestrictViolation(err) {
			RespondRequestError(w, r, http.StatusConflict, apierror.TemplateInUse, "Template is in use; detach from clusters first.")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete cluster template")
		return
	}
	recordAudit(r, h.queries, "admin.cluster_template.deleted", "cluster_template", tmpl.ID.String(), tmpl.Name, nil)
	w.WriteHeader(http.StatusNoContent)
}

// ────────────────────────────────────────────────────────────────────────
// Per-cluster apply / detach / status endpoints
// ────────────────────────────────────────────────────────────────────────

// Apply handles POST /api/v1/clusters/{cluster_id}/template/. Binds the
// template to the cluster (replacing any previous binding) and enqueues
// the convergence task. Returns 202 Accepted with the current status row.
func (h *ClusterTemplateHandler) Apply(w http.ResponseWriter, r *http.Request) {
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
	// Migration 057: maintenance window gate on cluster_template.apply.
	if EnforceMaintenanceWindow(w, r, h.maintenanceGate, "cluster_template.apply",
		MaintenanceGateClusterLabels(cluster),
		pgtype.UUID{Bytes: clusterID, Valid: true}, pgtype.UUID{}) {
		return
	}
	var req ApplyClusterTemplateRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	templateID, err := uuid.Parse(req.TemplateID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid template_id")
		return
	}
	tmpl, err := h.queries.GetClusterTemplateByID(r.Context(), templateID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster template not found")
		return
	}

	// Migration 067 — pre-flight every ${vault://...} reference in the
	// spec so a bad template (missing key / wrong connection) fails the
	// API call instead of the worker. We DELIBERATELY DO NOT persist
	// the resolved values: the spec snapshot keeps the original
	// ${vault://...} markers, and the worker re-resolves at install
	// time. Cluster-scoped apply, no project context, so unqualified
	// refs require the explicit ${vault://<connection>/...} form.
	if _, vaultErr := vaultResolveBlob(r.Context(), h.vaultResolver, uuid.Nil, string(tmpl.Spec)); vaultErr != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.VaultResolveFailed, vaultErr.Error())
		return
	}

	app, err := h.upsertApplicationAndEnqueue(r, sqlc.UpsertClusterTemplateApplicationParams{
		ClusterID:    clusterID,
		TemplateID:   tmpl.ID,
		SpecSnapshot: tmpl.Spec,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ApplyError, "Failed to bind template to cluster")
		return
	}

	h.publishTemplateBindingChanged(clusterID, app.Status)
	recordAudit(r, h.queries, "cluster.template_applied", "cluster", clusterID.String(), cluster.Name, map[string]any{
		"template_id":   tmpl.ID.String(),
		"template_name": tmpl.Name,
	})
	RespondJSON(w, http.StatusAccepted, applicationToResponse(app, tmpl.Name))
}

// GetApplication handles GET /api/v1/clusters/{cluster_id}/template/.
func (h *ClusterTemplateHandler) GetApplication(w http.ResponseWriter, r *http.Request) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	app, err := h.queries.GetClusterTemplateApplication(r.Context(), clusterID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "No template applied to this cluster")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.LookupError, "Failed to load template application")
		return
	}
	tmpl, err := h.queries.GetClusterTemplateByID(r.Context(), app.TemplateID)
	templateName := ""
	if err == nil {
		templateName = tmpl.Name
	}
	RespondJSON(w, http.StatusOK, applicationToResponse(app, templateName))
}

// Reapply handles POST /api/v1/clusters/{cluster_id}/template/reapply/.
// Used for drift correction — resets the application status to pending
// and re-enqueues the apply task. Spec snapshot is refreshed from the
// current template body so the convergence target tracks the latest.
func (h *ClusterTemplateHandler) Reapply(w http.ResponseWriter, r *http.Request) {
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
	app, err := h.queries.GetClusterTemplateApplication(r.Context(), clusterID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "No template applied to this cluster")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.LookupError, "Failed to load template application")
		return
	}
	tmpl, err := h.queries.GetClusterTemplateByID(r.Context(), app.TemplateID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.LookupError, "Template no longer exists")
		return
	}
	app, err = h.upsertApplicationAndEnqueue(r, sqlc.UpsertClusterTemplateApplicationParams{
		ClusterID:    clusterID,
		TemplateID:   tmpl.ID,
		SpecSnapshot: tmpl.Spec,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ApplyError, "Failed to reset template application")
		return
	}
	h.publishTemplateBindingChanged(clusterID, app.Status)
	recordAudit(r, h.queries, "cluster.template_reapplied", "cluster", clusterID.String(), cluster.Name, map[string]any{
		"template_id":   tmpl.ID.String(),
		"template_name": tmpl.Name,
	})
	RespondJSON(w, http.StatusAccepted, applicationToResponse(app, tmpl.Name))
}

// Detach handles DELETE /api/v1/clusters/{cluster_id}/template/. Removes
// the binding (and the associated registration-policy row) but leaves
// any tools/projects the apply task installed in place — the operator
// can clean those up via the individual handlers if a full teardown is
// desired. This conservative behavior matches the user expectation that
// "unbind" not destroy operator-installed workloads.
func (h *ClusterTemplateHandler) Detach(w http.ResponseWriter, r *http.Request) {
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
	if err := h.queries.DeleteClusterTemplateApplication(r.Context(), clusterID); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DetachError, "Failed to detach template")
		return
	}
	// Best-effort detach of the policy stamp. Errors here are non-fatal —
	// the binding is already gone; the worst case is a stale policy row
	// that the next apply (to any template) will overwrite.
	_ = h.queries.DeleteClusterRegistrationPolicy(r.Context(), clusterID)
	h.publishTemplateBindingChanged(clusterID, "detached")
	recordAudit(r, h.queries, "cluster.template_detached", "cluster", clusterID.String(), cluster.Name, nil)
	w.WriteHeader(http.StatusNoContent)
}

// enqueueApply schedules a cluster_template:apply task. Optional —
// nil-safe when no queue is wired, in which case the periodic sweep
// will eventually pick up pending rows.
func (h *ClusterTemplateHandler) enqueueApply(r *http.Request, clusterID uuid.UUID) {
	if h == nil || (h.queue == nil && h.taskOutbox == nil) {
		return
	}
	task, err := tasks.NewClusterTemplateApplyTask(clusterID)
	if err != nil {
		return
	}
	payload := observability.EnrichTaskPayload(r.Context(), task.Payload(), middleware.GetCorrelationID(r.Context()))
	task = asynq.NewTask(task.Type(), payload, asynq.MaxRetry(3))
	if enqueueClusterTemplateApplyOutbox(r.Context(), h.taskOutbox, task, clusterID) {
		return
	}
	if h.queue != nil {
		_, _ = h.queue.Enqueue(task, asynq.Queue(tasks.ClusterTemplateApplyQueueName))
	}
}

func (h *ClusterTemplateHandler) upsertApplicationAndEnqueue(r *http.Request, params sqlc.UpsertClusterTemplateApplicationParams) (sqlc.ClusterTemplateApplication, error) {
	if h != nil && h.taskOutbox != nil {
		if task, err := tasks.NewClusterTemplateApplyTask(params.ClusterID); err == nil {
			payload := observability.EnrichTaskPayload(r.Context(), task.Payload(), middleware.GetCorrelationID(r.Context()))
			task = asynq.NewTask(task.Type(), payload, asynq.MaxRetry(3))
			app, atomic, err := upsertClusterTemplateApplicationWithTaskOutbox(r.Context(), h.queries, h.taskOutbox, params, task, tasks.TaskOutboxOptions{
				DedupeKey:           fmt.Sprintf("cluster_template_apply:%s", params.ClusterID.String()),
				QueueName:           tasks.ClusterTemplateApplyQueueName,
				MaxRetry:            3,
				MaxDeliveryAttempts: 20,
			})
			if atomic {
				return app, err
			}
		}
	}
	app, err := h.queries.UpsertClusterTemplateApplication(r.Context(), params)
	if err != nil {
		return sqlc.ClusterTemplateApplication{}, err
	}
	h.enqueueApply(r, params.ClusterID)
	return app, nil
}

// ────────────────────────────────────────────────────────────────────────
// Error classification helpers
// ────────────────────────────────────────────────────────────────────────

// isUniqueViolation returns true for Postgres unique_violation (23505).
// Matches the pattern used by ToolHandler.EnsureInstalled.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	// Fallback: some pooled/wrapped error paths don't preserve *pgconn.PgError
	// for errors.As, so match the stable SQLSTATE text (cf. db.isUndefinedTable
	// which matches 42P01 by string for the same reason).
	return strings.Contains(err.Error(), "SQLSTATE 23505")
}

// isFKRestrictViolation returns true for the 23503 foreign_key_violation
// raised when the FK ON DELETE RESTRICT clause blocks a cluster_templates
// DELETE while at least one cluster_template_applications row still
// references it.
func isFKRestrictViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23503"
	}
	return false
}
