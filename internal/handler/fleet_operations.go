// Fleet operations handler (migration 056).
//
// Coordinated multi-cluster operations — drain N clusters, upgrade a
// tool across the fleet, apply a template fanout — gated on a label
// selector + an on_error policy + a max_concurrent budget. The
// operator-facing surface is:
//
//   GET    /api/v1/fleet-operations/                — paginated list
//   POST   /api/v1/fleet-operations/                — create + auto-launch
//   GET    /api/v1/fleet-operations/{id}/           — detail w/ counters
//   GET    /api/v1/fleet-operations/{id}/targets/   — paginated per-cluster
//   POST   /api/v1/fleet-operations/{id}/pause/     — running -> paused
//   POST   /api/v1/fleet-operations/{id}/resume/    — paused -> running
//   POST   /api/v1/fleet-operations/{id}/abort/     — any non-terminal -> aborted
//   POST   /api/v1/fleet-operations/{id}/retry-failed/  — re-enqueue failed targets
//
// Once dispatched, an operation's selector and operation_spec are
// frozen — the only mutating endpoints are the four state-transition
// hooks above. That's an explicit constraint to keep the "what is
// this operation actually doing?" answer stable across the lifetime
// of a 50-cluster fanout.
//
// Auth gating uses the new ResourceFleetOperations rbac resource
// (read for list/get/targets; create for POST /; update for the
// state-transition endpoints; delete for the terminal-state cleanup
// path). A "fleet runbook author" role gets fleet_operations:*
// without needing clusters:* or cluster_templates:*.

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

// FleetOperationQuerier is the narrow database surface the handler
// needs. The production *sqlc.Queries satisfies it; tests stand up a
// narrow fake without dragging in the full Queries.
type FleetOperationQuerier interface {
	CreateFleetOperation(ctx context.Context, arg sqlc.CreateFleetOperationParams) (sqlc.FleetOperation, error)
	GetFleetOperation(ctx context.Context, id uuid.UUID) (sqlc.FleetOperation, error)
	ListFleetOperations(ctx context.Context, arg sqlc.ListFleetOperationsParams) ([]sqlc.FleetOperation, error)
	CountFleetOperations(ctx context.Context, status pgtype.Text) (int64, error)
	SetFleetOperationStatus(ctx context.Context, arg sqlc.SetFleetOperationStatusParams) (sqlc.FleetOperation, error)
	ListFleetOperationTargets(ctx context.Context, arg sqlc.ListFleetOperationTargetsParams) ([]sqlc.FleetOperationTarget, error)
	CountFleetOperationTargets(ctx context.Context, operationID uuid.UUID) (int64, error)
	RequeueFailedTargets(ctx context.Context, operationID uuid.UUID) error
}

// FleetOperationTrigger lets the handler poke the orchestrator after
// a create/resume/retry so the operator's click reaches the worker
// without waiting for the next periodic tick. Optional — when not
// wired the periodic scheduler still drives forward progress.
type FleetOperationTrigger interface {
	TriggerFleetOrchestrate(ctx context.Context)
}

// FleetOperationHandler owns /api/v1/fleet-operations/*.
type FleetOperationHandler struct {
	queries FleetOperationQuerier
	trigger FleetOperationTrigger
}

// NewFleetOperationHandler constructs the handler.
func NewFleetOperationHandler(queries FleetOperationQuerier) *FleetOperationHandler {
	return &FleetOperationHandler{queries: queries}
}

// SetTrigger wires the optional orchestrator-kick interface.
func (h *FleetOperationHandler) SetTrigger(t FleetOperationTrigger) {
	if h == nil {
		return
	}
	h.trigger = t
}

// ─────────────────────────────────────────────────────────────────────
// Wire shapes
// ─────────────────────────────────────────────────────────────────────

// FleetOperationResponse is the wire shape returned by list/get.
type FleetOperationResponse struct {
	ID                        string          `json:"id"`
	Name                      string          `json:"name"`
	Description               string          `json:"description"`
	OperationType             string          `json:"operation_type"`
	OperationSpec             json.RawMessage `json:"operation_spec"`
	Selector                  json.RawMessage `json:"selector"`
	Strategy                  string          `json:"strategy"`
	MaxConcurrent             int32           `json:"max_concurrent"`
	OnError                   string          `json:"on_error"`
	RespectMaintenanceWindows bool            `json:"respect_maintenance_windows"`
	Status                    string          `json:"status"`
	TotalClusters             int32           `json:"total_clusters"`
	CompletedClusters         int32           `json:"completed_clusters"`
	FailedClusters            int32           `json:"failed_clusters"`
	SkippedClusters           int32           `json:"skipped_clusters"`
	StartedAt                 string          `json:"started_at,omitempty"`
	CompletedAt               string          `json:"completed_at,omitempty"`
	LastError                 string          `json:"last_error,omitempty"`
	CreatedBy                 string          `json:"created_by,omitempty"`
	CreatedAt                 string          `json:"created_at"`
	UpdatedAt                 string          `json:"updated_at"`
}

