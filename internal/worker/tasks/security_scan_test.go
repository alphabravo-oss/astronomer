package tasks

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type securityFetchRequester struct {
	lastPath string
	paths    []string
	resps    map[string]*protocol.K8sResponsePayload
}

func (r *securityFetchRequester) Do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (*protocol.K8sResponsePayload, error) {
	r.lastPath = method + " " + path
	r.paths = append(r.paths, r.lastPath)
	if r.resps != nil {
		if resp, ok := r.resps[r.lastPath]; ok {
			return resp, nil
		}
	}
	return nil, nil
}

func TestFetchClusterScanReportUsesResolvedReportName(t *testing.T) {
	t.Parallel()

	scan := map[string]any{
		"apiVersion": "cis.cattle.io/v1",
		"kind":       "ClusterScan",
		"metadata":   map[string]any{"name": "demo"},
		"status":     map[string]any{"reportName": "scan-report-demo-abc123"},
	}
	report := map[string]any{
		"apiVersion": "cis.cattle.io/v1",
		"kind":       "ClusterScanReport",
		"metadata":   map[string]any{"name": "scan-report-demo-abc123"},
	}
	scanRaw, err := json.Marshal(scan)
	if err != nil {
		t.Fatalf("marshal scan: %v", err)
	}
	reportRaw, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	req := &securityFetchRequester{
		resps: map[string]*protocol.K8sResponsePayload{
			"GET /apis/cis.cattle.io/v1/clusterscans/demo": {
				StatusCode: http.StatusOK,
				Body:       base64.StdEncoding.EncodeToString(scanRaw),
			},
			"GET /apis/cis.cattle.io/v1/clusterscanreports/scan-report-demo-abc123": {
				StatusCode: http.StatusOK,
				Body:       base64.StdEncoding.EncodeToString(reportRaw),
			},
		},
	}

	got, found, err := fetchClusterScanReport(context.Background(), req, "cluster-1", "demo")
	if err != nil {
		t.Fatalf("fetchClusterScanReport() error = %v", err)
	}
	if !found {
		t.Fatal("expected report to be found")
	}
	if got["kind"] != "ClusterScanReport" {
		t.Fatalf("kind = %v", got["kind"])
	}
	if req.lastPath != "GET /apis/cis.cattle.io/v1/clusterscanreports/scan-report-demo-abc123" {
		t.Fatalf("unexpected path %q", req.lastPath)
	}
}

func TestFetchClusterScanReportFallsBackToOwnerMatchedList(t *testing.T) {
	t.Parallel()

	list := map[string]any{
		"items": []map[string]any{
			{
				"metadata": map[string]any{
					"name": "scan-report-demo-xyz789",
					"ownerReferences": []map[string]any{
						{"name": "demo"},
					},
				},
			},
		},
	}
	report := map[string]any{
		"apiVersion": "cis.cattle.io/v1",
		"kind":       "ClusterScanReport",
		"metadata":   map[string]any{"name": "scan-report-demo-xyz789"},
	}
	listRaw, err := json.Marshal(list)
	if err != nil {
		t.Fatalf("marshal list: %v", err)
	}
	reportRaw, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	req := &securityFetchRequester{
		resps: map[string]*protocol.K8sResponsePayload{
			"GET /apis/cis.cattle.io/v1/clusterscans/demo": {
				StatusCode: http.StatusOK,
				Body:       base64.StdEncoding.EncodeToString([]byte(`{"status":{}}`)),
			},
			"GET /apis/cis.cattle.io/v1/clusterscanreports": {
				StatusCode: http.StatusOK,
				Body:       base64.StdEncoding.EncodeToString(listRaw),
			},
			"GET /apis/cis.cattle.io/v1/clusterscanreports/scan-report-demo-xyz789": {
				StatusCode: http.StatusOK,
				Body:       base64.StdEncoding.EncodeToString(reportRaw),
			},
		},
	}

	got, found, err := fetchClusterScanReport(context.Background(), req, "cluster-1", "demo")
	if err != nil {
		t.Fatalf("fetchClusterScanReport() error = %v", err)
	}
	if !found {
		t.Fatal("expected report to be found")
	}
	if got["kind"] != "ClusterScanReport" {
		t.Fatalf("kind = %v", got["kind"])
	}
	if req.lastPath != "GET /apis/cis.cattle.io/v1/clusterscanreports/scan-report-demo-xyz789" {
		t.Fatalf("unexpected path %q", req.lastPath)
	}
}
