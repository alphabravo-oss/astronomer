package handler

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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
	ListAuditLogV1FilteredPage(ctx context.Context, arg sqlc.AuditLogFilterParams) (sqlc.AuditLogPage, error)
	ListAuditLogV1FilteredKeyset(ctx context.Context, arg sqlc.AuditLogFilterParams) ([]sqlc.AuditLog, error)
	CountAuditLogV1Filtered(ctx context.Context, arg sqlc.AuditLogFilterParams) (int64, error)
}

type AuditExportOperationStore interface {
	GetAuditExportOperation(context.Context, uuid.UUID) (sqlc.GetAuditExportOperationRow, error)
	GetAuditExportArtifact(context.Context, uuid.UUID) (sqlc.GetAuditExportArtifactRow, error)
}

type AuditExportMutationTx interface {
	CreateAuditExportOperation(context.Context, sqlc.CreateAuditExportOperationParams) (sqlc.CreateAuditExportOperationRow, error)
	audit.OutboxQuerier
}

type auditExportRunTxFunc func(context.Context, func(AuditExportMutationTx) error) error

const (
	auditListCountLimit    = sqlc.DefaultAuditLogCountLimit
	auditCSVExportPageSize = int32(500)
	auditExportMaxRows     = int64(100_000)
	auditExportMaxRange    = 31 * 24 * time.Hour
)

// AuditHandler handles audit log endpoints.
type AuditHandler struct {
	queries    auditReaderV1
	operations AuditExportOperationStore
	runTx      auditExportRunTxFunc
}

// NewAuditHandler creates a new audit handler.
func NewAuditHandler(queries auditReaderV1) *AuditHandler {
	h := &AuditHandler{queries: queries}
	if operations, ok := queries.(AuditExportOperationStore); ok {
		h.operations = operations
	}
	return h
}

func (h *AuditHandler) SetRunTx(runTx auditExportRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *AuditHandler) DurableExportWired() bool {
	return h != nil && h.operations != nil && h.runTx != nil
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
		ActionClass:     audit.ClassifyActionClass(a.Action, a.Source, a.ActionClass),
		ResourceType:    a.ResourceType,
		ResourceID:      a.ResourceID,
		ResourceName:    a.ResourceName,
		Detail:          a.Detail,
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
// action, action_class, audience (people|system|all), result, correlation_id,
// request_id, cluster_id, project_id, from, and to.
func (h *AuditHandler) List(w http.ResponseWriter, r *http.Request) {
	limit := auditQueryLimit(r)
	offset := int32(queryOffset(r))
	filter, filterErr := auditFilterFromRequest(r, limit, offset)
	if filterErr != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidFilter, filterErr.Error())
		return
	}

	userIDStr := r.URL.Query().Get("user_id")
	resourceType := r.URL.Query().Get("resource_type")
	action := r.URL.Query().Get("action")
	actionClass := r.URL.Query().Get("action_class")
	sinceIDStr := r.URL.Query().Get("since")

	var (
		logs       []sqlc.AuditLog
		total      int64
		pagination *paging.Metadata
		err        error
	)

	switch {
	case sinceIDStr == "" && supportsFilteredAudit(h.queries):
		filterReader := h.queries.(auditFilterReader)
		page, pageErr := filterReader.ListAuditLogV1FilteredPage(r.Context(), filter)
		err = pageErr
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list audit logs")
			return
		}
		logs = page.Logs
		metadata := paging.Uncounted(int(limit), queryOffset(r), len(logs), page.HasMore)
		pagination = &metadata
	case actionClass != "":
		logs, err = h.queries.ListAuditLogV1ByActionClass(r.Context(), sqlc.ListAuditLogsByActionClassParams{
			ActionClass: actionClass,
			Limit:       limit,
			Offset:      offset,
		})
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list audit logs")
			return
		}
		total, err = h.queries.CountAuditLogV1ByActionClass(r.Context(), actionClass)

	case sinceIDStr != "":
		sinceID, parseErr := uuid.Parse(sinceIDStr)
		if parseErr != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidSince, "Invalid since cursor")
			return
		}
		logs, err = h.listAuditLogsSince(r.Context(), sqlc.ListAuditLogsSinceParams{
			SinceID: sinceID,
			Limit:   limit,
		})
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list audit logs")
			return
		}
		total = int64(len(logs))
	case userIDStr != "":
		uid, parseErr := uuid.Parse(userIDStr)
		if parseErr != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid user_id")
			return
		}
		pgtypeUID := pgtype.UUID{Bytes: uid, Valid: true}
		logs, err = h.listAuditLogsByUser(r.Context(), sqlc.ListAuditLogsByUserParams{
			UserID: pgtypeUID,
			Limit:  limit,
			Offset: offset,
		})
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list audit logs")
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
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list audit logs")
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
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list audit logs")
			return
		}
		total, err = h.countAuditLogs(r.Context())

	default:
		logs, err = h.listAuditLogs(r.Context(), sqlc.ListAuditLogsParams{
			Limit:  limit,
			Offset: offset,
		})
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list audit logs")
			return
		}
		total, err = h.countAuditLogs(r.Context())
	}

	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count audit logs")
		return
	}
	if pagination == nil && total >= int64(filter.CountLimit) && filter.CountLimit > 0 {
		w.Header().Set("X-Total-Count-Capped", "true")
	}

	items := make([]AuditLogResponse, 0, len(logs))
	for _, a := range logs {
		items = append(items, auditLogToResponse(a))
	}

	if pagination != nil {
		paging.Write(w, items, *pagination)
		return
	}
	paging.Write(w, items, paging.Exact(total, int(limit), queryOffset(r), len(items)))
}

