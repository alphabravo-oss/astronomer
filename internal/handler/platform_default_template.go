// Package handler — sprint-074 platform-default cluster-template
// endpoint.
//
// Closes the "register a cluster, get nothing" gap. Sprint 074
// introduces a single platform-wide default cluster_template
// (typically the seeded "Platform baseline" — trivy-operator,
// kube-state-metrics, node-exporter, fluent-bit, cert-manager) that the
// cluster Create handler auto-attaches to every newly-registered
// cluster. This handler is the operator-facing surface for managing
// that default and for back-filling existing clusters.
//
// Endpoints (all under /api/v1, superuser-gated):
//
//	GET    /admin/platform-settings/default-cluster-template/
//	    Returns { template_id, template } where template is the resolved
//	    cluster_templates row (or nulls when no default is set).
//
//	PUT    /admin/platform-settings/default-cluster-template/
//	    Body: {"template_id": "<uuid>"} sets the default; {"template_id": null}
//	    clears it (back to legacy "no auto-attach" behavior).
//
//	POST   /admin/platform-settings/default-cluster-template/reapply/{cluster_id}/
//	    Forces the current platform default onto an existing cluster by
//	    writing a cluster_template_applications row. Useful when an
//	    operator changes the baseline and wants to back-fill clusters
//	    that registered before the change. Idempotent — the underlying
//	    UpsertClusterTemplateApplication overwrites the existing row.
//
// Why a separate handler instead of extending PlatformSettingsHandler?
// The platform_settings table (migration 046) is a key/value JSONB
// store; default_cluster_template_id is a real FK column on
// platform_configuration (the singleton row from migration 001). The
// shapes don't mix cleanly, and adding template-management logic into
// the registry-driven settings handler would force every test fake to
// grow ClusterTemplate query methods. A dedicated handler is the
// smaller change.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// PlatformDefaultTemplateQuerier is the narrow DB surface this handler
// needs. Production wires *sqlc.Queries; test fakes implement only
// these methods. Mirrors PlatformSettingsQuerier's slim-interface
// pattern so the handler stays trivially mockable.
type PlatformDefaultTemplateQuerier interface {
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	GetPlatformConfig(ctx context.Context) (sqlc.PlatformConfiguration, error)
	SetPlatformDefaultClusterTemplate(ctx context.Context, defaultClusterTemplateID pgtype.UUID) (sqlc.PlatformConfiguration, error)
	GetClusterTemplateByID(ctx context.Context, id uuid.UUID) (sqlc.ClusterTemplate, error)
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	UpsertClusterTemplateApplication(ctx context.Context, arg sqlc.UpsertClusterTemplateApplicationParams) (sqlc.ClusterTemplateApplication, error)
}

// PlatformDefaultTemplateHandler owns
// /api/v1/admin/platform-settings/default-cluster-template/*.
type PlatformDefaultTemplateHandler struct {
	queries PlatformDefaultTemplateQuerier
	// queue schedules the cluster_template:apply task on reapply.
	// Nil-safe — drift_check sweep is the fallback.
	queue      ClusterDecommissionEnqueuer
	taskOutbox tasks.TaskOutboxWriter
}

// NewPlatformDefaultTemplateHandler wires the handler. queries may be
// nil for degenerate test installs; every endpoint returns 503 in that
// case.
func NewPlatformDefaultTemplateHandler(queries PlatformDefaultTemplateQuerier) *PlatformDefaultTemplateHandler {
	return &PlatformDefaultTemplateHandler{queries: queries}
}

// SetApplyQueue wires the asynq client used by Reapply to enqueue the
// cluster_template:apply task. Optional and nil-safe.
func (h *PlatformDefaultTemplateHandler) SetApplyQueue(q ClusterDecommissionEnqueuer) {
	if h == nil {
		return
	}
	h.queue = q
}

// SetTaskOutbox wires the durable task outbox used before direct Redis enqueue.
// Optional and nil-safe.
func (h *PlatformDefaultTemplateHandler) SetTaskOutbox(q tasks.TaskOutboxWriter) {
	if h == nil {
		return
	}
	h.taskOutbox = q
}