func fleetOperationToResponse(op sqlc.FleetOperation) FleetOperationResponse {
	resp := FleetOperationResponse{
		ID:                        op.ID.String(),
		Name:                      op.Name,
		Description:               op.Description,
		OperationType:             op.OperationType,
		OperationSpec:             op.OperationSpec,
		Selector:                  op.Selector,
		Strategy:                  op.Strategy,
		MaxConcurrent:             op.MaxConcurrent,
		OnError:                   op.OnError,
		RespectMaintenanceWindows: op.RespectMaintenanceWindows,
		Status:                    op.Status,
		TotalClusters:             op.TotalClusters,
		CompletedClusters:         op.CompletedClusters,
		FailedClusters:            op.FailedClusters,
		SkippedClusters:           op.SkippedClusters,
		LastError:                 op.LastError,
		CreatedAt:                 op.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:                 op.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if op.StartedAt.Valid {
		resp.StartedAt = op.StartedAt.Time.UTC().Format(time.RFC3339)
	}
	if op.CompletedAt.Valid {
		resp.CompletedAt = op.CompletedAt.Time.UTC().Format(time.RFC3339)
	}
	if op.CreatedBy.Valid {
		resp.CreatedBy = uuid.UUID(op.CreatedBy.Bytes).String()
	}
	return resp
}

// FleetOperationTargetResponse is the wire shape returned by the
// /{id}/targets/ endpoint.
type FleetOperationTargetResponse struct {
	ID               string `json:"id"`
	OperationID      string `json:"operation_id"`
	ClusterID        string `json:"cluster_id"`
	Status           string `json:"status"`
	SubOperationID   string `json:"sub_operation_id,omitempty"`
	SubOperationType string `json:"sub_operation_type,omitempty"`
	StartedAt        string `json:"started_at,omitempty"`
	CompletedAt      string `json:"completed_at,omitempty"`
	LastError        string `json:"last_error,omitempty"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

func fleetTargetToResponse(t sqlc.FleetOperationTarget) FleetOperationTargetResponse {
	resp := FleetOperationTargetResponse{
		ID:               t.ID.String(),
		OperationID:      t.OperationID.String(),
		ClusterID:        t.ClusterID.String(),
		Status:           t.Status,
		SubOperationType: t.SubOperationType,
		LastError:        t.LastError,
		CreatedAt:        t.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:        t.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if t.SubOperationID.Valid {
		resp.SubOperationID = uuid.UUID(t.SubOperationID.Bytes).String()
	}
	if t.StartedAt.Valid {
		resp.StartedAt = t.StartedAt.Time.UTC().Format(time.RFC3339)
	}
	if t.CompletedAt.Valid {
		resp.CompletedAt = t.CompletedAt.Time.UTC().Format(time.RFC3339)
	}
	return resp
}

// CreateFleetOperationRequest is the POST body shape.
type CreateFleetOperationRequest struct {
	Name                      string          `json:"name"`
	Description               string          `json:"description"`
	OperationType             string          `json:"operation_type"`
	OperationSpec             json.RawMessage `json:"operation_spec"`
	Selector                  json.RawMessage `json:"selector"`
	Strategy                  string          `json:"strategy"`
	MaxConcurrent             int32           `json:"max_concurrent"`
	OnError                   string          `json:"on_error"`
	RespectMaintenanceWindows *bool           `json:"respect_maintenance_windows,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────
// Validation
// ─────────────────────────────────────────────────────────────────────

// fleetOpTypesImplemented are the operation types the orchestrator in
// this slice actually knows how to dispatch. The others land here as
// 400 at create time — better to fail fast than to enqueue a fanout
// that's going to skip every target.
var fleetOpTypesImplemented = map[string]struct{}{
	tasks.FleetOpTypeToolUpgrade:   {},
	tasks.FleetOpTypeToolInstall:   {},
	tasks.FleetOpTypeToolUninstall: {},
	tasks.FleetOpTypeApplyTemplate: {},
}

// fleetOpTypesReserved are the operation types we'll accept the name
// of but can't dispatch yet. Marking them reserved lets the UI ship a
// "drain namespaces" form behind a feature flag without the API
// throwing a hard 400; the orchestrator will mark targets skipped
// with a clear last_error.
//
// We currently treat reserved as 400 in this slice to keep the API
// contract honest — once drain_namespaces et al. ship, move them to
// the implemented map. This map exists so a future code reader knows
// the distinction.
var fleetOpTypesReserved = map[string]struct{}{
	tasks.FleetOpTypeDrainNamespaces:  {},
	tasks.FleetOpTypeRotateAgentToken: {},
	tasks.FleetOpTypeCustomHelm:       {},
}

// validateFleetOperation validates a CreateFleetOperationRequest. The
// rules are deliberately strict (reject unknown operation_type, reject
// empty selector, clamp max_concurrent into [1, 100]) so an operator
// can't accidentally fanout across the whole fleet or DDoS the
// management plane with a max_concurrent=10000 request.
func validateFleetOperation(req *CreateFleetOperationRequest) error {
	if strings.TrimSpace(req.Name) == "" {
		return errors.New("name is required")
	}
	if req.OperationType == "" {
		return errors.New("operation_type is required")
	}
	if _, ok := fleetOpTypesImplemented[req.OperationType]; !ok {
		if _, reserved := fleetOpTypesReserved[req.OperationType]; reserved {
			return fmt.Errorf("operation_type %q is reserved but not yet implemented", req.OperationType)
		}
		return fmt.Errorf("operation_type %q is not a known fleet operation", req.OperationType)
	}
	// Selector — non-empty + parseable.
	sel, err := tasks.ParseFleetSelector(req.Selector)
	if err != nil {
		return fmt.Errorf("invalid selector: %w", err)
	}
	if sel.IsEmpty() {
		return errors.New("selector must contain at least one matchLabels entry or matchExpressions row")
	}
	// Strategy / max_concurrent / on_error — defaults applied here so
	// the create path's call site stays readable.
	switch req.Strategy {
	case "":
		req.Strategy = tasks.FleetStrategyParallel
	case tasks.FleetStrategyParallel, tasks.FleetStrategySequential:
		// ok
	default:
		return fmt.Errorf("strategy must be one of sequential|parallel, got %q", req.Strategy)
	}
	if req.MaxConcurrent <= 0 {
		req.MaxConcurrent = 3
	}
	if req.MaxConcurrent > 100 {
		return errors.New("max_concurrent must be <= 100")
	}
	if req.Strategy == tasks.FleetStrategySequential {
		// Sequential forces max_concurrent to 1 — the orchestrator's
		// runtime cap is belt-and-suspenders.
		req.MaxConcurrent = 1
	}
	switch req.OnError {
	case "":
		req.OnError = tasks.FleetOnErrorAbort
	case tasks.FleetOnErrorAbort, tasks.FleetOnErrorContinue:
		// ok
	default:
		return fmt.Errorf("on_error must be one of abort|continue, got %q", req.OnError)
	}
	// Per-type operation_spec sanity check. Detailed shape checks happen
	// at dispatch time; this is just "did you put SOMETHING parseable
	// in the spec?".
	switch req.OperationType {
	case tasks.FleetOpTypeToolUpgrade, tasks.FleetOpTypeToolInstall, tasks.FleetOpTypeToolUninstall:
		if err := validateFleetToolSpec(req.OperationSpec); err != nil {
			return err
		}
	case tasks.FleetOpTypeApplyTemplate:
		if err := validateFleetApplyTemplateSpec(req.OperationSpec); err != nil {
			return err
		}
	}
	return nil
}

func validateFleetToolSpec(raw json.RawMessage) error {
	if len(raw) == 0 {
		return errors.New("operation_spec is required for tool operations")
	}
	var s struct {
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return fmt.Errorf("operation_spec: %w", err)
	}
	if strings.TrimSpace(s.Slug) == "" {
		return errors.New("operation_spec.slug is required for tool operations")
	}
	return nil
}

func validateFleetApplyTemplateSpec(raw json.RawMessage) error {
	if len(raw) == 0 {
		return errors.New("operation_spec is required for apply_template")
	}
	var s struct {
		TemplateID string `json:"template_id"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return fmt.Errorf("operation_spec: %w", err)
	}
	if _, err := uuid.Parse(s.TemplateID); err != nil {
		return errors.New("operation_spec.template_id must be a UUID")
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────
// Endpoints
// ─────────────────────────────────────────────────────────────────────

// List handles GET /api/v1/fleet-operations/.
func (h *FleetOperationHandler) List(w http.ResponseWriter, r *http.Request) {
	status := pgtype.Text{}
	if s := r.URL.Query().Get("status"); s != "" {
		status = pgtype.Text{String: s, Valid: true}
	}
	items, err := h.queries.ListFleetOperations(r.Context(), sqlc.ListFleetOperationsParams{
		Status:      status,
		QueryLimit:  int32(queryInt(r, "limit", 20)),
		QueryOffset: int32(queryInt(r, "offset", 0)),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list fleet operations")
		return
	}
	total, _ := h.queries.CountFleetOperations(r.Context(), status)
	resp := make([]FleetOperationResponse, 0, len(items))
	for _, op := range items {
		resp = append(resp, fleetOperationToResponse(op))
	}
	RespondPaginated(w, r, resp, total)
}

// Get handles GET /api/v1/fleet-operations/{id}/.
func (h *FleetOperationHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid fleet operation ID")
		return
	}
	op, err := h.queries.GetFleetOperation(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Fleet operation not found")
		return
	}
	RespondJSON(w, http.StatusOK, fleetOperationToResponse(op))
}

// Create handles POST /api/v1/fleet-operations/. The orchestrator
// picks up the freshly-inserted pending row on its next tick (every
// 10s); when a trigger is wired the handler nudges the worker
// immediately so an operator's click doesn't wait for the period.
func (h *FleetOperationHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateFleetOperationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	if err := validateFleetOperation(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	respectMW := true
	if req.RespectMaintenanceWindows != nil {
		respectMW = *req.RespectMaintenanceWindows
	}
	op, err := h.queries.CreateFleetOperation(r.Context(), sqlc.CreateFleetOperationParams{
		Name:                      req.Name,
		Description:               req.Description,
		OperationType:             req.OperationType,
		OperationSpec:             normalizeJSON(req.OperationSpec),
		Selector:                  normalizeJSON(req.Selector),
		Strategy:                  req.Strategy,
		MaxConcurrent:             req.MaxConcurrent,
		OnError:                   req.OnError,
		RespectMaintenanceWindows: respectMW,
		CreatedBy:                 currentUserUUID(r),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create fleet operation")
		return
	}
	recordAudit(r, h.queries, "fleet.operation.created", "fleet_operation", op.ID.String(), op.Name, map[string]any{
		"operation_type": op.OperationType,
		"strategy":       op.Strategy,
		"max_concurrent": op.MaxConcurrent,
		"on_error":       op.OnError,
	})
	h.kickOrchestrator(r.Context())
	RespondJSON(w, http.StatusCreated, fleetOperationToResponse(op))
}

// ListTargets handles GET /api/v1/fleet-operations/{id}/targets/.
func (h *FleetOperationHandler) ListTargets(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid fleet operation ID")
		return
	}
	// Ensure the operation exists so we 404 cleanly rather than
	// returning an empty list for a typo.
	if _, err := h.queries.GetFleetOperation(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Fleet operation not found")
		return
	}
	items, err := h.queries.ListFleetOperationTargets(r.Context(), sqlc.ListFleetOperationTargetsParams{
		OperationID: id,
		QueryLimit:  int32(queryInt(r, "limit", 50)),
		QueryOffset: int32(queryInt(r, "offset", 0)),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list fleet targets")
		return
	}
	total, _ := h.queries.CountFleetOperationTargets(r.Context(), id)
	resp := make([]FleetOperationTargetResponse, 0, len(items))
	for _, t := range items {
		resp = append(resp, fleetTargetToResponse(t))
	}
	RespondPaginated(w, r, resp, total)
}

// Pause handles POST /api/v1/fleet-operations/{id}/pause/.
// running -> paused. Pause is the operator's "kill switch" — the
// orchestrator stops dispatching new targets but in-flight targets
// run to completion. Paused operations resume from where they left
// off; an operator who wants to stop in-flight work uses abort.
func (h *FleetOperationHandler) Pause(w http.ResponseWriter, r *http.Request) {
	h.transitionStatus(w, r, []string{tasks.FleetOpStatusRunning}, tasks.FleetOpStatusPaused, "paused", "fleet.operation.paused")
}

// Resume handles POST /api/v1/fleet-operations/{id}/resume/.
// paused -> running.
func (h *FleetOperationHandler) Resume(w http.ResponseWriter, r *http.Request) {
	h.transitionStatus(w, r, []string{tasks.FleetOpStatusPaused}, tasks.FleetOpStatusRunning, "", "fleet.operation.resumed")
}

// Abort handles POST /api/v1/fleet-operations/{id}/abort/.
// Any non-terminal -> aborted. In-flight targets are NOT cancelled
// mid-run; their results are recorded and then the operation ends.
// The operator's intent matters more than the eventual consistency
// here — an operator who clicks abort wants to STOP, even if a
// few in-flight upgrades are about to land.
func (h *FleetOperationHandler) Abort(w http.ResponseWriter, r *http.Request) {
	h.transitionStatus(w, r,
		[]string{tasks.FleetOpStatusPending, tasks.FleetOpStatusRunning, tasks.FleetOpStatusPaused},
		tasks.FleetOpStatusAborted, "aborted by operator", "fleet.operation.aborted",
	)
}

// RetryFailed handles POST /api/v1/fleet-operations/{id}/retry-failed/.
// Re-enqueues every 'failed' target back to 'pending' and, if the
// parent operation is in a terminal-failed state, transitions it
// back to 'running' so the orchestrator picks up the retry.
func (h *FleetOperationHandler) RetryFailed(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid fleet operation ID")
		return
	}
	op, err := h.queries.GetFleetOperation(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Fleet operation not found")
		return
	}
	if err := h.queries.RequeueFailedTargets(r.Context(), id); err != nil {
		RespondError(w, http.StatusInternalServerError, "retry_error", "Failed to requeue failed targets")
		return
	}
	// When the parent is in a terminal state because of those failures
	// (failed / aborted), bump it back to running so the orchestrator
	// reconciles. We leave 'completed' alone — completed has no
	// failures by definition.
	if op.Status == tasks.FleetOpStatusFailed || op.Status == tasks.FleetOpStatusAborted {
		if _, err := h.queries.SetFleetOperationStatus(r.Context(), sqlc.SetFleetOperationStatusParams{
			ID:        id,
			Status:    tasks.FleetOpStatusRunning,
			LastError: "",
		}); err != nil {
			RespondError(w, http.StatusInternalServerError, "retry_error", "Failed to reset operation status")
			return
		}
	}
	recordAudit(r, h.queries, "fleet.operation.retry_failed", "fleet_operation", id.String(), op.Name, nil)
	h.kickOrchestrator(r.Context())
	op, _ = h.queries.GetFleetOperation(r.Context(), id)
	RespondJSON(w, http.StatusAccepted, fleetOperationToResponse(op))
}

// transitionStatus is the shared body of Pause / Resume / Abort.
// allowedFrom is the list of source statuses the transition is valid
// from; trying to pause a paused operation returns a 409.
func (h *FleetOperationHandler) transitionStatus(w http.ResponseWriter, r *http.Request, allowedFrom []string, to, lastErr, auditAction string) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid fleet operation ID")
		return
	}
	op, err := h.queries.GetFleetOperation(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondError(w, http.StatusNotFound, "not_found", "Fleet operation not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, "lookup_error", "Failed to load fleet operation")
		return
	}
	if !fleetContainsString(allowedFrom, op.Status) {
		RespondError(w, http.StatusConflict, "invalid_transition",
			fmt.Sprintf("Cannot transition from %q to %q", op.Status, to))
		return
	}
	updated, err := h.queries.SetFleetOperationStatus(r.Context(), sqlc.SetFleetOperationStatusParams{
		ID:        id,
		Status:    to,
		LastError: lastErr,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "update_error", "Failed to transition status")
		return
	}
	recordAudit(r, h.queries, auditAction, "fleet_operation", id.String(), op.Name, map[string]any{
		"from": op.Status,
		"to":   to,
	})
	h.kickOrchestrator(r.Context())
	RespondJSON(w, http.StatusAccepted, fleetOperationToResponse(updated))
}

// kickOrchestrator nudges the worker — optional, no-op when no
// trigger is wired (the periodic scheduler still drives forward
// progress on a 10s cadence).
func (h *FleetOperationHandler) kickOrchestrator(ctx context.Context) {
	if h == nil || h.trigger == nil {
		return
	}
	h.trigger.TriggerFleetOrchestrate(ctx)
}

// fleetContainsString is a tiny helper for the allowedFrom guard.
// Stand-alone (rather than importing slices) so the handler keeps
// compiling against the same Go version as the rest of the package.
func fleetContainsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// normalizeJSON turns a nil RawMessage into an empty object so the DB
// JSONB column never holds a JSON null. Mirrors the cluster_templates
// handler's behaviour for the same reason.
func normalizeJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}
