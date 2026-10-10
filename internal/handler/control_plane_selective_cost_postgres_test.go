package handler

// Source-only larger-cardinality characterization draft; not applied or compiled.
import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// explainCachedControlPlanePlan measures the exact prepared statement already
// executed through the production generated query and pgx statement cache. It
// does not EXPLAIN a fresh parameterized text statement with a separate plan.
func explainCachedControlPlanePlan(t *testing.T, ctx context.Context, tx pgx.Tx, capture *controlPlaneSQLCapture) json.RawMessage {
	t.Helper()
	var name string
	var generic, custom int64
	if err := tx.QueryRow(ctx, `SELECT name,generic_plans,custom_plans FROM pg_prepared_statements WHERE statement=$1`, capture.statement).Scan(&name, &generic, &custom); err != nil {
		t.Fatal(err)
	}
	args := make([]string, 0, len(capture.args))
	for _, arg := range capture.args {
		var text, cast string
		switch value := arg.(type) {
		case int32:
			args = append(args, strconv.FormatInt(int64(value), 10))
			continue
		case pgtype.Text:
			if !value.Valid {
				args = append(args, "NULL::text")
				continue
			}
			text, cast = value.String, "::text"
		case time.Time:
			text, cast = value.UTC().Format(time.RFC3339Nano), "::timestamptz"
		case uuid.UUID:
			text, cast = value.String(), "::uuid"
		default:
			t.Fatalf("unowned control-plane parameter type %T", arg)
		}
		// Let the same server quote synthetic fixture values correctly for its
		// string mode. Neither identifiers nor literals come from operator input.
		var quoted string
		if err := tx.QueryRow(ctx, `SELECT pg_catalog.quote_literal($1::text)`, text).Scan(&quoted); err != nil {
			t.Fatal(err)
		}
		args = append(args, quoted+cast)
	}
	var raw json.RawMessage
	statement := "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) EXECUTE " + pgx.Identifier{name}.Sanitize() + "(" + strings.Join(args, ",") + ")"
	if err := tx.QueryRow(ctx, statement, pgx.QueryExecModeSimpleProtocol).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	t.Logf("actual cached statement before EXPLAIN EXECUTE: generic_plans=%d custom_plans=%d", generic, custom)
	return raw
}

type cachedControlPlaneCounters struct{ generic, custom int64 }

// A statement may not exist before its first production execution. Subsequent
// cases retain the same physical cache, so assertions use deltas, not totals.
func readCachedControlPlaneCounters(t *testing.T, ctx context.Context, tx pgx.Tx, queryName string) cachedControlPlaneCounters {
	t.Helper()
	var result cachedControlPlaneCounters
	var statements int
	if err := tx.QueryRow(ctx, `SELECT count(*),COALESCE(sum(generic_plans),0),COALESCE(sum(custom_plans),0) FROM pg_prepared_statements WHERE split_part(statement, E'\n', 1)=$1`, "-- name: "+queryName+" :many").Scan(&statements, &result.generic, &result.custom); err != nil {
		t.Fatal(err)
	}
	if statements > 1 {
		t.Fatal("ambiguous production prepared statement")
	}
	return result
}

func assertCachedControlPlaneDelta(t *testing.T, mode string, before, after cachedControlPlaneCounters) {
	t.Helper()
	generic, custom := after.generic-before.generic, after.custom-before.custom
	t.Logf("eight production executions mode=%s counters before=(generic:%d custom:%d) after=(generic:%d custom:%d) delta=(generic:%d custom:%d)", mode, before.generic, before.custom, after.generic, after.custom, generic, custom)
	if generic < 0 || custom < 0 || generic+custom != 8 {
		t.Fatalf("expected exactly eight cached production executions, got generic=%d custom=%d", generic, custom)
	}
	if (mode == "force_generic_plan" && (generic != 8 || custom != 0)) || (mode == "force_custom_plan" && (custom != 8 || generic != 0)) {
		t.Fatal("actual production cache counters contradict forced plan mode")
	}
}

