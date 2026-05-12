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

type auditReaderV1 interface {
	GetAuditLogV1ByID(ctx context.Context, id uuid.UUID) (sqlc.AuditLog, error)
	ListAuditLogV1(ctx context.Context, arg sqlc.ListAuditLogsParams) ([]sqlc.AuditLog, error)
	ListAuditLogV1ByUser(ctx context.Context, arg sqlc.ListAuditLogsByUserParams) ([]sqlc.AuditLog, error)
	ListAuditLogV1ByResourceType(ctx context.Context, arg sqlc.ListAuditLogsByResourceTypeParams) ([]sqlc.AuditLog, error)
	ListAuditLogV1ByAction(ctx context.Context, arg sqlc.ListAuditLogsByActionParams) ([]sqlc.AuditLog, error)
	ListAuditLogV1ByActionClass(ctx context.Context, arg sqlc.ListAuditLogsByActionClassParams) ([]sqlc.AuditLog, error)
	ListAuditLogV1Since(ctx context.Context, arg sqlc.ListAuditLogsSinceParams) ([]sqlc.AuditLog, error)
	CountAuditLogV1(ctx context.Context) (int64, error)
	CountAuditLogV1ByUser(ctx context.Context, userID pgtype.UUID) (int64, error)
	CountAuditLogV1ByActionClass(ctx context.Context, actionClass string) (int64, error)
}

// AuditHandler handles audit log endpoints.
type AuditHandler struct {
	queries auditReaderV1
}

// NewAuditHandler creates a new audit handler.
func NewAuditHandler(queries auditReaderV1) *AuditHandler {
	return &AuditHandler{queries: queries}
}

// AuditLogResponse represents an audit log entry in API responses.
type AuditLogResponse struct {
	ID            string          `json:"id"`
	UserID        *string         `json:"user_id"`
	Source        string          `json:"source"`
	CorrelationID string          `json:"correlation_id"`
	Action        string          `json:"action"`
	ActionClass   string          `json:"action_class"`
	ResourceType  string          `json:"resource_type"`
	ResourceID    string          `json:"resource_id"`
	ResourceName  string          `json:"resource_name"`
	Detail        json.RawMessage `json:"detail"`
	IPAddress     *string         `json:"ip_address"`
	UserAgent     string          `json:"user_agent"`
	RequestID     string          `json:"request_id"`
	CreatedAt     string          `json:"created_at"`
	UpdatedAt     string          `json:"updated_at"`
}

