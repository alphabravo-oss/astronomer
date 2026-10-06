package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/tableviews"
)

const maxTableViewBodyBytes = tableviews.MaxStateBytes + 4*1024

// TableViewsQuerier is the read model for saved table views.
type TableViewsQuerier interface {
	ListUserTableViews(context.Context, sqlc.ListUserTableViewsParams) ([]sqlc.UserTableView, error)
}

// TableViewsMutationTx is the transaction-bound write surface. Every mutation
// commits together with its audit outbox intent.
type TableViewsMutationTx interface {
	audit.OutboxQuerier
	LockUserTableViewScope(context.Context, string) error
	CountUserTableViews(context.Context, sqlc.CountUserTableViewsParams) (int64, error)
	CreateUserTableView(context.Context, sqlc.CreateUserTableViewParams) (sqlc.UserTableView, error)
	GetUserTableView(context.Context, sqlc.GetUserTableViewParams) (sqlc.UserTableView, error)
	UpdateUserTableView(context.Context, sqlc.UpdateUserTableViewParams) (sqlc.UserTableView, error)
	ClearUserTableViewDefault(context.Context, sqlc.ClearUserTableViewDefaultParams) error
	SetUserTableViewDefault(context.Context, sqlc.SetUserTableViewDefaultParams) (sqlc.UserTableView, error)
	DeleteUserTableView(context.Context, sqlc.DeleteUserTableViewParams) (sqlc.UserTableView, error)
}

// TableViewsRunTxFunc runs fn inside one mandatory-audit transaction.
type TableViewsRunTxFunc func(context.Context, func(TableViewsMutationTx) error) error

// TableViewsHandler serves the self-service /auth/me/table-views/ resource.
// Every query is scoped to the authenticated user; there is no cross-user path.
type TableViewsHandler struct {
	reader TableViewsQuerier
	runTx  TableViewsRunTxFunc
}

func NewTableViewsHandler(reader TableViewsQuerier, runTx TableViewsRunTxFunc) *TableViewsHandler {
	return &TableViewsHandler{reader: reader, runTx: runTx}
}

