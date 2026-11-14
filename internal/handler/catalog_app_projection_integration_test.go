//go:build integration

package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestClusterAppsProjectsDeliveryBeforePagination(t *testing.T) {
	t.Parallel()
	ctx, tx := paginationPostgresTx(t)
	cluster, project, source, bundle, version := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	paginationExec(t, ctx, tx, `INSERT INTO clusters(id,name,display_name) VALUES($1,$2,$2)`, cluster, cluster.String())
	paginationExec(t, ctx, tx, `INSERT INTO projects(id,name,display_name,cluster_id) VALUES($1,$2,$2,$3)`, project, project.String(), cluster)
	paginationExec(t, ctx, tx, `INSERT INTO delivery_sources(id,project_id,name,source_type,url) VALUES($1,$2,'source','helm_http','https://example.invalid')`, source, project)
	paginationExec(t, ctx, tx, `INSERT INTO component_bundles(id,project_id,name) VALUES($1,$2,'bundle')`, bundle, project)
	paginationExec(t, ctx, tx, `INSERT INTO component_bundle_versions(id,bundle_id,source_id,version,renderer,requested_revision,spec_digest) VALUES($1,$2,$3,'1','helm','1','sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')`, version, bundle, source)
	for _, tc := range []struct {
		name, phase, deletion, stored, want string
		observed                            int
	}{
		{"ready", "ready", "active", "installing", "installed", 1},
		{"deleting", "ready", "deleting", "installed", "uninstalling", 1},
		{"failed", "failed", "active", "installing", "failed_install", 1},
		{"stale", "ready", "active", "upgrading", "upgrading", 0},
		{"unknown", "removed", "active", "installed", "unknown", 1},
		{"removed", "removed", "deleted", "uninstalling", "", 1},
	} {
		target := uuid.New()
		paginationExec(t, ctx, tx, `INSERT INTO delivery_targets(id,project_id,name,bundle_version_id,deletion_state) VALUES($1,$2,$3,$4,$5)`, target, project, tc.name, version, tc.deletion)
		paginationExec(t, ctx, tx, `INSERT INTO cluster_deployments(target_id,cluster_id,phase,desired_generation,observed_generation,desired_spec_digest,observed_spec_digest) VALUES($1,$2,$3,1,$4,'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')`, target, cluster, tc.phase, tc.observed)
		paginationExec(t, ctx, tx, `INSERT INTO installed_charts(cluster_id,project_id,release_name,namespace,status,request_id) VALUES($1,$2,$3,'apps',$4,$5)`, cluster, project, tc.name, tc.stored, target)
	}
	h := NewCatalogHandler(sqlc.New(tx))
	router := chi.NewRouter()
	router.Get("/clusters/{cluster_id}/apps/", h.ListClusterApps)
	seen := map[string]string{}
	for _, offset := range []string{"0", "2", "4", "6"} {
		request := httptest.NewRequest(http.MethodGet, "/clusters/"+cluster.String()+"/apps/?limit=2&offset="+offset, nil).WithContext(ctx)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%d %s", recorder.Code, recorder.Body.String())
		}
		var response struct {
			Data []struct {
				ReleaseName string `json:"release_name"`
				Status      string `json:"status"`
			} `json:"data"`
			Pagination struct {
				Total int `json:"total"`
			} `json:"pagination"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Pagination.Total != 5 {
			t.Fatalf("total=%d", response.Pagination.Total)
		}
		for _, row := range response.Data {
			if _, exists := seen[row.ReleaseName]; exists {
				t.Fatal("duplicate page row")
			}
			seen[row.ReleaseName] = row.Status
		}
	}
	want := map[string]string{"ready": "installed", "deleting": "uninstalling", "failed": "failed_install", "stale": "upgrading", "unknown": "unknown"}
	for name, status := range want {
		if seen[name] != status {
			t.Fatalf("%s=%s, want %s", name, seen[name], status)
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("rows=%v", seen)
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM installed_charts WHERE cluster_id=$1`, cluster).Scan(&count); err != nil || count != 6 {
		t.Fatalf("GET mutated compatibility rows: count=%d err=%v", count, err)
	}
}
