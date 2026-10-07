package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func estateTestSearch() *estateSearchSpec {
	return &estateSearchSpec{Namespace: "benchmark", Types: []string{"pods"}, Limit: 100, Fanout: []string{estateTestManifest().Members[0].ClusterID, estateTestManifest().Members[1].ClusterID}}
}
func TestRealEstateSearchFanoutProof(t *testing.T) {
	for _, kind := range []string{"valid", "restricted", "not_self", "extra", "missing", "duplicate", "pagination", "inactive", "decommissioned"} {
		t.Run(kind, func(t *testing.T) {
			scope := estateTestSearch()
			for i := 3; i <= 23; i++ {
				scope.Fanout = append(scope.Fanout, fmt.Sprintf("00000000-0000-4000-8000-%012d", i))
			}
			// Explicitly declared local cluster is permitted; no implicit local exclusion.
			calls := 0
			client := estateHTTPClient()
			client.Transport = estateRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Authorization") != "Bearer search-only" {
					t.Fatal("search credential not used")
				}
				if r.URL.Path == "/api/v1/rbac/my-permissions" {
					return estateResponse(map[string]any{"data": map[string]any{"subject": map[string]any{"self": kind != "not_self"}, "superuser": kind != "restricted"}}), nil
				}
				calls++
				if r.URL.Query().Get("status") != "active" {
					t.Fatal("census not active filtered")
				}
				offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
				items := []map[string]any{}
				for i := offset; i < min(offset+20, len(scope.Fanout)); i++ {
					id := scope.Fanout[i]
					if kind == "extra" && i == 0 {
						id = "00000000-0000-4000-8000-000000000099"
					}
					if kind == "duplicate" && i == 1 {
						id = scope.Fanout[0]
					}
					status := "active"
					if kind == "inactive" {
						status = "pending"
					}
					row := map[string]any{"id": id, "status": status, "is_local": i == 0}
					if kind == "decommissioned" {
						row["decommissioned_at"] = "2026-01-01T00:00:00Z"
					}
					items = append(items, row)
				}
				total := len(scope.Fanout)
				if kind == "missing" {
					total--
				}
				more := offset+len(items) < total
				if kind == "pagination" {
					more = false
				}
				return estateResponse(map[string]any{"data": items, "pagination": map[string]any{"total": total, "has_more": more}}), nil
			})
			err := verifyEstateSearch(context.Background(), client, "https://api.test", "search-only", scope)
			if (err == nil) != (kind == "valid") {
				t.Fatalf("scope proof %s: %v", kind, err)
			}
			if kind == "valid" && calls != 2 {
				t.Fatal("fanout did not traverse all pages")
			}
			if (kind == "restricted" || kind == "not_self") && calls != 0 {
				t.Fatal("unproved authority initiated census")
			}
		})
	}
}
func TestRealEstateSearchBodyRejectsPartialAndForeignResults(t *testing.T) {
	scope := estateTestSearch()
	for _, kind := range []string{"complete", "partial", "truncated", "foreign", "namespace", "missing_count", "missing_results"} {
		t.Run(kind, func(t *testing.T) {
			item := map[string]any{"cluster_id": scope.Fanout[0], "namespace": scope.Namespace}
			if kind == "foreign" {
				item["cluster_id"] = "foreign"
			}
			if kind == "namespace" {
				item["namespace"] = "foreign"
			}
			data := map[string]any{"type": "pods", "clusters_queried": 2, "clusters_failed": 0, "truncated": false, "errors": []any{}, "results": []any{item}}
			if kind == "partial" {
				data["clusters_failed"] = 1
				data["errors"] = []any{map[string]any{"cluster_id": scope.Fanout[1]}}
			}
			if kind == "truncated" {
				data["truncated"] = true
			}
			if kind == "missing_count" {
				delete(data, "clusters_queried")
			}
			if kind == "missing_results" {
				delete(data, "results")
			}
			resp := estateResponse(map[string]any{"data": data})
			raw := readTestBody(t, resp)
			err := validateEstateSearchBody(raw, scope, "pods")
			if (err == nil) != (kind == "complete") {
				t.Fatalf("body %s: %v", kind, err)
			}
		})
	}
}

func readTestBody(t *testing.T, r *http.Response) []byte {
	t.Helper()
	defer func() { _ = r.Body.Close() }()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestRealEstateSearchMonitorRejectsChangedFanoutAndJoins(t *testing.T) {
	scope := estateTestSearch()
	calls := 0
	client := estateHTTPClient()
	client.Transport = estateRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v1/rbac/my-permissions" {
			return estateResponse(map[string]any{"data": map[string]any{"subject": map[string]any{"self": true}, "superuser": true}}), nil
		}
		calls++
		id := scope.Fanout[1]
		if calls > 1 {
			id = "00000000-0000-4000-8000-000000000099"
		}
		return estateResponse(map[string]any{"data": []any{map[string]any{"id": scope.Fanout[0], "status": "active"}, map[string]any{"id": id, "status": "active"}}, "pagination": map[string]any{"total": 2, "has_more": false}}), nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := verifyEstateSearch(ctx, client, "https://api.test", "", scope); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		monitorEstateSearch(ctx, time.Millisecond, func(ctx context.Context) bool {
			if verifyEstateSearch(ctx, client, "https://api.test", "", scope) != nil {
				cancel()
				return false
			}
			return true
		})
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scope monitor did not stop on changed fanout")
	}
	if ctx.Err() == nil || calls != 2 {
		t.Fatal("scope drift did not cancel phase")
	}
}
