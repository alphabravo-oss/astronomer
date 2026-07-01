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

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type listEnvelope struct {
	Data       []map[string]any `json:"data"`
	Pagination Pagination       `json:"pagination"`
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
	if pg.Total != 25 {
		t.Fatalf("total = %d, want 25", pg.Total)
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
	if first.Pagination.Total != 25 {
		t.Fatalf("page 1 total = %d, want 25", first.Pagination.Total)
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

func doListNodes(t *testing.T, h *WorkloadHandler, query string) listEnvelope {
	t.Helper()
	rc := chi.NewRouteContext()
	rc.URLParams.Add("cluster_id", "c1")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/c1/nodes/"+query, nil)
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
