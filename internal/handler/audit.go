package handler

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// AuditQuerier abstracts audit-log-related database queries.
type AuditQuerier interface {
	GetAuditLogByID(ctx context.Context, id uuid.UUID) (sqlc.AuditLog, error)
	ListAuditLogs(ctx context.Context, arg sqlc.ListAuditLogsParams) ([]sqlc.AuditLog, error)
	ListAuditLogsByUser(ctx context.Context, arg sqlc.ListAuditLogsByUserParams) ([]sqlc.AuditLog, error)
	ListAuditLogsByResourceType(ctx context.Context, arg sqlc.ListAuditLogsByResourceTypeParams) ([]sqlc.AuditLog, error)
	ListAuditLogsByAction(ctx context.Context, arg sqlc.ListAuditLogsByActionParams) ([]sqlc.AuditLog, error)
	CountAuditLogs(ctx context.Context) (int64, error)
	CountAuditLogsByUser(ctx context.Context, userID pgtype.UUID) (int64, error)
}

// AuditHandler handles audit log endpoints.
type AuditHandler struct {
	queries AuditQuerier
}

// NewAuditHandler creates a new audit handler.
func NewAuditHandler(queries AuditQuerier) *AuditHandler {
	return &AuditHandler{queries: queries}
}

// AuditLogResponse represents an audit log entry in API responses.
type AuditLogResponse struct {
	ID           string          `json:"id"`
	UserID       *string         `json:"user_id"`
	Action       string          `json:"action"`
	ResourceType string          `json:"resource_type"`
	ResourceID   string          `json:"resource_id"`
	ResourceName string          `json:"resource_name"`
	Detail       json.RawMessage `json:"detail"`
	IPAddress    *string         `json:"ip_address"`
	UserAgent    string          `json:"user_agent"`
	RequestID    string          `json:"request_id"`
	CreatedAt    string          `json:"created_at"`
	UpdatedAt    string          `json:"updated_at"`
}

func auditLogToResponse(a sqlc.AuditLog) AuditLogResponse {
	resp := AuditLogResponse{
		ID:           a.ID.String(),
		Action:       a.Action,
		ResourceType: a.ResourceType,
		ResourceID:   a.ResourceID,
		ResourceName: a.ResourceName,
		Detail:       a.Detail,
		UserAgent:    a.UserAgent,
		RequestID:    a.RequestID,
		CreatedAt:    a.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:    a.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if a.UserID.Valid {
		s := uuid.UUID(a.UserID.Bytes).String()
		resp.UserID = &s
	}
	if a.IpAddress != nil {
		s := a.IpAddress.String()
		resp.IPAddress = &s
	}
	return resp
}

// List handles GET /api/v1/audit/.
// Supports optional query params: user_id, resource_type, action.
func (h *AuditHandler) List(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	userIDStr := r.URL.Query().Get("user_id")
	resourceType := r.URL.Query().Get("resource_type")
	action := r.URL.Query().Get("action")

	var (
		logs  []sqlc.AuditLog
		total int64
		err   error
	)

	switch {
	case userIDStr != "":
		uid, parseErr := uuid.Parse(userIDStr)
		if parseErr != nil {
			RespondError(w, http.StatusBadRequest, "invalid_user_id", "Invalid user_id")
			return
		}
		pgtypeUID := pgtype.UUID{Bytes: uid, Valid: true}
		logs, err = h.queries.ListAuditLogsByUser(r.Context(), sqlc.ListAuditLogsByUserParams{
			UserID: pgtypeUID,
			Limit:  limit,
			Offset: offset,
		})
		if err != nil {
			RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list audit logs")
			return
		}
		total, err = h.queries.CountAuditLogsByUser(r.Context(), pgtypeUID)

	case resourceType != "":
		logs, err = h.queries.ListAuditLogsByResourceType(r.Context(), sqlc.ListAuditLogsByResourceTypeParams{
			ResourceType: resourceType,
			Limit:        limit,
			Offset:       offset,
		})
		if err != nil {
			RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list audit logs")
			return
		}
		total, err = h.queries.CountAuditLogs(r.Context())

	case action != "":
		logs, err = h.queries.ListAuditLogsByAction(r.Context(), sqlc.ListAuditLogsByActionParams{
			Action: action,
			Limit:  limit,
			Offset: offset,
		})
		if err != nil {
			RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list audit logs")
			return
		}
		total, err = h.queries.CountAuditLogs(r.Context())

	default:
		logs, err = h.queries.ListAuditLogs(r.Context(), sqlc.ListAuditLogsParams{
			Limit:  limit,
			Offset: offset,
		})
		if err != nil {
			RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list audit logs")
			return
		}
		total, err = h.queries.CountAuditLogs(r.Context())
	}

	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count audit logs")
		return
	}

	items := make([]AuditLogResponse, 0, len(logs))
	for _, a := range logs {
		items = append(items, auditLogToResponse(a))
	}

	RespondPaginated(w, r, items, total)
}

