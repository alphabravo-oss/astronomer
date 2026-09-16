package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
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
	filterArg       sqlc.AuditLogFilterParams
	countFilterArg  sqlc.AuditLogFilterParams
	filterCalled    bool
	countCalled     bool
	countValue      int64
	pageHasMore     bool
	keysetPages     [][]sqlc.AuditLog
	keysetArgs      []sqlc.AuditLogFilterParams
	exportOperation sqlc.GetAuditExportOperationRow
	exportArtifact  sqlc.GetAuditExportArtifactRow
}

func (f *filteredAuditQueries) GetAuditExportOperation(context.Context, uuid.UUID) (sqlc.GetAuditExportOperationRow, error) {
	return f.exportOperation, nil
}

func (f *filteredAuditQueries) GetAuditExportArtifact(context.Context, uuid.UUID) (sqlc.GetAuditExportArtifactRow, error) {
	return f.exportArtifact, nil
}

type auditExportTx struct {
	created sqlc.CreateAuditExportOperationParams
}

func (tx *auditExportTx) CreateAuditExportOperation(_ context.Context, arg sqlc.CreateAuditExportOperationParams) (sqlc.CreateAuditExportOperationRow, error) {
	tx.created = arg
	now := time.Now().UTC()
	return sqlc.CreateAuditExportOperationRow{ID: uuid.New(), RequestedBy: arg.RequestedBy, RequestSpec: arg.RequestSpec, Status: "pending", ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now, Created: true}, nil
}

func (tx *auditExportTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	return sqlc.AuditOutbox{ID: arg.ID}, nil
}

func (f *filteredAuditQueries) ListAuditLogV1FilteredPage(_ context.Context, arg sqlc.AuditLogFilterParams) (sqlc.AuditLogPage, error) {
	f.filterArg = arg
	f.filterCalled = true
	return sqlc.AuditLogPage{Logs: []sqlc.AuditLog{f.v1Log}, HasMore: f.pageHasMore}, nil
}

func (f *filteredAuditQueries) CountAuditLogV1Filtered(_ context.Context, arg sqlc.AuditLogFilterParams) (int64, error) {
	f.countFilterArg = arg
	f.countCalled = true
	if f.countValue != 0 {
		return f.countValue, nil
	}
	return 1, nil
}

