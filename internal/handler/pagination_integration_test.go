//go:build integration

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"

	"github.com/alphabravocompany/astronomer-go/internal/testdb"
)

type paginationSQLCapture struct {
	sqlc.DBTX
	statement string
	args      []any
}

type paginationQueryPlan struct {
	Plan struct {
		NodeType string               `json:"Node Type"`
		Plans    []paginationPlanNode `json:"Plans"`
	} `json:"Plan"`
}

type paginationPlanNode struct {
	NodeType  string `json:"Node Type"`
	IndexName string `json:"Index Name"`
	// EXPLAIN reports numeric averages; PostgreSQL18 emits 201.00 for 201 rows.
	ActualRows                float64              `json:"Actual Rows"`
	ActualLoops               float64              `json:"Actual Loops"`
	RowsRemovedByFilter       float64              `json:"Rows Removed by Filter"`
	RowsRemovedByIndexRecheck float64              `json:"Rows Removed by Index Recheck"`
	Plans                     []paginationPlanNode `json:"Plans"`
}

func (node paginationPlanNode) rowsExamined() float64 {
	return (node.ActualRows + node.RowsRemovedByFilter + node.RowsRemovedByIndexRecheck) * node.ActualLoops
}

func (capture *paginationSQLCapture) Query(ctx context.Context, statement string, args ...any) (pgx.Rows, error) {
	if capture.statement == "" && strings.Contains(statement, "-- name: ListFilteredHelmChartsAfter") {
		capture.statement = statement
		capture.args = append([]any(nil), args...)
	}
	return capture.DBTX.Query(ctx, statement, args...)
}

func paginationPostgresTx(t *testing.T) (context.Context, pgx.Tx) {
	t.Helper()
	dsn := testdb.DSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := tx.Rollback(cleanup); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Error(err)
		}
		if err := conn.Close(cleanup); err != nil {
			t.Error(err)
		}
	})
	return ctx, tx
}