// Export handles GET /api/v1/audit/export/?format=csv.
// Streams audit log entries as CSV. Same filters as the list endpoint.
func (h *AuditHandler) Export(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "csv"
	}
	if format != "csv" {
		RespondError(w, http.StatusBadRequest, "invalid_format", "Only 'csv' export format is supported")
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="audit_log_export.csv"`)
	w.WriteHeader(http.StatusOK)

	writer := csv.NewWriter(w)
	defer writer.Flush()

	// Header row.
	_ = writer.Write([]string{
		"id", "created_at", "user_id", "action", "resource_type",
		"resource_id", "resource_name", "user_agent", "request_id", "detail",
	})

	userIDStr := r.URL.Query().Get("user_id")
	resourceType := r.URL.Query().Get("resource_type")
	action := r.URL.Query().Get("action")

	const pageSize = 500
	offset := int32(0)
	for {
		logs, err := h.fetchExportPage(r.Context(), userIDStr, resourceType, action, pageSize, offset)
		if err != nil {
			// Mid-stream error: log a CSV row with the error and stop.
			_ = writer.Write([]string{"error", "", "", "", "", "", "", "", "", err.Error()})
			return
		}
		if len(logs) == 0 {
			return
		}
		for _, entry := range logs {
			row := []string{
				entry.ID.String(),
				entry.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
				csvNullableUUID(entry.UserID),
				entry.Action,
				entry.ResourceType,
				entry.ResourceID,
				entry.ResourceName,
				entry.UserAgent,
				entry.RequestID,
				string(entry.Detail),
			}
			_ = writer.Write(row)
		}
		writer.Flush()
		if len(logs) < pageSize {
			return
		}
		offset += pageSize
	}
}

func (h *AuditHandler) fetchExportPage(ctx context.Context, userIDStr, resourceType, action string, limit, offset int32) ([]sqlc.AuditLog, error) {
	switch {
	case userIDStr != "":
		uid, err := uuid.Parse(userIDStr)
		if err != nil {
			return nil, fmt.Errorf("invalid user_id")
		}
		return h.queries.ListAuditLogsByUser(ctx, sqlc.ListAuditLogsByUserParams{
			UserID: pgtype.UUID{Bytes: uid, Valid: true},
			Limit:  limit,
			Offset: offset,
		})
	case resourceType != "":
		return h.queries.ListAuditLogsByResourceType(ctx, sqlc.ListAuditLogsByResourceTypeParams{
			ResourceType: resourceType,
			Limit:        limit,
			Offset:       offset,
		})
	case action != "":
		return h.queries.ListAuditLogsByAction(ctx, sqlc.ListAuditLogsByActionParams{
			Action: action,
			Limit:  limit,
			Offset: offset,
		})
	}
	return h.queries.ListAuditLogs(ctx, sqlc.ListAuditLogsParams{Limit: limit, Offset: offset})
}

func csvNullableUUID(id pgtype.UUID) string {
	if id.Valid {
		return uuid.UUID(id.Bytes).String()
	}
	return ""
}

// Get handles GET /api/v1/audit/{id}/.
func (h *AuditHandler) Get(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid audit log ID")
		return
	}

	auditLog, err := h.queries.GetAuditLogByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Audit log entry not found")
		return
	}

	RespondJSON(w, http.StatusOK, auditLogToResponse(auditLog))
}
