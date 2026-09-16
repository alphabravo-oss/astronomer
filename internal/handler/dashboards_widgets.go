package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (h *DashboardHandler) AdminList(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	rows, err := h.queries.ListDashboardWidgets(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	out := make([]WidgetResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, widgetToResponse(row))
	}
	// The current SQL query materializes the bounded admin collection. Apply
	// the shared in-memory window so the advertised pagination is real and an
	// empty collection still carries a contract-valid positive limit.
	page, pagination := pageWindow(r, out)
	paging.Write(w, page, pagination)
}

// AdminGet handles GET /api/v1/admin/dashboard-widgets/{id}/.
func (h *DashboardHandler) AdminGet(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid widget ID")
		return
	}
	row, err := h.queries.GetDashboardWidgetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Widget not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, widgetToResponse(row))
}

// AdminCreate handles POST /api/v1/admin/dashboard-widgets/.
func (h *DashboardHandler) AdminCreate(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	var req WidgetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, err.Error())
		return
	}
	if err := h.validateWidgetRequest(r.Context(), req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, err.Error())
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	scopeIDs := req.ScopeIDs
	if scopeIDs == nil {
		scopeIDs = []uuid.UUID{}
	}
	params := sqlc.CreateDashboardWidgetParams{
		Name:           req.Name,
		Description:    req.Description,
		WidgetType:     req.WidgetType,
		Spec:           defaultJSONObject(req.Spec),
		Scope:          req.Scope,
		ScopeIds:       scopeIDs,
		GridX:          req.Grid.X,
		GridY:          req.Grid.Y,
		GridW:          defaultInt32(req.Grid.W, 4),
		GridH:          defaultInt32(req.Grid.H, 2),
		RefreshSeconds: defaultInt32(req.RefreshSeconds, 60),
		Enabled:        enabled,
		CreatedBy:      currentUserUUID(r),
	}
	row, err := executeMutation(r, h.runTx,
		func(q DashboardMutationTx) (sqlc.DashboardWidget, error) {
			return q.CreateDashboardWidget(r.Context(), params)
		},
		func(row sqlc.DashboardWidget) mutationAuditEvent {
			return mutationAuditEvent{
				action: "admin.dashboard_widget.created", resourceType: "dashboard_widget",
				resourceID: row.ID.String(), resourceName: row.Name, status: http.StatusCreated,
				detail: map[string]any{"widget_type": row.WidgetType, "scope": row.Scope},
			}
		})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	w.Header().Set("Location", "/api/v1/admin/dashboard-widgets/"+row.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, widgetToResponse(row))
}

// AdminUpdate handles PUT /api/v1/admin/dashboard-widgets/{id}/.
func (h *DashboardHandler) AdminUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid widget ID")
		return
	}
	var req WidgetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, err.Error())
		return
	}
	if err := h.validateWidgetRequest(r.Context(), req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, err.Error())
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	scopeIDs := req.ScopeIDs
	if scopeIDs == nil {
		scopeIDs = []uuid.UUID{}
	}
	params := sqlc.UpdateDashboardWidgetParams{
		ID:             id,
		Name:           req.Name,
		Description:    req.Description,
		WidgetType:     req.WidgetType,
		Spec:           defaultJSONObject(req.Spec),
		Scope:          req.Scope,
		ScopeIds:       scopeIDs,
		GridX:          req.Grid.X,
		GridY:          req.Grid.Y,
		GridW:          defaultInt32(req.Grid.W, 4),
		GridH:          defaultInt32(req.Grid.H, 2),
		RefreshSeconds: defaultInt32(req.RefreshSeconds, 60),
		Enabled:        enabled,
	}
	row, err := executeMutation(r, h.runTx,
		func(q DashboardMutationTx) (sqlc.DashboardWidget, error) {
			return q.UpdateDashboardWidget(r.Context(), params)
		},
		func(row sqlc.DashboardWidget) mutationAuditEvent {
			return mutationAuditEvent{
				action: "admin.dashboard_widget.updated", resourceType: "dashboard_widget",
				resourceID: row.ID.String(), resourceName: row.Name, status: http.StatusOK,
				detail: map[string]any{"widget_type": row.WidgetType, "scope": row.Scope},
			}
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Widget not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, widgetToResponse(row))
}

// AdminDelete handles DELETE /api/v1/admin/dashboard-widgets/{id}/.
func (h *DashboardHandler) AdminDelete(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid widget ID")
		return
	}
	deleteWidget := func(q DashboardQuerier) (sqlc.DashboardWidget, error) {
		row, getErr := q.GetDashboardWidgetByID(r.Context(), id)
		if getErr != nil {
			return sqlc.DashboardWidget{}, getErr
		}
		if deleteErr := q.DeleteDashboardWidget(r.Context(), id); deleteErr != nil {
			return sqlc.DashboardWidget{}, deleteErr
		}
		return row, nil
	}
	_, err = executeMutation(r, h.runTx,
		func(q DashboardMutationTx) (sqlc.DashboardWidget, error) { return deleteWidget(q) },
		func(row sqlc.DashboardWidget) mutationAuditEvent {
			return mutationAuditEvent{
				action: "admin.dashboard_widget.deleted", resourceType: "dashboard_widget",
				resourceID: row.ID.String(), resourceName: row.Name, status: http.StatusNoContent,
			}
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Widget not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Admin: datasource CRUD ────────────────────────────────────────────

// AdminListDatasources handles GET /api/v1/admin/prometheus-datasources/.