// defaultTemplateResponse is the wire shape for GET. TemplateID is a
// pointer-string so we can serialize "null" when no default is
// configured (the JSON-encoder treats nil pointers as null). The
// resolved template is inlined so the operator UI doesn't need a
// follow-up fetch.
type defaultTemplateResponse struct {
	TemplateID *string                  `json:"template_id"`
	Template   *clusterTemplateLiteForm `json:"template"`
}

// clusterTemplateLiteForm is a trimmed wire shape — name/description/spec
// only — for embedding in the GET response. We intentionally don't
// reuse the full cluster_templates handler DTO to avoid a dependency
// cycle and to keep the response body small.
type clusterTemplateLiteForm struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Spec        json.RawMessage `json:"spec"`
}

// Get handles GET /api/v1/admin/platform-settings/default-cluster-template/.
func (h *PlatformDefaultTemplateHandler) Get(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "not_configured", "Platform configuration not available")
		return
	}
	cfg, err := h.queries.GetPlatformConfig(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	resp := defaultTemplateResponse{}
	if !cfg.DefaultClusterTemplateID.Valid {
		// No default configured — auto-attach is off. The frontend
		// renders this as "Auto-attach disabled" with a picker to
		// turn it on. Return nulls (not omitted) so the operator can
		// see the explicit "unset" state without inferring it.
		RespondJSON(w, http.StatusOK, resp)
		return
	}
	tmpl, err := h.queries.GetClusterTemplateByID(r.Context(), uuid.UUID(cfg.DefaultClusterTemplateID.Bytes))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Race / stale: the operator deleted the template after
			// the cron sweep cleared the FK. Surface as "id set but
			// template gone" so the operator can decide whether to
			// pick a new one or clear.
			id := uuid.UUID(cfg.DefaultClusterTemplateID.Bytes).String()
			resp.TemplateID = &id
			RespondJSON(w, http.StatusOK, resp)
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	id := tmpl.ID.String()
	resp.TemplateID = &id
	resp.Template = &clusterTemplateLiteForm{
		ID:          tmpl.ID.String(),
		Name:        tmpl.Name,
		Description: tmpl.Description,
		Spec:        tmpl.Spec,
	}
	RespondJSON(w, http.StatusOK, resp)
}

// putRequest is the body shape for PUT. We use *string instead of
// string so the JSON body `{"template_id": null}` is distinguishable
// from the missing-key case (also accepted as a clear). The pointer
// also lets us reject the empty-string-as-uuid case (which would
// otherwise look identical to a missing field).
type putRequest struct {
	TemplateID *string `json:"template_id"`
}

// Update handles PUT /api/v1/admin/platform-settings/default-cluster-template/.
//
// Body shapes:
//   - {"template_id": "<uuid>"} — set the default; validates the UUID
//     points to an existing cluster_templates row.
//   - {"template_id": null}    — clear the default (legacy behavior:
//     new clusters come up bare).
//   - {}                       — same as null.
func (h *PlatformDefaultTemplateHandler) Update(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "not_configured", "Platform configuration not available")
		return
	}

	var req putRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	// Capture the previous value for the audit trail. Best-effort: a
	// fetch failure shouldn't block the write — we'll just log a less
	// detailed audit row.
	var oldValue any
	if prev, err := h.queries.GetPlatformConfig(r.Context()); err == nil {
		if prev.DefaultClusterTemplateID.Valid {
			oldValue = uuid.UUID(prev.DefaultClusterTemplateID.Bytes).String()
		}
	}

	var target pgtype.UUID
	if req.TemplateID != nil && *req.TemplateID != "" {
		parsed, err := uuid.Parse(*req.TemplateID)
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, "validation_error", "template_id must be a UUID or null")
			return
		}
		// Validate the template exists BEFORE writing so we return a
		// clean 400 instead of a Postgres FK violation surfaced as a
		// 500. The platform_configuration FK is the second-line guard
		// (it'll catch a row that disappears between the check and
		// the write — that's a 500 path which is correct given the
		// rare race).
		if _, err := h.queries.GetClusterTemplateByID(r.Context(), parsed); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				RespondRequestError(w, r, http.StatusBadRequest, "validation_error", "template_id does not reference an existing cluster_templates row")
				return
			}
			RespondRequestError(w, r, http.StatusInternalServerError, "db_error", err.Error())
			return
		}
		target = pgtype.UUID{Bytes: parsed, Valid: true}
	} else {
		// Either {"template_id": null} or {} — both mean "clear".
		target = pgtype.UUID{Valid: false}
	}

	updated, err := h.queries.SetPlatformDefaultClusterTemplate(r.Context(), target)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "db_error", err.Error())
		return
	}

	var newValue any
	if updated.DefaultClusterTemplateID.Valid {
		newValue = uuid.UUID(updated.DefaultClusterTemplateID.Bytes).String()
	}
	recordAudit(r, h.queries, "admin.platform_default_template.updated", "platform_configuration", "1", "", map[string]any{
		"old_template_id": oldValue,
		"new_template_id": newValue,
	})

	// Re-render via GET semantics so the response is consistent with
	// the read endpoint (operator UI uses the same parser).
	h.Get(w, r)
}

