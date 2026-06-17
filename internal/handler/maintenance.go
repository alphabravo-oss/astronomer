// Package handler — migration 057: maintenance windows.
//
// Operator-defined time windows that gate destructive mutations across
// the management plane. The use case is change-management 101: an
// operator declares "no destructive ops between 9am-5pm UTC Monday
// through Friday on tier=prod clusters" and Astronomer either refuses
// the request (409 maintenance_window_active) or defers it (202 +
// deferred_id) until the window opens again.
//
// This file owns the REST surface:
//
//   GET    /api/v1/admin/maintenance-windows/         — list
//   POST   /api/v1/admin/maintenance-windows/         — create
//   GET    /api/v1/admin/maintenance-windows/{id}/    — get
//   PUT    /api/v1/admin/maintenance-windows/{id}/    — update
//   DELETE /api/v1/admin/maintenance-windows/{id}/    — delete
//   GET    /api/v1/admin/maintenance-windows/active/  — currently-active list (operator widget)
//   GET    /api/v1/admin/deferred-operations/         — list deferred ops
//   POST   /api/v1/admin/deferred-operations/{id}/cancel/ — cancel a deferred op
//
// All routes are superuser-gated inside the handler so the failure mode
// is a clean 403 instead of a generic permission rejection. The
// CRUD writers invalidate the evaluator's in-memory cache so operator
// changes take effect immediately rather than after the 30s TTL.

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/robfig/cron/v3"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/maintenance"
)

// MaintenanceQuerier is the slice of *sqlc.Queries the handler needs.
// Defined as an interface so tests can pass narrow fakes.
type MaintenanceQuerier interface {
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	// Window CRUD.
	ListMaintenanceWindows(ctx context.Context) ([]sqlc.MaintenanceWindow, error)
	GetMaintenanceWindow(ctx context.Context, id uuid.UUID) (sqlc.MaintenanceWindow, error)
	GetMaintenanceWindowByName(ctx context.Context, name string) (sqlc.MaintenanceWindow, error)
	CreateMaintenanceWindow(ctx context.Context, arg sqlc.CreateMaintenanceWindowParams) (sqlc.MaintenanceWindow, error)
	UpdateMaintenanceWindow(ctx context.Context, arg sqlc.UpdateMaintenanceWindowParams) (sqlc.MaintenanceWindow, error)
	DeleteMaintenanceWindow(ctx context.Context, id uuid.UUID) error
	// Deferred ops.
	ListDeferredOperations(ctx context.Context, arg sqlc.ListDeferredOperationsParams) ([]sqlc.DeferredOperation, error)
	GetDeferredOperation(ctx context.Context, id uuid.UUID) (sqlc.DeferredOperation, error)
	MarkDeferredCancelled(ctx context.Context, arg sqlc.MarkDeferredCancelledParams) error
	CountDeferredOperations(ctx context.Context) (int64, error)
}

// MaintenanceHandler wraps /api/v1/admin/maintenance-windows/* and the
// deferred-operations admin surface.
type MaintenanceHandler struct {
	queries   MaintenanceQuerier
	evaluator *maintenance.Evaluator
}

// NewMaintenanceHandler builds a MaintenanceHandler. evaluator may be
// nil; the handler still operates CRUD-side, the in-memory cache just
// won't be invalidated on writes (the 30s TTL will eventually catch up).
func NewMaintenanceHandler(queries MaintenanceQuerier, evaluator *maintenance.Evaluator) *MaintenanceHandler {
	return &MaintenanceHandler{queries: queries, evaluator: evaluator}
}

