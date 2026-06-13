// Package handler — compliance baselines API (sprint 17, migration 064).
//
// Surfaces the four-preset compliance baselines feature to operators
// via /api/v1/admin/compliance-baselines/*. The actual apply / revert
// / diff logic lives in internal/compliance — this handler is the
// thin HTTP veneer over that engine plus the audit + RBAC gate.
//
// Endpoints (all superuser-gated inside the handler — same pattern as
// admin_drill.go / admin_queues.go / platform_settings.go):
//
//	GET    /admin/compliance-baselines/                           — list (joined with registry)
//	GET    /admin/compliance-baselines/{id}/                      — single baseline
//	GET    /admin/compliance-baselines/{id}/diff/                 — current-vs-target preview
//	POST   /admin/compliance-baselines/{id}/apply/                — apply
//	GET    /admin/compliance-baselines/active/                    — most-recent applied
//	GET    /admin/compliance-baseline-applications/               — history
//	POST   /admin/compliance-baseline-applications/{id}/revert/   — revert
//
// Audit events: compliance.baseline.{viewed,applied,reverted}.
//
// Metrics: astronomer_compliance_baseline_active{slug} gauge — 1 for
// the currently-active slug, 0 for the others. Updated after every
// Apply / Revert.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/compliance"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// ── metrics ───────────────────────────────────────────────────────────

var complianceBaselineActiveGauge = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "astronomer",
		Subsystem: "compliance",
		Name:      "baseline_active",
		Help:      "Currently-applied compliance baseline. 1 for the active slug, 0 for the others.",
	},
	observability.MetricLabels("slug"),
)

func init() {
	prometheus.MustRegister(complianceBaselineActiveGauge)
}

// ── seams ─────────────────────────────────────────────────────────────

// runTxFunc is the seam Apply / Revert use to obtain a tx-scoped
// Querier. Production code wires a closure that begins a pgx tx and
// passes sqlc.New(tx); test code wires a no-op tx that just hands
// the in-memory fake Querier to the engine. The signature returns
// (commit, rollback) so the handler can defer rollback and call
// commit on success — keeping the tx-lifecycle dance out of the
// engine itself.
type runTxFunc func(ctx context.Context, fn func(q compliance.Querier) error) error

// ComplianceBaselineReader is the read-only Querier interface the
// handler uses for the non-mutating endpoints (List / Get / History
// / Active / Diff). Apply / Revert use the engine's wider Querier
// which is satisfied by a tx-scoped *sqlc.Queries.
type ComplianceBaselineReader interface {
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	ListComplianceBaselines(ctx context.Context) ([]sqlc.ComplianceBaseline, error)
	GetComplianceBaseline(ctx context.Context, id uuid.UUID) (sqlc.ComplianceBaseline, error)
	GetComplianceBaselineApplication(ctx context.Context, id uuid.UUID) (sqlc.ComplianceBaselineApplication, error)
	GetActiveComplianceBaselineApplication(ctx context.Context) (sqlc.ComplianceBaselineApplication, error)
	ListComplianceBaselineApplications(ctx context.Context, limit int32) ([]sqlc.ComplianceBaselineApplication, error)
	// For diff (read-only):
	GetPlatformSetting(ctx context.Context, key string) (sqlc.PlatformSetting, error)
	GetQuotaPlan(ctx context.Context, name string) (sqlc.QuotaPlan, error)
}

// ComplianceBaselinesHandler owns the /admin/compliance-baselines/*
// routes. nil-safe: routes are not registered if the handler isn't
// wired in routes.go.
type ComplianceBaselinesHandler struct {
	reader ComplianceBaselineReader
	// runTx wraps a body in a tx-scoped Querier. nil means Apply /
	// Revert return 503; reads still work.
	runTx  runTxFunc
	logger *slog.Logger
	// auditQ is the auditWriter the audit_helpers package expects.
	// In production it's the same *sqlc.Queries as reader; this is
	// kept separate so the test fake can satisfy the read interface
	// without also implementing CreateAuditLogV1.
	auditQ any
}

// NewComplianceBaselinesHandler wires the handler. reader is required
// for all reads; runTx is required for Apply / Revert. When runTx is
// nil, Apply / Revert return 503 — the read endpoints still work.
func NewComplianceBaselinesHandler(reader ComplianceBaselineReader, runTx runTxFunc, logger *slog.Logger) *ComplianceBaselinesHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ComplianceBaselinesHandler{
		reader: reader,
		runTx:  runTx,
		logger: logger,
		auditQ: reader,
	}
}

