package handler

import (
	"net/url"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/google/uuid"
)

func TestCatalogGlobalCandidatePostgresEmptyProgressTraversal(t *testing.T) {
	for _, filter := range []string{"absent-tag", "late-tag"} {
		t.Run(filter, func(t *testing.T) {
			ctx, tx := paginationPostgresTx(t)
			prefix := "w07-window-" + uuid.NewString()
			cluster, project, publicRepo, privateRepo := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			paginationExec(t, ctx, tx, `INSERT INTO clusters(id,name,display_name) VALUES($1,$2,$2)`, cluster, prefix)
			paginationExec(t, ctx, tx, `INSERT INTO projects(id,name,display_name,cluster_id) VALUES($1,$2,$2,$3)`, project, prefix, cluster)
			paginationExec(t, ctx, tx, `INSERT INTO helm_repositories(id,name,url) VALUES($1,$2,'https://example.invalid/owned')`, publicRepo, prefix+"-public")
			paginationExec(t, ctx, tx, `INSERT INTO helm_repositories(id,name,url,owner_project_id) VALUES($1,$2,'https://example.invalid/owned',$3)`, privateRepo, prefix+"-private", project)
			paginationExec(t, ctx, tx, `INSERT INTO helm_charts(repository_id,name) SELECT $1,$2||'-'||lpad(n::text,5,'0') FROM generate_series(1,201)n`, publicRepo, prefix)
			privateName := "00000-private-" + prefix
			paginationExec(t, ctx, tx, `INSERT INTO helm_charts(repository_id,name) VALUES($1,$2)`, privateRepo, privateName)
			var last uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT id FROM helm_charts WHERE repository_id=$1 AND name=$2`, publicRepo, prefix+"-00201").Scan(&last); err != nil {
				t.Fatal(err)
			}
			tag := filter + "-" + prefix
			if filter == "late-tag" {
				paginationExec(t, ctx, tx, `INSERT INTO helm_chart_tags(chart_id,tag) VALUES($1,$2)`, last, tag)
			}
			h := NewCatalogHandler(sqlc.New(tx))
			codec, err := paging.NewCursorCodec("fixture-cursor-authority")
			if err != nil {
				t.Fatal(err)
			}
			h.SetCursorCodec(codec)
			h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: []rbac.RoleBinding{{RoleRules: []rbac.Rule{{Resource: "catalog", Verbs: []string{"read"}}}}}})
			actor := uuid.New()
			values := url.Values{"cursor": {""}, "limit": {"200"}, "tag": {tag}}
			tokens := map[string]bool{}
			matches := 0
			windows := 0
			for {
				windows++
				if windows > 13 {
					t.Fatal("bounded 201-key traversal did not terminate")
				}
				page := catalogPreparedCursorPage(t, ctx, h, actor, values)
				for _, chart := range page.Data {
					if chart.ID != last || chart.RepositoryID != publicRepo {
						t.Fatal("late-filter/private chart leaked")
					}
					matches++
				}
				if !page.Pagination.HasMore {
					if page.Pagination.NextCursor != nil {
						t.Fatal("terminal window carried cursor")
					}
					break
				}
				if page.Pagination.NextCursor == nil {
					t.Fatal("empty progress lost continuation")
				}
				token := *page.Pagination.NextCursor
				if tokens[token] {
					t.Fatal("zero-match cursor did not advance")
				}
				tokens[token] = true
				binding := paging.Binding("catalog-charts:v2", "name:asc,repository_id:asc,id:asc", actor.String(), "true", "", tag, "", "", "")
				position, err := codec.Decode(token, binding)
				if err != nil || position.Group == nil || *position.Group != publicRepo || position.Text == nil || !strings.HasPrefix(*position.Text, prefix) || *position.Text == privateName {
					t.Fatal("cursor disclosed an unauthorized candidate key")
				}
				values.Set("cursor", token)
			}
			expected := 0
			if filter == "late-tag" {
				expected = 1
			}
			if windows != 13 || matches != expected {
				t.Fatalf("actual windows=%d matches=%d expected=%d", windows, matches, expected)
			}
		})
	}
}