// Export handles GET /api/v1/audit/export/?format=csv.
// Streams audit log entries as CSV. Same filters as the list endpoint.
func (h *AuditHandler) Export(w http.ResponseWriter, r *http.Request) {
	filter, filterReader, ok := h.validatedAuditExportFilter(w, r)
	if !ok {
		return
	}
	filter.CountLimit = int32(auditExportMaxRows + 1)
	total, err := filterReader.CountAuditLogV1Filtered(r.Context(), filter)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to size audit export")
		return
	}
	if total > auditExportMaxRows {
		w.Header().Set("Link", `</api/v1/audit/exports/>; rel="create"`)
		RespondRequestError(w, r, http.StatusRequestEntityTooLarge, apierror.InvalidFilter, "Audit export exceeds the synchronous limit; create a durable export operation")
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="audit_log_export.csv"`)
	w.WriteHeader(http.StatusOK)

	if err := writeAuditCSV(r.Context(), filterReader, filter, w); err != nil {
		return
	}
}

// CreateExport handles POST /api/v1/audit/exports/ and creates a durable CSV
// export operation. Query filters intentionally match the bounded GET export.
func (h *AuditHandler) CreateExport(w http.ResponseWriter, r *http.Request) {
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	filter, _, ok := h.validatedAuditExportFilter(w, r)
	if !ok {
		return
	}
	h.acceptAuditExport(w, r, filter, strings.TrimSpace(r.Header.Get("Idempotency-Key")))
}

func (h *AuditHandler) validatedAuditExportFilter(w http.ResponseWriter, r *http.Request) (sqlc.AuditLogFilterParams, auditFilterReader, bool) {
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "csv"
	}
	if format != "csv" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidFormat, "Only 'csv' export format is supported")
		return sqlc.AuditLogFilterParams{}, nil, false
	}

	filter, filterErr := auditFilterFromRequest(r, auditCSVExportPageSize, 0)
	if filterErr != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidFilter, filterErr.Error())
		return sqlc.AuditLogFilterParams{}, nil, false
	}
	sinceIDStr := r.URL.Query().Get("since")
	if sinceIDStr != "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidSince, "since cursor export is not supported")
		return sqlc.AuditLogFilterParams{}, nil, false
	}
	if !filter.HasFrom || !filter.HasTo {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidFilter, "audit export requires explicit from and to timestamps")
		return sqlc.AuditLogFilterParams{}, nil, false
	}
	if !filter.To.After(filter.From) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidFilter, "audit export to must be after from")
		return sqlc.AuditLogFilterParams{}, nil, false
	}
	if filter.To.Sub(filter.From) > auditExportMaxRange {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidFilter, "audit export range cannot exceed 31 days")
		return sqlc.AuditLogFilterParams{}, nil, false
	}
	filterReader, ok := h.queries.(auditFilterReader)
	if !ok {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ListError, "Audit export is not configured")
		return sqlc.AuditLogFilterParams{}, nil, false
	}
	return filter, filterReader, true
}

type AuditExportOperationResponse struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	Attempts    int32      `json:"attempt_count"`
	ErrorCode   string     `json:"error_code,omitempty"`
	Filename    string     `json:"filename,omitempty"`
	SHA256      string     `json:"sha256,omitempty"`
	Size        int64      `json:"size"`
	ExpiresAt   time.Time  `json:"expires_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	StatusURL   string     `json:"status_url"`
	DownloadURL string     `json:"download_url,omitempty"`
}

