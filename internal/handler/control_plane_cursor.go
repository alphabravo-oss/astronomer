package handler

import (
	"context"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/jackc/pgx/v5/pgtype"
)

type controlPlaneCursorQuerier interface {
	ListControlPlaneAlertsAfter(context.Context, sqlc.ListControlPlaneAlertsAfterParams) ([]sqlc.ControlPlaneAlert, error)
	ListControlPlaneSilencesAfter(context.Context, sqlc.ListControlPlaneSilencesAfterParams) ([]sqlc.ControlPlaneSilence, error)
}

func (h *ControlPlaneHandler) SetCursorCodec(codec *paging.CursorCodec) { h.cursorCodec = codec }

func (h *ControlPlaneHandler) ListAlerts(w http.ResponseWriter, r *http.Request) {
	if _, ok := readCursorQuery(w, r); !ok {
		return
	}
	if !validatePageOffset(w, r) {
		return
	}
	status, controller := r.URL.Query().Get("status"), r.URL.Query().Get("controller")
	binding := paging.Binding("control-plane-alerts:v2", "global-administration", "fired_at:desc,id:desc", cursorActor(r), status, controller)
	store, capable := h.queries.(controlPlaneCursorQuerier)
	page, ok := readCursorRequest(w, r, 50, h.cursorCodec, capable, binding)
	if !ok {
		return
	}
	if page.provided && page.cursor.Version != 1 {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid alert cursor position")
		return
	}
	arg := sqlc.ListControlPlaneAlertsParams{Limit: page.queryLimit(), Offset: page.offset}
	if status != "" {
		arg.Status = pgtype.Text{String: status, Valid: true}
	}
	if controller != "" {
		arg.Controller = pgtype.Text{String: controller, Valid: true}
	}
	var rows []sqlc.ControlPlaneAlert
	var err error
	if page.provided {
		rows, err = store.ListControlPlaneAlertsAfter(r.Context(), sqlc.ListControlPlaneAlertsAfterParams{Status: arg.Status, Controller: arg.Controller, AfterFiredAt: page.cursor.Time, AfterID: page.cursor.ID, QueryLimit: page.queryLimit()})
	} else {
		rows, err = h.queries.ListControlPlaneAlerts(r.Context(), arg)
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.AlertError, "Failed to list control plane alerts")
		return
	}
	rows, more := trimCursorRows(rows, page)
	var next *paging.Cursor
	if more {
		last := rows[len(rows)-1]
		position := paging.NewCursor(last.FiredAt, last.ID, binding, page.seen+len(rows))
		next = &position
	}
	resp := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, controlPlaneAlertResponse(row))
	}
	metadata, ok := page.metadata(w, r, len(rows), next, nil)
	if ok {
		paging.Write(w, resp, metadata)
	}
}

func (h *ControlPlaneHandler) ListSilences(w http.ResponseWriter, r *http.Request) {
	if _, ok := readCursorQuery(w, r); !ok {
		return
	}
	if !validatePageOffset(w, r) {
		return
	}
	binding := paging.Binding("control-plane-silences:v2", "global-administration", "created_at:desc,id:desc", cursorActor(r))
	store, capable := h.queries.(controlPlaneCursorQuerier)
	page, ok := readCursorRequest(w, r, 50, h.cursorCodec, capable, binding)
	if !ok {
		return
	}
	if page.provided && page.cursor.Version != 1 {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid silence cursor position")
		return
	}
	var rows []sqlc.ControlPlaneSilence
	var err error
	if page.provided {
		rows, err = store.ListControlPlaneSilencesAfter(r.Context(), sqlc.ListControlPlaneSilencesAfterParams{AfterCreatedAt: page.cursor.Time, AfterID: page.cursor.ID, QueryLimit: page.queryLimit()})
	} else {
		rows, err = h.queries.ListControlPlaneSilences(r.Context(), sqlc.ListControlPlaneSilencesParams{Limit: page.queryLimit(), Offset: page.offset})
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.SilenceError, "Failed to list control plane silences")
		return
	}
	rows, more := trimCursorRows(rows, page)
	var next *paging.Cursor
	if more {
		last := rows[len(rows)-1]
		position := paging.NewCursor(last.CreatedAt, last.ID, binding, page.seen+len(rows))
		next = &position
	}
	resp := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, controlPlaneSilenceResponse(row))
	}
	metadata, ok := page.metadata(w, r, len(rows), next, nil)
	if ok {
		paging.Write(w, resp, metadata)
	}
}