// MaintenanceWindowResponse is the wire shape for a single window.
type MaintenanceWindowResponse struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Description     string            `json:"description"`
	Mode            string            `json:"mode"`
	CronOpen        string            `json:"cron_open"`
	DurationMinutes int               `json:"duration_minutes"`
	Timezone        string            `json:"timezone"`
	ClusterSelector map[string]string `json:"cluster_selector"`
	OperationTypes  []string          `json:"operation_types"`
	OnBlock         string            `json:"on_block"`
	Enabled         bool              `json:"enabled"`
	CreatedBy       string            `json:"created_by,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// ActiveMaintenanceWindowResponse extends the base shape with the
// computed open/close timestamps for the operator dashboard widget.
type ActiveMaintenanceWindowResponse struct {
	MaintenanceWindowResponse
	Active    bool      `json:"active"`
	NextOpen  time.Time `json:"next_open"`
	NextClose time.Time `json:"next_close,omitempty"`
}

// MaintenanceWindowRequest is the wire shape for POST/PUT bodies. Empty
// strings on optional fields default to the schema defaults.
type MaintenanceWindowRequest struct {
	Name            string            `json:"name"`
	Description     string            `json:"description"`
	Mode            string            `json:"mode"`
	CronOpen        string            `json:"cron_open"`
	DurationMinutes int               `json:"duration_minutes"`
	Timezone        string            `json:"timezone"`
	ClusterSelector map[string]string `json:"cluster_selector"`
	OperationTypes  []string          `json:"operation_types"`
	OnBlock         string            `json:"on_block"`
	Enabled         *bool             `json:"enabled"`
}

// DeferredOperationResponse is the wire shape for a single deferred op.
type DeferredOperationResponse struct {
	ID              string          `json:"id"`
	WindowID        string          `json:"window_id"`
	OperationType   string          `json:"operation_type"`
	OperationSpec   json.RawMessage `json:"operation_spec"`
	TargetClusterID string          `json:"target_cluster_id,omitempty"`
	TargetProjectID string          `json:"target_project_id,omitempty"`
	Status          string          `json:"status"`
	DeferredUntil   *time.Time      `json:"deferred_until,omitempty"`
	ExpiresAt       *time.Time      `json:"expires_at,omitempty"`
	RequestedBy     string          `json:"requested_by,omitempty"`
	LastError       string          `json:"last_error"`
	DispatchedAt    *time.Time      `json:"dispatched_at,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// List handles GET /api/v1/admin/maintenance-windows/.
func (h *MaintenanceHandler) List(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	rows, err := h.queries.ListMaintenanceWindows(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	out := make([]MaintenanceWindowResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, windowToWire(row))
	}
	// ListMaintenanceWindows returns every window unpaginated; there is no
	// COUNT query, so Total is the page length. // TODO(total)
	limit, offset := queryLimitOffset(r, 20)
	RespondList(w, out, NewPagination(len(out), limit, offset, len(out)))
}

// Get handles GET /api/v1/admin/maintenance-windows/{id}/.
func (h *MaintenanceHandler) Get(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid window ID")
		return
	}
	row, err := h.queries.GetMaintenanceWindow(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Window not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, windowToWire(row))
}

// Create handles POST /api/v1/admin/maintenance-windows/.
func (h *MaintenanceHandler) Create(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	var req MaintenanceWindowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	req = applyRequestDefaults(req)
	if msg, ok := validateRequest(req); !ok {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, msg)
		return
	}
	if _, err := h.queries.GetMaintenanceWindowByName(r.Context(), req.Name); err == nil {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "A window with that name already exists")
		return
	}
	sel, _ := json.Marshal(req.ClusterSelector)
	if string(sel) == "null" {
		sel = []byte("{}")
	}
	ops, _ := json.Marshal(req.OperationTypes)
	if string(ops) == "null" {
		ops = []byte("[]")
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	row, err := h.queries.CreateMaintenanceWindow(r.Context(), sqlc.CreateMaintenanceWindowParams{
		Name:            req.Name,
		Description:     req.Description,
		Mode:            req.Mode,
		CronOpen:        req.CronOpen,
		DurationMinutes: int32(req.DurationMinutes),
		Timezone:        req.Timezone,
		ClusterSelector: sel,
		OperationTypes:  ops,
		OnBlock:         req.OnBlock,
		Enabled:         enabled,
		CreatedBy:       currentUserUUID(r),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, err.Error())
		return
	}
	h.invalidate()
	recordAudit(r, h.queries, "admin.maintenance_window.created", "maintenance_window", row.ID.String(), row.Name, map[string]any{
		"mode":     row.Mode,
		"on_block": row.OnBlock,
		"enabled":  row.Enabled,
	})
	w.Header().Set("Location", "/api/v1/admin/maintenance-windows/"+row.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, windowToWire(row))
}

// Update handles PUT /api/v1/admin/maintenance-windows/{id}/.
func (h *MaintenanceHandler) Update(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid window ID")
		return
	}
	existing, err := h.queries.GetMaintenanceWindow(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Window not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	var req MaintenanceWindowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	req = applyRequestDefaults(req)
	if msg, ok := validateRequest(req); !ok {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, msg)
		return
	}
	if req.Name != existing.Name {
		if other, err := h.queries.GetMaintenanceWindowByName(r.Context(), req.Name); err == nil && other.ID != id {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "A different window already uses that name")
			return
		}
	}
	sel, _ := json.Marshal(req.ClusterSelector)
	if string(sel) == "null" {
		sel = []byte("{}")
	}
	ops, _ := json.Marshal(req.OperationTypes)
	if string(ops) == "null" {
		ops = []byte("[]")
	}
	enabled := existing.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	row, err := h.queries.UpdateMaintenanceWindow(r.Context(), sqlc.UpdateMaintenanceWindowParams{
		ID:              id,
		Name:            req.Name,
		Description:     req.Description,
		Mode:            req.Mode,
		CronOpen:        req.CronOpen,
		DurationMinutes: int32(req.DurationMinutes),
		Timezone:        req.Timezone,
		ClusterSelector: sel,
		OperationTypes:  ops,
		OnBlock:         req.OnBlock,
		Enabled:         enabled,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, err.Error())
		return
	}
	h.invalidate()
	recordAudit(r, h.queries, "admin.maintenance_window.updated", "maintenance_window", row.ID.String(), row.Name, map[string]any{
		"mode":     row.Mode,
		"on_block": row.OnBlock,
		"enabled":  row.Enabled,
	})
	RespondJSON(w, http.StatusOK, windowToWire(row))
}