func (h *AuditHandler) acceptAuditExport(w http.ResponseWriter, r *http.Request, filter sqlc.AuditLogFilterParams, idempotencyKey string) {
	if h.runTx == nil || h.operations == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "Audit export operation store is not configured")
		return
	}
	caller := currentUserUUID(r)
	if !caller.Valid || caller.Bytes == uuid.Nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Invalid caller")
		return
	}
	filter.Offset, filter.CountLimit, filter.HasBefore = 0, 0, false
	filter.BeforeTime, filter.BeforeID = time.Time{}, uuid.Nil
	spec, err := json.Marshal(audit.ExportSpec{Filter: filter})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InvalidRequest, "Failed to encode audit export")
		return
	}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(idempotencyKey)))
	var row sqlc.CreateAuditExportOperationRow
	err = h.runTx(r.Context(), func(q AuditExportMutationTx) error {
		var createErr error
		row, createErr = q.CreateAuditExportOperation(r.Context(), sqlc.CreateAuditExportOperationParams{
			RequestedBy: caller.Bytes, RequestDigest: digest, RequestSpec: spec,
		})
		if createErr != nil {
			return createErr
		}
		if !row.Created {
			var stored audit.ExportSpec
			if json.Unmarshal(row.RequestSpec, &stored) != nil {
				return errOperationIdempotencyConflict
			}
			storedDigest, storedErr := canonicalOperationRequestDigest(stored)
			requestDigest, requestErr := canonicalOperationRequestDigest(audit.ExportSpec{Filter: filter})
			if storedErr != nil || requestErr != nil || storedDigest != requestDigest {
				return errOperationIdempotencyConflict
			}
			return nil
		}
		return recordAuditOutbox(r, q, "audit_log.export_accepted", "audit_export_operation", row.ID.String(), "audit-export", http.StatusAccepted,
			map[string]any{"operation_id": row.ID.String(), "expires_at": row.ExpiresAt.UTC().Format(time.RFC3339)})
	})
	if err != nil {
		if errors.Is(err, errOperationIdempotencyConflict) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key already identifies a different audit export")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DBError, "Failed to persist audit export operation")
		return
	}
	RespondAcceptedOperation(w, auditExportStatusURL(row.ID), auditExportResponse(row.ID, row.Status, row.AttemptCount, row.ErrorCode, row.Filename, row.ArtifactSha256, row.ArtifactSize, row.ExpiresAt, row.CompletedAt, row.CreatedAt, row.UpdatedAt))
}

func (h *AuditHandler) GetExportOperation(w http.ResponseWriter, r *http.Request) {
	id, ok := parseScopeID(w, r, "id", "audit export operation")
	if !ok {
		return
	}
	if h.operations == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "Audit export operation store is not configured")
		return
	}
	row, err := h.operations.GetAuditExportOperation(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Audit export operation not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, "Failed to get audit export operation")
		return
	}
	if !auditExportOwnedByCaller(r, row.RequestedBy) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Audit export operation not found")
		return
	}
	RespondJSON(w, http.StatusOK, auditExportResponse(row.ID, row.Status, row.AttemptCount, row.ErrorCode, row.Filename, row.ArtifactSha256, row.ArtifactSize, row.ExpiresAt, row.CompletedAt, row.CreatedAt, row.UpdatedAt))
}

func (h *AuditHandler) DownloadExport(w http.ResponseWriter, r *http.Request) {
	id, ok := parseScopeID(w, r, "id", "audit export operation")
	if !ok {
		return
	}
	if h.operations == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "Audit export operation store is not configured")
		return
	}
	row, err := h.operations.GetAuditExportArtifact(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Completed audit export not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, "Failed to get audit export artifact")
		return
	}
	if !auditExportOwnedByCaller(r, row.RequestedBy) || !row.ExpiresAt.After(time.Now().UTC()) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Completed audit export not found")
		return
	}
	w.Header().Set("Content-Type", row.ArtifactContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, row.Filename))
	w.Header().Set("Content-Length", strconv.FormatInt(row.ArtifactSize, 10))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(row.Artifact)
}

