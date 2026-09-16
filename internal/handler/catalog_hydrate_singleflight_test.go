package handler

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

type hydrationTestQuerier struct {
	CatalogQuerier
	chart   sqlc.HelmChart
	repo    sqlc.HelmRepository
	updates atomic.Int32
}

func (q *hydrationTestQuerier) GetHelmChartByID(context.Context, uuid.UUID) (sqlc.HelmChart, error) {
	return q.chart, nil
}

func (q *hydrationTestQuerier) GetHelmRepositoryByID(context.Context, uuid.UUID) (sqlc.HelmRepository, error) {
	return q.repo, nil
}

func (q *hydrationTestQuerier) UpdateHelmChartVersionContent(context.Context, sqlc.UpdateHelmChartVersionContentParams) error {
	q.updates.Add(1)
	return nil
}

func TestHydrateChartVersionCollapsesConcurrentMisses(t *testing.T) {
	defer httpclient.DisableGuardForTest()()
	archive := hydrationChartArchive(t)
	var downloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		downloads.Add(1)
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write(archive)
	}))
	t.Cleanup(server.Close)

	repoID, chartID, versionID := uuid.New(), uuid.New(), uuid.New()
	q := &hydrationTestQuerier{
		chart: sqlc.HelmChart{ID: chartID, RepositoryID: repoID, Name: "operator"},
		repo:  sqlc.HelmRepository{ID: repoID, Url: server.URL, RepoType: "helm"},
	}
	h := NewCatalogHandler(q)
	version := sqlc.HelmChartVersion{ID: versionID, ChartID: chartID, Version: "1.0.0", Urls: []byte(`["` + server.URL + `/operator.tgz"]`)}

	const callers = 24
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := h.hydrateChartVersion(context.Background(), version)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("hydrateChartVersion: %v", err)
		}
	}
	if got := downloads.Load(); got != 1 {
		t.Fatalf("archive downloads = %d, want 1", got)
	}
	if got := q.updates.Load(); got != 1 {
		t.Fatalf("cache writes = %d, want 1", got)
	}
}

func TestHydrateChartVersionEnforcesEndToEndDeadline(t *testing.T) {
	defer httpclient.DisableGuardForTest()()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	repoID, chartID := uuid.New(), uuid.New()
	q := &hydrationTestQuerier{
		chart: sqlc.HelmChart{ID: chartID, RepositoryID: repoID, Name: "slow"},
		repo:  sqlc.HelmRepository{ID: repoID, Url: server.URL, RepoType: "helm"},
	}
	h := NewCatalogHandler(q)
	h.chartHydrationTimeout = 25 * time.Millisecond
	version := sqlc.HelmChartVersion{ID: uuid.New(), ChartID: chartID, Version: "1", Urls: []byte(`["` + server.URL + `/slow.tgz"]`)}
	started := time.Now()
	_, err := h.hydrateChartVersion(context.Background(), version)
	if err == nil {
		t.Fatal("expected hydration deadline error")
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("hydration exceeded strict deadline: %s", elapsed)
	}
}

func hydrationChartArchive(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for name, body := range map[string]string{
		"operator/Chart.yaml":  "apiVersion: v2\nname: operator\nversion: 1.0.0\n",
		"operator/values.yaml": "replicas: 2\n",
		"operator/README.md":   "# Operator\n",
	} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