// Reapply handles POST /admin/platform-settings/default-cluster-template/reapply/{cluster_id}/.
//
// Writes a cluster_template_applications row binding the cluster to
// the current platform default. The apply worker (sprint 049) does the
// actual reconcile. Idempotent — re-running on an already-bound
// cluster overwrites the existing row and resets it to 'pending'.
func (h *PlatformDefaultTemplateHandler) Reapply(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "not_configured", "Platform configuration not available")
		return
	}
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), clusterID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, "not_found", "Cluster not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	cfg, err := h.queries.GetPlatformConfig(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	if !cfg.DefaultClusterTemplateID.Valid {
		// No default configured — reapply has nothing to reapply.
		// We could 404 here, but a 409-Conflict ("no platform default
		// is set; configure one first") is the better operator UX —
		// it's a state error, not a missing resource. The frontend
		// renders it as a banner pointing back at the PUT endpoint.
		RespondRequestError(w, r, http.StatusConflict, "no_default", "No platform default cluster template is configured. Set one via PUT /admin/platform-settings/default-cluster-template/ first.")
		return
	}
	tmpl, err := h.queries.GetClusterTemplateByID(r.Context(), uuid.UUID(cfg.DefaultClusterTemplateID.Bytes))
	if err != nil {
		// Stale FK target — surface as 409 so the operator sees a
		// recoverable state error (pick a new default) rather than a
		// generic 500.
		RespondRequestError(w, r, http.StatusConflict, "stale_default", "Platform default cluster template no longer exists. Pick a new one.")
		return
	}
	app, err := h.queries.UpsertClusterTemplateApplication(r.Context(), sqlc.UpsertClusterTemplateApplicationParams{
		ClusterID:    cluster.ID,
		TemplateID:   tmpl.ID,
		SpecSnapshot: tmpl.Spec,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	recordAudit(r, h.queries, "cluster.template.reapplied", "cluster", cluster.ID.String(), cluster.Name, map[string]any{
		"template_id":   tmpl.ID.String(),
		"template_name": tmpl.Name,
		"source":        "platform_default_reapply",
	})

	// Enqueue the apply task so the operator sees progress without
	// waiting for the drift_check sweep. Best-effort.
	if h.queue != nil || h.taskOutbox != nil {
		if task, err := tasks.NewClusterTemplateApplyTask(cluster.ID); err == nil {
			payload := observability.EnrichTaskPayload(r.Context(), task.Payload(), middleware.GetCorrelationID(r.Context()))
			t := asynq.NewTask(task.Type(), payload, asynq.MaxRetry(3))
			if !enqueueClusterTemplateApplyOutbox(r.Context(), h.taskOutbox, t, cluster.ID) && h.queue != nil {
				_, _ = h.queue.Enqueue(t, asynq.Queue(tasks.ClusterTemplateApplyQueueName))
			}
		}
	}

	RespondJSON(w, http.StatusAccepted, map[string]any{
		"cluster_id":    app.ClusterID.String(),
		"template_id":   app.TemplateID.String(),
		"template_name": tmpl.Name,
		"status":        app.Status,
	})
}

// gate enforces superuser-only access. Same pattern PlatformSettingsHandler uses.
func (h *PlatformDefaultTemplateHandler) gate(w http.ResponseWriter, r *http.Request) bool {
	_, ok := requireSuperuser(w, r, h.queries, superuserGateConfig{
		StoreUnavailableStatus:  http.StatusInternalServerError,
		StoreUnavailableCode:    "internal_error",
		StoreUnavailableMessage: "User store not configured",
		ForbiddenMessage:        "Platform default template administration requires superuser privileges",
	})
	return ok
}
