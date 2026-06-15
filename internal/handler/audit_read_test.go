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

type filteredAuditQueries struct {
	fakeAuditQueries
	filterArg      sqlc.AuditLogFilterParams
	countFilterArg sqlc.AuditLogFilterParams
	filterCalled   bool
	countCalled    bool
}

func (f *filteredAuditQueries) ListAuditLogV1Filtered(_ context.Context, arg sqlc.AuditLogFilterParams) ([]sqlc.AuditLog, error) {
	f.filterArg = arg
	f.filterCalled = true
	return []sqlc.AuditLog{f.v1Log}, nil
}

func (f *filteredAuditQueries) CountAuditLogV1Filtered(_ context.Context, arg sqlc.AuditLogFilterParams) (int64, error) {
	f.countFilterArg = arg
	f.countCalled = true
	return 1, nil
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

func TestAuditHandlerListSupportsComposableSearchFilters(t *testing.T) {
	clusterID := uuid.New().String()
	projectID := uuid.New().String()
	from := "2026-06-14T10:00:00Z"
	to := "2026-06-14T11:00:00Z"
	fake := &filteredAuditQueries{
		fakeAuditQueries: fakeAuditQueries{
			v1Log: sqlc.AuditLog{
				ID:              uuid.New(),
				UserID:          pgtype.UUID{Bytes: uuid.New(), Valid: true},
				Source:          "http",
				CorrelationID:   "corr-3",
				Action:          "cluster.delete",
				ActionClass:     "mutation",
				ResourceType:    "cluster",
				ResourceID:      clusterID,
				ResourceName:    "prod-east",
				HttpMethod:      http.MethodDelete,
				Path:            "/api/v1/clusters/" + clusterID + "/",
				StatusCode:      403,
				DurationMs:      12,
				RequestID:       "req-3",
				ActorAuthMethod: "session",
				CreatedAt:       time.Unix(300, 0).UTC(),
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/audit/?actor=admin@example.com&target=prod&action=cluster.delete&action_class=mutation&result=failure&correlation_id=corr-3&request_id=req-3&cluster_id="+clusterID+"&project_id="+projectID+"&status_code=403&from="+from+"&to="+to+"&limit=600&offset=10",
		nil,
	)
	rr := httptest.NewRecorder()

	NewAuditHandler(fake).List(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if !fake.filterCalled || !fake.countCalled {
		t.Fatal("expected composable audit filter list/count paths")
	}
	if fake.filterArg.Limit != 500 || fake.filterArg.Offset != 10 {
		t.Fatalf("pagination = limit %d offset %d, want 500/10", fake.filterArg.Limit, fake.filterArg.Offset)
	}
	if fake.filterArg.Actor != "admin@example.com" ||
		fake.filterArg.Target != "prod" ||
		fake.filterArg.Action != "cluster.delete" ||
		fake.filterArg.ActionClass != "mutation" ||
		fake.filterArg.Result != "failure" ||
		fake.filterArg.CorrelationID != "corr-3" ||
		fake.filterArg.RequestID != "req-3" ||
		fake.filterArg.ClusterID != clusterID ||
		fake.filterArg.ProjectID != projectID ||
		!fake.filterArg.HasStatusCode ||
		fake.filterArg.StatusCode != 403 ||
		!fake.filterArg.HasFrom ||
		!fake.filterArg.HasTo {
		t.Fatalf("unexpected filter arg: %#v", fake.filterArg)
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
	row := body.Data[0]
	if row.Status != "failure" || row.StatusCode != 403 || row.HTTPMethod != http.MethodDelete || row.DurationMs != 12 {
		t.Fatalf("unexpected response row: %#v", row)
	}
}

func TestAuditHandlerListRejectsInvalidSearchFilter(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/?result=maybe", nil)
	rr := httptest.NewRecorder()

	NewAuditHandler(&filteredAuditQueries{}).List(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
}