// Delete handles DELETE /api/v1/admin/maintenance-windows/{id}/.
func (h *MaintenanceHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid window ID")
		return
	}
	existing, err := h.queries.GetMaintenanceWindow(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Window not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	if err := h.queries.DeleteMaintenanceWindow(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DeleteError, err.Error())
		return
	}
	h.invalidate()
	recordAudit(r, h.queries, "admin.maintenance_window.deleted", "maintenance_window", id.String(), existing.Name, nil)
	w.WriteHeader(http.StatusNoContent)
}

// ListActive handles GET /api/v1/admin/maintenance-windows/active/.
// Used by the dashboard widget. Returns one row per enabled window with
// the active flag + next-open / next-close timestamps already computed.
func (h *MaintenanceHandler) ListActive(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	rows, err := h.queries.ListMaintenanceWindows(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	now := time.Now().UTC()
	out := make([]ActiveMaintenanceWindowResponse, 0, len(rows))
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		win, err := maintenance.FromSQLC(row)
		if err != nil {
			continue
		}
		active, _ := maintenance.IsActive(win, now)
		next := maintenance.NextOpen(win, now)
		entry := ActiveMaintenanceWindowResponse{
			MaintenanceWindowResponse: windowToWire(row),
			Active:                    active,
			NextOpen:                  next,
		}
		if active {
			entry.NextClose = maintenance.NextClose(win, now)
		}
		out = append(out, entry)
	}
	// The active-windows widget returns the filtered enabled set unpaginated;
	// there is no COUNT query, so Total is the page length. // TODO(total)
	limit, offset := queryLimitOffset(r, 20)
	RespondList(w, out, NewPagination(len(out), limit, offset, len(out)))
}

// ListDeferred handles GET /api/v1/admin/deferred-operations/.
func (h *MaintenanceHandler) ListDeferred(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	limit, offset := queryLimitOffset(r, 50)
	rows, err := h.queries.ListDeferredOperations(r.Context(), sqlc.ListDeferredOperationsParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	total, err := h.queries.CountDeferredOperations(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	out := make([]DeferredOperationResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, deferredToWire(row))
	}
	RespondPaginated(w, r, out, total)
}

// CancelDeferred handles POST /api/v1/admin/deferred-operations/{id}/cancel/.
func (h *MaintenanceHandler) CancelDeferred(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid deferred-operation ID")
		return
	}
	row, err := h.queries.GetDeferredOperation(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Deferred operation not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	if row.Status != "pending" {
		RespondRequestError(w, r, http.StatusConflict, apierror.NotCancellable,
			"Only pending operations can be cancelled; this one is "+row.Status)

		return
	}
	if err := h.queries.MarkDeferredCancelled(r.Context(), sqlc.MarkDeferredCancelledParams{
		ID:        id,
		LastError: "cancelled by operator",
	}); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CancelFailed, err.Error())
		return
	}
	recordAudit(r, h.queries, "admin.deferred_operation.cancelled", "deferred_operation", id.String(), row.OperationType, map[string]any{
		"window_id": row.WindowID.String(),
	})
	w.WriteHeader(http.StatusNoContent)
}

// gate enforces superuser-only access. Returns true if the request may
// proceed; emits 401/403 otherwise. Same pattern as admin_queues.go.
func (h *MaintenanceHandler) gate(w http.ResponseWriter, r *http.Request) bool {
	_, ok := requireSuperuser(w, r, h.queries, superuserGateConfig{
		StoreUnavailableMessage: "Maintenance store not configured",
		ForbiddenMessage:        "Maintenance windows require superuser privileges",
	})
	return ok
}

func (h *MaintenanceHandler) invalidate() {
	if h == nil || h.evaluator == nil {
		return
	}
	h.evaluator.Invalidate()
}

