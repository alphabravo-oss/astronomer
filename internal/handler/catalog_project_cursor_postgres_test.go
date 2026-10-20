package handler

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

type rejectProjectRepositoryMaterialization struct {
	*sqlc.Queries
	calls int
}

func (q *rejectProjectRepositoryMaterialization) ListCatalogsForProject(context.Context, uuid.UUID) ([]sqlc.HelmRepositoryWithOwner, error) {
	q.calls++
	return nil, errors.New("signed chart listing must scope repositories inside SQL")
}

func TestCatalogProjectCursorPostgresSQLVisibilityWithoutRepositoryMaterialization(t *testing.T) {
	ctx, tx := paginationPostgresTx(t)
	prefix := "project-cursor-" + uuid.NewString()
	cluster, project, foreign := uuid.New(), uuid.New(), uuid.New()
	paginationExec(t, ctx, tx, `INSERT INTO clusters(id,name,display_name)VALUES($1,$2,$2)`, cluster, prefix)
	for _, id := range []uuid.UUID{project, foreign} {
		paginationExec(t, ctx, tx, `INSERT INTO projects(id,name,display_name,cluster_id)VALUES($1,$2,$2,$3)`, id, id.String(), cluster)
	}
	global, owned, subscribed, private := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	paginationExec(t, ctx, tx, `INSERT INTO helm_repositories(id,name,url)VALUES($1,$2,'https://example.invalid/charts')`, global, global.String())
	for _, item := range []struct{ id, owner uuid.UUID }{{owned, project}, {subscribed, foreign}, {private, foreign}} {
		paginationExec(t, ctx, tx, `INSERT INTO helm_repositories(id,name,url,owner_project_id)VALUES($1,$2,'https://example.invalid/charts',$3)`, item.id, item.id.String(), item.owner)
	}
	paginationExec(t, ctx, tx, `INSERT INTO project_catalog_subscriptions(project_id,catalog_id)VALUES($1,$2)`, project, subscribed)
	paginationExec(t, ctx, tx, `INSERT INTO helm_charts(repository_id,name)SELECT r,$1||'-'||lpad(n::text,5,'0')FROM unnest($2::uuid[])r CROSS JOIN generate_series(1,3348)n`, prefix, []uuid.UUID{global, owned, subscribed})
	paginationExec(t, ctx, tx, `INSERT INTO helm_charts(repository_id,name)VALUES($1,$2)`, private, prefix+"-00000-private")
	q := &rejectProjectRepositoryMaterialization{Queries: sqlc.New(tx)}
	h := NewCatalogHandler(q)
	codec, err := paging.NewCursorCodec("fixture-cursor-authority")
	if err != nil {
		t.Fatal(err)
	}
	h.SetCursorCodec(codec)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: []rbac.RoleBinding{{ProjectID: project.String(), RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceCatalog), Verbs: []string{string(rbac.VerbRead)}}}}}})
	values := url.Values{"project_id": {project.String()}, "search": {prefix}, "limit": {"200"}, "cursor": {""}}
	actor := uuid.New()
	seen := map[uuid.UUID]bool{}
	repos := map[uuid.UUID]bool{}
	for pages := 0; ; pages++ {
		if pages > 60 {
			t.Fatal("project cursor did not terminate")
		}
		page := catalogCursorPage(t, h, actor, values)
		for _, row := range page.Data {
			if row.RepositoryID == private || !strings.HasPrefix(row.Name, prefix) {
				t.Fatal("project visibility/filter leaked")
			}
			if seen[row.ID] {
				t.Fatal("project cursor repeated chart")
			}
			seen[row.ID] = true
			repos[row.RepositoryID] = true
		}
		if !page.Pagination.HasMore {
			if page.Pagination.NextCursor != nil {
				t.Fatal("project final page has cursor")
			}
			break
		}
		if page.Pagination.NextCursor == nil {
			t.Fatal("project continuation missing")
		}
		values.Set("cursor", *page.Pagination.NextCursor)
	}
	if len(seen) != 10044 || len(repos) != 3 || q.calls != 0 {
		t.Fatalf("seen%d repos%d materialization calls%d", len(seen), len(repos), q.calls)
	}
}
