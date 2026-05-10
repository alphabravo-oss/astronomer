package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
)

// TestFlattenCISReport feeds a representative ClusterScanReport (matching the
// shape cis-operator produces) through the parser and asserts the row gets
// the right counts + flattened findings, with correct field mapping for
// id, severity, status, description, and remediation.
func TestFlattenCISReport(t *testing.T) {
	t.Parallel()

	// reportJSON mirrors the structure cis-operator emits in
	// `spec.reportJSON` — a JSON-encoded string holding totals + a
	// `results[]` array of sections, each with an inner `checks[]`.
	innerReport := map[string]any{
		"total": 4,
		"pass":  2,
		"fail":  1,
		"warn":  1,
		"skip":  0,
		"results": []any{
			map[string]any{
				"section": "1.1",
				"checks": []any{
					map[string]any{
						"id":              "1.1.1",
						"test_desc":       "Ensure that the API server pod specification file permissions are set to 644",
						"state":           "pass",
						"scored_severity": "high",
						"remediation":     "chmod 644 /etc/kubernetes/manifests/kube-apiserver.yaml",
					},
					map[string]any{
						"id":              "1.1.2",
						"test_desc":       "Ensure that the API server pod specification file ownership is set to root:root",
						"state":           "fail",
						"scored_severity": "high",
						"remediation":     "chown root:root /etc/kubernetes/manifests/kube-apiserver.yaml",
					},
				},
			},
			map[string]any{
				"section": "1.2",
				"checks": []any{
					map[string]any{
						"id":              "1.2.1",
						"test_desc":       "Ensure that the --anonymous-auth argument is set to false",
						"state":           "warn",
						"scored_severity": "medium",
						"remediation":     "Set --anonymous-auth=false in the API server config.",
					},
					map[string]any{
						"id":              "1.2.2",
						"test_desc":       "Ensure that the --basic-auth-file argument is not set",
						"state":           "pass",
						"scored_severity": "medium",
						"remediation":     "Remove --basic-auth-file from the API server config.",
					},
				},
			},
		},
	}
	innerRaw, err := json.Marshal(innerReport)
	if err != nil {
		t.Fatalf("marshal inner report: %v", err)
	}
	report := map[string]any{
		"apiVersion": "cis.cattle.io/v1",
		"kind":       "ClusterScanReport",
		"metadata":   map[string]any{"name": "astronomer-cis-1"},
		"spec": map[string]any{
			"scanProfileName": "rke2-cis-1.8-permissive",
			"reportJSON":      string(innerRaw),
		},
	}

	counts, findings, _, _ := FlattenCISReport(report)

	if counts.Total != 4 {
		t.Errorf("Total = %d, want 4", counts.Total)
	}
	if counts.Pass != 2 {
		t.Errorf("Pass = %d, want 2", counts.Pass)
	}
	if counts.Fail != 1 {
		t.Errorf("Fail = %d, want 1", counts.Fail)
	}
	if counts.Warn != 1 {
		t.Errorf("Warn = %d, want 1", counts.Warn)
	}
	if counts.Skip != 0 {
		t.Errorf("Skip = %d, want 0", counts.Skip)
	}

	if got := len(findings); got != 4 {
		t.Fatalf("len(findings) = %d, want 4", got)
	}

	wantFirst := CISFinding{
		TestID:      "1.1.1",
		Severity:    "high",
		Status:      "pass",
		Description: "Ensure that the API server pod specification file permissions are set to 644",
		Remediation: "chmod 644 /etc/kubernetes/manifests/kube-apiserver.yaml",
	}
	if findings[0] != wantFirst {
		t.Errorf("findings[0] = %+v, want %+v", findings[0], wantFirst)
	}

	// The fail finding must be present and faithfully mapped — this is the
	// row a UI badge would highlight.
	var foundFail bool
	for _, f := range findings {
		if f.TestID == "1.1.2" {
			foundFail = true
			if f.Status != "fail" {
				t.Errorf("1.1.2 status = %q, want fail", f.Status)
			}
			if f.Severity != "high" {
				t.Errorf("1.1.2 severity = %q, want high", f.Severity)
			}
			if f.Remediation == "" {
				t.Errorf("1.1.2 remediation should not be empty")
			}
		}
	}
	if !foundFail {
		t.Error("expected finding for test_id 1.1.2")
	}
}

// TestFlattenCISReport_AlternateShape exercises the fallback paths: a report
// that uses the inlined `spec.report` object (instead of `spec.reportJSON`
// string) and uses `tests[]` / `results[]` instead of `checks[]` for the
// inner record list.
func TestFlattenCISReport_AlternateShape(t *testing.T) {
	t.Parallel()

	report := map[string]any{
		"spec": map[string]any{
			"scanProfileName": "cis-1.8",
			"report": map[string]any{
				"tests": []any{
					map[string]any{
						"section": "5.1",
						"results": []any{
							map[string]any{
								"number":      "5.1.1",
								"description": "Ensure that the cluster-admin role is only used where required",
								"status":      "fail",
								"severity":    "low",
								"remediation": "Review users with cluster-admin role binding.",
							},
						},
					},
				},
			},
		},
	}

	counts, findings, summaryRaw, _ := FlattenCISReport(report)

	if len(findings) != 1 {
		t.Fatalf("len(findings) = %d, want 1", len(findings))
	}
	// When totals aren't given upstream the parser falls back to len(findings).
	if counts.Total != 1 {
		t.Errorf("Total fallback = %d, want 1", counts.Total)
	}
	if findings[0].TestID != "5.1.1" {
		t.Errorf("TestID = %q, want 5.1.1", findings[0].TestID)
	}
	if findings[0].Status != "fail" {
		t.Errorf("Status = %q, want fail", findings[0].Status)
	}
	if findings[0].Description != "Ensure that the cluster-admin role is only used where required" {
		t.Errorf("Description = %q", findings[0].Description)
	}

	// Summary must include the totals so the UI can render aggregates.
	var summary map[string]any
	if err := json.Unmarshal(summaryRaw, &summary); err != nil {
		t.Fatalf("unmarshal summary: %v", err)
	}
	if _, ok := summary["total"]; !ok {
		t.Errorf("summary missing total field: %v", summary)
	}
}