func auditLogToResponse(a sqlc.AuditLog) AuditLogResponse {
	resp := AuditLogResponse{
		ID:            a.ID.String(),
		Source:        a.Source,
		CorrelationID: a.CorrelationID,
		Action:        a.Action,
		ActionClass:   a.ActionClass,
		ResourceType:  a.ResourceType,
		ResourceID:    a.ResourceID,
		ResourceName:  a.ResourceName,
		Detail:        a.Detail,
		UserAgent:     a.UserAgent,
		RequestID:     a.RequestID,
		CreatedAt:     a.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:     a.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
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
	actionClass := r.URL.Query().Get("action_class")
	sinceIDStr := r.URL.Query().Get("since")

	var (
		logs  []sqlc.AuditLog
		total int64
		err   error
	)

	switch {
	case actionClass != "":
		logs, err = h.queries.ListAuditLogV1ByActionClass(r.Context(), sqlc.ListAuditLogsByActionClassParams{
			ActionClass: actionClass,
			Limit:       limit,
			Offset:      offset,
		})
		if err != nil {
			RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list audit logs")
			return
		}
		total, err = h.queries.CountAuditLogV1ByActionClass(r.Context(), actionClass)

	case sinceIDStr != "":
		sinceID, parseErr := uuid.Parse(sinceIDStr)
		if parseErr != nil {
			RespondError(w, http.StatusBadRequest, "invalid_since", "Invalid since cursor")
			return
		}
		logs, err = h.listAuditLogsSince(r.Context(), sqlc.ListAuditLogsSinceParams{
			SinceID: sinceID,
			Limit:   limit,
		})
		if err != nil {
			RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list audit logs")
			return
		}
		total = int64(len(logs))
	case userIDStr != "":
		uid, parseErr := uuid.Parse(userIDStr)
		if parseErr != nil {
			RespondError(w, http.StatusBadRequest, "invalid_user_id", "Invalid user_id")
			return
		}
		pgtypeUID := pgtype.UUID{Bytes: uid, Valid: true}
		logs, err = h.listAuditLogsByUser(r.Context(), sqlc.ListAuditLogsByUserParams{
			UserID: pgtypeUID,
			Limit:  limit,
			Offset: offset,
		})
		if err != nil {
			RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list audit logs")
			return
		}
		total, err = h.countAuditLogsByUser(r.Context(), pgtypeUID)

	case resourceType != "":
		logs, err = h.listAuditLogsByResourceType(r.Context(), sqlc.ListAuditLogsByResourceTypeParams{
			ResourceType: resourceType,
			Limit:        limit,
			Offset:       offset,
		})
		if err != nil {
			RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list audit logs")
			return
		}
		total, err = h.countAuditLogs(r.Context())

	case action != "":
		logs, err = h.listAuditLogsByAction(r.Context(), sqlc.ListAuditLogsByActionParams{
			Action: action,
			Limit:  limit,
			Offset: offset,
		})
		if err != nil {
			RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list audit logs")
			return
		}
		total, err = h.countAuditLogs(r.Context())

	default:
		logs, err = h.listAuditLogs(r.Context(), sqlc.ListAuditLogsParams{
			Limit:  limit,
			Offset: offset,
		})
		if err != nil {
			RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list audit logs")
			return
		}
		total, err = h.countAuditLogs(r.Context())
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
		"id", "created_at", "user_id", "source", "correlation_id", "action",
		"resource_type", "resource_id", "resource_name", "user_agent",
		"request_id", "detail",
	})

	userIDStr := r.URL.Query().Get("user_id")
	resourceType := r.URL.Query().Get("resource_type")
	action := r.URL.Query().Get("action")
	sinceIDStr := r.URL.Query().Get("since")
	if sinceIDStr != "" {
		_ = writer.Write([]string{"error", "", "", "", "", "", "", "", "", "", "", "since cursor export is not supported"})
		return
	}

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
				entry.Source,
				entry.CorrelationID,
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
		return h.listAuditLogsByUser(ctx, sqlc.ListAuditLogsByUserParams{
			UserID: pgtype.UUID{Bytes: uid, Valid: true},
			Limit:  limit,
			Offset: offset,
		})
	case resourceType != "":
		return h.listAuditLogsByResourceType(ctx, sqlc.ListAuditLogsByResourceTypeParams{
			ResourceType: resourceType,
			Limit:        limit,
			Offset:       offset,
		})
	case action != "":
		return h.listAuditLogsByAction(ctx, sqlc.ListAuditLogsByActionParams{
			Action: action,
			Limit:  limit,
			Offset: offset,
		})
	}
	return h.listAuditLogs(ctx, sqlc.ListAuditLogsParams{Limit: limit, Offset: offset})
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

	auditLog, err := h.getAuditLogByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Audit log entry not found")
		return
	}

	RespondJSON(w, http.StatusOK, auditLogToResponse(auditLog))
}

func (h *AuditHandler) getAuditLogByID(ctx context.Context, id uuid.UUID) (sqlc.AuditLog, error) {
	return h.queries.GetAuditLogV1ByID(ctx, id)
}

func (h *AuditHandler) listAuditLogs(ctx context.Context, arg sqlc.ListAuditLogsParams) ([]sqlc.AuditLog, error) {
	return h.queries.ListAuditLogV1(ctx, arg)
}

func (h *AuditHandler) listAuditLogsByUser(ctx context.Context, arg sqlc.ListAuditLogsByUserParams) ([]sqlc.AuditLog, error) {
	return h.queries.ListAuditLogV1ByUser(ctx, arg)
}

func (h *AuditHandler) listAuditLogsByResourceType(ctx context.Context, arg sqlc.ListAuditLogsByResourceTypeParams) ([]sqlc.AuditLog, error) {
	return h.queries.ListAuditLogV1ByResourceType(ctx, arg)
}

func (h *AuditHandler) listAuditLogsByAction(ctx context.Context, arg sqlc.ListAuditLogsByActionParams) ([]sqlc.AuditLog, error) {
	return h.queries.ListAuditLogV1ByAction(ctx, arg)
}

func (h *AuditHandler) listAuditLogsSince(ctx context.Context, arg sqlc.ListAuditLogsSinceParams) ([]sqlc.AuditLog, error) {
	return h.queries.ListAuditLogV1Since(ctx, arg)
}

func (h *AuditHandler) countAuditLogs(ctx context.Context) (int64, error) {
	return h.queries.CountAuditLogV1(ctx)
}

func (h *AuditHandler) countAuditLogsByUser(ctx context.Context, userID pgtype.UUID) (int64, error) {
	return h.queries.CountAuditLogV1ByUser(ctx, userID)
}