// NewComplianceBaselinesHandlerFromPool is the production wiring
// convenience used by server.go. It begins a pgx tx, hands the
// tx-scoped *sqlc.Queries to the engine, and commits/rolls-back
// based on the engine's error return.
func NewComplianceBaselinesHandlerFromPool(pool *pgxpool.Pool, logger *slog.Logger) *ComplianceBaselinesHandler {
	reader := sqlc.New(pool)
	runTx := func(ctx context.Context, fn func(q compliance.Querier) error) error {
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := fn(sqlc.New(tx)); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	return &ComplianceBaselinesHandler{
		reader: reader,
		runTx:  runTx,
		logger: logger,
		auditQ: reader,
	}
}

// ── shared response shapes ────────────────────────────────────────────

// baselineResponse joins one DB row with its canonical spec from the
// registry. The DB carries only the slug + display fields; the
// `spec` field below is the rich registry view the operator sees.
type baselineResponse struct {
	ID          uuid.UUID               `json:"id"`
	Slug        string                  `json:"slug"`
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Version     string                  `json:"version"`
	Enabled     bool                    `json:"enabled"`
	Spec        compliance.BaselineSpec `json:"spec"`
	Active      bool                    `json:"active"`
}

type applicationResponse struct {
	ID            uuid.UUID       `json:"id"`
	BaselineID    uuid.UUID       `json:"baseline_id"`
	BaselineSlug  string          `json:"baseline_slug"`
	BaselineName  string          `json:"baseline_name"`
	AppliedBy     *string         `json:"applied_by,omitempty"`
	AppliedAt     string          `json:"applied_at"`
	Status        string          `json:"status"`
	RevertedAt    *string         `json:"reverted_at,omitempty"`
	RevertedBy    *string         `json:"reverted_by,omitempty"`
	Notes         string          `json:"notes"`
	PreviousState json.RawMessage `json:"previous_state,omitempty"`
}

// ── endpoints ─────────────────────────────────────────────────────────

// List handles GET /admin/compliance-baselines/.
func (h *ComplianceBaselinesHandler) List(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	rows, err := h.reader.ListComplianceBaselines(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	activeSlug := ""
	if active, err := h.reader.GetActiveComplianceBaselineApplication(r.Context()); err == nil {
		if base, err := h.reader.GetComplianceBaseline(r.Context(), active.BaselineID); err == nil {
			activeSlug = base.Slug
		}
	}
	out := make([]baselineResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toBaselineResponse(row, activeSlug))
	}
	recordAudit(r, h.auditQ, "compliance.baseline.viewed", "compliance_baseline", "", "list", map[string]any{
		"count": len(out),
	})
	RespondJSON(w, http.StatusOK, out)
}

// Get handles GET /admin/compliance-baselines/{id}/.
func (h *ComplianceBaselinesHandler) Get(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, ok := parseUUID(w, r)
	if !ok {
		return
	}
	row, err := h.reader.GetComplianceBaseline(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, "not_found", "Baseline not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	activeSlug := ""
	if active, err := h.reader.GetActiveComplianceBaselineApplication(r.Context()); err == nil {
		if base, err := h.reader.GetComplianceBaseline(r.Context(), active.BaselineID); err == nil {
			activeSlug = base.Slug
		}
	}
	recordAudit(r, h.auditQ, "compliance.baseline.viewed", "compliance_baseline", id.String(), row.Slug, nil)
	RespondJSON(w, http.StatusOK, toBaselineResponse(row, activeSlug))
}

// Diff handles GET /admin/compliance-baselines/{id}/diff/.
func (h *ComplianceBaselinesHandler) Diff(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, ok := parseUUID(w, r)
	if !ok {
		return
	}
	// Diff is read-only; use the reader directly (no tx).
	res, err := compliance.Diff(r.Context(), readerAsQuerier{h.reader}, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, "not_found", "Baseline not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, "diff_error", err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, res)
}

// ApplyRequest is the body shape for POST .../apply/.
type ApplyRequest struct {
	Notes string `json:"notes,omitempty"`
}

// Apply handles POST /admin/compliance-baselines/{id}/apply/.
func (h *ComplianceBaselinesHandler) Apply(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "not_configured", "Compliance apply not wired (no tx pool)")
		return
	}
	id, ok := parseUUID(w, r)
	if !ok {
		return
	}
	var req ApplyRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
			return
		}
	}
	userID := callerUUID(r)

	// Capture pre-apply active slug for the audit detail.
	prevActiveSlug := ""
	if active, err := h.reader.GetActiveComplianceBaselineApplication(r.Context()); err == nil {
		if base, err := h.reader.GetComplianceBaseline(r.Context(), active.BaselineID); err == nil {
			prevActiveSlug = base.Slug
		}
	}

	var appID uuid.UUID
	txErr := h.runTx(r.Context(), func(q compliance.Querier) error {
		var err error
		appID, err = compliance.Apply(r.Context(), q, id, userID, req.Notes, h.logger)
		return err
	})
	if txErr != nil {
		if errors.Is(txErr, compliance.ErrAuditRetentionDowngrade) {
			RespondRequestError(w, r, http.StatusConflict, "audit_retention_downgrade", txErr.Error())
			return
		}
		if errors.Is(txErr, compliance.ErrBaselineDisabled) {
			RespondRequestError(w, r, http.StatusConflict, "baseline_disabled", txErr.Error())
			return
		}
		if errors.Is(txErr, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, "not_found", "Baseline not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, "apply_error", txErr.Error())
		return
	}

	// Refresh the active-baseline gauge + audit log.
	baseline, _ := h.reader.GetComplianceBaseline(r.Context(), id)
	h.updateActiveGauge(baseline.Slug)
	recordAudit(r, h.auditQ, "compliance.baseline.applied", "compliance_baseline", id.String(), baseline.Slug, map[string]any{
		"application_id":   appID.String(),
		"slug":             baseline.Slug,
		"prev_active_slug": prevActiveSlug,
		"notes":            req.Notes,
	})

	RespondJSON(w, http.StatusOK, map[string]any{
		"application_id": appID,
		"baseline_id":    id,
		"slug":           baseline.Slug,
	})
}

