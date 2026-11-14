//go:build integration

package catalog

import (
	"io"
	"log"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	distribution "github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/uuid"
	chart "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/chart/v2/util"
	"helm.sh/helm/v4/pkg/registry"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/testdb"
)

// A real distribution registry serves uploaded Helm archives over TLS. The
// operation-scoped client trusts only this owned server; public-host guards are
// covered separately. PostgreSQL runs the production migrations.
func TestOCIIngestionPersistsVersionsAndConverges(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(distribution.New(distribution.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 20 * time.Second
	publisher, err := registry.NewClient(registry.ClientOptHTTPClient(client), registry.ClientOptWriter(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	host := strings.TrimPrefix(server.URL, "https://")
	for _, version := range []string{"1.0.0", "1.1.0"} {
		file, err := util.Save(&chart.Chart{Metadata: &chart.Metadata{APIVersion: "v2", Name: "owned-chart", Version: version, Description: "owned OCI integration", AppVersion: "2.0"}}, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		archive, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := publisher.Push(archive, host+"/charts/owned-chart:"+version); err != nil {
			t.Fatal(err)
		}
	}
	pool := testdb.Pool(t)
	q := sqlc.New(pool)
	ctx := WithNetworkClient(t.Context(), client)
	repo := sqlc.HelmRepository{ID: uuid.New(), Url: "oci://" + host + "/charts", RepoType: "oci", AuthConfig: []byte(`{"charts":["owned-chart"]}`)}
	_, err = pool.Exec(ctx, `INSERT INTO helm_repositories(id,name,url,repo_type,auth_config) VALUES($1,$2,$3,'oci',$4)`, repo.ID, repo.ID.String(), repo.Url, repo.AuthConfig)
	if err != nil {
		t.Fatal(err)
	}
	for i, wanted := range []int{2, 0} {
		charts, versions, err := IngestOCIRepo(ctx, q, repo, nil, nil)
		if err != nil || charts != 1 || versions != wanted {
			t.Fatalf("ingest %d: charts=%d versions=%d err=%v", i, charts, versions, err)
		}
	}
	row, err := q.GetHelmChartByRepoAndName(ctx, sqlc.GetHelmChartByRepoAndNameParams{RepositoryID: repo.ID, Name: "owned-chart"})
	if err != nil || row.Description != "owned OCI integration" {
		t.Fatalf("persisted metadata: %v %v", row, err)
	}
	for _, version := range []string{"1.0.0", "1.1.0"} {
		persisted, err := q.GetHelmChartVersion(ctx, sqlc.GetHelmChartVersionParams{ChartID: row.ID, Version: version})
		if err != nil || persisted.AppVersion != "2.0" {
			t.Fatalf("persisted version %s: %v", version, err)
		}
	}
}
