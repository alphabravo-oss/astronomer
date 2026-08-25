// Package handler — sprint-074 platform-default cluster-template
// endpoint.
//
// Closes the "register a cluster, get nothing" gap. Sprint 074
// introduces a single platform-wide default cluster_template
// (typically the seeded "Platform baseline" — trivy-operator,
// kube-state-metrics, node-exporter, fluent-bit, ingress-nginx, cert-manager,
// gatekeeper) that the
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
	"fmt"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
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

// PlatformDefaultTemplateMutationTx is the transaction-bound platform
// configuration, template-application task, and mandatory-audit surface.
type PlatformDefaultTemplateMutationTx interface {
	PlatformDefaultTemplateQuerier
	GetPlatformConfigForUpdate(ctx context.Context) (sqlc.PlatformConfiguration, error)
	clusterTemplateApplicationTaskOutboxQuerier
	tasks.TaskOutboxWriter
	audit.OutboxQuerier
}

type platformDefaultTemplateRunTxFunc func(context.Context, func(PlatformDefaultTemplateMutationTx) error) error

// PlatformDefaultTemplateHandler owns
// /api/v1/admin/platform-settings/default-cluster-template/*.
type PlatformDefaultTemplateHandler struct {
	queries PlatformDefaultTemplateQuerier
	runTx   platformDefaultTemplateRunTxFunc
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

// SetRunTx wires the production transaction used by default changes and
// reapply intents.
func (h *PlatformDefaultTemplateHandler) SetRunTx(runTx platformDefaultTemplateRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

// TransactionalAuditWired is a production wiring probe.
func (h *PlatformDefaultTemplateHandler) TransactionalAuditWired() bool {
	return h != nil && h.runTx != nil
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

func defaultTemplateResponseFor(cfg sqlc.PlatformConfiguration, tmpl *sqlc.ClusterTemplate) defaultTemplateResponse {
	resp := defaultTemplateResponse{}
	if !cfg.DefaultClusterTemplateID.Valid {
		return resp
	}
	id := uuid.UUID(cfg.DefaultClusterTemplateID.Bytes).String()
	resp.TemplateID = &id
	if tmpl != nil {
		resp.Template = &clusterTemplateLiteForm{ID: tmpl.ID.String(), Name: tmpl.Name, Description: tmpl.Description, Spec: tmpl.Spec}
	}
	return resp
}

// Get handles GET /api/v1/admin/platform-settings/default-cluster-template/.
func (h *PlatformDefaultTemplateHandler) Get(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Platform configuration not available")
		return
	}
	cfg, err := h.queries.GetPlatformConfig(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	resp := defaultTemplateResponseFor(cfg, nil)
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
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	resp = defaultTemplateResponseFor(cfg, &tmpl)
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
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Platform configuration not available")
		return
	}

	var req putRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}

	var target pgtype.UUID
	var parsedTarget uuid.UUID
	if req.TemplateID != nil && *req.TemplateID != "" {
		parsed, err := uuid.Parse(*req.TemplateID)
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "template_id must be a UUID or null")
			return
		}
		parsedTarget = parsed
		target = pgtype.UUID{Bytes: parsed, Valid: true}
	} else {
		// Either {"template_id": null} or {} — both mean "clear".
		target = pgtype.UUID{Valid: false}
	}

	type updateResult struct {
		config   sqlc.PlatformConfiguration
		template *sqlc.ClusterTemplate
	}
	errTemplateNotFound := errors.New("platform default template not found")
	mutate := func(q PlatformDefaultTemplateQuerier) (updateResult, error) {
		var tmpl *sqlc.ClusterTemplate
		if target.Valid {
			row, err := q.GetClusterTemplateByID(r.Context(), parsedTarget)
			if errors.Is(err, pgx.ErrNoRows) {
				return updateResult{}, errTemplateNotFound
			}
			if err != nil {
				return updateResult{}, err
			}
			tmpl = &row
		}
		updated, err := q.SetPlatformDefaultClusterTemplate(r.Context(), target)
		return updateResult{config: updated, template: tmpl}, err
	}
	var result updateResult
	var err error
	if h.runTx != nil {
		err = h.runTx(r.Context(), func(q PlatformDefaultTemplateMutationTx) error {
			previous, lockErr := q.GetPlatformConfigForUpdate(r.Context())
			if lockErr != nil {
				return lockErr
			}
			result, lockErr = mutate(q)
			if lockErr != nil {
				return lockErr
			}
			var oldValue, newValue any
			if previous.DefaultClusterTemplateID.Valid {
				oldValue = uuid.UUID(previous.DefaultClusterTemplateID.Bytes).String()
			}
			if result.config.DefaultClusterTemplateID.Valid {
				newValue = uuid.UUID(result.config.DefaultClusterTemplateID.Bytes).String()
			}
			return recordAuditOutbox(r, q, "admin.platform_default_template.updated", "platform_configuration", "1", "", http.StatusOK, map[string]any{
				"old_template_id": oldValue, "new_template_id": newValue,
			})
		})
	} else {
		previous, getErr := h.queries.GetPlatformConfig(r.Context())
		if getErr != nil {
			err = getErr
		} else {
			result, err = mutate(h.queries)
		}
		if err == nil {
			var oldValue, newValue any
			if previous.DefaultClusterTemplateID.Valid {
				oldValue = uuid.UUID(previous.DefaultClusterTemplateID.Bytes).String()
			}
			if result.config.DefaultClusterTemplateID.Valid {
				newValue = uuid.UUID(result.config.DefaultClusterTemplateID.Bytes).String()
			}
			recordAudit(r, h.queries, "admin.platform_default_template.updated", "platform_configuration", "1", "", map[string]any{
				"old_template_id": oldValue, "new_template_id": newValue,
			})
		}
	}
	if errors.Is(err, errTemplateNotFound) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "template_id does not reference an existing cluster_templates row")
		return
	}
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DBError, "Failed to update platform default cluster template")
		return
	}
	RespondJSON(w, http.StatusOK, defaultTemplateResponseFor(result.config, result.template))
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
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Platform configuration not available")
		return
	}
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	var cluster sqlc.Cluster
	var tmpl sqlc.ClusterTemplate
	var app sqlc.ClusterTemplateApplication
	var errNoDefault = errors.New("no platform default")
	var errStaleDefault = errors.New("stale platform default")
	var errClusterNotFound = errors.New("cluster not found")
	buildTask := func(clusterID uuid.UUID) (*asynq.Task, error) {
		task, err := tasks.NewClusterTemplateApplyTask(clusterID)
		if err != nil {
			return nil, err
		}
		payload := observability.EnrichTaskPayload(r.Context(), task.Payload(), middleware.GetCorrelationID(r.Context()))
		return asynq.NewTask(task.Type(), payload, asynq.MaxRetry(3)), nil
	}
	load := func(q PlatformDefaultTemplateQuerier, cfg sqlc.PlatformConfiguration) error {
		var loadErr error
		cluster, loadErr = q.GetClusterByID(r.Context(), clusterID)
		if errors.Is(loadErr, pgx.ErrNoRows) {
			return errClusterNotFound
		}
		if loadErr != nil {
			return loadErr
		}
		if !cfg.DefaultClusterTemplateID.Valid {
			return errNoDefault
		}
		tmpl, loadErr = q.GetClusterTemplateByID(r.Context(), uuid.UUID(cfg.DefaultClusterTemplateID.Bytes))
		if errors.Is(loadErr, pgx.ErrNoRows) {
			return errStaleDefault
		}
		return loadErr
	}
	if h.runTx != nil {
		err = h.runTx(r.Context(), func(q PlatformDefaultTemplateMutationTx) error {
			cfg, txErr := q.GetPlatformConfigForUpdate(r.Context())
			if txErr != nil {
				return txErr
			}
			if txErr = load(q, cfg); txErr != nil {
				return txErr
			}
			task, txErr := buildTask(cluster.ID)
			if txErr != nil {
				return txErr
			}
			var persisted bool
			app, persisted, txErr = upsertClusterTemplateApplicationWithTaskOutbox(r.Context(), q, q, sqlc.UpsertClusterTemplateApplicationParams{
				ClusterID: cluster.ID, TemplateID: tmpl.ID, SpecSnapshot: tmpl.Spec,
			}, task, tasks.TaskOutboxOptions{
				DedupeKey: clusterTemplateApplyDedupeKey(cluster.ID), QueueName: tasks.ClusterTemplateApplyQueueName,
				MaxRetry: 3, MaxDeliveryAttempts: 20,
			})
			if txErr != nil {
				return txErr
			}
			if !persisted {
				return fmt.Errorf("cluster template application task outbox is not transactionally available")
			}
			return recordAuditOutbox(r, q, "cluster.template.reapplied", "cluster", cluster.ID.String(), cluster.Name, http.StatusAccepted, map[string]any{
				"template_id": tmpl.ID.String(), "template_name": tmpl.Name, "source": "platform_default_reapply",
			})
		})
	} else {
		cfg, cfgErr := h.queries.GetPlatformConfig(r.Context())
		if cfgErr != nil {
			err = cfgErr
		} else if err = load(h.queries, cfg); err == nil {
			app, err = h.queries.UpsertClusterTemplateApplication(r.Context(), sqlc.UpsertClusterTemplateApplicationParams{
				ClusterID: cluster.ID, TemplateID: tmpl.ID, SpecSnapshot: tmpl.Spec,
			})
			if err == nil {
				recordAudit(r, h.queries, "cluster.template.reapplied", "cluster", cluster.ID.String(), cluster.Name, map[string]any{
					"template_id": tmpl.ID.String(), "template_name": tmpl.Name, "source": "platform_default_reapply",
				})
				if task, taskErr := buildTask(cluster.ID); taskErr == nil {
					if !enqueueClusterTemplateApplyOutbox(r.Context(), h.taskOutbox, task, cluster.ID) && h.queue != nil {
						_, _ = h.queue.Enqueue(task, asynq.Queue(tasks.ClusterTemplateApplyQueueName))
					}
				}
			}
		}
	}
	if errors.Is(err, errClusterNotFound) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	if errors.Is(err, errNoDefault) {
		RespondRequestError(w, r, http.StatusConflict, apierror.NoDefault, "No platform default cluster template is configured. Set one via PUT /admin/platform-settings/default-cluster-template/ first.")
		return
	}
	if errors.Is(err, errStaleDefault) {
		RespondRequestError(w, r, http.StatusConflict, apierror.StaleDefault, "Platform default cluster template no longer exists. Pick a new one.")
		return
	}
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DBError, "Failed to reapply platform default cluster template")
		return
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
