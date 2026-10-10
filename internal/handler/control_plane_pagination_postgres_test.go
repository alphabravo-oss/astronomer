package handler

import (
	"context"
	"encoding/json"
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
)

type controlPlaneSQLCapture struct {
	sqlc.DBTX
	statement string
	args      []any
	queryName string
}

func (c *controlPlaneSQLCapture) Query(ctx context.Context, statement string, args ...any) (pgx.Rows, error) {
	if c.statement == "" && strings.Contains(statement, "-- name: "+c.queryName+" :many") {
		c.statement = statement
		c.args = append([]any(nil), args...)
	}
	return c.DBTX.Query(ctx, statement, args...)
}

type controlPlanePageRow struct {
	ID         uuid.UUID `json:"id"`
	FiredAt    time.Time `json:"firedAt"`
	CreatedAt  time.Time `json:"createdAt"`
	Controller string    `json:"controller"`
}

func controlPlaneCursorPage(t *testing.T, h *ControlPlaneHandler, actor uuid.UUID, collection string, values url.Values) paging.Response[controlPlanePageRow] {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/controllers/"+collection+"/?"+values.Encode(), nil)
	req = req.WithContext(reqctx.WithUser(req.Context(), &reqctx.User{ID: actor.String()}))
	rec := httptest.NewRecorder()
	if collection == "alerts" {
		h.ListAlerts(rec, req)
	} else {
		h.ListSilences(rec, req)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("%s page status%d: %s", collection, rec.Code, rec.Body.String())
	}
	var page paging.Response[controlPlanePageRow]
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Pagination.Total != nil {
		t.Fatal("signed control-plane page fabricated a total")
	}
	return page
}
func assertControlPlanePlan(t *testing.T, nodes []paginationPlanNode, index string) bool {
	t.Helper()
	found := false
	for _, n := range nodes {
		if n.NodeType == "Sort" || n.NodeType == "Incremental Sort" || n.NodeType == "Seq Scan" {
			t.Fatalf("control-plane keyset lacks complete indexed ordering: %s", n.NodeType)
		}
		if n.IndexName == index {
			found = true
			if n.ActualRows < 0 || n.RowsRemovedByFilter < 0 || n.RowsRemovedByIndexRecheck < 0 || n.ActualLoops <= 0 || n.rowsExamined() > 201 {
				t.Fatalf("ordering index read%g rows for201-row page", n.rowsExamined())
			}
		}
		found = assertControlPlanePlan(t, n.Plans, index) || found
	}
	return found
}
func TestControlPlaneCursorPostgresTraversalTiesAndDeletedAnchor(t *testing.T) {
	for _, collection := range []string{"alerts", "silences"} {
		t.Run(collection, func(t *testing.T) {
			ctx, tx := paginationPostgresTx(t)
			controller := "p-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
			if collection == "alerts" {
				// A batch can share one timestamp; ID ordering must seek within
				// the entire tie cohort rather than sort every remaining alert.
				paginationExec(t, ctx, tx, `INSERT INTO control_plane_alerts(controller,condition_type,status,message,fired_at) SELECT $1,'fixture','resolved','fixture',TIMESTAMPTZ '2020-01-01' FROM generate_series(1,10042)n`, controller)
				paginationExec(t, ctx, tx, `ANALYZE control_plane_alerts`)
			} else {
				paginationExec(t, ctx, tx, `INSERT INTO control_plane_silences(controller,reason,starts_at,ends_at,created_at) SELECT $1,'fixture',TIMESTAMPTZ '2020-01-01',TIMESTAMPTZ '2020-01-02',TIMESTAMPTZ '2020-01-01' + (n/2)*INTERVAL '1 second' FROM generate_series(1,10042)n`, controller)
				paginationExec(t, ctx, tx, `ANALYZE control_plane_silences`)
			}
			initialCapture := &controlPlaneSQLCapture{DBTX: tx, queryName: "ListControlPlaneAlerts"}
			capture := &controlPlaneSQLCapture{DBTX: initialCapture, queryName: "ListControlPlaneAlertsAfter"}
			index := "idx_control_plane_alerts_keyset"
			if collection == "silences" {
				initialCapture.queryName = "ListControlPlaneSilences"
				capture.queryName = "ListControlPlaneSilencesAfter"
				index = "idx_control_plane_silences_keyset"
			}
			h := NewControlPlaneHandler(sqlc.New(capture), nil)
			codec, err := paging.NewCursorCodec("fixture-cursor-authority")
			if err != nil {
				t.Fatal(err)
			}
			h.SetCursorCodec(codec)
			actor := uuid.New()
			values := url.Values{"cursor": {""}, "limit": {"200"}}
			if collection == "alerts" {
				values.Set("status", "resolved")
				values.Set("controller", controller)
			}
			seen := map[uuid.UUID]bool{}
			lastTime := time.Time{}
			lastID := ""
			deleted := uuid.Nil
			pages := 0
			for {
				page := controlPlaneCursorPage(t, h, actor, collection, values)
				pages++
				if pages > 60 {
					t.Fatal("cursor did not terminate")
				}
				for _, row := range page.Data {
					timestamp := row.FiredAt
					if collection == "silences" {
						timestamp = row.CreatedAt
					}
					if row.Controller != controller {
						t.Fatal("control-plane filter leaked another controller")
					}
					if seen[row.ID] {
						t.Fatal("cursor repeated a row")
					}
					if !lastTime.IsZero() && (timestamp.After(lastTime) || timestamp.Equal(lastTime) && row.ID.String() >= lastID) {
						t.Fatal("timestamp/ID order did not progress through ties")
					}
					seen[row.ID] = true
					lastTime = timestamp
					lastID = row.ID.String()
				}
				if pages == 1 {
					anchor := page.Data[len(page.Data)-1].ID
					deleted = anchor
					if collection == "alerts" {
						paginationExec(t, ctx, tx, `DELETE FROM control_plane_alerts WHERE id=$1`, anchor)
					} else {
						paginationExec(t, ctx, tx, `DELETE FROM control_plane_silences WHERE id=$1`, anchor)
					}
				}
				if !page.Pagination.HasMore {
					if page.Pagination.NextCursor != nil {
						t.Fatal("final page advertises continuation")
					}
					break
				}
				if page.Pagination.NextCursor == nil || len(page.Data) != 200 {
					t.Fatal("actual sentinel continuation missing")
				}
				values.Set("cursor", *page.Pagination.NextCursor)
			}
			if len(seen) != 10042 || !seen[deleted] {
				t.Fatalf("traversed%d rows, want10042 including previously read deleted anchor", len(seen))
			}
			for _, measuredCapture := range []*controlPlaneSQLCapture{initialCapture, capture} {
				var plan json.RawMessage
				if err := tx.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+measuredCapture.statement, measuredCapture.args...).Scan(&plan); err != nil {
					t.Fatal(err)
				}
				t.Logf("%s %s generated keyset plan: %s", collection, measuredCapture.queryName, plan)
				var measured []paginationQueryPlan
				if err := json.Unmarshal(plan, &measured); err != nil {
					t.Fatal(err)
				}
				if len(measured) != 1 || !assertControlPlanePlan(t, measured[0].Plan.Plans, index) {
					t.Fatal("control-plane continuation lacks complete ordering index")
				}
			}

		})
	}
}

