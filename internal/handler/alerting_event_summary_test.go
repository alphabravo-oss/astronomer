package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type alertEventSummaryStore struct {
	AlertingQuerier
	row  sqlc.GetAlertEventSummaryRow
	args []pgtype.UUID
}

func (s *alertEventSummaryStore) GetAlertEventSummary(_ context.Context, clusterID pgtype.UUID) (sqlc.GetAlertEventSummaryRow, error) {
	s.args = append(s.args, clusterID)
	return s.row, nil
}

func TestAlertEventSummaryUsesAuthoritativeAggregate(t *testing.T) {
	clusterID := uuid.New()
	store := &alertEventSummaryStore{row: sqlc.GetAlertEventSummaryRow{
		Total: 2001, Firing: 9, Acknowledged: 4, Resolved: 1980,
		Silenced: 8, FiringCritical: 2, FiringWarning: 6, FiringInfo: 1,
	}}
	h := NewAlertingHandler(store)
	recorder := httptest.NewRecorder()
	h.EventSummary(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/alerting/events/summary/?clusterId="+clusterID.String(), nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data alertEventSummaryResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Total != 2001 || response.Data.FiringCritical != 2 || response.Data.FiringWarning != 6 {
		t.Fatalf("summary = %+v", response.Data)
	}
	if response.Data.AsOf.IsZero() || len(store.args) != 1 || !store.args[0].Valid || uuid.UUID(store.args[0].Bytes) != clusterID {
		t.Fatalf("as_of=%s args=%+v", response.Data.AsOf, store.args)
	}
}

func TestAlertEventSummaryRejectsInvalidCluster(t *testing.T) {
	store := &alertEventSummaryStore{}
	h := NewAlertingHandler(store)
	recorder := httptest.NewRecorder()
	h.EventSummary(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/alerting/events/summary/?clusterId=invalid", nil))

	if recorder.Code != http.StatusBadRequest || len(store.args) != 0 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, len(store.args), recorder.Body.String())
	}
}
