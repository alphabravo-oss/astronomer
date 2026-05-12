package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type fakeAuditQueries struct {
	v1Log         sqlc.AuditLog
	v1ListCalled  bool
	v1GetCalled   bool
	v1CountCalled bool
	v1SinceCalled bool
}

func (f *fakeAuditQueries) GetAuditLogV1ByID(_ context.Context, _ uuid.UUID) (sqlc.AuditLog, error) {
	f.v1GetCalled = true
	return f.v1Log, nil
}

func (f *fakeAuditQueries) ListAuditLogV1(_ context.Context, _ sqlc.ListAuditLogsParams) ([]sqlc.AuditLog, error) {
	f.v1ListCalled = true
	return []sqlc.AuditLog{f.v1Log}, nil
}

func (f *fakeAuditQueries) ListAuditLogV1ByUser(_ context.Context, _ sqlc.ListAuditLogsByUserParams) ([]sqlc.AuditLog, error) {
	return []sqlc.AuditLog{f.v1Log}, nil
}

func (f *fakeAuditQueries) ListAuditLogV1ByResourceType(_ context.Context, _ sqlc.ListAuditLogsByResourceTypeParams) ([]sqlc.AuditLog, error) {
	return []sqlc.AuditLog{f.v1Log}, nil
}

func (f *fakeAuditQueries) ListAuditLogV1ByAction(_ context.Context, _ sqlc.ListAuditLogsByActionParams) ([]sqlc.AuditLog, error) {
	return []sqlc.AuditLog{f.v1Log}, nil
}

func (f *fakeAuditQueries) ListAuditLogV1Since(_ context.Context, _ sqlc.ListAuditLogsSinceParams) ([]sqlc.AuditLog, error) {
	f.v1SinceCalled = true
	return []sqlc.AuditLog{f.v1Log}, nil
}

func (f *fakeAuditQueries) CountAuditLogV1(_ context.Context) (int64, error) {
	f.v1CountCalled = true
	return 1, nil
}

func (f *fakeAuditQueries) CountAuditLogV1ByUser(_ context.Context, _ pgtype.UUID) (int64, error) {
	return 1, nil
}

func (f *fakeAuditQueries) ListAuditLogV1ByActionClass(_ context.Context, _ sqlc.ListAuditLogsByActionClassParams) ([]sqlc.AuditLog, error) {
	return nil, nil
}

func (f *fakeAuditQueries) CountAuditLogV1ByActionClass(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

func TestAuditHandlerListPrefersAuditLogV1(t *testing.T) {
	id := uuid.New()
	fake := &fakeAuditQueries{
		v1Log: sqlc.AuditLog{
			ID:            id,
			Source:        "http",
			CorrelationID: "corr-1",
			Action:        "request.post",
			ResourceType:  "cluster",
			ResourceName:  "from-v1",
			CreatedAt:     time.Unix(100, 0).UTC(),
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/?limit=1&offset=0", nil)
	rr := httptest.NewRecorder()

	NewAuditHandler(fake).List(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !fake.v1ListCalled || !fake.v1CountCalled {
		t.Fatal("expected v1 list/count paths to be used")
	}

	var body struct {
		Data []AuditLogResponse `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Data) != 1 {
		t.Fatalf("len(data) = %d, want 1", len(body.Data))
	}
	if body.Data[0].ResourceName != "from-v1" {
		t.Fatalf("resource_name = %q, want from-v1", body.Data[0].ResourceName)
	}
	if body.Data[0].Source != "http" {
		t.Fatalf("source = %q, want http", body.Data[0].Source)
	}
	if body.Data[0].CorrelationID != "corr-1" {
		t.Fatalf("correlation_id = %q, want corr-1", body.Data[0].CorrelationID)
	}
}

func TestListAuditLogsForResourcePrefersAuditLogV1(t *testing.T) {
	fake := &fakeAuditQueries{
		v1Log: sqlc.AuditLog{
			ID:           uuid.New(),
			Action:       "request.put",
			ResourceType: "project",
			ResourceName: "activity-v1",
			CreatedAt:    time.Unix(300, 0).UTC(),
		},
	}

	logs, err := listAuditLogsForResource(context.Background(), fake, sqlc.ListAuditLogsParams{Limit: 1})
	if err != nil {
		t.Fatalf("listAuditLogsForResource error: %v", err)
	}
	if len(logs) != 1 || logs[0].ResourceName != "activity-v1" {
		t.Fatalf("unexpected logs: %+v", logs)
	}
	if !fake.v1ListCalled {
		t.Fatal("expected resource helper to use v1 logs")
	}

	total, err := countAuditLogsForResource(context.Background(), fake)
	if err != nil {
		t.Fatalf("countAuditLogsForResource error: %v", err)
	}
	if total != 1 {
		t.Fatalf("total = %d, want 1", total)
	}
	if !fake.v1CountCalled {
		t.Fatal("expected resource helper to use v1 count")
	}
}

func TestAuditHandlerListSupportsSinceCursor(t *testing.T) {
	id := uuid.New()
	fake := &fakeAuditQueries{
		v1Log: sqlc.AuditLog{
			ID:            id,
			Source:        "service",
			CorrelationID: "corr-2",
			Action:        "auth.login_failed",
			ResourceType:  "user",
			ResourceName:  "from-since",
			CreatedAt:     time.Unix(200, 0).UTC(),
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/?since="+uuid.New().String()+"&limit=1", nil)
	rr := httptest.NewRecorder()

	NewAuditHandler(fake).List(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !fake.v1SinceCalled {
		t.Fatal("expected since cursor path to be used")
	}

	var body struct {
		Data []AuditLogResponse `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Data) != 1 || body.Data[0].CorrelationID != "corr-2" {
		t.Fatalf("unexpected response body: %+v", body.Data)
	}
}