func TestControlPlaneCursorPostgresRepresentativeSelectiveCost(t *testing.T) {
	ctx, tx := paginationPostgresTx(t)
	if tx.Conn().Config().DefaultQueryExecMode != pgx.QueryExecModeCacheStatement {
		t.Fatal("fixture must retain production-default pgx statement-cache mode")
	}
	common := "common-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	rare := "rare-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	absent := "absent-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	// 100k rows: about 1% currently active, 1% one sparse controller, and an
	// intentionally correlated subset with only nine active sparse rows.
	// Actual production writer states are active/resolved, not fixture-only
	// status aliases. The transaction rolls back every inserted row.
	paginationExec(t, ctx, tx, `INSERT INTO control_plane_alerts(controller,condition_type,status,message,fired_at)
SELECT CASE WHEN n%100=0 THEN $1 ELSE $2 END, 'fixture',
CASE WHEN n%101=0 THEN 'active' ELSE 'resolved' END, 'fixture',
TIMESTAMPTZ '2020-01-01' + (n/2)*INTERVAL '1 second'
FROM generate_series(1,100000)n`, rare, common)
	paginationExec(t, ctx, tx, `ANALYZE control_plane_alerts`)
	codec, err := paging.NewCursorCodec("fixture-cursor-authority")
	if err != nil {
		t.Fatal(err)
	}
	for _, filter := range []struct{ name, status, controller string }{
		{"unfiltered", "", ""},
		{"common status", "resolved", ""},
		{"sparse status", "active", ""},
		{"common controller", "", common},
		{"sparse controller", "", rare},
		{"common status and controller", "resolved", common},
		{"sparse status and controller", "active", rare},
		{"no matching controller", "", absent},
		{"no matching combination", "active", absent},
	} {
		for _, mode := range []string{"auto", "force_generic_plan", "force_custom_plan"} {
			t.Run(filter.name+"/"+mode, func(t *testing.T) {
				if _, err := tx.Exec(ctx, "SET LOCAL plan_cache_mode = "+mode); err != nil {
					t.Fatal(err)
				}
				values := url.Values{"cursor": {""}, "limit": {"200"}}
				if filter.status != "" {
					values.Set("status", filter.status)
				}
				if filter.controller != "" {
					values.Set("controller", filter.controller)
				}
				actor := uuid.New()
				for _, name := range []string{"ListControlPlaneAlerts", "ListControlPlaneAlertsAfter"} {
					capture := &controlPlaneSQLCapture{DBTX: tx, queryName: name}
					h := NewControlPlaneHandler(sqlc.New(capture), nil)
					h.SetCursorCodec(codec)
					before := readCachedControlPlaneCounters(t, ctx, tx, name)
					page := controlPlaneCursorPage(t, h, actor, "alerts", values)
					// Exercise the exact generated request repeatedly on the same physical
					// connection so auto can cross its custom/generic plan threshold.
					for warm := 0; warm < 7; warm++ {
						controlPlaneCursorPage(t, h, actor, "alerts", values)
					}
					assertCachedControlPlaneDelta(t, mode, before, readCachedControlPlaneCounters(t, ctx, tx, name))
					if capture.statement == "" {
						t.Fatal("actual generated statement was not captured")
					}
					for _, row := range page.Data {
						if filter.controller != "" && row.Controller != filter.controller {
							t.Fatal("controller predicate leaked")
						}
						var status string
						if err := tx.QueryRow(ctx, `SELECT status FROM control_plane_alerts WHERE id=$1`, row.ID).Scan(&status); err != nil {
							t.Fatal(err)
						}
						if filter.status != "" && status != filter.status {
							t.Fatal("status predicate leaked")
						}
					}
					raw := explainCachedControlPlanePlan(t, ctx, tx, capture)
					var measured []paginationQueryPlan
					if err := json.Unmarshal(raw, &measured); err != nil {
						t.Fatal(err)
					}
					if len(measured) != 1 {
						t.Fatal("unexpected explain shape")
					}
					t.Logf("representative100k %s %s %s actual cached plan; examined index rows include removals and loops: %s", filter.name, mode, name, raw)
					if page.Pagination.NextCursor == nil {
						// An empty/short page honestly emits no continuation. Measure the
						// exact typed After SQL separately with a fixture upper anchor,
						// without claiming an HTTP producer advertised another page.
						if name == "ListControlPlaneAlerts" {
							shortCapture := &controlPlaneSQLCapture{DBTX: tx, queryName: "ListControlPlaneAlertsAfter"}
							query := sqlc.New(shortCapture)
							params := sqlc.ListControlPlaneAlertsAfterParams{
								Status:       pgtype.Text{String: filter.status, Valid: filter.status != ""},
								Controller:   pgtype.Text{String: filter.controller, Valid: filter.controller != ""},
								AfterFiredAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Add(50001 * time.Second),
								AfterID:      uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff"),
								QueryLimit:   201,
							}
							before := readCachedControlPlaneCounters(t, ctx, tx, "ListControlPlaneAlertsAfter")
							for warm := 0; warm < 8; warm++ {
								rows, err := query.ListControlPlaneAlertsAfter(ctx, params)
								if err != nil {
									t.Fatal(err)
								}
								for _, row := range rows {
									if (filter.status != "" && row.Status != filter.status) || (filter.controller != "" && row.Controller != filter.controller) {
										t.Fatal("short continuation predicate leaked")
									}
								}
								if strings.HasPrefix(filter.name, "no matching") && len(rows) != 0 {
									t.Fatal("no-match continuation returned data")
								}
							}
							assertCachedControlPlaneDelta(t, mode, before, readCachedControlPlaneCounters(t, ctx, tx, "ListControlPlaneAlertsAfter"))
							raw := explainCachedControlPlanePlan(t, ctx, tx, shortCapture)
							t.Logf("representative100k %s %s synthetic upper anchor exact After SQL (no advertised continuation): %s", filter.name, mode, raw)
						}
						break
					}
					values.Set("cursor", *page.Pagination.NextCursor)
				}
			})
		}
	}
}
