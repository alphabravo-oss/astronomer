package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type monitoringEndpointDeleteQuerier struct {
	MonitoringQuerier
	backend   sqlc.MonitoringBackend
	getErr    error
	deleteErr error
	deletedID uuid.UUID
}

func (q *monitoringEndpointDeleteQuerier) GetDefaultMonitoringBackend(context.Context) (sqlc.MonitoringBackend, error) {
	return q.backend, q.getErr
}

func (q *monitoringEndpointDeleteQuerier) DeleteDefaultMonitoringBackendIfUnused(_ context.Context, id uuid.UUID) (sqlc.MonitoringBackend, error) {
	q.deletedID = id
	if q.deleteErr != nil {
		return sqlc.MonitoringBackend{}, q.deleteErr
	}
	return q.backend, nil
}

func monitoringEndpointDeleteRequest(id string) *http.Request {
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/monitoring/endpoints/"+id+"/", nil)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", id)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
}

func TestDeleteMonitoringEndpoint(t *testing.T) {
	id := uuid.New()
	tests := []struct {
		name      string
		pathID    string
		backendID uuid.UUID
		getErr    error
		deleteErr error
		want      int
		deleted   bool
	}{
		{name: "deletes unused endpoint", pathID: id.String(), backendID: id, want: http.StatusNoContent, deleted: true},
		{name: "rejects dependent configuration", pathID: id.String(), backendID: id, deleteErr: pgx.ErrNoRows, want: http.StatusConflict, deleted: true},
		{name: "hides a different endpoint", pathID: id.String(), backendID: uuid.New(), want: http.StatusNotFound},
		{name: "reports an absent endpoint", pathID: id.String(), backendID: id, getErr: pgx.ErrNoRows, want: http.StatusNotFound},
		{name: "rejects an invalid id", pathID: "not-a-uuid", backendID: id, want: http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := &monitoringEndpointDeleteQuerier{
				backend: sqlc.MonitoringBackend{ID: tc.backendID, BackendType: "thanos"},
				getErr:  tc.getErr, deleteErr: tc.deleteErr,
			}
			h := NewMonitoringHandlerWithQueries(q, nil)
			recorder := httptest.NewRecorder()
			h.DeleteEndpoint(recorder, monitoringEndpointDeleteRequest(tc.pathID))
			if recorder.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tc.want, recorder.Body.String())
			}
			if got := q.deletedID != uuid.Nil; got != tc.deleted {
				t.Fatalf("delete called = %v, want %v", got, tc.deleted)
			}
		})
	}
}

type monitoringEndpointReadOnlyQuerier struct {
	MonitoringQuerier
	backend sqlc.MonitoringBackend
}

func (q *monitoringEndpointReadOnlyQuerier) GetDefaultMonitoringBackend(context.Context) (sqlc.MonitoringBackend, error) {
	return q.backend, nil
}

func TestDeleteMonitoringEndpointFailsClosedWithoutDeleteQuery(t *testing.T) {
	id := uuid.New()
	h := NewMonitoringHandlerWithQueries(&monitoringEndpointReadOnlyQuerier{
		backend: sqlc.MonitoringBackend{ID: id, BackendType: "thanos"},
	}, nil)
	recorder := httptest.NewRecorder()
	h.DeleteEndpoint(recorder, monitoringEndpointDeleteRequest(id.String()))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusServiceUnavailable, recorder.Body.String())
	}
}