// Active handles GET /admin/compliance-baselines/active/.
func (h *ComplianceBaselinesHandler) Active(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	app, err := h.reader.GetActiveComplianceBaselineApplication(r.Context())
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondJSON(w, http.StatusOK, map[string]any{"active": nil})
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	base, _ := h.reader.GetComplianceBaseline(r.Context(), app.BaselineID)
	RespondJSON(w, http.StatusOK, map[string]any{
		"active": toApplicationResponse(app, base),
	})
}

// History handles GET /admin/compliance-baseline-applications/.
func (h *ComplianceBaselinesHandler) History(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	rows, err := h.reader.ListComplianceBaselineApplications(r.Context(), 100)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	out := make([]applicationResponse, 0, len(rows))
	for _, app := range rows {
		base, _ := h.reader.GetComplianceBaseline(r.Context(), app.BaselineID)
		out = append(out, toApplicationResponse(app, base))
	}
	RespondJSON(w, http.StatusOK, out)
}

// Revert handles POST /admin/compliance-baseline-applications/{id}/revert/.
func (h *ComplianceBaselinesHandler) Revert(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "not_configured", "Compliance revert not wired (no tx pool)")
		return
	}
	id, ok := parseUUID(w, r)
	if !ok {
		return
	}
	userID := callerUUID(r)

	txErr := h.runTx(r.Context(), func(q compliance.Querier) error {
		return compliance.Revert(r.Context(), q, id, userID, h.logger)
	})
	if txErr != nil {
		if errors.Is(txErr, compliance.ErrNewerApplicationExists) {
			RespondRequestError(w, r, http.StatusConflict, "newer_application_exists", txErr.Error())
			return
		}
		if errors.Is(txErr, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, "not_found", "Application not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, "revert_error", txErr.Error())
		return
	}

	// After revert the active gauge should reflect whatever (if
	// anything) is now the most-recent applied row. Reset every
	// baseline slug to 0, then set the new active to 1.
	h.updateActiveGauge("")
	app, err := h.reader.GetActiveComplianceBaselineApplication(r.Context())
	if err == nil {
		base, _ := h.reader.GetComplianceBaseline(r.Context(), app.BaselineID)
		h.updateActiveGauge(base.Slug)
	}

	recordAudit(r, h.auditQ, "compliance.baseline.reverted", "compliance_baseline_application", id.String(), "", map[string]any{
		"application_id": id.String(),
	})

	RespondJSON(w, http.StatusOK, map[string]any{
		"application_id": id,
		"status":         "reverted",
	})
}

// ── helpers ───────────────────────────────────────────────────────────

func (h *ComplianceBaselinesHandler) gate(w http.ResponseWriter, r *http.Request) bool {
	_, ok := requireSuperuser(w, r, h.reader, superuserGateConfig{
		StoreUnavailableCode:    "not_configured",
		StoreUnavailableMessage: "Baseline store not configured",
		ForbiddenMessage:        "Compliance baselines require superuser privileges",
	})
	return ok
}

func (h *ComplianceBaselinesHandler) updateActiveGauge(activeSlug string) {
	// Reset every known slug to 0; set activeSlug to 1.
	for _, b := range compliance.Registry() {
		v := 0.0
		if b.Slug == activeSlug {
			v = 1.0
		}
		complianceBaselineActiveGauge.WithLabelValues(observability.MetricValues(b.Slug)...).Set(v)
	}
}

