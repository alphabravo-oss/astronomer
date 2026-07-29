package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// TestLegacyNodeMetrics_NotStub verifies the node-metrics endpoint no longer
// hard-codes a 501. With no Prometheus backend configured it now falls back to
// the node's advertised capacity (fetched over the tunnel) and returns a live
// gauge summary, so the node detail page can render real CPU/memory capacity
// instead of an unconditional "not implemented".
func TestLegacyNodeMetrics_NotStub(t *testing.T) {
	nodeBody, _ := json.Marshal(map[string]any{
		"metadata": map[string]any{"name": "node-a"},
		"status": map[string]any{
			"capacity": map[string]string{"cpu": "4", "memory": "8Gi", "pods": "110"},
		},
	})
	empty, _ := json.Marshal(map[string]any{"items": []any{}})

	stub := &stubK8sRequester{respFn: func(req stubReq) (*protocol.K8sResponsePayload, error) {
		body := empty
		if strings.HasPrefix(req.Path, "/api/v1/nodes/") {
			body = nodeBody
		}
		return &protocol.K8sResponsePayload{
			StatusCode: http.StatusOK,
			Body:       base64.StdEncoding.EncodeToString(body),
		}, nil
	}}
	h := NewMonitoringHandlerWithRequester(stub)

	rc := chi.NewRouteContext()
	rc.URLParams.Add("cluster_id", "c1")
	rc.URLParams.Add("node", "node-a")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/monitoring/metrics/node/c1/node-a/", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
	rec := httptest.NewRecorder()

	h.LegacyNodeMetrics(rec, req)

	if rec.Code == http.StatusNotImplemented {
		t.Fatalf("node metrics endpoint still returns 501")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	// The legacy metrics endpoints wrap their Prometheus-style {status,data}
	// body inside the platform's own {data:...} envelope (RespondJSON), exactly
	// like the 6 sibling LegacyNode*/cluster-summary endpoints. So the real
	// shape is {"data":{"status":"success","data":{...}}}.
	var resp struct {
		Data struct {
			Status string         `json:"status"`
			Data   map[string]any `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data.Status != "success" {
		t.Fatalf("status field = %q, want success", resp.Data.Status)
	}
	// parseCPU("4") == 4000 millicores; the fallback must surface real capacity.
	if cpu, _ := resp.Data.Data["cpuCapacity"].(float64); cpu != 4000 {
		t.Fatalf("cpuCapacity = %v, want 4000 (capacity not populated)", resp.Data.Data["cpuCapacity"])
	}
	if mem, _ := resp.Data.Data["memoryCapacity"].(float64); mem != 8*1024*1024*1024 {
		t.Fatalf("memoryCapacity = %v, want %d", resp.Data.Data["memoryCapacity"], 8*1024*1024*1024)
	}
}
