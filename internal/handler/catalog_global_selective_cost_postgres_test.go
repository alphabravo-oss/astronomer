package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
)

// Candidate-window successor: every actual Scan node is summed against the
// unchanged 250-row budget. Original 54 characterization plans remain retained.
type catalogPreparedCapture struct {
	sqlc.DBTX
	queryName string
	statement string
	args      []any
}

func (capture *catalogPreparedCapture) Query(ctx context.Context, statement string, args ...any) (pgx.Rows, error) {
	if capture.statement == "" && strings.HasPrefix(statement, "-- name: "+capture.queryName+" :many\n") {
		capture.statement = statement
		capture.args = append([]any(nil), args...)
	}
	return capture.DBTX.Query(ctx, statement, args...)
}

func explainCatalogPreparedPlan(t *testing.T, ctx context.Context, tx pgx.Tx, capture *catalogPreparedCapture) json.RawMessage {
	t.Helper()
	var name string
	if err := tx.QueryRow(ctx, `SELECT name FROM pg_prepared_statements WHERE statement=$1`, capture.statement).Scan(&name); err != nil {
		t.Fatal(err)
	}
	literals := make([]string, 0, len(capture.args))
	for _, arg := range capture.args {
		var value, cast string
		switch arg := arg.(type) {
		case int32:
			literals = append(literals, strconv.FormatInt(int64(arg), 10))
			continue
		case bool:
			literals = append(literals, strconv.FormatBool(arg))
			continue
		case string:
			value, cast = arg, "::text"
		case uuid.UUID:
			value, cast = arg.String(), "::uuid"
		case []uuid.UUID:
			if arg == nil {
				literals = append(literals, "NULL::uuid[]")
				continue
			}
			ids := make([]string, len(arg))
			for i, id := range arg {
				ids[i] = id.String()
			}
			value, cast = "{"+strings.Join(ids, ",")+"}", "::uuid[]"
		default:
			t.Fatalf("unowned catalog query parameter type %T", arg)
		}
		var quoted string
		if err := tx.QueryRow(ctx, `SELECT pg_catalog.quote_literal($1::text)`, value).Scan(&quoted); err != nil {
			t.Fatal(err)
		}
		literals = append(literals, quoted+cast)
	}
	var raw json.RawMessage
	statement := "EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) EXECUTE " + pgx.Identifier{name}.Sanitize() + "(" + strings.Join(literals, ",") + ")"
	if err := tx.QueryRow(ctx, statement, pgx.QueryExecModeSimpleProtocol).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func catalogPlanExamined(t *testing.T, raw json.RawMessage) float64 {
	t.Helper()
	var plans []struct {
		Plan paginationPlanNode `json:"Plan"`
	}
	if err := json.Unmarshal(raw, &plans); err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 {
		t.Fatal("unexpected catalog explain shape")
	}
	var examined float64
	var visit func(paginationPlanNode)
	visit = func(node paginationPlanNode) {
		if strings.Contains(node.NodeType, "Scan") {
			examined += node.rowsExamined()
		}
		for _, child := range node.Plans {
			visit(child)
		}
	}
	visit(plans[0].Plan)
	return examined
}

// Attach every actual handler query to the bounded fixture context. The
// existing shared page helper uses request Background and is unchanged.
func catalogPreparedCursorPage(t *testing.T, ctx context.Context, h *CatalogHandler, actor uuid.UUID, values url.Values) paging.Response[sqlc.HelmChart] {
	t.Helper()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/catalog/charts/?"+values.Encode(), nil)
	req = req.WithContext(reqctx.WithUser(req.Context(), &reqctx.User{ID: actor.String()}))
	rec := httptest.NewRecorder()
	h.ListCharts(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("page status=%d body=%s", rec.Code, rec.Body.String())
	}
	var page paging.Response[sqlc.HelmChart]
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Pagination.Total != nil {
		t.Fatal("cursor page fabricated or repeated an exact count")
	}
	return page
}

func TestCatalogGlobalCursorPostgresPreparedSelectiveCharacterization(t *testing.T) {
	ctx, tx := paginationPostgresTx(t)
	if tx.Conn().Config().DefaultQueryExecMode != pgx.QueryExecModeCacheStatement {
		t.Fatal("production statement cache required")
	}
	var existing int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM helm_charts`).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	if existing != 0 {
		t.Fatal("controlled fixture requires an empty exclusively owned cursor database; existing rows are never removed")
	}
	prefix := "catalogcost-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	commonTag, rareTag, needle, absent := "common-"+prefix, "rare-"+prefix, "needle-"+prefix, "absent-"+prefix
	cluster, project := uuid.New(), uuid.New()
	paginationExec(t, ctx, tx, `INSERT INTO clusters(id,name,display_name) VALUES($1,$2,$2)`, cluster, prefix)
	paginationExec(t, ctx, tx, `INSERT INTO projects(id,name,display_name,cluster_id) VALUES($1,$2,$2,$3)`, project, prefix, cluster)
	repos := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	for i, repo := range repos {
		var owner any
		if i == 4 {
			owner = project
		}
		paginationExec(t, ctx, tx, `INSERT INTO helm_repositories(id,name,url,owner_project_id) VALUES($1,$2,'https://example.invalid/owned-fixture',$3)`, repo, repo.String(), owner)
		paginationExec(t, ctx, tx, `INSERT INTO helm_charts(repository_id,name,description)
SELECT $1,$2||'-'||lpad(n::text,5,'0'),CASE WHEN n%101=0 THEN $3 ELSE 'common' END
FROM generate_series(1,20000)n`, repo, prefix, needle)
	}
	paginationExec(t, ctx, tx, `INSERT INTO helm_chart_tags(chart_id,tag)
SELECT id,CASE WHEN right(name,5)::integer%100=0 THEN $1 ELSE $2 END
FROM helm_charts WHERE repository_id=ANY($3::uuid[])`, rareTag, commonTag, repos)
	for _, table := range []string{"helm_charts", "helm_repositories", "helm_chart_tags", "catalog_global_chart_keys"} {
		// Only these three fixed identifiers are admitted; no operator SQL input.
		paginationExec(t, ctx, tx, "ANALYZE "+table)
	}
	codec, err := paging.NewCursorCodec("fixture-cursor-authority")
	if err != nil {
		t.Fatal(err)
	}
	var anchorID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM helm_charts WHERE repository_id=$1 ORDER BY name,id LIMIT 1`, repos[0]).Scan(&anchorID); err != nil {
		t.Fatal(err)
	}
	for _, filter := range []struct {
		name, tag, search            string
		expectedFirst, expectedAfter int
		advertised                   bool
	}{
		{"unfiltered", "", "", 16, 16, true}, {"common tag", commonTag, "", 16, 16, true}, {"sparse tag", rareTag, "", 0, 0, true},
		{"common search", "", prefix, 16, 16, true}, {"sparse search", "", needle, 0, 0, true},
		{"common tag and search", commonTag, prefix, 16, 16, true}, {"sparse correlated tag and search", rareTag, needle, 0, 0, true},
		{"absent tag", absent, "", 0, 0, true}, {"absent search", "", absent, 0, 0, true},
	} {
		for _, mode := range []string{"auto", "force_generic_plan", "force_custom_plan"} {
			t.Run(filter.name+"/"+mode, func(t *testing.T) {
				if _, err := tx.Exec(ctx, "SET LOCAL plan_cache_mode = "+mode); err != nil {
					t.Fatal(err)
				}
				actor := uuid.New()
				values := url.Values{"cursor": {""}, "limit": {"200"}}
				if filter.tag != "" {
					values.Set("tag", filter.tag)
				}
				if filter.search != "" {
					values.Set("search", filter.search)
				}
				for _, phase := range []string{"First", "After"} {
					name := "ListCatalogGlobalCandidateWindowFirst"
					if phase == "After" {
						name = "ListCatalogGlobalCandidateWindowAfter"
					}
					capture := &catalogPreparedCapture{DBTX: tx, queryName: name}
					h := NewCatalogHandler(sqlc.New(capture))
					h.SetCursorCodec(codec)
					h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: []rbac.RoleBinding{{RoleRules: []rbac.Rule{{Resource: "catalog", Verbs: []string{"read"}}}}}})
					before := readCachedControlPlaneCounters(t, ctx, tx, name)
					page := catalogPreparedCursorPage(t, ctx, h, actor, values)
					for warm := 0; warm < 7; warm++ {
						catalogPreparedCursorPage(t, ctx, h, actor, values)
					}
					assertCachedControlPlaneDelta(t, mode, name, before, readCachedControlPlaneCounters(t, ctx, tx, name))
					if capture.statement == "" {
						t.Fatal("production catalog query was not captured")
					}
					expected := filter.expectedFirst
					if phase == "After" {
						expected = filter.expectedAfter
					}
					if len(page.Data) != expected {
						t.Fatalf("%s/%s returned=%d expected=%d", filter.name, phase, len(page.Data), expected)
					}
					if (page.Pagination.NextCursor != nil) != filter.advertised {
						t.Fatalf("%s/%s advertised continuation=%v expected=%v", filter.name, phase, page.Pagination.NextCursor != nil, filter.advertised)
					}
					for _, row := range page.Data {
						if row.RepositoryID == repos[4] {
							t.Fatal("private chart leaked into global visibility")
						}
						if filter.tag != "" {
							var found bool
							if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM helm_chart_tags WHERE chart_id=$1 AND tag=$2)`, row.ID, filter.tag).Scan(&found); err != nil {
								t.Fatal(err)
							}
							if !found {
								t.Fatal("tag predicate leaked")
							}
						}
						if filter.search != "" && !strings.Contains(strings.ToLower(row.Name+" "+row.DisplayName+" "+row.Description), strings.ToLower(filter.search)) {
							t.Fatal("search predicate leaked")
						}
					}
					raw := explainCatalogPreparedPlan(t, ctx, tx, capture)
					t.Logf("catalog100k filter=%s mode=%s query=%s phase=%s examined=%g actual cached plan=%s", filter.name, mode, name, phase, catalogPlanExamined(t, raw), raw)
					if examined := catalogPlanExamined(t, raw); examined > 250 {
						t.Fatalf("%s/%s candidate plan examined=%g exceeds unchanged 250 budget", filter.name, phase, examined)
					}
					if phase == "First" {
						if page.Pagination.NextCursor != nil {
							values.Set("cursor", *page.Pagination.NextCursor)
						} else {
							// No advertised continuation for short/empty pages. A producer-signed
							// synthetic lower position measures the real After query separately.
							binding := paging.Binding("catalog-charts:v2", "name:asc,repository_id:asc,id:asc", actor.String(), "true", "", filter.tag, literalChartSearchPattern(filter.search), "", "")
							cursor, err := codec.Encode(paging.NewTextCursor("", anchorID, &repos[0], binding, 0))
							if err != nil {
								t.Fatal(err)
							}
							values.Set("cursor", cursor)
						}
					}
				}
			})
		}
	}
}