type tableViewResponse struct {
	ID        uuid.UUID       `json:"id"`
	TableKey  string          `json:"table_key"`
	Name      string          `json:"name"`
	State     json.RawMessage `json:"state"`
	IsDefault bool            `json:"is_default"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func tableViewFromRow(row sqlc.UserTableView) tableViewResponse {
	state := row.State
	if len(state) == 0 {
		state = json.RawMessage(`{}`)
	}
	return tableViewResponse{
		ID: row.ID, TableKey: row.TableKey, Name: row.Name, State: state,
		IsDefault: row.IsDefault, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

var (
	errTableViewLimit    = errors.New("table view limit reached")
	errTableViewNotFound = errors.New("table view not found")
)

func tableViewScopeLock(userID uuid.UUID, tableKey string) string {
	return "user_table_views:" + userID.String() + ":" + tableKey
}

func (h *TableViewsHandler) decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxTableViewBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid table view document")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Request body must contain one JSON object")
		return false
	}
	return true
}

func (h *TableViewsHandler) preflight(w http.ResponseWriter, r *http.Request, needTx bool) (uuid.UUID, bool) {
	userID, ok := preferenceUserID(r)
	if !ok {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return uuid.Nil, false
	}
	if h == nil || (needTx && h.runTx == nil) || (!needTx && h.reader == nil) {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Table views are not configured")
		return uuid.Nil, false
	}
	return userID, true
}

// List returns the caller's saved views for one table key.
func (h *TableViewsHandler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.preflight(w, r, false)
	if !ok {
		return
	}
	tableKey := r.URL.Query().Get("table_key")
	if err := tableviews.ValidateTableKey(tableKey); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	rows, err := h.reader.ListUserTableViews(r.Context(), sqlc.ListUserTableViewsParams{UserID: userID, TableKey: tableKey})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to load table views")
		return
	}
	items := make([]tableViewResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, tableViewFromRow(row))
	}
	RespondJSON(w, http.StatusOK, items)
}

// createTableViewRequest is the POST body.
//
// openapi:request TableViewCreateRequest
type createTableViewRequest struct {
	TableKey string          `json:"table_key"`
	Name     string          `json:"name"`
	State    json.RawMessage `json:"state"`
}

// Create saves a new named view, enforcing the per-table cap and unique name.
func (h *TableViewsHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.preflight(w, r, true)
	if !ok {
		return
	}
	var req createTableViewRequest
	if !h.decodeBody(w, r, &req) {
		return
	}
	if err := tableviews.ValidateTableKey(req.TableKey); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	name, err := tableviews.NormalizeName(req.Name)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	state, err := tableviews.NormalizeState(req.State)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	var stored sqlc.UserTableView
	err = h.runTx(r.Context(), func(q TableViewsMutationTx) error {
		if err := q.LockUserTableViewScope(r.Context(), tableViewScopeLock(userID, req.TableKey)); err != nil {
			return err
		}
		count, err := q.CountUserTableViews(r.Context(), sqlc.CountUserTableViewsParams{UserID: userID, TableKey: req.TableKey})
		if err != nil {
			return err
		}
		if count >= tableviews.MaxViewsPerTable {
			return errTableViewLimit
		}
		stored, err = q.CreateUserTableView(r.Context(), sqlc.CreateUserTableViewParams{
			UserID: userID, TableKey: req.TableKey, Name: name, State: state,
		})
		if err != nil {
			return err
		}
		return recordAuditOutbox(r, q, "user.table_view.created", "user_table_view", stored.ID.String(), name, http.StatusCreated, map[string]any{
			"table_key": req.TableKey,
		})
	})
	if h.respondMutationError(w, r, err, "Failed to save table view") {
		return
	}
	RespondJSON(w, http.StatusCreated, tableViewFromRow(stored))
}

// updateTableViewRequest is the PATCH body.
//
// openapi:request TableViewUpdateRequest
type updateTableViewRequest struct {
	Name      *string          `json:"name"`
	State     *json.RawMessage `json:"state"`
	IsDefault *bool            `json:"is_default"`
}

// Update renames a view, replaces its state, and/or toggles its default flag.
func (h *TableViewsHandler) Update(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.preflight(w, r, true)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid table view id")
		return
	}
	var req updateTableViewRequest
	if !h.decodeBody(w, r, &req) {
		return
	}
	if req.Name == nil && req.State == nil && req.IsDefault == nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Nothing to update")
		return
	}
	params := sqlc.UpdateUserTableViewParams{ID: id, UserID: userID}
	if req.Name != nil {
		name, err := tableviews.NormalizeName(*req.Name)
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
			return
		}
		params.Name = pgtype.Text{String: name, Valid: true}
	}
	if req.State != nil {
		state, err := tableviews.NormalizeState(*req.State)
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
			return
		}
		params.State = state
	}
	var stored sqlc.UserTableView
	err = h.runTx(r.Context(), func(q TableViewsMutationTx) error {
		current, err := q.GetUserTableView(r.Context(), sqlc.GetUserTableViewParams{ID: id, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return errTableViewNotFound
		}
		if err != nil {
			return err
		}
		stored = current
		if req.Name != nil || req.State != nil {
			if stored, err = q.UpdateUserTableView(r.Context(), params); err != nil {
				return err
			}
		}
		if req.IsDefault != nil {
			if *req.IsDefault {
				if err := q.ClearUserTableViewDefault(r.Context(), sqlc.ClearUserTableViewDefaultParams{UserID: userID, TableKey: current.TableKey, ID: id}); err != nil {
					return err
				}
			}
			if stored, err = q.SetUserTableViewDefault(r.Context(), sqlc.SetUserTableViewDefaultParams{ID: id, UserID: userID, IsDefault: *req.IsDefault}); err != nil {
				return err
			}
		}
		return recordAuditOutbox(r, q, "user.table_view.updated", "user_table_view", id.String(), stored.Name, http.StatusOK, map[string]any{
			"table_key": stored.TableKey, "renamed": req.Name != nil,
			"state_changed": req.State != nil, "is_default": stored.IsDefault,
		})
	})
	if h.respondMutationError(w, r, err, "Failed to update table view") {
		return
	}
	RespondJSON(w, http.StatusOK, tableViewFromRow(stored))
}

// Delete removes one of the caller's views.
func (h *TableViewsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.preflight(w, r, true)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid table view id")
		return
	}
	err = h.runTx(r.Context(), func(q TableViewsMutationTx) error {
		deleted, err := q.DeleteUserTableView(r.Context(), sqlc.DeleteUserTableViewParams{ID: id, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return errTableViewNotFound
		}
		if err != nil {
			return err
		}
		return recordAuditOutbox(r, q, "user.table_view.deleted", "user_table_view", id.String(), deleted.Name, http.StatusNoContent, map[string]any{
			"table_key": deleted.TableKey,
		})
	})
	if h.respondMutationError(w, r, err, "Failed to delete table view") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// respondMutationError writes the response for err and reports whether it did.
func (h *TableViewsHandler) respondMutationError(w http.ResponseWriter, r *http.Request, err error, message string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, errTableViewLimit):
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "At most 20 saved views are allowed per table; delete one first")
	case errors.Is(err, errTableViewNotFound):
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Table view not found")
	case isUniqueViolation(err):
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "A view with that name already exists for this table")
	default:
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.InternalError, message)
	}
	return true
}
