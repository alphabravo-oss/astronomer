package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type listEnvelope struct {
	Data       []map[string]any `json:"data"`
	Pagination paging.Metadata  `json:"pagination"`
}

// TestPageWindow covers the slicing helper the cluster resource list endpoints
// use: it must return the requested window, report the full total, and only
// advertise a next page when the window is actually truncated.
func TestPageWindow(t *testing.T) {
	items := make([]int, 25)
	for i := range items {
		items[i] = i
	}

	req := httptest.NewRequest(http.MethodGet, "/?limit=20&offset=0", nil)
	page, pg := pageWindow(req, items)
	if len(page) != 20 {
		t.Fatalf("page 1 len = %d, want 20", len(page))
	}
	if pg.Total == nil || *pg.Total != 25 {
		t.Fatalf("total = %v, want 25", pg.Total)
	}
	if !pg.HasMore || pg.NextOffset == nil || *pg.NextOffset != 20 {
		t.Fatalf("page 1 should advertise next_offset=20, got has_more=%v next=%v", pg.HasMore, pg.NextOffset)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/?limit=20&offset=20", nil)
	page2, pg2 := pageWindow(req2, items)
	if len(page2) != 5 {
		t.Fatalf("page 2 len = %d, want 5", len(page2))
	}
	if page2[0] != 20 {
		t.Fatalf("page 2 first item = %d, want 20 (duplicate page bug)", page2[0])
	}
	if pg2.HasMore || pg2.NextOffset != nil {
		t.Fatalf("page 2 is the last page, should not advertise next: has_more=%v next=%v", pg2.HasMore, pg2.NextOffset)
	}

	// Offset past the end yields an empty (not full) page.
	req3 := httptest.NewRequest(http.MethodGet, "/?limit=20&offset=100", nil)
	page3, pg3 := pageWindow(req3, items)
	if len(page3) != 0 || pg3.HasMore {
		t.Fatalf("out-of-range offset should return empty last page, got len=%d has_more=%v", len(page3), pg3.HasMore)
	}
}

func TestSortWorkloadItemsUsesStableIdentityTieBreaker(t *testing.T) {
	items := []map[string]any{
		{"namespace": "team-b", "kind": "Deployment", "name": "same", "createdAt": "2026-01-01T00:00:00Z"},
		{"namespace": "team-a", "kind": "StatefulSet", "name": "same", "createdAt": "2026-01-02T00:00:00Z"},
		{"namespace": "team-a", "kind": "Deployment", "name": "alpha", "createdAt": "2026-01-03T00:00:00Z"},
	}
	sortWorkloadItems(items, "name_desc")
	got := []string{
		items[0]["namespace"].(string) + "/" + items[0]["kind"].(string) + "/" + items[0]["name"].(string),
		items[1]["namespace"].(string) + "/" + items[1]["kind"].(string) + "/" + items[1]["name"].(string),
		items[2]["namespace"].(string) + "/" + items[2]["kind"].(string) + "/" + items[2]["name"].(string),
	}
	want := []string{"team-a/StatefulSet/same", "team-b/Deployment/same", "team-a/Deployment/alpha"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("sorted identities = %v, want %v", got, want)
	}
	if validWorkloadSort("partial-page-local") {
		t.Fatal("unsupported sort must fail closed")
	}
}

// TestListNodes_HonoursLimitOffset drives ListNodes end-to-end against a stub
// agent returning 25 nodes. Before the fix the handler returned all 25 while
// advertising limit=20, and "Next" (offset=20) re-fetched the identical full
// set. After the fix each page carries only its slice and the totals line up.
func TestListNodes_HonoursLimitOffset(t *testing.T) {
	nodes := make([]map[string]any, 25)
	for i := range nodes {
		nodes[i] = map[string]any{"metadata": map[string]any{"name": fmt.Sprintf("node-%02d", i)}}
	}
	nodeBody, _ := json.Marshal(map[string]any{"items": nodes})
	podBody, _ := json.Marshal(map[string]any{"items": []any{}})

	stub := &stubK8sRequester{respFn: func(req stubReq) (*protocol.K8sResponsePayload, error) {
		body := podBody
		if strings.HasPrefix(req.Path, "/api/v1/nodes") {
			body = nodeBody
		}
		return &protocol.K8sResponsePayload{
			StatusCode: http.StatusOK,
			Body:       base64.StdEncoding.EncodeToString(body),
		}, nil
	}}
	h := NewWorkloadHandlerWithRequester(stub)

	first := doListNodes(t, h, "?limit=20&offset=0")
	if len(first.Data) != 20 {
		t.Fatalf("page 1 returned %d nodes, want 20 (limit ignored)", len(first.Data))
	}
	if first.Pagination.Total == nil || *first.Pagination.Total != 25 {
		t.Fatalf("page 1 total = %v, want 25", first.Pagination.Total)
	}
	if !first.Pagination.HasMore || first.Pagination.NextOffset == nil || *first.Pagination.NextOffset != 20 {
		t.Fatalf("page 1 should advertise next_offset=20, got %+v", first.Pagination)
	}
	if name := first.Data[0]["name"]; name != "node-00" {
		t.Fatalf("page 1 first node = %v, want node-00", name)
	}

	second := doListNodes(t, h, "?limit=20&offset=20")
	if len(second.Data) != 5 {
		t.Fatalf("page 2 returned %d nodes, want 5 (duplicate full page)", len(second.Data))
	}
	if name := second.Data[0]["name"]; name != "node-20" {
		t.Fatalf("page 2 first node = %v, want node-20 (Next re-served page 1)", name)
	}
	if second.Pagination.HasMore || second.Pagination.NextOffset != nil {
		t.Fatalf("page 2 is last, should not advertise next: %+v", second.Pagination)
	}
}

func TestListEventsClampsUpstreamLimit(t *testing.T) {
	eventBody, err := json.Marshal(map[string]any{"items": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		query     string
		wantLimit string
	}{
		{name: "default", wantLimit: "100"},
		{name: "negative", query: "?limit=-1", wantLimit: "100"},
		{name: "overflow", query: "?limit=999999999", wantLimit: "500"},
		{name: "bounded", query: "?limit=25", wantLimit: "25"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clusterID := uuid.NewString()
			stub := &stubK8sRequester{respFn: func(req stubReq) (*protocol.K8sResponsePayload, error) {
				if req.Path != "/api/v1/events?limit="+tt.wantLimit {
					t.Fatalf("upstream path = %q, want bounded limit %s", req.Path, tt.wantLimit)
				}
				return &protocol.K8sResponsePayload{
					StatusCode: http.StatusOK,
					Body:       base64.StdEncoding.EncodeToString(eventBody),
				}, nil
			}}
			h := NewWorkloadHandlerWithRequester(stub)
			rc := chi.NewRouteContext()
			rc.URLParams.Add("cluster_id", clusterID)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID+"/events/"+tt.query, nil)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
			rec := httptest.NewRecorder()

			h.ListEvents(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func doListNodes(t *testing.T, h *WorkloadHandler, query string) listEnvelope {
	t.Helper()
	clusterID := uuid.NewString()
	rc := chi.NewRouteContext()
	rc.URLParams.Add("cluster_id", clusterID)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID+"/nodes/"+query, nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
	rec := httptest.NewRecorder()
	h.ListNodes(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var env listEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode list envelope: %v", err)
	}
	return env
}
