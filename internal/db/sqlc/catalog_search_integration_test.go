package sqlc

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCatalogSearchFiltersScopeBeforePaginationAndCounts(t *testing.T) {
	dsn := os.Getenv("AUDIT_OUTBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("AUDIT_OUTBOX_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	cluster, project := uuid.New(), uuid.New()
	global, otherGlobal, private := uuid.New(), uuid.New(), uuid.New()
	prefix := "catalog-search-" + uuid.NewString()
	exec(`INSERT INTO clusters(id,name,display_name) VALUES($1,$2,$2)`, cluster, prefix)
	exec(`INSERT INTO projects(id,name,display_name,cluster_id) VALUES($1,$2,$2,$3)`, project, prefix, cluster)
	for _, repo := range []uuid.UUID{global, otherGlobal} {
		exec(`INSERT INTO helm_repositories(id,name,url) VALUES($1,$2,'https://example.invalid/charts')`, repo, repo.String())
	}
	exec(`INSERT INTO helm_repositories(id,name,url,owner_project_id) VALUES($1,$2,'https://example.invalid/private',$3)`, private, private.String(), project)
	for i := 0; i < 60; i++ {
		exec(`INSERT INTO helm_charts(repository_id,name) VALUES($1,$2)`, global, fmt.Sprintf("%s-a-unrelated-%02d", prefix, i))
	}
	name := prefix + `-z-literal50%_\chart`
	targets := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for i, repo := range []uuid.UUID{global, otherGlobal, private} {
		exec(`INSERT INTO helm_charts(id,repository_id,name,display_name,description) VALUES($1,$2,$3,$4,$5)`, targets[i], repo, name, prefix+"-FancyDisplay", prefix+"-NeedleFinal")
	}
	exec(`INSERT INTO helm_charts(repository_id,name) VALUES($1,$2)`, global, prefix+"-z-literal50XXZchart")
	for _, id := range []uuid.UUID{targets[0], targets[2]} {
		exec(`INSERT INTO helm_chart_tags(chart_id,tag) VALUES($1,'mesh')`, id)
	}
	secondaryCluster, secondaryProject, unrelatedProject := uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO clusters(id,name,display_name) VALUES($1,$2,$2)`, secondaryCluster, secondaryCluster.String())
	for _, id := range []uuid.UUID{secondaryProject, unrelatedProject} {
		exec(`INSERT INTO projects(id,name,display_name,cluster_id) VALUES($1,$2,$2,$3)`, id, id.String(), secondaryCluster)
	}
	for _, namespace := range []string{"team-a", "team-b"} {
		exec(`INSERT INTO project_namespaces(project_id,cluster_id,namespace) VALUES($1,$2,$3)`, secondaryProject, cluster, namespace)
	}
	q := New(tx)
	var projectRows []Project
	for offset := int32(0); ; offset++ {
		page, err := q.ListCatalogProjectsByCluster(ctx, ListCatalogProjectsByClusterParams{ClusterID: cluster, QueryLimit: 1, QueryOffset: offset})
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		if len(page) != 1 || offset > 10 {
			t.Fatalf("invalid project pagination: %+v", page)
		}
		projectRows = append(projectRows, page...)
	}
	foundProjects := map[uuid.UUID]bool{}
	for _, row := range projectRows {
		if foundProjects[row.ID] {
			t.Fatal("secondary namespaces duplicated project")
		}
		foundProjects[row.ID] = true
	}
	if !foundProjects[project] || !foundProjects[secondaryProject] || foundProjects[unrelatedProject] {
		t.Fatalf("wrong effective cluster projects %+v", projectRows)
	}
	// A secondary cluster association changes membership, not project read authority.
	for _, tc := range []struct {
		name               string
		projects, clusters []uuid.UUID
		want               int64
	}{
		{"direct project", []uuid.UUID{secondaryProject}, nil, 1},
		{"unrelated project", []uuid.UUID{unrelatedProject}, nil, 0},
		{"namespace only has no broad grants", nil, nil, 0},
	} {
		args := ListClusterProjectsForScopesParams{SelectedClusterID: cluster, ProjectIds: tc.projects, ClusterIds: tc.clusters, QueryLimit: 1}
		rows, err := q.ListClusterProjectsForScopes(ctx, args)
		if err != nil {
			t.Fatal(err)
		}
		total, err := q.CountClusterProjectsForScopes(ctx, CountClusterProjectsForScopesParams{SelectedClusterID: cluster, ProjectIds: tc.projects, ClusterIds: tc.clusters})
		if err != nil || total != tc.want || int64(len(rows)) != tc.want {
			t.Fatalf("%s: rows=%+v total=%d err=%v", tc.name, rows, total, err)
		}
		if len(rows) > 0 && rows[0].ID != secondaryProject {
			t.Fatal("wrong authorized secondary project")
		}
	}
	clusterVisible, err := q.ListClusterProjectsForScopes(ctx, ListClusterProjectsForScopesParams{SelectedClusterID: cluster, ClusterIds: []uuid.UUID{cluster}, QueryLimit: 100})
	if err != nil {
		t.Fatal(err)
	}
	clusterCount, err := q.CountClusterProjectsForScopes(ctx, CountClusterProjectsForScopesParams{SelectedClusterID: cluster, ClusterIds: []uuid.UUID{cluster}})
	if err != nil || clusterCount != int64(len(clusterVisible)) {
		t.Fatalf("cluster grant count mismatch: %d %v", clusterCount, err)
	}
	for _, row := range clusterVisible {
		if row.ID == secondaryProject || row.ClusterID != cluster {
			t.Fatal("secondary association widened project authority")
		}
	}
	assignments, err := q.ListProjectNamespaceScopes(ctx, []uuid.UUID{secondaryProject})
	if err != nil || len(assignments) != 2 {
		t.Fatalf("batch namespace scope read: %+v %v", assignments, err)
	}
	check := func(arg ListFilteredHelmChartsParams, wantTotal int64, wantRows int) []HelmChart {
		t.Helper()
		rows, err := q.ListFilteredHelmCharts(ctx, arg)
		if err != nil {
			t.Fatal(err)
		}
		total, err := q.CountFilteredHelmCharts(ctx, CountFilteredHelmChartsParams{GlobalScope: arg.GlobalScope, RepositoryIds: arg.RepositoryIds, Tag: arg.Tag, SearchPattern: arg.SearchPattern})
		if err != nil {
			t.Fatal(err)
		}
		if total != wantTotal || len(rows) != wantRows {
			t.Fatalf("predicate%+v total%d rows%d want%d/%d", arg, total, len(rows), wantTotal, wantRows)
		}
		return rows
	}
	// This chart lies beyond sixty unrelated records; search must precede LIMIT.
	rows := check(ListFilteredHelmChartsParams{RepositoryIds: []uuid.UUID{global}, SearchPattern: "%" + prefix + "-needlefinal%", QueryLimit: 1}, 1, 1)
	if rows[0].ID != targets[0] {
		t.Fatalf("wrong later-page search match %+v", rows)
	}
	// Literal wildcard characters must not match the deliberately similar name.
	pattern := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(`literal50%_\chart`) + "%"
	check(ListFilteredHelmChartsParams{RepositoryIds: []uuid.UUID{global}, SearchPattern: pattern, QueryLimit: 10}, 1, 1)
	// Global browse cannot leak the project-owned match; duplicate chart names
	// across repositories have stable distinct pages and matching total counts.
	arg := ListFilteredHelmChartsParams{GlobalScope: true, SearchPattern: "%" + prefix + "-FancyDisplay%", QueryLimit: 1}
	first := check(arg, 2, 1)
	arg.QueryOffset = 1
	second := check(arg, 2, 1)
	if first[0].ID == second[0].ID || first[0].RepositoryID.String() > second[0].RepositoryID.String() {
		t.Fatalf("unstable same-name pages: %v then%v", first[0].ID, second[0].ID)
	}
	arg.QueryOffset = 0
	arg.Tag = "mesh"
	check(arg, 1, 1)
	// No visible repositories is empty, never a fallback to global catalogs.
	check(ListFilteredHelmChartsParams{SearchPattern: "%" + prefix + "%", QueryLimit: 10}, 0, 0)
	privateRows := check(ListFilteredHelmChartsParams{RepositoryIds: []uuid.UUID{private}, SearchPattern: "%" + prefix + "%", QueryLimit: 10}, 1, 1)
	if privateRows[0].ID != targets[2] {
		t.Fatal("explicit authorized repository not honored")
	}
}
