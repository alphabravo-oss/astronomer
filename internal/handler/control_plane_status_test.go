package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type stubControlPlaneQ struct{}

func (stubControlPlaneQ) GetDefaultControlPlanePolicy(ctx context.Context) (sqlc.ControlPlanePolicy, error) {
	return sqlc.ControlPlanePolicy{}, pgx.ErrNoRows
}
func (stubControlPlaneQ) UpsertDefaultControlPlanePolicy(ctx context.Context, arg sqlc.UpsertDefaultControlPlanePolicyParams) (sqlc.ControlPlanePolicy, error) {
	return sqlc.ControlPlanePolicy{}, nil
}
func (stubControlPlaneQ) ListControlPlaneAlerts(ctx context.Context, arg sqlc.ListControlPlaneAlertsParams) ([]sqlc.ControlPlaneAlert, error) {
	return nil, nil
}
func (stubControlPlaneQ) GetActiveControlPlaneAlert(ctx context.Context, arg sqlc.GetActiveControlPlaneAlertParams) (sqlc.ControlPlaneAlert, error) {
	return sqlc.ControlPlaneAlert{}, pgx.ErrNoRows
}
func (stubControlPlaneQ) CreateControlPlaneAlert(ctx context.Context, arg sqlc.CreateControlPlaneAlertParams) (sqlc.ControlPlaneAlert, error) {
	return sqlc.ControlPlaneAlert{}, nil
}
func (stubControlPlaneQ) ResolveControlPlaneAlert(ctx context.Context, arg sqlc.ResolveControlPlaneAlertParams) (sqlc.ControlPlaneAlert, error) {
	return sqlc.ControlPlaneAlert{}, nil
}
func (stubControlPlaneQ) AcknowledgeControlPlaneAlert(ctx context.Context, arg sqlc.AcknowledgeControlPlaneAlertParams) (sqlc.ControlPlaneAlert, error) {
	return sqlc.ControlPlaneAlert{}, nil
}
func (stubControlPlaneQ) CreateControlPlaneSilence(ctx context.Context, arg sqlc.CreateControlPlaneSilenceParams) (sqlc.ControlPlaneSilence, error) {
	return sqlc.ControlPlaneSilence{}, nil
}
func (stubControlPlaneQ) ListControlPlaneSilences(ctx context.Context, arg sqlc.ListControlPlaneSilencesParams) ([]sqlc.ControlPlaneSilence, error) {
	return nil, nil
}
func (stubControlPlaneQ) GetActiveControlPlaneSilences(ctx context.Context) ([]sqlc.ControlPlaneSilence, error) {
	return nil, nil
}
func (stubControlPlaneQ) DeleteControlPlaneSilence(ctx context.Context, id uuid.UUID) error {
	return nil
}
func (stubControlPlaneQ) ListEnabledNotificationChannels(ctx context.Context) ([]sqlc.NotificationChannel, error) {
	return nil, nil
}

// TEST-06: ControlPlaneHandler.Status with a stub querier and nil sub-handlers.
func TestControlPlaneStatus_WithStubQuerier(t *testing.T) {
	h := &ControlPlaneHandler{queries: stubControlPlaneQ{}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/controllers/status/", nil)
	rec := httptest.NewRecorder()
	h.Status(rec, req)
	if rec.Code >= 500 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
