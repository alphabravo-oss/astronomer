package handler

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

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

type auditFilterReader interface {
	ListAuditLogV1Filtered(ctx context.Context, arg sqlc.AuditLogFilterParams) ([]sqlc.AuditLog, error)
	CountAuditLogV1Filtered(ctx context.Context, arg sqlc.AuditLogFilterParams) (int64, error)
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
	ID              string          `json:"id"`
	UserID          *string         `json:"user_id"`
	User            string          `json:"user"`
	Source          string          `json:"source"`
	CorrelationID   string          `json:"correlation_id"`
	Action          string          `json:"action"`
	ActionClass     string          `json:"action_class"`
	ResourceType    string          `json:"resource_type"`
	ResourceID      string          `json:"resource_id"`
	ResourceName    string          `json:"resource_name"`
	Detail          json.RawMessage `json:"detail"`
	Details         json.RawMessage `json:"details"`
	ActorAuthMethod string          `json:"actor_auth_method"`
	HTTPMethod      string          `json:"http_method"`
	Path            string          `json:"path"`
	StatusCode      int32           `json:"status_code"`
	Status          string          `json:"status"`
	DurationMs      int64           `json:"duration_ms"`
	IPAddress       *string         `json:"ip_address"`
	SourceIP        string          `json:"source_ip"`
	UserAgent       string          `json:"user_agent"`
	RequestID       string          `json:"request_id"`
	Timestamp       string          `json:"timestamp"`
	CreatedAt       string          `json:"created_at"`
	UpdatedAt       string          `json:"updated_at"`
}