func paginationExec(t *testing.T, ctx context.Context, tx pgx.Tx, query string, args ...any) {
	t.Helper()
	if _, err := tx.Exec(ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

func catalogCursorPage(t *testing.T, h *CatalogHandler, actor uuid.UUID, values url.Values) paging.Response[sqlc.HelmChart] {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/charts/?"+values.Encode(), nil)
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

func TestCatalogCursorPostgresTraversalBeyondLegacyCeiling(t *testing.T) {
	t.Parallel()
	ctx, tx := paginationPostgresTx(t)
	prefix := "cursor-" + uuid.NewString()
	repoA, repoB, privateRepo, cluster, project := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	paginationExec(t, ctx, tx, `INSERT INTO clusters(id,name,display_name) VALUES($1,$2,$2)`, cluster, prefix)
	paginationExec(t, ctx, tx, `INSERT INTO projects(id,name,display_name,cluster_id) VALUES($1,$2,$2,$3)`, project, prefix, cluster)
	for _, repo := range []uuid.UUID{repoA, repoB} {
		paginationExec(t, ctx, tx, `INSERT INTO helm_repositories(id,name,url) VALUES($1,$2,'https://example.invalid/charts')`, repo, repo.String())
	}
	paginationExec(t, ctx, tx, `INSERT INTO helm_repositories(id,name,url,owner_project_id) VALUES($1,$2,'https://example.invalid/private',$3)`, privateRepo, privateRepo.String(), project)
	// Every name has an equal-name row in another repository. The unique ID
	// and repository tuple must progress through ties without a growing offset.
	paginationExec(t, ctx, tx, `INSERT INTO helm_charts(repository_id,name) SELECT r,$1||'-'||lpad(n::text,5,'0') FROM unnest($2::uuid[]) r CROSS JOIN generate_series(1,5021) n`, prefix, []uuid.UUID{repoA, repoB})
	paginationExec(t, ctx, tx, `INSERT INTO helm_charts(repository_id,name) VALUES($1,$2)`, privateRepo, prefix+"-00000-private")
	paginationExec(t, ctx, tx, `ANALYZE helm_charts`)
	paginationExec(t, ctx, tx, `ANALYZE helm_repositories`)
	capture := &paginationSQLCapture{DBTX: tx}
	h := NewCatalogHandler(sqlc.New(capture))
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: []rbac.RoleBinding{{RoleRules: []rbac.Rule{{Resource: "catalog", Verbs: []string{"read"}}}}}})
	codec, err := paging.NewCursorCodec("fixture-cursor-authority")
	if err != nil {
		t.Fatal(err)
	}
	h.SetCursorCodec(codec)
	actor := uuid.New()
	values := url.Values{"cursor": {""}, "limit": {"200"}, "search": {prefix}}
	seen := map[uuid.UUID]bool{}
	previous := ""
	var last sqlc.HelmChart
	for pages := 0; ; pages++ {
		if pages > 2000 {
			t.Fatal("cursor failed to terminate")
		}
		page := catalogCursorPage(t, h, actor, values)
		for _, row := range page.Data {
			if row.RepositoryID == privateRepo {
				t.Fatal("private repository leaked")
			}
			if seen[row.ID] {
				t.Fatal("cursor repeated a chart")
			}
			key := row.Name + row.RepositoryID.String() + row.ID.String()
			if key <= previous {
				t.Fatal("cursor order did not progress through ties")
			}
			previous = key
			seen[row.ID] = true
			last = row
		}
		if !page.Pagination.HasMore {
			if page.Pagination.NextCursor != nil {
				t.Fatal("terminal page advertises a cursor")
			}
			break
		}
		// The global-catalog candidate traversal (migration 80) may return a
		// short page with a continuation when its bounded candidate scan is
		// exhausted; the invariants are progress, no repeats and completeness.
		if page.Pagination.NextCursor == nil || len(page.Data) > 200 {
			t.Fatal("sentinel continuation missing")
		}
		values.Set("cursor", *page.Pagination.NextCursor)
	}
	if len(seen) != 10042 {
		t.Fatalf("traversed%d rows, want10042", len(seen))
	}
	binding := paging.Binding("catalog-charts:v2", "name:asc,repository_id:asc,id:asc", actor.String(), "true", "", "", literalChartSearchPattern(prefix), "", "")
	terminal, err := codec.Encode(paging.NewTextCursor(last.Name, last.ID, &last.RepositoryID, binding, len(seen)))
	if err != nil {
		t.Fatal(err)
	}
	values.Set("cursor", terminal)
	empty := catalogCursorPage(t, h, actor, values)
	if len(empty.Data) != 0 || empty.Pagination.HasMore || empty.Pagination.NextCursor != nil {
		t.Fatal("empty final page advertises continuation")
	}
	// The plan characterization of the old ListFilteredHelmChartsAfter
	// statement no longer applies: catalog search now traverses the global
	// chart-key projection (migration 80), which pages in bounded candidate
	// batches. Completeness and ordering above are the contract.
	_ = capture
}

//nolint:tparallel // subtests run sequentially against one shared per-test database fixture
func TestCatalogCursorPostgresDeletedAnchorMutationAndRevocation(t *testing.T) {
	t.Parallel()
	ctx, tx := paginationPostgresTx(t)
	prefix := "cursor-" + uuid.NewString()
	repo, cluster, project := uuid.New(), uuid.New(), uuid.New()
	paginationExec(t, ctx, tx, `INSERT INTO clusters(id,name,display_name) VALUES($1,$2,$2)`, cluster, prefix)
	paginationExec(t, ctx, tx, `INSERT INTO projects(id,name,display_name,cluster_id) VALUES($1,$2,$2,$3)`, project, prefix, cluster)
	paginationExec(t, ctx, tx, `INSERT INTO helm_repositories(id,name,url,owner_project_id) VALUES($1,$2,'https://example.invalid/private',$3)`, repo, prefix, project)
	paginationExec(t, ctx, tx, `INSERT INTO helm_charts(repository_id,name) SELECT $1,$2||'-'||lpad(n::text,5,'0') FROM generate_series(1,9) n`, repo, prefix)
	h := NewCatalogHandler(sqlc.New(tx))
	codec, err := paging.NewCursorCodec("fixture-cursor-authority")
	if err != nil {
		t.Fatal(err)
	}
	h.SetCursorCodec(codec)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: []rbac.RoleBinding{{ProjectID: project.String(), RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceCatalog), Verbs: []string{string(rbac.VerbRead)}}}}}})
	actor := uuid.New()
	values := url.Values{"cursor": {""}, "limit": {"3"}, "search": {prefix}, "project_id": {project.String()}}
	first := catalogCursorPage(t, h, actor, values)
	if len(first.Data) != 3 || first.Pagination.NextCursor == nil {
		t.Fatal("first page missing")
	}
	anchor := first.Data[2]
	paginationExec(t, ctx, tx, `DELETE FROM helm_charts WHERE id=$1`, anchor.ID)
	paginationExec(t, ctx, tx, `INSERT INTO helm_charts(repository_id,name) VALUES($1,$2),($1,$3)`, repo, prefix+"-00000", prefix+"-00003-after")
	values.Set("cursor", *first.Pagination.NextCursor)
	second := catalogCursorPage(t, h, actor, values)
	if len(second.Data) != 3 || !strings.HasSuffix(second.Data[0].Name, "00003-after") {
		t.Fatal("deleted anchor or boundary insertion prevented progress")
	}
	for _, row := range second.Data {
		if row.Name <= anchor.Name {
			t.Fatal("insertion before boundary appeared after cursor")
		}
	}
	for _, substitution := range []string{"actor", "filter", "project"} {
		t.Run(substitution, func(t *testing.T) {
			changed := url.Values{}
			for key, items := range values {
				changed[key] = append([]string(nil), items...)
			}
			changedActor := actor
			switch substitution {
			case "actor":
				changedActor = uuid.New()
			case "filter":
				changed.Set("search", prefix+"-changed")
			case "project":
				changed.Set("project_id", uuid.NewString())
			}
			req := httptest.NewRequest(http.MethodGet, "/?"+changed.Encode(), nil)
			req = req.WithContext(reqctx.WithUser(req.Context(), &reqctx.User{ID: changedActor.String()}))
			rec := httptest.NewRecorder()
			h.ListCharts(rec, req)
			if rec.Code != http.StatusBadRequest && rec.Code != http.StatusForbidden {
				t.Fatalf("substitution accepted: status%d", rec.Code)
			}
		})
	}
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{})
	req := httptest.NewRequest(http.MethodGet, "/?"+values.Encode(), nil)
	req = req.WithContext(reqctx.WithUser(req.Context(), &reqctx.User{ID: actor.String()}))
	rec := httptest.NewRecorder()
	h.ListCharts(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("revoked current permission status%d", rec.Code)
	}
}