// Selective predicates can examine more index entries than the response size.
// Retain actual generated initial/continuation plans without claiming that
// the 201-row unfiltered seek bound applies to arbitrary selective filters.
func TestControlPlaneCursorPostgresSelectiveFilterPlans(t *testing.T) {
	ctx, tx := paginationPostgresTx(t)
	controller := "f-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	other := "o-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	paginationExec(t, ctx, tx, `INSERT INTO control_plane_alerts(controller,condition_type,status,message,fired_at) SELECT CASE WHEN n%3=0 THEN $1 ELSE $2 END,'fixture',CASE WHEN n%2=0 THEN 'resolved' ELSE 'firing' END,'fixture',TIMESTAMPTZ '2020-01-01' + n*INTERVAL '1 second' FROM generate_series(1,10042)n`, controller, other)
	paginationExec(t, ctx, tx, `ANALYZE control_plane_alerts`)
	codec, err := paging.NewCursorCodec("fixture-cursor-authority")
	if err != nil {
		t.Fatal(err)
	}
	for _, filters := range []struct{ name, status, controller string }{
		{"status", "resolved", ""},
		{"controller", "", controller},
		{"status and controller", "resolved", controller},
	} {
		t.Run(filters.name, func(t *testing.T) {
			actor := uuid.New()
			values := url.Values{"cursor": {""}, "limit": {"200"}}
			if filters.status != "" {
				values.Set("status", filters.status)
			}
			if filters.controller != "" {
				values.Set("controller", filters.controller)
			}
			for _, queryName := range []string{"ListControlPlaneAlerts", "ListControlPlaneAlertsAfter"} {
				capture := &controlPlaneSQLCapture{DBTX: tx, queryName: queryName}
				h := NewControlPlaneHandler(sqlc.New(capture), nil)
				h.SetCursorCodec(codec)
				page := controlPlaneCursorPage(t, h, actor, "alerts", values)
				if len(page.Data) != 200 || page.Pagination.NextCursor == nil {
					t.Fatal("selective sentinel page unavailable")
				}
				for _, row := range page.Data {
					if filters.controller != "" && row.Controller != filters.controller {
						t.Fatal("selective controller leaked")
					}
					var status string
					if err := tx.QueryRow(ctx, `SELECT status FROM control_plane_alerts WHERE id=$1`, row.ID).Scan(&status); err != nil {
						t.Fatal(err)
					}
					if filters.status != "" && status != filters.status {
						t.Fatal("selective status leaked")
					}
				}
				var raw json.RawMessage
				if capture.statement == "" {
					t.Fatal("actual generated query not captured")
				}
				if err := tx.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+capture.statement, capture.args...).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				var measured []paginationQueryPlan
				if err := json.Unmarshal(raw, &measured); err != nil {
					t.Fatal(err)
				}
				if len(measured) != 1 {
					t.Fatal("unexpected explain shape")
				}
				t.Logf("%s selective actual plan (output plus filter/recheck removals count toward examined rows): %s", queryName, raw)
				values.Set("cursor", *page.Pagination.NextCursor)
			}
		})
	}
}
