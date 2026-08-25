package handler

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func (f *fakeRatingsQuerier) LockChartRatingMutationKey(_ context.Context, key string) error {
	f.lockKeys = append(f.lockKeys, key)
	return nil
}

func (f *fakeRatingsQuerier) GetChartRatingByIDForUpdate(ctx context.Context, id uuid.UUID) (sqlc.ChartRating, error) {
	return f.GetChartRatingByID(ctx, id)
}

func (f *fakeRatingsQuerier) GetChartRatingByUserAndInstallationForUpdate(ctx context.Context, arg sqlc.GetChartRatingByUserAndInstallationParams) (sqlc.ChartRating, error) {
	return f.GetChartRatingByUserAndInstallation(ctx, arg)
}

func (f *fakeRatingsQuerier) GetChartRatingByUserAndChartNoInstallForUpdate(ctx context.Context, arg sqlc.GetChartRatingByUserAndChartNoInstallParams) (sqlc.ChartRating, error) {
	return f.GetChartRatingByUserAndChartNoInstall(ctx, arg)
}

func (f *fakeRatingsQuerier) GetUserByIDForUpdate(ctx context.Context, id uuid.UUID) (sqlc.User, error) {
	return f.GetUserByID(ctx, id)
}

func (f *fakeRatingsQuerier) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if f.outboxErr != nil {
		return sqlc.AuditOutbox{}, f.outboxErr
	}
	f.audits = append(f.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action, ResourceType: arg.ResourceType, ResourceID: arg.ResourceID, Detail: arg.Detail}, nil
}

func cloneRatingMap[T any](source map[uuid.UUID]T) map[uuid.UUID]T {
	out := make(map[uuid.UUID]T, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

func fakeRatingsRunTx(q *fakeRatingsQuerier) chartRatingRunTxFunc {
	return func(_ context.Context, fn func(ChartRatingMutationTx) error) error {
		ratings := cloneRatingMap(q.ratings)
		aggregates := cloneRatingMap(q.aggregates)
		audits := append([]sqlc.UpsertAuditOutboxParams(nil), q.audits...)
		locks := append([]string(nil), q.lockKeys...)
		if err := fn(q); err != nil {
			q.ratings, q.aggregates, q.audits, q.lockKeys = ratings, aggregates, audits, locks
			return err
		}
		return nil
	}
}

func TestEveryChartRatingMutationUsesTransactionalExecutor(t *testing.T) {
	path, err := filepath.Abs("chart_ratings.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"CreateRating": false, "UpdateRating": false, "DeleteRating": false}
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if _, tracked := want[fn.Name.Name]; !tracked {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if ok {
				if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "executeChartRatingMutation" {
					want[fn.Name.Name] = true
				}
			}
			return true
		})
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s does not use executeChartRatingMutation", name)
		}
	}
}

func TestChartRatingAuditFailureRollsBackEveryMutationAndAggregate(t *testing.T) {
	for _, operation := range []string{"create", "upsert", "update", "delete"} {
		t.Run(operation, func(t *testing.T) {
			h, q, chartID, userID := newRatingsHandler(t)
			seedID := uuid.New()
			if operation != "create" {
				q.ratings[seedID] = sqlc.ChartRating{ID: seedID, ChartID: chartID, UserID: userID, Stars: 2, Note: "original"}
			}
			q.aggregates[chartID] = sqlc.ChartRatingAggregate{ChartID: chartID, RatingCount: 7, RatingSum: 14}
			q.outboxErr = errors.New("audit-SENTINEL")
			router := mountRatings(h)
			var req *http.Request
			switch operation {
			case "create":
				req = doAuth(http.MethodPost, "/charts/"+chartID.String()+"/ratings/", []byte(`{"stars":5,"note":"new"}`), userID)
			case "upsert":
				req = doAuth(http.MethodPost, "/charts/"+chartID.String()+"/ratings/", []byte(`{"stars":5,"note":"replacement"}`), userID)
			case "update":
				req = doAuth(http.MethodPut, "/charts/"+chartID.String()+"/ratings/"+seedID.String()+"/", []byte(`{"stars":5,"note":"replacement"}`), userID)
			case "delete":
				req = doAuth(http.MethodDelete, "/charts/"+chartID.String()+"/ratings/"+seedID.String()+"/", nil, userID)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "audit_unavailable") || strings.Contains(w.Body.String(), "audit-SENTINEL") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if operation == "create" && len(q.ratings) != 0 {
				t.Fatalf("create rollback retained ratings=%+v", q.ratings)
			}
			if operation != "create" {
				got, ok := q.ratings[seedID]
				if !ok || got.Stars != 2 || got.Note != "original" {
					t.Fatalf("%s rollback row=%+v present=%t", operation, got, ok)
				}
			}
			if got := q.aggregates[chartID]; got.RatingCount != 7 || got.RatingSum != 14 {
				t.Fatalf("%s rollback aggregate=%+v", operation, got)
			}
			if len(q.audits) != 0 {
				t.Fatalf("rolled-back audits=%+v", q.audits)
			}
		})
	}
}

