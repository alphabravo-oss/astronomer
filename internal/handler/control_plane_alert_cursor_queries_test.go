package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type controlPlaneCursorDispatchDB struct {
	statement string
	args      []any
	ctx       context.Context
}

func (*controlPlaneCursorDispatchDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	panic("cursor read attempted a write")
}
func (*controlPlaneCursorDispatchDB) QueryRow(context.Context, string, ...any) pgx.Row {
	panic("cursor list attempted an unrelated query")
}
func (db *controlPlaneCursorDispatchDB) Query(ctx context.Context, statement string, args ...any) (pgx.Rows, error) {
	db.statement, db.args, db.ctx = statement, args, ctx
	return nil, errors.New("synthetic captured SQL boundary")
}

// Capture the actual generated query and parameters reached by the handler,
// rather than mock each finite helper method. PostgreSQL cost/traversal tests
// independently exercise successful rows through these same generated calls.
func TestControlPlaneCursorDispatchUsesEightFiniteGeneratedQueries(t *testing.T) {
	actor := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	anchor := uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	stamp := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	codec, err := paging.NewCursorCodec("fixture-cursor-authority")
	if err != nil {
		t.Fatal(err)
	}
	for _, filter := range []struct{ variant, status, controller string }{
		{"All", "", ""}, {"Status", "active", ""},
		{"Controller", "", "fixture-controller"}, {"StatusController", "active", "fixture-controller"},
	} {
		for _, phase := range []string{"First", "After"} {
			t.Run(filter.variant+phase, func(t *testing.T) {
				db := &controlPlaneCursorDispatchDB{}
				h := NewControlPlaneHandler(sqlc.New(db), nil)
				h.SetCursorCodec(codec)
				values := url.Values{"cursor": {""}, "limit": {"200"}}
				var expected []any
				if filter.status != "" {
					values.Set("status", filter.status)
					expected = append(expected, filter.status)
				}
				if filter.controller != "" {
					values.Set("controller", filter.controller)
					expected = append(expected, filter.controller)
				}
				if phase == "After" {
					binding := paging.Binding("control-plane-alerts:v2", "global-administration", "fired_at:desc,id:desc", actor.String(), filter.status, filter.controller)
					token, err := codec.Encode(paging.NewCursor(stamp, anchor, binding, 200))
					if err != nil {
						t.Fatal(err)
					}
					values.Set("cursor", token)
					expected = append(expected, stamp, anchor)
				}
				expected = append(expected, int32(201))
				r := httptest.NewRequest(http.MethodGet, "/controllers/alerts/?"+values.Encode(), nil)
				r = r.WithContext(reqctx.WithUser(r.Context(), &reqctx.User{ID: actor.String()}))
				w := httptest.NewRecorder()
				h.ListAlerts(w, r)
				name := "ListControlPlaneCursorAlerts" + filter.variant + phase
				if w.Code != http.StatusInternalServerError || !strings.HasPrefix(db.statement, "-- name: "+name+" :many\n") || strings.Contains(db.statement, " OR ") || db.ctx != r.Context() {
					t.Fatalf("finite dispatch did not reach %s with request context", name)
				}
				if len(expected) != len(db.args) {
					t.Fatalf("parameter count=%d, want%d", len(db.args), len(expected))
				}
				for index, value := range expected {
					if db.args[index] != value {
						t.Fatalf("bound parameter%d changed", index)
					}
				}
			})
		}
	}
}

func TestControlPlaneCursorAbsentOptInKeepsLegacyGeneratedQuery(t *testing.T) {
	db := &controlPlaneCursorDispatchDB{}
	h := NewControlPlaneHandler(sqlc.New(db), nil)
	r := httptest.NewRequest(http.MethodGet, "/controllers/alerts/?limit=25&offset=25&status=active", nil)
	w := httptest.NewRecorder()
	h.ListAlerts(w, r)
	if w.Code != http.StatusInternalServerError || !strings.HasPrefix(db.statement, "-- name: ListControlPlaneAlerts :many\n") || len(db.args) != 4 || db.args[0] != int32(25) || db.args[1] != int32(25) {
		t.Fatal("legacy offset query or exact requested limit/offset changed")
	}
}