func parseUUID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid UUID")
		return uuid.Nil, false
	}
	return id, true
}

func callerUUID(r *http.Request) uuid.UUID {
	user, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok {
		return uuid.Nil
	}
	id, err := uuid.Parse(user.ID)
	if err != nil {
		return uuid.Nil
	}
	return id
}

func toBaselineResponse(row sqlc.ComplianceBaseline, activeSlug string) baselineResponse {
	out := baselineResponse{
		ID:          row.ID,
		Slug:        row.Slug,
		Name:        row.Name,
		Description: row.Description,
		Version:     row.Version,
		Enabled:     row.Enabled,
		Active:      row.Slug == activeSlug,
	}
	if reg, ok := compliance.BySlug(row.Slug); ok {
		out.Spec = reg.Spec
	}
	return out
}

func toApplicationResponse(app sqlc.ComplianceBaselineApplication, base sqlc.ComplianceBaseline) applicationResponse {
	out := applicationResponse{
		ID:            app.ID,
		BaselineID:    app.BaselineID,
		BaselineSlug:  base.Slug,
		BaselineName:  base.Name,
		AppliedAt:     app.AppliedAt.UTC().Format("2006-01-02T15:04:05Z"),
		Status:        app.Status,
		Notes:         app.Notes,
		PreviousState: app.PreviousState,
	}
	if app.AppliedBy.Valid {
		id := uuid.UUID(app.AppliedBy.Bytes).String()
		out.AppliedBy = &id
	}
	if app.RevertedAt.Valid {
		s := app.RevertedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
		out.RevertedAt = &s
	}
	if app.RevertedBy.Valid {
		id := uuid.UUID(app.RevertedBy.Bytes).String()
		out.RevertedBy = &id
	}
	return out
}

// readerAsQuerier adapts a read-only ComplianceBaselineReader to the
// wider compliance.Querier the engine expects. The mutating methods
// panic — Diff is the only entry point that uses this adapter, and
// Diff never calls them.
type readerAsQuerier struct{ r ComplianceBaselineReader }

func (a readerAsQuerier) GetComplianceBaseline(ctx context.Context, id uuid.UUID) (sqlc.ComplianceBaseline, error) {
	return a.r.GetComplianceBaseline(ctx, id)
}
func (a readerAsQuerier) ListComplianceBaselines(ctx context.Context) ([]sqlc.ComplianceBaseline, error) {
	return a.r.ListComplianceBaselines(ctx)
}
func (a readerAsQuerier) CreateComplianceBaselineApplication(ctx context.Context, arg sqlc.CreateComplianceBaselineApplicationParams) (sqlc.ComplianceBaselineApplication, error) {
	panic("readerAsQuerier: write call not allowed (Diff is read-only)")
}
func (a readerAsQuerier) GetComplianceBaselineApplication(ctx context.Context, id uuid.UUID) (sqlc.ComplianceBaselineApplication, error) {
	return a.r.GetComplianceBaselineApplication(ctx, id)
}
func (a readerAsQuerier) GetActiveComplianceBaselineApplication(ctx context.Context) (sqlc.ComplianceBaselineApplication, error) {
	return a.r.GetActiveComplianceBaselineApplication(ctx)
}
func (a readerAsQuerier) ListComplianceBaselineApplications(ctx context.Context, limit int32) ([]sqlc.ComplianceBaselineApplication, error) {
	return a.r.ListComplianceBaselineApplications(ctx, limit)
}
func (a readerAsQuerier) MarkComplianceBaselineApplicationReverted(ctx context.Context, arg sqlc.MarkComplianceBaselineApplicationRevertedParams) error {
	panic("readerAsQuerier: write call not allowed (Diff is read-only)")
}
func (a readerAsQuerier) GetPlatformSetting(ctx context.Context, key string) (sqlc.PlatformSetting, error) {
	return a.r.GetPlatformSetting(ctx, key)
}
func (a readerAsQuerier) UpsertPlatformSetting(ctx context.Context, arg sqlc.UpsertPlatformSettingParams) (sqlc.PlatformSetting, error) {
	panic("readerAsQuerier: write call not allowed (Diff is read-only)")
}
func (a readerAsQuerier) GetQuotaPlan(ctx context.Context, name string) (sqlc.QuotaPlan, error) {
	return a.r.GetQuotaPlan(ctx, name)
}
func (a readerAsQuerier) UpsertQuotaPlan(ctx context.Context, arg sqlc.UpsertQuotaPlanParams) (sqlc.QuotaPlan, error) {
	panic("readerAsQuerier: write call not allowed (Diff is read-only)")
}
