package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type configurationQueries struct {
	*minimalCatalogQuerier
	entries    []sqlc.ListApplicationCatalogPresentationsRow
	versions   map[string]sqlc.HelmChartVersion
	repository sqlc.HelmRepository
}

func (q *configurationQueries) ListApplicationCatalogPresentations(context.Context) ([]sqlc.ListApplicationCatalogPresentationsRow, error) {
	return q.entries, nil
}
func (q *configurationQueries) GetApplicationCatalogPresentationByChartVersion(context.Context, uuid.UUID) (sqlc.CatalogBlessedChart, error) {
	return sqlc.CatalogBlessedChart{}, pgx.ErrNoRows
}
func (q *configurationQueries) GetHelmRepositoryByID(context.Context, uuid.UUID) (sqlc.HelmRepository, error) {
	return q.repository, nil
}
func (q *configurationQueries) GetHelmChartVersion(_ context.Context, p sqlc.GetHelmChartVersionParams) (sqlc.HelmChartVersion, error) {
	v, ok := q.versions[p.Version]
	if !ok {
		return v, pgx.ErrNoRows
	}
	return v, nil
}
func TestCuratedVersionsResolvePinsBeforePagination(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	q := &configurationQueries{repository: sqlc.HelmRepository{Url: "https://example.com"}, versions: map[string]sqlc.HelmChartVersion{"1.0.0": {ID: uuid.New(), Version: "1.0.0", Digest: digest}}}
	artifact := `{"repository":"example","chart":"example","version":"1.0.0","versions":[{"version":"1.0.0","available":true,"recommended":true,"configuration":{"schema":{"type":"object"},"presets":[],"defaultPreset":"default","namespace":"apps"},"artifact":{"repository":"example","name":"example","version":"1.0.0","digest":"` + digest + `"}}]}`
	q.entries = []sqlc.ListApplicationCatalogPresentationsRow{{ChartName: "example", RepoUrl: q.repository.Url, VerificationStatus: "verified", Artifact: []byte(artifact)}}
	h := &CatalogHandler{queries: q}
	for _, offset := range []string{"0", "1"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/?view=curated&limit=1&offset="+offset, nil)
		if !h.listCuratedChartVersions(rec, req, sqlc.HelmChart{Name: "example"}) || rec.Code != 200 {
			t.Fatalf("unexpected response %d %s", rec.Code, rec.Body.String())
		}
		var response struct {
			Data       []configuredChartVersion `json:"data"`
			Pagination struct {
				Total int `json:"total"`
			} `json:"pagination"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Pagination.Total != 1 {
			t.Fatal("incorrect curated total")
		}
		if offset == "0" && (len(response.Data) != 1 || response.Data[0].Version != "1.0.0") {
			t.Fatal("pin lost outside upstream page")
		}
		if offset == "0" && (response.Data[0].CatalogVersion.Configuration == nil || len(response.Data[0].CatalogVersion.Configuration.Schema) == 0) {
			t.Fatal("version-owned form missing from the curated version record")
		}
		if offset == "1" && len(response.Data) != 0 {
			t.Fatal("offset ignored")
		}
	}
	q.entries[0].Revoked = true
	rec := httptest.NewRecorder()
	h.listCuratedChartVersions(rec, httptest.NewRequest("GET", "/?view=curated", nil), sqlc.HelmChart{Name: "example"})
	if rec.Code != 503 {
		t.Fatal("revoked catalog was used")
	}
}
func TestCatalogConfigurationEnforcedAtMutationPreflight(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	raw := `{"repository":"example","chart":"example","version":"1","versions":[{"version":"1","available":true,"artifact":{"repository":"example","name":"example","version":"1","digest":"` + digest + `"},"configuration":{"schema":{"type":"object","properties":{"replicas":{"type":"integer","minimum":1}}}}}]}`
	entry := sqlc.CatalogBlessedChart{Artifact: []byte(raw), VerificationStatus: "verified"}
	version := sqlc.HelmChartVersion{Version: "1", Digest: digest, DefaultValues: "replicas: 1"}
	cluster := sqlc.Cluster{Status: "ready"}
	for _, tc := range []struct {
		values  string
		allowed bool
	}{{"replicas: 2", true}, {"replicas: 0", false}, {"replicas: invalid", false}} {
		checks, allowed := catalogInstallChecks(cluster, version, entry, tc.values)
		if allowed != tc.allowed {
			t.Fatalf("%s: allowed %v, checks %+v", tc.values, allowed, checks)
		}
	}
	version.Digest = "sha256:" + strings.Repeat("b", 64)
	if _, allowed := catalogInstallChecks(cluster, version, entry, "replicas: 2"); allowed {
		t.Fatal("mutable archive passed mutation preflight")
	}
}