func TestChartRatingAggregateFailureRollsBackMutationBeforeAudit(t *testing.T) {
	h, q, chartID, userID := newRatingsHandler(t)
	q.aggregateErr = errors.New("aggregate-SENTINEL")
	w := httptest.NewRecorder()
	mountRatings(h).ServeHTTP(w, doAuth(http.MethodPost, "/charts/"+chartID.String()+"/ratings/", []byte(`{"stars":5}`), userID))
	if w.Code != http.StatusInternalServerError || len(q.ratings) != 0 || len(q.audits) != 0 {
		t.Fatalf("status=%d ratings=%+v audits=%+v body=%s", w.Code, q.ratings, q.audits, w.Body.String())
	}
}

func TestChartRatingAuditMetadataOmitsReviewText(t *testing.T) {
	h, q, chartID, userID := newRatingsHandler(t)
	secretReview := "private-review-SENTINEL"
	w := httptest.NewRecorder()
	mountRatings(h).ServeHTTP(w, doAuth(http.MethodPost, "/charts/"+chartID.String()+"/ratings/", []byte(`{"stars":4,"note":"`+secretReview+`"}`), userID))
	if w.Code != http.StatusCreated || len(q.audits) != 1 {
		t.Fatalf("status=%d audits=%d body=%s", w.Code, len(q.audits), w.Body.String())
	}
	detail := string(q.audits[0].Detail)
	if strings.Contains(detail, secretReview) || strings.Contains(detail, "note") || strings.Contains(detail, "comment") {
		t.Fatalf("audit leaked review material: %s", detail)
	}
	if q.audits[0].Action != "chart.rating.created" || !strings.Contains(detail, chartID.String()) || !strings.Contains(detail, `"stars":4`) {
		t.Fatalf("audit metadata is incomplete: %+v", q.audits[0])
	}
}

func TestChartRatingMutationFailsClosedWithoutTransactionRunner(t *testing.T) {
	q := newFakeRatingsQuerier()
	chartID, userID := uuid.New(), uuid.New()
	q.charts[chartID] = sqlc.HelmChart{ID: chartID}
	h := NewChartRatingsHandler(q)
	w := httptest.NewRecorder()
	mountRatings(h).ServeHTTP(w, doAuth(http.MethodPost, "/charts/"+chartID.String()+"/ratings/", []byte(`{"stars":5}`), userID))
	if w.Code != http.StatusServiceUnavailable || len(q.ratings) != 0 {
		t.Fatalf("status=%d ratings=%+v body=%s", w.Code, q.ratings, w.Body.String())
	}
}

func TestChartRatingUpdateRejectsCrossChartURLWithoutMutation(t *testing.T) {
	h, q, chartID, userID := newRatingsHandler(t)
	otherChartID := uuid.New()
	q.charts[otherChartID] = sqlc.HelmChart{ID: otherChartID}
	ratingID := uuid.New()
	q.ratings[ratingID] = sqlc.ChartRating{ID: ratingID, ChartID: chartID, UserID: userID, Stars: 2, Note: "original"}
	w := httptest.NewRecorder()
	mountRatings(h).ServeHTTP(w, doAuth(http.MethodPut, "/charts/"+otherChartID.String()+"/ratings/"+ratingID.String()+"/", []byte(`{"stars":5,"note":"replacement"}`), userID))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if got := q.ratings[ratingID]; got.Stars != 2 || got.Note != "original" {
		t.Fatalf("cross-chart update mutated row=%+v", got)
	}
}
