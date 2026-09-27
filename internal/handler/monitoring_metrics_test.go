package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
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
	clusterID := uuid.NewString()

	rc := chi.NewRouteContext()
	rc.URLParams.Add("cluster_id", clusterID)
	rc.URLParams.Add("node", "node-a")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/monitoring/metrics/node/"+clusterID+"/node-a/", nil)
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

func TestRealNodeSummaryResolvesNodeExporterInstanceByNodeName(t *testing.T) {
	clusterID := uuid.NewString()
	q := standaloneMonitoringQueries{row: sqlc.GetClusterMonitoringContextRow{
		ClusterID:             uuid.MustParse(clusterID),
		StackNamespace:        "monitoring",
		PrometheusReleaseName: "prometheus",
		LastAppliedSpecHash:   "installed",
		Status:                "healthy",
		ThanosSidecarEnabled:  false,
	}}
	var mu sync.Mutex
	queries := []string{}
	stub := &stubK8sRequester{respFn: func(req stubReq) (*protocol.K8sResponsePayload, error) {
		body := `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"7"]}]}}`
		if strings.Contains(req.Path, "services?labelSelector") {
			body = `{"items":[{"metadata":{"name":"prometheus-kube-prometheus-prometheus"},"spec":{"ports":[{"port":9090}]}}]}`
		} else if parsed, err := url.Parse(req.Path); err == nil {
			mu.Lock()
			queries = append(queries, parsed.Query().Get("query"))
			mu.Unlock()
		}
		return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64.StdEncoding.EncodeToString([]byte(body))}, nil
	}}
	h := NewMonitoringHandlerWithQueries(q, stub)
	summary, ok, err := h.realNodeSummary(context.Background(), clusterID, "node-a")
	if err != nil || !ok {
		t.Fatalf("realNodeSummary ok=%v err=%v", ok, err)
	}
	if summaryFloat(summary["cpuUsage"]) == 0 || summaryFloat(summary["memoryCapacity"]) == 0 {
		t.Fatalf("expected non-zero node metrics, got %#v", summary)
	}
	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(queries, "\n")
	if strings.Contains(joined, `instance="node-a"`) {
		t.Fatalf("queries still assume the node name is the scrape instance: %s", joined)
	}
	if !strings.Contains(joined, `node_uname_info{nodename="node-a"`) {
		t.Fatalf("node-exporter queries do not resolve the node through node_uname_info: %s", joined)
	}
	if !strings.Contains(joined, `mountpoint=~"^/$|^/etc/hostname$"`) ||
		!strings.Contains(joined, `max by(instance) (node_filesystem_size_bytes`) {
		t.Fatalf("disk queries do not support containerized node root filesystems: %s", joined)
	}
}

func TestResolveLegacyWorkloadKind(t *testing.T) {
	stub := &stubK8sRequester{respFn: func(req stubReq) (*protocol.K8sResponsePayload, error) {
		body := `{"items":[],"metadata":{}}`
		if strings.Contains(req.Path, "/deployments?") {
			body = `{"items":[{"metadata":{"name":"web","namespace":"team-a"},"spec":{"selector":{"matchLabels":{"app":"web"}},"template":{"spec":{"containers":[]}}}}],"metadata":{}}`
		}
		return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64.StdEncoding.EncodeToString([]byte(body))}, nil
	}}
	h := NewMonitoringHandlerWithRequester(stub)
	kind, err := h.resolveLegacyWorkloadKind(context.Background(), uuid.NewString(), "team-a", "web")
	if err != nil {
		t.Fatalf("resolveLegacyWorkloadKind: %v", err)
	}
	if kind != "Deployment" {
		t.Fatalf("kind = %q, want Deployment", kind)
	}
}
