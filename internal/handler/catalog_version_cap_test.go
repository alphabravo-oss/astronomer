package handler

// The interactive Sync button and the unattended catalog:sync sweep must agree
// on which chart versions a repository yields.
//
// They did not. The worker capped each chart at the newest
// catalog.MaxIndexVersionsPerChart and GC'd anything outside that set; the
// handler ingested every version in the index. So an operator clicked Sync,
// saw 40 versions, and six hours later had 3 — with no error, no audit entry,
// and nothing in the UI to explain the disappearance. The caps are not an
// optimisation detail: the worker's GC makes the smaller one authoritative.
//
// The two paths cannot share one ingest function today (different queriers,
// different insert strategies — the handler bulk-inserts, the worker fetches a
// .tgz per new version), so this test pins the observable contract instead.

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

// capIndexYAML deliberately lists versions out of order so an implementation
// that just truncates the raw index entry keeps the wrong three.
const capIndexYAML = `apiVersion: v1
entries:
  app:
  - name: app
    version: 0.9.0
  - name: app
    version: 2.1.0
  - name: app
    version: 1.4.2
  - name: app
    version: 2.0.0
  - name: app
    version: 1.0.0
`

// capHandlerQuerier records the versions fetchAndIngestRepoIndex would insert.
type capHandlerQuerier struct {
	CatalogQuerier
	versions []string
}

func (q *capHandlerQuerier) GetHelmChartByRepoAndName(context.Context, sqlc.GetHelmChartByRepoAndNameParams) (sqlc.HelmChart, error) {
	return sqlc.HelmChart{}, pgx.ErrNoRows
}

func (q *capHandlerQuerier) CreateHelmChart(_ context.Context, arg sqlc.CreateHelmChartParams) (sqlc.HelmChart, error) {
	return sqlc.HelmChart{ID: uuid.New(), RepositoryID: arg.RepositoryID, Name: arg.Name}, nil
}

func (q *capHandlerQuerier) ListChartVersionStrings(context.Context, uuid.UUID) ([]string, error) {
	return nil, nil
}

func (q *capHandlerQuerier) BulkCreateHelmChartVersions(_ context.Context, arg sqlc.BulkCreateHelmChartVersionsParams) ([]string, error) {
	var rows []struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(arg.Rows, &rows); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		q.versions = append(q.versions, r.Version)
		out = append(out, r.Version)
	}
	return out, nil
}

// capWorkerQuerier records the versions the sweep would insert. It embeds the
// (much larger) RuntimeQuerier so any unexpected call nil-derefs loudly.
type capWorkerQuerier struct {
	tasks.RuntimeQuerier
	repos    []sqlc.HelmRepository
	versions []string
}

func (q *capWorkerQuerier) ListEnabledHelmRepositories(context.Context) ([]sqlc.HelmRepository, error) {
	return q.repos, nil
}
func (q *capWorkerQuerier) UpdateHelmRepositoryLastSynced(context.Context, uuid.UUID) error {
	return nil
}
func (q *capWorkerQuerier) GetHelmChartByRepoAndName(context.Context, sqlc.GetHelmChartByRepoAndNameParams) (sqlc.HelmChart, error) {
	return sqlc.HelmChart{}, pgx.ErrNoRows
}
func (q *capWorkerQuerier) CreateHelmChart(_ context.Context, arg sqlc.CreateHelmChartParams) (sqlc.HelmChart, error) {
	return sqlc.HelmChart{ID: uuid.New(), RepositoryID: arg.RepositoryID, Name: arg.Name}, nil
}
func (q *capWorkerQuerier) GetHelmChartVersion(context.Context, sqlc.GetHelmChartVersionParams) (sqlc.HelmChartVersion, error) {
	return sqlc.HelmChartVersion{}, pgx.ErrNoRows
}
func (q *capWorkerQuerier) CreateHelmChartVersion(_ context.Context, arg sqlc.CreateHelmChartVersionParams) (sqlc.HelmChartVersion, error) {
	q.versions = append(q.versions, arg.Version)
	return sqlc.HelmChartVersion{ID: uuid.New(), ChartID: arg.ChartID, Version: arg.Version}, nil
}
func (q *capWorkerQuerier) ListChartVersions(context.Context, sqlc.ListChartVersionsParams) ([]sqlc.HelmChartVersion, error) {
	return nil, nil
}
func (q *capWorkerQuerier) ListChartsByRepository(context.Context, sqlc.ListChartsByRepositoryParams) ([]sqlc.HelmChart, error) {
	return nil, nil
}

func TestHandlerAndWorkerIngestTheSameVersionSet(t *testing.T) {
	defer httpclient.DisableGuardForTest()()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write([]byte(capIndexYAML))
	}))
	t.Cleanup(srv.Close)

	repoRecord := sqlc.HelmRepository{ID: uuid.New(), Name: "shared", Url: srv.URL, RepoType: "helm"}

	// Interactive path (the Sync button).
	hq := &capHandlerQuerier{}
	h := NewCatalogHandler(hq)
	if _, _, err := h.fetchAndIngestRepoIndex(context.Background(), repoRecord); err != nil {
		t.Fatalf("handler ingest: %v", err)
	}

	// Scheduled path (catalog:sync @every 6h).
	wq := &capWorkerQuerier{repos: []sqlc.HelmRepository{repoRecord}}
	tasks.ConfigureRuntime(tasks.RuntimeDependencies{Queries: wq, Log: slog.Default()})
	t.Cleanup(func() { tasks.ConfigureRuntime(tasks.RuntimeDependencies{}) })
	if err := tasks.HandleCatalogSync(context.Background(), &asynq.Task{}); err != nil {
		t.Fatalf("worker sweep: %v", err)
	}

	sort.Strings(hq.versions)
	sort.Strings(wq.versions)
	if len(hq.versions) != len(wq.versions) {
		t.Fatalf("version-set size differs: handler=%v worker=%v — whichever ingests more has its extras GC'd by the next sweep", hq.versions, wq.versions)
	}
	for i := range hq.versions {
		if hq.versions[i] != wq.versions[i] {
			t.Fatalf("version sets differ: handler=%v worker=%v", hq.versions, wq.versions)
		}
	}
	// And both must be the NEWEST N, not the first N in file order.
	want := []string{"1.4.2", "2.0.0", "2.1.0"}
	if len(hq.versions) != catalog.MaxIndexVersionsPerChart {
		t.Fatalf("expected the cap (%d) to apply, got %v", catalog.MaxIndexVersionsPerChart, hq.versions)
	}
	for i := range want {
		if hq.versions[i] != want[i] {
			t.Fatalf("expected the newest %d versions %v, got %v", catalog.MaxIndexVersionsPerChart, want, hq.versions)
		}
	}
}