func (f *filteredAuditQueries) ListAuditLogV1FilteredKeyset(_ context.Context, arg sqlc.AuditLogFilterParams) ([]sqlc.AuditLog, error) {
	f.keysetArgs = append(f.keysetArgs, arg)
	index := len(f.keysetArgs) - 1
	if index >= len(f.keysetPages) {
		return nil, nil
	}
	return f.keysetPages[index], nil
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
	if !fake.filterCalled || fake.countCalled {
		t.Fatal("expected one composable audit page query without a duplicate count")
	}
	if fake.filterArg.Limit != 500 || fake.filterArg.Offset != 10 {
		t.Fatalf("pagination = limit %d offset %d, want 500/10", fake.filterArg.Limit, fake.filterArg.Offset)
	}
	if fake.filterArg.Q != "" {
		t.Fatalf("q should be empty when not supplied: %#v", fake.filterArg)
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

func TestAuditHandlerListAcceptsAudience(t *testing.T) {
	fake := &filteredAuditQueries{
		fakeAuditQueries: fakeAuditQueries{
			v1Log: sqlc.AuditLog{ID: uuid.New(), Action: "role.create", CreatedAt: time.Unix(1, 0).UTC()},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/?audience=people", nil)
	rr := httptest.NewRecorder()
	NewAuditHandler(fake).List(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if !fake.filterCalled || fake.filterArg.Audience != "people" {
		t.Fatalf("audience = %q, called=%v", fake.filterArg.Audience, fake.filterCalled)
	}
}

func TestAuditHandlerListAcceptsQSearch(t *testing.T) {
	fake := &filteredAuditQueries{
		fakeAuditQueries: fakeAuditQueries{
			v1Log: sqlc.AuditLog{ID: uuid.New(), Action: "auth.login", CreatedAt: time.Unix(1, 0).UTC()},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/?q=login", nil)
	rr := httptest.NewRecorder()
	NewAuditHandler(fake).List(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if !fake.filterCalled || fake.filterArg.Q != "login" {
		t.Fatalf("filter Q = %q, called=%v", fake.filterArg.Q, fake.filterCalled)
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

func TestAuditHandlerListUsesLookaheadWithoutCounting(t *testing.T) {
	fake := &filteredAuditQueries{
		fakeAuditQueries: fakeAuditQueries{v1Log: sqlc.AuditLog{ID: uuid.New(), CreatedAt: time.Now()}},
		pageHasMore:      true,
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/", nil)
	rr := httptest.NewRecorder()
	NewAuditHandler(fake).List(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if fake.countCalled {
		t.Fatal("interactive audit list performed a count query")
	}
	var body struct {
		Pagination struct {
			Total      *int64 `json:"total"`
			HasMore    bool   `json:"has_more"`
			NextOffset *int   `json:"next_offset"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Pagination.Total != nil || !body.Pagination.HasMore || body.Pagination.NextOffset == nil {
		t.Fatalf("unexpected lookahead pagination: %+v", body.Pagination)
	}
}

func TestAuditHandlerExportRequiresBoundedRange(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/export/?format=csv", nil)
	rr := httptest.NewRecorder()
	NewAuditHandler(&filteredAuditQueries{}).Export(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/audit/export/?format=csv&from=2026-01-01T00:00:00Z&to=2026-02-02T00:00:00Z", nil)
	rr = httptest.NewRecorder()
	NewAuditHandler(&filteredAuditQueries{}).Export(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("wide-range status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestAuditHandlerExportDirectsOversizeToDurableOperation(t *testing.T) {
	fake := &filteredAuditQueries{countValue: auditExportMaxRows + 1}
	h := NewAuditHandler(fake)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/export/?format=csv&from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z", nil)
	rr := httptest.NewRecorder()
	h.Export(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Link"); got != `</api/v1/audit/exports/>; rel="create"` {
		t.Fatalf("Link = %q", got)
	}
	if fake.countFilterArg.CountLimit != int32(auditExportMaxRows+1) {
		t.Fatalf("count limit = %d", fake.countFilterArg.CountLimit)
	}
	if rr.Header().Get("Content-Type") == "text/csv; charset=utf-8" {
		t.Fatal("oversize export committed CSV headers")
	}
}

func TestAuditHandlerCreateExportCreatesDurableOperation(t *testing.T) {
	fake := &filteredAuditQueries{}
	tx := &auditExportTx{}
	h := NewAuditHandler(fake)
	h.SetRunTx(func(ctx context.Context, fn func(AuditExportMutationTx) error) error { return fn(tx) })
	req := httptest.NewRequest(http.MethodPost, "/api/v1/audit/exports/?format=csv&from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z", nil)
	req.Header.Set("Idempotency-Key", "incident-export-42")
	callerID := uuid.New()
	req = req.WithContext(reqctx.WithUser(req.Context(), &reqctx.User{ID: callerID.String(), AuthMethod: "jwt"}))
	rr := httptest.NewRecorder()
	h.CreateExport(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Location"); got == "" {
		t.Fatal("durable export response has no operation Location")
	}
	if tx.created.RequestedBy != callerID || len(tx.created.RequestSpec) == 0 || len(tx.created.RequestDigest) != 64 {
		t.Fatalf("durable export request = %#v", tx.created)
	}
}

func TestAuditHandlerCreateExportRequiresIdempotencyKey(t *testing.T) {
	h := NewAuditHandler(&filteredAuditQueries{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/audit/exports/?format=csv&from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z", nil)
	rr := httptest.NewRecorder()
	h.CreateExport(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestAuditHandlerExportUsesStableKeysetCursor(t *testing.T) {
	first := make([]sqlc.AuditLog, auditCSVExportPageSize)
	base := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	for i := range first {
		first[i] = sqlc.AuditLog{ID: uuid.New(), CreatedAt: base.Add(-time.Duration(i) * time.Second)}
	}
	last := first[len(first)-1]
	second := []sqlc.AuditLog{{ID: uuid.New(), CreatedAt: last.CreatedAt.Add(-time.Second)}}
	fake := &filteredAuditQueries{countValue: int64(len(first) + len(second)), keysetPages: [][]sqlc.AuditLog{first, second}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/export/?format=csv&from=2026-01-01T00:00:00Z&to=2026-01-03T00:00:00Z", nil)
	rr := httptest.NewRecorder()
	NewAuditHandler(fake).Export(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if len(fake.keysetArgs) != 2 {
		t.Fatalf("keyset calls = %d, want 2", len(fake.keysetArgs))
	}
	if fake.keysetArgs[0].HasBefore {
		t.Fatal("first export page unexpectedly had a cursor")
	}
	got := fake.keysetArgs[1]
	if !got.HasBefore || !got.BeforeTime.Equal(last.CreatedAt) || got.BeforeID != last.ID {
		t.Fatalf("second cursor = %#v, want (%s,%s)", got, last.CreatedAt, last.ID)
	}
}

func TestAuditHandlerDownloadExportEnforcesOwnership(t *testing.T) {
	operationID, ownerID := uuid.New(), uuid.New()
	fake := &filteredAuditQueries{exportArtifact: sqlc.GetAuditExportArtifactRow{
		ID: operationID, RequestedBy: ownerID, Filename: "audit.csv",
		ArtifactContentType: "text/csv; charset=utf-8", Artifact: []byte("id\n1\n"),
		ArtifactSize: 5, ExpiresAt: time.Now().Add(time.Hour),
	}}
	h := NewAuditHandler(fake)
	for name, caller := range map[string]uuid.UUID{"owner": ownerID, "different user": uuid.New()} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/exports/"+operationID.String()+"/download/", nil)
			routeCtx := chi.NewRouteContext()
			routeCtx.URLParams.Add("id", operationID.String())
			ctx := context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx)
			ctx = reqctx.WithUser(ctx, &reqctx.User{ID: caller.String(), AuthMethod: "jwt"})
			req = req.WithContext(ctx)
			rr := httptest.NewRecorder()
			h.DownloadExport(rr, req)
			if caller == ownerID {
				if rr.Code != http.StatusOK || rr.Body.String() != "id\n1\n" {
					t.Fatalf("owner download status=%d body=%q", rr.Code, rr.Body.String())
				}
				return
			}
			if rr.Code != http.StatusNotFound {
				t.Fatalf("foreign download status=%d, want 404", rr.Code)
			}
		})
	}
}