// TestFlattenCISReport_Empty ensures we return a deterministic empty payload
// when the input has no recognizable shape — important because we still
// want to mark the scan completed (with zero findings) rather than crash.
func TestFlattenCISReport_Empty(t *testing.T) {
	t.Parallel()

	counts, findings, summaryRaw, resultsRaw := FlattenCISReport(map[string]any{})
	if counts.Total != 0 || counts.Pass != 0 || counts.Fail != 0 {
		t.Errorf("expected zero counts, got %+v", counts)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings, got %d", len(findings))
	}
	if len(summaryRaw) == 0 {
		t.Error("summary must be non-empty JSON")
	}
	if len(resultsRaw) == 0 {
		t.Error("results must be non-empty JSON")
	}
}

// TestDefaultCISProfileForDistribution pins the distribution → profile
// mapping. This lives behind the "default profile" UX so a user who
// triggers a scan without specifying a profile gets the right bench.
func TestDefaultCISProfileForDistribution(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"":        "cis-1.8-profile",
		"rke":     "rke-profile-permissive-1.8",
		"rke1":    "rke-profile-permissive-1.8",
		"rke2":    "rke2-cis-1.8-profile-permissive",
		"k3s":     "k3s-cis-1.8-profile-permissive",
		"eks":     "eks-profile-1.5.0",
		"aks":     "aks-profile",
		"gke":     "gke-profile-1.6.0",
		"vanilla": "cis-1.8-profile",
		"  RKE2 ": "rke2-cis-1.8-profile-permissive", // case + whitespace tolerance
	}
	for input, want := range cases {
		got := defaultCISProfileForDistribution(input)
		if got != want {
			t.Errorf("distribution=%q: got %q, want %q", input, got, want)
		}
	}
}

type cisTestRequester struct {
	lastPath string
	resp     *protocol.K8sResponsePayload
	resps    map[string]*protocol.K8sResponsePayload
}

func (r *cisTestRequester) Do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (*protocol.K8sResponsePayload, error) {
	r.lastPath = method + " " + path
	if r.resps != nil {
		if resp, ok := r.resps[r.lastPath]; ok {
			return resp, nil
		}
	}
	if r.resp != nil {
		return r.resp, nil
	}
	return &protocol.K8sResponsePayload{StatusCode: http.StatusNotFound}, nil
}

func TestResolveClusterScanProfileNameAcceptsBenchmarkVersion(t *testing.T) {
	t.Parallel()

	list := map[string]any{
		"items": []map[string]any{
			{
				"metadata": map[string]any{"name": "k3s-cis-1.8-profile-permissive"},
				"spec":     map[string]any{"benchmarkVersion": "k3s-cis-1.8-permissive"},
			},
		},
	}
	raw, err := json.Marshal(list)
	if err != nil {
		t.Fatalf("marshal list: %v", err)
	}
	req := &cisTestRequester{
		resp: &protocol.K8sResponsePayload{
			StatusCode: http.StatusOK,
			Body:       base64.StdEncoding.EncodeToString(raw),
		},
	}
	h := &SecurityHandler{k8s: req}

	got := h.resolveClusterScanProfileName(context.Background(), uuid.New(), "k3s", "k3s-cis-1.8-permissive")
	if got != "k3s-cis-1.8-profile-permissive" {
		t.Fatalf("resolveClusterScanProfileName() = %q", got)
	}
	if req.lastPath != "GET /apis/cis.cattle.io/v1/clusterscanprofiles" {
		t.Fatalf("unexpected path %q", req.lastPath)
	}
}

func TestResolveClusterScanProfileNameRecommendsHighestLiveProfile(t *testing.T) {
	t.Parallel()

	list := map[string]any{
		"items": []map[string]any{
			{
				"metadata": map[string]any{"name": "k3s-cis-1.8-profile-permissive"},
				"spec":     map[string]any{"benchmarkVersion": "k3s-cis-1.8-permissive"},
			},
			{
				"metadata": map[string]any{"name": "k3s-cis-1.10-profile"},
				"spec":     map[string]any{"benchmarkVersion": "k3s-cis-1.10"},
			},
			{
				"metadata": map[string]any{"name": "k3s-cis-1.11-profile"},
				"spec":     map[string]any{"benchmarkVersion": "k3s-cis-1.11"},
			},
		},
	}
	raw, err := json.Marshal(list)
	if err != nil {
		t.Fatalf("marshal list: %v", err)
	}
	req := &cisTestRequester{
		resp: &protocol.K8sResponsePayload{
			StatusCode: http.StatusOK,
			Body:       base64.StdEncoding.EncodeToString(raw),
		},
	}
	h := &SecurityHandler{k8s: req}

	got := h.resolveClusterScanProfileName(context.Background(), uuid.New(), "k3s", "")
	if got != "k3s-cis-1.11-profile" {
		t.Fatalf("resolveClusterScanProfileName() = %q", got)
	}
	if req.lastPath != "GET /apis/cis.cattle.io/v1/clusterscanprofiles" {
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
	req := &cisTestRequester{
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
	h := &SecurityHandler{k8s: req}

	got, found, err := h.fetchClusterScanReport(context.Background(), uuid.New(), "demo")
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