func auditLogToResponse(a sqlc.AuditLog) AuditLogResponse {
	userLabel := "system"
	if a.UserID.Valid {
		userLabel = uuid.UUID(a.UserID.Bytes).String()
	}
	resp := AuditLogResponse{
		ID:              a.ID.String(),
		User:            userLabel,
		Source:          a.Source,
		CorrelationID:   a.CorrelationID,
		Action:          a.Action,
		ActionClass:     a.ActionClass,
		ResourceType:    a.ResourceType,
		ResourceID:      a.ResourceID,
		ResourceName:    a.ResourceName,
		Detail:          a.Detail,
		Details:         a.Detail,
		ActorAuthMethod: a.ActorAuthMethod,
		HTTPMethod:      a.HttpMethod,
		Path:            a.Path,
		StatusCode:      a.StatusCode,
		Status:          auditStatusLabel(a.StatusCode),
		DurationMs:      a.DurationMs,
		UserAgent:       a.UserAgent,
		RequestID:       a.RequestID,
		Timestamp:       a.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		CreatedAt:       a.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:       a.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if a.UserID.Valid {
		s := uuid.UUID(a.UserID.Bytes).String()
		resp.UserID = &s
	}
	if a.IpAddress != nil {
		s := a.IpAddress.String()
		resp.IPAddress = &s
		resp.SourceIP = s
	}
	return resp
}

// List handles GET /api/v1/audit/.
// Supports optional query params: user_id, actor, resource_type, target,
// action, action_class, result, correlation_id, request_id, cluster_id,
// project_id, from, and to.
func (h *AuditHandler) List(w http.ResponseWriter, r *http.Request) {
	limit := auditQueryLimit(r)
	offset := int32(queryInt(r, "offset", 0))
	filter, filterErr := auditFilterFromRequest(r, limit, offset)
	if filterErr != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_filter", filterErr.Error())
		return
	}

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
	case sinceIDStr == "" && supportsFilteredAudit(h.queries):
		filterReader := h.queries.(auditFilterReader)
		logs, err = filterReader.ListAuditLogV1Filtered(r.Context(), filter)
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, "list_error", "Failed to list audit logs")
			return
		}
		total, err = filterReader.CountAuditLogV1Filtered(r.Context(), filter)
	case actionClass != "":
		logs, err = h.queries.ListAuditLogV1ByActionClass(r.Context(), sqlc.ListAuditLogsByActionClassParams{
			ActionClass: actionClass,
			Limit:       limit,
			Offset:      offset,
		})
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, "list_error", "Failed to list audit logs")
			return
		}
		total, err = h.queries.CountAuditLogV1ByActionClass(r.Context(), actionClass)

	case sinceIDStr != "":
		sinceID, parseErr := uuid.Parse(sinceIDStr)
		if parseErr != nil {
			RespondRequestError(w, r, http.StatusBadRequest, "invalid_since", "Invalid since cursor")
			return
		}
		logs, err = h.listAuditLogsSince(r.Context(), sqlc.ListAuditLogsSinceParams{
			SinceID: sinceID,
			Limit:   limit,
		})
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, "list_error", "Failed to list audit logs")
			return
		}
		total = int64(len(logs))
	case userIDStr != "":
		uid, parseErr := uuid.Parse(userIDStr)
		if parseErr != nil {
			RespondRequestError(w, r, http.StatusBadRequest, "invalid_user_id", "Invalid user_id")
			return
		}
		pgtypeUID := pgtype.UUID{Bytes: uid, Valid: true}
		logs, err = h.listAuditLogsByUser(r.Context(), sqlc.ListAuditLogsByUserParams{
			UserID: pgtypeUID,
			Limit:  limit,
			Offset: offset,
		})
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, "list_error", "Failed to list audit logs")
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
			RespondRequestError(w, r, http.StatusInternalServerError, "list_error", "Failed to list audit logs")
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
			RespondRequestError(w, r, http.StatusInternalServerError, "list_error", "Failed to list audit logs")
			return
		}
		total, err = h.countAuditLogs(r.Context())

	default:
		logs, err = h.listAuditLogs(r.Context(), sqlc.ListAuditLogsParams{
			Limit:  limit,
			Offset: offset,
		})
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, "list_error", "Failed to list audit logs")
			return
		}
		total, err = h.countAuditLogs(r.Context())
	}

	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "count_error", "Failed to count audit logs")
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
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_format", "Only 'csv' export format is supported")
		return
	}

	filter, filterErr := auditFilterFromRequest(r, 500, 0)
	if filterErr != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_filter", filterErr.Error())
		return
	}
	sinceIDStr := r.URL.Query().Get("since")
	if sinceIDStr != "" {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_since", "since cursor export is not supported")
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
		"action_class", "resource_type", "resource_id", "resource_name",
		"http_method", "path", "status_code", "duration_ms", "ip_address",
		"user_agent", "request_id", "detail",
	})

	const pageSize = 500
	offset := int32(0)
	for {
		logs, err := h.fetchExportPage(r.Context(), filter, pageSize, offset)
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
				entry.ActionClass,
				entry.ResourceType,
				entry.ResourceID,
				entry.ResourceName,
				entry.HttpMethod,
				entry.Path,
				strconv.Itoa(int(entry.StatusCode)),
				strconv.FormatInt(entry.DurationMs, 10),
				csvNullableIP(entry.IpAddress),
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

func (h *AuditHandler) fetchExportPage(ctx context.Context, filter sqlc.AuditLogFilterParams, limit, offset int32) ([]sqlc.AuditLog, error) {
	filter.Limit = limit
	filter.Offset = offset
	if filterReader, ok := h.queries.(auditFilterReader); ok {
		return filterReader.ListAuditLogV1Filtered(ctx, filter)
	}
	switch {
	case filter.UserID.Valid:
		return h.listAuditLogsByUser(ctx, sqlc.ListAuditLogsByUserParams{
			UserID: filter.UserID,
			Limit:  limit,
			Offset: offset,
		})
	case filter.ResourceType != "":
		return h.listAuditLogsByResourceType(ctx, sqlc.ListAuditLogsByResourceTypeParams{
			ResourceType: filter.ResourceType,
			Limit:        limit,
			Offset:       offset,
		})
	case filter.Action != "":
		return h.listAuditLogsByAction(ctx, sqlc.ListAuditLogsByActionParams{
			Action: filter.Action,
			Limit:  limit,
			Offset: offset,
		})
	case filter.ActionClass != "":
		return h.queries.ListAuditLogV1ByActionClass(ctx, sqlc.ListAuditLogsByActionClassParams{
			ActionClass: filter.ActionClass,
			Limit:       limit,
			Offset:      offset,
		})
	case filter.CorrelationID != "":
		_, err := uuid.Parse(filter.CorrelationID)
		if err != nil {
			return nil, fmt.Errorf("filtered export requires the composable audit reader")
		}
		return nil, fmt.Errorf("filtered export requires the composable audit reader")
	}
	return h.listAuditLogs(ctx, sqlc.ListAuditLogsParams{Limit: limit, Offset: offset})
}

func csvNullableUUID(id pgtype.UUID) string {
	if id.Valid {
		return uuid.UUID(id.Bytes).String()
	}
	return ""
}

func csvNullableIP(ip any) string {
	if ip == nil {
		return ""
	}
	return fmt.Sprint(ip)
}

func auditStatusLabel(statusCode int32) string {
	switch {
	case statusCode == 0:
		return "success"
	case statusCode >= 200 && statusCode < 400:
		return "success"
	case statusCode >= 500:
		return "error"
	default:
		return "failure"
	}
}

func supportsFilteredAudit(q auditReaderV1) bool {
	if q == nil {
		return false
	}
	_, ok := q.(auditFilterReader)
	return ok
}

func auditQueryLimit(r *http.Request) int32 {
	limit := queryInt(r, "limit", queryInt(r, "pageSize", queryInt(r, "page_size", 20)))
	if limit < 1 {
		limit = 20
	}
	if limit > 500 {
		limit = 500
	}
	return int32(limit)
}

func auditFilterFromRequest(r *http.Request, limit, offset int32) (sqlc.AuditLogFilterParams, error) {
	q := r.URL.Query()
	filter := sqlc.AuditLogFilterParams{
		Actor:         strings.TrimSpace(q.Get("actor")),
		ResourceType:  strings.TrimSpace(q.Get("resource_type")),
		ResourceID:    strings.TrimSpace(q.Get("resource_id")),
		ResourceName:  strings.TrimSpace(q.Get("resource_name")),
		Target:        strings.TrimSpace(q.Get("target")),
		Action:        strings.TrimSpace(q.Get("action")),
		ActionClass:   strings.TrimSpace(q.Get("action_class")),
		Result:        strings.TrimSpace(q.Get("result")),
		Source:        strings.TrimSpace(q.Get("source")),
		CorrelationID: strings.TrimSpace(q.Get("correlation_id")),
		RequestID:     strings.TrimSpace(q.Get("request_id")),
		ClusterID:     strings.TrimSpace(q.Get("cluster_id")),
		ProjectID:     strings.TrimSpace(q.Get("project_id")),
		Limit:         limit,
		Offset:        offset,
	}
	if filter.ActionClass == "" {
		filter.ActionClass = strings.TrimSpace(q.Get("actionClass"))
	}
	if filter.CorrelationID == "" {
		filter.CorrelationID = strings.TrimSpace(q.Get("correlationId"))
	}
	if filter.RequestID == "" {
		filter.RequestID = strings.TrimSpace(q.Get("requestId"))
	}
	if filter.ClusterID == "" {
		filter.ClusterID = strings.TrimSpace(q.Get("clusterId"))
	}
	if filter.ProjectID == "" {
		filter.ProjectID = strings.TrimSpace(q.Get("projectId"))
	}
	if userIDStr := strings.TrimSpace(q.Get("user_id")); userIDStr != "" {
		uid, err := uuid.Parse(userIDStr)
		if err != nil {
			return filter, fmt.Errorf("invalid user_id")
		}
		filter.UserID = pgtype.UUID{Bytes: uid, Valid: true}
	}
	switch filter.Result {
	case "", "success", "failure", "error":
	default:
		return filter, fmt.Errorf("result must be success, failure, or error")
	}
	if statusCodeStr := strings.TrimSpace(q.Get("status_code")); statusCodeStr != "" {
		statusCode, err := strconv.Atoi(statusCodeStr)
		if err != nil || statusCode < 0 || statusCode > 599 {
			return filter, fmt.Errorf("invalid status_code")
		}
		filter.StatusCode = int32(statusCode)
		filter.HasStatusCode = true
	}
	from, hasFrom, err := auditTimeParam(q.Get("from"))
	if err != nil {
		return filter, fmt.Errorf("invalid from timestamp")
	}
	filter.From = from
	filter.HasFrom = hasFrom
	to, hasTo, err := auditTimeParam(q.Get("to"))
	if err != nil {
		return filter, fmt.Errorf("invalid to timestamp")
	}
	filter.To = to
	filter.HasTo = hasTo
	return filter, nil
}

func auditTimeParam(raw string) (time.Time, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false, err
	}
	return t.UTC(), true, nil
}

// Get handles GET /api/v1/audit/{id}/.
func (h *AuditHandler) Get(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid audit log ID")
		return
	}

	auditLog, err := h.getAuditLogByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, "not_found", "Audit log entry not found")
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
