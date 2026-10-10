package handler

import (
	"context"
	"fmt"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"net/http"
	"strings"
)

func (h *ResourceHandler) ListAuditLogs(w http.ResponseWriter, r *http.Request) {
	if !validateLegacyPage(w, r) {
		return
	}

	if h.queries == nil {
		paging.Write(w, []any{}, paging.Exact(0, queryLimit(r, 20), queryOffset(r), len([]any{})))
		return
	}
	logs, err := listAuditLogsForResource(r.Context(), h.queries, sqlc.ListAuditLogsParams{
		Limit:  queryLimit32(r, 20),
		Offset: queryOffset32(r),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.AuditError, "Failed to load audit logs")
		return
	}
	total, _ := countAuditLogsForResource(r.Context(), h.queries)
	items := make([]map[string]any, 0, len(logs))
	for _, item := range logs {
		items = append(items, map[string]any{
			"id":           item.ID.String(),
			"action":       item.Action,
			"resourceType": item.ResourceType,
			"resourceName": item.ResourceName,
			"user":         nullableUserID(item.UserID),
			"userAgent":    item.UserAgent,
			"sourceIP":     "",
			"status":       "success",
			"details":      item.Detail,
			"timestamp":    item.CreatedAt.UTC().Format(timeLayout),
		})
	}
	paging.Write(w, items, paging.Exact(total, queryLimit(r, 20), queryOffset(r), len(items)))
}

func listAuditLogsForResource(ctx context.Context, q any, arg sqlc.ListAuditLogsParams) ([]sqlc.AuditLog, error) {
	if v1, ok := q.(auditReaderV1); ok && v1 != nil {
		return v1.ListAuditLogV1(ctx, arg)
	}
	return nil, fmt.Errorf("audit v1 reader not configured")
}

func countAuditLogsForResource(ctx context.Context, q any) (int64, error) {
	if v1, ok := q.(auditReaderV1); ok && v1 != nil {
		return v1.CountAuditLogV1(ctx)
	}
	return 0, fmt.Errorf("audit v1 reader not configured")
}

func (h *ResourceHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	if !validateLegacyPage(w, r) {
		return
	}

	if h.queries == nil {
		paging.Write(w, []any{}, paging.Exact(0, queryLimit(r, 20), queryOffset(r), len([]any{})))
		return
	}
	limit := queryLimit32(r, 20)
	offset := queryOffset32(r)
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	if len(search) > 200 {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, "search must be at most 200 characters")
		return
	}
	var (
		users []sqlc.User
		total int64
		err   error
	)
	if search == "" {
		users, err = h.queries.ListUsers(r.Context(), sqlc.ListUsersParams{Limit: limit, Offset: offset})
		if err == nil {
			total, err = h.queries.CountUsers(r.Context())
		}
	} else {
		directory, ok := h.queries.(userDirectoryQuerier)
		if !ok {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.UsersError, "user directory search not configured")
			return
		}
		users, err = directory.SearchUsers(r.Context(), sqlc.SearchUsersParams{
			Search: search, PageLimit: limit, PageOffset: offset,
		})
		if err == nil {
			total, err = directory.CountSearchUsers(r.Context(), search)
		}
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UsersError, "Failed to load users")
		return
	}
	items := make([]map[string]any, 0, len(users))
	for _, user := range users {
		items = append(items, mapUser(user))
	}
	paging.Write(w, items, paging.Exact(total, queryLimit(r, 20), queryOffset(r), len(items)))
}