// applyRequestDefaults fills in schema defaults for empty fields so the
// operator can POST a minimal body.
func applyRequestDefaults(req MaintenanceWindowRequest) MaintenanceWindowRequest {
	if req.Mode == "" {
		req.Mode = maintenance.ModeBlackout
	}
	if req.Timezone == "" {
		req.Timezone = "UTC"
	}
	if req.OnBlock == "" {
		req.OnBlock = maintenance.OnBlockRefuse
	}
	if req.DurationMinutes == 0 {
		req.DurationMinutes = 60
	}
	return req
}

// validateRequest enforces the same constraints the DB does, plus the
// "is this actually a parseable cron expression" check the schema can't
// express. Returns (msg, ok=false) on failure.
func validateRequest(req MaintenanceWindowRequest) (string, bool) {
	if strings.TrimSpace(req.Name) == "" {
		return "name is required", false
	}
	if req.Mode != maintenance.ModeBlackout && req.Mode != maintenance.ModePermitted {
		return "mode must be 'blackout' or 'permitted'", false
	}
	if req.OnBlock != maintenance.OnBlockRefuse && req.OnBlock != maintenance.OnBlockDefer {
		return "on_block must be 'refuse' or 'defer'", false
	}
	if strings.TrimSpace(req.CronOpen) == "" {
		return "cron_open is required", false
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	if _, err := parser.Parse(strings.TrimSpace(req.CronOpen)); err != nil {
		return "cron_open is not a valid 5-field cron expression: " + err.Error(), false
	}
	if _, err := time.LoadLocation(req.Timezone); err != nil {
		return "timezone is not a valid IANA name: " + err.Error(), false
	}
	if req.DurationMinutes < 1 {
		return "duration_minutes must be >= 1", false
	}
	if req.DurationMinutes > 7*24*60 {
		return "duration_minutes cannot exceed one week", false
	}
	for _, opType := range req.OperationTypes {
		if !isKnownOpType(opType) {
			return "unknown operation_type: " + opType, false
		}
	}
	return "", true
}

// isKnownOpType returns true if opType matches one of the registered
// destructive operation labels. Validating up front catches typos in
// the operator's request body before the window is silently never
// applied.
func isKnownOpType(opType string) bool {
	for _, t := range maintenance.KnownOperationTypes {
		if t == opType {
			return true
		}
	}
	return false
}

// windowToWire converts a sqlc.MaintenanceWindow row into the JSON wire
// shape the handler emits. Best-effort decode of JSONB columns: a
// corrupt blob ends up as an empty selector / op list rather than a
// 500, so the operator sees the stale-but-readable row.
func windowToWire(row sqlc.MaintenanceWindow) MaintenanceWindowResponse {
	out := MaintenanceWindowResponse{
		ID:              row.ID.String(),
		Name:            row.Name,
		Description:     row.Description,
		Mode:            row.Mode,
		CronOpen:        row.CronOpen,
		DurationMinutes: int(row.DurationMinutes),
		Timezone:        row.Timezone,
		ClusterSelector: map[string]string{},
		OperationTypes:  []string{},
		OnBlock:         row.OnBlock,
		Enabled:         row.Enabled,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}
	if len(row.ClusterSelector) > 0 {
		_ = json.Unmarshal(row.ClusterSelector, &out.ClusterSelector)
	}
	if len(row.OperationTypes) > 0 {
		_ = json.Unmarshal(row.OperationTypes, &out.OperationTypes)
	}
	if row.CreatedBy.Valid {
		out.CreatedBy = uuid.UUID(row.CreatedBy.Bytes).String()
	}
	return out
}

func deferredToWire(row sqlc.DeferredOperation) DeferredOperationResponse {
	out := DeferredOperationResponse{
		ID:            row.ID.String(),
		WindowID:      row.WindowID.String(),
		OperationType: row.OperationType,
		OperationSpec: row.OperationSpec,
		Status:        row.Status,
		LastError:     row.LastError,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
	if row.TargetClusterID.Valid {
		out.TargetClusterID = uuid.UUID(row.TargetClusterID.Bytes).String()
	}
	if row.TargetProjectID.Valid {
		out.TargetProjectID = uuid.UUID(row.TargetProjectID.Bytes).String()
	}
	if row.DeferredUntil.Valid {
		t := row.DeferredUntil.Time
		out.DeferredUntil = &t
	}
	if row.ExpiresAt.Valid {
		t := row.ExpiresAt.Time
		out.ExpiresAt = &t
	}
	if row.DispatchedAt.Valid {
		t := row.DispatchedAt.Time
		out.DispatchedAt = &t
	}
	if row.RequestedBy.Valid {
		out.RequestedBy = uuid.UUID(row.RequestedBy.Bytes).String()
	}
	return out
}

// Compile-time assertion that *pgtype.UUID convert helpers exist, so a
// future refactor catches the signature change.
var _ = pgtype.UUID{}