func auditExportOwnedByCaller(r *http.Request, owner uuid.UUID) bool {
	caller := currentUserUUID(r)
	return caller.Valid && caller.Bytes == owner
}

func auditExportStatusURL(id uuid.UUID) string {
	return "/api/v1/audit/exports/" + id.String() + "/"
}

func auditExportResponse(id uuid.UUID, status string, attempts int32, errorCode, filename string, sha pgtype.Text, size int64, expires time.Time, completed pgtype.Timestamptz, created, updated time.Time) AuditExportOperationResponse {
	response := AuditExportOperationResponse{ID: id.String(), Status: status, Attempts: attempts, ErrorCode: errorCode, Filename: filename, Size: size, ExpiresAt: expires.UTC(), CreatedAt: created.UTC(), UpdatedAt: updated.UTC(), StatusURL: auditExportStatusURL(id)}
	if sha.Valid {
		response.SHA256 = sha.String
	}
	if completed.Valid {
		at := completed.Time.UTC()
		response.CompletedAt = &at
	}
	if status == "succeeded" && expires.After(time.Now().UTC()) {
		response.DownloadURL = auditExportStatusURL(id) + "download/"
	}
	return response
}

type auditCSVPageReader interface {
	ListAuditLogV1FilteredKeyset(context.Context, sqlc.AuditLogFilterParams) ([]sqlc.AuditLog, error)
}

func writeAuditCSV(ctx context.Context, reader auditCSVPageReader, filter sqlc.AuditLogFilterParams, output io.Writer) error {
	writer := csv.NewWriter(output)
	if err := writer.Write([]string{
		"id", "created_at", "user_id", "source", "correlation_id", "action",
		"action_class", "resource_type", "resource_id", "resource_name",
		"http_method", "path", "status_code", "duration_ms", "ip_address",
		"user_agent", "request_id", "detail",
	}); err != nil {
		return err
	}
	for {
		logs, err := reader.ListAuditLogV1FilteredKeyset(ctx, filter)
		if err != nil {
			return err
		}
		for _, entry := range logs {
			if err := writer.Write([]string{
				entry.ID.String(), entry.CreatedAt.UTC().Format(time.RFC3339), csvNullableUUID(entry.UserID),
				entry.Source, entry.CorrelationID, entry.Action, entry.ActionClass, entry.ResourceType,
				entry.ResourceID, entry.ResourceName, entry.HttpMethod, entry.Path,
				strconv.Itoa(int(entry.StatusCode)), strconv.FormatInt(entry.DurationMs, 10),
				csvNullableIP(entry.IpAddress), entry.UserAgent, entry.RequestID, string(entry.Detail),
			}); err != nil {
				return err
			}
		}
		writer.Flush()
		if err := writer.Error(); err != nil {
			return err
		}
		if len(logs) < int(auditCSVExportPageSize) {
			return nil
		}
		last := logs[len(logs)-1]
		filter.HasBefore, filter.BeforeTime, filter.BeforeID = true, last.CreatedAt, last.ID
	}
}

func (h *AuditHandler) GenerateAuditExport(ctx context.Context, spec audit.ExportSpec, output io.Writer) error {
	reader, ok := h.queries.(auditCSVPageReader)
	if !ok {
		return errors.New("audit export reader is not configured")
	}
	spec.Filter.Limit = auditCSVExportPageSize
	spec.Filter.Offset, spec.Filter.CountLimit, spec.Filter.HasBefore = 0, 0, false
	return writeAuditCSV(ctx, reader, spec.Filter, output)
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
	limit := queryLimitMax(r, queryInt(r, "pageSize", queryInt(r, "page_size", 20)), 500)
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
		Q:             strings.TrimSpace(q.Get("q")),
		Audience:      strings.TrimSpace(q.Get("audience")),
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
		CountLimit:    auditListCountLimit,
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
	switch filter.Audience {
	case "", "people", "system", "all":
	default:
		return filter, fmt.Errorf("audience must be people, system, or all")
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid audit log ID")
		return
	}

	auditLog, err := h.getAuditLogByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Audit log entry not found")
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
