package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

type fakeBlessedStore struct {
	repos   []sqlc.UpsertDefaultHelmRepositoryParams
	charts  []sqlc.CreateBlessedChartParams
	cleared int
}

func (f *fakeBlessedStore) UpsertDefaultHelmRepository(_ context.Context, p sqlc.UpsertDefaultHelmRepositoryParams) error {
	f.repos = append(f.repos, p)
	return nil
}
func (f *fakeBlessedStore) DeleteBlessedChartsBySource(_ context.Context, _ string) error {
	f.cleared++
	return nil
}
func (f *fakeBlessedStore) CreateBlessedChart(_ context.Context, p sqlc.CreateBlessedChartParams) error {
	f.charts = append(f.charts, p)
	return nil
}

const goodCatalog = `
apiVersion: catalog.astronomer.io/v1
kind: Catalog
metadata: { name: test }
entries:
  - { chart: cert-manager, repo: https://charts.jetstack.io, repoName: jetstack, category: security, mgmtSafe: false, versions: last:3 }
  - { chart: longhorn, repo: https://charts.longhorn.io, repoName: longhorn, category: storage }
`

func TestLoad_FetchParseReconcile(t *testing.T) {
	// The blessed-catalog fetch is now SSRF-guarded; the test server is on
	// loopback, so disable the guard for this test (production keeps it on).
	defer httpclient.DisableGuardForTest()()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(goodCatalog))
	}))
	defer srv.Close()

	store := &fakeBlessedStore{}
	n, err := Load(context.Background(), store, &http.Client{Timeout: 5 * time.Second}, srv.URL)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if n != 2 {
		t.Fatalf("entries = %d, want 2", n)
	}
	if len(store.repos) != 2 {
		t.Fatalf("repos seeded = %d, want 2 (distinct)", len(store.repos))
	}
	if store.cleared != 1 {
		t.Fatalf("blessed rows cleared %d times, want 1", store.cleared)
	}
	if len(store.charts) != 2 {
		t.Fatalf("blessed charts = %d, want 2", len(store.charts))
	}
	// cert-manager mgmtSafe:false must survive; longhorn defaults to true.
	for _, c := range store.charts {
		switch c.ChartName {
		case "cert-manager":
			if c.MgmtSafe {
				t.Error("cert-manager should be mgmtSafe=false")
			}
			if c.VersionPolicy != "last:3" {
				t.Errorf("cert-manager version policy = %q", c.VersionPolicy)
			}
		case "longhorn":
			if !c.MgmtSafe {
				t.Error("longhorn should default to mgmtSafe=true")
			}
		}
	}
}

func TestLoad_BlankURLIsNoop(t *testing.T) {
	store := &fakeBlessedStore{}
	n, err := Load(context.Background(), store, http.DefaultClient, "")
	if err != nil || n != 0 || store.cleared != 0 {
		t.Fatalf("blank url should be a no-op, got n=%d err=%v cleared=%d", n, err, store.cleared)
	}
}

func TestParseCatalog_Rejects(t *testing.T) {
	cases := map[string]string{
		"bad apiVersion": `{apiVersion: v2, kind: Catalog, entries: [{chart: x, repo: https://h, repoName: x, category: security}]}`,
		"non-url repo":   `{apiVersion: catalog.astronomer.io/v1, kind: Catalog, entries: [{chart: x, repo: nope, repoName: x, category: security}]}`,
		"bad category":   `{apiVersion: catalog.astronomer.io/v1, kind: Catalog, entries: [{chart: x, repo: https://h, repoName: x, category: bogus}]}`,
		"bad version":    `{apiVersion: catalog.astronomer.io/v1, kind: Catalog, entries: [{chart: x, repo: https://h, repoName: x, category: security, versions: latest}]}`,
		"dup chart":      `{apiVersion: catalog.astronomer.io/v1, kind: Catalog, entries: [{chart: x, repo: https://h, repoName: x, category: security}, {chart: x, repo: https://h, repoName: x, category: security}]}`,
		"empty":          `{apiVersion: catalog.astronomer.io/v1, kind: Catalog, entries: []}`,
	}
	for name, doc := range cases {
		if _, err := ParseCatalog([]byte(doc)); err == nil {
			t.Errorf("%s: expected rejection, got nil", name)
		}
	}
}
