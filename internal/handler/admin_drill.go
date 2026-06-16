// Package handler — backup restore drill reporting.
//
// Surfaces the rows the management-plane-restore-drill CronJob writes
// (see deploy/chart/templates/management-plane-restore-drill-cronjob.yaml).
// The drill itself is the audit half of NIST CP-9 / ISO 27001 A.12.3.1
// — "backups exist" is necessary but not sufficient; we also need a
// recorded proof that they were restorable. This handler is the
// dashboard's read view onto that proof:
//
//	GET /api/v1/admin/backup-drill/           — latest result + age in seconds
//	GET /api/v1/admin/backup-drill/history/   — paginated history
//
// Both endpoints are superuser-gated inside the handler (same pattern as
// admin_queues.go) rather than via middleware so the failure mode is a
// clean 403 instead of a generic permission rejection.
package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// AdminDrillQuerier is the slice of sqlc.Queries the handler needs.
// Narrow on purpose — the handler only needs to gate on superuser and
// read drill rows, so tests can satisfy it with a tiny fake.
type AdminDrillQuerier interface {
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	GetLatestBackupDrillResult(ctx context.Context) (sqlc.BackupDrillResult, error)
	GetLatestSuccessfulBackupDrillResult(ctx context.Context) (sqlc.BackupDrillResult, error)
	ListBackupDrillResults(ctx context.Context, arg sqlc.ListBackupDrillResultsParams) ([]sqlc.BackupDrillResult, error)
	CountBackupDrillResults(ctx context.Context) (int64, error)
}

// AdminDrillHandler wraps GET /api/v1/admin/backup-drill/*.
type AdminDrillHandler struct {
	queries AdminDrillQuerier
}

// NewAdminDrillHandler returns a usable handler. queries may be nil for
// degenerate installs that disable the management DB; the handler then
// renders 503.
func NewAdminDrillHandler(queries AdminDrillQuerier) *AdminDrillHandler {
	return &AdminDrillHandler{queries: queries}
}

// BackupDrillResult is the wire shape for a single drill row. Distinct
// from sqlc.BackupDrillResult because the JSON wants nullable fields as
// pointers (instead of pgtype wrappers) and "seconds since" computed.
type BackupDrillResult struct {
	ID            string     `json:"id"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
	Status        string     `json:"status"`
	BackupKey     string     `json:"backup_key"`
	SchemaVersion *int32     `json:"schema_version"`
	ErrorMessage  string     `json:"error_message"`
	CreatedAt     time.Time  `json:"created_at"`
}

// BackupDrillLatestResponse is the wire shape for GET /admin/backup-drill/.
// "AgeSeconds" is the gap between now and the most recent successful
// drill; the dashboard banner shows red when it exceeds the Prometheus
// staleness threshold (14d). NULL when no successful drill has ever run.
type BackupDrillLatestResponse struct {
	Latest                  *BackupDrillResult `json:"latest"`
	LatestSuccess           *BackupDrillResult `json:"latest_success"`
	LatestSuccessAgeSeconds *float64           `json:"latest_success_age_seconds"`
}

// GetLatest handles GET /api/v1/admin/backup-drill/.
func (h *AdminDrillHandler) GetLatest(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}

	resp := BackupDrillLatestResponse{}

	latest, err := h.queries.GetLatestBackupDrillResult(r.Context())
	switch {
	case err == nil:
		out := toWireResult(latest)
		resp.Latest = &out
	case errors.Is(err, pgx.ErrNoRows):
		// No drill has ever run — that's a valid state pre-first-run, so
		// don't 500. The dashboard will show "never run" and the
		// staleness alert will fire on the metric side.
	default:
		RespondRequestError(w, r, http.StatusInternalServerError, "db_error", err.Error())
		return
	}

	success, err := h.queries.GetLatestSuccessfulBackupDrillResult(r.Context())
	switch {
	case err == nil:
		out := toWireResult(success)
		resp.LatestSuccess = &out
		age := time.Since(success.StartedAt).Seconds()
		resp.LatestSuccessAgeSeconds = &age
	case errors.Is(err, pgx.ErrNoRows):
		// No successful drill yet.
	default:
		RespondRequestError(w, r, http.StatusInternalServerError, "db_error", err.Error())
		return
	}

	RespondJSON(w, http.StatusOK, resp)
}

// ListHistory handles GET /api/v1/admin/backup-drill/history/.
func (h *AdminDrillHandler) ListHistory(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}

	limit, offset := queryLimitOffset(r, 20)

	rows, err := h.queries.ListBackupDrillResults(r.Context(), sqlc.ListBackupDrillResultsParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "db_error", err.Error())
		return
	}

	total, err := h.queries.CountBackupDrillResults(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "db_error", err.Error())
		return
	}

	out := make([]BackupDrillResult, 0, len(rows))
	for _, row := range rows {
		out = append(out, toWireResult(row))
	}
	RespondPaginated(w, r, out, total)
}

// toWireResult converts the sqlc row (with pgtype wrappers) into the
// pointer-nullable JSON shape the frontend expects.
func toWireResult(row sqlc.BackupDrillResult) BackupDrillResult {
	out := BackupDrillResult{
		ID:           row.ID.String(),
		StartedAt:    row.StartedAt,
		Status:       row.Status,
		BackupKey:    row.BackupKey,
		ErrorMessage: row.ErrorMessage,
		CreatedAt:    row.CreatedAt,
	}
	if row.FinishedAt.Valid {
		t := row.FinishedAt.Time
		out.FinishedAt = &t
	}
	if row.SchemaVersion.Valid {
		v := row.SchemaVersion.Int32
		out.SchemaVersion = &v
	}
	return out
}

// gate enforces superuser-only access and emits the admin audit row.
// Mirrors the pattern in admin_queues.go so behaviour is identical
// (401 unauth → 403 not-superuser → audit row on success).
func (h *AdminDrillHandler) gate(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := requireSuperuser(w, r, h.queries, superuserGateConfig{
		StoreUnavailableMessage: "Admin store not configured",
		ForbiddenMessage:        "Backup drill view requires superuser privileges",
	}); !ok {
		return false
	}
	recordAudit(r, h.queries, "admin.backup_drill.viewed", "platform", "", "backup_drill", map[string]any{
		"path": r.URL.Path,
	})
	return true
}
